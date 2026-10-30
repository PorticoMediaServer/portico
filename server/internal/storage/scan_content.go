package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/mediaexec"
)

// descriptorDirectoryRoot is the one sandbox path an admitted disc folder is
// mounted at. It is a mount point, never a host path.
const descriptorDirectoryRoot = "/disc"

type scanInputKey struct{}
type scanInput struct {
	client               *Client
	source               string
	root                 InventoryRequest
	blob                 *InventoryBlobAdmission
	invalidBlobAdmission bool
}

// WithScanInput binds optional analysis reads to the same admitted inventory
// root. An optional blob admission retains the actual job/policy/target facts
// for a remote bounded reader; it supplies no authority by itself. Local helper
// custody is unchanged, and no playback version evidence is created.
func WithScanInput(ctx context.Context, client *Client, source string, root InventoryRequest, admission ...InventoryBlobAdmission) context.Context {
	input := scanInput{client: client, source: source, root: root, invalidBlobAdmission: len(admission) > 1}
	if len(admission) == 1 {
		captured := admission[0]
		input.blob = &captured
	}
	return context.WithValue(ctx, scanInputKey{}, input)
}
func inventoryReadRequest(ctx context.Context, path string) (InventoryRequest, *scanInput, error) {
	input, ok := ctx.Value(scanInputKey{}).(scanInput)
	if !ok || input.client == nil {
		return InventoryRequest{}, nil, nil
	}
	if input.invalidBlobAdmission {
		return InventoryRequest{}, nil, ErrRemoteConfig
	}
	relative, err := filepath.Rel(input.root.Root, path)
	if err != nil || !filepath.IsLocal(relative) || relative == "." {
		return InventoryRequest{}, nil, ErrInventoryChanged
	}
	r := input.root
	r.RelativePath = relative
	return r, &input, nil
}

// RunScanCommand runs a media tool through the already-supervised helper. Unix
// replaces the helper; Windows retains a locked input and owns a nested job.
func RunScanCommand(ctx context.Context, binary string, args []string, path string, out io.Writer) (bool, error) {
	return runScanCommand(ctx, binary, args, path, false, false, out)
}

// RunDiscScanCommand preserves only FFmpeg's closed bluray: input syntax while
// retaining the same admitted regular file, root guard, helper and supervisor.
// It adds no directory/remote/network authority or general URL substitution.
func RunDiscScanCommand(ctx context.Context, binary string, args []string, path string, bluray bool, out io.Writer) (bool, error) {
	return runScanCommand(ctx, binary, args, path, bluray, false, out)
}

// RunDiscDirectoryCommand reads an admitted disc folder through one inherited
// directory descriptor. The helper mounts only that descriptor read-only at
// descriptorDirectoryRoot inside the empty sandbox root; the tool never sees a
// host path and cannot walk out with "..".
func RunDiscDirectoryCommand(ctx context.Context, binary string, args []string, path string, out io.Writer) (bool, error) {
	return runScanCommand(ctx, binary, args, path, true, true, out)
}

// DirectoryDiscScanSupported reports whether this host can mount an admitted
// directory descriptor into the scan sandbox. Older bubblewrap releases and
// non-Linux hosts keep disc folders unsupported rather than exposing a path.
func DirectoryDiscScanSupported() bool {
	directoryDiscSupported.Do(func() {
		if runtime.GOOS != "linux" {
			return
		}
		bwrap, err := exec.LookPath("bwrap")
		if err != nil {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		out, _ := exec.CommandContext(ctx, bwrap, "--help").CombinedOutput()
		directoryDiscSupported.ok = bytes.Contains(out, []byte("--ro-bind-fd"))
	})
	return directoryDiscSupported.ok
}

var directoryDiscSupported struct {
	sync.Once
	ok bool
}

func runScanCommand(ctx context.Context, binary string, args []string, path string, bluray, directory bool, out io.Writer) (bool, error) {
	r, input, err := inventoryReadRequest(ctx, path)
	if err != nil {
		return true, err
	}
	if input == nil {
		return false, nil
	}
	if err = CheckScanRead(ctx, path); err != nil {
		return true, err
	}
	index := -1
	prefix := ""
	if bluray {
		prefix = "bluray:"
	}
	for i, arg := range args {
		if arg == path || arg == "bluray:"+path {
			argPrefix := ""
			if arg == "bluray:"+path {
				argPrefix = "bluray:"
			}
			// A directory reader takes either the structure tool's plain path
			// or FFmpeg's closed bluray: input; the mount path keeps exactly
			// that spelling. A file scan keeps its single declared protocol.
			if !directory && argPrefix != prefix || directory && arg != path && argPrefix != "bluray:" {
				return true, errors.New("invalid scan input protocol")
			}
			if index != -1 {
				return true, errors.New("ambiguous scan input")
			}
			index, prefix = i, argPrefix
		}
	}
	if index < 0 || len(args) > 128 {
		return true, errors.New("invalid scan command")
	}
	c := input.client
	if c.IsRemote(path) {
		return true, ErrRemoteRange
	} // Native objects require an authenticated acquisition bridge.
	if c.Guard != nil {
		if err = c.Guard(path); err != nil {
			return true, err
		}
	}
	// The helper opens the admitted file and hands it over as descriptor 3; the
	// tool (sandboxed, or with the baseline) never gets the path (mediaexec).
	fdArgs := append([]string{}, args...)
	job := mediaexec.Job{Executable: binary, Args: fdArgs, Background: true, MaxCPUSeconds: scanCPUSeconds}
	if directory {
		fdArgs[index] = prefix + descriptorDirectoryRoot
		job.InputFD, job.InputMount = 3, descriptorDirectoryRoot
	} else {
		fdArgs[index] = prefix + descriptorInput
	}
	argv, err := mediaexec.Argv(job)
	if err != nil {
		return true, err
	}
	operation := "inventory-command"
	if directory {
		operation = "inventory-directory-command"
	}
	req := request{Operation: operation, Path: path, Inventory: &r, Argv: argv}
	if c.MountedRoot != nil {
		req.MountedRoot = c.MountedRoot(path)
	}
	raw, err := json.Marshal(req)
	if err != nil {
		return true, err
	}
	cmd, err := scanHelperCommand(c.Binary, raw)
	if err != nil {
		return true, err
	}
	err = c.Supervisor.Run(ctx, "scan:"+input.source, cmd, func(reader io.Reader) error {
		n, e := io.Copy(out, io.LimitReader(reader, (4<<20)+1))
		if n > 4<<20 {
			return errors.New("scan output exceeds limit")
		}
		return e
	})
	return true, err
}

// openInventoryRoot compares the root actually retained by OpenRoot, not merely
// a stat done before an unrelated pathname open. Source replacement fails closed.
func openInventoryRoot(r InventoryRequest) (*os.Root, error) {
	if !filepath.IsAbs(r.Root) {
		return nil, ErrInventoryChanged
	}
	root, err := os.OpenRoot(r.Root)
	if err != nil {
		return nil, err
	}
	info, err := root.Stat(".")
	if err != nil || !info.IsDir() || r.RootIdentity != "" && !SameRoot(r.RootIdentity, RootIdentity(snapshot(r.Root, info))) {
		root.Close()
		return nil, ErrInventoryChanged
	}
	return root, nil
}

// openInventoryDirectory opens an admitted directory beneath the retained
// inventory root. The returned descriptor is the only authority the scan
// sandbox gets; no host pathname is passed to the tool.
func openInventoryDirectory(r InventoryRequest) (*os.File, error) {
	path, err := inventoryPath(r)
	if err != nil {
		return nil, err
	}
	relative, err := filepath.Rel(r.Root, path)
	if err != nil || !filepath.IsLocal(relative) || relative == "." {
		return nil, ErrInventoryChanged
	}
	root, err := openInventoryRoot(r)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	dir, err := root.Open(relative)
	if err != nil {
		return nil, err
	}
	info, err := dir.Stat()
	if err != nil || !info.IsDir() {
		dir.Close()
		return nil, ErrInventoryChanged
	}
	return dir, nil
}

func openInventoryContent(r InventoryRequest) (*os.File, error) {
	path, err := inventoryPath(r)
	if err != nil {
		return nil, err
	}
	relative, err := filepath.Rel(r.Root, path)
	if err != nil || !filepath.IsLocal(relative) || relative == "." {
		return nil, ErrInventoryChanged
	}
	root, err := openInventoryRoot(r)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	file, err := root.Open(relative)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		file.Close()
		return nil, ErrInventoryChanged
	}
	return file, nil
}
func inventoryBlobHelper(r request, out io.Writer) error {
	maximum := int64(8 << 20)
	if r.Operation == "inventory-subtitle-blob" && binarySubtitlePath(r.Path) {
		maximum = 512 << 20
	}
	if r.Inventory == nil || r.Limit <= 0 || r.Limit > maximum {
		return errors.New("invalid inventory blob")
	}
	file, err := openInventoryContent(*r.Inventory)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.Size() > r.Limit {
		return errors.New("invalid local metadata file")
	}
	n, err := io.Copy(out, io.LimitReader(file, r.Limit+1))
	if n > r.Limit {
		return errors.New("local metadata exceeds limit")
	}
	return err
}

// scanHelperCommand starts the helper for one scan command. The helper moves
// the media file to descriptor 3 before it execs the tool. A placeholder holds
// 3 from the helper's start, so the Go runtime's own poller (kqueue on macOS)
// is never given 3 and then overwritten by that move: every probe failed so.
func scanHelperCommand(binary string, request []byte) (*exec.Cmd, error) {
	if runtime.GOOS == "windows" {
		cmd := exec.Command(binary, "--portico-storage-helper")
		cmd.Stdin = bytes.NewReader(request)
		return cmd, nil
	}
	placeholder, err := descriptorPlaceholder()
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(binary, "--portico-storage-helper")
	cmd.Stdin = bytes.NewReader(request)
	cmd.ExtraFiles = []*os.File{placeholder}
	return cmd, nil
}

var placeholderOnce struct {
	sync.Once
	file *os.File
	err  error
}

// descriptorPlaceholder is one read-only /dev/null shared by every scan
// helper start; each child gets its own copy as descriptor 3.
func descriptorPlaceholder() (*os.File, error) {
	placeholderOnce.Do(func() { placeholderOnce.file, placeholderOnce.err = os.Open(os.DevNull) })
	return placeholderOnce.file, placeholderOnce.err
}
