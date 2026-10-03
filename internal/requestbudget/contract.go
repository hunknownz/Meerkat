// Package requestbudget defines the private, executor-neutral request budget contract.
package requestbudget

import (
	"context"
	"errors"

	"github.com/hunknownz/Meerkat/internal/model"
)

const (
	PolicyVersion = "estimated-tokens-v1"
	MaxRequests   = int64(256)
	MaxOutput     = int64(8192)
	MaxCount      = int64(1_000_000_000_000)
	Reserved      = "reserved"
	Sent          = "sent"
	Settled       = "settled"
	Unknown       = "unknown"
	Canceled      = "canceled"
)

var (
	ErrDenied   = errors.New("budget: request denied")
	ErrUnknown  = errors.New("budget: outcome unknown")
	ErrConflict = errors.New("budget: request conflict")
)

// Policy is private frozen authority for a Run. Tokens are estimate reservations,
// not a proven tokenizer/price ceiling. The request count and deadline are gates.
type Policy struct {
	RunID, TaskID, SessionID, ProfileID, ProfileDigest, ContractDigest string
	Provider, Model, Version, Deadline                                 string
	TaskTokens, RunTokens, TaskRequests                                int64
	WrapUpTokens                                                       int64
	State                                                              string
}

type Request struct {
	ID            string `json:"id"`
	Digest        string `json:"digest"`
	API           string `json:"api"`
	Provider      string `json:"provider"`
	Model         string `json:"model"`
	InputEstimate int64  `json:"inputEstimate"`
	MaxOutput     int64  `json:"maxOutput"`
}

type Grant struct {
	ID             string `json:"id"`
	ReservedTokens int64  `json:"reservedTokens"`
	MaxOutput      int64  `json:"maxOutput"`
}

type Begin struct {
	ID     string `json:"id"`
	Digest string `json:"digest"`
}

type Settlement struct {
	ID       string            `json:"id"`
	State    string            `json:"state"`
	Tokens   model.TokenCounts `json:"tokens"`
	Terminal bool              `json:"terminal"`
}

type Record struct {
	Request              Request     `json:"request"`
	RunID                string      `json:"runId"`
	Grant                Grant       `json:"grant"`
	State                string      `json:"state"`
	SentDigest           *string     `json:"sentDigest"`
	Settlement           *Settlement `json:"settlement"`
	Overrun              bool        `json:"overrun"`
	CreatedAt, UpdatedAt string
}

// Authority never receives prompt text, payloads, headers or credentials.
type Authority interface {
	Reserve(context.Context, Request) (Grant, error)
	Begin(context.Context, Begin) error
	Settle(context.Context, Settlement) error
}

type Outcome struct {
	Confirmed bool
	Overrun   bool
	Requests  int
	Usage     model.Usage
}
