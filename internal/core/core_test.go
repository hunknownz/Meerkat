package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hunknownz/Meerkat/internal/executor"
	"github.com/hunknownz/Meerkat/internal/model"
	"github.com/hunknownz/Meerkat/internal/store"
)

const ctxText = "PRIVATE-CONTEXT-TEXT do not leak"

// fakeExec simulates a role step with real Git operations in the task worktree.
type fakeExec struct {
	mu        sync.Mutex
	verdicts  []string // consumed per reviewer run; default pass
	polish    bool     // polisher commits a change
	devPath   string   // path the developer writes (default src/a.txt)
	devTwo    bool     // developer makes two commits
	badSHA    bool
	badDigest bool
	revMutate bool
	block     bool // block until ctx is done
	roles     []string
	reqs      []executor.Request
	active    atomic.Int32
	maxActive atomic.Int32
	gate      chan struct{} // optional: each run waits for gate or ctx
}

func (f *fakeExec) Name() string                   { return "fake" }
func (f *fakeExec) Validate(p model.Profile) error { return nil }

func sh(t testing.TB, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func commit(dir, path, content string) error {
	full := filepath.Join(dir, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		return err
	}
	for _, a := range [][]string{{"add", "-A"}, {"commit", "-qm", "change " + path}} {
		c := exec.Command("git", a...)
		c.Dir = dir
		if out, err := c.CombinedOutput(); err != nil {
			return fmt.Errorf("%v %s", err, out)
		}
	}
	return nil
}

func (f *fakeExec) Execute(ctx context.Context, req executor.Request, onEvent func(model.RunEvent), onStart func(executor.Process)) (executor.Result, error) {
	n := f.active.Add(1)
	defer f.active.Add(-1)
	for {
		m := f.maxActive.Load()
		if n <= m || f.maxActive.CompareAndSwap(m, n) {
			break
		}
	}
	f.mu.Lock()
	f.roles = append(f.roles, req.Role)
	f.reqs = append(f.reqs, req)
	verdict := "pass"
	if req.Role == "reviewer" && len(f.verdicts) > 0 {
		verdict, f.verdicts = f.verdicts[0], f.verdicts[1:]
	}
	f.mu.Unlock()
	res := executor.Result{RunID: req.RunID, Executor: "fake", Role: req.Role, BaselineSHA: req.ExpectedSHA, StartedAt: time.Now(),
		Process: &executor.Process{Executor: "fake", Host: "test", StartedAt: time.Now()}}
	onStart(*res.Process)
	onEvent(model.RunEvent{Type: "tool", Summary: "bash"})
	if f.gate != nil {
		select {
		case <-f.gate:
		case <-ctx.Done():
		}
	}
	if f.block {
		<-ctx.Done()
	}
	total := int64(100)
	zero := int64(0)
	res.Usage = model.Usage{Tokens: model.TokenCounts{Input: &total, Output: &zero, CacheRead: &zero, CacheWrite: &zero, Total: &total}, UsageCompleteness: model.UsageComplete}
	if ctx.Err() != nil {
		res.Outcome, res.Category = model.RunStopped, executor.CatCanceled
		return res, &executor.Error{Category: executor.CatCanceled}
	}
	rep := &executor.Report{Summary: "done", Checks: []executor.Check{{Command: "go test", Result: "pass"}}, KnownGaps: []string{}}
	var err error
	switch req.Role {
	case "developer":
		p := f.devPath
		if p == "" {
			p = "src/a.txt"
		}
		err = commit(req.Worktree, p, req.RunID)
		if f.devTwo && err == nil {
			err = commit(req.Worktree, "src/b.txt", req.RunID)
		}
		rep.Decision = "changed"
	case "polisher":
		rep.Decision = "no_change"
		if f.polish {
			err = commit(req.Worktree, "src/polish.txt", req.RunID)
			rep.Decision = "changed"
		}
	case "reviewer":
		rep.Verdict = verdict
		if verdict == "changes_requested" {
			rep.Findings = []executor.Finding{{ID: "F1", Summary: "fix it"}}
		}
		if f.revMutate {
			err = commit(req.Worktree, "src/rev.txt", "x")
		}
	}
	if err != nil {
		return res, err
	}
	head, _ := git(req.Worktree, "rev-parse", "HEAD")
	res.ResultSHA, res.Clean, res.Committed = head, true, head != req.ExpectedSHA
	rep.CandidateSHA = head
	if f.badSHA {
		rep.CandidateSHA = strings.Repeat("a", 40)
	}
	d := req.ContextDigest
	if f.badDigest {
		d = "sha256:" + strings.Repeat("0", 64)
	}
	rep.ContextDigest = &d
	res.Report, res.Outcome, res.EndedAt = rep, model.RunSucceeded, time.Now()
	return res, nil
}

type env struct {
	t        *testing.T
	root     string
	repo     string
	data     string
	st       *store.Store
	c        *Core
	fx       *fakeExec
	profiles map[string]string
}

func setup(t *testing.T) *env {
	t.Helper()
	for k, v := range map[string]string{"GIT_AUTHOR_NAME": "t", "GIT_AUTHOR_EMAIL": "t@e", "GIT_COMMITTER_NAME": "t", "GIT_COMMITTER_EMAIL": "t@e", "GIT_CONFIG_GLOBAL": "/dev/null"} {
		t.Setenv(k, v)
	}
	root, _ := filepath.EvalSymlinks(t.TempDir())
	e := &env{t: t, root: root, repo: filepath.Join(root, "repo"), data: filepath.Join(root, "data"), fx: &fakeExec{}, profiles: map[string]string{}}
	os.Mkdir(e.repo, 0o755)
	sh(t, e.repo, "init", "-q", "-b", "main")
	if err := commit(e.repo, "README", "hi"); err != nil {
		t.Fatal(err)
	}
	for _, role := range model.Roles {
		p := filepath.Join(root, role+".json")
		os.WriteFile(p, []byte(`{"projectId":"demo","executor":"fake","provider":"prov","model":"m-1","authEnv":"FAKE_SECRET_ENV","piCommand":["pi-private-cmd"],"instructions":[],"limits":{"maxTokens":1000,"maxWallSeconds":600}}`), 0o600)
		e.profiles[role] = p
	}
	st, err := store.Open(e.data)
	if err != nil {
		t.Fatal(err)
	}
	e.st = st
	e.c = e.newCore()
	t.Cleanup(func() { e.c.Close(); st.Close() })
	return e
}

func (e *env) newCore() *Core {
	c, err := New(e.st, Registry{"fake": e.fx}, Options{Heartbeat: 50 * time.Millisecond, Poll: 10 * time.Millisecond})
	if err != nil {
		e.t.Fatal(err)
	}
	return c
}

func (e *env) worktree(name string) string {
	wt := filepath.Join(e.root, name)
	sh(e.t, e.repo, "worktree", "add", "-q", "-b", name, wt)
	return wt
}

func (e *env) input(wt string, mut func(m map[string]any)) []byte {
	m := map[string]any{
		"project": map[string]any{"id": "demo", "name": "Demo"}, "repository": e.repo, "worktree": wt, "title": "T", "goal": "Do it",
		"scope": []any{"src"}, "acceptance": []any{"works"}, "context": map[string]any{"version": 1, "text": ctxText},
		"profiles": map[string]any{"developer": e.profiles["developer"], "reviewer": e.profiles["reviewer"], "polisher": e.profiles["polisher"]},
	}
	if mut != nil {
		mut(m)
	}
	b, _ := json.Marshal(m)
	return b
}

func (e *env) prepare(wt string, mut func(m map[string]any)) model.Task {
	e.t.Helper()
	task, err := e.c.Prepare(e.input(wt, mut))
	if err != nil {
		e.t.Fatal(err)
	}
	return task
}

func (e *env) exec(ids ...string) Result {
	e.t.Helper()
	res, err := e.c.Execute(context.Background(), ids, false, false)
	if err != nil {
		e.t.Fatal(err)
	}
	return res
}

func (e *env) state() *model.State {
	s, err := e.st.Read()
	if err != nil {
		e.t.Fatal(err)
	}
	return s
}

func taskOf(s *model.State, id string) model.Task { return *findTask(s, id) }

func want(t *testing.T, res Result, i int, state, reason string) {
	t.Helper()
	got := res.Tasks[i]
	if got.State != state || (reason != "" && deref(got.StateReason) != reason) {
		t.Fatalf("task %d = %s/%s, want %s/%s", i, got.State, deref(got.StateReason), state, reason)
	}
}

func TestNormalFlowAndRedaction(t *testing.T) {
	e := setup(t)
	wt := e.worktree("feat")
	task := e.prepare(wt, nil)
	res := e.exec(task.ID)
	want(t, res, 0, model.TaskDelivered, "")
	if got := strings.Join(e.fx.roles, ","); got != "developer,reviewer,polisher,reviewer" {
		t.Fatal(got)
	}
	s := e.state()
	states := []string{}
	for _, d := range s.Deliveries {
		states = append(states, d.State)
	}
	if strings.Join(states, ",") != "first,final_candidate,delivered" {
		t.Fatal(states)
	}
	head := sh(t, wt, "rev-parse", "HEAD")
	if deref(taskOf(s, task.ID).CandidateSha) != head || s.Deliveries[2].CandidateSha != head {
		t.Fatal("delivered candidate mismatch")
	}
	for _, r := range s.Runs {
		if r.AgentID != "Agent-01" || r.Usage == nil || r.ContextRef == nil || r.ModelSnapshot.Model != "m-1" || r.Metrics.WallSeconds == nil || len(r.Events) == 0 {
			t.Fatalf("run identity incomplete: %+v", r)
		}
	}
	snap, err := e.c.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if snap.SchemaVersion != 1 || snap.Controller.State != model.ControllerIdle || snap.Usage.KnownSubtotal != 400 || len(snap.Contexts) != 1 {
		t.Fatalf("snapshot: %+v", snap.Controller)
	}
	b, _ := json.Marshal(snap)
	for _, bad := range []string{ctxText, "FAKE_SECRET_ENV", "pi-private-cmd", e.profiles["developer"], "configFile", "authEnv", e.c.token, "\"pid\""} {
		if strings.Contains(string(b), bad) {
			t.Fatalf("snapshot leaks %q", bad)
		}
	}
}

func TestReviewFixAndCap(t *testing.T) {
	e := setup(t)
	e.fx.verdicts = []string{"changes_requested", "pass", "pass"}
	task := e.prepare(e.worktree("fix"), nil)
	want(t, e.exec(task.ID), 0, model.TaskDelivered, "")
	if got := strings.Join(e.fx.roles, ","); got != "developer,reviewer,developer,reviewer,polisher,reviewer" {
		t.Fatal(got)
	}
	var rounds []int
	for _, r := range runsOf(e.state(), task.ID) {
		if r.Metrics == nil || r.Metrics.FixRound == nil {
			t.Fatalf("fixRound unknown for run %s", r.Role)
		}
		rounds = append(rounds, *r.Metrics.FixRound)
	}
	if fmt.Sprint(rounds) != "[0 0 1 0 0 0]" {
		t.Fatalf("fix rounds %v", rounds)
	}
	if !strings.Contains(e.fx.reqs[2].TaskBrief, "fix it") {
		t.Fatal("fix brief lacks findings")
	}

	e.fx.roles = nil
	e.fx.verdicts = []string{"changes_requested", "changes_requested", "changes_requested"}
	t2 := e.prepare(e.worktree("cap"), func(m map[string]any) { m["budget"] = map[string]any{"maxFixRounds": 1} })
	want(t, e.exec(t2.ID), 0, model.TaskBlocked, "fix_rounds_exhausted")
	if got := strings.Join(e.fx.roles, ","); got != "developer,reviewer,developer,reviewer" {
		t.Fatal(got)
	}
}

func TestPolishChangeRecheck(t *testing.T) {
	e := setup(t)
	e.fx.polish = true
	wt := e.worktree("pol")
	task := e.prepare(wt, nil)
	want(t, e.exec(task.ID), 0, model.TaskDelivered, "")
	s := e.state()
	last := s.Reviews[len(s.Reviews)-1]
	if head := sh(t, wt, "rev-parse", "HEAD"); last.CandidateSha != head || deref(taskOf(s, task.ID).CandidateSha) != head || last.ContextDigest != task.ContextRef.Digest {
		t.Fatal("final review not on polished SHA")
	}
}

func TestViolations(t *testing.T) {
	cases := []struct {
		name   string
		set    func(f *fakeExec)
		reason string
	}{
		{"sha", func(f *fakeExec) { f.badSHA = true }, executor.CatReportStale},
		{"digest", func(f *fakeExec) { f.badDigest = true }, executor.CatReportStale},
		{"scope", func(f *fakeExec) { f.devPath = "other/x.txt" }, "scope_violation"},
		{"twocommits", func(f *fakeExec) { f.devTwo = true }, "commit_count"},
		{"reviewer", func(f *fakeExec) { f.revMutate = true }, executor.CatReviewerMutation},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := setup(t)
			tc.set(e.fx)
			task := e.prepare(e.worktree("v"), nil)
			want(t, e.exec(task.ID), 0, model.TaskFailed, tc.reason)
		})
	}
	t.Run("config", func(t *testing.T) {
		e := setup(t)
		task := e.prepare(e.worktree("cfg"), nil)
		os.WriteFile(e.profiles["reviewer"], []byte(`{"projectId":"demo","executor":"fake","provider":"prov","model":"other","authEnv":"FAKE_SECRET_ENV"}`), 0o600)
		if _, err := e.c.Execute(context.Background(), []string{task.ID}, false, false); err == nil || !strings.Contains(err.Error(), "profile_changed") {
			t.Fatal(err)
		}
	})
	t.Run("context", func(t *testing.T) {
		e := setup(t)
		task := e.prepare(e.worktree("ctx"), nil)
		// Tamper with stored context text via direct SQL: the frozen digest no longer matches.
		db := rawDB(t, e)
		var payload string
		db.QueryRow("SELECT payload FROM contexts").Scan(&payload)
		db.Exec("UPDATE contexts SET payload = ?", strings.Replace(payload, "PRIVATE", "CHANGED", 1))
		want(t, e.exec(task.ID), 0, model.TaskFailed, "context_changed")
	})
}

func rawDB(t *testing.T, e *env) *sql.DB {
	db, err := sql.Open("sqlite", "file:"+filepath.Join(e.data, "meerkat.db")+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestSchedulingParallelQueueDependencies(t *testing.T) {
	e := setup(t)
	e.fx.gate = make(chan struct{})
	a := e.prepare(e.worktree("wa"), nil)
	b := e.prepare(e.worktree("wb"), nil)
	go func() {
		deadline := time.Now().Add(5 * time.Second)
		for e.fx.active.Load() < 2 && time.Now().Before(deadline) {
			time.Sleep(5 * time.Millisecond)
		}
		close(e.fx.gate)
	}()
	res := e.exec(a.ID, b.ID)
	want(t, res, 0, model.TaskDelivered, "")
	want(t, res, 1, model.TaskDelivered, "")
	if e.fx.maxActive.Load() != 2 {
		t.Fatal("independent worktrees did not run concurrently")
	}

	// Same worktree: strictly serialized. Both tasks froze the same baseline at
	// prepare and c2 declares no dependency on c1, so c2 must NOT implicitly adopt
	// c1's delivered candidate; it fails until the coordinator prepares again.
	e.fx.gate, e.fx.maxActive = nil, atomic.Int32{}
	wt := e.worktree("same")
	c1 := e.prepare(wt, nil)
	c2 := e.prepare(wt, nil)
	res = e.exec(c1.ID, c2.ID)
	want(t, res, 0, model.TaskDelivered, "")
	want(t, res, 1, model.TaskFailed, "baseline_changed_requires_prepare")
	if e.fx.maxActive.Load() != 1 {
		t.Fatal("same worktree ran concurrently")
	}

	// Dependency failure blocks the downstream task.
	e.fx.devPath = "outside/x"
	up := e.prepare(e.worktree("up"), nil)
	down := e.prepare(e.worktree("down"), func(m map[string]any) { m["dependencies"] = []any{up.ID} })
	res = e.exec(up.ID, down.ID)
	want(t, res, 0, model.TaskFailed, "scope_violation")
	want(t, res, 1, model.TaskBlocked, "dependency_failed")
}

// runsOf returns the task's runs in start order.
func runsOf(s *model.State, taskID string) []model.Run {
	var out []model.Run
	for _, r := range s.Runs {
		if r.TaskID == taskID {
			out = append(out, r)
		}
	}
	at := func(r model.Run) time.Time { v, _ := time.Parse(time.RFC3339Nano, r.StartedAt); return v }
	slices.SortStableFunc(out, func(a, b model.Run) int { return at(a).Compare(at(b)) })
	return out
}

func TestQueueMetricsOneSlot(t *testing.T) {
	e := setup(t)
	one := 1
	if _, err := e.c.Settings(model.SettingsPatch{MaxConcurrency: &one}); err != nil {
		t.Fatal(err)
	}
	a := e.prepare(e.worktree("qa"), nil)
	b := e.prepare(e.worktree("qb"), nil)
	res := e.exec(a.ID, b.ID)
	want(t, res, 0, model.TaskDelivered, "")
	want(t, res, 1, model.TaskDelivered, "")
	st := e.state()
	ra, rb := runsOf(st, a.ID), runsOf(st, b.ID)
	if len(ra) == 0 || len(rb) == 0 {
		t.Fatal("missing runs")
	}
	ts := func(s string) time.Time {
		v, err := time.Parse(time.RFC3339Nano, s)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	first, waited := ra, rb
	if ts(rb[0].StartedAt).Before(ts(ra[0].StartedAt)) {
		first, waited = rb, ra
	}
	w := waited[0].Metrics
	if w == nil || w.QueueSeconds == nil || w.QueuedAt == nil || *w.QueueSeconds <= 0 {
		t.Fatalf("queued task has no positive queue wait: %+v", w)
	}
	// The single slot was held by the other task's whole pipeline: the wait covers it, and starts no
	// later than the first task's first run (both were queued together).
	lastEnd := ts(*first[len(first)-1].EndedAt)
	if q := ts(*w.QueuedAt); q.After(ts(first[0].StartedAt)) || q.Add(time.Duration(*w.QueueSeconds*float64(time.Second))).Before(lastEnd) {
		t.Fatalf("queue wait %v from %s does not cover slot holder ending %s", *w.QueueSeconds, *w.QueuedAt, lastEnd)
	}
	if fq := first[0].Metrics; fq == nil || fq.QueueSeconds == nil || *fq.QueueSeconds < 0 || *fq.QueueSeconds >= *w.QueueSeconds {
		t.Fatalf("first task queue %+v", fq)
	}
	for _, runs := range [][]model.Run{first, waited} {
		for i, r := range runs {
			m := r.Metrics
			if m == nil || m.FixRound == nil || *m.FixRound != 0 || m.ModelSeconds != nil || m.TestSeconds != nil || m.WallSeconds == nil {
				t.Fatalf("run %d metrics %+v", i, m)
			}
			if i > 0 && (m.QueueSeconds == nil || *m.QueueSeconds != 0 || m.QueuedAt == nil || *m.QueuedAt != r.StartedAt) {
				t.Fatalf("later role queue %+v", m)
			}
		}
	}
}

func TestInspectPreservesUnknown(t *testing.T) {
	e := setup(t)
	wt := e.worktree("insp")
	f := inspect(wt)
	if f.Err != nil || !f.Exists || f.Branch != "insp" || !f.Clean {
		t.Fatalf("facts %+v", f)
	}
	// A tag with the same name must not change the branch reading (no --short disambiguation).
	sh(t, e.repo, "tag", "insp")
	if f := inspect(wt); f.Err != nil || f.Branch != "insp" {
		t.Fatalf("ambiguous facts %+v", f)
	}
	sh(t, wt, "checkout", "-q", "--detach")
	if f := inspect(wt); f.Err != nil || !f.Exists || f.Branch != "" {
		t.Fatalf("detached facts %+v", f)
	}
	if f := inspect(filepath.Join(e.root, "nope")); f.Err != nil || f.Exists {
		t.Fatalf("missing facts %+v", f)
	}
	// Git failing to answer is unknown (Err), never a determined branch/dirty fact.
	plain := filepath.Join(e.root, "plain")
	os.Mkdir(plain, 0o755)
	t.Setenv("GIT_CEILING_DIRECTORIES", e.root)
	f = inspect(plain)
	var ge *gitError
	if f.Exists || !errors.As(f.Err, &ge) || ge.Code != 128 || ge.Stderr == "" {
		t.Fatalf("unknown facts %+v", f)
	}
	if _, why := verifyRole("developer", model.Task{}, "", f, executor.Result{Report: &executor.Report{Summary: "s", Checks: []executor.Check{}, KnownGaps: []string{}}}); why != inspectFailed {
		t.Fatalf("verify reason %q", why)
	}
}

func waitRun(t *testing.T, e *env, state string) model.Run {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, r := range e.state().Runs {
			if r.State == state {
				return r
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("run never reached " + state)
	return model.Run{}
}

func TestStopReceiptAndBudget(t *testing.T) {
	e := setup(t)
	e.fx.block = true
	task := e.prepare(e.worktree("stop"), nil)
	done := make(chan Result)
	go func() { r, _ := e.c.Execute(context.Background(), []string{task.ID}, false, false); done <- r }()
	run := waitRun(t, e, model.RunRunning)
	if _, err := e.c.Execute(context.Background(), []string{task.ID}, false, false); !errors.Is(err, ErrBusy) {
		t.Fatal("overlapping dispatch not rejected", err)
	}
	reqID := "11111111-2222-4333-8444-555555555555"
	rc, err := e.c.Stop(run.ID, reqID)
	if err != nil || !rc.Accepted || rc.State != model.StopPending || rc.Outcome != nil {
		t.Fatal(rc, err)
	}
	res := <-done
	want(t, res, 0, model.TaskStopped, "stop_requested")
	rc, _ = e.st.RequestStop(run.ID, reqID)
	if !rc.Duplicate || rc.State != model.StopProcessed || deref(rc.Outcome) != model.RunStopped {
		t.Fatalf("receipt %+v", rc)
	}

	e.fx.block = false
	b := e.prepare(e.worktree("budget"), func(m map[string]any) { m["budget"] = map[string]any{"maxTokens": 150} })
	want(t, e.exec(b.ID), 0, model.TaskStopped, "budget_tokens")
	last := e.fx.reqs[len(e.fx.reqs)-1]
	if last.Role != "reviewer" || last.RemainingTokens != 50 {
		t.Fatalf("budget cap not applied: %s %d", last.Role, last.RemainingTokens)
	}
}

func TestConcurrentCloseExecute(t *testing.T) {
	e := setup(t)
	task := e.prepare(e.worktree("race"), nil)
	e.c.Close()
	for i := 0; i < 20; i++ {
		c := e.newCore()
		errc := make(chan error, 1)
		start := make(chan struct{})
		go func() { <-start; _, err := c.Execute(context.Background(), []string{task.ID}, true, true); errc <- err }()
		close(start)
		if err := c.Close(); err != nil {
			t.Fatal(err)
		}
		if err := <-errc; err != nil && !errors.Is(err, ErrClosed) {
			t.Fatalf("iteration %d: %v", i, err)
		}
		if _, err := c.Execute(context.Background(), []string{task.ID}, false, false); !errors.Is(err, ErrClosed) {
			t.Fatalf("execute after close: %v", err)
		}
	}
	e.c = e.newCore()
}

func TestUnknownResume(t *testing.T) {
	e := setup(t)
	wt := e.worktree("unk")
	task := e.prepare(wt, nil)
	other := e.prepare(wt, nil)
	runID := newUUID()
	live := os.Getpid()
	err := e.c.update(func(s *model.State) error {
		s.Runs = append(s.Runs, model.Run{ID: runID, TaskID: task.ID, Role: "developer", State: model.RunRunning, PID: &live, Host: e.c.host,
			StartedAt: now(), UpdatedAt: now(), Summary: mustJSON(runSummary{Purpose: "implement", Limits: limits{MaxTokens: 10, MaxWallSeconds: 10}})})
		findTask(s, task.ID).State = model.TaskImplementing
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	e.c.Close()
	e.c = e.newCore()
	s := e.state()
	if taskOf(s, task.ID).State != model.TaskUnknown || findRun(s, runID).State != model.RunUnknown {
		t.Fatal("not reconciled to unknown")
	}
	for _, tc := range []struct {
		ids         []string
		resume, ack bool
		msg         string
	}{
		{[]string{other.ID}, false, false, "occupied"},
		{[]string{task.ID}, false, false, "resume"},
		{[]string{task.ID}, true, false, "acknowledge"},
		{[]string{task.ID}, true, true, "alive"},
	} {
		if _, err := e.c.Execute(context.Background(), tc.ids, tc.resume, tc.ack); err == nil || !strings.Contains(err.Error(), tc.msg) {
			t.Fatalf("want %q, got %v", tc.msg, err)
		}
	}
	dead := exec.Command("true")
	dead.Run()
	pid := dead.Process.Pid
	e.c.update(func(s *model.State) error { findRun(s, runID).PID = &pid; return nil })
	res, err := e.c.Execute(context.Background(), []string{task.ID}, true, true)
	if err != nil {
		t.Fatal(err)
	}
	want(t, res, 0, model.TaskDelivered, "")
	if findRun(e.state(), runID).State != model.RunInterrupted {
		t.Fatal("unknown run not acknowledged as interrupted")
	}
}

func TestForeignOwnerFencing(t *testing.T) {
	e := setup(t)
	e.fx.block = true
	task := e.prepare(e.worktree("fence"), nil)
	done := make(chan Result)
	go func() { r, _ := e.c.Execute(context.Background(), []string{task.ID}, false, false); done <- r }()
	waitRun(t, e, model.RunRunning)
	if _, err := rawDB(t, e).Exec("UPDATE controller_lease SET token = 'foreign'"); err != nil {
		t.Fatal(err)
	}
	res := <-done
	if res.Fatal != "controller_lost" {
		t.Fatalf("%+v", res)
	}
	if _, err := e.c.Prepare(e.input(e.worktree("fence2"), nil)); !errors.Is(err, store.ErrLeaseLost) {
		t.Fatal(err)
	}
	s := e.state()
	if len(s.Tasks) != 1 || s.Runs[0].State != model.RunRunning {
		t.Fatal("write after lease loss")
	}
	snap, err := e.c.Snapshot()
	if err != nil || snap.Controller.State != model.ControllerUnknown || snap.Runs[0].State != model.RunUnknown || snap.Tasks[0].State != model.TaskUnknown {
		t.Fatalf("snapshot %+v %v", snap.Controller, err)
	}
	if _, err := e.c.Settings(model.SettingsPatch{}); !errors.Is(err, store.ErrLeaseLost) {
		t.Fatal(err)
	}
}

func TestPrepareValidationAndSettings(t *testing.T) {
	e := setup(t)
	wt := e.worktree("prep")
	bad := map[string]func(m map[string]any){
		"unknown key": func(m map[string]any) { m["extra"] = 1 },
		"glob":        func(m map[string]any) { m["scope"] = []any{"src/*"} },
		"traversal":   func(m map[string]any) { m["scope"] = []any{"../x"} },
		"secret":      func(m map[string]any) { m["scope"] = []any{"config/.env"} },
		"credential":  func(m map[string]any) { m["goal"] = "use sk-ant-abcdefghijklmnopqrstuvwxyz123" },
		"primary":     func(m map[string]any) { m["worktree"] = e.repo },
		"missing dep": func(m map[string]any) { m["dependencies"] = []any{newUUID()} },
		"issue query": func(m map[string]any) {
			m["issueRef"] = map[string]any{"url": "https://github.com/o/r/issues/1?token=x", "title": "i"}
		},
		"relative cfg":  func(m map[string]any) { m["profiles"].(map[string]any)["developer"] = "dev.json" },
		"bad changeId":  func(m map[string]any) { m["changeId"] = "../x" },
		"long title":    func(m map[string]any) { m["title"] = strings.Repeat("x", 201) },
		"nested extras": func(m map[string]any) { m["context"].(map[string]any)["secret"] = "x" },
	}
	for name, mut := range bad {
		if _, err := e.c.Prepare(e.input(wt, mut)); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if _, err := e.c.Prepare(append(e.input(wt, nil), make([]byte, MaxInput)...)); err == nil {
		t.Error("oversize accepted")
	}
	os.WriteFile(filepath.Join(wt, "dirty"), []byte("x"), 0o644)
	if _, err := e.c.Prepare(e.input(wt, nil)); err == nil {
		t.Error("dirty accepted")
	}
	os.Remove(filepath.Join(wt, "dirty"))
	sh(t, wt, "checkout", "-q", "-b", "master")
	if _, err := e.c.Prepare(e.input(wt, nil)); err == nil {
		t.Error("protected branch accepted")
	}
	sh(t, wt, "checkout", "-q", "prep")

	task := e.prepare(wt, func(m map[string]any) {
		m["changeId"] = "chg-1"
		m["context"].(map[string]any)["sources"] = []any{map[string]any{"url": "https://ex.com/doc?sig=abc#f", "title": "d"}}
		m["issueRef"] = map[string]any{"url": "https://GitHub.com/o/r/issues/7", "title": "i"}
	})
	s := e.state()
	if c := s.Contexts[0]; c.Sources[0].URL != "https://ex.com/doc" || deref(task.ChangeID) != "chg-1" || task.IssueRef.URL != "https://github.com/o/r/issues/7" {
		t.Fatalf("normalization: %+v", c.Sources)
	}
	if _, err := e.c.Prepare(e.input(wt, func(m map[string]any) {
		m["context"] = map[string]any{"id": task.ContextRef.ID, "version": 1, "text": "different"}
	})); err == nil {
		t.Error("context version mutation accepted")
	}

	five := 5
	if _, err := e.c.Settings(model.SettingsPatch{MaxConcurrency: &five}); err == nil {
		t.Error("maxConcurrency 5 accepted")
	}
	if _, err := e.c.Settings(model.SettingsPatch{DefaultProfiles: map[string]map[string]string{"demo": {"developer": task.ProfileIDs["reviewer"]}}}); err == nil {
		t.Error("mismatched role profile accepted")
	}
	one := 1
	set, err := e.c.Settings(model.SettingsPatch{MaxConcurrency: &one, DefaultProfiles: map[string]map[string]string{"demo": {"developer": task.ProfileIDs["developer"]}}})
	if err != nil || set.MaxConcurrency != 1 {
		t.Fatal(err)
	}
	if _, err := ParseSettingsPatch([]byte(`{"maxConcurrency":2,"x":1}`)); err == nil {
		t.Error("unknown settings key accepted")
	}
}

func TestDelegateRunsOnlyDeveloper(t *testing.T) {
	e := setup(t)
	wt := e.worktree("dlg")
	res, err := e.c.Delegate(context.Background(), e.input(wt, nil))
	if err != nil {
		t.Fatal(err)
	}
	if res.Mode != "delegate" || len(res.Tasks) != 1 {
		t.Fatalf("result: %+v", res)
	}
	want(t, res, 0, model.TaskFirstDelivery, DelegateCandidate)
	if got := strings.Join(e.fx.roles, ","); got != "developer" {
		t.Fatal("roles", got)
	}
	s := e.state()
	tk := taskOf(s, res.Tasks[0].ID)
	if tk.Origin != OriginDelegate || deref(tk.CandidateSha) != sh(t, wt, "rev-parse", "HEAD") || tk.ResumeRole != nil {
		t.Fatalf("task: %+v", tk)
	}
	if len(s.Reviews) != 0 || len(s.Deliveries) != 1 || s.Deliveries[0].State != "first" || len(s.Runs) != 1 || s.Runs[0].Usage == nil {
		t.Fatal("expected one run, one candidate delivery, no review")
	}
	// Never silently continued through the review flow.
	if _, err := e.c.Execute(context.Background(), []string{tk.ID}, true, true); err == nil {
		t.Fatal("execute accepted a delegated task")
	}
	snap, err := e.c.Snapshot()
	if err != nil || snap.Tasks[0].State != model.TaskFirstDelivery || snap.Tasks[0].Usage.KnownSubtotal != 100 {
		t.Fatalf("snapshot: %v %+v", err, snap.Tasks)
	}
	// Restart keeps the settled candidate (no reconcile to stopped).
	e.c.Close()
	e.c = e.newCore()
	if got := taskOf(e.state(), tk.ID); !isDelegateCandidate(got) {
		t.Fatalf("after restart: %s/%s", got.State, deref(got.StateReason))
	}
	if len(e.fx.roles) != 1 {
		t.Fatal("extra executor invocations")
	}
}

// A settled delegate candidate releases its worktree: a second bounded delegate in the same clean linked worktree
// succeeds and both candidate tasks and deliveries stay in history.
func TestSequentialDelegatesReuseWorktree(t *testing.T) {
	e := setup(t)
	wt := e.worktree("seq")
	first, err := e.c.Delegate(context.Background(), e.input(wt, nil))
	if err != nil {
		t.Fatal(err)
	}
	want(t, first, 0, model.TaskFirstDelivery, DelegateCandidate)
	sha1 := sh(t, wt, "rev-parse", "HEAD")
	second, err := e.c.Delegate(context.Background(), e.input(wt, func(m map[string]any) { m["title"] = "T2" }))
	if err != nil {
		t.Fatalf("second delegate in same worktree: %v", err)
	}
	want(t, second, 0, model.TaskFirstDelivery, DelegateCandidate)
	sha2 := sh(t, wt, "rev-parse", "HEAD")
	if second.Tasks[0].ID == first.Tasks[0].ID || sha1 == sha2 {
		t.Fatal("second delegate did not produce a new candidate")
	}
	if got := strings.Join(e.fx.roles, ","); got != "developer,developer" {
		t.Fatal("roles", got)
	}
	s := e.state()
	t1, t2 := taskOf(s, first.Tasks[0].ID), taskOf(s, second.Tasks[0].ID)
	if !isDelegateCandidate(t1) || !isDelegateCandidate(t2) || deref(t1.CandidateSha) != sha1 || deref(t2.CandidateSha) != sha2 {
		t.Fatalf("tasks: %+v %+v", t1, t2)
	}
	if len(s.Deliveries) != 2 || len(s.Runs) != 2 || len(s.Reviews) != 0 {
		t.Fatalf("history: %d deliveries %d runs %d reviews", len(s.Deliveries), len(s.Runs), len(s.Reviews))
	}
	got := map[string]string{}
	for _, d := range s.Deliveries {
		if d.State != "first" {
			t.Fatalf("delivery state %s", d.State)
		}
		got[d.TaskID] = d.CandidateSha
	}
	if got[t1.ID] != sha1 || got[t2.ID] != sha2 {
		t.Fatalf("deliveries: %v", got)
	}
	// A regular managed task in the same worktree is still allowed only while no other active task holds it.
	managed := e.prepare(wt, func(m map[string]any) { m["title"] = "M" })
	if managed.State == "" {
		t.Fatal("managed task not prepared")
	}
}

func TestDryPreparePure(t *testing.T) {
	e := setup(t)
	wt := e.worktree("dry")
	before, _ := json.Marshal(e.state())
	d, err := e.c.DryPrepare(e.input(wt, nil))
	if err != nil {
		t.Fatal(err)
	}
	if !d.Valid || d.Branch != "dry" || d.Head != sh(t, wt, "rev-parse", "HEAD") || d.Profiles["developer"].Model != "m-1" {
		t.Fatalf("dry: %+v", d)
	}
	b, _ := json.Marshal(d)
	for _, bad := range []string{ctxText, "FAKE_SECRET_ENV", "pi-private-cmd", e.profiles["developer"]} {
		if strings.Contains(string(b), bad) {
			t.Fatalf("dry run leaks %q", bad)
		}
	}
	// Invalid profile and dirty worktree are refused.
	if _, err := e.c.DryPrepare(e.input(wt, func(m map[string]any) {
		m["profiles"].(map[string]any)["reviewer"] = filepath.Join(e.root, "missing.json")
	})); err == nil {
		t.Fatal("missing profile accepted")
	}
	os.WriteFile(filepath.Join(wt, "dirt"), []byte("x"), 0o644)
	if _, err := e.c.DryPrepare(e.input(wt, nil)); err == nil || !strings.Contains(err.Error(), "clean") {
		t.Fatalf("dirty worktree: %v", err)
	}
	if _, err := e.c.DryPrepare(e.input(e.repo, nil)); err == nil {
		t.Fatal("primary worktree accepted")
	}
	after, _ := json.Marshal(e.state())
	if string(before) != string(after) || len(e.fx.roles) != 0 {
		t.Fatal("dry run changed state or spawned an executor")
	}
}
