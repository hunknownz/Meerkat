package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hunknownz/Meerkat/internal/core"
	"github.com/hunknownz/Meerkat/internal/model"
	"github.com/hunknownz/Meerkat/internal/server"
	"github.com/hunknownz/Meerkat/internal/store"
)

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

func run(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var o, e syncBuf
	code := Run(Env{Stdin: strings.NewReader(""), Stdout: &o, Stderr: &e, Ctx: context.Background()}, args)
	return code, o.String(), e.String()
}

func privDir(t *testing.T) string {
	d, err := os.MkdirTemp("", "mkc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(d) })
	return d
}

func TestHelpVersionUsage(t *testing.T) {
	if c, o, _ := run(t, "version"); c != 0 || strings.TrimSpace(o) != server.Version {
		t.Fatal(o)
	}
	if c, o, _ := run(t, "help"); c != 0 || !strings.Contains(o, "serve") {
		t.Fatal("help")
	}
	for _, a := range [][]string{{}, {"nope"}, {"issue"}, {"execute"}, {"stop"}, {"prepare"}, {"export", "--format", "xml"}, {"snapshot", "extra"}, {"serve", "--port", "-1"}, {"issue", "read", "--url", "x"}} {
		if c, _, e := run(t, a...); c != ExitUsage {
			t.Fatalf("%v -> %d %s", a, c, e)
		}
	}
}

func TestNoDaemonIsPreflight(t *testing.T) {
	d := privDir(t)
	if c, _, e := run(t, "snapshot", "--data-dir", d); c != ExitUsage || !strings.Contains(e, "daemon unavailable") {
		t.Fatalf("%d %s", c, e)
	}
}

func TestOfflineExportBackupRestoreDoctor(t *testing.T) {
	d := privDir(t)
	c, o, e := run(t, "export", "--data-dir", d, "--format", "json")
	if c != 0 || !json.Valid([]byte(o)) {
		t.Fatalf("export json %d %s", c, e)
	}
	if c, _, _ := run(t, "export", "--data-dir", d, "--format", "csv"); c != 0 {
		t.Fatal("csv")
	}
	bk := filepath.Join(privDir(t), "b.db")
	if c, _, e := run(t, "backup", "--data-dir", d, "--output", bk); c != 0 {
		t.Fatal(e)
	}
	if c, _, _ := run(t, "backup", "--data-dir", d, "--output", bk); c == 0 {
		t.Fatal("backup overwrote")
	}
	if c, _, _ := run(t, "restore", "--backup", bk, "--to", d); c != ExitUsage {
		t.Fatal("restore must refuse existing dir")
	}
	fresh := filepath.Join(privDir(t), "fresh")
	if c, o, e := run(t, "restore", "--backup", bk, "--to", fresh); c != 0 || !strings.Contains(o, `"merged": false`) {
		t.Fatal(e)
	}
	if c, o, _ := run(t, "doctor", "--data-dir", d); c != 0 || !strings.Contains(o, `"daemonActive": false`) {
		t.Fatal(o)
	}
	if c, _, _ := run(t, "migrate", "--data-dir", d, "--from", filepath.Join(d, "missing")); c == 0 {
		t.Fatal("migrate from missing succeeded")
	}
}

func TestServeSocketCommandsAndOwnerExclusion(t *testing.T) {
	d := privDir(t)
	ctx, cancel := context.WithCancel(context.Background())
	var o, e syncBuf
	done := make(chan int, 1)
	go func() { done <- Run(Env{Stdout: &o, Stderr: &e, Ctx: ctx}, []string{"serve", "--data-dir", d}) }()
	deadline := time.Now().Add(5 * time.Second)
	for !server.Alive(d) {
		if time.Now().After(deadline) {
			t.Fatalf("serve not up: %s", e.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
	var start map[string]any
	if json.Unmarshal([]byte(o.String()), &start) != nil || start["version"] != server.Version || !strings.HasPrefix(start["url"].(string), "http://127.0.0.1:") {
		t.Fatalf("startup %s", o.String())
	}
	if c, out, e := run(t, "snapshot", "--data-dir", d); c != 0 || !strings.Contains(out, `"schemaVersion": 1`) || !strings.Contains(out, "localHistory") {
		t.Fatalf("snapshot %d %s", c, e)
	}
	if c, out, e := run(t, "doctor", "--data-dir", d); c != 0 || !strings.Contains(out, `"daemonActive": true`) || !strings.Contains(out, `"serviceVersion": "`+server.Version+`"`) {
		t.Fatalf("doctor %d %s %s", c, out, e)
	}
	if c, out, _ := run(t, "settings", "--data-dir", d, "--max-concurrency", "3"); c != 0 || !strings.Contains(out, `"maxConcurrency": 3`) {
		t.Fatal(out)
	}
	if c, _, _ := run(t, "settings", "--data-dir", d, "--max-concurrency", "9"); c != ExitUsage {
		t.Fatal("invalid settings")
	}
	if c, _, _ := run(t, "stop", "--data-dir", d, "--run", "not-a-uuid"); c != ExitUsage {
		t.Fatal("bad stop")
	}
	if c, out, e := mcpRun(t, d); c != 0 || e != "" {
		t.Fatalf("mcp %d %s", c, e)
	} else if r := mcpLines(t, out)[2]; r["result"].(map[string]any)["isError"] == true || r["result"].(map[string]any)["_meta"].(map[string]any)["snapshot"].(map[string]any)["schemaVersion"] != float64(1) || strings.Contains(out, "sessionToken") {
		t.Fatalf("mcp snapshot %s", out)
	}
	for _, a := range [][]string{{"serve"}, {"export"}, {"backup", "--output", filepath.Join(d, "x.db")}, {"migrate", "--from", d}} {
		if c, _, _ := run(t, append(a, "--data-dir", d)...); c != ExitUsage {
			t.Fatalf("%v allowed while daemon active", a)
		}
	}
	cancel()
	select {
	case c := <-done:
		if c != 0 {
			t.Fatalf("serve exit %d %s", c, e.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("serve did not stop")
	}
	if _, err := os.Lstat(server.SocketPath(d)); !os.IsNotExist(err) {
		t.Fatal("socket left behind")
	}
}

func TestRunFlagsAndResultCodes(t *testing.T) {
	d := privDir(t)
	// Alias parses (then fails preflight because no daemon), old alias preserved.
	for _, f := range []string{"--acknowledge-interruption", "--acknowledge"} {
		if c, _, e := run(t, "execute", "--data-dir", d, "--task", "x", f); c != ExitUsage || !strings.Contains(e, "daemon unavailable") {
			t.Fatalf("%s: %d %s", f, c, e)
		}
	}
	in := filepath.Join(d, "in.json")
	os.WriteFile(in, []byte(`{}`), 0o600)
	if c, _, e := run(t, "run", "--data-dir", d, "--input", in, "--dry-run"); c != ExitUsage || !strings.Contains(e, "daemon unavailable") {
		t.Fatalf("dry-run without daemon: %d %s", c, e)
	}
	for _, a := range [][]string{{"run"}, {"run", "--input", in, "--config", "x"}, {"run", "--input", in, "--acknowledge"}} {
		if c, _, e := run(t, append(a, "--data-dir", d)...); c != ExitUsage || strings.Contains(e, "daemon unavailable") {
			t.Fatalf("%v -> %d %s", a, c, e)
		}
	}
	reason := core.DelegateCandidate
	cand := core.Result{Mode: "delegate", Tasks: []core.TaskResult{{State: model.TaskFirstDelivery, StateReason: &reason}}}
	if resultCode(cand, model.TaskFirstDelivery, core.DelegateCandidate) != ExitOK || resultCode(cand, model.TaskDelivered, "") != ExitFailed {
		t.Fatal("candidate codes")
	}
	if resultCode(core.Result{Tasks: []core.TaskResult{{State: model.TaskFirstDelivery}}}, model.TaskFirstDelivery, core.DelegateCandidate) != ExitFailed {
		t.Fatal("candidate without reason accepted")
	}
	if resultCode(core.Result{Tasks: []core.TaskResult{{State: model.TaskDelivered}}}, model.TaskDelivered, "") != ExitOK ||
		resultCode(core.Result{Tasks: []core.TaskResult{{State: model.TaskFinalCandidate}}}, model.TaskDelivered, "") != ExitFailed ||
		resultCode(core.Result{}, model.TaskDelivered, "") != ExitFailed {
		t.Fatal("execute codes")
	}
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestRunDryRunThroughDaemon(t *testing.T) {
	for k, v := range map[string]string{"GIT_AUTHOR_NAME": "t", "GIT_AUTHOR_EMAIL": "t@e", "GIT_COMMITTER_NAME": "t", "GIT_COMMITTER_EMAIL": "t@e", "GIT_CONFIG_GLOBAL": "/dev/null"} {
		t.Setenv(k, v)
	}
	root, _ := filepath.EvalSymlinks(privDir(t))
	repo, wt := filepath.Join(root, "repo"), filepath.Join(root, "wt")
	os.Mkdir(repo, 0o755)
	git(t, repo, "init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(repo, "README"), []byte("hi"), 0o644)
	git(t, repo, "add", "-A")
	git(t, repo, "commit", "-qm", "init")
	git(t, repo, "worktree", "add", "-q", "-b", "feat", wt)
	cfg := filepath.Join(root, "profile.json") // all roles may share one private config
	os.WriteFile(cfg, []byte(`{"projectId":"demo","provider":"prov","model":"m-1","authEnv":"SECRET_KEY_ENV","piCommand":["pi-private"]}`), 0o600)
	input := func(worktree string) string {
		b, _ := json.Marshal(map[string]any{"project": map[string]any{"id": "demo", "name": "Demo"}, "repository": repo, "worktree": worktree,
			"title": "T", "goal": "G", "scope": []any{"src"}, "acceptance": []any{"ok"}, "context": map[string]any{"version": 1, "text": "PRIVATE-CTX"},
			"profiles": map[string]any{"developer": cfg, "reviewer": cfg, "polisher": cfg}})
		p := filepath.Join(root, "in.json")
		os.WriteFile(p, b, 0o600)
		return p
	}

	d := privDir(t)
	ctx, cancel := context.WithCancel(context.Background())
	var o, e syncBuf
	done := make(chan int, 1)
	go func() { done <- Run(Env{Stdout: &o, Stderr: &e, Ctx: ctx}, []string{"serve", "--data-dir", d}) }()
	defer func() { cancel(); <-done }()
	deadline := time.Now().Add(5 * time.Second)
	for !server.Alive(d) {
		if time.Now().After(deadline) {
			t.Fatalf("serve not up: %s", e.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
	c, out, errOut := run(t, "run", "--data-dir", d, "--input", input(wt), "--dry-run")
	if c != 0 || !strings.Contains(out, `"valid": true`) || !strings.Contains(out, `"branch": "feat"`) {
		t.Fatalf("dry-run %d %s %s", c, out, errOut)
	}
	for _, bad := range []string{"PRIVATE-CTX", "SECRET_KEY_ENV", "pi-private", cfg} {
		if strings.Contains(out, bad) {
			t.Fatalf("dry-run leaks %q", bad)
		}
	}
	if c, _, _ := run(t, "run", "--data-dir", d, "--input", input(repo), "--dry-run"); c != ExitUsage {
		t.Fatal("primary worktree accepted")
	}
	os.WriteFile(filepath.Join(wt, "dirt"), []byte("x"), 0o644)
	if c, _, errOut := run(t, "run", "--data-dir", d, "--input", input(wt), "--dry-run"); c != ExitUsage || !strings.Contains(errOut, "clean") {
		t.Fatalf("dirty accepted %d %s", c, errOut)
	}
	_, snap, _ := run(t, "snapshot", "--data-dir", d)
	var s struct {
		Data struct {
			Tasks, Runs, Profiles, Contexts, Projects []any
		}
	}
	if json.Unmarshal([]byte(snap), &s) != nil || len(s.Data.Tasks)+len(s.Data.Runs)+len(s.Data.Profiles)+len(s.Data.Contexts)+len(s.Data.Projects) != 0 {
		t.Fatalf("dry-run recorded state: %s", snap)
	}
}

func writePriv(t *testing.T, path string, v any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(v)
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestMigrateProjectMovesAndConflictReport(t *testing.T) {
	const pid, tid, rid = "generic-project", "00000000-0000-4000-8000-000000000041", "00000000-0000-4000-8000-000000000042"
	d := privDir(t)
	st, err := store.Open(d)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Update(func(s *model.State) error {
		s.Projects = append(s.Projects, model.Project{ID: pid, Name: "Generic", Repositories: []string{"/new/repo"}, CreatedAt: "2026-01-02T00:00:00Z", UpdatedAt: "2026-01-02T00:00:00Z"})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	st.Close()
	src := filepath.Join(privDir(t), "old")
	writePriv(t, filepath.Join(src, "workflow", "state.json"), map[string]any{"schemaVersion": 1,
		"projects": []any{map[string]any{"id": pid, "name": "Generic", "repositories": []string{"/old/repo"}, "createdAt": "2025-01-01T00:00:00Z", "updatedAt": "2025-01-01T00:00:00Z"}},
		"contexts": []any{}, "profiles": []any{}, "deliveries": []any{}, "reviews": []any{},
		"tasks": []any{map[string]any{"id": tid, "projectId": pid, "repository": "/old/repo", "worktree": "/old/wt", "title": "t", "goal": "g", "scope": []string{},
			"acceptance": []string{}, "dependencies": []string{}, "state": "delivered", "createdAt": "x", "updatedAt": "x"}},
		"runs": []any{map[string]any{"id": rid, "taskId": tid, "role": "developer", "state": "succeeded", "startedAt": "x", "updatedAt": "x", "events": []any{},
			"usage": map[string]any{"tokens": map[string]any{"input": 7, "output": 3, "total": 10}, "usageCompleteness": "partial", "estimatedCostUsd": nil}}},
	})
	// No option: sanitized conflict kind/id, not a generic failure, no paths.
	c, _, e := run(t, "migrate", "--data-dir", d, "--from", src)
	if c != ExitFailed || !strings.Contains(e, "import conflict: projects "+pid) || strings.Contains(e, "/old/repo") || strings.Contains(e, src) {
		t.Fatalf("conflict report %d %s", c, e)
	}
	mdir := privDir(t)
	if c, _, _ := run(t, "migrate", "--data-dir", d, "--from", src, "--project-moves", "moves.json"); c != ExitUsage {
		t.Fatal("relative --project-moves accepted")
	}
	bad := filepath.Join(mdir, "bad.json")
	writePriv(t, bad, []any{map[string]any{"projectId": pid, "fromRepositories": []string{"/old/repo"}, "toRepositories": []string{"/new/repo"}, "force": true}})
	if c, _, e := run(t, "migrate", "--data-dir", d, "--from", src, "--project-moves", bad); c != ExitUsage || strings.Contains(e, bad) {
		t.Fatalf("invalid moves %d %s", c, e)
	}
	changed := filepath.Join(mdir, "changed.json")
	writePriv(t, changed, []any{map[string]any{"projectId": pid, "fromRepositories": []string{"/old/repo"}, "toRepositories": []string{"/other/repo"}}})
	if c, _, e := run(t, "migrate", "--data-dir", d, "--from", src, "--project-moves", changed); c != ExitFailed || !strings.Contains(e, "import conflict: projectMove "+pid) {
		t.Fatalf("changed target %d %s", c, e)
	}
	good := filepath.Join(mdir, "moves.json")
	writePriv(t, good, []any{map[string]any{"projectId": pid, "fromRepositories": []string{"/old/repo"}, "toRepositories": []string{"/new/repo"}}})
	c, o, e := run(t, "migrate", "--data-dir", d, "--from", src, "--project-moves", good)
	if c != 0 || !strings.Contains(o, `"projectMoves"`) || !strings.Contains(o, `"digest": "sha256:`) || !strings.Contains(o, pid) {
		t.Fatalf("move %d %s %s", c, o, e)
	}
	if strings.Contains(o, "/old/repo") || strings.Contains(o, "/new/repo") || strings.Contains(o, good) || strings.Contains(o, "projectRelocations") {
		t.Fatalf("report leaks paths/private payload: %s", o)
	}
	if c, o, _ := run(t, "migrate", "--data-dir", d, "--from", src, "--project-moves", good); c != 0 || !strings.Contains(o, `"repeat": true`) {
		t.Fatalf("repeat %s", o)
	}
	st, err = store.Open(d)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	s, _ := st.Read()
	if len(s.Projects) != 1 || s.Projects[0].Repositories[0] != "/new/repo" || s.Projects[0].CreatedAt != "2026-01-02T00:00:00Z" {
		t.Fatalf("destination project changed %+v", s.Projects)
	}
	rows, _ := st.MetricsRows()
	if len(rows) != 1 || rows[0].RunID != rid || rows[0].Input == nil || *rows[0].Input != 7 || rows[0].CostUsd != nil {
		t.Fatalf("history usage %+v", rows)
	}
}

const mcpSession = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}
{"jsonrpc":"2.0","method":"notifications/initialized"}
{"jsonrpc":"2.0","id":2,"method":"tools/list"}
{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"get_monitor_snapshot","arguments":{}}}
`

func mcpRun(t *testing.T, dir string) (int, string, string) {
	t.Helper()
	var o, e syncBuf
	code := Run(Env{Stdin: strings.NewReader(mcpSession), Stdout: &o, Stderr: &e, Ctx: context.Background()}, []string{"mcp", "--data-dir", dir})
	return code, o.String(), e.String()
}

// mcpLines requires every stdout line to be a JSON-RPC 2.0 message.
func mcpLines(t *testing.T, out string) []map[string]any {
	t.Helper()
	var res []map[string]any
	for _, l := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil || m["jsonrpc"] != "2.0" {
			t.Fatalf("non JSON-RPC stdout line %q", l)
		}
		res = append(res, m)
	}
	return res
}

func TestMCPCommandWithoutDaemon(t *testing.T) {
	d := privDir(t)
	c, out, e := mcpRun(t, d)
	if c != 0 || e != "" {
		t.Fatalf("mcp %d %s", c, e)
	}
	msgs := mcpLines(t, out)
	if len(msgs) != 3 {
		t.Fatalf("want 3 replies, got %s", out)
	}
	r := msgs[2]["result"].(map[string]any)
	if r["isError"] != true || !strings.Contains(out, "meerkat serve") || strings.Contains(out, d) {
		t.Fatalf("unavailable %s", out)
	}
	if _, err := os.Lstat(server.SocketPath(d)); !os.IsNotExist(err) {
		t.Fatal("mcp must not start a daemon")
	}
	if c, _, _ := run(t, "mcp", "--data-dir", d, "extra"); c != ExitUsage {
		t.Fatal("extra arg accepted")
	}
	if c, o, _ := run(t, "help"); c != 0 || !strings.Contains(o, "mcp") {
		t.Fatal("help lacks mcp")
	}
}
