package disc

import (
	"context"
	"io"
	"strconv"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/mediaexec"
	"github.com/PorticoMediaServer/portico/server-next/internal/mediatools"
)

// outputBuffer fails closed on truncation. The helper also buffers at most
// 1MiB before publishing JSON, so oversized structures never look successful.
type outputBuffer struct{ data []byte }

func (b *outputBuffer) Write(p []byte) (int, error) {
	if len(b.data)+len(p) > MaxOutputBytes {
		return 0, ErrProtocol
	}
	b.data = append(b.data, p...)
	return len(p), nil
}

// Tool runs the disc structure helper. ScanCommand, when the caller sets it,
// runs the helper through the caller's own admitted-file reader, which hands the
// file or folder over as a descriptor; it reports handled=false to fall back to
// the path-based sandbox. A disc folder has no fallback.
type Tool struct {
	Binary      string
	ScanCommand func(ctx context.Context, binary string, args []string, path string, directory bool, out io.Writer) (handled bool, err error)
}

func (t Tool) run(ctx context.Context, path string, args ...string) ([]byte, error) {
	return t.runPath(ctx, path, false, args...)
}

// runPath runs the helper over an admitted file or, when directory is true,
// over an admitted disc folder through one inherited directory descriptor.
func (t Tool) runPath(ctx context.Context, path string, directory bool, args ...string) ([]byte, error) {
	if !cleanLocalPath(path) {
		return nil, ErrProtocol
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	binary := t.Binary
	if binary == "" {
		binary = mediatools.Resolve("portico-disc")
	}
	out := &outputBuffer{}
	// An inventory invocation already owns its admitted root/read guard. Reuse
	// that actual confined helper acquisition rather than opening its pathname
	// again. Remote and directory inputs remain closed by the existing adapter.
	run := func(ctx context.Context, binary string, args []string, path string, out io.Writer) (bool, error) {
		if t.ScanCommand == nil {
			return false, nil
		}
		return t.ScanCommand(ctx, binary, args, path, directory, out)
	}
	if handled, err := run(ctx, binary, args, path, out); handled {
		if err != nil {
			return nil, err
		}
		return out.data, nil
	}
	if directory {
		// A disc folder has no path-based fallback.
		return nil, ErrProtocol
	}
	cmd, err := mediaexec.CommandContext(ctx, mediaexec.Job{Executable: binary, Args: args, ReadPaths: []string{path}, MaxFileBytes: mediaexec.NoOutputFiles})
	if err != nil {
		return nil, err
	}
	cmd.Stdout = out
	cmd.Stderr = io.Discard
	cmd.WaitDelay = time.Second
	if err = cmd.Run(); err != nil {
		return nil, err
	}
	return out.data, nil
}
func (t Tool) ProbeLocal(ctx context.Context, path string) (Probe, error) {
	raw, err := t.run(ctx, path, "probe", path)
	if err != nil {
		return Probe{}, err
	}
	return DecodeProbe(raw)
}

// ProbeDirectory observes an admitted BDMV or VIDEO_TS folder through the
// descriptor-bound directory input; no host pathname is handed to the helper.
func (t Tool) ProbeDirectory(ctx context.Context, path string) (Probe, error) {
	raw, err := t.runPath(ctx, path, true, "probe", path)
	if err != nil {
		return Probe{}, err
	}
	return DecodeProbe(raw)
}
func (t Tool) SeekLocal(ctx context.Context, path string, playlist int, ms int64) (Seek, error) {
	if playlist < 0 || playlist > 99999 || ms < 0 {
		return Seek{}, ErrProtocol
	}
	raw, err := t.run(ctx, path, "seek", path, strconv.Itoa(playlist), strconv.FormatInt(ms, 10))
	if err != nil {
		return Seek{}, err
	}
	return DecodeSeek(raw, playlist, ms)
}
