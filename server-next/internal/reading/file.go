package reading

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/libraryfile"
	"github.com/PorticoMediaServer/portico/server-next/internal/streaming"
)

// bookFile is a book's file opened for reading: one in a library folder on this server, opened
// through libraryfile, or one on a remote source, read through that source.
type bookFile struct {
	// name is the file's path, for its format and its name when saved.
	name     string
	size     int64
	modified time.Time
	// at reads parts of the file, as an archive is read.
	at io.ReaderAt
	// whole gives the file for reading by range from any place, as a PDF and a saved file are.
	whole func() (io.ReadSeekCloser, error)
	close func()
}

// openFile opens the file the scanner recorded under this id: a local one through libraryfile,
// a remote one through its source (behind that source's throttle, which waits when the source
// says to slow down, as playback's reads do). It must still have the size, and for a local
// file the modified time, the scanner recorded: otherwise ErrChanged. A file that is gone, or
// whose source cannot be reached, is ErrUnavailable.
func (s *Service) openFile(ctx context.Context, fileID int64) (*bookFile, db.FileOnDisk, error) {
	place, err := s.db.FileOnDisk(ctx, fileID)
	if errors.Is(err, db.ErrNotFound) {
		return s.openRemote(ctx, fileID)
	}
	if err != nil {
		return nil, place, err
	}
	folders, err := s.db.LibraryFolders(ctx)
	if err != nil {
		return nil, place, err
	}
	f, err := libraryfile.Open(place.Path, folders)
	if err != nil {
		return nil, place, ErrUnavailable
	}
	info, err := f.Stat()
	if err != nil || info.Size() != place.Size || info.ModTime().UnixNano() != place.ModifiedNS {
		f.Close()
		return nil, place, ErrChanged
	}
	return &bookFile{name: place.Path, size: place.Size, modified: info.ModTime(), at: f, close: func() { f.Close() },
		whole: func() (io.ReadSeekCloser, error) {
			_, err := f.Seek(0, io.SeekStart)
			return nopCloser{f}, err
		}}, place, nil
}

func (s *Service) openRemote(ctx context.Context, fileID int64) (*bookFile, db.FileOnDisk, error) {
	sourceID, place, err := s.db.RemoteFileOf(ctx, fileID)
	switch {
	case errors.Is(err, db.ErrNotFound):
		return nil, place, ErrUnavailable
	case err != nil:
		return nil, place, err
	case s.remote == nil:
		return nil, place, ErrRemote
	}
	storage, _, err := s.remote(ctx, sourceID)
	if err != nil {
		return nil, place, ErrUnavailable
	}
	file, err := storage.Open(ctx, place.Path)
	if err != nil {
		if ctx.Err() != nil {
			return nil, place, ctx.Err()
		}
		return nil, place, ErrUnavailable
	}
	if file.Size() != place.Size {
		file.Close()
		return nil, place, ErrChanged
	}
	modified := time.Unix(0, place.ModifiedNS)
	// The source's file reads ahead by itself: each small read is answered from 1 MiB blocks,
	// one request each, keeping the last four (remote.Throttled). A zip reader asks for a few
	// kilobytes at a time, so a comic page of a few megabytes is a few requests, and one
	// request for a part or page holds at most 4 MiB however large the book. The file is
	// closed when that request is answered.
	return &bookFile{name: place.Path, size: place.Size, modified: modified, at: file, close: func() { file.Close() },
		whole: func() (io.ReadSeekCloser, error) {
			// The whole file, as playback reads it: one request from each place reading starts.
			rs, _, _, err := streaming.RemoteFile(storage, place.Path, place.Size, modified).Open(ctx)
			return rs, err
		}}, place, nil
}

type nopCloser struct{ io.ReadSeeker }

func (nopCloser) Close() error { return nil }
