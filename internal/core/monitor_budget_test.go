package core

import (
	"github.com/hunknownz/Meerkat/internal/model"
	"testing"
)

func TestMonitorBudgetDefaultsAndExplicitLegacyCap(t *testing.T) {
	b, err := normBudget(nil)
	if err != nil || b.Mode != "monitor" || b.HardTokenCap() {
		t.Fatal(b, err)
	}
	n := int64(100)
	b, err = normBudget(&budgetInput{MaxTokens: &n})
	if err != nil || b.Mode != "" || !b.HardTokenCap() {
		t.Fatal("legacy explicit cap changed", b, err)
	}
	mode := "monitor"
	b, err = normBudget(&budgetInput{MaxTokens: &n, Mode: &mode})
	if err != nil || b.HardTokenCap() {
		t.Fatal(b, err)
	}
	mode = "invalid"
	if _, err = normBudget(&budgetInput{Mode: &mode}); err == nil {
		t.Fatal("invalid mode accepted")
	}
	if !model.DefaultBudget().HardTokenCap() {
		t.Fatal("historical fallback changed")
	}
}

func TestMonitorTaskCompletesBeyondSoftThreshold(t *testing.T) {
	e := setup(t)
	tt := e.prepare(e.worktree("monitor"), func(m map[string]any) { m["budget"] = map[string]any{"mode": "monitor", "maxTokens": 50} })
	want(t, e.exec(tt.ID), 0, model.TaskDelivered, "")
	e.fx.mu.Lock()
	defer e.fx.mu.Unlock()
	if len(e.fx.reqs) < 3 {
		t.Fatal("whole delivery chain not executed")
	}
	for _, r := range e.fx.reqs {
		if r.RemainingTokens != 1000 {
			t.Fatal("Profile Run cap changed", r.RemainingTokens)
		}
	}
}
