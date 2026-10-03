package model

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
)

func FrozenProfileDigest(p Profile) string {
	b, _ := json.Marshal(p)
	return fmt.Sprintf("%x", sha256.Sum256(b))
}

// FrozenTaskDigest excludes execution progress, retaining the prepared contract
// and all selected profile records. It is shared by scheduling and request gates.
func FrozenTaskDigest(st *State, t Task) string {
	t.State, t.RecordedState, t.CreatedAt, t.UpdatedAt = "", "", "", ""
	t.StateReason, t.ResumeRole, t.CandidateSha = nil, nil, nil
	profiles := []Profile{}
	for _, role := range Roles {
		id := t.ProfileIDs[role]
		for _, p := range st.Profiles {
			if p.ID == id {
				profiles = append(profiles, p)
				break
			}
		}
	}
	b, _ := json.Marshal([]any{t, profiles})
	return fmt.Sprintf("%x", sha256.Sum256(b))
}
