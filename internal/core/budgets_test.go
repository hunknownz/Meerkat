package core

import (
	"context"
	"strings"
	"testing"

	"github.com/hunknownz/Meerkat/internal/executor"
	"github.com/hunknownz/Meerkat/internal/model"
	budget "github.com/hunknownz/Meerkat/internal/requestbudget"
)

type budgetFake struct {
	*statefulFake
	mode string
}

func (f *budgetFake) Capabilities() executor.Capabilities {
	return executor.Capabilities{Protocol: "budget-fixture", PersistentSessions: true, RequestBudgetGate: true}
}
func (f *budgetFake) Execute(ctx context.Context, req executor.Request, event func(model.RunEvent), start func(executor.Process)) (executor.Result, error) {
	if req.Budget == nil {
		return executor.Result{}, invalid("request authority missing")
	}
	if f.mode == "no-request" {
		return f.statefulFake.Execute(ctx, req, event, start)
	}
	r := budget.Request{ID: newUUID(), Digest: strings.Repeat("a", 64), API: "openai-completions", Provider: req.Profile.Provider, Model: req.Profile.Model, InputEstimate: 10, MaxOutput: 50}
	if f.mode == "wrap" {
		r.MaxOutput = req.RemainingTokens
	}
	if _, err := req.Budget.Reserve(ctx, r); err != nil {
		return executor.Result{}, err
	}
	if f.mode == "wrap" {
		select {
		case <-req.WrapUp:
		default:
			return executor.Result{}, invalid("reservation did not trigger wrap-up")
		}
	}
	if err := req.Budget.Begin(ctx, budget.Begin{ID: r.ID, Digest: strings.Repeat("b", 64)}); err != nil {
		return executor.Result{}, err
	}
	input, output, total := int64(10), int64(5), int64(15)
	v := budget.Settlement{ID: r.ID, State: budget.Settled, Terminal: true, Tokens: model.TokenCounts{Input: &input, Output: &output, Total: &total}}
	if f.mode == "overrun" {
		total, input = 80, 75
	}
	if f.mode == "unknown" {
		v = budget.Settlement{ID: r.ID, State: budget.Unknown}
	}
	if f.mode != "missing" {
		if err := req.Budget.Settle(ctx, v); err != nil {
			return executor.Result{}, err
		}
	}
	return f.statefulFake.Execute(ctx, req, event, start)
}

func TestBudgetWrapUpUsesReservationsAndKeepsConfirmedSettlement(t *testing.T) {
	e := setup(t)
	useBudgetFixture(t, e, "wrap")
	task := e.prepare(e.worktree("budget-wrap"), func(m map[string]any) {
		m["budget"] = map[string]any{"maxTokens": 1000, "stageReserves": map[string]any{"wrapUpTokens": 20}}
	})
	if r := e.exec(task.ID); r.Tasks[0].State != model.TaskDelivered {
		t.Fatal(r)
	}
	for _, r := range e.state().Runs {
		if r.Usage == nil || r.Usage.Tokens.Total == nil || *r.Usage.Tokens.Total != 15 {
			t.Fatal("wrap-up changed settlement", r)
		}
	}
}

func useBudgetFixture(t *testing.T, e *env, mode string) {
	t.Helper()
	e.c.Close()
	f := &budgetFake{statefulFake: &statefulFake{fakeExec: e.fx}, mode: mode}
	c, err := New(e.st, Registry{"fake": f}, Options{Poll: e.c.opts.Poll, Heartbeat: e.c.opts.Heartbeat})
	if err != nil {
		t.Fatal(err)
	}
	e.c = c
}

func TestBudgetLedgerOverridesExecutorUsageWithoutDoubleCount(t *testing.T) {
	e := setup(t)
	useBudgetFixture(t, e, "settled")
	task := e.prepare(e.worktree("budget-accounting"), nil)
	r := e.exec(task.ID)
	if r.Tasks[0].State != model.TaskDelivered {
		t.Fatal(r)
	}
	st := e.state()
	for _, run := range st.Runs {
		if run.Usage == nil || run.Usage.Tokens.Total == nil || *run.Usage.Tokens.Total != 15 || run.Usage.Tokens.CacheRead != nil || run.Usage.EstimatedCostUsd != nil {
			t.Fatal("normalized SDK counts or price replaced wire evidence", run.Usage)
		}
		rows, err := e.st.RequestBudgetRecords(run.ID)
		if err != nil || len(rows) != 1 || rows[0].State != budget.Settled {
			t.Fatal(rows, err)
		}
	}
	tokens, _ := budgetUse(st, task.ID)
	if tokens != int64(len(st.Runs))*15 {
		t.Fatal("usage counted twice", tokens)
	}
}

func TestBudgetNoRequestsNeverCopiesNormalizedSDKUsage(t *testing.T) {
	e := setup(t)
	useBudgetFixture(t, e, "no-request")
	task := e.prepare(e.worktree("budget-no-request"), nil)
	e.exec(task.ID)
	for _, r := range e.state().Runs {
		if r.Usage == nil || r.Usage.Tokens.Total != nil || r.Usage.EstimatedCostUsd != nil || r.Usage.UsageCompleteness != model.UsageUnknown {
			t.Fatal("unobserved usage copied from SDK", r.Usage)
		}
	}
}

func TestBudgetUnknownAndOverrunNeverDeliverCode(t *testing.T) {
	for _, mode := range []string{"unknown", "missing", "overrun"} {
		t.Run(mode, func(t *testing.T) {
			e := setup(t)
			useBudgetFixture(t, e, mode)
			task := e.prepare(e.worktree("budget-"+mode), nil)
			r := e.exec(task.ID)
			want := model.TaskUnknown
			if mode == "overrun" {
				want = model.TaskStopped
			}
			if r.Tasks[0].State != want {
				t.Fatal(r)
			}
			st := e.state()
			if len(st.Runs) != 1 || len(st.Deliveries) != 0 {
				t.Fatal("unverified delivery or later role executed", len(st.Runs), len(st.Deliveries))
			}
			if mode != "overrun" && st.Runs[0].Usage.Tokens.Total != nil {
				t.Fatal("unknown fell back to SDK usage")
			}
			ss, err := e.st.SessionForRun(st.Runs[0].ID)
			if err != nil || ss.State != model.SessionIdle {
				t.Fatal("budget uncertainty conflated with verified history", ss, err)
			}
		})
	}
}
