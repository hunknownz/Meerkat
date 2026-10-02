package core

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

var (
	shaRE             = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)
	protectedBranches = []string{"main", "master", "develop", "trunk"}
)

func git(dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0", "LC_ALL=C")
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

func gitOK(dir string, args ...string) bool { _, err := git(dir, args...); return err == nil }

func isAncestor(dir, a, b string) bool {
	return shaRE.MatchString(a) && shaRE.MatchString(b) && gitOK(dir, "merge-base", "--is-ancestor", a, b)
}

// wtFacts are Git facts gathered before any SQL transaction.
type wtFacts struct {
	Exists bool
	Branch string
	Head   string
	Clean  bool
}

func inspect(wt string) wtFacts {
	var f wtFacts
	if fi, err := os.Stat(wt); err != nil || !fi.IsDir() {
		return f
	}
	head, err := git(wt, "rev-parse", "HEAD")
	if err != nil || !shaRE.MatchString(head) {
		return f
	}
	st, err := git(wt, "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return f
	}
	f.Exists, f.Head, f.Clean = true, head, st == ""
	f.Branch, _ = git(wt, "symbolic-ref", "--short", "-q", "HEAD")
	return f
}

// changedPaths lists paths changed between two commits (nil on error).
func changedPaths(wt, from, to string) []string {
	out, err := git(wt, "diff", "--name-only", "--no-renames", "-z", from, to)
	if err != nil {
		return nil
	}
	paths := []string{}
	for _, p := range strings.Split(out, "\x00") {
		if p != "" {
			paths = append(paths, p)
		}
	}
	return paths
}

func commitCount(wt, from, to string) int {
	out, err := git(wt, "rev-list", "--count", from+".."+to)
	if err != nil {
		return -1
	}
	n, err := strconv.Atoi(out)
	if err != nil {
		return -1
	}
	return n
}

func inScope(p string, scope []string) bool {
	return slices.ContainsFunc(scope, func(s string) bool { return p == s || strings.HasPrefix(p, s+"/") })
}

func realDir(p string) (string, bool) {
	if !filepath.IsAbs(p) {
		return "", false
	}
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		return "", false
	}
	fi, err := os.Stat(r)
	return r, err == nil && fi.IsDir()
}

func gitTop(d string) string {
	t, err := git(d, "rev-parse", "--show-toplevel")
	if err != nil {
		return ""
	}
	r, _ := filepath.EvalSymlinks(t)
	return r
}

func gitAbs(d string, args ...string) string {
	t, err := git(d, args...)
	if err != nil || t == "" {
		return ""
	}
	if !filepath.IsAbs(t) {
		t = filepath.Join(d, t)
	}
	r, _ := filepath.EvalSymlinks(t)
	return r
}
