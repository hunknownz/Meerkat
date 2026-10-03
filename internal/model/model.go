// Package model holds the typed Meerkat 0.3.0 workflow records and their stable JSON field names.
//
// The JSON names match the Node 0.2.x workflow store (workflow/store.mjs FIELDS) and the public
// snapshot served by GET /api/workflow (workflow/core.mjs readWorkflow), so prepared/executed state
// and snapshots stay compatible while the core is ported. Unknown values are represented with
// pointers (JSON null) and are never coerced to zero.
package model

import (
	"encoding/json"
	"slices"
)

// SchemaVersion is the version of the complete-state and public snapshot shapes.
const SchemaVersion = 1

// Bounded limits inherited from the 0.2.x store.
const (
	MaxRunEvents    = 50
	MaxStopRequests = 128
	MaxStopReceipts = 1024
)

// Settings defaults and bounds.
const (
	DefaultMaxConcurrency = 2
	DefaultMaxFixRounds   = 2
	MinConcurrency        = 1
	MaxConcurrency        = 4
	MaxFixRoundsLimit     = 2
)

// Roles is the ordered list of pipeline roles.
var Roles = []string{"developer", "reviewer", "polisher"}

// Task states.
const (
	TaskReady          = "ready"
	TaskQueued         = "queued"
	TaskBlocked        = "blocked"
	TaskImplementing   = "implementing"
	TaskFirstDelivery  = "first_delivery"
	TaskChecking       = "checking"
	TaskFinalCandidate = "final_candidate"
	TaskPolishing      = "polishing"
	TaskRechecking     = "rechecking"
	TaskFixing         = "fixing"
	TaskDelivered      = "delivered"
	TaskFailed         = "failed"
	TaskStopped        = "stopped"
	TaskUnknown        = "unknown"
)

// Run states.
const (
	RunStarting    = "starting"
	RunRunning     = "running"
	RunStopping    = "stopping"
	RunSucceeded   = "succeeded"
	RunFailed      = "failed"
	RunStopped     = "stopped"
	RunInterrupted = "interrupted"
	RunUnknown     = "unknown"
)

// Record origins.
const (
	OriginNative       = "native"
	OriginLegacyImport = "legacy_import"
)

var (
	// TaskStates lists every valid task state.
	TaskStates = []string{TaskReady, TaskQueued, TaskBlocked, TaskImplementing, TaskFirstDelivery, TaskChecking,
		TaskFinalCandidate, TaskPolishing, TaskRechecking, TaskFixing, TaskDelivered, TaskFailed, TaskStopped, TaskUnknown}
	// ActiveTaskStates are "in progress under a controller"; at most one per worktree.
	ActiveTaskStates = []string{TaskImplementing, TaskFirstDelivery, TaskChecking, TaskFinalCandidate, TaskPolishing,
		TaskRechecking, TaskFixing}
	// RunStates lists every valid run state.
	RunStates = []string{RunStarting, RunRunning, RunStopping, RunSucceeded, RunFailed, RunStopped, RunInterrupted, RunUnknown}
	// ActiveRunStates mean "a process may be live".
	ActiveRunStates = []string{RunStarting, RunRunning, RunStopping}
	// TerminalRunStates are final run outcomes.
	TerminalRunStates = []string{RunSucceeded, RunFailed, RunStopped, RunInterrupted}
	// DeliveryStates lists delivery states.
	DeliveryStates = []string{"first", "final_candidate", "delivered"}
)

// IsRole reports whether r is a pipeline role.
func IsRole(r string) bool { return slices.Contains(Roles, r) }

// IsActiveRunState reports whether s means a process may be live.
func IsActiveRunState(s string) bool { return slices.Contains(ActiveRunStates, s) }

// IsActiveTaskState reports whether s is an in-progress task state.
func IsActiveTaskState(s string) bool { return slices.Contains(ActiveTaskStates, s) }

// IsTerminalRunState reports whether s is a final run state.
func IsTerminalRunState(s string) bool { return slices.Contains(TerminalRunStates, s) }

// Project groups repositories.
type Project struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Repositories []string `json:"repositories"`
	CreatedAt    string   `json:"createdAt"`
	UpdatedAt    string   `json:"updatedAt"`
}

// ContextSource is a reference attached to a context version (query/fragment stripped).
type ContextSource struct {
	URL   string `json:"url,omitempty"`
	Title string `json:"title,omitempty"`
	Hash  string `json:"hash,omitempty"`
}

// Context is one immutable version of a context family (id + version). Text is private.
type Context struct {
	ID        string          `json:"id"`
	ProjectID string          `json:"projectId"`
	Version   int             `json:"version"`
	Digest    string          `json:"digest"`
	Text      string          `json:"text"`
	Sources   []ContextSource `json:"sources"`
	CreatedAt string          `json:"createdAt"`
}

// PublicContext is the context projection exposed in snapshots (no text).
type PublicContext struct {
	ID        string          `json:"id"`
	ProjectID string          `json:"projectId"`
	Version   int             `json:"version"`
	Digest    string          `json:"digest"`
	Sources   []ContextSource `json:"sources"`
	CreatedAt string          `json:"createdAt"`
}

// Public returns the text-free projection.
func (c Context) Public() PublicContext {
	return PublicContext{ID: c.ID, ProjectID: c.ProjectID, Version: c.Version, Digest: c.Digest, Sources: c.Sources, CreatedAt: c.CreatedAt}
}

// Ref returns the exact reference to this version.
func (c Context) Ref() ContextRef { return ContextRef{ID: c.ID, Version: c.Version, Digest: c.Digest} }

// ContextRef binds a task/run/delivery to an exact context version and digest.
type ContextRef struct {
	ID      string `json:"id"`
	Version int    `json:"version"`
	Digest  string `json:"digest"`
}

// ProfileLimits are a frozen profile's per-run limits.
type ProfileLimits struct {
	MaxWallSeconds float64 `json:"maxWallSeconds,omitempty"`
	MaxTokens      float64 `json:"maxTokens,omitempty"`
}

// Profile is a frozen, private executor profile snapshot. AuthEnv is an environment variable NAME,
// never a credential value. Never expose Profile publicly; use Public().
type Profile struct {
	ID           string        `json:"id"`
	ProjectID    string        `json:"projectId"`
	Role         string        `json:"role"`
	Executor     string        `json:"executor,omitempty"`
	Provider     string        `json:"provider"`
	Model        string        `json:"model"`
	AuthEnv      string        `json:"authEnv,omitempty"`
	Instructions []string      `json:"instructions"`
	Limits       ProfileLimits `json:"limits"`
	PiCommand    []string      `json:"piCommand"`
	ConfigFile   string        `json:"configFile,omitempty"`
	ConfigDigest string        `json:"configDigest,omitempty"`
	CreatedAt    string        `json:"createdAt"`
}

// PublicProfile carries only the safe profile fields.
type PublicProfile struct {
	ID        string        `json:"id"`
	ProjectID string        `json:"projectId"`
	Role      string        `json:"role"`
	Executor  string        `json:"executor,omitempty"`
	Provider  string        `json:"provider"`
	Model     string        `json:"model"`
	Limits    ProfileLimits `json:"limits"`
}

// Public returns the safe projection (no authEnv, command, config path or instructions).
func (p Profile) Public() PublicProfile {
	return PublicProfile{ID: p.ID, ProjectID: p.ProjectID, Role: p.Role, Executor: p.Executor, Provider: p.Provider, Model: p.Model, Limits: p.Limits}
}

// IssueRef links a task to its source Issue.
type IssueRef struct {
	URL       string `json:"url"`
	Title     string `json:"title"`
	UpdatedAt string `json:"updatedAt,omitempty"`
	BodyHash  string `json:"bodyHash,omitempty"`
}

// Budget bounds one task.
type Budget struct {
	MaxTokens      int64          `json:"maxTokens"`
	MaxWallSeconds int64          `json:"maxWallSeconds"`
	MaxFixRounds   int            `json:"maxFixRounds"`
	StageReserves  *StageReserves `json:"stageReserves,omitempty"`
}

// StageReserves is explicit frozen configuration, not a prediction of model use.
// Wrap-up is part of the current role's allowance, never extra authorization.
type StageReserves struct {
	ReviewTokens  int64 `json:"reviewTokens"`
	FixTokens     int64 `json:"fixTokens"`
	PolishTokens  int64 `json:"polishTokens"`
	WrapUpTokens  int64 `json:"wrapUpTokens"`
	WrapUpSeconds int64 `json:"wrapUpSeconds"`
}

// DefaultBudget mirrors workflow/core.mjs DEFAULT_BUDGET.
func DefaultBudget() Budget { return Budget{MaxTokens: 500_000, MaxWallSeconds: 1800, MaxFixRounds: 2} }

// Task is a prepared unit of work with a frozen contract (context + profiles).
type Task struct {
	ID            string            `json:"id"`
	ProjectID     string            `json:"projectId"`
	ChangeID      *string           `json:"changeId,omitempty"`
	Repository    string            `json:"repository"`
	Worktree      string            `json:"worktree"`
	Branch        *string           `json:"branch,omitempty"`
	Title         string            `json:"title"`
	Goal          string            `json:"goal"`
	Scope         []string          `json:"scope"`
	Acceptance    []string          `json:"acceptance"`
	Dependencies  []string          `json:"dependencies"`
	ContextRef    ContextRef        `json:"contextRef"`
	ProfileIDs    map[string]string `json:"profileIds"`
	State         string            `json:"state"`
	RecordedState string            `json:"recordedState,omitempty"`
	StateReason   *string           `json:"stateReason"`
	ResumeRole    *string           `json:"resumeRole"`
	CandidateSha  *string           `json:"candidateSha"`
	BaselineSha   *string           `json:"baselineSha"`
	CreatedAt     string            `json:"createdAt"`
	UpdatedAt     string            `json:"updatedAt"`
	IssueRef      *IssueRef         `json:"issueRef,omitempty"`
	Budget        *Budget           `json:"budget,omitempty"`
	Origin        string            `json:"origin,omitempty"`
}

// RunEvent is a bounded, secret-free event summary.
type RunEvent struct {
	Type       string `json:"type"`
	Summary    string `json:"summary"`
	ObservedAt string `json:"observedAt"`
}

// ModelSnapshot records the profile/provider/model used by a run.
type ModelSnapshot struct {
	ProfileID string `json:"profileId"`
	Provider  string `json:"provider"`
	Model     string `json:"model"`
}

// TimeMetrics are nullable run timing measurements. Wall time and agent/model time are kept separate;
// unmeasured values stay null.
type TimeMetrics struct {
	QueuedAt     *string  `json:"queuedAt"`
	QueueSeconds *float64 `json:"queueSeconds"`
	WallSeconds  *float64 `json:"wallSeconds"`
	ModelSeconds *float64 `json:"modelSeconds"`
	TestSeconds  *float64 `json:"testSeconds"`
	FixRound     *int     `json:"fixRound"`
}

// Run is one executor invocation for a task role. PID/Host/LeaseToken are private process facts.
type Run struct {
	ID               string          `json:"id"`
	AgentID          string          `json:"agentId,omitempty"`
	TaskID           string          `json:"taskId"`
	Role             string          `json:"role"`
	ProfileID        string          `json:"profileId,omitempty"`
	Executor         string          `json:"executor,omitempty"`
	ModelSnapshot    *ModelSnapshot  `json:"modelSnapshot,omitempty"`
	ContextRef       *ContextRef     `json:"contextRef,omitempty"`
	State            string          `json:"state"`
	RecordedState    string          `json:"recordedState,omitempty"`
	PID              *int            `json:"pid,omitempty"`
	Host             string          `json:"host,omitempty"`
	ProcessStartedAt string          `json:"processStartedAt,omitempty"`
	LeaseToken       string          `json:"-"`
	HeartbeatAt      string          `json:"heartbeatAt,omitempty"`
	StartedAt        string          `json:"startedAt,omitempty"`
	EndedAt          *string         `json:"endedAt,omitempty"`
	UpdatedAt        string          `json:"updatedAt"`
	Events           []RunEvent      `json:"events"`
	Summary          json.RawMessage `json:"summary,omitempty"`
	Usage            *Usage          `json:"usage,omitempty"`
	Metrics          *TimeMetrics    `json:"metrics,omitempty"`
	Origin           string          `json:"origin,omitempty"`
}

// Public returns the run without private process facts.
func (r Run) Public() Run {
	r.PID = nil
	r.Host = ""
	r.ProcessStartedAt = ""
	r.LeaseToken = ""
	return r
}

// Delivery is a candidate commit produced by the pipeline.
type Delivery struct {
	ID           string          `json:"id"`
	TaskID       string          `json:"taskId"`
	ContextRef   ContextRef      `json:"contextRef"`
	CandidateSha string          `json:"candidateSha"`
	Repository   string          `json:"repository"`
	RunIDs       []string        `json:"runIds"`
	Checks       json.RawMessage `json:"checks"`
	KnownGaps    []string        `json:"knownGaps"`
	State        string          `json:"state"`
	CreatedAt    string          `json:"createdAt"`
	UpdatedAt    string          `json:"updatedAt"`
}

// Review is an immutable reviewer verdict bound to an exact SHA and context digest.
type Review struct {
	ID            string          `json:"id"`
	TaskID        string          `json:"taskId"`
	RunID         string          `json:"runId"`
	CandidateSha  string          `json:"candidateSha"`
	ContextDigest string          `json:"contextDigest"`
	Verdict       string          `json:"verdict"`
	Findings      json.RawMessage `json:"findings"`
	Checks        json.RawMessage `json:"checks"`
	CreatedAt     string          `json:"createdAt"`
}

// State is the complete authoritative state in the 0.2.x state.json shape.
type State struct {
	SchemaVersion int        `json:"schemaVersion"`
	Projects      []Project  `json:"projects"`
	Contexts      []Context  `json:"contexts"`
	Tasks         []Task     `json:"tasks"`
	Runs          []Run      `json:"runs"`
	Deliveries    []Delivery `json:"deliveries"`
	Reviews       []Review   `json:"reviews"`
	Profiles      []Profile  `json:"profiles"`
}

// EmptyState returns a truthful empty state.
func EmptyState() *State {
	return &State{SchemaVersion: SchemaVersion, Projects: []Project{}, Contexts: []Context{}, Tasks: []Task{}, Runs: []Run{},
		Deliveries: []Delivery{}, Reviews: []Review{}, Profiles: []Profile{}}
}

// Settings are future-run settings.
type Settings struct {
	MaxConcurrency  int                          `json:"maxConcurrency"`
	MaxFixRounds    int                          `json:"maxFixRounds"`
	DefaultProfiles map[string]map[string]string `json:"defaultProfiles"`
	UpdatedAt       string                       `json:"updatedAt,omitempty"`
}

// DefaultSettings returns maxConcurrency 2, maxFixRounds 2, defaultProfiles {}.
func DefaultSettings() Settings {
	return Settings{MaxConcurrency: DefaultMaxConcurrency, MaxFixRounds: DefaultMaxFixRounds, DefaultProfiles: map[string]map[string]string{}}
}

// SettingsPatch is a partial settings update; nil fields are unchanged.
type SettingsPatch struct {
	MaxConcurrency  *int                         `json:"maxConcurrency,omitempty"`
	MaxFixRounds    *int                         `json:"maxFixRounds,omitempty"`
	DefaultProfiles map[string]map[string]string `json:"defaultProfiles,omitempty"`
}

// StopRequest is a pending request to stop one run.
type StopRequest struct {
	Type      string `json:"type"`
	RequestID string `json:"requestId"`
	RunID     string `json:"runId"`
	CreatedAt string `json:"createdAt"`
}

// Stop request states. Accepted (pending) never means stopped.
const (
	StopPending   = "pending"
	StopProcessed = "processed"
)

// StopReceipt acknowledges a stop request. Accepted only means the request was recorded; Outcome is the
// run's actual terminal state once processed (null while pending).
type StopReceipt struct {
	RequestID   string  `json:"requestId"`
	RunID       string  `json:"runId"`
	Accepted    bool    `json:"accepted"`
	Duplicate   bool    `json:"duplicate"`
	State       string  `json:"state"`
	CreatedAt   string  `json:"createdAt"`
	ProcessedAt *string `json:"processedAt"`
	Outcome     *string `json:"outcome"`
}

// ControllerLease is a held scheduler lease. Token is the fencing secret for this data directory.
type ControllerLease struct {
	Token       string `json:"-"`
	PID         int    `json:"pid"`
	Host        string `json:"host"`
	AcquiredAt  string `json:"acquiredAt"`
	HeartbeatAt string `json:"heartbeatAt"`
}

// Controller states.
const (
	ControllerRunning = "running"
	ControllerIdle    = "idle"
	ControllerUnknown = "unknown"
)

// ControllerStatus is the public controller projection.
type ControllerStatus struct {
	State       string  `json:"state"`
	HeartbeatAt *string `json:"heartbeatAt"`
}

// LeaseFacts are the private recorded facts about the current lease row (for recovery; not public).
type LeaseFacts struct {
	Present     bool   `json:"present"`
	PID         int    `json:"pid,omitempty"`
	Host        string `json:"host,omitempty"`
	AcquiredAt  string `json:"acquiredAt,omitempty"`
	HeartbeatAt string `json:"heartbeatAt,omitempty"`
	Stale       bool   `json:"stale"`
}

// WorktreeClaim records which task occupies a worktree and under which lease token it was claimed.
type WorktreeClaim struct {
	Worktree     string `json:"worktree"`
	TaskID       string `json:"taskId"`
	ClaimedAt    string `json:"claimedAt"`
	CurrentLease bool   `json:"currentLease"`
}
