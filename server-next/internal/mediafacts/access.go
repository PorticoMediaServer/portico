package mediafacts

import (
	"context"
	"errors"
	"io"
	"os"
	"syscall"
)

// ScanMode says how a probe names its input to the caller's reader.
type ScanMode int

const (
	// ScanFile is one admitted regular file.
	ScanFile ScanMode = iota
	// ScanBluRay is one admitted file read through FFmpeg's bluray: input.
	ScanBluRay
	// ScanDirectory is one admitted disc folder, mounted for the tool.
	ScanDirectory
)

// scanAudioPrefixLimit is how much of a music file a header parser may read.
const scanAudioPrefixLimit = 1 << 20

var errSourceChanged = errors.New("the file changed while it was being read")

var errDirectoryReader = errors.New("a disc folder needs the caller's directory reader")

// runScan hands the tool to the caller's reader when one is set. It reports
// handled=false when the probe should run the tool on the path itself.
func (p Probe) runScan(ctx context.Context, binary string, args []string, path string, mode ScanMode, out io.Writer) (bool, error) {
	if p.ScanCommand == nil {
		return false, nil
	}
	return p.ScanCommand(ctx, binary, args, path, mode, out)
}

// readPrefix returns at most limit leading bytes of an admitted file.
func (p Probe) readPrefix(ctx context.Context, path string, limit int64) ([]byte, error) {
	if p.ReadPrefix != nil {
		return p.ReadPrefix(ctx, path, limit)
	}
	// O_NONBLOCK: a named pipe opens at once and is refused as not a regular file.
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	before, err := f.Stat()
	if err != nil || !before.Mode().IsRegular() {
		return nil, errSourceChanged
	}
	prefix, err := io.ReadAll(io.LimitReader(f, limit))
	if err != nil {
		return nil, err
	}
	after, err := f.Stat()
	if err != nil || before.Size() != after.Size() || before.ModTime() != after.ModTime() {
		return nil, errSourceChanged
	}
	return prefix, nil
}
