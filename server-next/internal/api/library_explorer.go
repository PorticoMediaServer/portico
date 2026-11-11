package api

import (
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// LibraryOrder is every library's id, in the order the owner wants everyone to see them.
type LibraryOrder struct {
	IDs []string `json:"ids"`
}

// FolderList is one folder of this machine as the folder explorer shows it: the folders in
// it, by name, 200 at a time. It lists folders only, never files.
type FolderList struct {
	// Path is the folder listed; "" is the top, whose folders are the places to start from.
	Path string `json:"path"`
	// Parent is the folder above, absent at the top.
	Parent  *string        `json:"parent,omitempty"`
	Folders []FolderChoice `json:"folders"`
	// NextCursor asks for the next 200.
	NextCursor string `json:"nextCursor,omitempty"`
	// Limited says the folder holds more than 20,000 entries and only the folders among the
	// first 20,000 are listed: type the path of one that is not shown.
	Limited bool `json:"limited,omitempty"`
}

// FolderChoice is one folder that can be opened or chosen.
type FolderChoice struct {
	Name string `json:"name"`
	Path string `json:"path"`
	// Writable is given when asked for: whether Portico may write into the folder.
	Writable *bool `json:"writable,omitempty"`
}

const (
	explorerPage  = 200
	explorerLimit = 20000
)

// systemFolders are folders an operating system or a NAS keeps for itself, hidden with the
// folders whose names start with a dot unless they are asked for.
var systemFolders = map[string]bool{"$recycle.bin": true, "system volume information": true, "lost+found": true, "@eadir": true,
	"#recycle": true, "@recycle": true, ".trashes": true}

func (s *Server) explorerRoutes() {
	Handle(s, "PUT /v1/admin/libraries/order", Owner, s.orderLibraries)
	Handle(s, "GET /v1/admin/filesystem", Owner, s.exploreFolders)
}

func (s *Server) orderLibraries(r *http.Request, req LibraryOrder) (None, error) {
	err := s.db.OrderLibraries(r.Context(), req.IDs, s.now())
	if errors.Is(err, db.ErrInvalid) {
		return None{}, ErrField("ids", "Give every library's id once, in the order wanted.")
	}
	return None{}, err
}

// exploreFolders lists the folders in one folder. Whoever is asking is the owner, who can
// already point a library at any folder the server can read; this only saves typing the path.
func (s *Server) exploreFolders(r *http.Request, _ None) (FolderList, error) {
	q := r.URL.Query()
	out := FolderList{Path: q.Get("path"), Folders: []FolderChoice{}}
	offset, _ := strconv.Atoi(q.Get("cursor"))
	if offset < 0 || q.Get("cursor") != "" && strconv.Itoa(offset) != q.Get("cursor") {
		return out, ErrField("cursor", "Start the list again.")
	}
	var names []string
	if out.Path == "" {
		names = startingFolders()
	} else {
		if !filepath.IsAbs(out.Path) || filepath.Clean(out.Path) != out.Path {
			return out, ErrField("path", "Give the folder's full path.")
		}
		for _, refused := range refusedFolders {
			if out.Path == refused || strings.HasPrefix(out.Path, refused+"/") {
				return out, ErrField("path", "This folder cannot be used for a library.")
			}
		}
		listed, limited, err := foldersIn(out.Path, q.Get("hidden") == "true")
		switch {
		case errors.Is(err, fs.ErrNotExist):
			return out, ErrField("path", "This folder was not found.")
		case errors.Is(err, fs.ErrPermission):
			return out, ErrField("path", "Portico has no permission to read this folder.")
		case err != nil:
			return out, ErrField("path", "This folder cannot be reached.")
		}
		names, out.Limited = listed, limited
		if parent := filepath.Dir(out.Path); parent != out.Path {
			out.Parent = &parent
		} else {
			top := ""
			out.Parent = &top
		}
	}
	for at := offset; at < len(names) && at < offset+explorerPage; at++ {
		choice := FolderChoice{Name: names[at], Path: filepath.Join(out.Path, names[at])}
		if out.Path == "" {
			choice.Path = names[at]
		}
		if q.Get("writable") == "true" {
			writable := canWrite(choice.Path)
			choice.Writable = &writable
		}
		out.Folders = append(out.Folders, choice)
	}
	if offset+explorerPage < len(names) {
		out.NextCursor = strconv.Itoa(offset + explorerPage)
	}
	return out, nil
}

// startingFolders are where the explorer starts: the top of the disk, or the drives.
func startingFolders() []string {
	if runtime.GOOS != "windows" {
		return []string{"/"}
	}
	var drives []string
	for letter := 'A'; letter <= 'Z'; letter++ {
		drive := string(letter) + `:\`
		if _, err := os.Stat(drive); err == nil {
			drives = append(drives, drive)
		}
	}
	return drives
}

// foldersIn names the folders in a folder, by name. A link to a folder counts as one. It
// reads at most explorerLimit entries and says when there were more.
func foldersIn(path string, hidden bool) (names []string, limited bool, err error) {
	dir, err := os.Open(path)
	if err != nil {
		return nil, false, err
	}
	defer dir.Close()
	if info, err := dir.Stat(); err != nil || !info.IsDir() {
		return nil, false, fs.ErrNotExist
	}
	entries, err := dir.ReadDir(explorerLimit + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, false, err
	}
	if limited = len(entries) > explorerLimit; limited {
		entries = entries[:explorerLimit]
	}
	for _, entry := range entries {
		name := entry.Name()
		if !hidden && (strings.HasPrefix(name, ".") || systemFolders[strings.ToLower(name)]) {
			continue
		}
		isDir := entry.IsDir()
		if entry.Type()&fs.ModeSymlink != 0 {
			info, err := os.Stat(filepath.Join(path, name))
			isDir = err == nil && info.IsDir()
		}
		if isDir {
			names = append(names, name)
		}
	}
	sort.Slice(names, func(i, j int) bool { return strings.ToLower(names[i]) < strings.ToLower(names[j]) })
	return names, limited, nil
}
