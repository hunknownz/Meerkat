package model

// Operation and member states describe scheduling, not code acceptance.
const (
	OperationQueued    = "queued"
	OperationRunning   = "running"
	OperationCompleted = "completed"
	OperationUnknown   = "unknown"
	MemberQueued       = "queued"
	MemberRunning      = "running"
	MemberCompleted    = "completed"
	MemberUnknown      = "unknown"
)

// DispatchRequest binds an explicit idempotency key to selected frozen tasks.
type DispatchRequest struct {
	RequestID   string   `json:"requestId"`
	TaskIDs     []string `json:"taskIds"`
	Resume      bool     `json:"resume,omitempty"`
	Acknowledge bool     `json:"acknowledge,omitempty"`
}

// OperationTask records one member's scheduling and actual work outcome.
type OperationTask struct {
	TaskID         string  `json:"taskId"`
	State          string  `json:"state"`
	ContractDigest string  `json:"contractDigest,omitempty"`
	TaskState      string  `json:"taskState"`
	StateReason    *string `json:"stateReason"`
	CandidateSHA   *string `json:"candidateSha"`
	ResumeRole     *string `json:"resumeRole"`
	StartedAt      *string `json:"startedAt"`
	EndedAt        *string `json:"endedAt"`
}

// Operation is a durable dispatch. Completed means every member has a result;
// inspect Tasks for delivered, blocked, failed or stopped outcomes.
type Operation struct {
	SchemaVersion   int             `json:"schemaVersion"`
	ID              string          `json:"id"`
	RequestID       string          `json:"requestId"`
	RequestDigest   string          `json:"requestDigest,omitempty"`
	State           string          `json:"state"`
	Mode            string          `json:"mode"`
	CreatedAt       string          `json:"createdAt"`
	UpdatedAt       string          `json:"updatedAt"`
	StartedAt       *string         `json:"startedAt"`
	EndedAt         *string         `json:"endedAt"`
	Reason          *string         `json:"reason"`
	FrozenFixRounds int             `json:"frozenFixRounds"`
	Tasks           []OperationTask `json:"tasks"`
}

// Public omits internal matching digests while preserving unknown results.
func (o Operation) Public() Operation {
	o.RequestDigest = ""
	o.Tasks = append([]OperationTask{}, o.Tasks...)
	for i := range o.Tasks {
		o.Tasks[i].ContractDigest = ""
	}
	return o
}

func OperationSettled(state string) bool {
	return state == OperationCompleted || state == OperationUnknown
}

// DispatchReceipt acknowledges persistence, not a task's final delivery.
type DispatchReceipt struct {
	Accepted  bool      `json:"accepted"`
	Duplicate bool      `json:"duplicate"`
	Operation Operation `json:"operation"`
}
