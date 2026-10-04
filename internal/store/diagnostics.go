package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"syscall"
	"time"

	"github.com/hunknownz/Meerkat/internal/model"
	budget "github.com/hunknownz/Meerkat/internal/requestbudget"
)

// Diagnostics contains aggregate facts only. Nil groups were not available in
// this schema; they are never replaced with an empty, apparently healthy group.
type Diagnostics struct {
	Schema          int            `json:"schema"`
	SupportedSchema int            `json:"supportedSchema"`
	Integrity       bool           `json:"integrity"`
	ForeignKeys     bool           `json:"foreignKeys"`
	LeasePresent    bool           `json:"leasePresent"`
	LeaseHeartbeat  *string        `json:"leaseHeartbeat"`
	Imports         int            `json:"imports"`
	Runs            map[string]int `json:"runs"`
	Sessions        map[string]int `json:"sessions"`
	BudgetRuns      map[string]int `json:"budgetRuns"`
	Requests        map[string]int `json:"requests"`
	Controls        map[string]int `json:"controls"`
}

type diagnosticGroup struct {
	table  string
	states []string
	dest   *map[string]int
}

// InspectReadOnly never calls Open, migrates, repairs or changes permissions.
// SQLite may use reader locks/shared memory for a live WAL; no rows or schema
// are written. All queries share one snapshot and have a caller deadline.
func InspectReadOnly(ctx context.Context, dir string) (Diagnostics, error) {
	r := Diagnostics{SupportedSchema: schemaVersion}
	for _, name := range []string{"", dbName, dbName + "-wal", dbName + "-shm"} {
		fi, err := os.Lstat(filepath.Join(dir, name))
		if errors.Is(err, os.ErrNotExist) && name != "" && name != dbName {
			continue
		}
		if err != nil {
			return r, ErrUnsafeDir
		}
		st, ok := fi.Sys().(*syscall.Stat_t)
		if !ok || int(st.Uid) != os.Getuid() || fi.Mode()&os.ModeSymlink != 0 ||
			name == "" && (!fi.IsDir() || fi.Mode().Perm() != 0o700) ||
			name != "" && (!fi.Mode().IsRegular() || fi.Mode().Perm() != 0o600) {
			return r, ErrUnsafeDir
		}
	}
	db, err := sql.Open("sqlite", dsn(filepath.Join(dir, dbName), dsnReadOnlyFile))
	if err != nil {
		return r, ErrBadBackup
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return r, ErrBadBackup
	}
	defer tx.Rollback()
	if tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&r.Schema) != nil {
		return r, ErrBadBackup
	}
	if r.Schema < 1 || r.Schema > schemaVersion {
		return r, ErrBadBackup
	}
	var integrity string
	if tx.QueryRowContext(ctx, "PRAGMA integrity_check(1)").Scan(&integrity) != nil || integrity != "ok" {
		return r, ErrBadBackup
	}
	r.Integrity = true
	rows, err := tx.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return r, ErrBadBackup
	}
	bad := rows.Next()
	err = rows.Err()
	rows.Close()
	if bad || err != nil {
		return r, ErrBadBackup
	}
	r.ForeignKeys = true
	// Validate schema presence even when a table happens to contain no records.
	tables := slices.Clone(schemaTables)
	if r.Schema >= 3 {
		tables = append(tables, operationTables...)
	}
	if r.Schema >= 4 {
		tables = append(tables, sessionTables...)
	}
	if r.Schema >= 5 {
		tables = append(tables, budgetTables...)
	}
	if r.Schema >= 6 {
		tables = append(tables, "worktree_checkpoints")
	}
	if r.Schema >= 7 {
		tables = append(tables, "budget_decisions")
	}
	if r.Schema >= 8 {
		tables = append(tables, "run_completions", "recovery_decisions")
	}
	if r.Schema >= 9 {
		tables = append(tables, controlTable)
	}
	for _, table := range tables {
		var n int
		if tx.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_schema WHERE type='table' AND name=?", table).Scan(&n) != nil || n != 1 {
			return r, ErrBadBackup
		}
	}
	var at string
	err = tx.QueryRowContext(ctx, "SELECT heartbeat_at FROM controller_lease WHERE id=1").Scan(&at)
	if err == nil {
		if _, e := time.Parse(time.RFC3339Nano, at); e != nil {
			return r, ErrBadBackup
		}
		r.LeasePresent, r.LeaseHeartbeat = true, &at
	} else if !errors.Is(err, sql.ErrNoRows) {
		return r, ErrBadBackup
	}
	if tx.QueryRowContext(ctx, "SELECT count(*) FROM imports").Scan(&r.Imports) != nil {
		return r, ErrBadBackup
	}
	groups := []diagnosticGroup{
		{"runs", model.RunStates, &r.Runs},
	}
	if r.Schema >= 4 {
		groups = append(groups, diagnosticGroup{"execution_sessions", []string{model.SessionIdle, model.SessionRunning, model.SessionUnknown}, &r.Sessions})
	}
	if r.Schema >= 5 {
		groups = append(groups, diagnosticGroup{"budget_runs", []string{"open", "closed", "unknown"}, &r.BudgetRuns},
			diagnosticGroup{"budget_requests", []string{budget.Reserved, budget.Sent, budget.Settled, budget.Unknown, budget.Canceled}, &r.Requests})
	}
	if r.Schema >= 9 {
		groups = append(groups, diagnosticGroup{controlTable, []string{model.ControlAccepted, model.ControlSending, model.ControlAcknowledged, model.ControlRejected, model.ControlUnknown}, &r.Controls})
	}
	for _, g := range groups {
		rows, err := tx.QueryContext(ctx, "SELECT state,count(*) FROM "+g.table+" GROUP BY state")
		if err != nil {
			return r, ErrBadBackup
		}
		counts := map[string]int{}
		for rows.Next() {
			var state string
			var n int
			if rows.Scan(&state, &n) != nil || !slices.Contains(g.states, state) {
				rows.Close()
				return r, ErrBadBackup
			}
			counts[state] = n
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return r, ErrBadBackup
		}
		*g.dest = counts
	}
	return r, nil
}
