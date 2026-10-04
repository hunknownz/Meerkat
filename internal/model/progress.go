package model

import "encoding/json"

// TaskProgress projects three independent meanings from SQLite's task, run and
// exact-SHA review records. It is not another writable state authority.
type TaskProgress struct {
	Execution string `json:"execution"`
	Phase     string `json:"phase"`
	Delivery  string `json:"delivery"`
}

func Progress(t Task, runs []Run, reviews []Review) TaskProgress {
	p := TaskProgress{Execution: "ready", Phase: "none", Delivery: "none"}
	switch t.State {
	case TaskQueued:
		p.Execution = "queued"
	case TaskBlocked:
		p.Execution = "blocked"
	case TaskPaused:
		p.Execution = "paused"
	case TaskUnknown:
		p.Execution = "unknown"
	case TaskFailed:
		p.Execution = "failed"
	case TaskStopped, TaskDelivered:
		p.Execution = "ended"
	default:
		if IsActiveTaskState(t.State) {
			p.Execution = "running"
		}
	}
	if t.Origin == "native_delegate" && t.State == TaskFirstDelivery && t.StateReason != nil && *t.StateReason == "delegate_candidate" {
		p.Execution = "ended"
	}
	for _, r := range runs {
		if r.TaskID != t.ID {
			continue
		}
		switch r.Role {
		case "developer":
			p.Phase = "development"
		case "reviewer":
			p.Phase = "review"
		case "polisher":
			p.Phase = "polish"
		}
		var summary struct {
			Purpose string `json:"purpose"`
		}
		if json.Unmarshal(r.Summary, &summary) == nil {
			if summary.Purpose == "fix" {
				p.Phase = "fix"
			}
			if summary.Purpose == "recheck" {
				p.Phase = "recheck"
			}
		}
		if p.Execution == "running" && IsActiveRunState(r.State) {
			for _, e := range r.Events {
				if e.Type == "budget" && (e.Summary == "wrap_up_requested" || e.Summary == "wrap_up_accepted") {
					p.Execution = "wrapping_up"
				}
			}
		}
	}
	switch t.State {
	case TaskImplementing:
		p.Phase = "development"
	case TaskChecking:
		p.Phase = "review"
	case TaskFixing:
		p.Phase = "fix"
	case TaskPolishing:
		p.Phase = "polish"
	case TaskRechecking:
		p.Phase = "recheck"
	case TaskDelivered:
		p.Phase = "complete"
	}
	if t.CandidateSha != nil && *t.CandidateSha != "" {
		p.Delivery = "candidate"
		for _, r := range reviews {
			if r.TaskID == t.ID && r.CandidateSha == *t.CandidateSha && r.ContextDigest == t.ContextRef.Digest {
				if r.Verdict == "pass" {
					p.Delivery = "reviewed"
				} else {
					p.Delivery = "candidate"
				}
			}
		}
		if t.State == TaskDelivered {
			p.Delivery = "local_delivery"
		}
	}
	return p
}
