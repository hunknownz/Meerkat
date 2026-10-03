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
	"strings"
	"sync"
	"testing"

	"github.com/hunknownz/Meerkat/internal/checkpoint"
)

// Installed Pi performs real write/bash tools against a disposable worktree;
// the only model endpoint is loopback and all authentication is a dummy key.
func TestInstalledPiCheckpointContinuation(t *testing.T) {
	binary := os.Getenv("MEERKAT_TEST_PI_RPC_BINARY")
	if binary == "" {
		t.Skip("installed Pi loopback probe opt in")
	}
	e := setup(t)
	var mu sync.Mutex
	phase, hits := 1, 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits++
		n, p := hits, phase
		mu.Unlock()
		var body map[string]any
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			t.Error("invalid wire request")
		}
		if r.Header.Get("Authorization") != "Bearer local-fake-key" {
			t.Error("unexpected authentication")
		}
		delta := map[string]any{"role": "assistant", "content": "local fixture finished"}
		finish := "stop"
		if n == 1 {
			name, args := "write", map[string]any{"path": "a.txt", "content": "preserved checkpoint work\n"}
			if p == 2 {
				name = "bash"
				q := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
				command := "git commit -qam 'continue checkpoint' && printf '{\"candidateSha\":\"%s\",\"contextDigest\":\"sha256:abc\",\"summary\":\"continued\",\"checks\":[],\"knownGaps\":[],\"decision\":\"changed\"}' \"$(git rev-parse HEAD)\" > " + q(e.report)
				args = map[string]any{"command": command}
			}
			argBytes, _ := json.Marshal(args)
			delta = map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"index": 0, "id": "fixture-tool", "type": "function", "function": map[string]any{"name": name, "arguments": string(argBytes)}}}}
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
	req := e.req("developer", binary)
	req.Profile.Provider, req.Profile.Model, req.Profile.AuthEnv = "fixture", "text-model", "LOCAL_FAKE_KEY"
	req.Profile.PiCommand = []string{binary, "--offline", "--no-themes", "--thinking", "off"}
	req.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + e.dir, "PI_CODING_AGENT_DIR=" + config, "PI_OFFLINE=1", "PI_TELEMETRY=0", "LOCAL_FAKE_KEY=local-fake-key", "GIT_CONFIG_NOSYSTEM=1"}
	b := SessionBinding{ID: "30000000-0000-4000-8000-000000000003", File: filepath.Join(e.dir, "checkpoint-history.jsonl"), Worktree: e.wt}
	p := NewPi()
	snap, err := p.InitializeSession(b)
	if err != nil {
		t.Fatal(err)
	}
	b.ProviderID, b.Digest = snap.ProviderID, snap.Digest
	req.Session, req.Budget = &b, &probeBudget{denyAfter: 1}
	first, err := p.Execute(context.Background(), req, nil, nil)
	if category(err) != CatTokenLimit || first.Session == nil || !first.Session.Confirmed || !first.CheckpointSafe || first.Clean || first.Committed {
		t.Fatalf("unsafe pause: category=%s session=%+v safe=%v", first.Category, first.Session, first.CheckpointSafe)
	}
	_, raw, err = checkpoint.Capture(e.wt, []string{"a.txt"})
	if err != nil {
		t.Fatal(err)
	}
	b.Digest = first.Session.Digest
	req.Checkpoint = &checkpoint.Binding{Digest: checkpoint.Digest(raw), Scope: []string{"a.txt"}}
	req.RemainingTokens -= *first.Usage.Tokens.Total
	req.Budget = &probeBudget{denyAfter: 8}
	mu.Lock()
	phase, hits = 2, 0
	mu.Unlock()
	second, err := p.Execute(context.Background(), req, nil, nil)
	if err != nil || !second.Committed || !second.Clean || second.Report == nil || second.Session == nil || !second.Session.Confirmed || second.Session.ID != first.Session.ID {
		t.Fatalf("continuation failed: category=%s error=%v", second.Category, err)
	}
	if run(t, e.wt, "show", "HEAD:a.txt") != "preserved checkpoint work" {
		t.Fatal("partial work lost")
	}
	if run(t, e.wt, "rev-list", "--count", e.sha+"..HEAD") != "1" {
		t.Fatal("wrong delivery commit count")
	}
}
