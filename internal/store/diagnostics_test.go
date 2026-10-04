package store

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/hunknownz/Meerkat/internal/model"
)

func legacyDiagnosticDB(t *testing.T, version int) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "private")
	if e := os.Mkdir(dir, 0o700); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(dir, dbName)
	db, e := sql.Open("sqlite", dsn(path, dsnBackupFile))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(migrationV1); e != nil {
		t.Fatal(e)
	}
	if version != 1 {
		if _, e = db.Exec("PRAGMA user_version = 100"); e != nil {
			t.Fatal(e)
		}
	}
	db.Close()
	if e = os.Chmod(path, 0o600); e != nil {
		t.Fatal(e)
	}
	return dir
}

func TestDiagnosticsDoesNotMigrateOrCreate(t *testing.T) {
	for _, v := range []int{1, 100} {
		dir := legacyDiagnosticDB(t, v)
		path := filepath.Join(dir, dbName)
		before, _ := os.ReadFile(path)
		entries, _ := os.ReadDir(dir)
		r, e := InspectReadOnly(context.Background(), dir)
		if v == 1 {
			if e != nil || r.Schema != 1 || r.Sessions != nil || r.Requests != nil || r.Controls != nil || !r.Integrity || !r.ForeignKeys {
				t.Fatalf("%+v %v", r, e)
			}
		} else if e == nil || r.Schema != 100 {
			t.Fatalf("future: %+v %v", r, e)
		}
		after, _ := os.ReadFile(path)
		afterEntries, _ := os.ReadDir(dir)
		if !reflect.DeepEqual(before, after) || len(entries) != len(afterEntries) {
			t.Fatal("doctor changed database or created files")
		}
	}
	missing := filepath.Join(t.TempDir(), "absent")
	if _, e := InspectReadOnly(context.Background(), missing); e == nil {
		t.Fatal("missing directory passed")
	}
	if _, e := os.Stat(missing); !os.IsNotExist(e) {
		t.Fatal("created missing directory")
	}
}

func TestDiagnosticsLiveWALAndUnknownCounts(t *testing.T) {
	s, dir := openTemp(t)
	if e := s.Update(func(st *model.State) error { seed(st); st.Runs[0].State = model.RunUnknown; return nil }); e != nil {
		t.Fatal(e)
	}
	r, e := InspectReadOnly(context.Background(), dir)
	if e != nil || r.Schema != schemaVersion || r.Runs[model.RunUnknown] != 1 || r.Sessions == nil || r.Requests == nil || r.Controls == nil {
		t.Fatalf("%+v %v", r, e)
	}
	st, e := s.Read()
	if e != nil || st.Runs[0].State != model.RunUnknown {
		t.Fatal("diagnostic changed state")
	}
}

func TestDiagnosticsUnsafeCorruptAndForeignKeys(t *testing.T) {
	t.Run("permissions", func(t *testing.T) {
		dir := legacyDiagnosticDB(t, 1)
		path := filepath.Join(dir, dbName)
		os.Chmod(path, 0o644)
		if _, e := InspectReadOnly(context.Background(), dir); e == nil {
			t.Fatal("unsafe file passed")
		}
		fi, _ := os.Stat(path)
		if fi.Mode().Perm() != 0o644 {
			t.Fatal("permissions repaired during diagnosis")
		}
	})
	t.Run("corrupt", func(t *testing.T) {
		dir := legacyDiagnosticDB(t, 1)
		os.WriteFile(filepath.Join(dir, dbName), []byte("not a database SECRET_SENTINEL"), 0o600)
		if _, e := InspectReadOnly(context.Background(), dir); e == nil {
			t.Fatal("corrupt file passed")
		}
	})
	t.Run("foreign_keys", func(t *testing.T) {
		dir := legacyDiagnosticDB(t, 1)
		db, _ := sql.Open("sqlite", dsn(filepath.Join(dir, dbName), dsnBackupFile))
		if _, e := db.Exec("PRAGMA foreign_keys=OFF; INSERT INTO profiles VALUES ('bad','missing','{}')"); e != nil {
			t.Fatal(e)
		}
		db.Close()
		if _, e := InspectReadOnly(context.Background(), dir); e == nil {
			t.Fatal("broken foreign key passed")
		}
	})
	t.Run("symlink", func(t *testing.T) {
		dir := legacyDiagnosticDB(t, 1)
		link := filepath.Join(t.TempDir(), "link")
		os.Symlink(dir, link)
		if _, e := InspectReadOnly(context.Background(), link); e == nil {
			t.Fatal("symlink passed")
		}
	})
}
