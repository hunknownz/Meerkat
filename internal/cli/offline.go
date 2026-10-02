package cli

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/hunknownz/Meerkat/internal/issues"
	"github.com/hunknownz/Meerkat/internal/model"
	"github.com/hunknownz/Meerkat/internal/server"
	"github.com/hunknownz/Meerkat/internal/store"
)

// openOffline opens the store only when no daemon owns it.
func openOffline(dd string) (*store.Store, int, error) {
	dir, err := dataDir(dd)
	if err != nil {
		return nil, ExitUsage, err
	}
	if server.Alive(dir) {
		return nil, ExitUsage, errors.New("daemon active: stop `meerkat serve` first")
	}
	st, err := store.Open(dir)
	if err != nil {
		return nil, ExitUsage, errors.New("cannot open data directory (must be private 0700 and owned by you)")
	}
	return st, ExitOK, nil
}

func absPath(p, name string) (string, error) {
	if p == "" {
		return "", usageErr{name + " required"}
	}
	a, err := filepath.Abs(p)
	if err != nil {
		return "", usageErr{"invalid " + name}
	}
	return a, nil
}

func cmdIssueRead(env Env, args []string) (int, error) {
	fs, _ := newFlags("issue read")
	url := fs.String("url", "", "Issue URL")
	out := fs.String("output", "", "output file (new, 0600)")
	if err := parse(fs, args); err != nil {
		return ExitUsage, err
	}
	if *url == "" {
		return ExitUsage, usageErr{"--url required"}
	}
	path, err := absPath(*out, "--output")
	if err != nil {
		return ExitUsage, err
	}
	client := &issues.Client{Runner: issues.ExecRunner{}}
	src, err := client.Read(env.Ctx, *url)
	if err != nil {
		return ExitFailed, errors.New("issue read failed: " + issues.Category(err))
	}
	if err := issues.WriteSource(path, src); err != nil {
		return ExitFailed, errors.New("cannot write source file")
	}
	writeJSON(env.Stdout, map[string]any{"ok": true, "data": map[string]any{
		"output": path, "snapshot": src.Snapshot, "sha256": src.SHA256, "untrusted": true}})
	return ExitOK, nil
}

func cmdMigrate(env Env, args []string) (int, error) {
	fs, dd := newFlags("migrate")
	from := fs.String("from", "", "0.2.x data directory")
	var roots multi
	fs.Var(&roots, "run-root", "run root directory (repeatable)")
	backup := fs.String("backup", "", "write a backup of the destination first")
	if err := parse(fs, args); err != nil {
		return ExitUsage, err
	}
	src, err := absPath(*from, "--from")
	if err != nil {
		return ExitUsage, err
	}
	if server.Alive(src) {
		return ExitUsage, errors.New("old owner active: stop it first")
	}
	st, code, err := openOffline(*dd)
	if err != nil {
		return code, err
	}
	defer st.Close()
	out := map[string]any{}
	if *backup != "" {
		b, err := absPath(*backup, "--backup")
		if err != nil {
			return ExitUsage, err
		}
		if err := st.Backup(b); err != nil {
			return ExitFailed, errors.New("backup failed (destination must not exist)")
		}
		out["backup"] = b
	}
	rep, err := st.ImportLegacy(src, roots)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrLiveController):
			return ExitUsage, errors.New("live controller detected; stop it first")
		case errors.Is(err, store.ErrUnsafeSource), errors.Is(err, model.ErrInvalid):
			return ExitUsage, errors.New("unsafe or invalid legacy source")
		case errors.Is(err, store.ErrCorruptSource):
			return ExitFailed, errors.New("corrupt legacy source")
		}
		return ExitFailed, errors.New("import failed")
	}
	out["import"] = rep
	writeJSON(env.Stdout, map[string]any{"ok": true, "data": out})
	return ExitOK, nil
}

func cmdBackup(env Env, args []string) (int, error) {
	fs, dd := newFlags("backup")
	o := fs.String("output", "", "backup file (must not exist)")
	if err := parse(fs, args); err != nil {
		return ExitUsage, err
	}
	path, err := absPath(*o, "--output")
	if err != nil {
		return ExitUsage, err
	}
	st, code, err := openOffline(*dd)
	if err != nil {
		return code, err
	}
	defer st.Close()
	if err := st.Backup(path); err != nil {
		return ExitFailed, errors.New("backup failed (destination must not exist)")
	}
	writeJSON(env.Stdout, map[string]any{"ok": true, "data": map[string]any{"backup": path}})
	return ExitOK, nil
}

// cmdRestore restores a validated backup into a fresh directory only; it never
// overwrites newer records. Merging into an existing store is a separate step.
func cmdRestore(env Env, args []string) (int, error) {
	fs, _ := newFlags("restore")
	b := fs.String("backup", "", "backup file")
	to := fs.String("to", "", "fresh data directory (must not exist)")
	if err := parse(fs, args); err != nil {
		return ExitUsage, err
	}
	src, err := absPath(*b, "--backup")
	if err != nil {
		return ExitUsage, err
	}
	dst, err := absPath(*to, "--to")
	if err != nil {
		return ExitUsage, err
	}
	if _, err := os.Lstat(dst); !errors.Is(err, os.ErrNotExist) {
		return ExitUsage, errors.New("--to must not exist (restore never overwrites)")
	}
	if err := store.Restore(src, dst); err != nil {
		switch {
		case errors.Is(err, store.ErrConflict):
			return ExitUsage, errors.New("--to must not exist (restore never overwrites)")
		case errors.Is(err, store.ErrBadBackup):
			return ExitUsage, errors.New("invalid backup (missing, corrupt or unrecoverable issue bodies)")
		}
		return ExitFailed, errors.New("restore failed; partial destination removed")
	}
	writeJSON(env.Stdout, map[string]any{"ok": true, "data": map[string]any{
		"dataDir": dst, "merged": false, "note": "restored into a fresh directory; existing data untouched"}})
	return ExitOK, nil
}

func cmdExport(env Env, args []string) (int, error) {
	fs, dd := newFlags("export")
	format := fs.String("format", "json", "json or csv")
	o := fs.String("output", "", "output file (default stdout)")
	if err := parse(fs, args); err != nil {
		return ExitUsage, err
	}
	if *format != "json" && *format != "csv" {
		return ExitUsage, usageErr{"--format must be json or csv"}
	}
	st, code, err := openOffline(*dd)
	if err != nil {
		return code, err
	}
	defer st.Close()
	b, err := st.ExportMetrics(*format)
	if err != nil {
		return ExitFailed, errors.New("export failed")
	}
	if *o == "" {
		_, _ = env.Stdout.Write(b)
		return ExitOK, nil
	}
	path, err := absPath(*o, "--output")
	if err != nil {
		return ExitUsage, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return ExitFailed, errors.New("cannot create --output (must not exist)")
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		return ExitFailed, errors.New("write failed")
	}
	if err := f.Close(); err != nil {
		return ExitFailed, errors.New("write failed")
	}
	writeJSON(env.Stdout, map[string]any{"ok": true, "data": map[string]any{"output": path, "format": *format}})
	return ExitOK, nil
}

// cmdDoctor reports local health without creating or mutating anything.
func cmdDoctor(env Env, args []string) (int, error) {
	fs, dd := newFlags("doctor")
	if err := parse(fs, args); err != nil {
		return ExitUsage, err
	}
	dir, err := dataDir(*dd)
	if err != nil {
		return ExitUsage, err
	}
	rep := map[string]any{"version": server.Version, "dataDir": dir}
	okAll := true
	if _, err := os.Lstat(dir); errors.Is(err, os.ErrNotExist) {
		rep["dataDirExists"] = false
		writeJSON(env.Stdout, map[string]any{"ok": true, "data": rep})
		return ExitOK, nil
	}
	private := server.CheckPrivateDir(dir) == nil
	rep["dataDirPrivate"] = private
	okAll = okAll && private
	alive := private && server.Alive(dir)
	rep["daemonActive"] = alive
	if private && !alive {
		if _, err := os.Lstat(filepath.Join(dir, "meerkat.db")); err == nil {
			if st, err := store.Open(dir); err == nil {
				if lf, err := st.LeaseFacts(); err == nil {
					rep["lease"] = map[string]any{"present": lf.Present, "stale": lf.Stale}
				}
				if im, err := st.Imports(); err == nil {
					rep["imports"] = len(im)
				}
				st.Close()
			} else {
				rep["storeOpen"] = false
				okAll = false
			}
		}
	}
	writeJSON(env.Stdout, map[string]any{"ok": okAll, "data": rep})
	if !okAll {
		return ExitUsage, nil
	}
	return ExitOK, nil
}
