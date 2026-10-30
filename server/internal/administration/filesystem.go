package administration

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

// FilesystemRoot is a place the picker can start from. Windows publishes drive
// letters, macOS publishes the boot volume and /Volumes, Linux publishes / and
// the mount table's real mount points.
type FilesystemRoot struct {
	Path          string `json:"path"`
	Name          string `json:"name"`
	Kind          string `json:"kind"`
	Description   string `json:"description"`
	NetworkTarget string `json:"networkTarget,omitempty"`
	DriveType     string `json:"driveType,omitempty"`
	// Classification is the server's own read of the file system behind the root:
	// a network drive or mounted network share is "network", every other root is
	// "local". Clients present it and never infer it from a path.
	Classification string `json:"classification,omitempty"`
}

// networkFilesystems and localFilesystems are the file system types the picker
// can name from the drive or mount table it already reads. Any other type,
// including a bare "fuse" or an empty name, stays unknown: the server does not
// assert local storage without evidence.
var networkFilesystems = map[string]bool{
	"nfs": true, "nfs4": true, "cifs": true, "smb": true, "smb3": true, "smbfs": true,
	"afpfs": true, "sshfs": true, "fuse.sshfs": true, "davfs": true, "davfs2": true,
	"webdav": true, "fuse.rclone": true, "rclone": true, "9p": true,
	"ceph": true, "glusterfs": true, "lustre": true, "gpfs": true,
}
var localFilesystems = map[string]bool{
	"ext2": true, "ext3": true, "ext4": true, "xfs": true, "btrfs": true, "zfs": true,
	"f2fs": true, "reiserfs": true, "jfs": true, "nilfs2": true, "bcachefs": true, "erofs": true,
	"vfat": true, "msdos": true, "fat": true, "fat32": true, "exfat": true,
	"ntfs": true, "ntfs3": true, "hfs": true, "hfsplus": true, "apfs": true, "ufs": true,
	"iso9660": true, "cd9660": true, "udf": true,
}

// classificationOf turns a known file system type into the picker's
// classification. An unknown or generic type returns no value.
func classificationOf(filesystem string) string {
	name := strings.ToLower(filesystem)
	if networkFilesystems[name] {
		return "network"
	}
	if localFilesystems[name] {
		return "local"
	}
	return ""
}

// FilesystemEntry is one directory (or, when files are requested, one file).
type FilesystemEntry struct {
	Name   string `json:"name"`
	Hidden bool   `json:"hidden"`
	Reason string `json:"reason,omitempty"`
	Path   string `json:"path"`
	Kind   string `json:"kind"`
	// Readable says the server could list it; an unreadable directory is still
	// shown so the owner can see why the picker stops there.
	Readable bool `json:"readable"`
	// Symlink is reported because following one changes what a scan will walk.
	Symlink bool  `json:"symlink"`
	Bytes   int64 `json:"bytes,omitempty"`
}

// FilesystemPage is one bounded page of a directory listing.
type FilesystemPage struct {
	// Path is empty when the caller asked for the roots.
	Path       string            `json:"path"`
	Parent     string            `json:"parent"`
	Separator  string            `json:"separator"`
	Platform   string            `json:"platform"`
	Roots      []FilesystemRoot  `json:"roots"`
	Entries    []FilesystemEntry `json:"entries"`
	NextCursor string            `json:"nextCursor"`
	// Writable reports whether the server can create files here, which is what a
	// recordings or backup folder actually needs.
	Writable        bool     `json:"writable"`
	WritableChecked bool     `json:"writableChecked"`
	RunsAs          string   `json:"runsAs,omitempty"`
	Reason          string   `json:"reason,omitempty"`
	Suggestions     []string `json:"suggestions"`
	// Truncated says the root list or directory scan was bounded.
	Truncated bool `json:"truncated"`
}

// pickerNameBefore is the folder picker's total order: case-insensitive, then
// exact.
func pickerNameBefore(a, b string) bool {
	if la, lb := strings.ToLower(a), strings.ToLower(b); la != lb {
		return la < lb
	}
	return a < b
}

// maxDirectoryNames bounds the read of one directory so a folder with a million
// entries cannot hold a request open.
const maxDirectoryNames = 20000

// hiddenName filters the names a folder picker should never offer: dotfiles on
// every platform plus the system directories that are never media roots.
func hiddenName(parent, name string) bool {
	return name == "" || strings.HasPrefix(name, ".") || platformHidden(filepath.Join(parent, name))
}

func blockedName(parent, name string) bool {
	switch strings.ToLower(name) {
	case "$recycle.bin", "system volume information", "$winreagent", "recovery", "config.msi":
		return true
	}
	return systemPath(filepath.Join(parent, name))
}

// BrowseOptions controls only the selected directory; it never inspects children.
type BrowseOptions struct {
	ShowHidden    bool
	Prefix        string
	CheckWritable bool
}

// filesystemReason preserves why the selected location could not be opened.
func filesystemReason(err error, link bool) string {
	if errors.Is(err, os.ErrPermission) {
		return "permission"
	}
	if errors.Is(err, syscall.ELOOP) {
		return "broken-link"
	}
	if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
		if link {
			return "broken-link"
		}
		return "missing"
	}
	if errors.Is(err, syscall.EIO) || errors.Is(err, syscall.ETIMEDOUT) {
		return "offline"
	}
	return "offline"
}

// systemPath refuses a browse into a tree that is not a media location and that
// would leak the shape of the host's private state.
func systemPath(path string) bool {
	clean := strings.ToLower(filepath.ToSlash(filepath.Clean(path)))
	for _, blocked := range []string{"/proc", "/sys", "/dev", "/run"} {
		if clean == blocked || strings.HasPrefix(clean, blocked+"/") {
			return true
		}
	}
	return false
}

// Browse lists one directory, or the platform roots when path is empty. Entries
// are directories unless includeFiles is set; files are offered for pickers that
// choose a single file (a playlist, a guide) rather than a folder.
func (s *Service) Browse(ctx context.Context, path, token string, limit int, includeFiles bool, options ...BrowseOptions) (FilesystemPage, error) {
	policy := s.pickerPolicy()
	opt := BrowseOptions{}
	if len(options) > 0 {
		opt = options[0]
	}
	if !safeText(opt.Prefix, 255) || strings.ContainsAny(opt.Prefix, `/\`) {
		return FilesystemPage{}, ErrInput
	}
	scope := fmt.Sprintf("filesystem:%x", sha256.Sum256([]byte(fmt.Sprintf("%s|%t|%t|%s", filepath.Clean(path), includeFiles, opt.ShowHidden, opt.Prefix))))
	out := FilesystemPage{Separator: string(os.PathSeparator), Platform: platformName(), Roots: []FilesystemRoot{}, Entries: []FilesystemEntry{}, Suggestions: []string{}}
	if account, e := user.Current(); e == nil {
		out.RunsAs = account.Username
	}
	if len(policy.Roots) == 0 {
		for _, root := range FilesystemRoots() {
			if policy.CheckBrowse(root.Path) == nil {
				out.Roots = append(out.Roots, root)
			}
		}
	} else {
		for _, root := range policy.SafeRoots() {
			out.Roots = append(out.Roots, FilesystemRoot{Path: root, Name: filepath.Base(root), Kind: "configured", Description: "Configured media root"})
		}
	}
	if len(out.Roots) > 64 {
		out.Roots, out.Truncated = out.Roots[:64], true
	}
	size, err := pageSize(limit)
	if err != nil {
		return out, err
	}
	c, err := decodeCursor(scope, token)
	if err != nil {
		return out, err
	}
	if path == "" {
		return out, nil
	}
	if !safeText(path, 4096) || !filepath.IsAbs(path) {
		return out, ErrInput
	}
	clean := filepath.Clean(path)
	if systemPath(clean) || policy.CheckBrowse(clean) != nil {
		return out, ErrDenied
	}
	out.Path = clean
	requested := clean
	clean, err = filepath.EvalSymlinks(clean)
	if err != nil {
		linkInfo, _ := os.Lstat(requested)
		out.Reason = filesystemReason(err, linkInfo != nil && linkInfo.Mode()&os.ModeSymlink != 0)
		if out.Reason == "permission" {
			return out, ErrDenied
		}
		// Suggestions examine only the requested parent, never its descendants.
		parent := filepath.Dir(requested)
		if policy.CheckBrowse(parent) == nil && !systemPath(parent) {
			if resolved, e := filepath.EvalSymlinks(parent); e == nil && policy.CheckBrowse(resolved) == nil && !systemPath(resolved) {
				if dir, e := os.Open(resolved); e == nil {
					names, _ := dir.Readdirnames(maxDirectoryNames)
					dir.Close()
					for _, name := range names {
						if strings.EqualFold(name, filepath.Base(requested)) && !blockedName(resolved, name) && (!hiddenName(resolved, name) || opt.ShowHidden) {
							candidate := filepath.Join(resolved, name)
							if info, e := os.Stat(candidate); e == nil && info.IsDir() && policy.CheckBrowse(candidate) == nil {
								out.Suggestions = append(out.Suggestions, candidate)
							}
							if len(out.Suggestions) == 8 {
								break
							}
						}
					}
				}
			}
		}
		return out, ErrNotFound
	}
	// The checks above were made on the name that was asked for. A link inside
	// a permitted folder can point outside it, so the folder that will actually
	// be listed is checked again.
	if systemPath(clean) || policy.CheckBrowse(clean) != nil {
		return out, ErrDenied
	}
	info, err := os.Stat(clean)
	if err != nil || !info.IsDir() {
		out.Reason = filesystemReason(err, false)
		if err == nil {
			out.Reason = "missing"
		}
		return out, ErrNotFound
	}
	out.Path = clean
	if parent := filepath.Dir(clean); parent != clean && policy.CheckBrowse(parent) == nil {
		out.Parent = parent
	}
	if opt.CheckWritable {
		out.WritableChecked = true
		out.Writable = filesystemWritable(clean)
	}
	// Browsing and permission checks never create a file or change a share's mtime.
	directory, err := os.Open(clean)
	if err != nil {
		out.Reason = filesystemReason(err, false)
		return out, ErrDenied
	}
	defer directory.Close()
	// Keep at most 20,000 matching names. Prefix mode streams through a bounded
	// million names / two seconds, so matching folders beyond the ordinary cap
	// can be reached without allocating or statting every nonmatching entry.
	kept := []string{}
	maximum := maxDirectoryNames + 1
	if opt.Prefix != "" {
		maximum = 1000000
	}
	deadline := time.Now().Add(2 * time.Second)
	for visited := 0; visited < maximum; {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		if time.Now().After(deadline) {
			out.Truncated = true
			break
		}
		names, e := directory.Readdirnames(min(512, maximum-visited))
		visited += len(names)
		for _, name := range names {
			match := strings.HasPrefix(name, opt.Prefix)
			if platformName() != "linux" {
				match = strings.HasPrefix(strings.ToLower(name), strings.ToLower(opt.Prefix))
			}
			if !match || blockedName(clean, name) || hiddenName(clean, name) && !opt.ShowHidden {
				continue
			}
			kept = append(kept, name)
			if len(kept) > maxDirectoryNames {
				out.Truncated = true
				break
			}
		}
		if len(kept) > maxDirectoryNames {
			kept = kept[:maxDirectoryNames]
			break
		}
		if errors.Is(e, io.EOF) {
			break
		}
		if e != nil {
			out.Reason = filesystemReason(e, false)
			return out, ErrDenied
		}
		if visited == maximum {
			out.Truncated = true
		}
	}
	// Case-insensitive order, with the exact name breaking ties, so "Films" and
	// "films" (distinct folders on Linux) are both reachable across a page
	// boundary: the cursor resumes strictly after the last name served.
	sort.Slice(kept, func(i, j int) bool { return pickerNameBefore(kept[i], kept[j]) })
	for _, name := range kept {
		if c.ID != "" && !pickerNameBefore(c.ID, name) {
			continue
		}
		full := filepath.Join(clean, name)
		if policy.CheckBrowse(full) != nil {
			continue
		}
		entry := FilesystemEntry{Name: name, Path: full, Kind: "directory", Readable: true, Hidden: hiddenName(clean, name)}
		link, err := os.Lstat(full)
		if err != nil {
			continue
		}
		entry.Symlink = link.Mode()&os.ModeSymlink != 0
		if entry.Symlink {
			if target, e := filepath.EvalSymlinks(full); e == nil && (systemPath(target) || policy.CheckBrowse(target) != nil) {
				continue
			}
		}
		resolved, err := os.Stat(full)
		if err != nil {
			// A broken link or an entry the server cannot stat is still shown, so
			// the owner can see it exists and why it cannot be chosen.
			entry.Readable = false
			entry.Reason = filesystemReason(err, entry.Symlink)
			entry.Kind = "unknown"
		} else if resolved.IsDir() {
			if handle, err := os.Open(full); err == nil {
				handle.Close()
			} else {
				entry.Readable = false
				entry.Reason = filesystemReason(err, false)
			}
		} else {
			if !includeFiles {
				continue
			}
			entry.Kind, entry.Bytes = "file", resolved.Size()
		}
		out.Entries = append(out.Entries, entry)
		if len(out.Entries) > size {
			out.Entries = out.Entries[:size]
			out.NextCursor = encodeCursor(scope, 0, out.Entries[size-1].Name)
			break
		}
	}
	return out, nil
}
