package disc

import (
	"context"
	"io"
	"os"
	"github.com/PorticoMediaServer/portico/server/internal/mediaexec"
	"github.com/PorticoMediaServer/portico/server/internal/mediatools"
	"strconv"
	"time"
)

// RunDescriptor never resolves a library pathname. Directory descriptors must be
// mounted by the sandbox; unsupported platforms refuse instead of falling back.
func (t Tool) runDescriptor(ctx context.Context, file *os.File, directory bool, args ...string) ([]byte, error) {
	if file == nil {
		return nil, ErrProtocol
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	binary := t.Binary
	if binary == "" {
		binary = mediatools.Resolve("portico-disc")
	}
	job := mediaexec.Job{Executable: binary, Args: args, Files: []*os.File{file}, MaxFileBytes: mediaexec.NoOutputFiles}
	if directory {
		job.InputFD = 3
		job.InputMount = "/disc"
	}
	cmd, err := mediaexec.CommandContext(ctx, job)
	if err != nil {
		return nil, err
	}
	out := &outputBuffer{}
	cmd.Stdout = out
	cmd.Stderr = io.Discard
	cmd.WaitDelay = time.Second
	if err = cmd.Run(); err != nil {
		return nil, err
	}
	return out.data, nil
}
func DescriptorPath(directory bool) string {
	if directory {
		return "/disc"
	}
	return "/dev/fd/3"
}
func (t Tool) ProbeDescriptor(ctx context.Context, file *os.File, directory bool) (Probe, error) {
	raw, err := t.runDescriptor(ctx, file, directory, "probe", DescriptorPath(directory))
	if err != nil {
		return Probe{}, err
	}
	return DecodeProbe(raw)
}
func (t Tool) SeekDescriptor(ctx context.Context, file *os.File, directory bool, playlist int, ms int64) (Seek, error) {
	if playlist < 0 || playlist > 99999 || ms < 0 {
		return Seek{}, ErrProtocol
	}
	raw, err := t.runDescriptor(ctx, file, directory, "seek", DescriptorPath(directory), strconv.Itoa(playlist), strconv.FormatInt(ms, 10))
	if err != nil {
		return Seek{}, err
	}
	return DecodeSeek(raw, playlist, ms)
}
