package model

import "testing"

func TestTaskProgressSeparatesUnknownExecutionAndExactCandidateReview(t *testing.T) {
	sha := "candidate"
	tt := Task{ID: "task", State: TaskUnknown, CandidateSha: &sha, ContextRef: ContextRef{Digest: "context"}}
	runs := []Run{{TaskID: "task", Role: "reviewer", State: RunRunning, Events: []RunEvent{{Type: "budget", Summary: "wrap_up_requested"}}}}
	reviews := []Review{{TaskID: "task", CandidateSha: sha, ContextDigest: "context", Verdict: "pass"}}
	p := Progress(tt, runs, reviews)
	if p.Execution != "unknown" || p.Phase != "review" || p.Delivery != "reviewed" {
		t.Fatal(p)
	}
	changed := "changed"
	tt.CandidateSha = &changed
	if Progress(tt, runs, reviews).Delivery != "candidate" {
		t.Fatal("stale review accepted")
	}
	tt.State = TaskChecking
	tt.CandidateSha = &sha
	if Progress(tt, runs, reviews).Execution != "wrapping_up" {
		t.Fatal("wrap-up lost")
	}
	tt.State = TaskDelivered
	if p = Progress(tt, runs, reviews); p.Delivery != "local_delivery" || p.Execution != "ended" || p.Phase != "complete" {
		t.Fatal(p)
	}
}

func TestDelegateCandidateIsAnEndedUnreviewedDelivery(t *testing.T) {
	sha, reason := "candidate", "delegate_candidate"
	p := Progress(Task{ID: "task", Origin: "native_delegate", State: TaskFirstDelivery, StateReason: &reason, CandidateSha: &sha}, nil, nil)
	if p.Execution != "ended" || p.Delivery != "candidate" {
		t.Fatal(p)
	}
}
