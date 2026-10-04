package core

import "github.com/hunknownz/Meerkat/internal/model"

// futureStageReserve conservatively retains every still-permitted fix and its
// review. A reserve is released only when its stage can no longer occur.
func futureStageReserve(t model.Task, p pipeline, s step) int64 {
	if t.Budget == nil || !t.Budget.HardTokenCap() || t.Budget.StageReserves == nil || t.Origin == OriginDelegate {
		return 0
	}
	r := t.Budget.StageReserves
	fixes := max(0, t.Budget.MaxFixRounds-p.fixRounds)
	if s.purpose == "fix" {
		fixes = max(0, fixes-1)
	}
	reviews := fixes
	polish := int64(0)
	if !p.polished && s.role != "polisher" {
		polish = r.PolishTokens
		reviews++
	}
	if s.role != "reviewer" {
		reviews++
	}
	return int64(reviews)*r.ReviewTokens + int64(fixes)*r.FixTokens + polish
}
