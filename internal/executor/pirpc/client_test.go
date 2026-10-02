package pirpc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type peer struct {
	reader *bufio.Reader
	writer *io.PipeWriter
	client *Client
}

func connect(t *testing.T) *peer {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	c, err := New(inW, outR)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close(); _ = inR.Close(); _ = outW.Close() })
	return &peer{bufio.NewReader(inR), outW, c}
}

func (p *peer) command() (map[string]any, error) {
	b, err := p.reader.ReadBytes('\n')
	if err != nil {
		return nil, err
	}
	var v map[string]any
	err = json.Unmarshal(b, &v)
	return v, err
}

func (p *peer) respond(cmd map[string]any, data any) error {
	b, err := json.Marshal(map[string]any{"id": cmd["id"], "type": "response", "command": cmd["type"], "success": true, "data": data})
	if err != nil {
		return err
	}
	_, err = p.writer.Write(append(b, '\n'))
	return err
}

func timeout(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func kind(t *testing.T, err error) Failure {
	t.Helper()
	var e *Error
	if !errors.As(err, &e) {
		t.Fatalf("expected safe RPC error, got %T", err)
	}
	return e.Kind
}

func TestCommandsAndUnicodeFraming(t *testing.T) {
	p := connect(t)
	ctx := timeout(t)
	var seen []string
	serverDone := make(chan error, 1)
	go func() {
		for _, expect := range []string{"prompt", "steer", "follow_up"} {
			cmd, err := p.command()
			if err != nil {
				serverDone <- err
				return
			}
			if cmd["type"] != expect || cmd["message"] != "one\u2028two\u2029three\nline" {
				serverDone <- fmt.Errorf("command framing mismatch")
				return
			}
			seen = append(seen, cmd["id"].(string))
			disposition := "queued"
			if expect == "prompt" {
				disposition = "started"
			}
			if err := p.respond(cmd, map[string]any{"disposition": disposition}); err != nil {
				serverDone <- err
				return
			}
		}
		serverDone <- nil
	}()
	for _, command := range []func(context.Context, string) (Receipt, error){p.client.Prompt, p.client.Steer, p.client.FollowUp} {
		rc, err := command(ctx, "one\u2028two\u2029three\nline")
		if err != nil || !rc.Accepted || rc.ID == "" {
			t.Fatalf("receipt=%+v err=%v", rc, err)
		}
	}
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
	if seen[0] == seen[1] || seen[1] == seen[2] {
		t.Fatal("request IDs reused")
	}
}

func TestOutOfOrderReplies(t *testing.T) {
	p := connect(t)
	ctx := timeout(t)
	serverDone := make(chan error, 1)
	go func() {
		first, err := p.command()
		if err != nil {
			serverDone <- err
			return
		}
		second, err := p.command()
		if err != nil {
			serverDone <- err
			return
		}
		for _, cmd := range []map[string]any{second, first} {
			if err := p.respond(cmd, map[string]any{"disposition": "queued"}); err != nil {
				serverDone <- err
				return
			}
		}
		serverDone <- nil
	}()
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, method := range []func(context.Context, string) (Receipt, error){p.client.Steer, p.client.FollowUp} {
		wg.Add(1)
		go func(f func(context.Context, string) (Receipt, error)) {
			defer wg.Done()
			_, err := f(ctx, "bounded instruction")
			errs <- err
		}(method)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
}

func TestAcceptedIsNotSettledAndEventsAreRedacted(t *testing.T) {
	p := connect(t)
	ctx := timeout(t)
	serverDone := make(chan error, 1)
	const diagnostic = "credential-value-that-must-stay-private"
	go func() {
		cmd, err := p.command()
		if err != nil {
			serverDone <- err
			return
		}
		if err := p.respond(cmd, map[string]any{"disposition": "started"}); err != nil {
			serverDone <- err
			return
		}
		for _, line := range []string{
			`{"type":"tool_execution_start","toolName":"` + diagnostic + `","args":{"command":"` + diagnostic + `"}}`,
			`{"type":"agent_end","text":"` + diagnostic + `"}`,
			`{"type":"message_end","message":{"role":"assistant","text":"` + diagnostic + `","usage":{"input":12,"output":3,"cacheRead":0,"cacheWrite":0,"cost":{"total":0}}}}`,
			`{"type":"agent_settled"}`,
		} {
			// CRLF is accepted; records can be fragmented across arbitrary writes.
			b := []byte(line + "\r\n")
			mid := len(b) / 2
			if _, err := p.writer.Write(b[:mid]); err != nil {
				serverDone <- err
				return
			}
			if _, err := p.writer.Write(b[mid:]); err != nil {
				serverDone <- err
				return
			}
		}
		serverDone <- nil
	}()
	rc, err := p.client.Prompt(ctx, "task")
	if err != nil || !rc.Accepted {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		select {
		case ev := <-p.client.Events():
			b, _ := json.Marshal(ev)
			if strings.Contains(string(b), diagnostic) {
				t.Fatal("private payload leaked")
			}
			if ev.Settled != (i == 3) {
				t.Fatalf("wrong completion boundary: %+v", ev)
			}
			if i == 0 && ev.Tool != "custom" {
				t.Fatal("untrusted tool name not reduced")
			}
			if i == 2 && (ev.Usage == nil || !ev.Usage.Final || ev.Usage.Input == nil || *ev.Usage.Input != 12 || ev.Usage.CostEstimateUSD != nil) {
				t.Fatalf("unknown fee was replaced or usage lost: %+v", ev.Usage)
			}
		case <-ctx.Done():
			t.Fatal("missing event")
		}
	}
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
}

func TestCancellationAndLateResponseDoNotReplay(t *testing.T) {
	p := connect(t)
	ctx := timeout(t)
	canceled, cancel := context.WithCancel(ctx)
	commandSeen := make(chan map[string]any, 1)
	go func() { cmd, _ := p.command(); commandSeen <- cmd }()
	result := make(chan error, 1)
	go func() { _, err := p.client.Prompt(canceled, "task"); result <- err }()
	cmd := <-commandSeen
	cancel()
	if err := <-result; kind(t, err) != Uncertain || !errors.Is(err, context.Canceled) {
		t.Fatalf("wrong delivery result: %v", err)
	}
	if err := p.respond(cmd, map[string]any{"disposition": "started"}); err != nil {
		t.Fatal(err)
	}
	serverDone := make(chan error, 1)
	go func() {
		next, err := p.command()
		if err != nil {
			serverDone <- err
			return
		}
		if next["type"] != "get_state" {
			serverDone <- fmt.Errorf("canceled prompt replayed")
			return
		}
		serverDone <- p.respond(next, idleState())
	}()
	if _, err := p.client.State(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
	before, cancelBefore := context.WithCancel(ctx)
	cancelBefore()
	if _, err := p.client.FollowUp(before, "task"); kind(t, err) != NotSent {
		t.Fatal("canceled command sent")
	}
}

func idleState() map[string]any {
	return map[string]any{"sessionId": "session-1", "isStreaming": false, "isCompacting": false, "pendingMessageCount": 0}
}

func TestStopClearsQueueBeforeAbortAndGatesInput(t *testing.T) {
	p := connect(t)
	ctx := timeout(t)
	serverDone := make(chan error, 1)
	go func() {
		for _, expect := range []string{"clear_queue", "abort", "get_state"} {
			cmd, err := p.command()
			if err != nil {
				serverDone <- err
				return
			}
			if cmd["type"] != expect {
				serverDone <- fmt.Errorf("wrong stop order")
				return
			}
			var data any
			if expect == "clear_queue" {
				data = map[string]any{"steering": []string{"private text"}, "followUp": []string{"private text"}}
			}
			if expect == "get_state" {
				data = idleState()
			}
			if err := p.respond(cmd, data); err != nil {
				serverDone <- err
				return
			}
		}
		serverDone <- nil
	}()
	rc, err := p.client.Stop(ctx)
	if err != nil || !rc.QueueCleared || !rc.AbortAccepted || !rc.Confirmed {
		t.Fatalf("stop=%+v err=%v", rc, err)
	}
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
	again, err := p.client.Stop(ctx)
	if err != nil || again != rc {
		t.Fatal("confirmed stop not idempotent")
	}
	if _, err := p.client.FollowUp(ctx, "restart accidentally"); kind(t, err) != NotSent {
		t.Fatal("input accepted after stop")
	}
}

func TestStopAcknowledgementWithoutIdleIsUncertain(t *testing.T) {
	p := connect(t)
	ctx := timeout(t)
	serverDone := make(chan error, 1)
	go func() {
		for i := 0; i < 3; i++ {
			cmd, err := p.command()
			if err != nil {
				serverDone <- err
				return
			}
			var data any
			if cmd["type"] == "get_state" {
				state := idleState()
				state["pendingMessageCount"] = 1
				data = state
			}
			if err := p.respond(cmd, data); err != nil {
				serverDone <- err
				return
			}
		}
		serverDone <- nil
	}()
	rc, err := p.client.Stop(ctx)
	if kind(t, err) != Uncertain || !rc.AbortAccepted || rc.Confirmed {
		t.Fatalf("unverified stop=%+v err=%v", rc, err)
	}
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
}

func TestRejectedErrorsAreSafe(t *testing.T) {
	p := connect(t)
	ctx := timeout(t)
	serverDone := make(chan error, 1)
	go func() {
		cmd, err := p.command()
		if err != nil {
			serverDone <- err
			return
		}
		b, _ := json.Marshal(map[string]any{"id": cmd["id"], "type": "response", "command": cmd["type"], "success": false, "error": "secret raw provider diagnostic"})
		_, err = p.writer.Write(append(b, '\n'))
		serverDone <- err
	}()
	_, err := p.client.Steer(ctx, "task")
	if kind(t, err) != Rejected || strings.Contains(err.Error(), "secret") {
		t.Fatalf("unsafe error: %v", err)
	}
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
}

func TestMalformedMismatchedAndTruncatedRecords(t *testing.T) {
	for _, mode := range []string{"json", "wrong-command", "missing-success", "no-newline", "oversize", "invalid-utf8"} {
		t.Run(mode, func(t *testing.T) {
			p := connect(t)
			ctx := timeout(t)
			serverDone := make(chan struct{})
			go func() {
				defer close(serverDone)
				cmd, err := p.command()
				if err != nil {
					return
				}
				line := "bad raw secret json\n"
				switch mode {
				case "wrong-command":
					line = fmt.Sprintf("{\"id\":%q,\"type\":\"response\",\"command\":\"abort\",\"success\":true}\n", cmd["id"])
				case "missing-success":
					line = fmt.Sprintf("{\"id\":%q,\"type\":\"response\",\"command\":\"prompt\"}\n", cmd["id"])
				case "no-newline":
					line = `{"type":"agent_settled"}`
				case "oversize":
					line = strings.Repeat("x", maxRecordBytes+2) + "\n"
				case "invalid-utf8":
					line = "{\"type\":\"" + string([]byte{0xff}) + "\"}\n"
				}
				_, _ = p.writer.Write([]byte(line))
				_ = p.writer.Close()
			}()
			_, err := p.client.Prompt(ctx, "task")
			if kind(t, err) != Uncertain || kind(t, p.client.Err()) != Protocol || strings.Contains(err.Error(), "secret") {
				t.Fatalf("err=%v terminal=%v", err, p.client.Err())
			}
			<-serverDone
		})
	}
}

func TestCloseUnblocksEventBackpressureAndWriter(t *testing.T) {
	t.Run("events", func(t *testing.T) {
		p := connect(t)
		ctx := timeout(t)
		serverDone := make(chan struct{})
		go func() {
			defer close(serverDone)
			for i := 0; i < maxEvents+2; i++ {
				if _, err := io.WriteString(p.writer, "{\"type\":\"agent_start\"}\n"); err != nil {
					return
				}
			}
		}()
		// Let the producer fill the bounded event queue, with no consumer.
		for len(p.client.events) < maxEvents {
			select {
			case <-ctx.Done():
				t.Fatal("event queue did not fill")
			default:
			}
			time.Sleep(time.Millisecond)
		}
		if err := p.client.Close(); err != nil {
			t.Fatal(err)
		}
		select {
		case <-serverDone:
		case <-ctx.Done():
			t.Fatal("producer remained blocked")
		}
	})
	t.Run("writer", func(t *testing.T) {
		p := connect(t)
		ctx := timeout(t)
		result := make(chan error, 1)
		go func() { _, err := p.client.Prompt(ctx, "task"); result <- err }()
		// No peer reads stdin, so a submitted write remains blocked until Close.
		for {
			p.client.mu.Lock()
			writing := false
			for _, req := range p.client.pending {
				writing = writing || req.sent
			}
			p.client.mu.Unlock()
			if writing {
				break
			}
			select {
			case <-ctx.Done():
				t.Fatal("writer never started")
			default:
			}
			time.Sleep(time.Millisecond)
		}
		if err := p.client.Close(); err != nil {
			t.Fatal(err)
		}
		select {
		case err := <-result:
			if kind(t, err) != Uncertain {
				t.Fatal(err)
			}
		case <-ctx.Done():
			t.Fatal("call remained blocked")
		}
	})
}

func TestShutdownDrainsFinalEvents(t *testing.T) {
	p := connect(t)
	ctx := timeout(t)
	serverDone := make(chan error, 1)
	go func() {
		_, err := p.command()
		if !errors.Is(err, io.EOF) {
			serverDone <- fmt.Errorf("stdin did not close cleanly")
			return
		}
		_, err = io.WriteString(p.writer, "{\"type\":\"agent_settled\"}\n")
		_ = p.writer.Close()
		serverDone <- err
	}()
	if err := p.client.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
	var settled bool
	for ev := range p.client.Events() {
		settled = settled || ev.Settled
	}
	if !settled {
		t.Fatal("shutdown discarded final event")
	}
	if _, err := p.client.Prompt(ctx, "new task"); kind(t, err) != NotSent {
		t.Fatal("accepted input during shutdown")
	}
}

func TestShutdownDeadlineIsUncertain(t *testing.T) {
	p := connect(t)
	ctx := timeout(t)
	seenEOF := make(chan struct{})
	go func() { _, _ = p.command(); close(seenEOF) }()
	deadline, cancel := context.WithCancel(ctx)
	result := make(chan error, 1)
	go func() { result <- p.client.Shutdown(deadline) }()
	<-seenEOF
	// The peer deliberately leaves stdout open after EOF; it has not exited.
	cancel()
	if err := <-result; kind(t, err) != Uncertain || !errors.Is(err, context.Canceled) {
		t.Fatalf("shutdown=%v", err)
	}
}

func TestMissingDispositionIsUncertain(t *testing.T) {
	p := connect(t)
	ctx := timeout(t)
	go func() {
		cmd, err := p.command()
		if err == nil {
			_ = p.respond(cmd, map[string]any{})
		}
	}()
	rc, err := p.client.Prompt(ctx, "task")
	if kind(t, err) != Uncertain || rc.Accepted {
		t.Fatal("unsupported receipt interpreted as accepted")
	}
}

func TestInvalidInputIsNotSent(t *testing.T) {
	p := connect(t)
	ctx := timeout(t)
	for _, message := range []string{"", " \n", string([]byte{0xff}), strings.Repeat("x", maxRecordBytes)} {
		if _, err := p.client.Prompt(ctx, message); kind(t, err) != NotSent {
			t.Fatal("invalid input sent")
		}
	}
}

func TestUnknownUsageAndMalformedState(t *testing.T) {
	ev, _ := eventSummary("message_end", []byte(`{"type":"message_end","message":{"role":"assistant","usage":{"input":8,"output":-1,"cacheRead":2,"cost":{"total":0}}}}`))
	if ev.Usage == nil || ev.Usage.Output != nil || ev.Usage.CacheWrite != nil || !ev.Usage.Invalid || ev.Usage.CostEstimateUSD != nil {
		t.Fatalf("unknown usage lost: %+v", ev.Usage)
	}
	for _, data := range []any{map[string]any{"sessionId": "session-1"}, map[string]any{"sessionId": "session-1", "isStreaming": false, "isCompacting": false, "pendingMessageCount": -1}} {
		p := connect(t)
		ctx := timeout(t)
		go func() {
			cmd, err := p.command()
			if err == nil {
				_ = p.respond(cmd, data)
			}
		}()
		if _, err := p.client.State(ctx); kind(t, err) != Protocol {
			t.Fatalf("malformed state accepted: %v", err)
		}
	}
}

// This optional compatibility probe starts Pi without a prompt, model request or
// credentials. It only exercises metadata and idle control over real local pipes.
func TestInstalledPiRPC(t *testing.T) {
	binary := os.Getenv("MEERKAT_TEST_PI_RPC_BINARY")
	if binary == "" {
		t.Skip("set MEERKAT_TEST_PI_RPC_BINARY for the installed-Pi metadata probe")
	}
	root := t.TempDir()
	sessions := filepath.Join(root, "sessions")
	if err := os.Mkdir(sessions, 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "--mode", "rpc", "--offline", "--session-dir", sessions, "--no-tools", "--no-extensions", "--no-skills", "--no-context-files", "--no-prompt-templates", "--no-themes")
	cmd.Dir = root
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME"), "PI_CODING_AGENT_DIR=" + filepath.Join(root, "config"), "PI_TELEMETRY=0", "PI_OFFLINE=1"}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal("installed Pi could not start")
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	c, err := New(stdin, stdout)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	go func() {
		for range c.Events() {
		}
	}()
	state, err := c.State(ctx)
	if err != nil || state.SessionID == "" || state.Streaming || state.PendingMessages != 0 {
		t.Fatalf("metadata compatibility failed: %v", err)
	}
	if state.SessionFile != "" && !strings.HasPrefix(state.SessionFile, sessions+string(filepath.Separator)) {
		t.Fatal("session path escaped private probe directory")
	}
	rc, err := c.Stop(ctx)
	if err != nil || !rc.Confirmed {
		t.Fatalf("idle control compatibility failed: %v", err)
	}
	if err := c.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatal("installed Pi did not shut down cleanly")
	}
}
