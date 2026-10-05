//go:build windows

package platform

import (
	"errors"
	"golang.org/x/sys/windows"
	"os"
	"unsafe"
)

func UserSID() (*windows.SID, error) {
	u, e := windows.GetCurrentProcessToken().GetTokenUser()
	if e != nil {
		return nil, e
	}
	return u.User.Sid, nil
}
func descriptor() (*windows.SECURITY_DESCRIPTOR, error) {
	sid, e := UserSID()
	if e != nil {
		return nil, e
	}
	return windows.SecurityDescriptorFromString("O:" + sid.String() + "D:P(A;OICI;FA;;;" + sid.String() + ")(A;OICI;FA;;;SY)")
}
func safePath(path string) bool {
	p, e := windows.UTF16PtrFromString(path)
	if e != nil {
		return false
	}
	a, e := windows.GetFileAttributes(p)
	return e == nil && a&windows.FILE_ATTRIBUTE_REPARSE_POINT == 0
}
func ownedSD(sd *windows.SECURITY_DESCRIPTOR) bool {
	sid, e := UserSID()
	if e != nil {
		return false
	}
	o, _, e := sd.Owner()
	return e == nil && o != nil && o.Equals(sid)
}

// PrivateSD rejects null DACLs and all grants to identities other than this
// user and SYSTEM, including inherited Users/Everyone/Administrators grants.
func PrivateSD(sd *windows.SECURITY_DESCRIPTOR) bool {
	if !ownedSD(sd) {
		return false
	}
	sid, e := UserSID()
	if e != nil {
		return false
	}
	acl, _, e := sd.DACL()
	if e != nil || acl == nil || acl.AceCount == 0 {
		return false
	}
	for i := uint32(0); i < uint32(acl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if windows.GetAce(acl, i, &ace) != nil {
			return false
		}
		if ace.Header.AceType == windows.ACCESS_DENIED_ACE_TYPE {
			continue
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			return false
		}
		grantee := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if ace.Mask != 0 && !grantee.Equals(sid) && grantee.String() != "S-1-5-18" {
			return false
		}
	}
	return true
}
func Owned(path string, fi os.FileInfo) bool {
	if fi == nil || fi.Mode()&os.ModeSymlink != 0 || !safePath(path) {
		return false
	}
	sd, e := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	return e == nil && ownedSD(sd)
}
func Private(path string, fi os.FileInfo, perm os.FileMode) bool {
	if !Owned(path, fi) {
		return false
	}
	sd, e := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	return e == nil && PrivateSD(sd)
}
func attrs() (*windows.SecurityAttributes, error) {
	sd, e := descriptor()
	if e != nil {
		return nil, e
	}
	return &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}, nil
}
func Mkdir(path string, perm os.FileMode) error {
	p, e := windows.UTF16PtrFromString(path)
	if e != nil {
		return e
	}
	sa, e := attrs()
	if e != nil {
		return e
	}
	return windows.CreateDirectory(p, sa)
}
func Chmod(path string, perm os.FileMode) error {
	fi, e := os.Lstat(path)
	if e != nil {
		return e
	}
	if !Owned(path, fi) {
		return errors.New("unsafe owner or reparse point")
	}
	sd, e := descriptor()
	if e != nil {
		return e
	}
	dacl, _, e := sd.DACL()
	if e != nil {
		return e
	}
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil)
}

// OpenFile opens the reparse point itself, then refuses it; it never follows
// a final symlink or junction. New private files get a protected user DACL.
func OpenFile(path string, flags int, perm os.FileMode) (*os.File, error) {
	p, e := windows.UTF16PtrFromString(path)
	if e != nil {
		return nil, e
	}
	access := uint32(windows.GENERIC_READ)
	if flags&os.O_WRONLY != 0 {
		access = windows.GENERIC_WRITE
	}
	if flags&os.O_RDWR != 0 {
		access = windows.GENERIC_READ | windows.GENERIC_WRITE
	}
	disposition := uint32(windows.OPEN_EXISTING)
	if flags&os.O_CREATE != 0 {
		disposition = windows.OPEN_ALWAYS
		if flags&os.O_EXCL != 0 {
			disposition = windows.CREATE_NEW
		}
	} else if flags&os.O_TRUNC != 0 {
		disposition = windows.TRUNCATE_EXISTING
	}
	sa, e := attrs()
	if e != nil {
		return nil, e
	}
	h, e := windows.CreateFile(p, access, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, sa, disposition, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if e != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: e}
	}
	var info windows.ByHandleFileInformation
	if windows.GetFileInformationByHandle(h, &info) != nil || info.FileAttributes&(windows.FILE_ATTRIBUTE_REPARSE_POINT|windows.FILE_ATTRIBUTE_DIRECTORY) != 0 {
		windows.CloseHandle(h)
		return nil, errors.New("unsafe file")
	}
	f := os.NewFile(uintptr(h), path)
	if flags&os.O_TRUNC != 0 && disposition == windows.OPEN_ALWAYS {
		if e = f.Truncate(0); e != nil {
			f.Close()
			return nil, e
		}
	}
	return f, nil
}

// Windows exposes no Unix directory fsync contract. File handles are flushed
// before atomic renames; this verifies the directory without claiming fsync.
func SyncDir(path string) error {
	fi, e := os.Lstat(path)
	if e != nil {
		return e
	}
	if !fi.IsDir() || !Private(path, fi, 0o700) {
		return errors.New("unsafe directory")
	}
	return nil
}
func Alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	h, e := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if e != nil {
		return !errors.Is(e, windows.ERROR_INVALID_PARAMETER)
	}
	defer windows.CloseHandle(h)
	var code uint32
	return windows.GetExitCodeProcess(h, &code) != nil || code == 259
}

// A persisted PID cannot prove descendant absence after the owning Job handle
// is lost. Recovery requires further evidence, never a stale-PID termination.
func GroupAbsent(pid, group int) bool { return false }
