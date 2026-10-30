//go:build !windows

package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"os/exec"
	"github.com/PorticoMediaServer/portico/server/internal/supervise"
	"syscall"
	"time"
)

// DescriptorPassing reports that a helper can hand the server an open file.
const DescriptorPassing = true

// OpenPlaybackDescriptor transfers a validated descriptor, not a source path
// reopened by the parent. No network-filesystem syscall runs in the parent.
func (c *Client) OpenPlaybackDescriptor(ctx context.Context, path string, size, modified int64, bindings ...*PlaybackBinding) (*os.File, error) {
	req, err := c.bindPlaybackRequest(request{Operation: "playback-descriptor", Path: path, Size: size, ModifiedNS: modified}, bindings)
	if err != nil {
		return nil, err
	}
	return c.openPlaybackDescriptorRequest(ctx, req)
}

// OpenDiscPlaybackDescriptor pins the exact inventory object beneath its
// admitted root. Directory inputs can only be handed to the fd-bound sandbox.
func (c *Client) OpenDiscPlaybackDescriptor(ctx context.Context, r InventoryRequest, object string, size, modified int64, directory bool) (*os.File, error) {
	path, err := inventoryPath(r)
	if err != nil {
		return nil, err
	}
	if err = CheckScanRead(ctx, path); err != nil {
		return nil, err
	}
	operation := "disc-file-descriptor"
	if directory {
		operation = "disc-directory-descriptor"
	}
	return c.openPlaybackDescriptorRequest(ctx, request{Operation: operation, Path: path, Inventory: &r, Identity: object, Size: size, ModifiedNS: modified})
}
func (c *Client) openPlaybackDescriptorRequest(ctx context.Context, req request) (*os.File, error) {
	path := req.Path

	if c.Guard != nil {
		if e := c.Guard(path); e != nil {
			return nil, e
		}
	}
	managed, err := c.managedRecording(ctx, path)
	if err != nil {
		return nil, err
	}
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	pair, e := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if e != nil {
		return nil, e
	}
	a := os.NewFile(uintptr(pair[0]), "playback-parent")
	b := os.NewFile(uintptr(pair[1]), "playback-child")
	defer b.Close()
	conn, e := net.FileConn(a)
	a.Close()
	if e != nil {
		return nil, e
	}
	defer conn.Close()
	socket := conn.(*net.UnixConn)
	req.ManagedArtifact = managed
	if req.MountedRoot == "" && c.MountedRoot != nil {
		req.MountedRoot = c.MountedRoot(path)
	}
	body, _ := json.Marshal(req)
	cmd := exec.Command(c.Binary, "--portico-storage-helper")
	cmd.Stdin = bytes.NewReader(body)
	cmd.ExtraFiles = []*os.File{b}
	files := make(chan *os.File)
	done := make(chan error, 1)
	supervise.Go("storage.playback-descriptor", func() {
		done <- c.Supervisor.Run(ctx, "playback:descriptor:"+fmtReaderID(), cmd, func(stdout io.Reader) error {
			var signal [1]byte
			if _, e := io.ReadFull(stdout, signal[:]); e != nil || signal[0] != 1 {
				return ErrPlaybackSource
			}
			_ = socket.SetReadDeadline(time.Now().Add(timeout))
			var data [1]byte
			oob := make([]byte, syscall.CmsgSpace(4))
			n, on, flags, _, err := socket.ReadMsgUnix(data[:], oob)
			if err != nil || n != 1 || data[0] != 1 || flags&syscall.MSG_CTRUNC != 0 {
				return ErrPlaybackSource
			}
			msgs, err := syscall.ParseSocketControlMessage(oob[:on])
			if err != nil {
				return ErrPlaybackSource
			}
			var descriptors []int
			for _, m := range msgs {
				fds, e := syscall.ParseUnixRights(&m)
				if e != nil {
					for _, fd := range descriptors {
						syscall.Close(fd)
					}
					return ErrPlaybackSource
				}
				descriptors = append(descriptors, fds...)
			}
			if len(descriptors) != 1 {
				for _, fd := range descriptors {
					syscall.Close(fd)
				}
				return ErrPlaybackSource
			}
			syscall.CloseOnExec(descriptors[0])
			f := os.NewFile(uintptr(descriptors[0]), "pinned-playback")
			select {
			case files <- f:
				return nil
			case <-ctx.Done():
				f.Close()
				return ctx.Err()
			}
		})
	})
	select {
	case f := <-files:
		return f, nil
	case e := <-done:
		if e == nil {
			e = ErrPlaybackSource
		}
		return nil, playbackOpenError(e)
	case <-ctx.Done():
		if ctx.Err() == context.DeadlineExceeded {
			return nil, ErrPlaybackTimeout
		}
		return nil, ctx.Err()
	}
}
func playbackDescriptorHelper(req request, out io.Writer) error {
	var f *os.File
	var e error
	switch req.Operation {
	case "disc-directory-descriptor":
		if req.Inventory == nil {
			return ErrPlaybackSource
		}
		f, e = openInventoryDirectory(*req.Inventory)
	case "disc-file-descriptor":
		if req.Inventory == nil {
			return ErrPlaybackSource
		}
		f, e = openInventoryContent(*req.Inventory)
	default:
		f, e = openPlaybackRequestFile(req)
	}
	if e != nil {
		return ErrPlaybackSource
	}
	defer f.Close()
	if e = lockRecordingFile(req, f); e != nil {
		return e
	}
	info, e := f.Stat()
	if e != nil || (req.Operation == "disc-directory-descriptor" && !info.IsDir() || req.Operation != "disc-directory-descriptor" && !info.Mode().IsRegular()) || info.Size() != req.Size || info.ModTime().UnixNano() != req.ModifiedNS {
		return ErrPlaybackSource
	}
	if req.Inventory != nil && req.Operation != "playback-descriptor" {
		if !matchesPlaybackObject(f, *req.Inventory, req.Identity) {
			return ErrPlaybackSource
		}
	}
	current, e := playbackCurrentInfo(req)
	if e != nil || !os.SameFile(info, current) {
		return ErrPlaybackSource
	}
	socket := os.NewFile(3, "playback-control")
	if socket == nil {
		return ErrPlaybackSource
	}
	defer socket.Close()
	if e := syscall.Sendmsg(int(socket.Fd()), []byte{1}, syscall.UnixRights(int(f.Fd())), nil, 0); e != nil {
		return e
	}
	_, e = out.Write([]byte{1})
	return e
}

// ValidatePlaybackDescriptor checks the already pinned file in a bounded child.
// The caller retains ownership and must keep file open until this call returns.
// No source path is reopened, and no source stat executes in the parent.
func (c *Client) ValidatePlaybackDescriptor(ctx context.Context, file *os.File, size, modified int64) error {
	if file == nil || size < 0 {
		return ErrPlaybackSource
	}
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	body, _ := json.Marshal(request{Operation: "playback-validate-descriptor", Size: size, ModifiedNS: modified})
	cmd := exec.Command(c.Binary, "--portico-storage-helper")
	cmd.Stdin = bytes.NewReader(body)
	cmd.ExtraFiles = []*os.File{file}
	err := c.Supervisor.Run(ctx, "playback:validate-descriptor:"+fmtReaderID(), cmd, func(stdout io.Reader) error {
		var signal [2]byte
		n, err := io.ReadFull(stdout, signal[:])
		if n != 1 || err != io.ErrUnexpectedEOF || signal[0] != 1 {
			return ErrPlaybackSource
		}
		return nil
	})
	if ctx.Err() == context.DeadlineExceeded {
		return ErrPlaybackTimeout
	}
	if err == nil {
		return nil
	}
	return playbackOpenError(err)
}

func playbackValidateDescriptorHelper(req request, out io.Writer) error {
	f := os.NewFile(3, "pinned-playback-validation")
	if f == nil {
		return ErrPlaybackSource
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != req.Size || info.ModTime().UnixNano() != req.ModifiedNS {
		return ErrPlaybackSource
	}
	_, err = out.Write([]byte{1})
	return err
}
