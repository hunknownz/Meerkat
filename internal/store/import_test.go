package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hunknownz/Meerkat/internal/model"
)

const (
	uP  = "00000000-0000-4000-8000-000000000001"
	uC  = "00000000-0000-4000-8000-000000000002"
	uPr = "00000000-0000-4000-8000-000000000003"
	uT  = "00000000-0000-4000-8000-000000000004"
	uR1 = "00000000-0000-4000-8000-000000000005"
	uR2 = "00000000-0000-4000-8000-000000000006"
	uD  = "00000000-0000-4000-8000-000000000007"
	uV  = "00000000-0000-4000-8000-000000000008"
	uTH = "00000000-0000-4000-8000-000000000009"
	uRH = "00000000-0000-4000-8000-00000000000a"
	uS  = "00000000-0000-4000-8000-00000000000b"
	uSR = "00000000-0000-4000-8000-00000000000c"
	uDB = "00000000-0000-4000-8000-00000000000d"
)

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(v)
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func legacyFixture() map[string]any {
	ctx := model.Context{ID: uC, ProjectID: uP, Version: 1, Text: "private context <text> & stuff", Sources: []model.ContextSource{{URL: "https://example.com/a", Title: "A"}}, CreatedAt: "2025-01-01T00:00:00Z"}
	ctx.Digest = contextDigest(ctx)
	ref := map[string]any{"id": uC, "version": 1, "digest": ctx.Digest}
	return map[string]any{
		"schemaVersion": 1,
		"projects":      []any{map[string]any{"id": uP, "name": "P", "repositories": []string{"/repo"}, "createdAt": "x", "updatedAt": "x"}},
		"contexts":      []any{ctx},
		"profiles": []any{map[string]any{"id": uPr, "projectId": uP, "role": "developer", "provider": "anthropic", "model": "m1",
			"authEnv": "ANTHROPIC_API_KEY", "instructions": []string{"be careful"}, "limits": map[string]any{}, "piCommand": []string{"pi", "--mode", "json"}, "createdAt": "x"}},
		"tasks": []any{
			map[string]any{"id": uT, "projectId": uP, "repository": "/repo", "worktree": "/wt", "title": "T", "goal": "G", "scope": []string{}, "acceptance": []string{},
				"dependencies": []string{}, "contextRef": ref, "profileIds": map[string]string{"developer": uPr}, "state": "implementing",
				"stateReason": nil, "resumeRole": nil, "candidateSha": nil, "baselineSha": nil, "createdAt": "x", "updatedAt": "x",
				"issueRef": map[string]any{"url": "https://github.com/o/r/issues/1", "title": "I"}},
			map[string]any{"id": uTH, "projectId": uP, "repository": "/repo", "worktree": "/wt2", "title": "old", "goal": "g", "scope": []string{}, "acceptance": []string{},
				"dependencies": []string{}, "state": "delivered", "createdAt": "x", "updatedAt": "x"},
		},
		"runs": []any{
			map[string]any{"id": uR1, "taskId": uT, "role": "developer", "profileId": uPr, "contextRef": ref, "state": "running", "pid": 999999,
				"startedAt": "2025-01-01T00:00:00Z", "updatedAt": "x", "events": []any{},
				"usage": map[string]any{"tokens": map[string]any{"input": 10, "output": 5, "cacheRead": 0, "cacheWrite": 0, "total": 15}, "usageCompleteness": "complete", "estimatedCostUsd": 0.01}},
			map[string]any{"id": uR2, "taskId": uT, "role": "reviewer", "state": "succeeded", "startedAt": "2025-01-01T00:00:00Z", "endedAt": "2025-01-01T00:01:00Z", "updatedAt": "x", "events": []any{}},
			map[string]any{"id": uRH, "taskId": uTH, "role": "developer", "state": "succeeded", "startedAt": "x", "updatedAt": "x", "events": []any{}},
		},
		"deliveries": []any{map[string]any{"id": uD, "taskId": uT, "contextRef": ref, "candidateSha": "abc", "repository": "/repo", "runIds": []string{uR1},
			"checks": []any{map[string]any{"name": "go test", "status": "passed"}}, "knownGaps": []string{"none"}, "state": "first", "createdAt": "x", "updatedAt": "x"}},
		"reviews": []any{map[string]any{"id": uV, "taskId": uT, "runId": uR2, "candidateSha": "abc", "contextDigest": ctx.Digest, "verdict": "pass", "findings": []any{}, "checks": []any{}, "createdAt": "x"}},
	}
}

func legacySource(t *testing.T, state map[string]any) (from, root string) {
	t.Helper()
	base := t.TempDir()
	from = filepath.Join(base, "old")
	writeJSON(t, filepath.Join(from, "workflow", "state.json"), state)
	writeJSON(t, filepath.Join(from, "workflow", "settings.json"), map[string]any{"maxConcurrency": 3, "maxFixRounds": 1, "defaultProfiles": map[string]any{}})
	writeJSON(t, filepath.Join(from, "workflow", "requests", "processed", "stop-"+uS+".json"), map[string]any{"type": "stop", "requestId": uS, "runId": uR2, "createdAt": "x"})
	body := "<!-- meerkat-delivery:" + uD + " -->\nhello\n"
	os.MkdirAll(filepath.Join(from, "issue-sync"), 0o700)
	if err := os.WriteFile(filepath.Join(from, "issue-sync", uD+".md"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	writeJSON(t, filepath.Join(from, "issue-sync", uD+".json"), map[string]any{"schemaVersion": 1, "deliveryId": uD, "taskId": uT,
		"issueUrl": "https://github.com/o/r/issues/1", "bodyHash": hashBody([]byte(body)), "state": "posting", "attempts": 1, "preparedAt": "x", "updatedAt": "x"})
	writeJSON(t, filepath.Join(from, "tasks.json"), []any{map[string]any{"id": uDB, "title": "dash task", "stage": "done"}})
	root = filepath.Join(base, "wt")
	// standalone receipt (no workflow run) + one linked to a workflow run (must not double count)
	writeJSON(t, filepath.Join(root, ".pi-developer", "runs", "a.json"), map[string]any{"projectId": "p", "model": "openai/gpt", "runId": uSR, "role": "developer",
		"startedAt": "2025-01-01T00:00:00Z", "endedAt": "2025-01-01T00:00:30Z", "tokens": map[string]any{"input": 1, "output": nil}, "usageCompleteness": "partial",
		"estimatedCostUsd": nil, "outcome": "success", "report": map[string]any{"summary": "x"}})
	writeJSON(t, filepath.Join(root, ".pi-developer", "runs", "b.json"), map[string]any{"runId": uR1, "role": "developer", "tokens": map[string]any{"input": 10}})
	return from, root
}

func TestImportLegacyRepeatAndHistory(t *testing.T) {
	s, _ := openTemp(t)
	from, root := legacySource(t, legacyFixture())
	before := snapshotTree(t, from)
	rep, err := s.ImportLegacy(from, []string{root})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Imported["tasks"] != 1 || rep.Imported["runs"] != 2 || rep.Imported["history"] != 4 || rep.UnknownRuns != 1 || rep.UnknownTasks != 1 {
		t.Fatalf("report %+v", rep)
	}
	if rep.Duplicates["standaloneLinked"] != 1 || rep.Imported["issueReceipts"] != 1 || rep.Imported["stopReceipts"] != 1 || rep.Imported["settings"] != 1 {
		t.Fatalf("report %+v", rep)
	}
	rb, _ := json.Marshal(rep)
	if strings.Contains(string(rb), "private context") || strings.Contains(string(rb), from) {
		t.Fatal("report leaks content or paths")
	}
	if snapshotTree(t, from) != before {
		t.Fatal("source mutated")
	}
	st, _ := s.Read()
	if len(st.Tasks) != 1 || st.Tasks[0].State != model.TaskUnknown || st.Tasks[0].RecordedState != "implementing" {
		t.Fatalf("task %+v", st.Tasks)
	}
	for _, r := range st.Runs {
		if r.ID == uR1 && (r.State != model.RunUnknown || r.PID != nil || r.Usage == nil || *r.Usage.Tokens.Total != 15) {
			t.Fatalf("run %+v", r)
		}
		if r.ID == uR2 && r.Usage != nil {
			t.Fatal("unknown usage must stay nil")
		}
	}
	if p := st.Profiles[0]; p.AuthEnv != "ANTHROPIC_API_KEY" || len(p.PiCommand) != 3 || p.Instructions[0] != "be careful" {
		t.Fatalf("profile not preserved %+v", p)
	}
	hist, _ := s.History()
	for _, h := range hist {
		if h.Executable || h.TaskID == uT {
			t.Fatalf("history must be non-executable legacy only: %+v", h)
		}
	}
	rc, err := s.LoadIssueReceipt(uD)
	if err != nil || rc.State != IssueUnknown || rc.Attempts != 1 || rc.Marker != DeliveryMarker(uD) {
		t.Fatalf("receipt %+v %v", rc, err)
	}
	if b, err := s.ReadIssueBody(rc); err != nil || !strings.Contains(string(b), "hello") {
		t.Fatal("body not published", err)
	}
	if fi, _ := os.Stat(rc.BodyPath); fi.Mode().Perm() != 0o600 {
		t.Fatal("body perms")
	}
	set, _ := s.GetSettings()
	if set.MaxConcurrency != 3 {
		t.Fatal("settings")
	}
	// repeat: idempotent
	rep2, err := s.ImportLegacy(from, []string{root})
	if err != nil || !rep2.Repeat || rep2.ImportID != rep.ImportID {
		t.Fatalf("repeat %+v %v", rep2, err)
	}
	st2, _ := s.Read()
	if len(st2.Runs) != len(st.Runs) {
		t.Fatal("repeat changed state")
	}
	// metrics: workflow + standalone + legacy history run, no double count of uR1
	js, err := s.ExportMetrics("json")
	if err != nil {
		t.Fatal(err)
	}
	var rows []MetricsRow
	json.Unmarshal(js, &rows)
	ids := map[string]int{}
	for _, r := range rows {
		ids[r.RunID]++
	}
	if len(rows) != 4 || ids[uR1] != 1 || ids[uSR] != 1 || ids[uRH] != 1 {
		t.Fatalf("metrics %s", js)
	}
	if strings.Contains(string(js), "ANTHROPIC") || strings.Contains(string(js), "private context") || strings.Contains(string(js), "piCommand") {
		t.Fatal("metrics leak")
	}
	csv, _ := s.ExportMetrics("csv")
	if !strings.Contains(string(csv), "changeId") || strings.Count(string(csv), "\n") != 5 {
		t.Fatalf("csv %s", csv)
	}
}

func snapshotTree(t *testing.T, root string) string {
	var b strings.Builder
	filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
		if err == nil && fi.Mode().IsRegular() {
			d, _ := os.ReadFile(p)
			b.WriteString(p + string(d))
		}
		return nil
	})
	return b.String()
}

func TestImportDivergentIsAtomic(t *testing.T) {
	s, _ := openTemp(t)
	from, root := legacySource(t, legacyFixture())
	if _, err := s.ImportLegacy(from, []string{root}); err != nil {
		t.Fatal(err)
	}
	before, _ := s.Read()
	hb, _ := s.History()
	// mutate same-ID task in a new source: must conflict, not skip by id
	fx := legacyFixture()
	fx["tasks"].([]any)[0].(map[string]any)["title"] = "changed"
	fx["projects"] = append(fx["projects"].([]any), map[string]any{"id": "00000000-0000-4000-8000-0000000000ff", "name": "New", "repositories": []string{}, "createdAt": "x", "updatedAt": "x"})
	from2, root2 := legacySource(t, fx)
	rep, err := s.ImportLegacy(from2, []string{root2})
	if !errors.Is(err, ErrConflict) || len(rep.Conflicts) == 0 || rep.Conflicts[0].ID != uT {
		t.Fatalf("want conflict, got %v %+v", err, rep)
	}
	after, _ := s.Read()
	ha, _ := s.History()
	if len(after.Projects) != len(before.Projects) || after.Tasks[0].Title != "T" || len(ha) != len(hb) {
		t.Fatal("divergent import was not atomic")
	}
	imps, _ := s.Imports()
	if len(imps) != 1 {
		t.Fatal("failed import recorded")
	}
}

func TestImportCorruptAndUnsafe(t *testing.T) {
	s, dir := openTemp(t)
	cases := map[string]func(map[string]any){
		"unknown field": func(m map[string]any) { m["tasks"].([]any)[0].(map[string]any)["apiKey"] = "x" },
		"digest mismatch": func(m map[string]any) {
			m["contexts"].([]any)[0] = func() model.Context { c := m["contexts"].([]any)[0].(model.Context); c.Text = "other"; return c }()
		},
		"secret profile": func(m map[string]any) {
			m["profiles"].([]any)[0].(map[string]any)["authEnv"] = "sk-ant-" + strings.Repeat("a", 30)
		},
		"secret command": func(m map[string]any) {
			m["profiles"].([]any)[0].(map[string]any)["piCommand"] = []string{"pi", "--api-key=" + "ghp_" + strings.Repeat("b", 36)}
		},
		"secret summary": func(m map[string]any) {
			m["runs"].([]any)[1].(map[string]any)["summary"] = map[string]any{"token": "abc"}
		},
		"bad relation": func(m map[string]any) { m["deliveries"].([]any)[0].(map[string]any)["runIds"] = []string{uRH} },
	}
	for name, mut := range cases {
		fx := legacyFixture()
		mut(fx)
		from, root := legacySource(t, fx)
		if _, err := s.ImportLegacy(from, []string{root}); err == nil {
			t.Fatalf("%s: expected rejection", name)
		} else if strings.Contains(err.Error(), "sk-ant") || strings.Contains(err.Error(), "ghp_") {
			t.Fatalf("%s: error leaks secret", name)
		}
	}
	st, _ := s.Read()
	if len(st.Tasks) != 0 {
		t.Fatal("rejected import wrote state")
	}
	// symlinked state file
	from, root := legacySource(t, legacyFixture())
	sp := filepath.Join(from, "workflow", "state.json")
	os.Rename(sp, sp+".real")
	os.Symlink(sp+".real", sp)
	if _, err := s.ImportLegacy(from, []string{root}); !errors.Is(err, ErrUnsafeSource) {
		t.Fatalf("symlink: %v", err)
	}
	// live source controller
	from, root = legacySource(t, legacyFixture())
	writeJSON(t, filepath.Join(from, "workflow", "controller.lock", "owner.json"), map[string]any{"pid": os.Getpid(), "host": func() string { h, _ := os.Hostname(); return h }()})
	if _, err := s.ImportLegacy(from, []string{root}); !errors.Is(err, ErrLiveController) {
		t.Fatalf("live source: %v", err)
	}
	// live destination controller
	from, root = legacySource(t, legacyFixture())
	l, _ := s.AcquireLease()
	if _, err := s.ImportLegacy(from, []string{root}); !errors.Is(err, ErrLiveController) {
		t.Fatalf("live dest: %v", err)
	}
	s.ReleaseLease(l.Token)
	// overlapping source/destination
	if _, err := s.ImportLegacy(dir, nil); !errors.Is(err, ErrUnsafeSource) {
		t.Fatalf("overlap: %v", err)
	}
	// relative run root
	if _, err := s.ImportLegacy(from, []string{"rel"}); !errors.Is(err, ErrUnsafeSource) {
		t.Fatalf("relative root: %v", err)
	}
}

func TestIssueReceiptCAS(t *testing.T) {
	s, _ := openTemp(t)
	path, hash, err := s.WriteIssueBody(uD, []byte("body"))
	if err != nil {
		t.Fatal(err)
	}
	r, created, err := s.SaveIssueReceipt(IssueReceipt{DeliveryID: uD, TaskID: uT, BodyHash: hash, BodyPath: path, State: IssuePending})
	if err != nil || !created {
		t.Fatal(err)
	}
	if _, c, err := s.SaveIssueReceipt(IssueReceipt{DeliveryID: uD, TaskID: uT, BodyHash: hash, BodyPath: path, State: IssuePending}); err != nil || c {
		t.Fatal("save must be create-once")
	}
	a, b := r, r
	a.State, b.State = IssuePosting, IssuePosting
	if _, err := s.CompareAndSetIssueReceipt(a); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CompareAndSetIssueReceipt(b); !errors.Is(err, ErrConflict) {
		t.Fatal("stale CAS must conflict")
	}
	if _, _, err := s.WriteIssueBody(uD, []byte("other")); !errors.Is(err, ErrConflict) {
		t.Fatal("body must not be overwritten")
	}
	if err := s.AddHistory(HistoryEntry{ID: "h1", Kind: "standalone_run", Executable: true}); err != nil {
		t.Fatal(err)
	}
	h, _ := s.History()
	if len(h) != 1 || h[0].Executable {
		t.Fatal("history must be non-executable")
	}
}
