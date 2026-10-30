//go:build !windows && !darwin

package administration

import (
	"golang.org/x/sys/unix"
	"os"
	"runtime"
	"sort"
	"strings"
)

func platformName() string { return runtime.GOOS }

// mountTables are the places a Linux host publishes its mount points. The first
// one that reads wins; a container without either still gets "/".
var mountTables = []string{"/proc/self/mountinfo", "/proc/mounts"}

// pseudoFilesystems never hold media and would only clutter the picker.
var pseudoFilesystems = map[string]bool{
	"proc": true, "sysfs": true, "devtmpfs": true, "devpts": true, "tmpfs": true, "cgroup": true,
	"cgroup2": true, "pstore": true, "securityfs": true, "debugfs": true, "tracefs": true,
	"configfs": true, "fusectl": true, "mqueue": true, "hugetlbfs": true, "bpf": true,
	"autofs": true, "binfmt_misc": true, "efivarfs": true, "ramfs": true, "squashfs": true,
	"nsfs": true, "overlay": true,
}

// unescapeMount undoes the octal escaping the kernel applies to mount points
// that contain spaces or tabs.
func unescapeMount(v string) string {
	if !strings.Contains(v, `\`) {
		return v
	}
	out := strings.Builder{}
	for i := 0; i < len(v); i++ {
		if v[i] == '\\' && i+3 < len(v) {
			value := 0
			valid := true
			for _, c := range v[i+1 : i+4] {
				if c < '0' || c > '7' {
					valid = false
					break
				}
				value = value*8 + int(c-'0')
			}
			if valid {
				out.WriteByte(byte(value))
				i += 3
				continue
			}
		}
		out.WriteByte(v[i])
	}
	return out.String()
}

// mountPoints reads the real mount points from the kernel's own table.
func mountPoints() []struct{ Path, Type string } {
	out := []struct{ Path, Type string }{}
	for _, table := range mountTables {
		raw, err := os.ReadFile(table)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(raw), "\n") {
			fields := strings.Fields(line)
			if table == "/proc/self/mountinfo" {
				// ... 4:mountpoint ... "-" fstype source options
				separator := -1
				for i, field := range fields {
					if field == "-" {
						separator = i
						break
					}
				}
				if separator < 0 || len(fields) < 5 || separator+1 >= len(fields) {
					continue
				}
				out = append(out, struct{ Path, Type string }{unescapeMount(fields[4]), fields[separator+1]})
				continue
			}
			if len(fields) < 3 {
				continue
			}
			out = append(out, struct{ Path, Type string }{unescapeMount(fields[1]), fields[2]})
		}
		if len(out) > 0 {
			return out
		}
	}
	return out
}

// mountClassification follows the mount the path actually lives on.
func mountClassification(path string) string {
	longest, filesystem := "", ""
	for _, mount := range mountPoints() {
		if mount.Path == "" || !strings.HasPrefix(path, mount.Path) {
			continue
		}
		if mount.Path != "/" && len(path) > len(mount.Path) && path[len(mount.Path)] != '/' {
			continue
		}
		if len(mount.Path) > len(longest) {
			longest, filesystem = mount.Path, mount.Type
		}
	}
	return classificationOf(filesystem)
}

// FilesystemRoots publishes "/" plus every real mount point the kernel reports,
// so a NAS mounted at /mnt/media is one click away.
func FilesystemRoots() []FilesystemRoot {
	out := []FilesystemRoot{{Path: "/", Name: "/", Kind: "root", Description: "The root filesystem"}}
	seen := map[string]bool{"/": true}
	paths := []string{}
	kinds := map[string]string{}
	rootType := ""
	for _, mount := range mountPoints() {
		if mount.Path == "/" {
			rootType = mount.Type
		}
		if pseudoFilesystems[mount.Type] || seen[mount.Path] || mount.Path == "" || !strings.HasPrefix(mount.Path, "/") {
			continue
		}
		if strings.HasPrefix(mount.Path, "/proc") || strings.HasPrefix(mount.Path, "/sys") || strings.HasPrefix(mount.Path, "/dev") || strings.HasPrefix(mount.Path, "/run") {
			continue
		}
		seen[mount.Path] = true
		paths = append(paths, mount.Path)
		kinds[mount.Path] = mount.Type
	}
	sort.Strings(paths)
	out[0].Classification = classificationOf(rootType)
	for _, path := range paths {
		out = append(out, FilesystemRoot{Path: path, Name: path, Kind: "mount", Description: "Mounted " + kinds[path], Classification: classificationOf(kinds[path])})
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" && !seen[home] {
		out = append(out, FilesystemRoot{Path: home, Name: "Home", Kind: "home", Description: "This account's home folder", Classification: mountClassification(home)})
	}
	return out
}

func platformHidden(string) bool { return false }
func filesystemWritable(path string) bool {
	return unix.Faccessat(unix.AT_FDCWD, path, unix.W_OK|unix.X_OK, unix.AT_EACCESS) == nil
}
