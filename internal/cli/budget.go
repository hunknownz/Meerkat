package cli

import (
	"encoding/json"
	"os"

	"github.com/hunknownz/Meerkat/internal/core"
	"github.com/hunknownz/Meerkat/internal/model"
	"github.com/hunknownz/Meerkat/internal/server"
)

func cmdBudgetPropose(env Env, args []string) (int, error) {
	fs, dd := newFlags("budget propose")
	task := fs.String("task", "", "task ID")
	tokens := fs.Int64("add-tokens", 0, "additional tokens")
	wall := fs.Int64("add-wall-seconds", 0, "additional execution seconds")
	reason := fs.String("reason", "", "reason")
	output := fs.String("output", "", "new private proposal file")
	if e := parse(fs, args); e != nil {
		return ExitUsage, e
	}
	in := model.BudgetIncrease{TaskID: *task, AddTokens: *tokens, AddWallSeconds: *wall, Reason: *reason}
	if !model.ValidBudgetIncrease(in) {
		return ExitUsage, usageErr{"task, positive addition and reason required"}
	}
	dir, e := dataDir(*dd)
	if e != nil {
		return ExitUsage, e
	}
	b, _ := json.Marshal(in)
	raw, code, e := call(env, dir, server.Request{Op: "propose-budget", Input: b})
	if e != nil {
		return code, e
	}
	p, e := core.ParseBudgetProposal(raw)
	if e != nil {
		return ExitFailed, e
	}
	if *output != "" {
		f, e := os.OpenFile(*output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if e != nil {
			return ExitUsage, usageErr{"proposal output must not already exist"}
		}
		b, _ := json.MarshalIndent(p, "", "  ")
		_, e = f.Write(append(b, '\n'))
		ce := f.Close()
		if e != nil || ce != nil {
			return ExitFailed, usageErr{"could not write proposal"}
		}
	}
	writeJSON(env.Stdout, map[string]any{"ok": true, "data": p})
	return ExitOK, nil
}
func cmdBudgetApply(env Env, args []string) (int, error) {
	fs, dd := newFlags("budget apply")
	input := fs.String("input", "", "proposal JSON")
	id := fs.String("request-id", "", "decision request UUID")
	ref := fs.String("authorization", "", "explicit authorization reference")
	apply := fs.Bool("apply", false, "explicitly authorize application")
	if e := parse(fs, args); e != nil {
		return ExitUsage, e
	}
	if *input == "" || !*apply {
		return ExitUsage, usageErr{"proposal, request ID, authorization and --apply required"}
	}
	raw, e := readInput(env, *input)
	if e != nil {
		return ExitUsage, e
	}
	p, e := core.ParseBudgetProposal(raw)
	if e != nil {
		return ExitUsage, e
	}
	in := model.BudgetDecisionInput{Proposal: p, RequestID: *id, AuthorizationRef: *ref, Apply: *apply}
	if !model.ValidBudgetDecision(in) {
		return ExitUsage, usageErr{"invalid explicit budget decision"}
	}
	b, _ := json.Marshal(in)
	return remote(env, *dd, server.Request{Op: "apply-budget-decision", Input: b})
}
func cmdBudgetReceipt(env Env, args []string) (int, error) {
	fs, dd := newFlags("budget receipt")
	id := fs.String("request-id", "", "decision UUID")
	if e := parse(fs, args); e != nil {
		return ExitUsage, e
	}
	if *id == "" {
		return ExitUsage, usageErr{"request ID required"}
	}
	return remote(env, *dd, server.Request{Op: "budget-decision", RequestID: *id})
}
