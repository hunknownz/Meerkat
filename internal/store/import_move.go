package store

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"sort"

	"github.com/hunknownz/Meerkat/internal/model"
)

// Project move limits.
const (
	MaxProjectMovesBytes = 64 << 10
	MaxProjectMoves      = 64
	MaxProjectMoveRepos  = 32
	maxProjectMoveIDLen  = 200
)

// ProjectMove is one explicit, auditable project relocation: the incoming legacy project
// with FromRepositories is accepted as the same logical project as the preexisting
// destination project with ToRepositories. The destination record is kept intact.
type ProjectMove struct {
	ProjectID        string   `json:"projectId"`
	FromRepositories []string `json:"fromRepositories"`
	ToRepositories   []string `json:"toRepositories"`
}

// ImportOptions are opt-in import adjustments. The zero value is plain ImportLegacy.
type ImportOptions struct {
	ProjectMoves []ProjectMove
}

// ProjectMoveReport is the sanitized move summary: ids, count and digest only (no paths).
type ProjectMoveReport struct {
	ProjectIDs []string `json:"projectIds"`
	Count      int      `json:"count"`
	Digest     string   `json:"digest"`
}

// projectRelocation is the private audit record kept in the imports payload.
type projectRelocation struct {
	Move     ProjectMove   `json:"move"`
	Original model.Project `json:"original"`
	Target   model.Project `json:"target"`
}

// importRecord is the stored imports payload: the sanitized report plus private relocation audit.
type importRecord struct {
	ImportReport
	ProjectRelocations []projectRelocation `json:"projectRelocations,omitempty"`
}

// LoadProjectMoves reads a private, regular, bounded moves file and validates it strictly.
func LoadProjectMoves(path string) ([]ProjectMove, error) {
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("%w: project moves path must be absolute", ErrUnsafeSource)
	}
	b, err := readSafe(filepath.Clean(path), MaxProjectMovesBytes)
	if err != nil {
		return nil, err
	}
	if b == nil {
		return nil, fmt.Errorf("%w: project moves file missing", ErrUnsafeSource)
	}
	return ParseProjectMoves(b)
}

// ParseProjectMoves strictly decodes and validates a project moves JSON array.
func ParseProjectMoves(b []byte) ([]ProjectMove, error) {
	if len(b) > MaxProjectMovesBytes {
		return nil, model.Invalidf("project moves exceed size limit")
	}
	var moves []ProjectMove
	if err := decodeStrict(b, &moves); err != nil {
		return nil, model.Invalidf("project moves must be a strict JSON array")
	}
	if err := validateProjectMoves(moves); err != nil {
		return nil, err
	}
	return moves, nil
}

func validateRepoList(list []string) error {
	if len(list) == 0 || len(list) > MaxProjectMoveRepos {
		return model.Invalidf("project move repositories must be a bounded non-empty list")
	}
	seen := map[string]bool{}
	for _, p := range list {
		if p == "" || !filepath.IsAbs(p) || filepath.Clean(p) != p || len(p) > 4096 || seen[p] {
			return model.Invalidf("project move repositories must be unique clean absolute paths")
		}
		seen[p] = true
	}
	return model.CheckStrings("project move", list...)
}

func validateProjectMoves(moves []ProjectMove) error {
	if len(moves) == 0 || len(moves) > MaxProjectMoves {
		return model.Invalidf("project moves must be a bounded non-empty array")
	}
	ids := map[string]bool{}
	for _, m := range moves {
		if m.ProjectID == "" || len(m.ProjectID) > maxProjectMoveIDLen || ids[m.ProjectID] {
			return model.Invalidf("project move ids must be unique and non-empty")
		}
		if err := model.CheckStrings("project move", m.ProjectID); err != nil {
			return err
		}
		ids[m.ProjectID] = true
		if err := validateRepoList(m.FromRepositories); err != nil {
			return err
		}
		if err := validateRepoList(m.ToRepositories); err != nil {
			return err
		}
		if sameList(m.FromRepositories, m.ToRepositories) {
			return model.Invalidf("project move must change repositories")
		}
	}
	return nil
}

func sameList(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func sortedMoves(moves []ProjectMove) []ProjectMove {
	out := append([]ProjectMove{}, moves...)
	sort.Slice(out, func(i, j int) bool { return out[i].ProjectID < out[j].ProjectID })
	return out
}

// projectMovesDigest is a stable digest of the (id-sorted) mapping.
func projectMovesDigest(moves []ProjectMove) string {
	sum := sha256.Sum256([]byte(canon(sortedMoves(moves))))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// moveImportID binds the import identity to both the source bytes and the mapping digest.
func moveImportID(srcHash, digest string) string {
	sum := sha256.Sum256([]byte("meerkat-import\x00" + srcHash + "\x00project-moves\x00" + digest))
	return hex.EncodeToString(sum[:])[:32]
}

// applyProjectMoves matches every move against an incoming project and the current destination
// project. Matched incoming projects are dropped from the merge (destination kept intact) and
// recorded privately. Any mismatch is reported as a conflict (fail closed).
func applyProjectMoves(cur, in *model.State, moves []ProjectMove, conflict func(string, string)) []projectRelocation {
	if len(moves) == 0 {
		return nil
	}
	curByID := map[string]model.Project{}
	for _, p := range cur.Projects {
		curByID[p.ID] = p
	}
	inIdx := map[string]int{}
	for i, p := range in.Projects {
		inIdx[p.ID] = i
	}
	drop := map[string]bool{}
	var rel []projectRelocation
	for _, m := range sortedMoves(moves) {
		i, okIn := inIdx[m.ProjectID]
		dst, okDst := curByID[m.ProjectID]
		if !okIn || !okDst {
			conflict("projectMove", m.ProjectID)
			continue
		}
		src := in.Projects[i]
		if src.Name != dst.Name || !sameList(src.Repositories, m.FromRepositories) || !sameList(dst.Repositories, m.ToRepositories) {
			conflict("projectMove", m.ProjectID)
			continue
		}
		drop[m.ProjectID] = true
		rel = append(rel, projectRelocation{Move: m, Original: src, Target: dst})
	}
	if len(drop) > 0 {
		kept := make([]model.Project, 0, len(in.Projects))
		for _, p := range in.Projects {
			if !drop[p.ID] {
				kept = append(kept, p)
			}
		}
		in.Projects = kept
	}
	return rel
}
