// Package archive7z is the owned resource-bounded CB7 worker. It runs only as a
// hidden mode of the one server executable (fixed argument vector, stdin for the
// archive, stdout for a typed reply or page bytes) so no second binary, PATH
// search or external decoder is installed. The parent owns the scratch root,
// deadlines, process-group kill and physical wait; this package owns parsing.
package archive7z

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/bodgit/sevenzip"
)

const (
	MaxSource      = 2 << 30
	maxHeaderBytes = 16 << 20
	MaxMember      = 32 << 20
	MaxMembers     = 4096
	MaxExpanded    = 2 << 30
	maxNameLength  = 1024
	// MaxComicInfo is the only non-page member a comic archive may expose:
	// root ComicInfo.xml, bounded well below a page so metadata can never become
	// a decompression or memory route.
	MaxComicInfo = 256 << 10
	// Mode is the hidden first argument that dispatches the server executable
	// into this worker before normal startup.
	Mode = "__reading-archive7z"
)

type Member struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
}

type reply struct {
	Error   string   `json:"error,omitempty"`
	Members []Member `json:"members,omitempty"`
	Size    int64    `json:"size,omitempty"`
}

// Main runs one worker invocation and returns the process exit code. All owned
// resources unwind before it returns.
func Main(args []string, stdin io.Reader, stdout io.Writer) int {
	code := 0
	if err := run(args, stdin, stdout); err != nil {
		_ = json.NewEncoder(stdout).Encode(reply{Error: err.Error()})
		code = 2
	}
	return code
}

func run(args []string, stdin io.Reader, stdout io.Writer) error {
	if err := limitProcess(); err != nil {
		return err
	}
	if len(args) == 0 {
		return errors.New("mode required")
	}
	switch args[0] {
	case "list":
		return list(stdin, stdout)
	case "page":
		if len(args) != 2 {
			return errors.New("member required")
		}
		return page(stdin, stdout, args[1])
	default:
		return errors.New("unknown mode")
	}
}

// spool copies stdin into the parent-provided private scratch root; 7z needs
// random access and no archive path is ever passed on the command line.
func spool(stdin io.Reader) (string, int64, error) {
	directory, err := os.MkdirTemp("", "portico-cb7-")
	if err != nil {
		return "", 0, err
	}
	file, err := os.Create(filepath.Join(directory, "archive.7z"))
	if err != nil {
		_ = os.RemoveAll(directory)
		return "", 0, err
	}
	written, err := io.Copy(file, io.LimitReader(stdin, MaxSource+1))
	closeErr := file.Close()
	if err != nil || closeErr != nil || written > MaxSource {
		_ = os.RemoveAll(directory)
		return "", 0, errors.New("archive too large")
	}
	return directory, written, nil
}

func admit(path string, size int64) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	var head [32]byte
	if _, err = file.ReadAt(head[:], 0); err != nil {
		return errors.New("unreadable archive")
	}
	if string(head[:6]) != "7z\xbc\xaf'\x1c" {
		return errors.New("not a 7z archive")
	}
	nextOffset := binary.LittleEndian.Uint64(head[12:20])
	nextSize := binary.LittleEndian.Uint64(head[20:28])
	if size < 32 || nextOffset > uint64(size)-32 || nextSize == 0 || nextSize > maxHeaderBytes || nextSize > uint64(size)-32-nextOffset {
		return errors.New("unbounded header")
	}
	var marker [1]byte
	if _, err = file.ReadAt(marker[:], int64(32)+int64(nextOffset)); err != nil || marker[0] != 0x01 {
		return errors.New("unsupported header")
	}
	return nil
}

func open(stdin io.Reader) (*sevenzip.ReadCloser, string, error) {
	directory, size, err := spool(stdin)
	if err != nil {
		return nil, "", err
	}
	archive := filepath.Join(directory, "archive.7z")
	if err = admit(archive, size); err != nil {
		_ = os.RemoveAll(directory)
		return nil, "", err
	}
	reader, err := sevenzip.OpenReader(archive)
	if err != nil {
		_ = os.RemoveAll(directory)
		return nil, "", errors.New("archive refused")
	}
	if len(reader.File) == 0 || len(reader.File) > MaxMembers {
		_ = reader.Close()
		_ = os.RemoveAll(directory)
		return nil, "", errors.New("member count out of bounds")
	}
	return reader, directory, nil
}

func list(stdin io.Reader, stdout io.Writer) error {
	reader, directory, err := open(stdin)
	if err != nil {
		return err
	}
	defer os.RemoveAll(directory)
	defer reader.Close()
	names := []Member{}
	seen := map[string]bool{}
	var expanded uint64
	for _, file := range reader.File {
		if !plainMember(file.Name, file.FileInfo().IsDir(), file.UncompressedSize) {
			continue
		}
		if seen[file.Name] {
			// Two entries with one exact name are not two pages: the first would
			// be served twice. Refuse the archive instead of guessing identity.
			return errors.New("duplicate member name")
		}
		seen[file.Name] = true
		if file.UncompressedSize > uint64(MaxMember) || uint64(MaxExpanded)-expanded < file.UncompressedSize {
			return errors.New("member bytes out of bounds")
		}
		expanded += file.UncompressedSize
		names = append(names, Member{Name: file.Name, Size: int64(file.UncompressedSize)})
	}
	return json.NewEncoder(stdout).Encode(reply{Members: names})
}

func page(stdin io.Reader, stdout io.Writer, name string) error {
	if !plainName(name) {
		return errors.New("invalid member")
	}
	reader, directory, err := open(stdin)
	if err != nil {
		return err
	}
	defer os.RemoveAll(directory)
	defer reader.Close()
	// The whole accepted set is validated before the selected entry opens: a
	// duplicate later in the archive must refuse it even when the requested name
	// was seen first.
	selected := -1
	seen := map[string]bool{}
	var expanded uint64
	for index, file := range reader.File {
		if !plainMember(file.Name, file.FileInfo().IsDir(), file.UncompressedSize) {
			continue
		}
		if seen[file.Name] || file.UncompressedSize > uint64(MaxMember) || uint64(MaxExpanded)-expanded < file.UncompressedSize {
			return errors.New("member set refused")
		}
		seen[file.Name] = true
		expanded += file.UncompressedSize
		if file.Name == name {
			selected = index
		}
	}
	if selected < 0 {
		return errors.New("member not found")
	}
	{
		file := reader.File[selected]
		body, err := file.Open()
		if err != nil {
			return errors.New("member refused")
		}
		defer body.Close()
		out, err := io.ReadAll(io.LimitReader(body, MaxMember+1))
		if err != nil || int64(len(out)) != int64(file.UncompressedSize) || len(out) > MaxMember {
			return errors.New("member out of bounds")
		}
		if _, err = stdout.Write(out); err != nil {
			return errors.New("output failed")
		}
		return nil
	}
}

func plainName(name string) bool {
	if name == "" || len(name) > maxNameLength || strings.ContainsAny(name, "\\\x00%?#:") || strings.HasPrefix(name, "/") {
		return false
	}
	for _, part := range strings.Split(name, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	for _, r := range name {
		if r < 32 || r == 127 {
			return false
		}
	}
	return true
}

func plainMember(name string, dir bool, size uint64) bool {
	if dir || size == 0 || size > MaxMember || !plainName(name) {
		return false
	}
	// One exact root metadata member, case-sensitive, under its own cap.
	if name == "ComicInfo.xml" {
		return size <= MaxComicInfo
	}
	lower := strings.ToLower(name)
	for _, ext := range []string{".jpg", ".jpeg", ".png", ".webp"} {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	return false
}
