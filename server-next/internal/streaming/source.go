package streaming

import (
	"context"
	"errors"
	"io"
	"os"
	"runtime"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/libraryfile"
)

// ErrSourceUnavailable says the file a stream plays cannot be opened: it is
// gone, it is outside every library folder, or it is not a regular file.
var ErrSourceUnavailable = errors.New("The file for this title cannot be reached.")

// ErrSourceChanged says the file is not the one that was scanned, so the plan
// made from its facts no longer describes it.
var ErrSourceChanged = errors.New("The file for this title has changed since it was scanned.")

// Source is where a stream's bytes come from. A library file is the only kind
// today; a remote source (WebDAV, rclone) is another implementation.
type Source interface {
	// Open returns the bytes for reading by range, with their size and modified
	// time. The caller closes it.
	Open(ctx context.Context) (io.ReadSeekCloser, int64, time.Time, error)
}

// libraryFile is a file inside one of the owner's library folders, opened by
// the server itself.
type libraryFile struct {
	path     string
	size     int64
	modified time.Time
	roots    func() []string
}

// Open follows the one rule for library files (package libraryfile). A size and
// modified time, when given, must still match.
func (f libraryFile) Open(ctx context.Context) (io.ReadSeekCloser, int64, time.Time, error) {
	if err := ctx.Err(); err != nil {
		return nil, 0, time.Time{}, err
	}
	file, err := libraryfile.Open(f.path, f.roots())
	if err != nil {
		return nil, 0, time.Time{}, ErrSourceUnavailable
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, 0, time.Time{}, ErrSourceUnavailable
	}
	if f.size > 0 && info.Size() != f.size || !f.modified.IsZero() && !info.ModTime().Equal(f.modified) {
		file.Close()
		return nil, 0, time.Time{}, ErrSourceChanged
	}
	return file, info.Size(), info.ModTime(), nil
}

// converterInput opens a source the way ffmpeg can be given it: as an open
// descriptor on Unix, by path on Windows.
func converterInput(ctx context.Context, source Source) (*PlaybackInput, error) {
	if source == nil {
		return nil, ErrSourceUnavailable
	}
	opened, _, _, err := source.Open(ctx)
	if err != nil {
		return nil, err
	}
	file, local := opened.(*os.File)
	if !local {
		opened.Close()
		return nil, errors.New("this kind of source cannot be converted yet")
	}
	if runtime.GOOS == "windows" {
		return &PlaybackInput{File: file, Argument: file.Name()}, nil
	}
	return &PlaybackInput{File: file, Argument: "/dev/fd/3", Inherit: true}, nil
}
