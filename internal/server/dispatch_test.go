package server

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hunknownz/Meerkat/internal/core"
	"github.com/hunknownz/Meerkat/internal/executor"
	"github.com/hunknownz/Meerkat/internal/model"
	"github.com/hunknownz/Meerkat/internal/store"
)

type blockingExecutor struct{}

func (blockingExecutor) Name() string                 { return "fixture" }
func (blockingExecutor) Validate(model.Profile) error { return nil }
func (blockingExecutor) Execute(ctx context.Context, req executor.Request, event func(model.RunEvent), start func(executor.Process)) (executor.Result, error) {
	p := executor.Process{Executor: "fixture", Host: "test", StartedAt: time.Now()}
	start(p)
	<-ctx.Done()
	return executor.Result{RunID: req.RunID, Role: req.Role, Executor: "fixture", Process: &p, Outcome: model.RunStopped, Category: executor.CatCanceled, StartedAt: p.StartedAt, EndedAt: time.Now()}, &executor.Error{Category: executor.CatCanceled}
}

func TestDurableDispatchOverSocketLostReplyWaitAndStop(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	repo, wt := filepath.Join(root, "repo"), filepath.Join(root, "worktree")
	os.Mkdir(repo, 0o755)
	git := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.invalid", "GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.invalid")
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatal(err, string(b))
		}
	}
	git(repo, "init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(repo, "README"), []byte("fixture"), 0o644)
	git(repo, "add", "README")
	git(repo, "commit", "-qm", "initial")
	git(repo, "worktree", "add", "-q", "-b", "codex/fixture", wt)
	profiles := map[string]string{}
	for _, role := range model.Roles {
		p := filepath.Join(root, role+".json")
		os.WriteFile(p, []byte(`{"projectId":"example","executor":"fixture","provider":"fixture","model":"fixture","authEnv":"FIXTURE_KEY","piCommand":["fixture"],"instructions":[],"limits":{"maxTokens":1000,"maxWallSeconds":60}}`), 0o600)
		profiles[role] = p
	}
	// Darwin's sockaddr_un path limit is shorter than a named t.TempDir path.
	st, err := store.Open(privDir(t))
	if err != nil {
		t.Fatal(err)
	}
	c, err := core.New(st, core.Registry{"fixture": blockingExecutor{}}, core.Options{Poll: 10 * time.Millisecond})
	if err != nil {
		st.Close()
		t.Fatal(err)
	}
	svc, err := New(c, st, nil)
	if err != nil {
		c.Close()
		st.Close()
		t.Fatal(err)
	}
	ln, err := ListenUnix(st.DataDir())
	if err != nil {
		svc.Shutdown()
		st.Close()
		t.Fatal(err)
	}
	go svc.ServeUnix(ln)
	t.Cleanup(func() { ln.Close(); svc.Shutdown(); st.Close() })
	input, _ := json.Marshal(map[string]any{"project": map[string]any{"id": "example", "name": "Example"}, "repository": repo, "worktree": wt, "title": "Bounded fixture", "goal": "Check cancellation only", "scope": []string{"README"}, "acceptance": []string{"Cancellation preserves unknown usage"}, "context": map[string]any{"version": 1, "text": "private fixture context"}, "profiles": profiles})
	task, err := c.Prepare(input)
	if err != nil {
		t.Fatal(err)
	}
	rid := "20000000-0000-4000-8000-000000000001"
	conn, err := net.Dial("unix", SocketPath(st.DataDir()))
	if err != nil {
		t.Fatal(err)
	}
	json.NewEncoder(conn).Encode(Request{Op: "dispatch", Tasks: []string{task.ID}, RequestID: rid})
	conn.(*net.UnixConn).CloseWrite()
	conn.Close() // lose the first receipt, with no cancellation of daemon work
	var op model.Operation
	until := time.Now().Add(5 * time.Second)
	for time.Now().Before(until) {
		op, err = c.OperationByRequest(rid)
		if err == nil && op.Tasks[0].TaskState == model.TaskImplementing {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil || op.Tasks[0].TaskState != model.TaskImplementing {
		t.Fatal(op, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	reply, err := Call(ctx, st.DataDir(), Request{Op: "operation", RequestID: rid})
	if err != nil || !reply.OK {
		t.Fatal(reply, err)
	}
	var recovered model.Operation
	if json.Unmarshal(reply.Data, &recovered) != nil || recovered.ID != op.ID {
		t.Fatal("receipt recovery mismatch")
	}
	reply, err = Call(ctx, st.DataDir(), Request{Op: "wait-operation", OperationID: op.ID, WaitMillis: 20})
	if err != nil || !reply.OK || json.Unmarshal(reply.Data, &recovered) != nil || recovered.State != model.OperationRunning {
		t.Fatal(reply, err)
	}
	snap, _ := c.Snapshot()
	if len(snap.Runs) != 1 || snap.Runs[0].State != model.RunRunning {
		t.Fatal("disconnect stopped execution")
	}
	resp, err := Call(ctx, st.DataDir(), Request{Op: "stop", RunID: snap.Runs[0].ID, RequestID: newUUID()})
	if err != nil || !resp.OK {
		t.Fatal(resp, err)
	}
	reply, err = Call(ctx, st.DataDir(), Request{Op: "wait-operation", OperationID: op.ID, WaitMillis: 3000})
	if err != nil || !reply.OK || json.Unmarshal(reply.Data, &recovered) != nil || recovered.State != model.OperationCompleted || recovered.Tasks[0].TaskState != model.TaskStopped {
		t.Fatal(reply, err)
	}
	if strings.Contains(string(reply.Data), "private fixture context") || strings.Contains(string(reply.Data), root) {
		t.Fatal("private data in operation")
	}
	if _, err := Call(ctx, st.DataDir(), Request{Op: "dispatch", RequestID: rid, Tasks: []string{task.ID}}); err != nil {
		t.Fatal("receipt replay failed", err)
	}
}
