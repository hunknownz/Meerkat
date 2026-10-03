package model

const (
	SessionIdle    = "idle"
	SessionRunning = "running"
	SessionUnknown = "unknown"
)

// Session is private authority, separate from legacy State and public snapshots.
// FileRef is relative to the private data directory; it contains executor history.
type Session struct {
	ID             string  `json:"id"`
	TaskID         string  `json:"taskId"`
	Role           string  `json:"role"`
	Executor       string  `json:"executor"`
	ProfileID      string  `json:"profileId"`
	ProfileDigest  string  `json:"profileDigest"`
	ContractDigest string  `json:"contractDigest"`
	ProviderID     string  `json:"providerId"`
	FileRef        string  `json:"fileRef"`
	FileDigest     string  `json:"fileDigest"`
	LastSHA        string  `json:"lastSha"`
	State          string  `json:"state"`
	ActiveRunID    *string `json:"activeRunId"`
	CreatedAt      string  `json:"createdAt"`
	UpdatedAt      string  `json:"updatedAt"`
}
