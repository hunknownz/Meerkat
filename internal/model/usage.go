package model

import (
	"bytes"
	"encoding/json"
	"math"
)

// Usage completeness values.
const (
	UsageComplete = "complete"
	UsagePartial  = "partial"
	UsageUnknown  = "unknown"
)

// Usage sources.
const (
	UsageSourceExecutor     = "executor"
	UsageSourceLegacyState  = "legacy_state"
	UsageSourceLegacyRecipt = "legacy_run_receipt"
	UsageSourceUnknown      = "unknown"
)

// TokenCounts are independent nullable counters; nil means unknown, never zero.
type TokenCounts struct {
	Input             *int64 `json:"input"`
	Output            *int64 `json:"output"`
	CacheRead         *int64 `json:"cacheRead"`
	CacheWrite        *int64 `json:"cacheWrite"`
	Total             *int64 `json:"total"`
	AssistantMessages *int64 `json:"assistantMessages,omitempty"`
}

// Usage is one run's measured usage.
type Usage struct {
	Tokens            TokenCounts `json:"tokens"`
	UsageCompleteness string      `json:"usageCompleteness"`
	EstimatedCostUsd  *float64    `json:"estimatedCostUsd"`
	Source            string      `json:"source,omitempty"`
}

// AggregateUsage is the cumulative usage of a set of runs (workflow/core.mjs aggregateUsage).
type AggregateUsage struct {
	Tokens           TokenCounts `json:"tokens"`
	KnownSubtotal    int64       `json:"knownSubtotal"`
	Completeness     string      `json:"completeness"`
	RunsWithProcess  int         `json:"runsWithProcess"`
	EstimatedCostUsd *float64    `json:"estimatedCostUsd"`
}

// ValidCompleteness reports whether c is a known completeness value.
func ValidCompleteness(c string) bool {
	return c == UsageComplete || c == UsagePartial || c == UsageUnknown
}

// Measured reports whether every token counter and the total are known and usage is complete.
func (u *Usage) Measured() bool {
	if u == nil || u.UsageCompleteness != UsageComplete {
		return false
	}
	t := u.Tokens
	return t.Input != nil && t.Output != nil && t.CacheRead != nil && t.CacheWrite != nil && t.Total != nil
}

// HasUnknown reports whether any part of the usage is unknown (nil usage included).
func (u *Usage) HasUnknown() bool { return !u.Measured() || u.EstimatedCostUsd == nil }

// ParseLegacyUsage converts a 0.2.x usage view ({tokens:{...}, usageCompleteness, estimatedCostUsd}) like
// workflow/core.mjs sanitizeUsage: invalid or missing counters stay nil; never coerced to zero.
// A missing or malformed object yields nil (usage unknown).
func ParseLegacyUsage(raw json.RawMessage, source string) *Usage {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		return nil
	}
	return usageFromParts(top["tokens"], top["usageCompleteness"], top["estimatedCostUsd"], source)
}

// ParseFlatLegacyUsage reads usage fields stored flat on a run receipt (tokens, usageCompleteness,
// estimatedCostUsd at top level), as written by scripts/run.mjs.
func ParseFlatLegacyUsage(top map[string]json.RawMessage, source string) *Usage {
	return usageFromParts(top["tokens"], top["usageCompleteness"], top["estimatedCostUsd"], source)
}

func usageFromParts(tokensRaw, complRaw, costRaw json.RawMessage, source string) *Usage {
	var tokens map[string]json.RawMessage
	if len(tokensRaw) == 0 || json.Unmarshal(tokensRaw, &tokens) != nil || tokens == nil {
		return nil
	}
	u := &Usage{Source: source, UsageCompleteness: UsageUnknown}
	u.Tokens.Input = countOf(tokens["input"])
	u.Tokens.Output = countOf(tokens["output"])
	u.Tokens.CacheRead = countOf(tokens["cacheRead"])
	u.Tokens.CacheWrite = countOf(tokens["cacheWrite"])
	u.Tokens.Total = countOf(tokens["total"])
	u.Tokens.AssistantMessages = countOf(tokens["assistantMessages"])
	var c string
	if json.Unmarshal(complRaw, &c) == nil && ValidCompleteness(c) {
		u.UsageCompleteness = c
	}
	u.EstimatedCostUsd = costOf(costRaw)
	return u
}

func countOf(raw json.RawMessage) *int64 {
	var n json.Number
	if len(raw) == 0 || json.Unmarshal(raw, &n) != nil {
		return nil
	}
	v, err := n.Int64()
	if err != nil {
		f, ferr := n.Float64()
		if ferr != nil || f < 0 || math.IsInf(f, 0) || f != math.Trunc(f) || f > math.MaxInt64 {
			return nil
		}
		v = int64(f)
	}
	if v < 0 {
		return nil
	}
	return &v
}

func costOf(raw json.RawMessage) *float64 {
	var n json.Number
	if len(raw) == 0 || json.Unmarshal(raw, &n) != nil {
		return nil
	}
	f, err := n.Float64()
	if err != nil || f < 0 || math.IsInf(f, 0) || math.IsNaN(f) {
		return nil
	}
	return &f
}

// Validate checks a native usage value: counters non-negative, known completeness, finite cost.
func (u *Usage) Validate() error {
	if u == nil {
		return nil
	}
	for _, p := range []*int64{u.Tokens.Input, u.Tokens.Output, u.Tokens.CacheRead, u.Tokens.CacheWrite, u.Tokens.Total, u.Tokens.AssistantMessages} {
		if p != nil && *p < 0 {
			return errInvalid("usage token counters must be non-negative")
		}
	}
	if !ValidCompleteness(u.UsageCompleteness) {
		return errInvalid("usage completeness must be complete, partial or unknown")
	}
	if c := u.EstimatedCostUsd; c != nil && (*c < 0 || math.IsNaN(*c) || math.IsInf(*c, 0)) {
		return errInvalid("usage cost must be a finite non-negative number")
	}
	return nil
}

// RunHadProcess mirrors aggregateUsage: a run counts when it recorded a PID or any usage.
func RunHadProcess(r Run) bool { return r.PID != nil || r.Usage != nil }

// Aggregate computes cumulative usage exactly like workflow/core.mjs aggregateUsage: a run that started a
// process without known usage makes the aggregate partial/unknown; tokens.total is set only when complete.
func Aggregate(runs []Run) AggregateUsage {
	var sums [4]int64
	var unknown [4]bool
	var knownSubtotal int64
	counted, spawned := 0, 0
	complete, costKnown := true, true
	cost := 0.0
	for _, r := range runs {
		if !RunHadProcess(r) {
			continue
		}
		spawned++
		u := r.Usage
		if u == nil {
			complete, costKnown = false, false
			unknown = [4]bool{true, true, true, true}
			continue
		}
		for i, p := range []*int64{u.Tokens.Input, u.Tokens.Output, u.Tokens.CacheRead, u.Tokens.CacheWrite} {
			if p != nil {
				sums[i] += *p
			} else {
				unknown[i] = true
			}
		}
		if u.Tokens.Total != nil {
			knownSubtotal += *u.Tokens.Total
			counted++
		} else {
			complete = false
		}
		if u.UsageCompleteness != UsageComplete {
			complete = false
		}
		// A provider-reported cost of exactly 0 is a legitimate known value; nil, negative,
		// NaN or infinite costs are unknown and keep the aggregate cost null.
		if c := u.EstimatedCostUsd; c != nil && *c >= 0 && !math.IsNaN(*c) && !math.IsInf(*c, 0) {
			cost += *c
		} else {
			costKnown = false
		}
	}
	out := AggregateUsage{KnownSubtotal: knownSubtotal, RunsWithProcess: spawned}
	switch {
	case spawned == 0:
		out.Completeness = UsageComplete
	case counted == 0:
		out.Completeness = UsageUnknown
	case complete:
		out.Completeness = UsageComplete
	default:
		out.Completeness = UsagePartial
	}
	ptr := func(i int) *int64 {
		if unknown[i] {
			return nil
		}
		v := sums[i]
		return &v
	}
	out.Tokens = TokenCounts{Input: ptr(0), Output: ptr(1), CacheRead: ptr(2), CacheWrite: ptr(3)}
	if out.Completeness == UsageComplete {
		v := knownSubtotal
		out.Tokens.Total = &v
	}
	if out.Completeness == UsageComplete && costKnown && spawned > 0 && !math.IsInf(cost, 0) && !math.IsNaN(cost) {
		c := math.Round(cost*1e6) / 1e6
		out.EstimatedCostUsd = &c
	}
	return out
}
