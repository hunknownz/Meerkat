package executor

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/hunknownz/Meerkat/internal/checkpoint"
	"github.com/hunknownz/Meerkat/internal/model"
)

const (
	maxInstructionFile  = 256 << 10
	maxInstructionTotal = 1 << 20
	maxBrief            = 256 << 10
)

var protectedBranches = []string{"main", "master", "develop", "trunk"}

// Pi runs the Pi coding agent CLI as a child process in JSON mode.
type Pi struct {
	// Grace is how long a stopped process group gets between SIGTERM and SIGKILL.
	Grace time.Duration
}

// NewPi returns a Pi executor with a 5s stop grace.
func NewPi() *Pi { return &Pi{Grace: 5 * time.Second} }

// Name implements Executor.
func (*Pi) Name() string { return "pi" }

// Validate checks a frozen profile for the Pi adapter. piCommand is the only adapter-specific field.
func (*Pi) Validate(p model.Profile) error {
	if p.Executor != "" && p.Executor != "pi" {
		return invalid("unsupported executor")
	}
	if p.Role != "" && !model.IsRole(p.Role) {
		return invalid("profile role is invalid")
	}
	for _, s := range []string{p.Provider, p.Model} {
		if s == "" || len(s) > 200 || strings.ContainsAny(s, " \t\r\n\x00") || model.LooksLikeCredential(s) {
			return invalid("profile provider and model must be non-empty plain names")
		}
	}
	if !model.ValidEnvName(p.AuthEnv) {
		return invalid("profile authEnv must be an environment variable name")
	}
	for _, a := range p.PiCommand {
		if a == "" || strings.ContainsRune(a, 0) || model.LooksLikeCredential(a) {
			return invalid("profile piCommand entries must be non-empty and credential-free")
		}
	}
	for _, ip := range p.Instructions {
		if ip == "" || filepath.IsAbs(ip) || !filepath.IsLocal(ip) {
			return invalid("profile instruction paths must be relative paths inside the worktree")
		}
	}
	if p.Limits.MaxTokens < 0 || p.Limits.MaxWallSeconds < 0 {
		return invalid("profile limits must be non-negative")
	}
	return nil
}

func gitOut(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

type prepared struct {
	worktree, branch, baseline, report string
	argv                               []string
	env                                []string
	tokens                             int64
	wall                               time.Duration
	prompt                             string
}

func (p *Pi) prepare(ctx context.Context, req Request) (*prepared, error) {
	pr := req.Profile
	if !model.IsRole(req.Role) || (pr.Role != "" && pr.Role != req.Role) {
		return nil, invalid("role is invalid or does not match the profile")
	}
	if req.RunID != "" && !safeIDRE.MatchString(req.RunID) {
		return nil, invalid("run id is invalid")
	}
	if !shaRE.MatchString(req.ExpectedSHA) {
		return nil, invalid("expected sha is invalid")
	}
	if req.ContextDigest != "" && !digestRE.MatchString(req.ContextDigest) {
		return nil, invalid("context digest is invalid")
	}
	if req.Context.Digest != "" && req.Context.Digest != req.ContextDigest {
		return nil, invalid("context digest does not match the frozen context")
	}
	if strings.TrimSpace(req.TaskBrief) == "" || len(req.TaskBrief) > maxBrief {
		return nil, invalid("task brief must be non-empty and bounded")
	}
	if req.RemainingTokens <= 0 || req.RemainingWall <= 0 {
		return nil, invalid("remaining token and time budgets must be positive")
	}
	out := &prepared{tokens: req.RemainingTokens, wall: req.RemainingWall}
	if m := pr.Limits.MaxTokens; m > 0 && int64(m) < out.tokens {
		out.tokens = int64(m)
	}
	if m := pr.Limits.MaxWallSeconds; m > 0 && time.Duration(m*float64(time.Second)) < out.wall {
		out.wall = time.Duration(m * float64(time.Second))
	}

	if !filepath.IsAbs(req.Worktree) {
		return nil, invalid("worktree must be absolute")
	}
	wt, err := filepath.EvalSymlinks(req.Worktree)
	if err != nil {
		return nil, invalid("worktree not found")
	}
	out.worktree = wt
	top, err := gitOut(ctx, wt, "rev-parse", "--show-toplevel")
	if rt, e2 := filepath.EvalSymlinks(top); err != nil || e2 != nil || rt != wt {
		return nil, invalid("worktree must be a Git worktree root")
	}
	gd, err1 := gitOut(ctx, wt, "rev-parse", "--absolute-git-dir")
	cd, err2 := gitOut(ctx, wt, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err1 != nil || err2 != nil || filepath.Clean(gd) == filepath.Clean(cd) {
		return nil, invalid("refusing primary worktree; use a linked worktree")
	}
	if out.branch, err = gitOut(ctx, wt, "symbolic-ref", "--short", "-q", "HEAD"); err != nil || out.branch == "" {
		return nil, invalid("worktree HEAD is detached")
	}
	if slices.Contains(protectedBranches, out.branch) {
		return nil, invalid("refusing protected branch")
	}
	if req.Checkpoint != nil {
		if req.Session == nil || req.Role == "reviewer" || checkpoint.Verify(wt, *req.Checkpoint) != nil {
			return nil, invalid("checkpoint worktree does not match")
		}
	} else if st, err := gitOut(ctx, wt, "status", "--porcelain"); err != nil || st != "" {
		return nil, invalid("worktree is not clean")
	}
	if out.baseline, err = gitOut(ctx, wt, "rev-parse", "HEAD"); err != nil || out.baseline != req.ExpectedSHA {
		return nil, invalid("worktree HEAD does not match expected sha")
	}
	if out.report, err = checkReportPath(req.ReportPath, wt); err != nil {
		return nil, err
	}
	instr, err := readInstructions(wt, pr.Instructions)
	if err != nil {
		return nil, err
	}

	env := req.Env
	if env == nil {
		env = os.Environ()
	}
	if !slices.ContainsFunc(env, func(kv string) bool { k, v, _ := strings.Cut(kv, "="); return k == pr.AuthEnv && v != "" }) {
		return nil, invalid("credential environment variable is not set")
	}
	out.env = env

	cmd := pr.PiCommand
	if len(cmd) == 0 {
		cmd = []string{"pi"}
	}
	tools := "read,bash,edit,write"
	if req.Role == "reviewer" {
		tools = "read,bash"
	}
	prompt := buildPrompt(req, wt, out.report, instr)
	if req.Session != nil {
		b := *req.Session
		if b.Worktree != wt || b.ProviderID == "" || !digestRE.MatchString(b.Digest) {
			return nil, invalid("session binding is invalid")
		}
		snap, err := p.InspectSession(b)
		if err != nil || snap.ProviderID != b.ProviderID || snap.Digest != b.Digest {
			return nil, invalid("session snapshot changed")
		}
		out.prompt = prompt
		out.argv = append(slices.Clone(cmd), "--mode", "rpc", "--session", b.File, "--no-extensions", "--no-skills", "--no-prompt-templates", "--no-context-files", "--tools", tools, "--model", pr.Provider+"/"+pr.Model)
	} else {
		out.argv = append(slices.Clone(cmd), "--print", "--mode", "json", "--no-session", "--no-extensions", "--no-skills",
			"--no-prompt-templates", "--no-context-files", "--tools", tools, "--model", pr.Provider+"/"+pr.Model, "--", prompt)
	}
	return out, nil
}

// readInstructions reads frozen instruction files confined to the worktree (os.Root refuses escapes).
func readInstructions(wt string, paths []string) ([][2]string, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	root, err := os.OpenRoot(wt)
	if err != nil {
		return nil, invalid("worktree cannot be opened")
	}
	defer root.Close()
	var out [][2]string
	total := 0
	for _, p := range paths {
		if !filepath.IsLocal(p) {
			return nil, invalid("instruction path escapes the worktree")
		}
		f, err := root.Open(p)
		if err != nil {
			return nil, invalid("instruction file missing or outside the worktree")
		}
		fi, err := f.Stat()
		var b []byte
		if err == nil && fi.Mode().IsRegular() {
			b, err = io.ReadAll(io.LimitReader(f, maxInstructionFile+1))
		}
		f.Close()
		if err != nil || fi == nil || !fi.Mode().IsRegular() || len(b) > maxInstructionFile {
			return nil, invalid("instruction file must be a regular file within size limits")
		}
		if total += len(b); total > maxInstructionTotal {
			return nil, invalid("instruction files exceed the total size limit")
		}
		out = append(out, [2]string{p, strings.TrimSpace(string(b))})
	}
	return out, nil
}

var roleHeader = map[string][]string{
	"developer": {"Implement the task below. Run relevant local checks. Finish with one scoped local commit using explicit pathspecs, leaving a clean working tree."},
	"reviewer": {"Review the current candidate commit against the task below. You are read-only: do not edit, create or delete files in the worktree, do not commit, reset, stash, or switch branches.",
		"Use read-only inspection and local checks only. Your verdict goes in the role report."},
	"polisher": {"Polish the current candidate for the task below: make only small, safe improvements. Run relevant local checks.",
		`If you change anything, finish with one scoped local commit using explicit pathspecs, leaving a clean working tree. If nothing needs changing, leave the tree and HEAD untouched and report decision "no_change".`},
}

func buildPrompt(req Request, wt, report string, instr [][2]string) string {
	parts := []string{"You are the " + req.Role + ` for project "` + req.Profile.ProjectID + `". Work only in the current Git worktree (` + wt + ")."}
	parts = append(parts, roleHeader[req.Role]...)
	parts = append(parts, "Do not push, open PRs, merge, deploy, create branches or worktrees, or touch production systems. Never print secrets.",
		"", "## Task", strings.TrimSpace(req.TaskBrief))
	if t := strings.TrimSpace(req.Context.Text); t != "" {
		parts = append(parts, "", "## Context", t)
	}
	for _, in := range instr {
		parts = append(parts, "", "## Project instructions: "+in[0], in[1])
	}
	digest, shaHint := "(none; use null)", "the final HEAD after your work (`git rev-parse HEAD`)"
	if req.ContextDigest != "" {
		digest = req.ContextDigest
	}
	shape := `{"candidateSha","contextDigest","summary","checks":[{"command","result"}],"knownGaps":[string],"decision":"changed"|"no_change"}`
	if req.Role == "reviewer" {
		shaHint = "the reviewed HEAD (" + req.ExpectedSHA + ")"
		shape = `{"candidateSha","contextDigest","verdict":"pass"|"changes_requested","summary","findings":[{"id","summary","path"?,"line"?}],"checks":[{"command","result"}],"knownGaps":[string]}`
	}
	parts = append(parts, "", "## Role report", "Report file: "+report, "Context digest: "+digest,
		"When finished, write one JSON object (max 65536 bytes) to the report file above (outside the worktree; create it as a new regular file, never a symlink).",
		"Set candidateSha to "+shaHint+" and contextDigest to the context digest above. Shape: "+shape)
	return strings.Join(parts, "\n")
}

func newRunID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func safeEvent(fn func(model.RunEvent), typ, summary string) {
	if fn == nil {
		return
	}
	defer func() { _ = recover() }()
	fn(model.RunEvent{Type: typ, Summary: summary, ObservedAt: time.Now().UTC().Format(time.RFC3339Nano)})
}

type procOutcome struct {
	t              *tracker
	proc           *Process
	exitCode       *int
	signal         string
	stop           string // "", CatCanceled, CatWallTimeout, CatTokenLimit
	spawnErr       bool
	session        *SessionOutcome
	protocolErr    bool
	checkpointSafe bool
}

func (p *Pi) run(ctx context.Context, pp *prepared, onEvent func(model.RunEvent), onStart func(Process)) procOutcome {
	o := procOutcome{t: newTracker()}
	if ctx.Err() != nil {
		o.stop = CatCanceled
		return o
	}
	cmd := exec.Command(pp.argv[0], pp.argv[1:]...)
	cmd.Dir, cmd.Env = pp.worktree, pp.env
	ownGroup(cmd) // stdin/stderr stay /dev/null: stderr may hold raw provider diagnostics
	stdout, err := cmd.StdoutPipe()
	if err == nil {
		err = cmd.Start()
	}
	if err != nil {
		o.spawnErr = true
		return o
	}
	host, _ := os.Hostname()
	pid := cmd.Process.Pid
	o.proc = &Process{Executor: "pi", PID: pid, PGID: pid, Host: host, StartedAt: time.Now().UTC()}
	if onStart != nil {
		func() { defer func() { _ = recover() }(); onStart(*o.proc) }()
	}

	var mu sync.Mutex
	stopReq := make(chan string, 1)
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		wall := time.NewTimer(pp.wall)
		defer wall.Stop()
		var reason string
		select {
		case <-done:
			return
		case <-ctx.Done():
			reason = CatCanceled
		case <-wall.C:
			reason = CatWallTimeout
		case reason = <-stopReq:
		}
		mu.Lock()
		o.stop = reason
		mu.Unlock()
		termGroup(pid)
		grace := time.NewTimer(p.Grace)
		defer grace.Stop()
		select {
		case <-done:
		case <-grace.C:
			killGroup(pid)
		}
	}()

	_ = readLines(stdout, func(b []byte) {
		ev := o.t.line(b)
		if ev == nil {
			return
		}
		if typ, sum, ok := structural(ev); ok {
			safeEvent(onEvent, typ, sum)
		}
		if o.t.liveTotal() > pp.tokens {
			select {
			case stopReq <- CatTokenLimit:
			default:
			}
		}
	}, func() { o.t.badLines++ })
	_ = cmd.Wait()
	close(done)
	wg.Wait()
	if o.stop != "" {
		killGroup(pid) // reap leftovers in our own group only
	}
	if ps := cmd.ProcessState; ps != nil {
		if c := ps.ExitCode(); c >= 0 {
			o.exitCode = &c
		}
		o.signal = exitSignal(ps)
	}
	return o
}

// Execute implements Executor. Failures return a Result (when the process was attempted) and a safe *Error.
func (p *Pi) Execute(ctx context.Context, req Request, onEvent func(model.RunEvent), onStart func(Process)) (Result, error) {
	if err := p.Validate(req.Profile); err != nil {
		return Result{}, err
	}
	pp, err := p.prepare(ctx, req)
	if err != nil {
		return Result{}, err
	}
	res := Result{RunID: req.RunID, Executor: "pi", Role: req.Role, BaselineSHA: pp.baseline, StartedAt: time.Now().UTC(),
		Model: model.ModelSnapshot{ProfileID: req.Profile.ID, Provider: req.Profile.Provider, Model: req.Profile.Model}}
	if res.RunID == "" {
		res.RunID = newRunID()
	}
	var o procOutcome
	if req.Session != nil {
		o = p.runRPC(ctx, pp, req, onEvent, onStart)
	} else {
		o = p.run(ctx, pp, onEvent, onStart)
	}
	res.EndedAt = time.Now().UTC()
	res.Process, res.ExitCode, res.Signal = o.proc, o.exitCode, o.signal
	res.Session = o.session
	res.CheckpointSafe = o.checkpointSafe
	res.Usage = o.t.usage(o.stop != "" || o.spawnErr)

	// Inspect Git state even when the parent context is canceled; never modify the worktree.
	gctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	wt := pp.worktree
	head, herr := gitOut(gctx, wt, "rev-parse", "HEAD")
	branch, _ := gitOut(gctx, wt, "symbolic-ref", "--short", "-q", "HEAD")
	st, serr := gitOut(gctx, wt, "status", "--porcelain")
	res.ResultSHA, res.Clean = head, serr == nil && st == ""
	if herr == nil && head != pp.baseline {
		res.Committed = exec.CommandContext(gctx, "git", "-C", wt, "merge-base", "--is-ancestor", pp.baseline, head).Run() == nil
	}
	rep, repCat := readReport(pp.report, req.Role)
	if rep != nil {
		want := req.ContextDigest
		got := ""
		if rep.ContextDigest != nil {
			got = *rep.ContextDigest
		}
		if rep.CandidateSHA != head || got != want || (rep.ContextDigest == nil) != (want == "") {
			rep, repCat = nil, CatReportStale
		}
	}

	cat := ""
	set := func(c string) {
		if cat == "" {
			cat = c
		}
	}
	switch {
	case o.spawnErr:
		set(CatSpawn)
	case o.stop != "":
		set(o.stop)
	case o.session != nil && !o.session.Confirmed:
		set(CatSessionUnknown)
	case o.protocolErr:
		set(CatProtocol)
	case o.t.providerError:
		set(CatProviderError)
	case o.exitCode == nil || *o.exitCode != 0:
		set(CatExit)
	case !o.t.settled:
		set(CatProtocol)
	case herr != nil || serr != nil:
		set(CatGit)
	case req.Role == "reviewer":
		if branch != pp.branch || head != pp.baseline || !res.Clean {
			set(CatReviewerMutation)
		}
	case branch != pp.branch:
		set(CatBranchChanged)
	case req.Role == "polisher" && rep != nil && rep.Decision == "no_change":
		if head != pp.baseline {
			set(CatDecisionMismatch)
		} else if !res.Clean {
			set(CatDirty)
		}
	case !res.Committed:
		set(CatNoCommit)
	case !res.Clean:
		set(CatDirty)
	}
	if repCat != "" {
		set(repCat)
	}
	if rep != nil && req.Role != "reviewer" && (rep.Decision == "changed") != res.Committed {
		set(CatDecisionMismatch)
	}
	if repCat == "" {
		res.Report = rep
	}
	res.Category = cat
	switch {
	case cat == "":
		res.Outcome = model.RunSucceeded
	case cat == CatCanceled:
		res.Outcome = model.RunStopped
	default:
		res.Outcome = model.RunFailed
	}
	safeEvent(onEvent, "lifecycle", res.Outcome)
	if cat != "" {
		return res, fail(cat)
	}
	return res, nil
}
