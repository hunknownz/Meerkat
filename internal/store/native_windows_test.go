//go:build windows

package store

import (
	"context"
	"github.com/hunknownz/Meerkat/internal/platform"
	"golang.org/x/sys/windows"
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
	for _, name := range []string{"", dbName, dbName + "-wal", dbName + "-shm"} {
		path := filepath.Join(dir, name)
		fi, e := os.Lstat(path)
		if e != nil {
			t.Log(name, e)
			continue
		}
		sd, e := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
		if e == nil {
			t.Log(name, sd.String(), "owned", platform.Owned(path, fi), "private", platform.Private(path, fi, 0o600))
		}
	}
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
