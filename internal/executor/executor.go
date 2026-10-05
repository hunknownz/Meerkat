// Package executor runs one bounded role step through an executor child CLI (currently only Pi).
//
// Executors never return raw provider errors, transcripts, tool arguments or credential values: errors
// carry a stable category and a fixed message; callbacks only receive model.RunEvent summaries and the
// identity of the process group the executor owns.
package executor

import (
	"context"
	"fmt"
	"time"

	"github.com/hunknownz/Meerkat/internal/checkpoint"
	"github.com/hunknownz/Meerkat/internal/model"
	"github.com/hunknownz/Meerkat/internal/requestbudget"
)

// Failure categories. All failures leave the worktree untouched (dirty code is preserved).
const (
	CatInvalidRequest   = "invalid_request"
	CatSpawn            = "spawn_error"
	CatCanceled         = "canceled"
	CatPauseRequested   = "pause_requested"
	CatWallTimeout      = "wall_timeout"
	CatTokenLimit       = "token_limit"
	CatProviderError    = "provider_error"
	CatExit             = "pi_exit"
	CatProtocol         = "protocol"
	CatReportMissing    = "report_missing"
	CatReportInvalid    = "report_invalid"
	CatReportStale      = "report_stale"
	CatReviewerMutation = "reviewer_mutation"
	CatBranchChanged    = "branch_changed"
	CatNoCommit         = "no_commit"
	CatDirty            = "dirty"
	CatDecisionMismatch = "decision_mismatch"
	CatGit              = "git_error"
	CatSessionUnknown   = "session_unknown"
	CatBudgetUnknown    = "budget_unknown"
	CatBudgetGate       = "budget_gate_unavailable"
)

var catMessages = map[string]string{
	CatSpawn: "executor process could not be started", CatCanceled: "run canceled", CatPauseRequested: "graceful pause requested",
	CatWallTimeout: "wall-time budget exhausted", CatTokenLimit: "token budget exhausted",
	CatProviderError: "provider reported an error", CatExit: "executor exited with failure",
	CatProtocol: "executor did not complete its JSON protocol", CatReportMissing: "role report missing",
	CatReportInvalid: "role report invalid", CatReportStale: "role report does not match candidate or context",
	CatReviewerMutation: "reviewer changed branch, HEAD or working tree", CatBranchChanged: "branch changed during run",
	CatNoCommit: "no new commit on task branch", CatDirty: "working tree left dirty",
	CatDecisionMismatch: "report decision does not match Git state", CatGit: "git inspection failed",
	CatSessionUnknown: "session outcome could not be verified",
	CatBudgetUnknown:  "request budget outcome could not be verified",
	CatBudgetGate:     "request budget bridge is unavailable",
}

// Error is a safe executor error. Message is fixed text or names a field, never a raw value.
type Error struct {
	Category string
	Message  string
}

func (e *Error) Error() string { return "executor: " + e.Category + ": " + e.Message }

func fail(cat string) *Error { return &Error{Category: cat, Message: catMessages[cat]} }

func invalid(format string, a ...any) *Error {
	return &Error{Category: CatInvalidRequest, Message: fmt.Sprintf(format, a...)}
}

// Request is one frozen role step.
type Request struct {
	RunID           string        // optional safe id; generated when empty
	Profile         model.Profile // frozen profile
	Context         model.Context // frozen context version
	Role            string
	Worktree        string
	TaskBrief       string
	ReportPath      string // absolute, outside the worktree, new file in a private directory
	ExpectedSHA     string // HEAD the step must start from
	RoleBaselineSHA string // original role baseline; may differ only for a verified checkpoint continuation
	ContextDigest   string // digest the report must bind to ("" means null)
	RemainingTokens int64
	RemainingWall   time.Duration
	Env             []string                // child environment; nil means os.Environ()
	Session         *SessionBinding         // private, verified history for a stateful executor
	Budget          requestbudget.Authority // private request authority, never serialized
	WrapUpTokens    int64
	WrapUpBefore    time.Duration
	WrapUp          <-chan struct{}     // authority notification; adapter converts it to its protocol
	Checkpoint      *checkpoint.Binding // private authority for an exact dirty start
	Controls        *ControlBinding     // private, durable Run controls owned by the scheduler
}

// Capabilities describe implemented operations, not planned adapters or policy.
type Capabilities struct {
	Protocol             string
	PersistentSessions   bool
	BidirectionalControl bool
	UsageEvents          bool
	RequestBudgetGate    bool
	GracefulWrapUp       bool
	GracefulPause        bool
	QueuedFollowUp       bool
}

// SessionBinding is an opaque, private executor history binding. The scheduler
// verifies the contract; the adapter verifies its backend's identity and format.
type SessionBinding struct {
	ID, ProviderID, File, Digest, Worktree string
}

type SessionSnapshot struct {
	ProviderID, Digest string
}

// StatefulExecutor keeps protocol/format inspection out of the scheduler. Old
// executors continue through Execute without advertising session support.
type StatefulExecutor interface {
	Executor
	Capabilities() Capabilities
	InitializeSession(SessionBinding) (SessionSnapshot, error)
	InspectSession(SessionBinding) (SessionSnapshot, error)
}

// SessionOutcome is private evidence after shutdown, not proof of code delivery.
type SessionOutcome struct {
	ID, ProviderID, Digest string
	Confirmed              bool
}

// Process is the identity of the process group owned by one run.
type Process struct {
	Executor  string    `json:"executor"`
	PID       int       `json:"pid"`
	PGID      int       `json:"pgid"`
	Host      string    `json:"host"`
	StartedAt time.Time `json:"startedAt"`
}

// Check is one local check result in a role report.
type Check struct {
	Command string `json:"command"`
	Result  string `json:"result"`
}

// Finding is one reviewer finding.
type Finding struct {
	ID      string `json:"id"`
	Summary string `json:"summary"`
	Path    string `json:"path,omitempty"`
	Line    *int   `json:"line,omitempty"`
}

// Report is a validated role report.
type Report struct {
	CandidateSHA  string    `json:"candidateSha"`
	ContextDigest *string   `json:"contextDigest"`
	Summary       string    `json:"summary"`
	Checks        []Check   `json:"checks"`
	KnownGaps     []string  `json:"knownGaps"`
	Verdict       string    `json:"verdict,omitempty"`
	Findings      []Finding `json:"findings,omitempty"`
	Decision      string    `json:"decision,omitempty"`
}

// Result is the outcome of one step. Category is empty on success.
type Result struct {
	RunID          string              `json:"runId"`
	Executor       string              `json:"executor"`
	Role           string              `json:"role"`
	Model          model.ModelSnapshot `json:"modelSnapshot"`
	StartedAt      time.Time           `json:"startedAt"`
	EndedAt        time.Time           `json:"endedAt"`
	Process        *Process            `json:"process,omitempty"`
	ExitCode       *int                `json:"exitCode"`
	Signal         string              `json:"signal,omitempty"`
	Usage          model.Usage         `json:"usage"`
	Outcome        string              `json:"outcome"` // model.RunSucceeded | RunFailed | RunStopped
	Category       string              `json:"category,omitempty"`
	BaselineSHA    string              `json:"baselineSha"`
	ResultSHA      string              `json:"resultSha"`
	Committed      bool                `json:"committed"`
	Clean          bool                `json:"clean"`
	Report         *Report             `json:"report"`
	Session        *SessionOutcome     `json:"-"`
	CheckpointSafe bool                `json:"-"` // confirmed idle exit with no unresolved/failed tool operation
}

// Executor runs one role step.
type Executor interface {
	Name() string
	Validate(p model.Profile) error
	Execute(ctx context.Context, req Request, onEvent func(model.RunEvent), onStart func(Process)) (Result, error)
}

var registry = map[string]Executor{"pi": NewPi()}

// Lookup returns the executor for a profile's executor field. Missing (legacy) means Pi.
func Lookup(name string) (Executor, error) {
	if name == "" {
		name = "pi"
	}
	if e, ok := registry[name]; ok {
		return e, nil
	}
	return nil, invalid("unsupported executor")
}
