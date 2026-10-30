//go:build darwin

package administration

import (
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
)

func platformName() string { return "darwin" }

// darwinClassification reads the volume's own file system type.
func darwinClassification(path string) string {
	var fs unix.Statfs_t
	if unix.Statfs(path, &fs) != nil {
		return ""
	}
	return classificationOf(unix.ByteSliceToString(fs.Fstypename[:]))
}

// FilesystemRoots publishes the boot volume, the mounted-volume directory and
// the signed-in account's home folder.
func FilesystemRoots() []FilesystemRoot {
	out := []FilesystemRoot{{Path: "/", Name: "Macintosh HD", Kind: "root", Description: "The boot volume", Classification: darwinClassification("/")}}
	if info, err := os.Stat("/Volumes"); err == nil && info.IsDir() {
		out = append(out, FilesystemRoot{Path: "/Volumes", Name: "Volumes", Kind: "volumes", Description: "External and network volumes", Classification: darwinClassification("/Volumes")})
		if directory, e := os.Open("/Volumes"); e == nil {
			entries, _ := directory.Readdir(65)
			directory.Close()
			for _, entry := range entries {
				if entry.IsDir() {
					path := filepath.Join("/Volumes", entry.Name())
					description, classification := "Mounted volume", ""
					var fs unix.Statfs_t
					if unix.Statfs(path, &fs) == nil {
						filesystem := unix.ByteSliceToString(fs.Fstypename[:])
						description = "Mounted " + filesystem
						classification = classificationOf(filesystem)
					}
					out = append(out, FilesystemRoot{Path: path, Name: entry.Name(), Kind: "volume", Description: description, Classification: classification})
				}
			}
		}
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		out = append(out, FilesystemRoot{Path: home, Name: "Home", Kind: "home", Description: "This account's home folder", Classification: darwinClassification(home)})
	}
	return out
}

func platformHidden(string) bool          { return false }
func filesystemWritable(path string) bool { return unix.Access(path, unix.W_OK|unix.X_OK) == nil }
