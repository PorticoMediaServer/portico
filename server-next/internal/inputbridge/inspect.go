package inputbridge

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"os/exec"
	"path/filepath"

	"github.com/PorticoMediaServer/portico/server-next/internal/decoder"
	"github.com/PorticoMediaServer/portico/server-next/internal/mediaexec"
	"github.com/PorticoMediaServer/portico/server-next/internal/mediafacts"
	"github.com/PorticoMediaServer/portico/server-next/internal/processpool"
)

// Inspect reads a file's facts, the way a scan reads a file on a disk here, from a source of
// a known size that is not a file here (remote storage). ffprobe runs on the decoder's
// isolated bridge: in its own network namespace, able to reach only this bridge's address,
// with its port reserved until it has really exited. ffprobe is the tool to run; empty finds
// the bundled or installed one.
func Inspect(ctx context.Context, supervisor *processpool.Supervisor, ffprobe string, source io.ReaderAt, size int64) (mediafacts.Facts, error) {
	bridge, err := Open(ctx, source, size)
	if err != nil {
		return mediafacts.Facts{}, err
	}
	defer bridge.Close()
	run := func(ctx context.Context, binary string, args []string, input string, _ mediafacts.ScanMode, out io.Writer) (bool, error) {
		binary, err := exec.LookPath(binary)
		if err == nil {
			binary, err = filepath.Abs(binary)
		}
		if err != nil {
			return true, err
		}
		libraries, err := mediaexec.ResolveLibraries(binary, binary, nil)
		if err != nil {
			return true, err
		}
		reservation, err := bridge.Reserve()
		if err != nil {
			return true, err
		}
		var key [8]byte
		rand.Read(key[:])
		return true, decoder.RunStreamingProbe(ctx, supervisor, hex.EncodeToString(key[:]), binary, input, args, reservation,
			func(r io.Reader) error { _, err := io.Copy(out, r); return err }, libraries...)
	}
	// A matroska header and a Musepack timeline are read from the file's first bytes.
	readPrefix := func(_ context.Context, _ string, limit int64) ([]byte, error) {
		prefix := make([]byte, min(limit, size))
		n, err := source.ReadAt(prefix, 0)
		if err == io.EOF && n == len(prefix) {
			err = nil
		}
		return prefix[:n], err
	}
	return mediafacts.Probe{Binary: ffprobe, ScanCommand: run, ReadPrefix: readPrefix}.InspectRemote(ctx, bridge.URL())
}
