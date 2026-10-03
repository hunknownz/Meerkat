package model

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
)

// CompletionEvidence is owner-only evidence after verified executor shutdown.
// It records one step, not a new execution request or an entire State snapshot.
type CompletionEvidence struct {
	SchemaVersion  int        `json:"schemaVersion"`
	ContractDigest string     `json:"contractDigest"`
	BeforeTask     Task       `json:"beforeTask"`
	BeforeRun      Run        `json:"beforeRun"`
	BeforeSession  Session    `json:"beforeSession"`
	AfterTask      Task       `json:"afterTask"`
	AfterRun       Run        `json:"afterRun"`
	AfterSession   Session    `json:"afterSession"`
	Deliveries     []Delivery `json:"deliveries"`
	Reviews        []Review   `json:"reviews"`
	ProcessGroupID int        `json:"processGroupId"`
	CreatedAt      string     `json:"createdAt"`
}

type CompletionRecord struct {
	Evidence CompletionEvidence `json:"evidence"`
	Digest   string             `json:"digest"`
	State    string             `json:"state"` // pending, settled, recovered
}

type RecoveryProposal struct {
	SchemaVersion    int    `json:"schemaVersion"`
	TaskID           string `json:"taskId"`
	RunID            string `json:"runId"`
	CompletionDigest string `json:"completionDigest"`
	EvidenceDigest   string `json:"evidenceDigest"`
}

type RecoveryInspection struct {
	SchemaVersion int               `json:"schemaVersion"`
	TaskID        string            `json:"taskId"`
	RunID         string            `json:"runId,omitempty"`
	Status        string            `json:"status"` // verified, blocked
	Checks        []RecoveryCheck   `json:"checks"`
	Proposal      *RecoveryProposal `json:"proposal,omitempty"`
}

type RecoveryCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"` // pass, blocked, unknown
	Reason string `json:"reason,omitempty"`
}

type RecoveryInput struct {
	Proposal         RecoveryProposal `json:"proposal"`
	RequestID        string           `json:"requestId"`
	AuthorizationRef string           `json:"authorizationRef"`
	Apply            bool             `json:"apply"`
}

type RecoveryDecision struct {
	RecoveryInput
	CreatedAt string `json:"createdAt"`
}

// Public recovery evidence contains no private process or authority fields.
type RecoverySummary struct {
	RunID        string  `json:"runId"`
	Role         string  `json:"role"`
	State        string  `json:"state"`
	CandidateSHA string  `json:"candidateSha"`
	RecordedAt   string  `json:"recordedAt"`
	RequestID    *string `json:"requestId"`
	RecoveredAt  *string `json:"recoveredAt"`
}

type RecoveryReceipt struct {
	RequestID string `json:"requestId"`
	TaskID    string `json:"taskId"`
	RunID     string `json:"runId"`
	CreatedAt string `json:"createdAt"`
}

func RecoveryDigest(v any) string {
	b, e := json.Marshal(v)
	if e != nil {
		return ""
	}
	return fmt.Sprintf("%x", sha256.Sum256(b))
}
func ValidRecoveryProposal(p RecoveryProposal) bool {
	return p.SchemaVersion == 1 && budgetUUID.MatchString(p.TaskID) && budgetUUID.MatchString(p.RunID) &&
		budgetDigest.MatchString(p.CompletionDigest) && budgetDigest.MatchString(p.EvidenceDigest)
}
func ValidRecoveryInput(v RecoveryInput) bool {
	return ValidRecoveryProposal(v.Proposal) && budgetUUID.MatchString(v.RequestID) && v.Apply && ValidBudgetText(v.AuthorizationRef)
}
