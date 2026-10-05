package executor

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/hunknownz/Meerkat/internal/model"
	"github.com/hunknownz/Meerkat/internal/platform"
)

const maxReport = 64 << 10

var (
	shaRE    = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)
	digestRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9:._+/=-]{0,199}$`)
	safeIDRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
)

func inside(parent, child string) bool {
	r, err := filepath.Rel(parent, child)
	return err == nil && (r == "." || (r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator)) && !filepath.IsAbs(r)))
}

// checkReportPath requires an absolute, new file outside the worktree in a private directory owned by us.
func checkReportPath(p, worktree string) (string, error) {
	if !filepath.IsAbs(p) || filepath.Clean(p) != p {
		return "", invalid("report path must be a clean absolute path")
	}
	dir, err := filepath.EvalSymlinks(filepath.Dir(p))
	if err != nil {
		return "", invalid("report directory does not exist")
	}
	fi, err := os.Stat(dir)
	if err != nil || !fi.IsDir() || !platform.Private(dir, fi, 0o700) {
		return "", invalid("report directory must be a private directory owned by the current user")
	}
	real := filepath.Join(dir, filepath.Base(p))
	if inside(worktree, real) || inside(worktree, p) {
		return "", invalid("report path must be outside the worktree")
	}
	if _, err := os.Lstat(real); !os.IsNotExist(err) {
		return "", invalid("report path already exists; use a new private path")
	}
	return real, nil
}

func bStr(s string, max int) bool { return len(s) <= max && strings.TrimSpace(s) != "" }

// readReport reads and validates a role report without following symlinks. It returns a category on failure.
func readReport(path, role string) (*Report, string) {
	fi, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil, CatReportMissing
	}
	if err != nil || !fi.Mode().IsRegular() {
		return nil, CatReportInvalid
	}
	f, err := openNoFollow(path)
	if err != nil {
		return nil, CatReportInvalid
	}
	defer f.Close()
	if st, err := f.Stat(); err != nil || !st.Mode().IsRegular() || st.Size() > maxReport || !platform.Owned(path, st) {
		return nil, CatReportInvalid
	}
	b, err := io.ReadAll(io.LimitReader(f, maxReport+1))
	if err != nil || len(b) > maxReport {
		return nil, CatReportInvalid
	}
	r, ok := validateReport(role, b)
	if !ok {
		return nil, CatReportInvalid
	}
	return r, ""
}

func validateReport(role string, b []byte) (*Report, bool) {
	var raw map[string]json.RawMessage
	var anyv any
	if json.Unmarshal(b, &raw) != nil || raw == nil || json.Unmarshal(b, &anyv) != nil || model.CheckFreeForm(anyv, "report") != nil {
		return nil, false
	}
	need := []string{"candidateSha", "contextDigest", "summary", "checks", "knownGaps"}
	if role == "reviewer" {
		need = append(need, "verdict", "findings")
	} else {
		need = append(need, "decision")
	}
	for _, k := range need {
		if v, ok := raw[k]; !ok || (k != "contextDigest" && bytes.Equal(bytes.TrimSpace(v), []byte("null"))) {
			return nil, false
		}
	}
	var r Report
	dec := json.NewDecoder(bytes.NewReader(b))
	if dec.Decode(&r) != nil {
		return nil, false
	}
	if !shaRE.MatchString(r.CandidateSHA) || (r.ContextDigest != nil && !digestRE.MatchString(*r.ContextDigest)) {
		return nil, false
	}
	if !bStr(r.Summary, 4000) || len(r.Checks) > 50 || len(r.KnownGaps) > 50 {
		return nil, false
	}
	for _, c := range r.Checks {
		if !bStr(c.Command, 500) || !bStr(c.Result, 1000) {
			return nil, false
		}
	}
	for _, g := range r.KnownGaps {
		if !bStr(g, 1000) {
			return nil, false
		}
	}
	if role == "reviewer" {
		if (r.Verdict != "pass" && r.Verdict != "changes_requested") || len(r.Findings) > 100 || r.Decision != "" {
			return nil, false
		}
		for _, f := range r.Findings {
			if !bStr(f.ID, 100) || !bStr(f.Summary, 2000) || len(f.Path) > 500 || (f.Line != nil && *f.Line <= 0) {
				return nil, false
			}
		}
		if r.Verdict == "changes_requested" && len(r.Findings) == 0 {
			return nil, false
		}
	} else if (r.Decision != "changed" && r.Decision != "no_change") || r.Verdict != "" || r.Findings != nil {
		return nil, false
	}
	if r.Checks == nil {
		r.Checks = []Check{}
	}
	if r.KnownGaps == nil {
		r.KnownGaps = []string{}
	}
	return &r, true
}
