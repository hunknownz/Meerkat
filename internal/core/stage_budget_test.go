package core

import (
	"testing"

	"github.com/hunknownz/Meerkat/internal/model"
)

func TestStageBudgetPreservesLaterReviewsAndPermittedFixes(t *testing.T) {
	b := model.DefaultBudget()
	b.StageReserves = &model.StageReserves{ReviewTokens: 100, FixTokens: 200, PolishTokens: 300, WrapUpTokens: 50}
	task := model.Task{Budget: &b}
	for _, tc := range []struct {
		name string
		p    pipeline
		s    step
		want int64
	}{
		{"development", pipeline{}, step{role: "developer", purpose: "implement"}, 1100},
		{"first review", pipeline{implemented: true}, step{role: "reviewer"}, 1000},
		{"first fix", pipeline{implemented: true}, step{role: "developer", purpose: "fix"}, 800},
		{"last fix", pipeline{implemented: true, fixRounds: 1}, step{role: "developer", purpose: "fix"}, 500},
		{"polish", pipeline{implemented: true}, step{role: "polisher"}, 700},
		{"post polish review", pipeline{implemented: true, polished: true}, step{role: "reviewer"}, 600},
		{"final permitted review", pipeline{implemented: true, polished: true, fixRounds: 2}, step{role: "reviewer"}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := futureStageReserve(task, tc.p, tc.s); got != tc.want {
				t.Fatalf("reserved %d, want %d", got, tc.want)
			}
		})
	}
	task.Origin = OriginDelegate
	if futureStageReserve(task, pipeline{}, step{role: "developer"}) != 0 {
		t.Fatal("one-run delegation reserved roles it will never run")
	}
}

func TestStageBudgetFrozenValidationAndExecution(t *testing.T) {
	e := setup(t)
	task := e.prepare(e.worktree("stage-budget"), func(m map[string]any) {
		m["budget"] = map[string]any{"maxTokens": 1800, "stageReserves": map[string]any{
			"reviewTokens": 100, "fixTokens": 200, "polishTokens": 100, "wrapUpTokens": 40, "wrapUpSeconds": 2,
		}}
	})
	if task.Budget.StageReserves == nil || task.Budget.StageReserves.WrapUpTokens != 40 {
		t.Fatal("stage configuration lost")
	}
	if r := e.exec(task.ID); r.Tasks[0].State != model.TaskDelivered {
		t.Fatal(r)
	}
	if e.fx.reqs[0].RemainingTokens != 900 || e.fx.reqs[0].WrapUpTokens != 40 {
		t.Fatal("developer consumed future-stage allowance", e.fx.reqs[0].RemainingTokens)
	}
	for _, b := range []*budgetInput{
		{MaxTokens: stageInt(100), StageReserves: &model.StageReserves{ReviewTokens: 25}},
		{StageReserves: &model.StageReserves{FixTokens: -1}},
		{MaxWallSeconds: stageInt(10), StageReserves: &model.StageReserves{WrapUpSeconds: 10}},
	} {
		if _, err := normBudget(b); err == nil {
			t.Fatal("invalid or development-starving stage allowance accepted", b)
		}
	}
}

func stageInt(v int64) *int64 { return &v }
