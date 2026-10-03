package core

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/hunknownz/Meerkat/internal/model"
	"github.com/hunknownz/Meerkat/internal/store"
)

func exhaustedTask(t *testing.T, e *env) model.Task {
	t.Helper()
	task := e.prepare(e.worktree("exhausted"), func(m map[string]any) {
		m["budget"] = map[string]any{"maxTokens": 100, "maxWallSeconds": 1800, "maxFixRounds": 1}
	})
	r := e.exec(task.ID)
	if r.Tasks[0].State != model.TaskPaused {
		t.Fatal(r)
	}
	return task
}
func proposedDecision(t *testing.T, e *env, task model.Task) model.BudgetDecisionInput {
	t.Helper()
	p, err := e.c.ProposeBudget(model.BudgetIncrease{TaskID: task.ID, AddTokens: 950, AddWallSeconds: 100, Reason: "Finish the bounded original task"})
	if err != nil {
		t.Fatal(err)
	}
	return model.BudgetDecisionInput{Proposal: p, RequestID: newUUID(), AuthorizationRef: "EXPLICIT_TEST_AUTHORIZATION_PRIVATE", Apply: true}
}
func TestBudgetDecisionContinuesExhaustedCheckpointWithoutReset(t *testing.T) {
	e, f := checkpointEnv(t)
	task := exhaustedTask(t, e)
	before := taskContract(e.state(), task)
	if _, err := e.c.Execute(context.Background(), []string{task.ID}, true, false); err == nil {
		t.Fatal("exhausted allowance resumed")
	}
	in := proposedDecision(t, e, task)
	v, _ := e.st.BudgetAuthorization(task.ID)
	if v.Revision != 0 || f.calls != 1 {
		t.Fatal("proposal granted or executed", v)
	}
	bad := in
	bad.Apply = false
	if _, err := e.c.ApplyBudgetDecision(bad); err == nil {
		t.Fatal("implicit authorization accepted")
	}
	rc, err := e.c.ApplyBudgetDecision(in)
	if err != nil || rc.Revision != 1 {
		t.Fatal(rc, err)
	}
	if f.calls != 1 || findTask(e.state(), task.ID).State != model.TaskPaused {
		t.Fatal("grant auto-resumed task")
	}
	v, _ = e.st.BudgetAuthorization(task.ID)
	if v.OriginalTokens != 100 || v.AddedTokens != 950 || v.AuthorizedTokens != 1050 {
		t.Fatal(v)
	}
	if after := taskContract(e.state(), *findTask(e.state(), task.ID)); after != before {
		t.Fatal("frozen contract overwritten")
	}
	r, err := e.c.Execute(context.Background(), []string{task.ID}, true, false)
	if err != nil || r.Tasks[0].State != model.TaskDelivered {
		t.Fatal(r, err)
	}
	if e.fx.reqs[0].RemainingTokens != 950 {
		t.Fatal("original use not charged", e.fx.reqs[0].RemainingTokens)
	}
	u, _ := budgetUse(e.state(), task.ID)
	if u != 500 {
		t.Fatal(u)
	}
	if again, err := e.c.ApplyBudgetDecision(in); err != nil || !reflect.DeepEqual(again, rc) {
		t.Fatal("receipt recovery failed", again, err)
	}
	if got, err := e.c.BudgetDecision(in.RequestID); err != nil || !reflect.DeepEqual(got, rc) {
		t.Fatal(got, err)
	}
	bad = in
	bad.AuthorizationRef = "different authorization"
	if _, err := e.c.ApplyBudgetDecision(bad); err == nil {
		t.Fatal("request ID reused with different input")
	}
	snap, _ := e.c.Snapshot()
	raw, _ := json.Marshal(snap)
	if strings.Contains(string(raw), in.AuthorizationRef) || strings.Contains(string(raw), in.Proposal.EvidenceDigest) {
		t.Fatal("private authorization leaked")
	}
	backup := filepath.Join(e.root, "budget-decision.db")
	if err := e.st.Backup(backup); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(e.root, "restored-decision")
	if err := store.Restore(backup, dest); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(dest)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	restored, err := st.BudgetAuthorization(task.ID)
	if err != nil || !reflect.DeepEqual(restored, v) {
		t.Fatal("decision lost across restore", restored, err)
	}
}
func TestBudgetDecisionRejectsChangedUnknownOrStaleEvidence(t *testing.T) {
	for _, kind := range []string{"file", "context", "profile", "usage", "stale", "overflow"} {
		t.Run(kind, func(t *testing.T) {
			e, f := checkpointEnv(t)
			task := exhaustedTask(t, e)
			in := proposedDecision(t, e, task)
			switch kind {
			case "file":
				os.WriteFile(filepath.Join(task.Worktree, "src", "partial.txt"), []byte("external change"), 0o644)
			case "context":
				if err := e.c.update(func(st *model.State) error {
					findTask(st, task.ID).ContextRef.Digest = strings.Repeat("d", 64)
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			case "profile":
				raw, _ := os.ReadFile(e.profiles["developer"])
				os.WriteFile(e.profiles["developer"], []byte(strings.Replace(string(raw), "m-1", "m-2", 1)), 0o600)
			case "usage":
				e.c.update(func(st *model.State) error {
					st.Runs[0].Usage.Tokens.Total = nil
					st.Runs[0].Usage.UsageCompleteness = model.UsageUnknown
					return nil
				})
			case "stale":
				if _, err := e.c.ApplyBudgetDecision(in); err != nil {
					t.Fatal(err)
				}
				in.RequestID = newUUID()
			case "overflow":
				in.Proposal.AddTokens = model.MaxTaskTokens
			}
			if _, err := e.c.ApplyBudgetDecision(in); err == nil {
				t.Fatal("invalid decision accepted")
			}
			if f.calls != 1 {
				t.Fatal("decision called executor")
			}
		})
	}
}
func TestBudgetDecisionConcurrentProposalHasOneWinner(t *testing.T) {
	e, _ := checkpointEnv(t)
	task := exhaustedTask(t, e)
	in := proposedDecision(t, e, task)
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v := in
			v.RequestID = newUUID()
			_, err := e.c.ApplyBudgetDecision(v)
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	n := 0
	for e := range results {
		if e == nil {
			n++
		}
	}
	if n != 1 {
		t.Fatal("proposal granted twice", n)
	}
	v, err := e.st.BudgetAuthorization(task.ID)
	if err != nil || v.Revision != 1 || v.AddedTokens != 950 {
		t.Fatal(v, err)
	}
}
