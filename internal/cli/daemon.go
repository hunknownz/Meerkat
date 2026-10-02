package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/hunknownz/Meerkat/internal/core"
	"github.com/hunknownz/Meerkat/internal/model"
	"github.com/hunknownz/Meerkat/internal/server"
	"github.com/hunknownz/Meerkat/internal/store"
)

func cmdServe(env Env, args []string) (int, error) {
	fs, dd := newFlags("serve")
	port := fs.Int("port", 0, "loopback port (0 = random)")
	if err := parse(fs, args); err != nil {
		return ExitUsage, err
	}
	if *port < 0 || *port > 65535 {
		return ExitUsage, usageErr{"invalid --port"}
	}
	dir, err := dataDir(*dd)
	if err != nil {
		return ExitUsage, err
	}
	if server.Alive(dir) {
		return ExitUsage, errors.New("daemon already active for this data directory")
	}
	st, err := store.Open(dir)
	if err != nil {
		return ExitUsage, errors.New("cannot open data directory (must be private 0700 and owned by you)")
	}
	defer st.Close()
	c, err := core.New(st, server.Registry())
	if err != nil {
		if errors.Is(err, store.ErrLeaseHeld) {
			return ExitUsage, errors.New("controller lease held by another live owner")
		}
		return ExitUsage, errors.New("cannot start core")
	}
	svc, err := server.New(c, st, nil)
	if err != nil {
		c.Close()
		return ExitFailed, err
	}
	ul, err := server.ListenUnix(dir)
	if err != nil {
		svc.Shutdown()
		return ExitUsage, errors.New("cannot create private command socket")
	}
	hl, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(*port)))
	if err != nil {
		ul.Close()
		svc.Shutdown()
		return ExitUsage, errors.New("cannot bind loopback port")
	}
	bound := hl.Addr().(*net.TCPAddr).Port
	hs := &http.Server{Handler: svc.HTTPHandler(bound), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second}
	ctx, stop := signal.NotifyContext(env.Ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go hs.Serve(hl)
	go svc.ServeUnix(ul)
	writeJSON(env.Stdout, map[string]any{"ok": true, "url": fmt.Sprintf("http://127.0.0.1:%d/", bound),
		"dataDir": dir, "socket": server.SocketPath(dir), "version": server.Version})
	<-ctx.Done()
	// Graceful: stop accepting, cancel owned operations, close core, then HTTP.
	ul.Close()
	sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go hs.Shutdown(sctx) // SSE streams end once the service context is canceled
	if err := svc.Shutdown(); err != nil {
		return ExitFailed, errors.New("core close failed")
	}
	_ = hs.Shutdown(sctx)
	return ExitOK, nil
}

func cmdPrepare(env Env, args []string) (int, error) {
	fs, dd := newFlags("prepare")
	in := fs.String("input", "", "task input JSON file or -")
	if err := parse(fs, args); err != nil {
		return ExitUsage, err
	}
	if *in == "" {
		return ExitUsage, usageErr{"--input required"}
	}
	b, err := readInput(env, *in)
	if err != nil {
		return ExitUsage, err
	}
	return remote(env, *dd, server.Request{Op: "prepare", Input: b})
}

func cmdExecute(env Env, args []string) (int, error) {
	fs, dd := newFlags("execute")
	var tasks multi
	fs.Var(&tasks, "task", "task id (repeatable)")
	resume := fs.Bool("resume", false, "resume interrupted tasks")
	ack := fs.Bool("acknowledge", false, "acknowledge risk prompts")
	if err := parse(fs, args); err != nil {
		return ExitUsage, err
	}
	if len(tasks) == 0 {
		return ExitUsage, usageErr{"--task required"}
	}
	return execute(env, *dd, tasks, *resume, *ack)
}

func execute(env Env, dd string, tasks []string, resume, ack bool) (int, error) {
	dir, err := dataDir(dd)
	if err != nil {
		return ExitUsage, err
	}
	data, code, err := call(env, dir, server.Request{Op: "execute", Tasks: tasks, Resume: resume, Acknowledge: ack})
	if err != nil {
		if err == errHandled {
			return code, nil
		}
		return code, err
	}
	var r core.Result
	if json.Unmarshal(data, &r) != nil {
		return ExitFailed, errors.New("invalid daemon reply")
	}
	code = resultCode(r)
	writeJSON(env.Stdout, map[string]any{"ok": code == ExitOK, "data": r})
	return code, nil
}

func resultCode(r core.Result) int {
	if r.Fatal != "" || r.Stopped != "" {
		return ExitFailed
	}
	for _, t := range r.Tasks {
		switch t.State {
		case model.TaskFailed, model.TaskStopped, model.TaskUnknown, model.TaskBlocked:
			return ExitFailed
		}
	}
	return ExitOK
}

func cmdRun(env Env, args []string) (int, error) {
	fs, dd := newFlags("run")
	in := fs.String("input", "", "task input JSON file or -")
	ack := fs.Bool("acknowledge", false, "acknowledge risk prompts")
	if err := parse(fs, args); err != nil {
		return ExitUsage, err
	}
	if *in == "" {
		return ExitUsage, usageErr{"--input required"}
	}
	b, err := readInput(env, *in)
	if err != nil {
		return ExitUsage, err
	}
	dir, err := dataDir(*dd)
	if err != nil {
		return ExitUsage, err
	}
	data, code, err := call(env, dir, server.Request{Op: "prepare", Input: b})
	if err != nil {
		if err == errHandled {
			return code, nil
		}
		return code, err
	}
	var t model.Task
	if json.Unmarshal(data, &t) != nil || t.ID == "" {
		return ExitFailed, errors.New("invalid daemon reply")
	}
	return execute(env, dir, []string{t.ID}, false, *ack)
}

func cmdSnapshot(env Env, args []string) (int, error) {
	fs, dd := newFlags("snapshot")
	if err := parse(fs, args); err != nil {
		return ExitUsage, err
	}
	return remote(env, *dd, server.Request{Op: "snapshot"})
}

func cmdStop(env Env, args []string) (int, error) {
	fs, dd := newFlags("stop")
	run := fs.String("run", "", "run id")
	rid := fs.String("request-id", "", "idempotency UUID (generated if empty)")
	if err := parse(fs, args); err != nil {
		return ExitUsage, err
	}
	if *run == "" {
		return ExitUsage, usageErr{"--run required"}
	}
	return remote(env, *dd, server.Request{Op: "stop", RunID: *run, RequestID: *rid})
}

func cmdSettings(env Env, args []string) (int, error) {
	fs, dd := newFlags("settings")
	in := fs.String("input", "", "settings patch JSON file or -")
	mc := fs.Int("max-concurrency", -1, "max concurrency 1..4")
	mf := fs.Int("max-fix-rounds", -1, "max fix rounds 0..2")
	if err := parse(fs, args); err != nil {
		return ExitUsage, err
	}
	var b []byte
	switch {
	case *in != "" && (*mc >= 0 || *mf >= 0):
		return ExitUsage, usageErr{"use --input or flags, not both"}
	case *in != "":
		var err error
		if b, err = readInput(env, *in); err != nil {
			return ExitUsage, err
		}
	default:
		p := map[string]int{}
		if *mc >= 0 {
			p["maxConcurrency"] = *mc
		}
		if *mf >= 0 {
			p["maxFixRounds"] = *mf
		}
		b, _ = json.Marshal(p)
	}
	return remote(env, *dd, server.Request{Op: "settings", Input: b})
}

func cmdIssueUpdate(env Env, args []string) (int, error) {
	fs, dd := newFlags("issue update")
	task := fs.String("task", "", "task id")
	apply := fs.Bool("apply", false, "actually post the comment (explicit only)")
	if err := parse(fs, args); err != nil {
		return ExitUsage, err
	}
	if *task == "" {
		return ExitUsage, usageErr{"--task required"}
	}
	return remote(env, *dd, server.Request{Op: "issue-update", Tasks: []string{*task}, Apply: *apply})
}
