package core

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hunknownz/Meerkat/internal/executor"
	"github.com/hunknownz/Meerkat/internal/model"
)

type statefulFake struct {
	*fakeExec
	unconfirmed bool
}

func (f *statefulFake) Capabilities() executor.Capabilities {
	return executor.Capabilities{Protocol: "fixture", PersistentSessions: true}
}
func (f *statefulFake) InitializeSession(b executor.SessionBinding) (executor.SessionSnapshot, error) {
	return executor.NewPi().InitializeSession(b)
}
func (f *statefulFake) InspectSession(b executor.SessionBinding) (executor.SessionSnapshot, error) {
	return executor.NewPi().InspectSession(b)
}
func (f *statefulFake) Execute(ctx context.Context, req executor.Request, onEvent func(model.RunEvent), onStart func(executor.Process)) (executor.Result, error) {
	if req.Session == nil {
		return executor.Result{}, fmt.Errorf("missing session")
	}
	snap, err := f.InspectSession(*req.Session)
	if err != nil || snap.Digest != req.Session.Digest {
		return executor.Result{}, fmt.Errorf("session mismatch")
	}
	r, err := f.fakeExec.Execute(ctx, req, onEvent, onStart)
	file, e := os.OpenFile(req.Session.File, os.O_WRONLY|os.O_APPEND, 0o600)
	if e != nil {
		return r, e
	}
	fmt.Fprintf(file, "{\"type\":\"message\",\"id\":%q,\"message\":{\"role\":\"assistant\",\"content\":\"private fixture history\"}}\n", req.RunID)
	file.Close()
	snap, e = f.InspectSession(*req.Session)
	r.Session = &executor.SessionOutcome{ID: req.Session.ID, ProviderID: snap.ProviderID, Digest: snap.Digest, Confirmed: e == nil && !f.unconfirmed}
	return r, err
}

func useSessions(t *testing.T, e *env) *statefulFake {
	t.Helper()
	e.c.Close()
	f := &statefulFake{fakeExec: e.fx}
	c, err := New(e.st, Registry{"fake": f}, Options{Poll: e.c.opts.Poll, Heartbeat: e.c.opts.Heartbeat})
	if err != nil {
		t.Fatal(err)
	}
	e.c = c
	return f
}

func TestSessionDirectoryParallelSetup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions")
	start := make(chan struct{})
	results := make(chan error, 64)
	for i := 0; i < cap(results); i++ {
		go func() { <-start; results <- privateSessionDir(path, true) }()
	}
	close(start)
	for i := 0; i < cap(results); i++ {
		if err := <-results; err != nil {
			t.Fatal("parallel directory initialization", err)
		}
	}
}

func TestSessionParallelTasksKeepHistoryIsolated(t *testing.T) {
	e := setup(t)
	useSessions(t, e)
	e.fx.gate = make(chan struct{})
	a := e.prepare(e.worktree("session-parallel-a"), nil)
	b := e.prepare(e.worktree("session-parallel-b"), nil)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	type outcome struct {
		result Result
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		r, err := e.c.Execute(ctx, []string{a.ID, b.ID}, false, false)
		done <- outcome{r, err}
	}()
	deadline := time.Now().Add(5 * time.Second)
	for e.fx.maxActive.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	close(e.fx.gate)
	got := <-done
	if got.err != nil || len(got.result.Tasks) != 2 || e.fx.maxActive.Load() < 2 {
		t.Fatal("session tasks did not run concurrently", got, e.fx.maxActive.Load())
	}
	for _, tt := range got.result.Tasks {
		if tt.State != model.TaskDelivered {
			t.Fatal(got.result)
		}
	}
	owners := map[string]string{}
	for _, req := range e.fx.reqs {
		ss, err := e.st.SessionForRun(req.RunID)
		if err != nil || ss.State != model.SessionIdle || req.Session == nil || ss.ID != req.Session.ID {
			t.Fatal("session binding", ss, err)
		}
		if prev, ok := owners[ss.ID]; ok && prev != ss.TaskID {
			t.Fatal("different tasks shared history")
		}
		owners[ss.ID] = ss.TaskID
	}
	if len(owners) != len(e.fx.reqs) || len(owners) < 4 {
		t.Fatal("task/role history was not independent", owners)
	}
}

func TestSessionDevelopmentFixReuseIndependentReviews(t *testing.T) {
	e := setup(t)
	useSessions(t, e)
	e.fx.verdicts = []string{"changes_requested", "pass", "pass"}
	e.fx.polish = true
	task := e.prepare(e.worktree("session-flow"), nil)
	res := e.exec(task.ID)
	if res.Tasks[0].State != model.TaskDelivered {
		t.Fatal(res)
	}
	e.fx.mu.Lock()
	reqs := append([]executor.Request{}, e.fx.reqs...)
	e.fx.mu.Unlock()
	dev, review := []string{}, []string{}
	for _, r := range reqs {
		ss, err := e.st.SessionForRun(r.RunID)
		if err != nil || ss.State != model.SessionIdle || ss.ActiveRunID != nil || r.Session == nil || ss.ID != r.Session.ID {
			t.Fatal(ss, err)
		}
		if r.Role == "developer" {
			dev = append(dev, ss.ID)
		}
		if r.Role == "reviewer" {
			review = append(review, ss.ID)
		}
	}
	if len(dev) != 2 || dev[0] != dev[1] || len(review) != 3 || review[0] == review[1] || review[1] == review[2] || review[0] == dev[0] {
		t.Fatal("role history isolation", dev, review)
	}
	snap, err := e.c.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(snap)
	if strings.Contains(string(b), "history.jsonl") || strings.Contains(string(b), "private fixture history") {
		t.Fatal("private sessions in public snapshot")
	}
	for _, r := range e.state().Runs {
		if r.Usage == nil || r.Usage.Tokens.Total == nil || *r.Usage.Tokens.Total != 100 {
			t.Fatal("run usage changed", r.Usage)
		}
	}
}

func TestSessionUnknownNeverAutomaticallyResumes(t *testing.T) {
	e := setup(t)
	f := useSessions(t, e)
	f.unconfirmed = true
	task := e.prepare(e.worktree("session-unknown"), nil)
	r := e.exec(task.ID)
	if r.Tasks[0].State != model.TaskUnknown {
		t.Fatal(r)
	}
	ss, err := e.st.SessionForRun(e.state().Runs[0].ID)
	if err != nil || ss.State != model.SessionUnknown {
		t.Fatal(ss, err)
	}
	if _, _, err := e.c.selectSession(e.state(), *findTask(e.state(), task.ID), e.fx.reqs[0].Profile, "developer", ss.LastSHA); err == nil {
		t.Fatal("unknown history reused")
	}
}

func TestSessionControllerRestartDoesNotReplayOwnedHistory(t *testing.T) {
	e := setup(t)
	f := useSessions(t, e)
	task := e.prepare(e.worktree("session-restart"), nil)
	st := e.state()
	prof := st.Profiles[0]
	for _, p := range st.Profiles {
		if p.ID == task.ProfileIDs["developer"] {
			prof = p
		}
	}
	ss, fresh, err := e.c.selectSession(st, task, prof, "developer", deref(task.BaselineSha))
	if err != nil || ss == nil || !fresh {
		t.Fatal(ss, err)
	}
	rid, at := newUUID(), now()
	if err := e.c.startRoleRecord(ss, rid, fresh, func(st *model.State) error {
		st.Runs = append(st.Runs, model.Run{ID: rid, TaskID: task.ID, State: model.RunStarting, Role: "developer", UpdatedAt: at})
		tt := findTask(st, task.ID)
		tt.State, tt.UpdatedAt = model.TaskImplementing, at
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	e.c.Close()
	c, err := New(e.st, Registry{"fake": f}, Options{Poll: e.c.opts.Poll, Heartbeat: e.c.opts.Heartbeat})
	if err != nil {
		t.Fatal(err)
	}
	e.c = c
	st = e.state()
	got, err := e.st.SessionForRun(rid)
	tt, run := findTask(st, task.ID), findRun(st, rid)
	if err != nil || got.State != model.SessionUnknown || got.ActiveRunID == nil || *got.ActiveRunID != rid || tt.State != model.TaskUnknown || tt.RecordedState != model.TaskImplementing || run.State != model.RunUnknown {
		t.Fatal("restart lost the unresolved claim", got, tt, run, err)
	}
	if len(e.fx.reqs) != 0 {
		t.Fatal("restart replayed a claimed run")
	}
	if _, _, err := e.c.selectSession(st, *tt, prof, "developer", got.LastSHA); err == nil {
		t.Fatal("unverified history became resumable")
	}
}

func TestSessionDevelopmentHistoryAfterVerifiedPolish(t *testing.T) {
	e := setup(t)
	useSessions(t, e)
	e.fx.verdicts = []string{"pass", "changes_requested", "pass"}
	e.fx.polish = true
	task := e.prepare(e.worktree("session-post-polish-fix"), nil)
	r := e.exec(task.ID)
	if r.Tasks[0].State != model.TaskDelivered {
		t.Fatal(r)
	}
	dev := []string{}
	for _, req := range e.fx.reqs {
		if req.Role == "developer" {
			dev = append(dev, req.Session.ID)
		}
	}
	if len(dev) != 2 || dev[0] != dev[1] {
		t.Fatal("verified polish broke development history", dev)
	}
}

func TestSessionFrozenProfileSHAAndFileMismatch(t *testing.T) {
	e := setup(t)
	useSessions(t, e)
	task := e.prepare(e.worktree("session-bindings"), nil)
	e.exec(task.ID)
	st := e.state()
	task = *findTask(st, task.ID)
	prof := e.fx.reqs[0].Profile
	all, _ := e.st.SessionsForTask(task.ID)
	ss := all[0]
	for _, tc := range []struct {
		name   string
		mutate func(*model.Task, *model.Profile, *string)
	}{
		{"goal", func(t *model.Task, p *model.Profile, s *string) { t.Goal = "changed" }},
		{"profile", func(t *model.Task, p *model.Profile, s *string) { p.Model = "different" }},
		{"sha", func(t *model.Task, p *model.Profile, s *string) { *s = strings.Repeat("f", 40) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tt, pp, sha := task, prof, ss.LastSHA
			tc.mutate(&tt, &pp, &sha)
			if _, _, err := e.c.selectSession(st, tt, pp, "developer", sha); err == nil {
				t.Fatal("mismatched history reused")
			}
		})
	}
	f, _ := os.OpenFile(e.c.sessionBinding(ss, task.Worktree).File, os.O_WRONLY|os.O_APPEND, 0o600)
	f.WriteString("{}\n")
	f.Close()
	if _, _, err := e.c.selectSession(st, task, prof, "developer", ss.LastSHA); err == nil {
		t.Fatal("changed private file reused")
	}
}
