//go:build windows

package administration

import (
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
	"syscall"
	"unsafe"
)

func platformName() string { return "windows" }

// FilesystemRoots publishes the drive letters that are actually present plus the
// user profile, which is where a single-machine install usually keeps media. A
// UNC path is typed rather than browsed: there is no portable way to enumerate
// network neighbours, and the picker accepts `\\server\share` directly.
func FilesystemRoots() []FilesystemRoot {
	out := []FilesystemRoot{}
	for letter := 'A'; letter <= 'Z'; letter++ {
		path := string(letter) + `:\`
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			root := FilesystemRoot{Path: path, Name: string(letter) + ":", Kind: "drive", Description: "Drive " + string(letter) + ":"}
			wide, _ := windows.UTF16PtrFromString(path)
			kinds := map[uint32]string{2: "removable", 3: "fixed", 4: "network", 5: "optical", 6: "ram"}
			driveType := windows.GetDriveType(wide)
			root.DriveType = kinds[driveType]
			root.Classification = windowsDriveClassification(driveType)
			local, _ := windows.UTF16PtrFromString(string(letter) + ":")
			target := make([]uint16, 32768)
			size := uint32(len(target))
			result, _, _ := windows.NewLazySystemDLL("mpr.dll").NewProc("WNetGetConnectionW").Call(uintptr(unsafe.Pointer(local)), uintptr(unsafe.Pointer(&target[0])), uintptr(unsafe.Pointer(&size)))
			if result == 0 {
				root.NetworkTarget = syscall.UTF16ToString(target)
				root.Description = "Network drive, " + root.NetworkTarget
				// A mapped share is a network drive whatever type Windows reports.
				root.Classification = "network"
			}
			out = append(out, root)
		}
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		out = append(out, FilesystemRoot{Path: home, Name: "Home", Kind: "home", Description: "This account's profile folder", Classification: windowsClassification(home)})
	}
	out = append(out, FilesystemRoot{Path: `\\`, Name: "Network share", Kind: "unc", Description: `Type a UNC path such as \\server\share`, Classification: "network"})
	return out
}

// windowsDriveClassification names only the drive types Windows reports
// outright. An unknown or unready drive stays unclassified.
func windowsDriveClassification(driveType uint32) string {
	switch driveType {
	case 2, 3, 5, 6: // removable, fixed, optical, ram disk
		return "local"
	case 4: // network
		return "network"
	}
	return ""
}

// windowsClassification follows the drive the path actually lives on.
func windowsClassification(path string) string {
	volume := filepath.VolumeName(path)
	if volume == "" {
		return ""
	}
	wide, err := windows.UTF16PtrFromString(volume + `\`)
	if err != nil {
		return ""
	}
	return windowsDriveClassification(windows.GetDriveType(wide))
}

func platformHidden(path string) bool {
	p, e := windows.UTF16PtrFromString(path)
	if e != nil {
		return false
	}
	a, e := windows.GetFileAttributes(p)
	return e == nil && a&windows.FILE_ATTRIBUTE_HIDDEN != 0
}
func filesystemWritable(path string) bool {
	p, e := windows.UTF16PtrFromString(path)
	if e != nil {
		return false
	}
	h, e := windows.CreateFile(p, windows.FILE_WRITE_DATA, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if e != nil {
		return false
	}
	windows.CloseHandle(h)
	return true
}
