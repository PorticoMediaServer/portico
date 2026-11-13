package streaming

import (
	"context"
	"os"
	"path/filepath"
	"sync"
)

// PlaybackInput is a validated source handed to a converter.
//
// On Unix the server opens the file itself and the child inherits it as
// descriptor 3, so the sandboxed converter never needs authority over a library
// path. On Windows the server holds it open and the child opens the same path
// by name. Callers therefore ask what to put after `-i` and whether to inherit.
type PlaybackInput struct {
	// File must stay open until the child exits. On Unix it is the inherited
	// descriptor; on Windows it is the handle that keeps the file in place.
	File *os.File
	// Argument is what follows `-i`.
	Argument string
	// Inherit says the file belongs in the child's ExtraFiles.
	Inherit bool
	// Options apply to this one reader, before -i. MountFD is inherited directory
	// descriptor 3, bound only at MountPath by mediaexec.
	Options   []string
	InputAt   func(context.Context, int64) ([]string, string, int64, error)
	MountFD   int
	MountPath string
	Validate  func(context.Context) error
	Extra     []*os.File
	// Lifetime is the original retained source's revocable lifetime. A converter
	// borrows it as well as its own operation context; possession of File does not
	// extend source or actor authority.
	Lifetime context.Context
	// ReleaseSource, when present, owns File and its source/reader pins together.
	// Close must call it instead of closing only the descriptor. Extra remains
	// owned by this input. It must not release another presentation's reference.
	ReleaseSource func() error
	closeOnce     sync.Once
	closeErr      error
}

// BorrowContext cancels an operation when either its caller or the retained
// source retires. The returned release must run after the child has exited.
func (p *PlaybackInput) BorrowContext(ctx context.Context) (context.Context, func()) {
	if p == nil || p.Lifetime == nil {
		return ctx, func() {}
	}
	run, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(p.Lifetime, cancel)
	if p.Lifetime.Err() != nil {
		cancel()
	}
	return run, func() { stop(); cancel() }
}

// ExtraFiles is the slice to assign to exec.Cmd.ExtraFiles: the descriptor on
// Unix, nothing on Windows, where ExtraFiles is not supported at all.
func (p *PlaybackInput) ExtraFiles() []*os.File {
	if p == nil || !p.Inherit || p.File == nil {
		return nil
	}
	return append([]*os.File{p.File}, p.Extra...)
}

// ReadPaths is what a sandboxed converter must be allowed to read by name: the
// source path where it is not handed over as a descriptor (Windows, and tests
// that pass a plain path).
func (p *PlaybackInput) ReadPaths() []string {
	if p == nil || p.Inherit || !filepath.IsAbs(p.Argument) || filepath.Clean(p.Argument) != p.Argument {
		return nil
	}
	return []string{p.Argument}
}

// Close releases the source. It is safe on a nil input, so a caller can defer it
// before checking the error.
func (p *PlaybackInput) Close() error {
	if p == nil {
		return nil
	}
	p.closeOnce.Do(func() {
		if p.ReleaseSource != nil {
			p.closeErr = p.ReleaseSource()
		} else if p.File != nil {
			p.closeErr = p.File.Close()
		}
		for _, f := range p.Extra {
			if f == nil {
				continue
			}
			if e := f.Close(); p.closeErr == nil {
				p.closeErr = e
			}
		}
	})
	return p.closeErr
}
