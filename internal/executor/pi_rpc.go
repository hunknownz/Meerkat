package executor

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/hunknownz/Meerkat/internal/checkpoint"
	"github.com/hunknownz/Meerkat/internal/executor/pibudget"
	"github.com/hunknownz/Meerkat/internal/executor/pirpc"
	"github.com/hunknownz/Meerkat/internal/model"
)

func (t *tracker) rpc(ev pirpc.Event) {
	if ev.Type == "auto_compaction_start" || ev.Type == "auto_retry_start" {
		t.badLines++
	} // hidden calls are not complete billing evidence
	msg := &piMessage{Role: "assistant"}
	if ev.ProviderFailure {
		msg.StopReason = "error"
	}
	if s := ev.Usage; s != nil {
		m := map[string]any{"input": s.Input, "output": s.Output, "cacheRead": s.CacheRead, "cacheWrite": s.CacheWrite}
		if s.CostEstimateUSD != nil {
			m["cost"] = map[string]any{"total": *s.CostEstimateUSD}
		}
		msg.Usage, _ = json.Marshal(m)
	}
	e := piEvent{Type: ev.Type}
	if ev.Assistant {
		e.Message = msg
	}
	b, _ := json.Marshal(e)
	t.line(b)
}

func rpcIdle(s pirpc.State, b SessionBinding, req Request) bool {
	return s.SessionID == b.ProviderID && s.SessionFile == b.File && s.Provider == req.Profile.Provider && s.ModelID == req.Profile.Model && !s.Streaming && !s.Compacting && s.PendingMessages == 0
}

// runRPC owns one child for one Run. A durable session survives shutdown; no
// task, budget, or queue policy is implemented by this protocol adapter.
func (p *Pi) runRPC(ctx context.Context, pp *prepared, req Request, onEvent func(model.RunEvent), onStart func(Process)) procOutcome {
	// Stream events and control receipts originate from different goroutines.
	// Preserve the executor callback's serial delivery contract.
	if onEvent != nil {
		var eventMu sync.Mutex
		callback := onEvent
		onEvent = func(ev model.RunEvent) {
			eventMu.Lock()
			defer eventMu.Unlock()
			callback(ev)
		}
	}
	o := procOutcome{t: newTracker(), session: &SessionOutcome{ID: req.Session.ID, ProviderID: req.Session.ProviderID}}
	if ctx.Err() != nil {
		o.stop = CatCanceled
		return o
	}
	var bridge *pibudget.Server
	if req.Budget != nil {
		var err error
		bridge, err = pibudget.Start(req.Profile.Provider, req.Profile.Model, req.Budget, pibudget.ReportOption{Role: req.Role, Write: reportWriter(req, pp)})
		if err != nil {
			o.stop = CatBudgetGate
			return o
		}
		defer bridge.Close()
		pp.argv = append(pp.argv, "--extension", bridge.Extension())
		pp.env = append(append([]string{}, pp.env...), bridge.Env())
	}
	cmd := exec.Command(pp.argv[0], pp.argv[1:]...)
	cmd.Dir, cmd.Env = pp.worktree, pp.env
	ownGroup(cmd)
	in, err := cmd.StdinPipe()
	var out io.ReadCloser
	if err == nil {
		out, err = cmd.StdoutPipe()
	}
	if err == nil {
		err = cmd.Start()
	}
	if err != nil {
		if in != nil {
			in.Close()
		}
		if out != nil {
			out.Close()
		}
		o.spawnErr = true
		if snap, e := p.InspectSession(*req.Session); e == nil && snap.Digest == req.Session.Digest {
			o.session.Digest, o.session.Confirmed = snap.Digest, true
		}
		return o
	}
	host, _ := os.Hostname()
	pid := cmd.Process.Pid
	o.proc = &Process{Executor: "pi", PID: pid, PGID: pid, Host: host, StartedAt: time.Now().UTC()}
	if onStart != nil {
		func() { defer func() { _ = recover() }(); onStart(*o.proc) }()
	}
	client, err := pirpc.New(in, out)
	if err != nil {
		termGroup(pid)
		killGroup(pid)
		cmd.Wait()
		o.protocolErr = true
		return o
	}
	var mu sync.Mutex
	activeTools := map[string]bool{}
	uncertainTools := false
	settled := make(chan struct{}, 1)
	limit := make(chan struct{}, 1)
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		for ev := range client.Events() {
			mu.Lock()
			switch ev.Type {
			case "tool_execution_start":
				if ev.ToolID == "" || activeTools[ev.ToolID] {
					uncertainTools = true
				} else {
					activeTools[ev.ToolID] = true
				}
			case "tool_execution_end":
				if !activeTools[ev.ToolID] || !ev.ToolOutcomeKnown || ev.ToolFailed {
					uncertainTools = true
				}
				delete(activeTools, ev.ToolID)
			}
			o.t.rpc(ev)
			over := o.t.liveTotal() > pp.tokens
			mu.Unlock()
			if over {
				select {
				case limit <- struct{}{}:
				default:
				}
			}
			if ev.Settled {
				select {
				case settled <- struct{}{}:
				default:
				}
			}
			switch {
			case ev.Tool != "" && ev.Type == "tool_execution_start":
				safeEvent(onEvent, "tool", ev.Tool)
			case ev.Settled:
				safeEvent(onEvent, "lifecycle", "settled")
			case ev.Type == "agent_start":
				safeEvent(onEvent, "lifecycle", "started")
			case ev.Type == "message_end" && ev.Assistant:
				safeEvent(onEvent, "lifecycle", "assistant_message")
			}
		}
	}()
	wait := make(chan error, 1)
	go func() { <-client.Done(); wait <- cmd.Wait() }()
	rctx, cancel := context.WithTimeout(ctx, pp.wall)
	defer cancel()
	verified := false
	uncertain := false
	var budgetHalt <-chan struct{}
	if bridge != nil {
		budgetHalt = bridge.Halt()
	}
	state, e := client.State(rctx)
	bridgeReady := true
	if bridge != nil {
		readyCtx, readyCancel := context.WithTimeout(rctx, 5*time.Second)
		bridgeReady = bridge.WaitReady(readyCtx)
		readyCancel()
		if !bridgeReady {
			o.stop = CatBudgetGate
		}
	}
	checkpointOK := req.Checkpoint == nil || checkpoint.Verify(req.Worktree, *req.Checkpoint) == nil
	if e == nil && rpcIdle(state, *req.Session, req) && bridgeReady && checkpointOK {
		rc, e := client.Prompt(rctx, pp.prompt)
		if e == nil && rc.Disposition == "started" {
			wrapUp := req.WrapUp
			wrapSent := false
			var controls <-chan RunControl
			if req.Controls != nil && req.Controls.Authority != nil {
				controls = req.Controls.Messages
			}
			var wrapTimer *time.Timer
			var wrapTime <-chan time.Time
			if req.WrapUpBefore > 0 {
				deadline, _ := rctx.Deadline()
				wrapTimer = time.NewTimer(max(0, time.Until(deadline)-req.WrapUpBefore))
				defer wrapTimer.Stop()
				wrapTime = wrapTimer.C
			}
		waiting:
			for {
				select {
				case control, open := <-controls:
					if !open {
						controls = nil
						continue
					}
					sent, unknown := sendRunControl(rctx, client, req.Controls.Authority, control, wrapSent)
					if unknown {
						uncertain, o.protocolErr = true, true
						break waiting
					}
					if sent && control.Kind == "pause" {
						o.pauseRequested = true
						wrapUp, wrapTime = nil, nil
						if wrapTimer != nil {
							wrapTimer.Stop()
						}
						safeEvent(onEvent, "control", "pause_acknowledged")
					}
					if sent && control.Kind == "follow_up" {
						safeEvent(onEvent, "control", "follow_up_acknowledged")
					}
					if sent && control.Kind == "instruction" {
						safeEvent(onEvent, "control", "instruction_acknowledged")
					}
					if sent && control.Kind == "wrap_up" {
						wrapSent, wrapUp, wrapTime = true, nil, nil
						if wrapTimer != nil {
							wrapTimer.Stop()
						}
						safeEvent(onEvent, "control", "wrap_up_requested")
						safeEvent(onEvent, "control", "wrap_up_acknowledged")
					}
				case <-wrapUp:
					wrapSent = true
					wrapUp, wrapTime = nil, nil
					if wrapTimer != nil {
						wrapTimer.Stop()
					}
					safeEvent(onEvent, "budget", "wrap_up_requested")
					steerCtx, steerCancel := context.WithTimeout(rctx, 3*time.Second)
					_, err := client.Steer(steerCtx, budgetWrapUpMessage)
					steerCancel()
					if err != nil {
						uncertain = true
						o.protocolErr = true
						break waiting
					}
					safeEvent(onEvent, "budget", "wrap_up_accepted")
				case <-wrapTime:
					wrapSent = true
					wrapUp, wrapTime = nil, nil
					safeEvent(onEvent, "budget", "wrap_up_requested")
					steerCtx, steerCancel := context.WithTimeout(rctx, 3*time.Second)
					_, err := client.Steer(steerCtx, budgetWrapUpMessage)
					steerCancel()
					if err != nil {
						uncertain = true
						o.protocolErr = true
						break waiting
					}
					safeEvent(onEvent, "budget", "wrap_up_accepted")
				case <-settled:
					state, e = client.State(rctx)
					verified = e == nil && rpcIdle(state, *req.Session, req)
					if e != nil {
						uncertain, o.protocolErr = true, true
						break waiting
					}
					if !verified {
						continue
					}
					if req.Controls != nil {
						if drain, ok := req.Controls.Authority.(ControlDrainAuthority); ok {
							ready, err := drain.Quiesce()
							if err != nil {
								uncertain, o.protocolErr = true, true
								break waiting
							}
							if !ready {
								verified = false
								continue
							}
						}
					}
					break waiting
				case <-limit:
					o.stop = CatTokenLimit
					break waiting
				case <-budgetHalt:
					denied, unknown := bridge.Outcome()
					if unknown {
						o.stop = CatBudgetUnknown
					} else if denied {
						o.stop = CatTokenLimit
					}
					break waiting
				case <-rctx.Done():
					o.stop = CatWallTimeout
					if ctx.Err() != nil {
						o.stop = CatCanceled
					}
					break waiting
				case <-client.Done():
					o.protocolErr = true
					break waiting
				}
			}
		} else {
			// No retry: after a missing reply the prompt may already be running.
			uncertain = true
			o.protocolErr = true
		}
	} else {
		if !bridgeReady && e == nil && rpcIdle(state, *req.Session, req) {
			verified = true // no prompt was submitted; the original history is idle
		} else {
			o.protocolErr = true
			uncertain = true
		}
	}
	grace := p.Grace
	if grace <= 0 {
		grace = 5 * time.Second
	}
	grace = min(grace, 5*time.Second)
	sctx, stopCancel := context.WithTimeout(context.Background(), grace)
	if !verified {
		rc, e := client.Stop(sctx)
		verified = e == nil && rc.Confirmed && rpcIdle(rc.State, *req.Session, req)
	}
	shutdownErr := client.Shutdown(sctx)
	stopCancel()
	reaped := false
	if shutdownErr == nil {
		select {
		case <-wait:
			reaped = true
		case <-time.After(grace):
		}
	}
	if !reaped {
		select {
		case <-wait:
			reaped = true
		default:
		}
	}
	if !reaped {
		termGroup(pid)
		select {
		case <-wait:
		case <-time.After(grace):
			killGroup(pid)
			<-wait
		}
	}
	client.Close()
	<-drained
	if o.t.liveTotal() > pp.tokens {
		o.stop = CatTokenLimit
	}
	if bridge != nil {
		denied, unknown := bridge.Outcome()
		if unknown {
			o.stop = CatBudgetUnknown
		} else if denied && o.stop == "" {
			o.stop = CatTokenLimit
		}
	}
	if ps := cmd.ProcessState; ps != nil {
		if code := ps.ExitCode(); code >= 0 {
			o.exitCode = &code
		}
		o.signal = exitSignal(ps)
	}
	snap, se := p.InspectSession(*req.Session)
	if verified && !uncertain && shutdownErr == nil && se == nil && o.exitCode != nil && *o.exitCode == 0 {
		o.session.Digest, o.session.Confirmed = snap.Digest, true
		o.checkpointSafe = len(activeTools) == 0 && !uncertainTools && groupGone(pid)
	}
	return o
}

// sendRunControl persists sending before a protocol effect. Definite refusal is
// different from a lost reply. No branch retries a control or changes its text.
func sendRunControl(ctx context.Context, client *pirpc.Client, authority ControlAuthority, v RunControl, alreadySent bool) (sent, unknown bool) {
	reject := func(reason string) (bool, bool) {
		return false, authority.Finish(v.ID, model.ControlRejected, "", reason) != nil
	}
	if !((v.Kind == "wrap_up" || v.Kind == "pause") && v.Message == "" || (v.Kind == "instruction" || v.Kind == "follow_up") && model.ValidInstructionMessage(v.Message)) {
		return reject("unsupported_control")
	}
	if alreadySent && v.Kind == "wrap_up" {
		return reject("wrap_up_already_requested")
	}
	if err := authority.Begin(v.ID); err != nil {
		if errors.Is(err, ErrControlRejected) {
			return false, false
		}
		return false, true
	}
	sctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	message := budgetWrapUpMessage
	if v.Kind == "instruction" || v.Kind == "follow_up" {
		message = v.Message
	}
	if v.Kind == "pause" {
		message = gracefulPauseMessage
	}
	var rc pirpc.Receipt
	var err error
	if v.Kind == "follow_up" {
		rc, err = client.FollowUp(sctx, message)
	} else {
		rc, err = client.Steer(sctx, message)
	}
	cancel()
	if err != nil {
		var rpcErr *pirpc.Error
		if errors.As(err, &rpcErr) && (rpcErr.Kind == pirpc.Rejected || rpcErr.Kind == pirpc.NotSent) {
			return reject("executor_refused")
		}
		_ = authority.Finish(v.ID, model.ControlUnknown, "", "protocol_reply_unknown")
		return false, true
	}
	if rc.Disposition != "queued" && rc.Disposition != "handled" {
		_ = authority.Finish(v.ID, model.ControlUnknown, "", "protocol_reply_unknown")
		return false, true
	}
	return true, authority.Finish(v.ID, model.ControlAcknowledged, rc.Disposition, "") != nil
}

const budgetWrapUpMessage = "Meerkat budget wrap-up: stop expanding scope. Finish only the current safe operation, preserve work and report the remaining steps and known gaps. If the role contract is already satisfied, write the required report and finish. Do not claim delivery or manufacture a commit when the work is incomplete. No further scope expansion or remote actions. All requests remain within the existing allowance."

const gracefulPauseMessage = "Meerkat graceful pause: finish only the current safe bounded operation, preserve all in-scope staged and unstaged work, and stop this turn. Do not commit incomplete work, start new work, or claim delivery. Briefly record completed and remaining steps in this session. If the original role was already fully completed with its valid report and commit, preserve that result. No remote actions; existing scope and allowance still apply."
