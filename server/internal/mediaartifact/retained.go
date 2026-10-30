package mediaartifact

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/artifactlease"
	"regexp"
)

var retainedKey = regexp.MustCompile(`^[a-f0-9]{64}\.[1-9][0-9]{0,18}$`)

// BeginRetained creates a lease-specific, crash-recoverable capture staging file.
// The domain persists its key and checkpoints; this method does not publish it.
func (s *Store) BeginRetained(key string, maxBytes int64) (*Writer, error) {
	if !retainedKey.MatchString(key) || maxBytes <= 0 {
		return nil, ErrIdentity
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, ErrState
	}
	path := filepath.Join(s.staging, "capture-"+key)
	f, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if e != nil {
		return nil, e
	}
	if e = artifactlease.Exclusive(f); e != nil {
		f.Close()
		return nil, e
	}
	if e = syncDirectory(s.staging); e != nil {
		f.Close()
		return nil, e
	}
	w := &Writer{store: s, file: f, path: path, hash: sha256.New(), limit: maxBytes, retained: true}
	s.writers[w] = true
	return w, nil
}

// ResumeRetained must only follow proof that the previous physical writer has
// retired. Bytes after the last durably acknowledged checkpoint are discarded;
// missing/truncated checkpoints fail closed rather than inventing coverage.
func (s *Store) ResumeRetained(ctx context.Context, key string, size, maxBytes int64) (*Writer, error) {
	if !retainedKey.MatchString(key) || size < 0 || size > maxBytes || maxBytes <= 0 {
		return nil, ErrIdentity
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, ErrState
	}
	path := filepath.Join(s.staging, "capture-"+key)
	for w := range s.writers {
		if w.path == path {
			return nil, ErrState
		}
	}
	entry, e := os.Lstat(path)
	if e != nil {
		return nil, e
	}
	if !entry.Mode().IsRegular() || entry.Size() < size {
		return nil, ErrIdentity
	}
	// Pin and exclusively lease the original inode before changing its mode.
	// Reject any sealed/link-before-staging-cleanup crash: never chmod/truncate
	// a published hard-link while recovering its old staging name.
	guard, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	if e = artifactlease.Exclusive(guard); e != nil {
		guard.Close()
		return nil, ErrLeased
	}
	pinned, e := guard.Stat()
	current, pe := os.Lstat(path)
	if e != nil || pe != nil || !os.SameFile(entry, pinned) || !os.SameFile(entry, current) || !artifactlease.SingleLink(pinned) {
		guard.Close()
		return nil, ErrIdentity
	}
	if e = guard.Chmod(0600); e != nil {
		guard.Close()
		return nil, e
	}
	f, e := os.OpenFile(path, os.O_RDWR, 0600)
	guard.Close()
	if e != nil {
		return nil, e
	}
	if e = artifactlease.Exclusive(f); e != nil {
		f.Close()
		return nil, ErrLeased
	}
	info, e := f.Stat()
	current, pe = os.Lstat(path)
	if e != nil || pe != nil || !os.SameFile(info, entry) || !os.SameFile(info, current) || !artifactlease.SingleLink(info) {
		f.Close()
		return nil, ErrIdentity
	}
	if e = f.Truncate(size); e != nil {
		f.Close()
		return nil, e
	}
	h := sha256.New()
	buf := make([]byte, 64<<10)
	remaining := size
	for remaining > 0 {
		if e = ctx.Err(); e != nil {
			f.Close()
			return nil, e
		}
		n, e := io.ReadFull(f, buf[:min(int64(len(buf)), remaining)])
		if n > 0 {
			h.Write(buf[:n])
			remaining -= int64(n)
		}
		if e != nil {
			f.Close()
			return nil, e
		}
	}
	w := &Writer{store: s, file: f, path: path, hash: h, limit: maxBytes, size: size, retained: true}
	s.writers[w] = true
	return w, nil
}

// Checkpoint returns a content identity only after fsync. The caller records the
// matching coverage and byte checkpoint in one guarded DB transaction afterward.
func (w *Writer) Checkpoint() (Object, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.ended {
		return Object{}, ErrState
	}
	if e := w.file.Sync(); e != nil {
		return Object{}, e
	}
	return Object{Digest: hex.EncodeToString(w.hash.Sum(nil)), Size: w.size}, nil
}

// WithClosedBytes holds the writer fence across decoder validation. The producer
// must already be physically retired; no decoder is allowed to reopen a path.
func (w *Writer) WithClosedBytes(ctx context.Context, validate func(io.ReaderAt, int64) error) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.ended || w.size <= 0 || validate == nil {
		return ErrState
	}
	if e := w.file.Sync(); e != nil {
		return e
	}
	if e := ctx.Err(); e != nil {
		return e
	}
	return validate(w.file, w.size)
}

// Retain ends writing without deleting recoverable bytes. Unlike Abort, it is
// used on cancellation, shutdown or an interrupted capture pending reconciliation.
func (w *Writer) Retain() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.ended {
		return nil
	}
	e := w.file.Sync()
	closeErr := w.file.Close()
	w.ended = true
	w.store.mu.Lock()
	delete(w.store.writers, w)
	w.store.mu.Unlock()
	return errors.Join(e, closeErr)
}

// ObjectPath is server-private catalog metadata. It is not a media URL or grant.
func (s *Store) ObjectPath(object Object) (string, error) {
	if !validObject(object) {
		return "", ErrIdentity
	}
	return filepath.Join(s.objects, object.Digest), nil
}
func (s *Store) RetainedExists(key string) bool {
	if !retainedKey.MatchString(key) {
		return false
	}
	st, e := os.Lstat(filepath.Join(s.staging, "capture-"+key))
	return e == nil && st.Mode().IsRegular()
}
func (s *Store) RemoveRetained(key string) error {
	if !retainedKey.MatchString(key) {
		return ErrIdentity
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	path := filepath.Join(s.staging, "capture-"+key)
	for w := range s.writers {
		if w.path == path {
			return ErrLeased
		}
	}
	entry, e := os.Lstat(path)
	if errors.Is(e, os.ErrNotExist) {
		return nil
	}
	if e != nil {
		return e
	}
	if !entry.Mode().IsRegular() {
		return ErrIdentity
	}
	f, e := os.Open(path)
	if errors.Is(e, os.ErrNotExist) {
		return nil
	}
	if e != nil {
		return e
	}
	defer f.Close()
	if e = artifactlease.Exclusive(f); e != nil {
		return ErrLeased
	}
	current, e := os.Lstat(path)
	if errors.Is(e, os.ErrNotExist) {
		return nil
	}
	pinned, pe := f.Stat()
	if e != nil || pe != nil || !os.SameFile(entry, current) || !os.SameFile(entry, pinned) {
		return ErrIdentity
	}
	if e = os.Remove(path); e != nil && !errors.Is(e, os.ErrNotExist) {
		return e
	}
	return syncDirectory(s.staging)
}
func RetainedCaptureKey(id string, generation int64) (string, error) {
	key := fmt.Sprintf("%s.%d", id, generation)
	if !retainedKey.MatchString(key) {
		return "", ErrIdentity
	}
	return key, nil
}

// Unlike disposable transcode staging, retained DVR staging survives every failed
// seal. Keep the physical writer lock through link + directory fsync + unlink.
func (w *Writer) sealRetainedLocked(ctx context.Context) (Object, error) {
	if w.ended {
		return Object{}, ErrState
	}
	if e := ctx.Err(); e != nil {
		return Object{}, e
	}
	if w.failure != nil {
		return Object{}, w.failure
	}
	if w.size <= 0 {
		return Object{}, ErrIdentity
	}
	object := Object{Digest: hex.EncodeToString(w.hash.Sum(nil)), Size: w.size}
	if e := w.file.Chmod(0400); e != nil {
		return Object{}, e
	}
	if e := w.file.Sync(); e != nil {
		return Object{}, e
	}
	s := w.store
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return Object{}, ErrState
	}
	target := filepath.Join(s.objects, object.Digest)
	if e := os.Link(w.path, target); e != nil {
		if !errors.Is(e, os.ErrExist) {
			return Object{}, e
		}
		current, ce := os.Lstat(target)
		own, oe := w.file.Stat()
		if ce != nil || oe != nil {
			return Object{}, ErrIdentity
		}
		if !os.SameFile(current, own) {
			f, e := openVerified(ctx, target, object)
			if e != nil {
				return Object{}, e
			}
			if e = f.Close(); e != nil {
				return Object{}, e
			}
		}
	}
	if e := syncDirectory(s.objects); e != nil {
		return Object{}, e
	}
	if e := ctx.Err(); e != nil {
		return Object{}, e
	}
	if e := os.Remove(w.path); e != nil && !errors.Is(e, os.ErrNotExist) {
		return Object{}, e
	}
	if e := syncDirectory(s.staging); e != nil {
		return Object{}, e
	}
	w.ended = true
	delete(s.writers, w)
	if e := w.file.Close(); e != nil {
		return Object{}, e
	}
	return object, nil
}
