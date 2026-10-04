//go:build unix

package executor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// Uses the installed Pi SDK and its actual tool schema validation. All model
// responses are local fixtures; no user credential or remote endpoint is used.
type reportControlProbe struct{ fixtureControlAuthority }

func (*reportControlProbe) Quiesce() (bool, error) { return true, nil }

func TestInstalledPiStructuredReportTool(t *testing.T) {
	binary := os.Getenv("MEERKAT_TEST_PI_RPC_BINARY")
	if binary == "" {
		t.Skip("installed Pi loopback probe opt in")
	}
	for _, scenario := range []string{"developer", "polisher", "reviewer", "developer-follow-up"} {
		t.Run(scenario, func(t *testing.T) {
			role := scenario
			followUp := scenario == "developer-follow-up"
			if followUp {
				role = "developer"
			}
			e := setup(t)
			controls := make(chan RunControl, 1)
			var mu sync.Mutex
			hits, toolVisible := 0, false
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body struct {
					Tools []struct {
						Function struct {
							Name string `json:"name"`
						} `json:"function"`
					} `json:"tools"`
				}
				if json.NewDecoder(r.Body).Decode(&body) != nil {
					t.Error("invalid request")
				}
				mu.Lock()
				hits++
				n := hits
				for _, tool := range body.Tools {
					toolVisible = toolVisible || tool.Function.Name == "meerkat_report"
				}
				mu.Unlock()
				delta := map[string]any{"role": "assistant", "content": "fixture finished"}
				finish := "stop"
				reportAt := 1
				if role == "developer" {
					reportAt = 2
				}
				if role == "developer" && n == 1 {
					delta = map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"index": 0, "id": "fixture-commit", "type": "function", "function": map[string]any{"name": "bash", "arguments": `{"command":"printf 'fixture change\\n' >> a.txt && git commit -qam fixture"}`}}}}
					finish = "tool_calls"
					if followUp {
						controls <- RunControl{ID: "fixture-follow-up", Kind: "follow_up", Message: "Follow-up fixture direction"}
						delta = map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"index": 0, "id": "fixture-draft", "type": "function", "function": map[string]any{"name": "bash", "arguments": `{"command":"printf 'fixture change\\n' >> a.txt && sleep 1"}`}}}}
					}
				}
				if followUp && n == 4 {
					if _, err := os.Stat(e.report); !os.IsNotExist(err) {
						t.Error("report saved before queued follow-up consumed")
					}
					delta = map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"index": 0, "id": "fixture-final-commit", "type": "function", "function": map[string]any{"name": "bash", "arguments": `{"command":"git commit -qam fixture"}`}}}}
					finish = "tool_calls"
				}
				if n == reportAt || followUp && n == 5 {
					draft := map[string]any{"summary": "Observed fixture", "checks": []any{}, "knownGaps": []any{}}
					if role == "reviewer" {
						draft["verdict"], draft["findings"] = "pass", []any{}
					} else {
						draft["decision"] = "no_change"
						if role == "developer" {
							draft["decision"] = "changed"
						}
					}
					args, _ := json.Marshal(draft)
					delta = map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"index": 0, "id": "fixture-report", "type": "function", "function": map[string]any{"name": "meerkat_report", "arguments": string(args)}}}}
					finish = "tool_calls"
				}
				chunk := map[string]any{"id": "fixture-response", "object": "chat.completion.chunk", "model": "text-model", "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}, "usage": map[string]any{"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15, "prompt_tokens_details": map[string]any{"cached_tokens": 0}}}
				raw, _ := json.Marshal(chunk)
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", raw)
			}))
			defer srv.Close()
			config := filepath.Join(e.dir, "pi-config")
			os.Mkdir(config, 0o700)
			models := map[string]any{"providers": map[string]any{"fixture": map[string]any{"baseUrl": srv.URL + "/v1", "api": "openai-completions", "apiKey": "$LOCAL_FAKE_KEY", "models": []any{map[string]any{"id": "text-model", "name": "Local fixture", "reasoning": false, "input": []string{"text"}, "contextWindow": 100000, "maxTokens": 1000}}}}}
			raw, _ := json.Marshal(models)
			os.WriteFile(filepath.Join(config, "models.json"), raw, 0o600)
			req := e.req(role, binary)
			req.Profile.Provider, req.Profile.Model, req.Profile.AuthEnv = "fixture", "text-model", "LOCAL_FAKE_KEY"
			req.Profile.PiCommand = []string{binary, "--offline", "--no-themes", "--thinking", "off"}
			req.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + e.dir, "PI_CODING_AGENT_DIR=" + config, "PI_OFFLINE=1", "PI_TELEMETRY=0", "LOCAL_FAKE_KEY=local-fake-key", "GIT_CONFIG_NOSYSTEM=1"}
			binding := SessionBinding{ID: "30000000-0000-4000-8000-000000000003", File: filepath.Join(e.dir, "history.jsonl"), Worktree: e.wt}
			p := NewPi()
			snap, err := p.InitializeSession(binding)
			if err != nil {
				t.Fatal(err)
			}
			binding.ProviderID, binding.Digest = snap.ProviderID, snap.Digest
			a := &probeBudget{denyAfter: 8}
			req.Session, req.Budget = &binding, a
			if followUp {
				req.Controls = &ControlBinding{Messages: controls, Authority: &reportControlProbe{fixtureControlAuthority: fixtureControlAuthority{path: filepath.Join(e.dir, "control-accepted")}}}
			}
			result, err := p.Execute(context.Background(), req, nil, nil)
			if err != nil || result.Report == nil || result.Report.CandidateSHA != run(t, e.wt, "rev-parse", "HEAD") || result.Report.ContextDigest == nil || *result.Report.ContextDigest != req.ContextDigest {
				if raw, e := os.ReadFile(binding.File); e == nil {
					for _, line := range bytes.Split(raw, []byte("\n")) {
						var entry struct {
							Message struct {
								Role    string `json:"role"`
								Content any    `json:"content"`
							} `json:"message"`
						}
						if json.Unmarshal(line, &entry) == nil && entry.Message.Role == "toolResult" {
							t.Logf("isolated fixture tool result: %v", entry.Message.Content)
						}
					}
				}
				t.Fatalf("report tool failed: %s %v", result.Category, err)
			}
			mu.Lock()
			defer mu.Unlock()
			a.mu.Lock()
			defer a.mu.Unlock()
			want := 2
			if role == "developer" {
				want = 3
			}
			if followUp {
				want = 6
				raw, err := os.ReadFile(binding.File)
				if err != nil || !bytes.Contains(raw, []byte("Follow-up fixture direction")) {
					t.Fatal("follow-up missing from same history")
				}
			}
			if !toolVisible || hits != want || len(a.settlements) != want {
				t.Fatal("tool/settlement count", toolVisible, hits, len(a.settlements))
			}
		})
	}
}

func TestReportWriterDerivesBindingsAndRefusesChangedReplay(t *testing.T) {
	e := setup(t)
	wt, base := e.wt, e.sha
	dir := t.TempDir()
	os.Chmod(dir, 0o700)
	path := filepath.Join(dir, "report.json")
	write := reportWriter(Request{Role: "developer", ContextDigest: "sha256:abc", ExpectedSHA: base}, &prepared{worktree: wt, report: path})
	draft := json.RawMessage(`{"summary":"Observed local work","checks":[],"knownGaps":[],"decision":"no_change"}`)
	for i := 0; i < 2; i++ {
		if _, err := write(context.Background(), draft); err != nil {
			t.Fatal(err)
		}
	}
	report, cat := readReport(path, "developer")
	if cat != "" || report.CandidateSHA != base || report.ContextDigest == nil || *report.ContextDigest != "sha256:abc" || len(report.Checks) != 0 {
		t.Fatal(report, cat)
	}
	if _, err := write(context.Background(), json.RawMessage(`{"summary":"Changed replay","checks":[],"knownGaps":[],"decision":"no_change"}`)); err == nil {
		t.Fatal("changed report overwrote original")
	}
}

func TestReportWriterRefusesBindingOverridesBadChecksAndSymlink(t *testing.T) {
	e := setup(t)
	wt, base := e.wt, e.sha
	dir := t.TempDir()
	os.Chmod(dir, 0o700)
	for _, body := range []string{
		`{"candidateSha":"` + base + `","summary":"x","checks":[],"knownGaps":[],"decision":"no_change"}`,
		`{"summary":"x","checks":[{"command":"checked","observed":"passed"}],"knownGaps":[],"decision":"no_change"}`,
		`{"summary":"x","checks":[{"command":"checked","result":"passed","observed":"passed"}],"knownGaps":[],"decision":"no_change"}`,
		`{"summary":"x","checks":[],"knownGaps":[],"verdict":"pass","findings":[]}`,
	} {
		path := filepath.Join(dir, "bad.json")
		write := reportWriter(Request{Role: "developer", ExpectedSHA: base}, &prepared{worktree: wt, report: path})
		if _, err := write(context.Background(), json.RawMessage(body)); err == nil {
			t.Fatal("invalid draft accepted")
		}
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatal("invalid draft wrote a report")
		}
	}
	target := filepath.Join(dir, "target")
	os.WriteFile(target, []byte("unchanged"), 0o600)
	path := filepath.Join(dir, "link.json")
	os.Symlink(target, path)
	write := reportWriter(Request{Role: "developer", ExpectedSHA: base}, &prepared{worktree: wt, report: path})
	if _, err := write(context.Background(), json.RawMessage(`{"summary":"x","checks":[],"knownGaps":[],"decision":"no_change"}`)); err == nil {
		t.Fatal("symlink report accepted")
	}
	if b, _ := os.ReadFile(target); string(b) != "unchanged" {
		t.Fatal("symlink target modified")
	}
}

func TestReportWriterReviewerPinsCandidateAndRetainsNullContext(t *testing.T) {
	e := setup(t)
	write := reportWriter(Request{Role: "reviewer", ExpectedSHA: e.sha}, &prepared{worktree: e.wt, report: e.report})
	draft := json.RawMessage(`{"summary":"Reviewed candidate","checks":[],"knownGaps":[],"verdict":"pass","findings":[]}`)
	if _, err := write(context.Background(), draft); err != nil {
		t.Fatal(err)
	}
	report, cat := readReport(e.report, "reviewer")
	if cat != "" || report.ContextDigest != nil || report.CandidateSHA != e.sha {
		t.Fatal(report, cat)
	}
	os.WriteFile(filepath.Join(e.wt, "a.txt"), []byte("changed\n"), 0o644)
	run(t, e.wt, "commit", "-qam", "changed candidate")
	if _, err := write(context.Background(), draft); err == nil {
		t.Fatal("changed reviewer candidate accepted")
	}
}
