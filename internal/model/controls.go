package model

import (
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
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
	Kind             string `json:"kind,omitempty"`
	Message          string `json:"message,omitempty"`
}

// ControlKind reports the effective control kind, defaulting to wrap_up for
// historical inputs that predate the Kind field.
func ControlKind(in WrapUpInput) string {
	if in.Kind == "" {
		return "wrap_up"
	}
	return in.Kind
}

// validControlInputBase holds the shared UUID, authorization and apply
// validation for every owned control input.
func validControlInputBase(v WrapUpInput) bool {
	return budgetUUID.MatchString(v.RunID) && budgetUUID.MatchString(v.SessionID) && budgetUUID.MatchString(v.RequestID) && v.Apply && ValidBudgetText(v.AuthorizationRef)
}

// ValidInstructionMessage reports whether m is bounded, well-formed and
// credential-free instruction text: nonblank, at most 4000 Unicode codepoints
// and 16000 bytes, valid UTF-8, and free of control characters except newline
// and tab.
func ValidInstructionMessage(m string) bool {
	if strings.TrimSpace(m) == "" || len(m) > 16000 || utf8.RuneCountInString(m) > 4000 || !utf8.ValidString(m) || LooksLikeCredential(m) {
		return false
	}
	return !strings.ContainsFunc(m, func(r rune) bool {
		return unicode.IsControl(r) && r != '\n' && r != '\t'
	})
}

// ValidWrapUpInput accepts historical inputs (blank Kind) and explicit
// wrap_up inputs, but refuses messages and any other control kind.
func ValidWrapUpInput(v WrapUpInput) bool {
	return validControlInputBase(v) && ControlKind(v) == "wrap_up" && v.Message == ""
}

// ValidInstructionInput accepts instruction inputs: the shared UUID/auth/apply
// requirements plus bounded, non-credential message text.
func ValidInstructionInput(v WrapUpInput) bool {
	return validControlInputBase(v) && ControlKind(v) == "instruction" && ValidInstructionMessage(v.Message)
}

func ValidPauseInput(v WrapUpInput) bool {
	return validControlInputBase(v) && ControlKind(v) == "pause" && v.Message == ""
}
func ValidFollowUpInput(v WrapUpInput) bool {
	return validControlInputBase(v) && ControlKind(v) == "follow_up" && ValidInstructionMessage(v.Message)
}
func ValidOwnedControlInput(v WrapUpInput) bool {
	return ValidWrapUpInput(v) || ValidInstructionInput(v) || ValidPauseInput(v) || ValidFollowUpInput(v)
}
func ValidControlID(v string) bool { return budgetUUID.MatchString(v) }

func ValidControlState(v string) bool {
	return slices.Contains([]string{ControlAccepted, ControlSending, ControlAcknowledged, ControlRejected, ControlUnknown}, v)
}

func ValidControlReason(v string) bool {
	return slices.Contains([]string{"run_ended_before_send", "run_interrupted", "contract_changed", "executor_refused", "wrap_up_already_requested", "protocol_reply_unknown", "controller_interrupted", "unsupported_control", "pause_requested"}, v)
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
	if !slices.Contains([]string{"wrap_up", "instruction", "pause", "follow_up"}, v.Kind) || v.SessionID == nil || !ValidControlState(v.State) {
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
