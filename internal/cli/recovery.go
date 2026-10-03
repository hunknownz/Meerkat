package cli

import (
	"encoding/json"
	"os"

	"github.com/hunknownz/Meerkat/internal/core"
	"github.com/hunknownz/Meerkat/internal/model"
	"github.com/hunknownz/Meerkat/internal/server"
)

func cmdRecoveryInspect(env Env, args []string) (int, error) {
	fs, dd := newFlags("recovery inspect")
	task := fs.String("task", "", "interrupted task ID")
	output := fs.String("output", "", "new private proposal file")
	if e := parse(fs, args); e != nil {
		return ExitUsage, e
	}
	if *task == "" {
		return ExitUsage, usageErr{"task required"}
	}
	dir, e := dataDir(*dd)
	if e != nil {
		return ExitUsage, e
	}
	raw, code, e := call(env, dir, server.Request{Op: "inspect-recovery", TaskID: *task})
	if e != nil {
		return code, e
	}
	var v model.RecoveryInspection
	if json.Unmarshal(raw, &v) != nil || v.SchemaVersion != 1 || v.TaskID != *task || (v.Status != "verified" && v.Status != "blocked") || (v.Status == "verified") != (v.Proposal != nil) || v.Proposal != nil && (!model.ValidRecoveryProposal(*v.Proposal) || v.Proposal.TaskID != v.TaskID || v.Proposal.RunID != v.RunID) {
		return ExitFailed, usageErr{"unreadable recovery inspection"}
	}
	if *output != "" {
		if v.Proposal == nil {
			return ExitFailed, usageErr{"recovery blocked; inspect without --output for reasons"}
		}
		f, e := os.OpenFile(*output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if e != nil {
			return ExitUsage, usageErr{"proposal file must be new"}
		}
		b, _ := json.MarshalIndent(v.Proposal, "", "  ")
		_, e = f.Write(append(b, '\n'))
		ce := f.Close()
		if e != nil || ce != nil {
			return ExitFailed, usageErr{"proposal could not be saved"}
		}
	}
	writeJSON(env.Stdout, map[string]any{"ok": true, "data": v})
	return ExitOK, nil
}
func cmdRecoveryApply(env Env, args []string) (int, error) {
	fs, dd := newFlags("recovery apply")
	input := fs.String("input", "", "proposal JSON")
	id := fs.String("request-id", "", "request UUID")
	ref := fs.String("authorization", "", "actual authorization reference")
	apply := fs.Bool("apply", false, "explicitly apply recovery")
	if e := parse(fs, args); e != nil {
		return ExitUsage, e
	}
	if *input == "" || !*apply {
		return ExitUsage, usageErr{"proposal, authorization, UUID and --apply required"}
	}
	raw, e := readInput(env, *input)
	if e != nil {
		return ExitUsage, e
	}
	p, e := core.ParseRecoveryProposal(raw)
	if e != nil {
		return ExitUsage, e
	}
	v := model.RecoveryInput{Proposal: p, RequestID: *id, AuthorizationRef: *ref, Apply: *apply}
	if !model.ValidRecoveryInput(v) {
		return ExitUsage, usageErr{"invalid recovery authorization"}
	}
	b, _ := json.Marshal(v)
	return remote(env, *dd, server.Request{Op: "apply-recovery", Input: b})
}
func cmdRecoveryReceipt(env Env, args []string) (int, error) {
	fs, dd := newFlags("recovery receipt")
	id := fs.String("request-id", "", "request UUID")
	if e := parse(fs, args); e != nil {
		return ExitUsage, e
	}
	if *id == "" {
		return ExitUsage, usageErr{"request UUID required"}
	}
	return remote(env, *dd, server.Request{Op: "recovery-decision", RequestID: *id})
}
