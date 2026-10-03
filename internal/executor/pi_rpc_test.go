//go:build unix

package executor

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hunknownz/Meerkat/internal/executor/pirpc"
	"github.com/hunknownz/Meerkat/internal/model"
)

// A real child process exercises pipes, lifecycle and Git without model access.
func TestPiRPCFixtureChild(t *testing.T) {
	if os.Getenv("MEERKAT_RPC_FIXTURE") != "1" {
		return
	}
	args := os.Args
	file := ""
	for i, a := range args {
		if a == "--session" && i+1 < len(args) {
			file = args[i+1]
		}
	}
	b, _ := os.ReadFile(file)
	var header struct {
		ID string `json:"id"`
	}
	json.Unmarshal([]byte(strings.Split(string(b), "\n")[0]), &header)
	os.WriteFile(os.Getenv("ARGS_OUT"), []byte(strings.Join(args, "\n")), 0o600)
	mode := os.Getenv("RPC_MODE")
	streaming := false
	enc := json.NewEncoder(os.Stdout)
	emit := func(v any) {
		if enc.Encode(v) != nil {
			os.Exit(3)
		}
	}
	response := func(id, cmd string, data any) {
		emit(map[string]any{"type": "response", "id": id, "command": cmd, "success": true, "data": data})
	}
	scan := bufio.NewScanner(os.Stdin)
	scan.Buffer(make([]byte, 65536), maxLine)
	for scan.Scan() {
		var q struct {
			ID      string `json:"id"`
			Type    string `json:"type"`
			Message string `json:"message"`
		}
		if json.Unmarshal(scan.Bytes(), &q) != nil {
			os.Exit(4)
		}
		log, _ := os.OpenFile(os.Getenv("ARGS_OUT")+".commands", os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
		fmt.Fprintln(log, q.Type)
		log.Close()
		switch q.Type {
		case "get_state":
			id := header.ID
			if mode == "bad_identity" {
				id = "different-session"
			}
			response(q.ID, q.Type, map[string]any{"sessionId": id, "sessionFile": file, "isStreaming": streaming, "isCompacting": false, "pendingMessageCount": 0, "model": map[string]string{"provider": "prov", "id": "m1"}})
		case "prompt":
			os.WriteFile(os.Getenv("ARGS_OUT")+".prompt", []byte(q.Message), 0o600)
			if mode == "lost_reply" {
				streaming = true
				continue
			}
			response(q.ID, q.Type, map[string]string{"disposition": "started"})
			streaming = true
			emit(map[string]any{"type": "agent_start"})
			emit(map[string]any{"type": "tool_execution_start", "toolName": "bash", "args": secret})
			if mode == "token_limit" {
				emit(map[string]any{"type": "message_end", "message": map[string]any{"role": "assistant", "usage": map[string]any{"input": 200, "output": 20, "cacheRead": 5, "cacheWrite": 1}}})
				continue
			}
			if mode == "block" || mode == "end_only" {
				if mode == "end_only" {
					emit(map[string]any{"type": "agent_end"})
				}
				continue
			}
			if mode != "reviewer" {
				f, _ := os.OpenFile("a.txt", os.O_WRONLY|os.O_APPEND, 0o644)
				fmt.Fprintln(f, "RPC delivery")
				f.Close()
				if exec.Command("git", "commit", "-qam", "RPC fixture").Run() != nil {
					os.Exit(5)
				}
			}
			sha, _ := exec.Command("git", "rev-parse", "HEAD").Output()
			report := map[string]any{"candidateSha": strings.TrimSpace(string(sha)), "contextDigest": "sha256:abc", "summary": "ok", "checks": []any{}, "knownGaps": []any{}}
			if mode == "reviewer" {
				report["verdict"] = "pass"
				report["findings"] = []any{}
			} else {
				report["decision"] = "changed"
			}
			raw, _ := json.Marshal(report)
			os.WriteFile(os.Getenv("REPORT"), raw, 0o600)
			entry, _ := json.Marshal(map[string]any{"type": "message", "id": "entry-" + fmt.Sprint(time.Now().UnixNano()), "message": map[string]any{"role": "assistant", "content": secret}})
			history, _ := os.OpenFile(file, os.O_WRONLY|os.O_APPEND, 0o600)
			history.Write(append(entry, '\n'))
			history.Close()
			emit(map[string]any{"type": "message_end", "message": map[string]any{"role": "assistant", "usage": map[string]any{"input": 100, "output": 20, "cacheRead": 5, "cacheWrite": 1, "cost": map[string]any{"total": 0.25}}}})
			emit(map[string]any{"type": "agent_end"})
			streaming = false
			emit(map[string]any{"type": "agent_settled"})
		case "clear_queue":
			response(q.ID, q.Type, nil)
		case "abort":
			streaming = false
			response(q.ID, q.Type, nil)
			emit(map[string]any{"type": "agent_settled"})
		default:
			os.Exit(6)
		}
	}
	os.Exit(0)
}

func rpcRequest(t *testing.T, e *env, mode string) Request {
	t.Helper()
	req := e.req("developer", os.Args[0])
	req.Profile.PiCommand = []string{os.Args[0], "-test.run=^TestPiRPCFixtureChild$", "--"}
	if mode == "reviewer" {
		req.Role, req.Profile.Role = "reviewer", "reviewer"
	}
	req.Env = append(req.Env, "MEERKAT_RPC_FIXTURE=1", "RPC_MODE="+mode, "GORACE=atexit_sleep_ms=0")
	file := filepath.Join(e.dir, "history.jsonl")
	b := SessionBinding{ID: "30000000-0000-4000-8000-000000000001", File: file, Worktree: e.wt}
	snap, err := NewPi().InitializeSession(b)
	if err != nil {
		t.Fatal(err)
	}
	b.ProviderID, b.Digest = snap.ProviderID, snap.Digest
	req.Session = &b
	return req
}

func TestPiRPCDeliverySessionReuseUsageAndPrivateArgv(t *testing.T) {
	e := setup(t)
	req := rpcRequest(t, e, "success")
	var events []model.RunEvent
	p := NewPi()
	r, err := p.Execute(context.Background(), req, func(ev model.RunEvent) { events = append(events, ev) }, nil)
	if err != nil || r.Session == nil || !r.Session.Confirmed || r.Outcome != model.RunSucceeded {
		t.Fatalf("%+v %v", r, err)
	}
	if r.Usage.Tokens.Total == nil || *r.Usage.Tokens.Total != 126 {
		t.Fatal(r.Usage)
	}
	args, _ := os.ReadFile(e.args)
	if strings.Contains(string(args), req.TaskBrief) || strings.Contains(string(args), "--print") || strings.Contains(string(args), secret) {
		t.Fatal("private prompt or obsolete JSON mode in argv")
	}
	for _, ev := range events {
		if strings.Contains(ev.Summary, secret) {
			t.Fatal("private event leaked")
		}
	}
	req.Session.Digest = r.Session.Digest
	req.ExpectedSHA = r.ResultSHA
	req.ReportPath = filepath.Join(e.dir, "report-two.json")
	req.Env = append(req.Env, "REPORT="+req.ReportPath)
	r2, err := p.Execute(context.Background(), req, nil, nil)
	if err != nil || r2.Session == nil || !r2.Session.Confirmed || r2.Session.ProviderID != r.Session.ProviderID || r2.Session.Digest == r.Session.Digest {
		t.Fatalf("%+v %v", r2, err)
	}
	if r2.Usage.Tokens.Total == nil || *r2.Usage.Tokens.Total != 126 {
		t.Fatal("history was counted twice", r2.Usage)
	}
}

func TestPiRPCStoppingUnknownReplyAndEndNotSettled(t *testing.T) {
	for _, mode := range []string{"block", "end_only", "lost_reply", "bad_identity"} {
		t.Run(mode, func(t *testing.T) {
			e := setup(t)
			req := rpcRequest(t, e, mode)
			req.RemainingWall = 300 * time.Millisecond
			p := NewPi()
			p.Grace = time.Second
			r, err := p.Execute(context.Background(), req, nil, nil)
			if err == nil || r.Session == nil {
				t.Fatal("unfinished task accepted", r)
			}
			commands, _ := os.ReadFile(e.args + ".commands")
			lines := strings.Split(strings.TrimSpace(string(commands)), "\n")
			clear, abort := -1, -1
			prompts := 0
			for i, s := range lines {
				if s == "clear_queue" {
					clear = i
				}
				if s == "abort" {
					abort = i
				}
				if s == "prompt" {
					prompts++
				}
			}
			if clear < 0 || abort <= clear || prompts > 1 {
				t.Fatal("stop order or prompt replay", lines)
			}
			if mode == "block" || mode == "end_only" {
				if category(err) != CatWallTimeout || !r.Session.Confirmed {
					t.Fatal("known timeout not preserved", r, err)
				}
			} else if r.Session.Confirmed {
				t.Fatal("uncertain identity or request became resumable")
			}
		})
	}
}

func TestPiSessionRejectsTamperingBeforeProcess(t *testing.T) {
	e := setup(t)
	req := rpcRequest(t, e, "success")
	f, _ := os.OpenFile(req.Session.File, os.O_WRONLY|os.O_APPEND, 0o600)
	fmt.Fprintln(f, `{"type":"message","message":{"role":"user","content":"extra"}}`)
	f.Close()
	if _, err := NewPi().Execute(context.Background(), req, nil, nil); err == nil {
		t.Fatal("changed session accepted")
	}
	if _, err := os.Stat(e.args); !os.IsNotExist(err) {
		t.Fatal("spawned after snapshot mismatch")
	}
}

func TestPiSessionRejectsUnsafeOrMismatchedHistory(t *testing.T) {
	for _, mode := range []string{"permissions", "symlink", "worktree", "identity", "truncated", "version"} {
		t.Run(mode, func(t *testing.T) {
			e := setup(t)
			req := rpcRequest(t, e, "success")
			path := req.Session.File
			switch mode {
			case "permissions":
				if err := os.Chmod(path, 0o644); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Rename(path, path+".original"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(path+".original", path); err != nil {
					t.Fatal(err)
				}
			case "truncated":
				if err := os.WriteFile(path, []byte(`{"type":"session"`), 0o600); err != nil {
					t.Fatal(err)
				}
			default:
				header := map[string]any{"type": "session", "version": 3, "id": req.Session.ProviderID, "cwd": req.Worktree}
				if mode == "worktree" {
					header["cwd"] = e.dir
				} else if mode == "identity" {
					header["id"] = "another-history"
				} else {
					header["version"] = 99
				}
				b, _ := json.Marshal(header)
				if err := os.WriteFile(path, append(b, '\n'), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := NewPi().InspectSession(*req.Session); err == nil {
				t.Fatal("unsafe history accepted")
			}
		})
	}
}

func TestPiRPCTokenLimitAndCallerStopSettleHistory(t *testing.T) {
	for _, mode := range []string{"token_limit", "caller_stop"} {
		t.Run(mode, func(t *testing.T) {
			e := setup(t)
			fixtureMode := mode
			if mode == "caller_stop" {
				fixtureMode = "block"
			}
			req := rpcRequest(t, e, fixtureMode)
			req.RemainingTokens = 100
			req.RemainingWall = 10 * time.Second
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			p := NewPi()
			p.Grace = time.Second
			r, err := p.Execute(ctx, req, func(ev model.RunEvent) {
				if mode == "caller_stop" && ev.Type == "lifecycle" && ev.Summary == "started" {
					cancel()
				}
			}, nil)
			want := CatTokenLimit
			if mode == "caller_stop" {
				want = CatCanceled
			}
			if category(err) != want || r.Session == nil || !r.Session.Confirmed || r.ExitCode == nil || *r.ExitCode != 0 || r.Committed {
				t.Fatal("stop did not preserve a verified idle session", r, err)
			}
			if mode == "token_limit" && (r.Usage.Tokens.Total == nil || *r.Usage.Tokens.Total != 226 || r.Usage.EstimatedCostUsd != nil) {
				t.Fatal("observed usage or unknown price changed", r.Usage)
			}
			commands, _ := os.ReadFile(e.args + ".commands")
			if strings.Count(string(commands), "prompt\n") != 1 || !strings.Contains(string(commands), "clear_queue\nabort\n") {
				t.Fatal("stop command sequence", string(commands))
			}
		})
	}
}

func TestInstalledPiPersistentSession(t *testing.T) {
	binary := os.Getenv("MEERKAT_TEST_PI_RPC_BINARY")
	if binary == "" {
		t.Skip("installed-Pi offline probe opt in")
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "private")
	os.Mkdir(dir, 0o700)
	b := SessionBinding{ID: "30000000-0000-4000-8000-000000000002", File: filepath.Join(dir, "history.jsonl"), Worktree: root}
	snap, err := NewPi().InitializeSession(b)
	if err != nil {
		t.Fatal(err)
	}
	b.ProviderID, b.Digest = snap.ProviderID, snap.Digest
	// Stored fixture history proves the same file is loaded, without a paid prompt.
	f, _ := os.OpenFile(b.File, os.O_WRONLY|os.O_APPEND, 0o600)
	f.WriteString(`{"type":"message","id":"a001","parentId":null,"timestamp":"2026-10-03T12:00:00Z","message":{"role":"user","content":"offline history fixture","timestamp":1791028800000}}` + "\n")
	f.Close()
	for i := 0; i < 2; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		cmd := exec.CommandContext(ctx, binary, "--mode", "rpc", "--offline", "--session", b.File, "--no-tools", "--no-extensions", "--no-skills", "--no-context-files", "--no-prompt-templates", "--no-themes")
		cmd.Dir = root
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME"), "PI_CODING_AGENT_DIR=" + filepath.Join(root, "config"), "PI_OFFLINE=1", "PI_TELEMETRY=0"}
		in, _ := cmd.StdinPipe()
		out, _ := cmd.StdoutPipe()
		if cmd.Start() != nil {
			cancel()
			t.Fatal("Pi start failed")
		}
		c, err := pirpc.New(in, out)
		if err != nil {
			cmd.Process.Kill()
			cmd.Wait()
			cancel()
			t.Fatal(err)
		}
		go func() {
			for range c.Events() {
			}
		}()
		state, e := c.State(ctx)
		if e != nil || state.SessionID != b.ProviderID || state.SessionFile != b.File || state.MessageCount == nil || *state.MessageCount != 1 {
			c.Close()
			cmd.Process.Kill()
			cmd.Wait()
			cancel()
			t.Fatalf("session reopen %d failed: %+v %v", i, state, e)
		}
		shutdownErr := c.Shutdown(ctx)
		waitErr := cmd.Wait()
		c.Close()
		cancel()
		if shutdownErr != nil || waitErr != nil {
			t.Fatal("Pi shutdown failed", shutdownErr, waitErr)
		}
	}
	if _, err := NewPi().InspectSession(b); err != nil {
		t.Fatal(err)
	}
}
