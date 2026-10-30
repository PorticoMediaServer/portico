//go:build windows

package identity

import (
	"errors"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

func resetWindowsSID() (*windows.SID, error) {
	user, e := windows.GetCurrentProcessToken().GetTokenUser()
	if e != nil {
		return nil, e
	}
	return user.User.Sid, nil
}

// Windows ignores Unix mode 0600. The security directory instead carries a
// protected inheritable DACL for this process identity, LocalSystem and Administrators only.
func prepareResetDirectory(path string) error {
	if info, e := os.Lstat(path); e == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || !resetPermissionsPrivate(path, info) {
			return errors.New("reset file directory must be private")
		}
		return nil
	} else if !errors.Is(e, os.ErrNotExist) {
		return e
	}
	sid, e := resetWindowsSID()
	if e != nil {
		return e
	}
	descriptor, e := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;" + sid.String() + ")(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)")
	if e != nil {
		return e
	}
	name, e := windows.UTF16PtrFromString(path)
	if e != nil {
		return e
	}
	attrs := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: descriptor}
	if e = windows.CreateDirectory(name, &attrs); e != nil {
		return e
	}
	info, e := os.Lstat(path)
	if e != nil {
		return e
	}
	if !resetPermissionsPrivate(path, info) {
		return errors.New("reset file directory must be private")
	}
	return nil
}
func resetPermissionsPrivate(path string, _ os.FileInfo) bool {
	sid, e := resetWindowsSID()
	if e != nil {
		return false
	}
	descriptor, e := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.OWNER_SECURITY_INFORMATION)
	if e != nil {
		return false
	}
	owner, _, e := descriptor.Owner()
	if e != nil || owner == nil {
		return false
	}
	allowed := func(s *windows.SID) bool {
		return s != nil && (s.Equals(sid) || s.IsWellKnown(windows.WinLocalSystemSid) || s.IsWellKnown(windows.WinBuiltinAdministratorsSid))
	}
	if !allowed(owner) {
		return false
	}
	acl, _, e := descriptor.DACL()
	if e != nil || acl == nil {
		return false
	}
	for i := uint32(0); i < uint32(acl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if windows.GetAce(acl, i, &ace) != nil || ace == nil || ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			return false
		}
		if !allowed((*windows.SID)(unsafe.Pointer(&ace.SidStart))) {
			return false
		}
	}
	return true
}
