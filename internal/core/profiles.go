package core

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/hunknownz/Meerkat/internal/model"
	"github.com/hunknownz/Meerkat/internal/platform"
)

// ResolveProfileReference accepts an explicit private path or a managed config
// name. Names have no filesystem semantics beyond data-dir/profiles/<name>.json.
func ResolveProfileReference(dataDir, ref string) string {
	if !filepath.IsAbs(ref) && slugRE.MatchString(ref) {
		return filepath.Join(dataDir, "profiles", ref+".json")
	}
	return ref
}

// ExecutionProfileChoice is read-only discovery metadata, never a private
// frozen Profile. In particular, command, credential reference and paths are absent.
type ExecutionProfileChoice struct {
	ID        string               `json:"id"`
	Valid     bool                 `json:"valid"`
	Binding   string               `json:"binding,omitempty"`
	ProjectID string               `json:"projectId,omitempty"`
	Executor  string               `json:"executor,omitempty"`
	Provider  string               `json:"provider,omitempty"`
	Model     string               `json:"model,omitempty"`
	Limits    *model.ProfileLimits `json:"limits,omitempty"`
}

// ListExecutionProfiles discovers managed private configs without creating a
// database, probing an executable, reading credential values or selecting a default.
func ListExecutionProfiles(dataDir string) ([]ExecutionProfileChoice, error) {
	out := []ExecutionProfileChoice{}
	dir := filepath.Join(dataDir, "profiles")
	fi, err := os.Lstat(dir)
	if errors.Is(err, os.ErrNotExist) {
		return out, nil
	}
	if err != nil || !fi.IsDir() || !platform.Private(dir, fi, 0o700) {
		return nil, invalid("execution Profile directory must be private and owned by the current user")
	}
	files, err := os.ReadDir(dir)
	if err != nil {
		return nil, invalid("execution Profile directory is unreadable")
	}
	for _, file := range files {
		id, jsonFile := strings.CutSuffix(file.Name(), ".json")
		if !jsonFile || !slugRE.MatchString(id) || model.LooksLikeCredential(id) || file.IsDir() {
			continue
		}
		choice := ExecutionProfileChoice{ID: id}
		p, err := InspectProfile(filepath.Join(dir, file.Name()))
		if err == nil {
			choice.Valid, choice.Executor, choice.Provider, choice.Model, choice.Limits = true, p.Executor, p.Provider, p.Model, &p.Limits
			choice.Binding = "reusable"
			if !p.Reusable {
				choice.Binding, choice.ProjectID = "project", p.ProjectID
			}
		}
		out = append(out, choice)
	}
	return out, nil
}
