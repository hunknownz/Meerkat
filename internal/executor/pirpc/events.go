package pirpc

import (
	"encoding/json"
	"math"
	"time"
)

// TokenSample is one assistant message sample, not settled cumulative usage.
// Final distinguishes message_end from an in-flight message_update. Missing or
// invalid fields stay nil; CostEstimateUSD is Pi's estimate, not a billed fee.
type TokenSample struct {
	Input, Output, CacheRead, CacheWrite *int64
	CostEstimateUSD                      *float64
	Final                                bool
	Invalid                              bool
}

// Event is an allowlisted structural event. It never contains prompt/response
// text, reasoning, tool arguments/results, queue text or provider diagnostics.
// Only Settled marks Pi will not continue automatically; task delivery is separate.
type Event struct {
	Type             string
	Tool             string
	ToolID           string // private structural identity; never projected into Run events
	ToolOutcomeKnown bool
	ToolFailed       bool
	ObservedAt       time.Time
	Settled          bool
	ProviderFailure  bool
	Assistant        bool
	Usage            *TokenSample
}

var eventTypes = map[string]bool{
	"agent_start": true, "agent_end": true, "agent_settled": true,
	"message_start": true, "message_update": true, "message_end": true,
	"tool_execution_start": true, "tool_execution_update": true, "tool_execution_end": true,
	"auto_compaction_start": true, "auto_compaction_end": true,
	"auto_retry_start": true, "auto_retry_end": true,
}

func eventSummary(typ string, b []byte) (Event, bool) {
	if !eventTypes[typ] {
		return Event{}, false
	}
	ev := Event{Type: typ, ObservedAt: time.Now().UTC(), Settled: typ == "agent_settled"}
	var v struct {
		ToolName   string          `json:"toolName"`
		ToolCallID string          `json:"toolCallId"`
		IsError    *bool           `json:"isError"`
		Usage      json.RawMessage `json:"usage"`
		Message    *struct {
			Role       string          `json:"role"`
			Usage      json.RawMessage `json:"usage"`
			StopReason string          `json:"stopReason"`
		} `json:"message"`
	}
	if json.Unmarshal(b, &v) != nil {
		return ev, true
	}
	if len(v.ToolCallID) <= 128 {
		ev.ToolID = v.ToolCallID
	}
	ev.ToolOutcomeKnown = v.IsError != nil
	ev.ToolFailed = v.IsError != nil && *v.IsError
	switch v.ToolName {
	case "read", "bash", "edit", "write", "grep", "find", "ls", "powershell":
		ev.Tool = v.ToolName
	case "":
	default:
		ev.Tool = "custom"
	}
	if (typ == "message_update" || typ == "message_end") &&
		(v.Message == nil || v.Message.Role == "" || v.Message.Role == "assistant") {
		ev.Assistant = v.Message != nil && v.Message.Role == "assistant"
		raw := v.Usage
		if v.Message != nil {
			if len(v.Message.Usage) > 0 {
				raw = v.Message.Usage
			}
			ev.ProviderFailure = v.Message.StopReason == "error"
		}
		if len(raw) > 0 && string(raw) != "null" {
			ev.Usage = sample(raw, typ == "message_end")
		}
	}
	return ev, true
}

func sample(raw json.RawMessage, final bool) *TokenSample {
	s := &TokenSample{Final: final}
	var v map[string]json.RawMessage
	if json.Unmarshal(raw, &v) != nil || v == nil {
		s.Invalid = true
		return s
	}
	var total int64
	for i, key := range []string{"input", "output", "cacheRead", "cacheWrite"} {
		var n json.Number
		if json.Unmarshal(v[key], &n) != nil {
			s.Invalid = true
			continue
		}
		f, err := n.Float64()
		if err != nil || f < 0 || f > 1<<53 || math.IsNaN(f) || math.IsInf(f, 0) || f != math.Trunc(f) {
			s.Invalid = true
			continue
		}
		p := new(int64)
		*p = int64(f)
		total += *p
		switch i {
		case 0:
			s.Input = p
		case 1:
			s.Output = p
		case 2:
			s.CacheRead = p
		case 3:
			s.CacheWrite = p
		}
	}
	var cost struct {
		Total *float64 `json:"total"`
	}
	if json.Unmarshal(v["cost"], &cost) == nil && cost.Total != nil &&
		*cost.Total >= 0 && !math.IsNaN(*cost.Total) && !math.IsInf(*cost.Total, 0) &&
		(*cost.Total > 0 || (total == 0 && !s.Invalid)) {
		s.CostEstimateUSD = cost.Total
	}
	return s
}
