package model

// SessionSummary omits provider history identifiers, private files and digests.
type SessionSummary struct {
	ID          string  `json:"id"`
	Role        string  `json:"role"`
	Executor    string  `json:"executor"`
	State       string  `json:"state"`
	ActiveRunID *string `json:"activeRunId"`
	LastSHA     string  `json:"lastSha"`
	UpdatedAt   string  `json:"updatedAt"`
}

// BudgetEvidence summarizes the request ledger, not a monetary billing limit.
// AvailableTokens includes conservative reservations. In monitor mode it is nil
// because there is no hard task token cap; otherwise nil means unverifiable.
type BudgetEvidence struct {
	Mode             string `json:"mode,omitempty"`
	Warning          bool   `json:"warning,omitempty"`
	AuthorizedTokens int64  `json:"authorizedTokens"`
	AvailableTokens  *int64 `json:"availableTokens"`
	ConfirmedTokens  int64  `json:"confirmedTokens"`
	ReservedTokens   int64  `json:"reservedTokens"`
	Requests         int    `json:"requests"`
	PendingRequests  int    `json:"pendingRequests"`
	UnknownRequests  int    `json:"unknownRequests"`
	Overrun          bool   `json:"overrun"`
}
