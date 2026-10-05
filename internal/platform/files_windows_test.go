//go:build windows

package platform

import (
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
	"testing"
)

func TestNativePrivateACLAndReparse(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "private")
	if e := Mkdir(dir, 0o700); e != nil {
		t.Fatal(e)
	}
	fi, _ := os.Lstat(dir)
	if !Private(dir, fi, 0o700) {
		t.Fatal("new directory not private")
	}
	path := filepath.Join(dir, "owned.json")
	f, e := OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if e != nil {
		t.Fatal(e)
	}
	f.WriteString("private")
	f.Sync()
	f.Close()
	fi, _ = os.Lstat(path)
	if !Private(path, fi, 0o600) {
		t.Fatal("new file not private")
	}
	sid, _ := UserSID()
	sd, e := windows.SecurityDescriptorFromString("O:" + sid.String() + "D:P(A;;FA;;;" + sid.String() + ")(A;;FR;;;WD)")
	if e != nil {
		t.Fatal(e)
	}
	acl, _, _ := sd.DACL()
	if e = windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); e != nil {
		t.Fatal(e)
	}
	if Private(path, fi, 0o600) {
		t.Fatal("Everyone grant accepted")
	}
	if e = Chmod(path, 0o600); e != nil {
		t.Fatal(e)
	}
	link := filepath.Join(dir, "link")
	if e = os.Symlink(path, link); e == nil {
		if f, e = OpenFile(link, os.O_RDONLY, 0); e == nil {
			f.Close()
			t.Fatal("followed reparse point")
		}
	}
	if GroupAbsent(os.Getpid(), os.Getpid()) {
		t.Fatal("stale identity accepted")
	}
}
