package executor

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
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
	worktree, branch, baseline, roleBaseline, report string
	argv                                             []string
	env                                              []string
	tokens                                           int64
	wall                                             time.Duration
	prompt                                           string
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
	out.roleBaseline = out.baseline
	if req.RoleBaselineSHA != "" {
		if !shaRE.MatchString(req.RoleBaselineSHA) || req.Checkpoint == nil || req.Role == "reviewer" {
			return nil, invalid("role baseline requires a verified coding checkpoint")
		}
		out.roleBaseline = req.RoleBaselineSHA
		if out.roleBaseline != out.baseline {
			if exec.CommandContext(ctx, "git", "-C", wt, "merge-base", "--is-ancestor", out.roleBaseline, out.baseline).Run() != nil {
				return nil, invalid("checkpoint role baseline is not an ancestor")
			}
			count, e := gitOut(ctx, wt, "rev-list", "--count", out.roleBaseline+".."+out.baseline)
			paths, pe := gitOut(ctx, wt, "diff", "--name-only", "--no-renames", "-z", out.roleBaseline, out.baseline)
			if e != nil || pe != nil || count != "1" || paths == "" {
				return nil, invalid("checkpoint must contain one scoped provisional commit")
			}
			for _, path := range strings.Split(strings.TrimSuffix(paths, "\x00"), "\x00") {
				if !slices.ContainsFunc(req.Checkpoint.Scope, func(scope string) bool {
					return path == scope || strings.HasPrefix(path, strings.TrimSuffix(scope, "/")+"/")
				}) {
					return nil, invalid("checkpoint commit is outside task scope")
				}
			}
		}
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
	if filepath.Base(cmd[0]) == "env" {
		if len(cmd) < 3 || !strings.HasPrefix(cmd[1], "PI_CODING_AGENT_DIR=") {
			return nil, invalid("unsupported executor environment wrapper")
		}
		agentDir := strings.TrimPrefix(cmd[1], "PI_CODING_AGENT_DIR=")
		if !filepath.IsAbs(agentDir) {
			return nil, invalid("executor directory must be absolute")
		}
		out.env = append(slices.DeleteFunc(slices.Clone(env), func(kv string) bool { return strings.HasPrefix(kv, "PI_CODING_AGENT_DIR=") }), cmd[1])
		cmd = cmd[2:]
	}
	tools := "read,bash,edit,write"
	if req.Role == "reviewer" {
		tools = "read,bash"
	}
	if req.Session != nil && req.Budget != nil {
		tools += ",meerkat_report"
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
	if req.Checkpoint != nil {
		parts = append(parts, "This is an explicit continuation of your saved session, not a new task. Keep the prior investigation and plan; do not repeat repository discovery or reread unchanged files already present in that history. Verify the saved working changes, then implement the remaining scope using targeted reads and batched edits/checks. All previous usage remains charged to this task.")
		if req.RoleBaselineSHA != "" && req.RoleBaselineSHA != req.ExpectedSHA {
			parts = append(parts, "The saved HEAD is a provisional commit after the original role baseline "+req.RoleBaselineSHA+". Complete that one scoped commit; amend it if necessary, never add a second commit. Report decision changed even if no further edits are needed: the role has already changed the original baseline.")
		}
	}
	parts = append(parts, "Do not push, open PRs, merge, deploy, create branches or worktrees, or touch production systems. Never print secrets.",
		"", "## Task", strings.TrimSpace(req.TaskBrief))
	if t := strings.TrimSpace(req.Context.Text); t != "" {
		parts = append(parts, "", "## Context", t)
	}
	for _, in := range instr {
		parts = append(parts, "", "## Project instructions: "+in[0], in[1])
	}
	if req.Session != nil && req.Budget != nil {
		parts = append(parts, "", "## Role report", "After finishing your scoped work and any required commit, call meerkat_report. Supply summary, checks with exact command/result fields, knownGaps, and decision (developer/polisher) or verdict/findings (reviewer). Do not write the private report file manually. Do not supply candidateSha or contextDigest: Go derives those exact bindings. Include only checks you actually ran; use [] if none. A successful tool reply confirms the report was saved, not final delivery. End the turn after a successful report; a new commit would invalidate that report.")
		return strings.Join(parts, "\n")
	}
	digest, shaHint := "null (write null when there is no frozen context)", "the final HEAD after your work (`git rev-parse HEAD`)"
	if req.ContextDigest != "" {
		digest = req.ContextDigest
	}
	if req.Role == "reviewer" {
		shaHint = "the reviewed HEAD (" + req.ExpectedSHA + ")"
	}
	parts = append(parts, "", "## Role report", "Report file: "+report, "Context digest: "+digest,
		"When finished, write one JSON object (max 65536 bytes) to the report file above (outside the worktree; create it as a new regular file, never a symlink).",
		"Set candidateSha to "+shaHint+" and contextDigest to the literal context digest above: copy it byte-for-byte including any sha256: prefix; never normalize, truncate, or re-derive it; write the JSON null value when the digest is null.",
		"Replace example summary, decision/verdict, findings and gaps with your actual result. checks must contain only commands you actually ran with observed outcomes; leave it empty if none ran. The example is not evidence of a passing task.",
		"The report must be valid JSON matching this exact shape:", "", "```json", reportExample(req.Role, req.ContextDigest, req.ExpectedSHA), "```")
	return strings.Join(parts, "\n")
}

// reportExample renders a deterministic, valid JSON role report example. The
// reviewer example pins candidateSha to the exact ExpectedSHA; developer and
// polisher examples carry a placeholder the model must substitute with the
// final HEAD. contextDigest is embedded literally, or null when absent.
func reportExample(role, contextDigest, expectedSHA string) string {
	checks := []map[string]string{}
	digest := any(nil)
	if contextDigest != "" {
		digest = contextDigest
	}
	sha := "<final HEAD from git rev-parse HEAD>"
	if role == "reviewer" {
		sha = expectedSHA
	}
	var ex any
	if role == "reviewer" {
		ex = struct {
			CandidateSHA  string              `json:"candidateSha"`
			ContextDigest any                 `json:"contextDigest"`
			Verdict       string              `json:"verdict"`
			Summary       string              `json:"summary"`
			Findings      []map[string]string `json:"findings"`
			Checks        []map[string]string `json:"checks"`
			KnownGaps     []string            `json:"knownGaps"`
		}{CandidateSHA: sha, ContextDigest: digest, Verdict: "pass", Summary: "<summarize the actual review>", Findings: []map[string]string{}, Checks: checks, KnownGaps: []string{}}
	} else {
		ex = struct {
			CandidateSHA  string              `json:"candidateSha"`
			ContextDigest any                 `json:"contextDigest"`
			Summary       string              `json:"summary"`
			Checks        []map[string]string `json:"checks"`
			KnownGaps     []string            `json:"knownGaps"`
			Decision      string              `json:"decision"`
		}{CandidateSHA: sha, ContextDigest: digest, Summary: "<summarize the actual work>", Checks: checks, KnownGaps: []string{}, Decision: "changed"}
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false) // keep <final HEAD ...> and Unicode readable in the example
	if err := enc.Encode(ex); err != nil {
		return ""
	}
	return strings.TrimSpace(buf.String())
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
	pauseRequested bool
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
		if err == nil {
			err = bindGroup(cmd)
			if err != nil {
				cmd.Process.Kill()
				cmd.Wait()
			}
		}
	}
	if err != nil {
		o.spawnErr = true
		return o
	}
	host, _ := os.Hostname()
	pid := cmd.Process.Pid
	defer releaseGroup(pid)
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
	if herr == nil && head != pp.roleBaseline {
		res.Committed = exec.CommandContext(gctx, "git", "-C", wt, "merge-base", "--is-ancestor", pp.roleBaseline, head).Run() == nil
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
		if head != pp.roleBaseline {
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
	if o.pauseRequested && (cat == CatNoCommit || cat == CatDirty || cat == CatReportMissing) {
		cat = CatPauseRequested
	}
	if repCat == "" {
		res.Report = rep
	}
	res.Category = cat
	switch {
	case cat == "":
		res.Outcome = model.RunSucceeded
	case cat == CatCanceled || cat == CatPauseRequested:
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
