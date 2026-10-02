// Package cli implements the meerkat command line. Exit codes: 0 success,
// 1 failed or stopped, 2 usage or preflight error. Output is sanitized JSON.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/hunknownz/Meerkat/internal/core"
	"github.com/hunknownz/Meerkat/internal/model"
	"github.com/hunknownz/Meerkat/internal/server"
)

// Exit codes.
const (
	ExitOK     = 0
	ExitFailed = 1
	ExitUsage  = 2
)

const usage = `meerkat ` + server.Version + ` - local fenced agent workflow

Usage: meerkat <command> [flags]   (every command accepts --data-dir, default ~/.meerkat)

Daemon:
  serve     [--port 0]                       run the daemon (127.0.0.1 browser API + private socket)
Via daemon:
  prepare   --input FILE|-                   record a ready task
  execute   --task ID [--task ID] [--resume] [--acknowledge-interruption]
                                             full review workflow; exit 0 only when delivered
  run       --input FILE|- [--dry-run]       one developer run -> local candidate (not reviewed);
                                             --dry-run validates only, records nothing
  snapshot                                   public snapshot (+ local history summary)
  stop      --run ID [--request-id UUID]     request a stop (accepted != stopped)
  settings  [--input FILE|-] [--max-concurrency N] [--max-fix-rounds N]
  issue update --task ID [--apply]           prepare update; post only with --apply
Offline:
  issue read --url URL --output FILE         read an Issue into an untrusted source file
  migrate   --from DIR [--run-root DIR]... [--backup FILE]
  backup    --output FILE
  restore   --backup FILE --to NEWDIR        restore into a fresh directory only
  export    [--format json|csv] [--output FILE]
  doctor
  version | help
`

// Env carries process I/O (injectable for tests).
type Env struct {
	Stdin          io.Reader
	Stdout, Stderr io.Writer
	Ctx            context.Context
}

type usageErr struct{ msg string }

func (e usageErr) Error() string { return e.msg }

// Run executes args (without program name) and returns the exit code.
func Run(env Env, args []string) int {
	if env.Ctx == nil {
		env.Ctx = context.Background()
	}
	if len(args) == 0 {
		fmt.Fprint(env.Stderr, usage)
		return ExitUsage
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "help", "-h", "--help":
		fmt.Fprint(env.Stdout, usage)
		return ExitOK
	case "version", "--version", "-v":
		fmt.Fprintln(env.Stdout, server.Version)
		return ExitOK
	}
	run, found := commands[cmd]
	if cmd == "issue" {
		if len(rest) == 0 {
			return usageFail(env, "issue requires read or update")
		}
		run, found = commands["issue "+rest[0]]
		rest = rest[1:]
	}
	if !found {
		return usageFail(env, "unknown command")
	}
	code, err := run(env, rest)
	if err != nil {
		var ue usageErr
		if errors.As(err, &ue) || errors.Is(err, flag.ErrHelp) {
			return usageFail(env, ue.msg)
		}
		return emitErr(env, code, err)
	}
	return code
}

var commands map[string]func(Env, []string) (int, error)

func init() {
	commands = map[string]func(Env, []string) (int, error){
		"serve":        cmdServe,
		"prepare":      cmdPrepare,
		"execute":      cmdExecute,
		"run":          cmdRun,
		"snapshot":     cmdSnapshot,
		"stop":         cmdStop,
		"settings":     cmdSettings,
		"issue update": cmdIssueUpdate,
		"issue read":   cmdIssueRead,
		"migrate":      cmdMigrate,
		"backup":       cmdBackup,
		"restore":      cmdRestore,
		"export":       cmdExport,
		"doctor":       cmdDoctor,
	}
}

func usageFail(env Env, msg string) int {
	if msg == "" {
		msg = "usage error"
	}
	writeJSON(env.Stderr, map[string]any{"ok": false, "code": "usage", "error": msg})
	return ExitUsage
}

func emitErr(env Env, code int, err error) int {
	if code == ExitOK {
		code = ExitFailed
	}
	msg := err.Error()
	if model.LooksLikeCredential(msg) || len(msg) > 300 {
		msg = "error"
	}
	writeJSON(env.Stderr, map[string]any{"ok": false, "error": msg})
	return code
}

func writeJSON(w io.Writer, v any) {
	e := json.NewEncoder(w)
	e.SetIndent("", "  ")
	_ = e.Encode(v)
}

type multi []string

func (m *multi) String() string     { return strings.Join(*m, ",") }
func (m *multi) Set(v string) error { *m = append(*m, v); return nil }

// newFlags returns a flag set with --data-dir.
func newFlags(name string) (*flag.FlagSet, *string) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dd := fs.String("data-dir", "", "data directory (default ~/.meerkat)")
	return fs, dd
}

func parse(fs *flag.FlagSet, args []string) error {
	if err := fs.Parse(args); err != nil {
		return usageErr{"invalid flags: " + err.Error()}
	}
	if fs.NArg() > 0 {
		return usageErr{"unexpected argument"}
	}
	return nil
}

func dataDir(flagVal string) (string, error) {
	if flagVal == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", usageErr{"cannot resolve home directory; pass --data-dir"}
		}
		flagVal = filepath.Join(home, ".meerkat")
	}
	abs, err := filepath.Abs(flagVal)
	if err != nil {
		return "", usageErr{"invalid --data-dir"}
	}
	return abs, nil
}

func readInput(env Env, path string) ([]byte, error) {
	var r io.Reader
	if path == "-" {
		r = env.Stdin
	} else {
		f, err := os.Open(path)
		if err != nil {
			return nil, usageErr{"cannot read --input"}
		}
		defer f.Close()
		r = f
	}
	b, err := io.ReadAll(io.LimitReader(r, core.MaxInput+1))
	if err != nil || len(b) > core.MaxInput || len(b) == 0 {
		return nil, usageErr{"input missing or too large"}
	}
	if !json.Valid(b) {
		return nil, usageErr{"input is not JSON"}
	}
	return b, nil
}

// call sends req to the daemon; a missing daemon is a preflight error (2).
func call(env Env, dir string, req server.Request) (json.RawMessage, int, error) {
	resp, err := server.Call(env.Ctx, dir, req)
	if err != nil {
		if errors.Is(err, server.ErrNoDaemon) || errors.Is(err, server.ErrUnsafeSocket) {
			return nil, ExitUsage, fmt.Errorf("daemon unavailable: start `meerkat serve`")
		}
		return nil, ExitFailed, err
	}
	if !resp.OK {
		code := ExitFailed
		if resp.Code == server.CodeInvalid || resp.Code == server.CodeBusy {
			code = ExitUsage
		}
		writeJSON(env.Stderr, map[string]any{"ok": false, "code": resp.Code, "error": resp.Error})
		return nil, code, errHandled
	}
	return resp.Data, ExitOK, nil
}

var errHandled = errors.New("handled")

// remote performs a daemon call and prints {ok,data}.
func remote(env Env, dd string, req server.Request) (int, error) {
	dir, err := dataDir(dd)
	if err != nil {
		return ExitUsage, err
	}
	data, code, err := call(env, dir, req)
	if err != nil {
		if err == errHandled {
			return code, nil
		}
		return code, err
	}
	writeJSON(env.Stdout, map[string]any{"ok": true, "data": data})
	return ExitOK, nil
}
