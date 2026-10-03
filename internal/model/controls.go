package model

import (
	"slices"
	"time"
)

const (
	ControlAccepted     = "accepted"
	ControlSending      = "sending"
	ControlAcknowledged = "acknowledged"
	ControlRejected     = "rejected"
	ControlUnknown      = "unknown"
)

type WrapUpInput struct {
	RunID            string `json:"runId"`
	SessionID        string `json:"sessionId"`
	RequestID        string `json:"requestId"`
	AuthorizationRef string `json:"authorizationRef"`
	Apply            bool   `json:"apply"`
}

func ValidWrapUpInput(v WrapUpInput) bool {
	return budgetUUID.MatchString(v.RunID) && budgetUUID.MatchString(v.SessionID) && budgetUUID.MatchString(v.RequestID) && v.Apply && ValidBudgetText(v.AuthorizationRef)
}

func ValidControlID(v string) bool { return budgetUUID.MatchString(v) }

func ValidControlState(v string) bool {
	return slices.Contains([]string{ControlAccepted, ControlSending, ControlAcknowledged, ControlRejected, ControlUnknown}, v)
}

func ValidControlReason(v string) bool {
	return slices.Contains([]string{"run_ended_before_send", "run_interrupted", "contract_changed", "executor_refused", "wrap_up_already_requested", "protocol_reply_unknown", "controller_interrupted", "unsupported_control"}, v)
}

func ValidControlReceipt(v ControlReceipt) bool {
	if !budgetUUID.MatchString(v.RequestID) || !budgetUUID.MatchString(v.TaskID) || !budgetUUID.MatchString(v.RunID) || !slices.Contains(RunStates, v.RunState) {
		return false
	}
	start, e := time.Parse(time.RFC3339Nano, v.CreatedAt)
	end, e2 := time.Parse(time.RFC3339Nano, v.UpdatedAt)
	if e != nil || e2 != nil || end.Before(start) {
		return false
	}
	if v.SessionID != nil && !budgetUUID.MatchString(*v.SessionID) {
		return false
	}
	if v.Outcome != nil && (!IsTerminalRunState(*v.Outcome) && *v.Outcome != RunUnknown) {
		return false
	}
	if v.Kind == "stop" {
		return v.Disposition == nil && v.Reason == nil && (v.State == ControlAccepted && v.Outcome == nil || v.State == "processed" && v.Outcome != nil && *v.Outcome != RunUnknown || v.State == ControlUnknown && v.Outcome != nil && *v.Outcome == RunUnknown)
	}
	if v.Kind != "wrap_up" || v.SessionID == nil || !ValidControlState(v.State) {
		return false
	}
	if v.State == ControlAcknowledged {
		return v.Disposition != nil && slices.Contains([]string{"queued", "handled"}, *v.Disposition) && v.Reason == nil
	}
	if v.Disposition != nil {
		return false
	}
	if v.State == ControlAccepted || v.State == ControlSending {
		return v.Reason == nil
	}
	return v.Reason != nil && ValidControlReason(*v.Reason)
}

// ControlRecord is private frozen authority and its durable protocol outcome.
type ControlRecord struct {
	Input          WrapUpInput `json:"input"`
	TaskID         string      `json:"taskId"`
	ContractDigest string      `json:"contractDigest"`
	ProfileDigest  string      `json:"profileDigest"`
	Digest         string      `json:"digest"`
	State          string      `json:"state"`
	Disposition    *string     `json:"disposition"`
	Reason         *string     `json:"reason"`
	CreatedAt      string      `json:"createdAt"`
	UpdatedAt      string      `json:"updatedAt"`
}

// ControlReceipt contains protocol state separately from actual Run state.
type ControlReceipt struct {
	RequestID   string  `json:"requestId"`
	TaskID      string  `json:"taskId"`
	RunID       string  `json:"runId"`
	SessionID   *string `json:"sessionId"`
	Kind        string  `json:"kind"`
	State       string  `json:"state"`
	Disposition *string `json:"disposition"`
	Reason      *string `json:"reason"`
	CreatedAt   string  `json:"createdAt"`
	UpdatedAt   string  `json:"updatedAt"`
	RunState    string  `json:"runState"`
	Outcome     *string `json:"outcome"`
}
