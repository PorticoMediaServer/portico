//go:build darwin || linux

package storage

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/supervise"
	"syscall"
	"time"
)

func duplicateRoot(file *os.File) (*os.File, error) {
	// F_DUPFD_CLOEXEC avoids descriptor inheritance races with concurrent exec.
	fd, _, errno := syscall.Syscall(syscall.SYS_FCNTL, file.Fd(), syscall.F_DUPFD_CLOEXEC, 0)
	if errno != 0 {
		return nil, errno
	}
	return os.NewFile(fd, "registered-root-borrow"), nil
}

type registrationResult struct {
	file  *os.File
	mount string
}

// RegisterRoot performs source filesystem calls only in the bounded helper.
// owner is a durable runtime owner, never the setup deadline context.
func (c *Client) RegisterRoot(setup context.Context, path, mountPath, mountIdentity string, owner context.Context) (*RootRegistration, error) {
	if c == nil || c.Supervisor == nil || c.Binary == "" || owner == nil || owner.Err() != nil || !filepath.IsAbs(path) || (mountPath != "" && !filepath.IsAbs(mountPath)) {
		return nil, ErrRootLease
	}
	if (mountPath == "") != (mountIdentity == "") {
		return nil, ErrRootLease
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
	syscall.ForkLock.RLock()
	pair, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err == nil {
		syscall.CloseOnExec(pair[0])
		syscall.CloseOnExec(pair[1])
	}
	syscall.ForkLock.RUnlock()
	if err != nil {
		return nil, err
	}
	a := os.NewFile(uintptr(pair[0]), "root-registration-parent")
	b := os.NewFile(uintptr(pair[1]), "root-registration-child")
	conn, err := net.FileConn(a)
	a.Close()
	if err != nil {
		b.Close()
		return nil, err
	}
	socket := conn.(*net.UnixConn)
	body, _ := json.Marshal(request{Operation: "register-root", Path: path, MountedRoot: mountPath, ExpectedMountIdentity: mountIdentity})
	cmd := exec.Command(c.Binary, "--portico-storage-helper")
	cmd.Stdin = bytes.NewReader(body)
	cmd.ExtraFiles = []*os.File{b}
	operationDone := func() {}
	stopScope := func() bool { return false }
	if c.SourceOperations != nil {
		operationDone, err = c.SourceOperations.Begin()
		if err != nil {
			b.Close()
			socket.Close()
			return nil, err
		}
		stopScope = context.AfterFunc(c.SourceOperations.Context(), cancel)
	}
	received := make(chan registrationResult)
	done := make(chan error, 1)
	supervise.Go("storage.root-registration", func() {
		done <- c.Supervisor.RunOwnedCompletion(ctx, "playback:register:"+fmtReaderID(), cmd, func(out io.Reader) error {
			var signal [1]byte
			if _, err := io.ReadFull(out, signal[:]); err != nil || signal[0] != 1 {
				return ErrRootLease
			}
			socket.SetReadDeadline(time.Now().Add(timeout))
			data := make([]byte, 4096)
			n, flags, fds, readErr := receiveRootDescriptors(socket, data)

			keep := false
			defer func() {
				if !keep {
					for _, fd := range fds {
						syscall.Close(fd)
					}
				}
			}()
			if readErr != nil || flags&(syscall.MSG_CTRUNC|syscall.MSG_TRUNC) != 0 || len(fds) != 1 || n < 1 || n == len(data) {
				return ErrRootLease
			}
			var meta struct{ Mount string }
			if json.Unmarshal(data[:n], &meta) != nil || (mountPath != "" && meta.Mount != mountIdentity) || (mountPath == "" && meta.Mount != "") {
				return ErrRootLease
			}
			file := os.NewFile(uintptr(fds[0]), "registered-source-root")
			select {
			case received <- registrationResult{file, meta.Mount}:
				keep = true
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}, func() { b.Close(); socket.Close(); stopScope() }, operationDone)
	})
	select {
	case value := <-received:
		if setup.Err() != nil || owner.Err() != nil {
			value.file.Close()
			return nil, ErrRootLease
		}
		var token [24]byte
		if _, err = rand.Read(token[:]); err != nil {
			value.file.Close()
			return nil, err
		}
		life, closeLife := context.WithCancel(context.Background())
		r := &RootRegistration{operations: c.SourceOperations, directory: value.file, path: path, id: hex.EncodeToString(token[:]), mountPath: mountPath, mountIdentity: value.mount, life: &registrationLifetime{Context: life, owner: owner}, cancel: closeLife}
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

func registerRootHelper(req request, _ io.Reader, out io.Writer) error {
	if !filepath.IsAbs(req.Path) {
		return ErrRootLease
	}
	root, err := os.OpenRoot(req.Path)
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
	mount := ""
	if req.MountedRoot != "" {
		if err = sameMountFilesystem(directory, req.MountedRoot); err != nil {
			return err
		}
		mount, err = physicalMountIdentity(req.MountedRoot)
		if err != nil || mount != req.ExpectedMountIdentity {
			return ErrRootLease
		}
	}
	body, _ := json.Marshal(struct{ Mount string }{mount})
	socket := os.NewFile(3, "root-registration-control")
	if socket == nil {
		return ErrRootLease
	}
	defer socket.Close()
	if err = syscall.Sendmsg(int(socket.Fd()), body, syscall.UnixRights(int(directory.Fd())), nil, 0); err != nil {
		return err
	}
	_, err = out.Write([]byte{1})
	return err
}

// RawConn.Read waits through the network poller outside ForkLock. The actual
// recvmsg is nonblocking; received handles become CLOEXEC before another Go exec
// can fork. No global fork lock is held while waiting for helper IO.
func receiveRootDescriptors(socket *net.UnixConn, data []byte) (n, flags int, fds []int, result error) {
	raw, err := socket.SyscallConn()
	if err != nil {
		return 0, 0, nil, err
	}
	oob := make([]byte, syscall.CmsgSpace(4*8))
	err = raw.Read(func(fd uintptr) bool {
		syscall.ForkLock.RLock()
		defer syscall.ForkLock.RUnlock()
		count, on, f, _, e := syscall.Recvmsg(int(fd), data, oob, 0)
		if e == syscall.EAGAIN || e == syscall.EWOULDBLOCK {
			return false
		}
		n, flags, result = count, f, e
		messages, parseErr := syscall.ParseSocketControlMessage(oob[:on])
		if parseErr != nil {
			result = parseErr
		}
		for _, message := range messages {
			values, e := syscall.ParseUnixRights(&message)
			if e != nil {
				result = e
			}
			for _, received := range values {
				syscall.CloseOnExec(received)
			}
			fds = append(fds, values...)
		}
		return true
	})
	if err != nil {
		result = err
	}
	return
}
