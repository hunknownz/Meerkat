package model

import (
	"encoding/json"
	"math"
	"testing"
)

func completeUsage(cost *float64) *Usage {
	n := int64(1)
	return &Usage{Tokens: TokenCounts{Input: &n, Output: &n, CacheRead: &n, CacheWrite: &n, Total: &n}, UsageCompleteness: UsageComplete, EstimatedCostUsd: cost}
}

func f64(v float64) *float64 { return &v }

func TestAggregateZeroCostIsKnown(t *testing.T) {
	pid := 1
	runs := []Run{{PID: &pid, Usage: completeUsage(f64(0))}, {PID: &pid, Usage: completeUsage(f64(0))}}
	a := Aggregate(runs)
	if a.EstimatedCostUsd == nil || *a.EstimatedCostUsd != 0 {
		t.Fatalf("zero cost should be known 0, got %v", a.EstimatedCostUsd)
	}
	runs = append(runs, Run{PID: &pid, Usage: completeUsage(f64(0.25))})
	if a := Aggregate(runs); a.EstimatedCostUsd == nil || *a.EstimatedCostUsd != 0.25 {
		t.Fatalf("mixed zero/positive cost, got %v", a.EstimatedCostUsd)
	}
}

func TestAggregateUnknownCostStaysNull(t *testing.T) {
	pid := 1
	for name, bad := range map[string]*float64{"nil": nil, "negative": f64(-1), "nan": f64(math.NaN()), "inf": f64(math.Inf(1))} {
		runs := []Run{{PID: &pid, Usage: completeUsage(f64(0))}, {PID: &pid, Usage: completeUsage(bad)}}
		a := Aggregate(runs)
		if a.EstimatedCostUsd != nil {
			t.Fatalf("%s: cost must be null, got %v", name, *a.EstimatedCostUsd)
		}
		b, err := json.Marshal(a)
		if err != nil {
			t.Fatalf("%s: marshal: %v", name, err)
		}
		var m map[string]json.RawMessage
		if err := json.Unmarshal(b, &m); err != nil || string(m["estimatedCostUsd"]) != "null" {
			t.Fatalf("%s: estimatedCostUsd must marshal as null: %s", name, b)
		}
	}
}

func TestParseLegacyZeroCost(t *testing.T) {
	u := ParseLegacyUsage(json.RawMessage(`{"tokens":{"input":1},"usageCompleteness":"partial","estimatedCostUsd":0}`), "x")
	if u == nil || u.EstimatedCostUsd == nil || *u.EstimatedCostUsd != 0 {
		t.Fatal("legacy zero cost should be known")
	}
	u = ParseLegacyUsage(json.RawMessage(`{"tokens":{"input":1},"estimatedCostUsd":-1}`), "x")
	if u == nil || u.EstimatedCostUsd != nil {
		t.Fatal("negative cost must be unknown")
	}
}
