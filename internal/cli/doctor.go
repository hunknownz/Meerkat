package cli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"time"

	"github.com/hunknownz/Meerkat/internal/core"
	"github.com/hunknownz/Meerkat/internal/executor"
	"github.com/hunknownz/Meerkat/internal/model"
	"github.com/hunknownz/Meerkat/internal/server"
	"github.com/hunknownz/Meerkat/internal/store"
)

func doctorCheck(id, status, message, next string) executor.DiagnosticCheck {
	return executor.DiagnosticCheck{ID: id, Status: status, Message: message, Next: next}
}

// cmdDoctor only diagnoses. It never opens the migrating store or applies a
// recovery. The explicit version probe uses a separate temporary directory.
func cmdDoctor(env Env, args []string) (int, error) {
	fs, dd := newFlags("doctor")
	profile := fs.String("profile", "", "one private profile to inspect")
	probe := fs.Bool("probe-executor", false, "isolated executable version check; requires --profile")
	if err := parse(fs, args); err != nil {
		return ExitUsage, err
	}
	if *probe && *profile == "" {
		return ExitUsage, usageErr{"--probe-executor requires --profile"}
	}
	dir, err := dataDir(*dd)
	if err != nil {
		return ExitUsage, err
	}
	ctx, cancel := context.WithTimeout(env.Ctx, 10*time.Second)
	defer cancel()
	rep := map[string]any{"schemaVersion": 1, "version": server.Version, "dataDir": dir, "daemonActive": nil}
	checks := []executor.DiagnosticCheck{}
	add := func(id, status, message, next string) {
		checks = append(checks, doctorCheck(id, status, message, next))
	}
	_, err = os.Lstat(dir)
	switch {
	case errors.Is(err, os.ErrNotExist):
		rep["dataDirExists"] = false
		rep["daemonActive"] = false
		add("data.directory", "warning", "The private data directory does not exist; nothing was created.", "Configure the runtime and start the service.")
	case err != nil || server.CheckPrivateDir(dir) != nil:
		rep["dataDirPrivate"] = false
		add("data.directory", "blocked", "The data directory is unreadable or is not a private 0700 directory owned by this user.", "Inspect the path, ownership and permissions; doctor does not repair them.")
	default:
		rep["dataDirExists"] = true
		rep["dataDirPrivate"] = true
		add("data.directory", "ok", "The private data directory is safe to inspect.", "")
		doctorService(ctx, dir, rep, &checks)
		_, err = os.Lstat(filepath.Join(dir, "meerkat.db"))
		if errors.Is(err, os.ErrNotExist) {
			add("store.database", "warning", "The database is absent; no database was created.", "Start the service to initialize local state.")
		} else {
			d, e := store.InspectReadOnly(ctx, dir)
			if e != nil {
				rep["storeOpen"] = false
				if d.Schema > 0 {
					rep["storeSchema"] = d.Schema
				}
				add("store.database", "blocked", "The database is unsafe, unsupported, corrupt or could not be inspected within the deadline.", "Check a backup and permissions; do not run migration or recovery until the cause is understood.")
			} else {
				rep["store"] = d
				rep["imports"] = d.Imports
				rep["lease"] = map[string]any{"present": d.LeasePresent, "heartbeatAt": d.LeaseHeartbeat, "stale": nil}
				add("store.database", "ok", "The read-only database snapshot passed integrity and foreign-key checks.", "")
				if d.Schema < d.SupportedSchema {
					add("store.schema", "warning", "The database uses an older supported schema; missing evidence remains null.", "Back up state before an intentional runtime migration; doctor does not migrate.")
				}
				if d.LeasePresent && rep["daemonActive"] != true {
					add("service.lease", "warning", "A controller lease is recorded but no healthy service was confirmed; PID identity was not checked.", "Investigate the service before restarting; a recorded PID is not authority to stop a process.")
				}
				doctorLifecycle(d, rep["daemonActive"] == true, &checks)
			}
		}
	}
	if *profile == "" {
		add("executor.profile", "not_checked", "No profile was selected; executor, model and credentials were not inspected.", "Supply --profile /absolute/private/profile.json to check one profile.")
	} else {
		p, e := core.InspectProfile(*profile)
		if e != nil {
			add("executor.profile", "blocked", "The selected profile failed the same private-file/config validation used by prepare.", "Check the absolute path, private ownership, config fields and limits.")
		} else {
			add("executor.profile", "ok", "The selected private profile is structurally valid; no task was created.", "")
			x, e := executor.Lookup(p.Executor)
			if e != nil || x.Validate(p) != nil {
				add("executor.adapter", "blocked", "The selected executor is not supported or its profile is invalid.", "Pi is currently the only implemented executor.")
			} else if dx, ok := x.(executor.DiagnosticExecutor); ok {
				checks = append(checks, dx.Diagnose(ctx, p, *probe)...)
			} else {
				add("executor.adapter", "not_checked", "This executor has no diagnostic adapter.", "")
			}
		}
	}
	status := "ok"
	for _, c := range checks {
		if c.Status == "blocked" {
			status = "blocked"
			break
		}
		if c.Status == "warning" {
			status = "warning"
		}
	}
	rep["status"] = status
	rep["checks"] = checks
	rep["executionReadiness"] = "not_verified"
	writeJSON(env.Stdout, map[string]any{"ok": status != "blocked", "data": rep})
	if status == "blocked" {
		return ExitUsage, nil
	}
	return ExitOK, nil
}

var doctorVersion = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:-[A-Za-z0-9.]+)?$`)

func doctorService(ctx context.Context, dir string, rep map[string]any, checks *[]executor.DiagnosticCheck) {
	add := func(status, message, next string) {
		*checks = append(*checks, doctorCheck("service.health", status, message, next))
	}
	if _, e := os.Lstat(server.SocketPath(dir)); runtime.GOOS != "windows" && errors.Is(e, os.ErrNotExist) {
		rep["daemonActive"] = false
		add("warning", "No service socket is present.", "Start the service to run tasks.")
		return
	}
	cctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	r, e := server.Call(cctx, dir, server.Request{Op: "health"})
	var health struct {
		Version string `json:"version"`
	}
	if e != nil || !r.OK || json.Unmarshal(r.Data, &health) != nil || len(health.Version) > 64 || !doctorVersion.MatchString(health.Version) || model.LooksLikeCredential(health.Version) {
		add("blocked", "The service socket is unsafe, unreachable or did not return a valid health response; service identity remains unknown.", "Inspect the existing service and socket before restarting; doctor does not remove or signal them.")
		return
	}
	rep["daemonActive"] = true
	rep["serviceVersion"] = health.Version
	if health.Version != server.Version {
		add("blocked", "The service and CLI versions do not match.", "Complete an idle, backed-up runtime switch before sending task commands.")
		return
	}
	add("ok", "The private socket returned the matching service version.", "")
}

func doctorLifecycle(d store.Diagnostics, alive bool, checks *[]executor.DiagnosticCheck) {
	add := func(id, status, message, next string) {
		*checks = append(*checks, doctorCheck(id, status, message, next))
	}
	unknown := d.Runs["unknown"] + d.Sessions["unknown"] + d.Requests["unknown"] + d.BudgetRuns["unknown"] + d.Controls["unknown"]
	pending := d.Runs["starting"] + d.Runs["running"] + d.Runs["stopping"] + d.Sessions["running"] + d.Requests["reserved"] + d.Requests["sent"] + d.BudgetRuns["open"] + d.Controls["accepted"] + d.Controls["sending"]
	switch {
	case unknown > 0:
		add("execution.state", "blocked", "Stored Runs, sessions, requests or controls have unknown outcomes; continuation of affected work is blocked.", "Inspect task recovery evidence explicitly; never replay uncertain runs or requests.")
	case pending > 0 && !alive:
		add("execution.state", "blocked", "Unfinished execution records exist without a confirmed healthy owner.", "Investigate the owner and recovery evidence; do not automatically replay or stop by an old PID.")
	case pending > 0:
		add("execution.state", "warning", "Execution records are pending under a healthy service; they are not settled outcomes.", "Check the task/operation snapshot for current progress.")
	default:
		add("execution.state", "ok", "Available stored lifecycle groups contain no pending or unknown records.", "")
	}
	if d.Sessions == nil || d.Requests == nil || d.Controls == nil {
		add("execution.evidence", "not_checked", "This older schema has no complete session/request/control evidence.", "Missing evidence is null, not zero.")
	}
	add("execution.identity", "not_checked", "Stored state counts do not verify session history, frozen contracts or process identity.", "Execution and explicit recovery verify those bindings before continuation.")
}
