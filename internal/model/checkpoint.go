package model

// Checkpoint is private authority. Public projections omit files and digests.
type Checkpoint struct {
	ID             string  `json:"id"`
	TaskID         string  `json:"taskId"`
	RunID          string  `json:"runId"`
	SessionID      string  `json:"sessionId"`
	SessionDigest  string  `json:"sessionDigest"`
	ContractDigest string  `json:"contractDigest"`
	ProfileDigest  string  `json:"profileDigest"`
	Role           string  `json:"role"`
	Purpose        string  `json:"purpose"`
	Worktree       string  `json:"worktree"`
	BaselineSHA    string  `json:"baselineSha"`
	HeadSHA        string  `json:"headSha"`
	Branch         string  `json:"branch"`
	FileRef        string  `json:"fileRef"`
	FileDigest     string  `json:"fileDigest"`
	FileCount      int     `json:"fileCount"`
	LastEventSeq   int     `json:"lastEventSeq"`
	State          string  `json:"state"` // saved or consumed; consumed is never replayable
	ResumedRunID   *string `json:"resumedRunId"`
	CreatedAt      string  `json:"createdAt"`
}

type CheckpointSummary struct {
	ID           string  `json:"id"`
	RunID        string  `json:"runId"`
	Role         string  `json:"role"`
	HeadSHA      string  `json:"headSha"`
	FileCount    int     `json:"fileCount"`
	State        string  `json:"state"`
	ResumedRunID *string `json:"resumedRunId"`
	CreatedAt    string  `json:"createdAt"`
}
