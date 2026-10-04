//go:build unix

package executor

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/hunknownz/Meerkat/internal/model"
)

func TestHumanInstructionKeepsAutomaticWrapUpAndSamePrompt(t *testing.T) {
	e := setup(t)
	req := rpcRequest(t, e, "wrap")
	req.RemainingWall = 900 * time.Millisecond
	req.WrapUpBefore = 300 * time.Millisecond
	a := &fixtureControlAuthority{path: e.args + ".authority"}
	ch := make(chan RunControl, 1)
	ch <- RunControl{ID: "human-control", Kind: "instruction", Message: "PRIVATE_DIRECTION"}
	req.Controls = &ControlBinding{Messages: ch, Authority: a}
	p := NewPi()
	p.Grace = time.Second
	r, _ := p.Execute(context.Background(), req, nil, nil)
	commands, _ := os.ReadFile(e.args + ".commands")
	directions, _ := os.ReadFile(e.args + ".steer")
	if strings.Count(string(commands), "steer\n") != 2 || strings.Count(string(commands), "prompt\n") != 1 || !strings.Contains(string(directions), "PRIVATE_DIRECTION") || !strings.Contains(string(directions), budgetWrapUpMessage) {
		t.Fatal("human steer replaced budget wrap-up or restarted session", string(commands))
	}
	if len(a.states) != 2 || a.states[1] != model.ControlAcknowledged || r.Outcome == model.RunSucceeded {
		t.Fatal(a.states, r)
	}
}
