//go:build unix

package executor

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"github.com/hunknownz/Meerkat/internal/model"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// The child deliberately ends its first turn before its queued follow-up.
func TestPauseFollowUpChild(t *testing.T) {
	if os.Getenv("MEERKAT_PAUSE_CHILD") != "1" {
		return
	}
	file := ""
	for i, a := range os.Args {
		if a == "--session" && i+1 < len(os.Args) {
			file = os.Args[i+1]
		}
	}
	raw, _ := os.ReadFile(file)
	var h struct{ ID string }
	json.Unmarshal([]byte(strings.Split(string(raw), "\n")[0]), &h)
	mode := os.Getenv("PAUSE_MODE")
	streaming := false
	pending := 0
	enc := json.NewEncoder(os.Stdout)
	emit := func(v any) {
		if enc.Encode(v) != nil {
			os.Exit(3)
		}
	}
	response := func(id, cmd string, d any) {
		emit(map[string]any{"type": "response", "id": id, "command": cmd, "success": true, "data": d})
	}
	end := func() {
		emit(map[string]any{"type": "message_end", "message": map[string]any{"role": "assistant", "usage": map[string]any{"input": 100, "output": 10, "cacheRead": 0, "cacheWrite": 0}}})
		emit(map[string]any{"type": "agent_end"})
		emit(map[string]any{"type": "agent_settled"})
	}
	scan := bufio.NewScanner(os.Stdin)
	scan.Buffer(make([]byte, 65536), maxLine)
	for scan.Scan() {
		var q struct{ ID, Type, Message string }
		json.Unmarshal(scan.Bytes(), &q)
		log, _ := os.OpenFile(os.Getenv("ARGS_OUT")+".commands", os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0600)
		fmt.Fprintln(log, q.Type)
		log.Close()
		switch q.Type {
		case "get_state":
			response(q.ID, q.Type, map[string]any{"sessionId": h.ID, "sessionFile": file, "isStreaming": streaming, "isCompacting": false, "pendingMessageCount": pending, "model": map[string]string{"provider": "prov", "id": "m1"}})
			if pending == 1 {
				pending = 0
				streaming = true
				emit(map[string]any{"type": "agent_start"})
				os.WriteFile("a.txt", []byte("follow-up completed\n"), 0644)
				if exec.Command("git", "commit", "-qam", "follow-up").Run() != nil {
					os.Exit(4)
				}
				sha, _ := exec.Command("git", "rev-parse", "HEAD").Output()
				report, _ := json.Marshal(map[string]any{"candidateSha": strings.TrimSpace(string(sha)), "contextDigest": "sha256:abc", "summary": "follow-up complete", "checks": []any{}, "knownGaps": []any{}, "decision": "changed"})
				os.WriteFile(os.Getenv("REPORT"), report, 0600)
				streaming = false
				end()
			}
		case "prompt":
			response(q.ID, q.Type, map[string]string{"disposition": "started"})
			streaming = true
			emit(map[string]any{"type": "agent_start"})
			emit(map[string]any{"type": "tool_execution_start", "toolName": "write", "toolCallId": "write-1"})
			os.WriteFile("a.txt", []byte("saved partial work\n"), 0644)
			if mode == "pause-committed" && exec.Command("git", "commit", "-qam", "provisional").Run() != nil {
				os.Exit(4)
			}
			emit(map[string]any{"type": "tool_execution_end", "toolName": "write", "toolCallId": "write-1", "isError": false, "result": map[string]any{"content": []any{}}})
		case "steer":
			if (mode != "pause" && mode != "pause-committed") || q.Message != gracefulPauseMessage {
				os.Exit(5)
			}
			response(q.ID, q.Type, map[string]string{"disposition": "queued"})
			streaming = false
			end()
		case "follow_up":
			if mode != "follow_up" {
				os.Exit(6)
			}
			response(q.ID, q.Type, map[string]string{"disposition": "queued"})
			pending = 1
			streaming = false
			end()
		case "clear_queue":
			pending = 0
			response(q.ID, q.Type, nil)
		case "abort":
			streaming = false
			response(q.ID, q.Type, nil)
		}
	}
	os.Exit(0)
}

func TestRPCGracefulPauseAndFollowUpWaitForKnownIdle(t *testing.T) {
	for _, kind := range []string{"pause", "pause-committed", "follow_up"} {
		t.Run(kind, func(t *testing.T) {
			e := setup(t)
			req := rpcRequest(t, e, "unused")
			exe, _ := os.Executable()
			req.Profile.PiCommand = []string{exe, "-test.run=TestPauseFollowUpChild", "--"}
			req.Env = append(req.Env, "MEERKAT_PAUSE_CHILD=1", "PAUSE_MODE="+kind)
			req.RemainingWall = 10 * time.Second
			a := &fixtureControlAuthority{path: e.args + ".authority"}
			ch := make(chan RunControl, 1)
			msg := ""
			if kind == "follow_up" {
				msg = "finish the same bounded task"
			}
			controlKind := kind
			if kind == "pause-committed" {
				controlKind = "pause"
			}
			ch <- RunControl{ID: "owned-control", Kind: controlKind, Message: msg}
			req.Controls = &ControlBinding{Messages: ch, Authority: a}
			res, err := NewPi().Execute(context.Background(), req, nil, nil)
			if res.Session == nil || !res.Session.Confirmed || !res.CheckpointSafe {
				t.Fatal("unsafe session", res, err)
			}
			if strings.HasPrefix(kind, "pause") {
				committed := kind == "pause-committed"
				if category(err) != CatPauseRequested || res.Outcome != model.RunStopped || res.Committed != committed || res.Clean != committed {
					t.Fatal(res, err)
				}
			} else if err != nil || res.Outcome != model.RunSucceeded || !res.Committed || !res.Clean {
				t.Fatal("follow-up shut down early", res, err)
			}
			commands, _ := os.ReadFile(e.args + ".commands")
			if strings.Count(string(commands), "prompt\n") != 1 || strings.Contains(string(commands), "abort\n") {
				t.Fatal("replayed or aborted", string(commands))
			}
			if len(a.states) != 2 || a.states[1] != model.ControlAcknowledged {
				t.Fatal(a.states)
			}
		})
	}
}
