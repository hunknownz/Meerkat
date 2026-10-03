// Package checkpoint captures private, exact local worktree evidence. It never
// restores or resets a worktree and does not certify delivery or external effects.
package checkpoint

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

const MaxBytes = 48 << 20
const maxFileBytes = 8 << 20
const maxFiles = 512

var ErrUnverifiable = errors.New("checkpoint: worktree evidence is unverifiable")

type File struct {
	Path    string `json:"path"`
	Mode    uint32 `json:"mode"`
	Missing bool   `json:"missing"`
	Body    []byte `json:"body"`
}

type Snapshot struct {
	Version    int    `json:"version"`
	Worktree   string `json:"worktree"`
	GitDir     string `json:"gitDir"`
	CommonDir  string `json:"commonDir"`
	Head       string `json:"head"`
	Branch     string `json:"branch"`
	Status     string `json:"status"`
	IndexPatch string `json:"indexPatch"`
	Files      []File `json:"files"`
}

type Binding struct {
	Digest string
	Scope  []string
}

func Digest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

type boundedBuffer struct {
	bytes.Buffer
	limit int
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > b.limit {
		return 0, ErrUnverifiable
	}
	return b.Buffer.Write(p)
}
func git(wt string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c := exec.CommandContext(ctx, "git", args...)
	c.Dir = wt
	c.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0", "LC_ALL=C")
	out := &boundedBuffer{limit: MaxBytes}
	c.Stdout = out
	if c.Run() != nil {
		return "", ErrUnverifiable
	}
	return out.String(), nil
}
func inScope(p string, scope []string) bool {
	return slices.ContainsFunc(scope, func(s string) bool { return p == s || strings.HasPrefix(p, s+"/") })
}

// Capture uses NUL-delimited Git paths, disables rename inference and preserves
// the index separately from working bytes. Symlinks, merge conflicts and large
// captures require manual intervention. Untracked ignored files are excluded.
func Capture(wt string, scope []string) (Snapshot, []byte, error) {
	fail := func() (Snapshot, []byte, error) { return Snapshot{}, nil, ErrUnverifiable }
	real, e := filepath.EvalSymlinks(wt)
	if e != nil || real != wt {
		return fail()
	}
	s := Snapshot{Version: 1, Worktree: wt, Files: []File{}}
	for _, v := range []struct {
		dst  *string
		args []string
	}{
		{&s.Head, []string{"rev-parse", "HEAD"}},
		{&s.Branch, []string{"symbolic-ref", "-q", "HEAD"}},
		{&s.GitDir, []string{"rev-parse", "--absolute-git-dir"}},
		{&s.CommonDir, []string{"rev-parse", "--path-format=absolute", "--git-common-dir"}},
	} {
		raw, e := git(wt, v.args...)
		if e != nil {
			return fail()
		}
		*v.dst = strings.TrimSpace(raw)
	}
	if s.GitDir == s.CommonDir || !strings.HasPrefix(s.Branch, "refs/heads/") {
		return fail()
	}
	s.Branch = strings.TrimPrefix(s.Branch, "refs/heads/")
	if x, e := git(wt, "ls-files", "--unmerged", "-z"); e != nil || x != "" {
		return fail()
	}
	s.Status, e = git(wt, "status", "--porcelain=v1", "-z", "--untracked-files=all", "--no-renames")
	if e != nil {
		return fail()
	}
	s.IndexPatch, e = git(wt, "diff", "--cached", "--binary", "--no-renames", "--no-ext-diff", "--no-textconv")
	if e != nil {
		return fail()
	}
	root, e := os.OpenRoot(wt)
	if e != nil {
		return fail()
	}
	defer root.Close()
	total := 0
	for _, entry := range strings.Split(s.Status, "\x00") {
		if entry == "" {
			continue
		}
		if len(entry) < 4 || entry[2] != ' ' || len(s.Files) >= maxFiles {
			return fail()
		}
		p := entry[3:]
		if !utf8.ValidString(p) || !filepath.IsLocal(p) || filepath.ToSlash(filepath.Clean(p)) != p || !inScope(p, scope) || strings.HasPrefix(p, ".git/") || p == ".git" {
			return fail()
		}
		v := File{Path: p}
		info, e := root.Lstat(p)
		if errors.Is(e, os.ErrNotExist) {
			v.Missing = true
		} else {
			if e != nil || !info.Mode().IsRegular() || info.Size() > maxFileBytes {
				return fail()
			}
			f, e := root.Open(p)
			if e != nil {
				return fail()
			}
			v.Body, e = io.ReadAll(io.LimitReader(f, maxFileBytes+1))
			after, se := f.Stat()
			f.Close()
			if e != nil || se != nil || !os.SameFile(info, after) || len(v.Body) > maxFileBytes || after.Size() != int64(len(v.Body)) {
				return fail()
			}
			v.Mode = uint32(after.Mode().Perm())
			total += len(v.Body)
			if total > 32<<20 {
				return fail()
			}
		}
		s.Files = append(s.Files, v)
	}
	b, e := json.Marshal(s)
	if e != nil || len(b) > MaxBytes {
		return fail()
	}
	return s, b, nil
}

func Verify(wt string, b Binding) error {
	_, raw, e := Capture(wt, b.Scope)
	if e != nil || Digest(raw) != b.Digest {
		return ErrUnverifiable
	}
	return nil
}

func Decode(raw []byte) (Snapshot, error) {
	var s Snapshot
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if len(raw) > MaxBytes || d.Decode(&s) != nil || s.Version != 1 || !filepath.IsAbs(s.Worktree) || !filepath.IsAbs(s.GitDir) || !filepath.IsAbs(s.CommonDir) || len(s.Files) > maxFiles {
		return Snapshot{}, ErrUnverifiable
	}
	if canonical, e := json.Marshal(s); e != nil || !bytes.Equal(canonical, raw) {
		return Snapshot{}, ErrUnverifiable
	}
	seen := map[string]bool{}
	total := 0
	for _, f := range s.Files {
		if !utf8.ValidString(f.Path) || !filepath.IsLocal(f.Path) || filepath.ToSlash(filepath.Clean(f.Path)) != f.Path || seen[f.Path] || f.Mode > 0o777 || len(f.Body) > maxFileBytes || f.Missing && (f.Mode != 0 || len(f.Body) != 0) {
			return Snapshot{}, ErrUnverifiable
		}
		seen[f.Path] = true
		total += len(f.Body)
	}
	if total > 32<<20 {
		return Snapshot{}, ErrUnverifiable
	}
	return s, nil
}
