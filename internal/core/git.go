package core

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
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

// gitError keeps the specific failure (exit code and a bounded stderr excerpt) instead of discarding it.
type gitError struct {
	Args   []string
	Code   int // process exit code; -1 when git did not run to completion (spawn failure, timeout, signal)
	Stderr string
	Err    error
}

func (e *gitError) Error() string {
	msg := "git " + strings.Join(e.Args, " ") + ": " + e.Err.Error()
	if e.Stderr != "" {
		msg += ": " + e.Stderr
	}
	return msg
}

func (e *gitError) Unwrap() error { return e.Err }

// exitCode returns git's exit code for err, or -1 if git did not exit normally.
func exitCode(err error) int {
	var ge *gitError
	if errors.As(err, &ge) {
		return ge.Code
	}
	return -1
}

func git(dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0", "LC_ALL=C")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		ge := &gitError{Args: args, Code: -1, Err: err, Stderr: strings.TrimSpace(stderr.String())}
		if len(ge.Stderr) > 512 {
			ge.Stderr = ge.Stderr[:512]
		}
		var xe *exec.ExitError
		if errors.As(err, &xe) && ctx.Err() == nil && xe.ExitCode() >= 0 {
			ge.Code = xe.ExitCode()
		}
		return strings.TrimSpace(string(out)), ge
	}
	return strings.TrimSpace(string(out)), nil
}

func gitOK(dir string, args ...string) bool { _, err := git(dir, args...); return err == nil }

func isAncestor(dir, a, b string) bool {
	return shaRE.MatchString(a) && shaRE.MatchString(b) && gitOK(dir, "merge-base", "--is-ancestor", a, b)
}

// wtFacts are Git facts gathered before any SQL transaction. Err is set when the facts could not be
// determined (Git failed to answer); such facts are unknown and must never be read as a semantic result
// such as "branch changed" or "dirty". Exists is only true when every fact was determined.
type wtFacts struct {
	Exists bool
	Branch string // "" means detached HEAD (only meaningful when Exists)
	Head   string
	Clean  bool
	Err    error
}

func inspectOnce(wt string) wtFacts {
	var f wtFacts
	if fi, err := os.Stat(wt); err != nil || !fi.IsDir() {
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			f.Err = err
		}
		return f
	}
	head, err := git(wt, "rev-parse", "--verify", "-q", "HEAD")
	if err != nil {
		if exitCode(err) != 1 || head != "" {
			f.Err = err // 1 without output: no commit yet (determined, Exists=false)
		}
		return f
	}
	if !shaRE.MatchString(head) {
		f.Err = fmt.Errorf("git rev-parse HEAD: unexpected output")
		return f
	}
	st, err := git(wt, "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		f.Err = err
		return f
	}
	// Read the full symbolic ref and strip the prefix ourselves: "--short" consults every ref to
	// disambiguate the name, which can fail or change shape while other worktrees update refs.
	ref, err := git(wt, "symbolic-ref", "-q", "HEAD")
	branch := ""
	switch {
	case err == nil && strings.HasPrefix(ref, "refs/heads/"):
		branch = strings.TrimPrefix(ref, "refs/heads/")
	case err == nil:
		branch = ref // symbolic, but not a local branch: never equal to a recorded branch name
	case exitCode(err) == 1 && ref == "":
		// -q: exit 1 without output means HEAD is detached (a determined fact).
	default:
		f.Err = err
		return f
	}
	f.Exists, f.Head, f.Clean, f.Branch = true, head, st == "", branch
	return f
}

// inspect gathers worktree facts. Only an undetermined inspection (Git failed to answer, e.g. a spawn
// failure or transient I/O error under load) is re-attempted, a bounded number of times; determined
// facts (missing worktree, other branch, dirty, any HEAD) are returned as-is and never retried.
func inspect(wt string) wtFacts {
	var f wtFacts
	for i := 0; i < 3; i++ {
		if f = inspectOnce(wt); f.Err == nil {
			return f
		}
		time.Sleep(time.Duration(i+1) * 50 * time.Millisecond)
	}
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
