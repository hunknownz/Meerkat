package model

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

const MaxTaskTokens int64 = 10_000_000
const MaxTaskWallSeconds int64 = 86400

type BudgetIncrease struct {
	TaskID         string `json:"taskId"`
	AddTokens      int64  `json:"addTokens"`
	AddWallSeconds int64  `json:"addWallSeconds"`
	Reason         string `json:"reason"`
}

// BudgetProposal is an owner-only, read-only proposal, never an authorization.
type BudgetProposal struct {
	BudgetIncrease
	SchemaVersion      int    `json:"schemaVersion"`
	Revision           int64  `json:"revision"`
	CurrentTokens      int64  `json:"currentTokens"`
	CurrentWallSeconds int64  `json:"currentWallSeconds"`
	ContractDigest     string `json:"contractDigest"`
	EvidenceDigest     string `json:"evidenceDigest"`
}

type BudgetDecisionInput struct {
	Proposal         BudgetProposal `json:"proposal"`
	RequestID        string         `json:"requestId"`
	AuthorizationRef string         `json:"authorizationRef"`
	Apply            bool           `json:"apply"`
}

// AuthorizationRef and the proposal evidence remain private audit records.
type BudgetDecision struct {
	BudgetDecisionInput
	CreatedAt string `json:"createdAt"`
}

type BudgetDecisionSummary struct {
	RequestID      string `json:"requestId"`
	Revision       int64  `json:"revision"`
	AddTokens      int64  `json:"addTokens"`
	AddWallSeconds int64  `json:"addWallSeconds"`
	Reason         string `json:"reason"`
	CreatedAt      string `json:"createdAt"`
}

type BudgetAuthorization struct {
	TaskID                string                  `json:"taskId"`
	OriginalTokens        int64                   `json:"originalTokens"`
	OriginalWallSeconds   int64                   `json:"originalWallSeconds"`
	AddedTokens           int64                   `json:"addedTokens"`
	AddedWallSeconds      int64                   `json:"addedWallSeconds"`
	AuthorizedTokens      int64                   `json:"authorizedTokens"`
	AuthorizedWallSeconds int64                   `json:"authorizedWallSeconds"`
	Revision              int64                   `json:"revision"`
	Decisions             []BudgetDecisionSummary `json:"decisions"`
}

var budgetUUID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
var budgetDigest = regexp.MustCompile(`^[0-9a-f]{64}$`)

func ValidBudgetText(s string) bool {
	return s != "" && strings.TrimSpace(s) == s && len(s) <= 512 && utf8.ValidString(s) &&
		!strings.ContainsFunc(s, unicode.IsControl) && !LooksLikeCredential(s)
}
func ValidBudgetIncrease(v BudgetIncrease) bool {
	return budgetUUID.MatchString(v.TaskID) && v.AddTokens >= 0 && v.AddTokens <= MaxTaskTokens &&
		v.AddWallSeconds >= 0 && v.AddWallSeconds <= MaxTaskWallSeconds && (v.AddTokens > 0 || v.AddWallSeconds > 0) && ValidBudgetText(v.Reason)
}
func ValidBudgetProposal(p BudgetProposal) bool {
	return ValidBudgetIncrease(p.BudgetIncrease) && p.SchemaVersion == 1 && p.Revision >= 0 && p.Revision < 512 &&
		p.CurrentTokens > 0 && p.CurrentTokens <= MaxTaskTokens-p.AddTokens && p.CurrentWallSeconds > 0 && p.CurrentWallSeconds <= MaxTaskWallSeconds-p.AddWallSeconds &&
		budgetDigest.MatchString(p.ContractDigest) && budgetDigest.MatchString(p.EvidenceDigest)
}
func ValidBudgetDecision(v BudgetDecisionInput) bool {
	return ValidBudgetProposal(v.Proposal) && budgetUUID.MatchString(v.RequestID) && v.Apply && ValidBudgetText(v.AuthorizationRef)
}
