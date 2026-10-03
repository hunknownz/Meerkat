package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/hunknownz/Meerkat/internal/model"
)

const maxSessionBytes = 64 << 20
const sessionBundleTable = "backup_session_files"

func fileDigest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

func (s *Store) readSessionBytes(ss model.Session) ([]byte, error) {
	for _, dir := range []string{filepath.Join(s.dir, "sessions"), filepath.Join(s.dir, "sessions", ss.ID)} {
		fi, err := os.Lstat(dir)
		if err != nil || !fi.IsDir() || fi.Mode().Perm() != 0o700 || !ownedNoSymlink(fi) {
			return nil, badBackup("session directory unsafe")
		}
	}
	b, err := readSafe(filepath.Join(s.dir, filepath.FromSlash(ss.FileRef)), maxSessionBytes)
	if err != nil || len(b) == 0 {
		return nil, badBackup("session file missing or unsafe")
	}
	return b, nil
}

// Bundle a quiescent DB snapshot with its private files. An idle file must
// match its confirmed digest; unknown history is preserved without certifying it.
func (s *Store) bundleSessions(dest string) error {
	db, err := sql.Open("sqlite", dsn(dest, dsnBackupFile))
	if err != nil {
		return ErrBadBackup
	}
	defer db.Close()
	tx, err := db.Begin()
	if err != nil {
		return ErrBadBackup
	}
	defer tx.Rollback()
	all, err := sessions(tx, "")
	if err != nil {
		return err
	}
	if _, err := tx.Exec("CREATE TABLE " + sessionBundleTable + " (id TEXT PRIMARY KEY,digest TEXT NOT NULL,body BLOB NOT NULL)"); err != nil {
		return ErrBadBackup
	}
	for _, ss := range all {
		if ss.State == model.SessionRunning {
			return fmt.Errorf("%w: active sessions cannot be backed up", ErrConflict)
		}
		b, err := s.readSessionBytes(ss)
		if err != nil {
			return err
		}
		h := fileDigest(b)
		if ss.State == model.SessionIdle && h != ss.FileDigest {
			return badBackup("session file does not match confirmed history")
		}
		if _, err := tx.Exec("INSERT INTO "+sessionBundleTable+" (id,digest,body) VALUES(?,?,?)", ss.ID, h, b); err != nil {
			return ErrBadBackup
		}
		again, err := s.readSessionBytes(ss)
		if err != nil || fileDigest(again) != h {
			return badBackup("session changed during backup")
		}
	}
	return tx.Commit()
}

func verifySessionBundle(q querier, fn func(model.Session, []byte) error) error {
	all, err := sessions(q, "")
	if err != nil {
		return badBackup("session records corrupt")
	}
	var exists int
	if q.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?", sessionBundleTable).Scan(&exists) != nil {
		return ErrBadBackup
	}
	if exists == 0 {
		if len(all) > 0 {
			return badBackup("session histories missing")
		}
		return nil
	}
	var total, bad int
	if q.QueryRow("SELECT count(*),coalesce(sum(typeof(body)!='blob' OR length(body)<1 OR length(body)>?),0) FROM "+sessionBundleTable, maxSessionBytes).Scan(&total, &bad) != nil || total != len(all) || bad != 0 {
		return badBackup("session histories incomplete or oversized")
	}
	for _, ss := range all {
		if ss.State == model.SessionRunning {
			return badBackup("active sessions are not a consistent backup")
		}
		var b []byte
		var h string
		if q.QueryRow("SELECT digest,body FROM "+sessionBundleTable+" WHERE id=?", ss.ID).Scan(&h, &b) != nil || !digestRE.MatchString(h) || fileDigest(b) != h || ss.State == model.SessionIdle && h != ss.FileDigest {
			return badBackup("session history hash mismatch")
		}
		if fn != nil {
			if err := fn(ss, b); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Store) materializeSessions() error {
	err := func() error {
		tx, err := s.rdb.Begin()
		if err != nil {
			return ErrBadBackup
		}
		defer tx.Rollback()
		return verifySessionBundle(tx, func(ss model.Session, b []byte) error {
			root := filepath.Join(s.dir, "sessions")
			if err := checkDir(root); err != nil {
				return err
			}
			if err := os.Mkdir(filepath.Join(root, ss.ID), 0o700); err != nil {
				return ErrBadBackup
			}
			f, err := os.OpenFile(filepath.Join(s.dir, filepath.FromSlash(ss.FileRef)), os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o600)
			if err != nil {
				return ErrBadBackup
			}
			_, err = f.Write(b)
			if err == nil {
				err = f.Sync()
			}
			ce := f.Close()
			if err != nil || ce != nil {
				return ErrBadBackup
			}
			return nil
		})
	}()
	if err != nil {
		return err
	}
	return s.tx(func(tx *sql.Tx) error { _, err := tx.Exec("DROP TABLE IF EXISTS " + sessionBundleTable); return err })
}
