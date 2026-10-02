package pirpc

import (
	"context"
	"encoding/json"
	"path/filepath"

	"github.com/hunknownz/Meerkat/internal/model"
)

// State is private control-plane data. SessionFile must not be published to the
// browser or used for restore until its identity and frozen contract are checked.
type State struct {
	SessionID       string
	SessionFile     string
	Streaming       bool
	Compacting      bool
	PendingMessages int64
}

func (c *Client) State(ctx context.Context) (State, error) {
	data, _, err := c.call(ctx, "get_state", "", false)
	if err != nil {
		return State{}, err
	}
	var v struct {
		SessionID       string `json:"sessionId"`
		SessionFile     string `json:"sessionFile"`
		Streaming       *bool  `json:"isStreaming"`
		Compacting      *bool  `json:"isCompacting"`
		PendingMessages *int64 `json:"pendingMessageCount"`
	}
	if json.Unmarshal(data, &v) != nil || v.SessionID == "" || len(v.SessionID) > 128 ||
		model.LooksLikeCredential(v.SessionID) || v.Streaming == nil || v.Compacting == nil ||
		v.PendingMessages == nil || *v.PendingMessages < 0 || len(v.SessionFile) > 4096 ||
		(v.SessionFile != "" && !filepath.IsAbs(v.SessionFile)) {
		return State{}, &Error{Kind: Protocol}
	}
	return State{v.SessionID, v.SessionFile, *v.Streaming, *v.Compacting, *v.PendingMessages}, nil
}

// StopReceipt confirms Pi's session is idle, not that a task is delivered or the
// process has exited. QueueCleared and AbortAccepted are separate acknowledgements.
type StopReceipt struct {
	QueueCleared  bool
	AbortAccepted bool
	Confirmed     bool
	State         State
}

// Stop gates new input, clears pending messages, aborts and verifies idle state.
// Gating is permanent for this Client. Reopening/continuing belongs to the future
// session manager after checkpoint and budget checks, never to this transport.
func (c *Client) Stop(ctx context.Context) (StopReceipt, error) {
	select {
	case <-ctx.Done():
		return StopReceipt{}, &Error{Kind: NotSent, cause: ctx.Err()}
	case <-c.done:
		return StopReceipt{}, &Error{Kind: Uncertain, cause: c.Err()}
	case <-c.stopGate:
	}
	defer func() { c.stopGate <- struct{}{} }()
	if ctx.Err() != nil {
		return StopReceipt{}, &Error{Kind: NotSent, cause: ctx.Err()}
	}
	if c.stopResult != nil {
		return *c.stopResult, nil
	}
	c.mu.Lock()
	c.inputsStopped = true
	c.mu.Unlock()
	var rc StopReceipt
	if _, _, err := c.call(ctx, "clear_queue", "", false); err != nil {
		return rc, err
	}
	rc.QueueCleared = true
	if _, _, err := c.call(ctx, "abort", "", false); err != nil {
		return rc, err
	}
	rc.AbortAccepted = true
	state, err := c.State(ctx)
	if err != nil {
		return rc, err
	}
	rc.State = state
	if state.Streaming || state.Compacting || state.PendingMessages != 0 {
		return rc, &Error{Kind: Uncertain}
	}
	rc.Confirmed = true
	c.stopResult = &rc
	return rc, nil
}
