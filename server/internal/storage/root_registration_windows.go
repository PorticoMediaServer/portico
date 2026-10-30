//go:build windows

package storage

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"golang.org/x/sys/windows"
	"github.com/PorticoMediaServer/portico/server/internal/supervise"
)

func duplicateRoot(file *os.File) (*os.File, error) {
	var handle windows.Handle
	err := windows.DuplicateHandle(windows.CurrentProcess(), windows.Handle(file.Fd()), windows.CurrentProcess(), &handle, 0, false, windows.DUPLICATE_SAME_ACCESS)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(handle), file.Name()), nil
}

// The helper retains its original handle until an acknowledgement. The parent
// duplicates it through Process.WithHandle, which pins the actual child process
// object even during cancellation; no PID lookup or pathname reopen occurs.
func (c *Client) RegisterRoot(setup context.Context, path, mountPath, mountIdentity string, owner context.Context) (*RootRegistration, error) {
	if c == nil || c.Supervisor == nil || c.Binary == "" || owner == nil || owner.Err() != nil || !filepath.IsAbs(path) {
		return nil, ErrRootLease
	}
	if mountPath != "" || mountIdentity != "" {
		return nil, ErrObservedSourceUnsupported
	}
	if err := setup.Err(); err != nil {
		return nil, err
	}
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(setup, timeout)
	defer cancel()
	stop := context.AfterFunc(owner, cancel)
	defer stop()
	body, _ := json.Marshal(request{Operation: "register-root", Path: path})
	body = append(body, '\n')
	input, writer := io.Pipe()
	cmd := exec.Command(c.Binary, "--portico-storage-helper")
	cmd.Stdin = io.MultiReader(bytes.NewReader(body), input)
	operationDone := func() {}
	stopScope := func() bool { return false }
	var err error
	if c.SourceOperations != nil {
		operationDone, err = c.SourceOperations.Begin()
		if err != nil {
			input.Close()
			writer.Close()
			return nil, err
		}
		stopScope = context.AfterFunc(c.SourceOperations.Context(), cancel)
	}
	received := make(chan *os.File)
	done := make(chan error, 1)
	supervise.Go("storage.root-registration", func() {
		defer input.Close()
		defer writer.Close()
		done <- c.Supervisor.RunOwnedCompletion(ctx, "playback:register:"+fmtReaderID(), cmd, func(out io.Reader) error {
			// Wait also joins the stdin copier, including on a failed handshake.
			defer writer.Close()
			var response struct{ Handle uintptr }
			decoder := json.NewDecoder(io.LimitReader(out, 4096))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&response); err != nil || response.Handle == 0 {
				return ErrRootLease
			}
			var handle windows.Handle
			var duplicateErr error
			if err := cmd.Process.WithHandle(func(process uintptr) {
				duplicateErr = windows.DuplicateHandle(windows.Handle(process), windows.Handle(response.Handle), windows.CurrentProcess(), &handle, 0, false, windows.DUPLICATE_SAME_ACCESS)
			}); err != nil {
				return err
			}
			if duplicateErr != nil {
				return duplicateErr
			}
			file := os.NewFile(uintptr(handle), path)
			if err := json.NewEncoder(writer).Encode(true); err != nil {
				file.Close()
				return err
			}
			writer.Close()
			select {
			case received <- file:
				return nil
			case <-ctx.Done():
				file.Close()
				return ctx.Err()
			}
		}, func() { stopScope() }, operationDone)
	})
	select {
	case file := <-received:
		if setup.Err() != nil || owner.Err() != nil {
			file.Close()
			return nil, ErrRootLease
		}
		var token [24]byte
		if _, err := rand.Read(token[:]); err != nil {
			file.Close()
			return nil, err
		}
		life, closeLife := context.WithCancel(context.Background())
		r := &RootRegistration{operations: c.SourceOperations, directory: file, path: path, id: hex.EncodeToString(token[:]), life: &registrationLifetime{Context: life, owner: owner}, cancel: closeLife}
		r.mu.Lock()
		r.stopOwner = context.AfterFunc(owner, func() { r.Close() })
		r.mu.Unlock()
		if owner.Err() != nil {
			r.Close()
			return nil, ErrRootLease
		}
		return r, nil
	case err := <-done:
		if err == nil {
			err = ErrRootLease
		}
		return nil, err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func registerRootHelper(req request, in io.Reader, out io.Writer) error {
	if !filepath.IsAbs(req.Path) {
		return ErrRootLease
	}
	if req.MountedRoot != "" {
		return ErrObservedSourceUnsupported
	}
	if err := checkLocalRoot(req.Path); err != nil {
		return err
	}
	resolved, err := filepath.EvalSymlinks(req.Path)
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(resolved)
	if err != nil {
		return err
	}
	defer root.Close()
	directory, err := root.Open(".")
	if err != nil {
		return err
	}
	defer directory.Close()
	info, err := directory.Stat()
	if err != nil || !info.IsDir() {
		return ErrRootLease
	}
	if _, _, err := windowsFileBinding(directory); err != nil {
		return err
	}
	if err := json.NewEncoder(out).Encode(struct{ Handle uintptr }{directory.Fd()}); err != nil {
		return err
	}
	var acquired bool
	if err := json.NewDecoder(io.LimitReader(in, 4096)).Decode(&acquired); err != nil || !acquired {
		return ErrRootLease
	}
	return nil
}

// Managed mount qualification is independent of plain local directory leases.
func physicalMountIdentity(string) (string, error) { return "", ErrObservedSourceUnsupported }
func sameMountFilesystem(*os.File, string) error   { return ErrObservedSourceUnsupported }
