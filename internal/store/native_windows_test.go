//go:build windows

package store

import (
	"context"
	"github.com/hunknownz/Meerkat/internal/platform"
	"os"
	"path/filepath"
	"testing"
)

func TestNativeSQLiteBackupAndDiagnostics(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state space # percent%")
	s, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	d, e := InspectReadOnly(context.Background(), dir)
	if e != nil || !d.Integrity || !d.ForeignKeys {
		t.Fatalf("diagnostics %+v %v", d, e)
	}
	dest := filepath.Join(t.TempDir(), "snapshot.db")
	if e = s.Backup(dest); e != nil {
		t.Fatal(e)
	}
	fi, e := os.Lstat(dest)
	if e != nil || !platform.Private(dest, fi, 0o600) {
		t.Fatal("backup ACL unsafe", e)
	}
	restored := filepath.Join(t.TempDir(), "restored")
	if e = Restore(dest, restored); e != nil {
		t.Fatal(e)
	}
	r, e := Open(restored)
	if e != nil {
		t.Fatal(e)
	}
	r.Close()
}
