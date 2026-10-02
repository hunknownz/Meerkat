package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

// backupBodiesTable exists only inside backup files. It carries the private Issue update bodies referenced by
// issue_receipts so that a backup stays a single self-contained .db file. Restore materializes the bodies into
// the new data directory and drops the table.
const backupBodiesTable = "backup_issue_bodies"

const backupBodiesDDL = "CREATE TABLE " + backupBodiesTable + " (id TEXT PRIMARY KEY, body_hash TEXT NOT NULL, body BLOB NOT NULL)"

// restoreFault is a test-only hook to inject a failure after the restore destination exists.
var restoreFault func() error

func badBackup(reason string) error { return fmt.Errorf("%w: %s", ErrBadBackup, reason) }

// Backup writes a consistent, self-contained copy of the database (including prepared Issue bodies) to
// destination, which must not exist. The source store is only read. A partial destination is removed on failure.
func (s *Store) Backup(destination string) (err error) {
	dest, err := filepath.Abs(destination)
	if err != nil {
		return fmt.Errorf("store: invalid backup destination")
	}
	f, err := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("%w: backup destination exists", ErrConflict)
		}
		return fmt.Errorf("store: create backup failed")
	}
	f.Close()
	defer func() { // we exclusively created dest above, so it is ours to remove
		if err != nil {
			os.Remove(dest + "-journal")
			os.Remove(dest)
		}
	}()
	if _, err := s.db.Exec("VACUUM INTO ?", dest); err != nil {
		return fmt.Errorf("store: backup failed")
	}
	if err := os.Chmod(dest, 0o600); err != nil {
		return fmt.Errorf("store: backup permissions failed")
	}
	if err := s.bundleBodies(dest); err != nil {
		return err
	}
	return ValidateBackup(dest)
}

// bundleBodies copies every body referenced by the snapshot's receipts into the snapshot itself. Receipts are read
// from the snapshot (not the live DB) so bodies and receipts are consistent; each body must be the store's own
// private, non-symlinked, bounded file whose hash matches the receipt.
func (s *Store) bundleBodies(dest string) error {
	db, err := sql.Open("sqlite", dsn(dest, dsnBackupFile))
	if err != nil {
		return fmt.Errorf("store: backup failed")
	}
	defer db.Close()
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("store: backup failed")
	}
	defer tx.Rollback()
	need, err := snapshotReceipts(tx)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(backupBodiesDDL); err != nil {
		return fmt.Errorf("store: backup failed")
	}
	if len(need) > 0 {
		fi, err := os.Lstat(s.bodyDir())
		if err != nil || !fi.IsDir() || !ownedNoSymlink(fi) {
			return badBackup("issue body directory is missing or unsafe")
		}
	}
	for _, r := range need {
		if r.BodyPath != s.bodyPath(r.DeliveryID) {
			return badBackup("issue body path is outside the data directory")
		}
		b, err := readSafe(r.BodyPath, MaxLegacyBodyBytes)
		if err != nil {
			return badBackup("issue body is unsafe")
		}
		if b == nil {
			return badBackup("issue body is missing")
		}
		if hashBody(b) != r.BodyHash {
			return badBackup("issue body does not match its receipt")
		}
		if _, err := tx.Exec("INSERT INTO "+backupBodiesTable+" (id, body_hash, body) VALUES (?, ?, ?)", r.DeliveryID, r.BodyHash, b); err != nil {
			return fmt.Errorf("store: backup failed")
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: backup failed")
	}
	return nil
}

// snapshotReceipts returns the receipts that reference a body file, keyed by delivery id.
func snapshotReceipts(q querier) (map[string]IssueReceipt, error) {
	rows, err := q.Query("SELECT id, payload FROM issue_receipts")
	if err != nil {
		return nil, badBackup("issue receipts unreadable")
	}
	defer rows.Close()
	need := map[string]IssueReceipt{}
	for rows.Next() {
		var id string
		var p []byte
		var r IssueReceipt
		if rows.Scan(&id, &p) != nil || json.Unmarshal(p, &r) != nil || r.DeliveryID != id || validReceipt(r) != nil {
			return nil, badBackup("issue receipt is corrupt")
		}
		if r.BodyPath != "" {
			need[id] = r
		}
	}
	if rows.Err() != nil {
		return nil, badBackup("issue receipts unreadable")
	}
	return need, nil
}

// verifyBackupBodies checks that the bundled bodies exactly cover the receipts that reference bodies, that each
// is bounded and matches its receipt hash, and calls fn (if non-nil) for every verified body. Backups without
// body-referencing receipts need no bundle (old format); old backups with such receipts are rejected.
func verifyBackupBodies(q querier, fn func(IssueReceipt, []byte) error) error {
	need, err := snapshotReceipts(q)
	if err != nil {
		return err
	}
	var n int
	if err := q.QueryRow("SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?", backupBodiesTable).Scan(&n); err != nil {
		return ErrBadBackup
	}
	if n == 0 {
		if len(need) > 0 {
			return badBackup("backup has issue receipts but no recoverable bodies")
		}
		return nil
	}
	var total, bad int
	if err := q.QueryRow("SELECT count(*), coalesce(sum(typeof(body) != 'blob' OR length(body) > ?), 0) FROM "+backupBodiesTable,
		MaxLegacyBodyBytes).Scan(&total, &bad); err != nil {
		return ErrBadBackup
	}
	if bad != 0 || total != len(need) {
		return badBackup("bundled issue bodies are incomplete or oversized")
	}
	rows, err := q.Query("SELECT id, body_hash, body FROM " + backupBodiesTable + " ORDER BY id")
	if err != nil {
		return ErrBadBackup
	}
	defer rows.Close()
	for rows.Next() {
		var id, h string
		var b []byte
		if rows.Scan(&id, &h, &b) != nil {
			return ErrBadBackup
		}
		r, ok := need[id]
		if !ok || h != r.BodyHash || len(b) > MaxLegacyBodyBytes || hashBody(b) != r.BodyHash {
			return badBackup("bundled issue body does not match its receipt")
		}
		delete(need, id)
		if fn != nil {
			if err := fn(r, b); err != nil {
				return err
			}
		}
	}
	if rows.Err() != nil || len(need) != 0 {
		return badBackup("bundled issue bodies are incomplete")
	}
	return nil
}

// ValidateBackup checks integrity, foreign keys, schema and bundled Issue bodies of a backup file.
func ValidateBackup(path string) error {
	fi, err := os.Lstat(path)
	if err != nil || !fi.Mode().IsRegular() {
		return ErrBadBackup
	}
	db, err := sql.Open("sqlite", dsn(path, dsnReadOnlyFile))
	if err != nil {
		return ErrBadBackup
	}
	defer db.Close()
	var res string
	if err := db.QueryRow("PRAGMA integrity_check").Scan(&res); err != nil || res != "ok" {
		return ErrBadBackup
	}
	if checkFKs(db) != nil {
		return ErrBadBackup
	}
	var v int
	if err := db.QueryRow("PRAGMA user_version").Scan(&v); err != nil || v != 1 {
		return ErrBadBackup
	}
	for _, t := range schemaTables {
		var n int
		if err := db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?", t).Scan(&n); err != nil || n != 1 {
			return ErrBadBackup
		}
	}
	return verifyBackupBodies(db, nil)
}

// Restore materializes a validated backup into dst, which must not exist. Issue bodies are written as private
// files in dst and receipt body paths are rebased onto dst; receipt ids, hashes, states and revisions are kept.
// The backup file is only read. If anything fails, the directory this call created is removed.
func Restore(backup, dst string) (err error) {
	src, err := filepath.Abs(backup)
	if err != nil {
		return ErrBadBackup
	}
	if dst, err = filepath.Abs(dst); err != nil {
		return fmt.Errorf("store: invalid restore destination")
	}
	if _, err := os.Lstat(dst); !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%w: restore destination exists", ErrConflict)
	}
	if err := ValidateBackup(src); err != nil {
		return err
	}
	if err := os.Mkdir(dst, 0o700); err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("%w: restore destination exists", ErrConflict)
		}
		return fmt.Errorf("store: create restore destination failed")
	}
	defer func() { // only reached after this call exclusively created dst
		if err != nil {
			os.RemoveAll(dst)
		}
	}()
	if err := os.Chmod(dst, 0o700); err != nil {
		return fmt.Errorf("store: restore permissions failed")
	}
	db := filepath.Join(dst, dbName)
	if err := copyNewFile(src, db); err != nil {
		return fmt.Errorf("store: restore copy failed")
	}
	if err := ValidateBackup(db); err != nil { // re-verify the copy we actually restore
		return err
	}
	s, err := Open(dst)
	if err != nil {
		return err
	}
	if err := s.materializeBodies(); err != nil {
		s.Close()
		return err
	}
	return s.Close()
}

// materializeBodies writes bundled bodies into this store's private body directory, rebases receipt paths and drops
// the bundle table in one short transaction, then verifies every body through ReadIssueBody.
func (s *Store) materializeBodies() error {
	var receipts []IssueReceipt
	err := func() error {
		tx, err := s.rdb.Begin()
		if err != nil {
			return fmt.Errorf("store: restore read failed")
		}
		defer tx.Rollback()
		return verifyBackupBodies(tx, func(r IssueReceipt, b []byte) error {
			if _, _, err := s.writeRestoredBody(r.DeliveryID, b); err != nil {
				return err
			}
			receipts = append(receipts, r)
			return nil
		})
	}()
	if err != nil {
		return err
	}
	if restoreFault != nil {
		if err := restoreFault(); err != nil {
			return err
		}
	}
	err = s.tx(func(tx *sql.Tx) error {
		for _, r := range receipts {
			cur, err := loadReceipt(tx, r.DeliveryID)
			if err != nil || cur.BodyHash != r.BodyHash || cur.Rev != r.Rev || cur.State != r.State {
				return badBackup("issue receipt changed during restore")
			}
			cur.BodyPath = s.bodyPath(cur.DeliveryID)
			if _, err := tx.Exec("UPDATE issue_receipts SET payload = ? WHERE id = ?", mustJSON(cur), cur.DeliveryID); err != nil {
				return fmt.Errorf("store: restore write failed")
			}
		}
		if _, err := tx.Exec("DROP TABLE IF EXISTS " + backupBodiesTable); err != nil {
			return fmt.Errorf("store: restore write failed")
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, r := range receipts {
		cur, err := s.LoadIssueReceipt(r.DeliveryID)
		if err != nil {
			return badBackup("restored issue receipt missing")
		}
		if _, err := s.ReadIssueBody(cur); err != nil {
			return badBackup("restored issue body unreadable")
		}
	}
	return nil
}

func (s *Store) writeRestoredBody(deliveryID string, body []byte) (string, string, error) {
	tmp, err := s.stageBody(deliveryID, body)
	if err != nil {
		return "", "", err
	}
	if err := s.publishBody(deliveryID, tmp); err != nil {
		return "", "", err
	}
	return s.bodyPath(deliveryID), hashBody(body), nil
}

// copyNewFile copies a regular, non-symlinked file to a new 0600 file (never overwriting).
func copyNewFile(src, dst string) error {
	in, err := os.OpenFile(src, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return err
	}
	defer in.Close()
	if fi, err := in.Stat(); err != nil || !fi.Mode().IsRegular() {
		return ErrBadBackup
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
