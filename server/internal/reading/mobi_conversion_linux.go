//go:build linux

package reading

import (
	"context"
	"crypto/sha256"
	"debug/elf"
	"encoding/hex"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strconv"
)

func verifiedMobiExecutable(path, digest string, static bool) (*os.File, error) {
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() || before.Size() <= 0 || before.Size() > 32<<20 || before.Mode().Perm()&0022 != 0 || before.Mode().Perm()&0111 == 0 {
		return nil, ErrUnavailable
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	good := false
	defer func() {
		if !good {
			file.Close()
		}
	}()
	after, err := file.Stat()
	if err != nil || !os.SameFile(before, after) {
		return nil, ErrChanged
	}
	hash := sha256.New()
	if _, err = io.Copy(hash, io.LimitReader(file, 32<<20+1)); err != nil {
		return nil, err
	}
	if hex.EncodeToString(hash.Sum(nil)) != digest {
		return nil, ErrUnavailable
	}
	binary, err := elf.NewFile(file)
	if err != nil {
		return nil, ErrUnavailable
	}
	if static {
		if binary.Type != elf.ET_EXEC || binary.Machine != elf.EM_X86_64 || binary.Class != elf.ELFCLASS64 {
			return nil, ErrUnavailable
		}
		needed, err := binary.DynString(elf.DT_NEEDED)
		if err != nil || len(needed) != 0 {
			return nil, ErrUnavailable
		}
		for _, program := range binary.Progs {
			if program.Type == elf.PT_INTERP {
				return nil, ErrUnavailable
			}
		}
	}
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	good = true
	return file, nil
}

func (c *MobiConverter) run(ctx context.Context, input *os.File) ([]byte, error) {
	sandbox, err := verifiedMobiExecutable(c.bundle.Sandbox, c.bundle.SandboxSHA256, false)
	if err != nil {
		return nil, err
	}
	defer sandbox.Close()
	worker, err := verifiedMobiExecutable(c.bundle.Worker, c.bundle.WorkerSHA256, true)
	if err != nil {
		return nil, err
	}
	defer worker.Close()
	// Exact inherited descriptors prevent path replacement from substituting
	// converter/input between verification and namespace mount setup.
	args := []string{"--unshare-all", "--die-with-parent", "--new-session", "--disable-userns", "--assert-userns-disabled", "--clearenv", "--cap-drop", "ALL", "--dir", "/input", "--ro-bind", "/proc/self/fd/4", "/worker", "--ro-bind", "/proc/self/fd/5", "/input/book.mobi", "--dir", "/output", "--dev", "/dev", "--remount-ro", "/dev", "--remount-ro", "/", "--size", strconv.Itoa(maxMobiOutput), "--tmpfs", "/output", "--chdir", "/output", "/worker"}
	command := exec.CommandContext(ctx, "/proc/self/fd/3", args...)
	command.ExtraFiles = []*os.File{sandbox, worker, input}
	command.Env = []string{}
	var output []byte
	retired := make(chan struct{})
	err = c.supervisor.RunOwnedCompletion(ctx, "reading-mobi", command, func(reader io.Reader) error {
		var readErr error
		output, readErr = io.ReadAll(io.LimitReader(reader, maxMobiOutput+1))
		if len(output) > maxMobiOutput {
			return ErrLimit
		}
		return readErr
	}, nil, func() { close(retired) })
	// Keep fd/scratch custody until physical retirement, including cancellation.
	<-retired
	if err != nil {
		clear(output)
		return nil, err
	}
	if len(output) == 0 {
		return nil, ErrInvalid
	}
	return output, nil
}

func validateMobiBundle(bundle MobiBundle) error {
	if runtime.GOARCH != "amd64" {
		return ErrUnavailable
	}
	sandbox, err := verifiedMobiExecutable(bundle.Sandbox, bundle.SandboxSHA256, false)
	if err != nil {
		return err
	}
	sandbox.Close()
	worker, err := verifiedMobiExecutable(bundle.Worker, bundle.WorkerSHA256, true)
	if err != nil {
		return err
	}
	return worker.Close()
}
