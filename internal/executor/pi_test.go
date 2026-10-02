//go:build unix

package executor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"testing/iotest"
	"time"

	"github.com/hunknownz/Meerkat/internal/model"
)

const secret = "sk-test-not-a-real-key-0123456789abcdef"

type env struct {
	t                    *testing.T
	wt, dir, report, sha string
	args                 string
}

func run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func setup(t *testing.T) *env {
	t.Helper()
	base, _ := filepath.EvalSymlinks(t.TempDir())
	repo := filepath.Join(base, "repo")
	os.Mkdir(repo, 0o755)
	run(t, repo, "init", "-q", "-b", "base")
	run(t, repo, "config", "user.name", "t")
	run(t, repo, "config", "user.email", "t@example.com")
	os.WriteFile(filepath.Join(repo, "a.txt"), []byte("a\n"), 0o644)
	os.WriteFile(filepath.Join(repo, "AGENTS.md"), []byte("be careful\n"), 0o644)
	run(t, repo, "add", ".")
	run(t, repo, "commit", "-q", "-m", "init")
	wt := filepath.Join(base, "wt")
	run(t, repo, "worktree", "add", "-q", "-b", "task", wt)
	dir := filepath.Join(base, "private")
	os.Mkdir(dir, 0o700)
	return &env{t: t, wt: wt, dir: dir, report: filepath.Join(dir, "report.json"), sha: run(t, wt, "rev-parse", "HEAD"),
		args: filepath.Join(dir, "args.txt")}
}

// fakePi writes a tiny shell script standing in for Pi. It records argv outside the worktree.
func (e *env) fakePi(body string) string {
	p := filepath.Join(e.dir, "pi.sh")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$ARGS_OUT\"\n" + body + "\n"
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		e.t.Fatal(err)
	}
	return p
}

func (e *env) req(role, pi string) Request {
	return Request{
		Profile: model.Profile{ID: "pr", ProjectID: "proj", Role: role, Provider: "prov", Model: "m1", AuthEnv: "FAKE_KEY",
			Instructions: []string{"AGENTS.md"}, PiCommand: []string{pi}},
		Context: model.Context{ID: "c", Version: 1, Digest: "sha256:abc", Text: "context text"},
		Role:    role, Worktree: e.wt, TaskBrief: "do the thing", ReportPath: e.report, ExpectedSHA: e.sha,
		ContextDigest: "sha256:abc", RemainingTokens: 1_000_000, RemainingWall: 20 * time.Second,
		Env: []string{"PATH=" + os.Getenv("PATH"), "HOME=" + e.dir, "FAKE_KEY=" + secret, "ARGS_OUT=" + e.args,
			"REPORT=" + e.report, "GIT_CONFIG_NOSYSTEM=1"},
	}
}

const (
	usageLine = `echo '{"type":"message_end","message":{"role":"assistant","usage":{"input":100,"output":20,"cacheRead":5,"cacheWrite":1,"cost":{"total":0.25}}}}'`
	settled   = `echo '{"type":"agent_settled"}'`
	commit    = `echo x >> a.txt && git commit -qam change`
)

func devReport(decision string) string {
	return `printf '{"candidateSha":"%s","contextDigest":"sha256:abc","summary":"ok","checks":[{"command":"go test","result":"pass"}],"knownGaps":[],"decision":"` + decision + `"}' "$(git rev-parse HEAD)" > "$REPORT"`
}

func revReport(verdict string) string {
	f := "[]"
	if verdict == "changes_requested" {
		f = `[{"id":"F1","summary":"bug","path":"a.txt","line":1}]`
	}
	return `printf '{"candidateSha":"%s","contextDigest":"sha256:abc","verdict":"` + verdict + `","summary":"ok","findings":` + f + `,"checks":[],"knownGaps":[]}' "$(git rev-parse HEAD)" > "$REPORT"`
}

func category(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Category
	}
	return ""
}

func TestDeveloperSuccessUsageAndArgv(t *testing.T) {
	e := setup(t)
	pi := e.fakePi(strings.Join([]string{
		`echo '{"type":"agent_start"}'`,
		`echo '{"type":"tool_execution_start","toolName":"bash","args":{"command":"cat ` + secret + `"}}'`,
		commit,
		// fragmented line across writes
		`printf '{"type":"message_end","message":{"role":"assis'; sleep 0.05; printf 'tant","usage":{"input":1,"output":2,"cacheRead":3,"cacheWrite":4,"cost":{"total":0.5}}}}\n'`,
		usageLine, devReport("changed"), settled}, "\n"))
	var mu sync.Mutex
	var events []model.RunEvent
	var started Process
	res, err := NewPi().Execute(context.Background(), e.req("developer", pi), func(ev model.RunEvent) {
		mu.Lock()
		events = append(events, ev)
		mu.Unlock()
	}, func(p Process) { started = p })
	if err != nil {
		t.Fatalf("err=%v res=%+v", err, res)
	}
	if res.Outcome != model.RunSucceeded || res.Report == nil || res.Report.Decision != "changed" || !res.Committed || !res.Clean {
		t.Fatalf("bad result %+v", res)
	}
	u := res.Usage
	if u.UsageCompleteness != model.UsageComplete || *u.Tokens.Input != 101 || *u.Tokens.Output != 22 || *u.Tokens.CacheRead != 8 ||
		*u.Tokens.CacheWrite != 5 || *u.Tokens.Total != 136 || *u.Tokens.AssistantMessages != 2 || u.EstimatedCostUsd == nil || *u.EstimatedCostUsd != 0.75 {
		t.Fatalf("usage %+v", u)
	}
	if started.PID == 0 || res.Process == nil || res.Process.PID != started.PID || res.Process.PGID != started.PID {
		t.Fatalf("process identity %+v %+v", started, res.Process)
	}
	args, _ := os.ReadFile(e.args)
	a := string(args)
	for _, want := range []string{"--print\n--mode\njson\n--no-session\n--no-extensions\n--no-skills\n--no-prompt-templates\n--no-context-files\n--tools\nread,bash,edit,write\n--model\nprov/m1\n--\n", "be careful", "context text", e.report} {
		if !strings.Contains(a, want) {
			t.Fatalf("argv missing %q:\n%s", want, a)
		}
	}
	if strings.Contains(a, secret) || strings.Contains(a, "FAKE_KEY=") {
		t.Fatal("credential leaked into argv")
	}
	got := fmt.Sprint(events)
	if strings.Contains(got, secret) || !strings.Contains(got, "{tool bash") || !strings.Contains(got, "succeeded") {
		t.Fatalf("events %s", got)
	}
}

func TestUsagePartialOversizeAndUnknown(t *testing.T) {
	e := setup(t)
	big := filepath.Join(e.dir, "big.json")
	line := `{"type":"message_end","message":{"role":"assistant","usage":{"input":999999,"output":1,"cacheRead":0,"cacheWrite":0},"pad":"` +
		strings.Repeat("x", maxLine) + `"}}` + "\n"
	os.WriteFile(big, []byte(line), 0o600)
	pi := e.fakePi(strings.Join([]string{commit, `cat "` + big + `"`,
		`echo '{"type":"message_end","message":{"role":"assistant","usage":{"input":10,"output":2,"cacheRead":1,"cost":{"total":0.1}}}}'`,
		devReport("changed"), settled}, "\n"))
	res, err := NewPi().Execute(context.Background(), e.req("developer", pi), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	u := res.Usage
	if u.UsageCompleteness != model.UsagePartial || *u.Tokens.Input != 10 || u.Tokens.CacheWrite != nil || u.Tokens.Total != nil || u.EstimatedCostUsd != nil {
		t.Fatalf("usage %+v", u)
	}

	tr := newTracker()
	if err := readLines(iotest.OneByteReader(strings.NewReader(`{"type":"agent_settled"}`+"\n"+`{"type":"message_update","message":{"role":"assistant","usage":{"input":7}}}`)), func(b []byte) { tr.line(b) }, func() { tr.badLines++ }); err != nil {
		t.Fatal(err)
	}
	if u := tr.usage(false); u.UsageCompleteness != model.UsageUnknown || u.Tokens.Input != nil || u.Tokens.Total != nil || tr.liveTotal() != 7 {
		t.Fatalf("unknown usage %+v", u)
	}
}

func TestReviewer(t *testing.T) {
	e := setup(t)
	pi := e.fakePi(revReport("changes_requested") + "\n" + settled)
	res, err := NewPi().Execute(context.Background(), e.req("reviewer", pi), nil, nil)
	if err != nil || res.Report == nil || res.Report.Verdict != "changes_requested" || len(res.Report.Findings) != 1 {
		t.Fatalf("err=%v res=%+v", err, res)
	}
	if a, _ := os.ReadFile(e.args); !strings.Contains(string(a), "--tools\nread,bash\n") {
		t.Fatal("reviewer must get read,bash tools")
	}

	e = setup(t)
	pi = e.fakePi("echo hacked > new.txt\n" + revReport("pass") + "\n" + settled)
	res, err = NewPi().Execute(context.Background(), e.req("reviewer", pi), nil, nil)
	if category(err) != CatReviewerMutation || res.Outcome != model.RunFailed {
		t.Fatalf("want reviewer_mutation, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(e.wt, "new.txt")); err != nil {
		t.Fatal("dirty code must be preserved")
	}
}

func TestReportBindingFailures(t *testing.T) {
	cases := map[string]struct{ body, cat string }{
		"stale sha":      {commit + "\n" + `printf '{"candidateSha":"%s","contextDigest":"sha256:abc","summary":"s","checks":[],"knownGaps":[],"decision":"changed"}' "$(git rev-parse HEAD~1)" > "$REPORT"`, CatReportStale},
		"stale digest":   {commit + "\n" + strings.Replace(devReport("changed"), "sha256:abc", "sha256:zzz", 1), CatReportStale},
		"missing":        {commit, CatReportMissing},
		"bad schema":     {commit + "\n" + `printf '{"candidateSha":"%s","contextDigest":"sha256:abc","summary":"","checks":[],"knownGaps":[],"decision":"changed"}' "$(git rev-parse HEAD)" > "$REPORT"`, CatReportInvalid},
		"oversize":       {commit + "\n" + `head -c 70000 /dev/zero | tr '\0' ' ' > "$REPORT"`, CatReportInvalid},
		"symlink":        {commit + "\n" + `ln -s /etc/hosts "$REPORT"`, CatReportInvalid},
		"credential":     {commit + "\n" + strings.Replace(devReport("changed"), `"summary":"ok"`, `"summary":"`+secret+`"`, 1), CatReportInvalid},
		"wrong decision": {commit + "\n" + devReport("no_change"), CatDecisionMismatch},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			e := setup(t)
			res, err := NewPi().Execute(context.Background(), e.req("developer", e.fakePi(c.body+"\n"+settled)), nil, nil)
			if category(err) != c.cat || res.Report != nil && c.cat != CatDecisionMismatch {
				t.Fatalf("want %s got %v report=%v", c.cat, err, res.Report)
			}
			if strings.Contains(err.Error(), secret) {
				t.Fatal("secret in error")
			}
		})
	}
}

func TestPolisher(t *testing.T) {
	e := setup(t)
	if _, err := NewPi().Execute(context.Background(), e.req("polisher", e.fakePi(devReport("no_change")+"\n"+settled)), nil, nil); err != nil {
		t.Fatalf("clean no_change: %v", err)
	}
	e = setup(t)
	_, err := NewPi().Execute(context.Background(), e.req("polisher", e.fakePi("echo y > a.txt\n"+devReport("no_change")+"\n"+settled)), nil, nil)
	if category(err) != CatDirty {
		t.Fatalf("dirty no_change: %v", err)
	}
	e = setup(t)
	_, err = NewPi().Execute(context.Background(), e.req("polisher", e.fakePi(commit+"\n"+devReport("no_change")+"\n"+settled)), nil, nil)
	if category(err) != CatDecisionMismatch {
		t.Fatalf("committed no_change: %v", err)
	}
}

func TestProcessFailures(t *testing.T) {
	cases := map[string]struct{ body, cat string }{
		"exit":     {"echo y > a.txt\n" + settled + "\nexit 3", CatExit},
		"provider": {`echo '{"type":"message_end","message":{"role":"assistant","stopReason":"error","errorMessage":"RAW-PROVIDER-ERR","usage":{}}}'` + "\n" + settled, CatProviderError},
		"protocol": {commit + "\n" + devReport("changed"), CatProtocol},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			e := setup(t)
			res, err := NewPi().Execute(context.Background(), e.req("developer", e.fakePi(c.body)), nil, nil)
			if category(err) != c.cat || strings.Contains(err.Error(), "RAW-PROVIDER-ERR") {
				t.Fatalf("want %s got %v", c.cat, err)
			}
			if name == "exit" {
				if b, _ := os.ReadFile(filepath.Join(e.wt, "a.txt")); string(b) != "y\n" || res.ExitCode == nil || *res.ExitCode != 3 {
					t.Fatal("dirty code must be preserved")
				}
			}
		})
	}
}

func alive(pid int) bool { return syscall.Kill(pid, 0) == nil }

// stubborn ignores SIGTERM and leaves a grandchild in its group, so stop must escalate to SIGKILL.
const stubborn = `trap '' TERM
sleep 30 &
echo $! > "$REPORT.child"
echo '{"type":"agent_start"}'
while :; do sleep 0.05; done`

func TestStopOwnedProcessGroup(t *testing.T) {
	for _, mode := range []string{"cancel", "wall", "tokens"} {
		t.Run(mode, func(t *testing.T) {
			e := setup(t)
			body := stubborn
			if mode == "tokens" {
				body = strings.Replace(body, "while", `echo '{"type":"message_update","message":{"role":"assistant","usage":{"input":5000}}}'`+"\nwhile", 1)
			}
			req := e.req("developer", e.fakePi(body))
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch mode {
			case "wall":
				req.RemainingWall = time.Second
			case "tokens":
				req.RemainingTokens = 1000
			}
			p := &Pi{Grace: 200 * time.Millisecond}
			start := time.Now()
			res, err := p.Execute(ctx, req, func(ev model.RunEvent) {
				if mode == "cancel" && ev.Summary == "started" {
					cancel() // the child pid is recorded before agent_start
				}
			}, nil)
			want := map[string]string{"cancel": CatCanceled, "wall": CatWallTimeout, "tokens": CatTokenLimit}[mode]
			if category(err) != want || time.Since(start) > 10*time.Second {
				t.Fatalf("want %s got %v", want, err)
			}
			if (mode == "cancel") != (res.Outcome == model.RunStopped) || res.Usage.UsageCompleteness == model.UsageComplete {
				t.Fatalf("outcome %s usage %s", res.Outcome, res.Usage.UsageCompleteness)
			}
			b, _ := os.ReadFile(e.report + ".child")
			child, _ := strconv.Atoi(strings.TrimSpace(string(b)))
			time.Sleep(100 * time.Millisecond)
			if child == 0 || alive(child) || alive(res.Process.PID) {
				t.Fatalf("owned processes still alive (child %d)", child)
			}
		})
	}
}

func TestValidationAndPreflight(t *testing.T) {
	if e, err := Lookup(""); err != nil || e.Name() != "pi" {
		t.Fatal("missing executor must default to pi")
	}
	if _, err := Lookup("codex"); category(err) != CatInvalidRequest {
		t.Fatal("unsupported executor must error")
	}
	e := setup(t)
	pi := e.fakePi(settled)
	mut := map[string]func(*Request){
		"unsupported executor": func(r *Request) { r.Profile.Executor = "codex" },
		"missing key":          func(r *Request) { r.Env = r.Env[:2] },
		"instruction escape":   func(r *Request) { r.Profile.Instructions = []string{"../repo/a.txt"} },
		"absolute instruction": func(r *Request) { r.Profile.Instructions = []string{"/etc/hosts"} },
		"report in worktree":   func(r *Request) { r.ReportPath = filepath.Join(e.wt, "r.json") },
		"report exists":        func(r *Request) { r.ReportPath = e.args; os.WriteFile(e.args, nil, 0o600) },
		"wrong sha":            func(r *Request) { r.ExpectedSHA = strings.Repeat("0", 40) },
		"digest mismatch":      func(r *Request) { r.ContextDigest = "sha256:other" },
		"no budget":            func(r *Request) { r.RemainingTokens = 0 },
		"role mismatch":        func(r *Request) { r.Role = "reviewer" },
	}
	for name, m := range mut {
		r := e.req("developer", pi)
		m(&r)
		if _, err := NewPi().Execute(context.Background(), r, nil, nil); category(err) != CatInvalidRequest {
			t.Errorf("%s: want invalid_request, got %v", name, err)
		}
	}
	pub := filepath.Join(filepath.Dir(e.dir), "public")
	os.Mkdir(pub, 0o755)
	os.Chmod(pub, 0o755)
	if _, err := checkReportPath(filepath.Join(pub, "r.json"), e.wt); err == nil {
		t.Error("non-private report directory must be refused")
	}
	// Symlink escape from an instruction path is refused by os.Root.
	d := t.TempDir()
	os.Symlink("/etc/hosts", filepath.Join(d, "link.md"))
	if _, err := readInstructions(d, []string{"link.md"}); err == nil {
		t.Error("symlink escape must be refused")
	}
}
