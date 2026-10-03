package store

import (
	"bytes"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/hunknownz/Meerkat/internal/model"
)

const uD2 = "00000000-0000-4000-8000-000000000017"

func TestRestoreSupportedSchemaHistory(t *testing.T) {
	for _, version := range []int{schemaV1, schemaV2, schemaV3} {
		t.Run(map[int]string{schemaV1: "v1", schemaV2: "v2", schemaV3: "v3"}[version], func(t *testing.T) {
			backup := filepath.Join(t.TempDir(), "history.db")
			v1Fixture(t, backup, dsnBackupFile)
			if version >= schemaV2 {
				db, err := sql.Open("sqlite", dsn(backup, dsnBackupFile))
				if err != nil {
					t.Fatal(err)
				}
				if _, err := db.Exec(migrationV2); err != nil {
					t.Fatal(err)
				}
				if version >= schemaV3 {
					if _, err := db.Exec(migrationV3); err != nil {
						t.Fatal(err)
					}
				}
				db.Close()
			}
			before := dumpRows(t, backup)
			if err := ValidateBackup(backup); err != nil {
				t.Fatal(err)
			}
			dst := filepath.Join(t.TempDir(), "restored")
			if err := Restore(backup, dst); err != nil {
				t.Fatal(err)
			}
			if got := dumpRows(t, filepath.Join(dst, dbName)); !reflect.DeepEqual(before, got) {
				t.Fatal("restore or migration changed history")
			}
			s, err := Open(dst)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			assertV2History(t, s, filepath.Join(dst, dbName))
			if v, _ := schemaInfo(t, backup); v != version {
				t.Fatal("source backup modified")
			}
		})
	}
}

// prepIssue stores a prepared body + receipt and returns the receipt and body.
func prepIssue(t *testing.T, s *Store, id string, body []byte) IssueReceipt {
	t.Helper()
	path, hash, err := s.WriteIssueBody(id, body)
	if err != nil {
		t.Fatal(err)
	}
	r, _, err := s.SaveIssueReceipt(IssueReceipt{DeliveryID: id, TaskID: uT, BodyHash: hash, BodyPath: path, State: IssuePending, PreparedAt: "x"})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func mustNotExist(t *testing.T, p string) {
	t.Helper()
	if _, err := os.Lstat(p); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("partial output left behind")
	}
}

func editBackup(t *testing.T, path, stmt string, args ...any) {
	t.Helper()
	db, err := sql.Open("sqlite", dsn(path, dsnBackupFile))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(stmt, args...); err != nil {
		t.Fatal(err)
	}
}

func TestBackupRestoreIssueBodiesSelfContained(t *testing.T) {
	s, dir := openTemp(t)
	body := []byte("prepared update payload")
	r1 := prepIssue(t, s, uD, body)
	r2 := prepIssue(t, s, uD2, []byte("second payload"))
	// advance one receipt to a non-initial state
	r2.State, r2.Attempts = IssueUnknown, 1
	r2, err := s.CompareAndSetIssueReceipt(r2)
	if err != nil {
		t.Fatal(err)
	}
	bk := filepath.Join(t.TempDir(), "b.db")
	if err := s.Backup(bk); err != nil {
		t.Fatal(err)
	}
	// original bodies become unavailable
	if err := os.RemoveAll(filepath.Join(dir, "issue-bodies")); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "restored")
	if err := Restore(bk, dst); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Lstat(dst); err != nil || fi.Mode().Perm() != 0o700 {
		t.Fatal("restored dir not private")
	}
	rs, err := Open(dst)
	if err != nil {
		t.Fatal(err)
	}
	defer rs.Close()
	for _, want := range []IssueReceipt{r1, r2} {
		got, err := rs.LoadIssueReceipt(want.DeliveryID)
		if err != nil {
			t.Fatal(err)
		}
		if got.DeliveryID != want.DeliveryID || got.BodyHash != want.BodyHash || got.State != want.State || got.Rev != want.Rev ||
			got.Attempts != want.Attempts || got.Marker != want.Marker || got.TaskID != want.TaskID {
			t.Fatal("receipt identity/state changed")
		}
		if got.BodyPath != rs.bodyPath(want.DeliveryID) || got.BodyPath == want.BodyPath {
			t.Fatal("body path not rebased")
		}
		fi, err := os.Lstat(got.BodyPath)
		if err != nil || fi.Mode().Perm() != 0o600 {
			t.Fatal("restored body not private")
		}
		if _, err := rs.ReadIssueBody(got); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := rs.LoadIssueReceipt(uD)
	if b, _ := rs.ReadIssueBody(got); !bytes.Equal(b, body) {
		t.Fatal("restored body mismatch")
	}
	var n int
	rs.db.QueryRow("SELECT count(*) FROM sqlite_master WHERE name = ?", backupBodiesTable).Scan(&n)
	if n != 0 {
		t.Fatal("bundle table left in restored store")
	}
	// the original store record is untouched
	if o, _ := s.LoadIssueReceipt(uD); o.BodyPath != r1.BodyPath || o.Rev != r1.Rev {
		t.Fatal("source mutated")
	}
	if err := rs.Backup(filepath.Join(t.TempDir(), "again.db")); err != nil {
		t.Fatal("restored store cannot be backed up again", err)
	}
}

func TestBackupRefusesBadBodies(t *testing.T) {
	cases := map[string]func(t *testing.T, path string){
		"missing":  func(t *testing.T, p string) { os.Remove(p) },
		"tampered": func(t *testing.T, p string) { os.WriteFile(p, []byte("tampered"), 0o600) },
		"symlink": func(t *testing.T, p string) {
			other := filepath.Join(t.TempDir(), "other")
			b, _ := os.ReadFile(p)
			os.WriteFile(other, b, 0o600)
			os.Remove(p)
			if err := os.Symlink(other, p); err != nil {
				t.Fatal(err)
			}
		},
		"oversized": func(t *testing.T, p string) { os.WriteFile(p, make([]byte, MaxLegacyBodyBytes+1), 0o600) },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			s, _ := openTemp(t)
			r := prepIssue(t, s, uD, []byte("payload"))
			mutate(t, r.BodyPath)
			bk := filepath.Join(t.TempDir(), "b.db")
			if err := s.Backup(bk); !errors.Is(err, ErrBadBackup) {
				t.Fatalf("backup accepted %s body: %v", name, err)
			}
			mustNotExist(t, bk)
		})
	}
}

func TestRestoreRefusesBadBundles(t *testing.T) {
	cases := map[string]func(t *testing.T, bk string){
		"corrupt body": func(t *testing.T, bk string) {
			editBackup(t, bk, "UPDATE "+backupBodiesTable+" SET body = ? WHERE id = ?", []byte("evil"), uD)
		},
		"tampered hash": func(t *testing.T, bk string) {
			editBackup(t, bk, "UPDATE "+backupBodiesTable+" SET body_hash = ? WHERE id = ?", hashBody([]byte("evil")), uD)
		},
		"missing body": func(t *testing.T, bk string) {
			editBackup(t, bk, "DELETE FROM "+backupBodiesTable+" WHERE id = ?", uD)
		},
		"extra body": func(t *testing.T, bk string) {
			editBackup(t, bk, "INSERT INTO "+backupBodiesTable+" VALUES (?, ?, ?)", uD2, hashBody([]byte("x")), []byte("x"))
		},
		"text body": func(t *testing.T, bk string) {
			editBackup(t, bk, "UPDATE "+backupBodiesTable+" SET body = CAST(body AS TEXT) WHERE id = ?", uD)
		},
		"old format with receipts": func(t *testing.T, bk string) {
			editBackup(t, bk, "DROP TABLE "+backupBodiesTable)
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			s, _ := openTemp(t)
			prepIssue(t, s, uD, []byte("payload"))
			bk := filepath.Join(t.TempDir(), "b.db")
			if err := s.Backup(bk); err != nil {
				t.Fatal(err)
			}
			mutate(t, bk)
			dst := filepath.Join(t.TempDir(), "restored")
			if err := Restore(bk, dst); !errors.Is(err, ErrBadBackup) {
				t.Fatalf("restore accepted %s: %v", name, err)
			}
			mustNotExist(t, dst)
		})
	}
}

func TestRestoreOldBackupWithoutReceipts(t *testing.T) {
	s, _ := openTemp(t)
	if err := s.SetSettings(defaultsWith(4)); err != nil {
		t.Fatal(err)
	}
	bk := filepath.Join(t.TempDir(), "b.db")
	if err := s.Backup(bk); err != nil {
		t.Fatal(err)
	}
	editBackup(t, bk, "DROP TABLE "+backupBodiesTable) // the pre-bundle format
	dst := filepath.Join(t.TempDir(), "restored")
	if err := Restore(bk, dst); err != nil {
		t.Fatal(err)
	}
	rs, err := Open(dst)
	if err != nil {
		t.Fatal(err)
	}
	defer rs.Close()
	if set, _ := rs.GetSettings(); set.MaxConcurrency != 4 {
		t.Fatal("settings not restored")
	}
}

func TestRestoreRefusesExistingDestinationAndCleansUp(t *testing.T) {
	s, _ := openTemp(t)
	prepIssue(t, s, uD, []byte("payload"))
	bk := filepath.Join(t.TempDir(), "b.db")
	if err := s.Backup(bk); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(bk)

	existing := t.TempDir()
	keep := filepath.Join(existing, "keep")
	os.WriteFile(keep, []byte("k"), 0o600)
	if err := Restore(bk, existing); !errors.Is(err, ErrConflict) {
		t.Fatal("restore into existing directory", err)
	}
	if b, err := os.ReadFile(keep); err != nil || string(b) != "k" {
		t.Fatal("existing destination touched")
	}
	if _, err := os.Lstat(filepath.Join(existing, dbName)); err == nil {
		t.Fatal("existing destination written")
	}

	restoreFault = func() error { return errors.New("injected") }
	defer func() { restoreFault = nil }()
	dst := filepath.Join(t.TempDir(), "restored")
	if err := Restore(bk, dst); err == nil {
		t.Fatal("fault not reported")
	}
	mustNotExist(t, dst)
	if after, _ := os.ReadFile(bk); !bytes.Equal(before, after) {
		t.Fatal("backup file mutated by restore")
	}
	restoreFault = nil
	if err := Restore(bk, dst); err != nil {
		t.Fatal("retry after cleanup failed", err)
	}
}

func defaultsWith(c int) model.Settings {
	set := model.DefaultSettings()
	set.MaxConcurrency = c
	return set
}
