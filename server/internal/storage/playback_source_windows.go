//go:build windows

package storage

import (
	"context"
	"os"
)

// Converted playback could not work on Windows at all.
//
// On Unix a helper opens the source, checks that its size and modification time
// still match what the session was created against, and hands the open
// descriptor back over a socket. The server passes that descriptor to ffmpeg as
// `/dev/fd/3`. The point is not convenience: it is that ffmpeg reads the exact
// file that was validated, so a rescan that replaces the file between the check
// and the read cannot substitute different media into somebody's stream.
//
// Windows has neither `/dev/fd` nor descriptor passing over a socket, so that
// path returned "unsupported" and every converted stream failed. What Windows
// does have is a share mode. A file opened without FILE_SHARE_WRITE and without
// FILE_SHARE_DELETE cannot be written, renamed, replaced or deleted by anybody
// else for as long as the handle is open — which is the same guarantee the
// descriptor hand-off provides, obtained from the other end.
//
// So on Windows the server opens the source itself with writers and deleters
// locked out, validates size and modification time on that handle, and keeps it
// open for the life of the conversion while ffmpeg opens the same path by name.
// ffmpeg asks for read access and read sharing, which this share mode permits;
// anything trying to modify the file is refused while the stream runs.
//
// Windows runtime validation remains required. The behaviour this depends on —
// that CreateFileW with dwShareMode = FILE_SHARE_READ blocks other writers and
// deleters, and that a second reader may still open the file — is documented
// Win32 behaviour, but "documented" and "observed" are different words.

// OpenPlaybackSource opens the source for a converter and reports what to hand
// ffmpeg after `-i`. The returned file must stay open until the child exits.
func (c *Client) OpenPlaybackSource(ctx context.Context, path string, size, modified int64, bindings ...*PlaybackBinding) (*os.File, string, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, "", false, err
	}
	if c != nil && c.Guard != nil {
		if err := c.Guard(path); err != nil {
			return nil, "", false, err
		}
	}
	req, err := c.bindPlaybackRequest(request{Path: path, Size: size, ModifiedNS: modified}, bindings)
	if err != nil {
		return nil, "", false, ErrPlaybackSource
	}
	if req.MountedRoot != "" {
		// Managed physical mounts are unsupported on Windows. In particular,
		// do not turn the explicit mounted binding into a local path fallback.
		return nil, "", false, ErrPlaybackSource
	}
	admitted, err := openPlaybackRequestFile(req)
	if err != nil {
		return nil, "", false, ErrPlaybackSource
	}
	defer admitted.Close()
	// Reuse the scan reader's handle-identity check and locked canonical path.
	// That path permits a scan-approved in-root symlink without reopening the
	// mutable link by name in the decoder. The returned file owns the one lock.
	file, path, closeInput, err := lockWindowsMediaInput(admitted)
	if err != nil {
		return nil, "", false, ErrPlaybackSource
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		closeInput()
		return nil, "", false, ErrPlaybackSource
	}
	// The same identity check the descriptor helper makes, on the handle that
	// will be held for the conversion rather than on a path that could change.
	if info.Size() != size || info.ModTime().UnixNano() != modified {
		closeInput()
		return nil, "", false, ErrPlaybackSource
	}
	// The path, not a descriptor: ffmpeg cannot be handed an inherited handle by
	// number on Windows, and it does not need to be, because nothing can change
	// the file underneath it while this handle is open.
	return file, path, false, nil
}
