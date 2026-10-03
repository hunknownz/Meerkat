//go:build unix

package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	budget "github.com/hunknownz/Meerkat/internal/requestbudget"
)

type probeBudget struct {
	mu          sync.Mutex
	requests    []budget.Request
	begins      []budget.Begin
	settlements []budget.Settlement
	denyAfter   int
}

func (a *probeBudget) Reserve(_ context.Context, r budget.Request) (budget.Grant, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.requests) >= a.denyAfter {
		return budget.Grant{}, budget.ErrDenied
	}
	a.requests = append(a.requests, r)
	return budget.Grant{ID: r.ID, ReservedTokens: r.InputEstimate + 50, MaxOutput: 50}, nil
}
func (a *probeBudget) Begin(_ context.Context, b budget.Begin) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.begins = append(a.begins, b)
	return nil
}
func (a *probeBudget) Settle(_ context.Context, v budget.Settlement) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.settlements = append(a.settlements, v)
	return nil
}

// Real installed Pi talks only to this test's loopback model, with a dummy key
// and isolated HOME/config. It exercises actual provider injection and retries.
func TestInstalledPiRequestBudgetBridge(t *testing.T) {
	binary := os.Getenv("MEERKAT_TEST_PI_RPC_BINARY")
	if binary == "" {
		t.Skip("installed-Pi loopback probe opt in")
	}
	for _, mode := range []string{"usage", "missing-usage", "denied-second", "denied-first"} {
		t.Run(mode, func(t *testing.T) {
			e := setup(t)
			a := &probeBudget{denyAfter: 1}
			if mode == "denied-first" {
				a.denyAfter = 0
			}
			var mu sync.Mutex
			hits := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				hits++
				mu.Unlock()
				a.mu.Lock()
				permitted := len(a.begins) > 0
				a.mu.Unlock()
				if !permitted {
					t.Error("HTTP sent before begin permission")
				}
				var body map[string]any
				if json.NewDecoder(r.Body).Decode(&body) != nil || body["max_tokens"] != float64(50) && body["max_completion_tokens"] != float64(50) {
					t.Error("wire limit not applied")
				}
				if r.Header.Get("Authorization") != "Bearer local-fake-key" {
					t.Error("unexpected authentication")
				}
				w.Header().Set("Content-Type", "text/event-stream")
				chunk := map[string]any{"id": "fixture-response", "object": "chat.completion.chunk", "model": "text-model", "choices": []any{map[string]any{"index": 0, "delta": map[string]any{"role": "assistant", "content": "local fake response"}, "finish_reason": "stop"}}}
				if mode == "denied-second" {
					chunk["choices"] = []any{map[string]any{"index": 0, "delta": map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"index": 0, "id": "fixture-tool", "type": "function", "function": map[string]any{"name": "read", "arguments": "{\"path\":\"a.txt\"}"}}}}, "finish_reason": "tool_calls"}}
				}
				if mode != "missing-usage" {
					chunk["usage"] = map[string]any{"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15, "prompt_tokens_details": map[string]any{"cached_tokens": 0}}
				}
				b, _ := json.Marshal(chunk)
				fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", b)
			}))
			defer server.Close()
			config := filepath.Join(e.dir, "pi-config")
			os.Mkdir(config, 0o700)
			models := map[string]any{"providers": map[string]any{"fixture": map[string]any{"baseUrl": server.URL + "/v1", "api": "openai-completions", "apiKey": "$LOCAL_FAKE_KEY", "models": []any{map[string]any{"id": "text-model", "name": "Local fixture", "reasoning": false, "input": []string{"text"}, "contextWindow": 100000, "maxTokens": 1000}}}}}
			b, _ := json.Marshal(models)
			if err := os.WriteFile(filepath.Join(config, "models.json"), b, 0o600); err != nil {
				t.Fatal(err)
			}
			req := e.req("developer", binary)
			req.Profile.Provider, req.Profile.Model, req.Profile.AuthEnv = "fixture", "text-model", "LOCAL_FAKE_KEY"
			req.Profile.PiCommand = []string{binary, "--offline", "--no-themes", "--thinking", "off"}
			req.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + e.dir, "PI_CODING_AGENT_DIR=" + config, "PI_OFFLINE=1", "PI_TELEMETRY=0", "LOCAL_FAKE_KEY=local-fake-key", "GIT_CONFIG_NOSYSTEM=1"}
			binding := SessionBinding{ID: "30000000-0000-4000-8000-000000000003", File: filepath.Join(e.dir, "budget-history.jsonl"), Worktree: e.wt}
			p := NewPi()
			snap, err := p.InitializeSession(binding)
			if err != nil {
				t.Fatal(err)
			}
			binding.ProviderID, binding.Digest = snap.ProviderID, snap.Digest
			req.Session, req.Budget = &binding, a
			result, err := p.Execute(context.Background(), req, nil, nil)
			if result.Session == nil || !result.Session.Confirmed {
				t.Fatalf("bridge/session failed: category=%s error=%v", result.Category, err)
			}
			mu.Lock()
			actualHits := hits
			mu.Unlock()
			a.mu.Lock()
			defer a.mu.Unlock()
			if mode == "denied-first" {
				if actualHits != 0 || result.Category != CatTokenLimit {
					t.Fatal("first denial bypassed", actualHits, result.Category)
				}
				return
			}
			if actualHits != 1 || len(a.requests) != 1 || len(a.begins) != 1 || len(a.settlements) != 1 {
				t.Fatal("bridge count", actualHits, len(a.requests), len(a.begins), len(a.settlements), result.Category)
			}
			v := a.settlements[0]
			if mode == "missing-usage" {
				if v.State != budget.Unknown || v.Tokens.Total != nil {
					t.Fatal("missing became zero", v)
				}
			} else if v.State != budget.Settled || v.Tokens.Total == nil || *v.Tokens.Total != 15 {
				t.Fatal("raw settlement", v)
			}
			if mode == "denied-second" && result.Category != CatTokenLimit {
				t.Fatal("second denial not surfaced", result.Category)
			}
		})
	}
}
