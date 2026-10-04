package cli

import (
	"encoding/json"
	"errors"

	"github.com/hunknownz/Meerkat/internal/model"
	"github.com/hunknownz/Meerkat/internal/server"
)

func cmdControlWrapUp(env Env, args []string) (int, error) {
	fs, dd := newFlags("control wrap-up")
	run := fs.String("run", "", "current Run UUID")
	session := fs.String("session", "", "current Session UUID")
	id := fs.String("request-id", "", "stable request UUID")
	ref := fs.String("authorization", "", "actual user authorization reference")
	apply := fs.Bool("apply", false, "explicitly request bounded wrap-up")
	if err := parse(fs, args); err != nil {
		return ExitUsage, err
	}
	in := model.WrapUpInput{RunID: *run, SessionID: *session, RequestID: *id, AuthorizationRef: *ref, Apply: *apply}
	if !model.ValidWrapUpInput(in) {
		return ExitUsage, usageErr{"Run, Session, request UUID, authorization and --apply required"}
	}
	b, _ := json.Marshal(in)
	return controlCall(env, *dd, server.Request{Op: "request-wrap-up", Input: b}, *id)
}

func cmdControlReceipt(env Env, args []string) (int, error) {
	fs, dd := newFlags("control receipt")
	id := fs.String("request-id", "", "request UUID")
	if err := parse(fs, args); err != nil {
		return ExitUsage, err
	}
	if !model.ValidControlID(*id) {
		return ExitUsage, usageErr{"request UUID required"}
	}
	return controlCall(env, *dd, server.Request{Op: "control-receipt", RequestID: *id}, *id)
}

func controlCall(env Env, dd string, req server.Request, id string) (int, error) {
	dir, err := dataDir(dd)
	if err != nil {
		return ExitUsage, err
	}
	b, code, err := call(env, dir, req)
	if err != nil {
		if err == errHandled {
			return code, nil
		}
		if req.Op != "control-receipt" {
			return code, errors.New("control reply unconfirmed; query control receipt with the same request ID before another write")
		}
		return code, err
	}
	var v model.ControlReceipt
	if json.Unmarshal(b, &v) != nil || v.RequestID != id || !model.ValidControlReceipt(v) {
		return ExitFailed, errors.New("control receipt unreadable; query before another write")
	}
	writeJSON(env.Stdout, map[string]any{"ok": true, "data": v})
	return ExitOK, nil
}

func cmdControlPause(env Env, args []string) (int, error) { return cmdOwnedControl(env, args, "pause") }
func cmdControlFollowUp(env Env, args []string) (int, error) {
	return cmdOwnedControl(env, args, "follow_up")
}
func cmdOwnedControl(env Env, args []string, kind string) (int, error) {
	fs, dd := newFlags("control " + kind)
	run := fs.String("run", "", "owned Run UUID")
	session := fs.String("session", "", "owned Session UUID")
	id := fs.String("request-id", "", "stable request UUID")
	ref := fs.String("authorization", "", "actual authorization reference")
	apply := fs.Bool("apply", false, "explicit control")
	input := fs.String("input", "", "bounded text file or stdin (-), follow-up only")
	if err := parse(fs, args); err != nil {
		return ExitUsage, err
	}
	v := model.WrapUpInput{Kind: kind, RunID: *run, SessionID: *session, RequestID: *id, AuthorizationRef: *ref, Apply: *apply}
	if kind == "follow_up" {
		if *input == "" {
			return ExitUsage, usageErr{"--input FILE|- required"}
		}
		text, err := readInput(env, *input)
		if err != nil {
			return ExitUsage, err
		}
		v.Message = string(text)
	} else if *input != "" {
		return ExitUsage, usageErr{"pause does not accept input text"}
	}
	if !model.ValidOwnedControlInput(v) {
		return ExitUsage, usageErr{"valid owned binding, bounded text, authorization and --apply required"}
	}
	b, _ := json.Marshal(v)
	op := "request-pause"
	if kind == "follow_up" {
		op = "request-follow-up"
	}
	return controlCall(env, *dd, server.Request{Op: op, Input: b}, *id)
}
