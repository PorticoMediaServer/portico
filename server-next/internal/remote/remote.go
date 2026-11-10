// Package remote is what the server knows about storage it reaches over the network and
// never mounts: a WebDAV address, an rclone remote, Google Drive. Each kind implements
// Storage. Everything the server asks of one source, scans and playback together, goes
// through that source's Throttle, because the real provider behind a WebDAV address or an
// rclone remote cannot be known and most of them count requests.
package remote

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"
)

// Entry is one name in a folder listing.
type Entry struct {
	Name       string
	Dir        bool
	Size       int64
	ModifiedNS int64
}

// File is an open remote file, read by range.
type File interface {
	io.ReaderAt
	io.Closer
	Size() int64
}

// Storage is one remote source. Paths are relative to the source, with forward slashes; ""
// is its top. An implementation answers a provider's "slow down" with a *SlowDown error and
// leaves the waiting and the asking again to Throttle.
type Storage interface {
	// List returns everything in a folder with one request: names, sizes and dates. It
	// returns a whole listing or an error, never part of one.
	List(ctx context.Context, dir string) ([]Entry, error)
	// Open opens a file for reading parts of it. Each ReadAt is one ranged request.
	Open(ctx context.Context, path string) (File, error)
}

// SlowDown is a provider saying it was asked too much. After is how long it said to wait, or
// 0 when it named no time.
type SlowDown struct{ After time.Duration }

func (s *SlowDown) Error() string { return "the remote source asked to be given a rest" }

// Limits are a source's own limits, set by its preset and editable by the owner.
type Limits struct {
	Connections int     // requests in flight at once
	PerSecond   float64 // requests started a second
}

// Preset is one of the four plain choices a source is given: how often it is scanned by
// itself, and its limits. How much of each file a scan reads is the library's scan depth,
// where the lower of the two wins.
type Preset struct {
	Name        string
	RescanEvery time.Duration // 0 is never: only when the owner asks
	Limits      Limits
}

// The presets, by the name stored with the source. "read-once" is the default.
var Presets = []Preset{
	{"list-only", 24 * time.Hour, Limits{2, 2}},
	{"read-once", 6 * time.Hour, Limits{4, 4}},
	{"everything", time.Hour, Limits{8, 10}},
	{"on-request", 0, Limits{2, 2}},
}

// Due reports whether a source scanned by itself every so often is due again. A local disk
// is listed on every scan; a remote source is not, because every folder is a request against
// its provider's allowance. Only a scan that started by itself asks this; one the owner asked
// for lists the source whatever the time.
func Due(lastScan time.Time, every time.Duration, now time.Time) bool {
	return every > 0 && (lastScan.IsZero() || !now.Before(lastScan.Add(every)))
}

// Throttle holds one source to its limits and makes it wait when its provider says to. One
// Throttle serves every use of the source.
type Throttle struct {
	slots chan struct{}
	every time.Duration

	mu      sync.Mutex
	next    time.Time     // when the next request may start
	backoff time.Duration // the wait used the last time the provider named none
	until   time.Time     // the provider asked for a rest until then
}

// NewThrottle makes a throttle. Limits of zero or less become one connection and one request
// a second, which no provider refuses.
func NewThrottle(l Limits) *Throttle {
	if l.Connections < 1 {
		l.Connections = 1
	}
	if l.PerSecond <= 0 {
		l.PerSecond = 1
	}
	return &Throttle{slots: make(chan struct{}, l.Connections), every: time.Duration(float64(time.Second) / l.PerSecond)}
}

// Waiting reports whether the provider asked for a rest that is not over, and until when:
// the source's page says "Waiting".
func (t *Throttle) Waiting() (time.Time, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.until, time.Now().Before(t.until)
}

// Do runs one request within the limits. When the request answers with a *SlowDown, Do waits
// as long as the provider asked (a minute, doubling to an hour, when it named no time) and
// asks again, so the caller carries on where it stopped. It gives up only when ctx ends.
func (t *Throttle) Do(ctx context.Context, request func() error) error {
	for {
		if err := t.turn(ctx); err != nil {
			return err
		}
		err := request()
		<-t.slots
		var slow *SlowDown
		if !errors.As(err, &slow) {
			if err == nil {
				t.mu.Lock()
				t.backoff = 0
				t.mu.Unlock()
			}
			return err
		}
		t.rest(slow.After)
	}
}

// turn waits for a free connection and for this request's place in the rate.
func (t *Throttle) turn(ctx context.Context) error {
	select {
	case t.slots <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	t.mu.Lock()
	now := time.Now()
	at := t.next
	if at.Before(t.until) {
		at = t.until
	}
	if at.Before(now) {
		at = now
	}
	t.next = at.Add(t.every)
	t.mu.Unlock()
	if wait := at.Sub(now); wait > 0 {
		timer := time.NewTimer(wait)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			<-t.slots
			return ctx.Err()
		}
	}
	return nil
}

// rest records that the provider asked for a rest.
func (t *Throttle) rest(after time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if after <= 0 {
		if t.backoff = 2 * t.backoff; t.backoff < time.Minute {
			t.backoff = time.Minute
		}
		if t.backoff > time.Hour {
			t.backoff = time.Hour
		}
		after = t.backoff
	}
	if until := time.Now().Add(after); until.After(t.until) {
		t.until = until
	}
}

// Throttled is a Storage whose every request goes through a throttle.
func Throttled(s Storage, t *Throttle) Storage { return throttled{s, t} }

type throttled struct {
	s Storage
	t *Throttle
}

func (s throttled) List(ctx context.Context, dir string) (entries []Entry, err error) {
	err = s.t.Do(ctx, func() error {
		entries, err = s.s.List(ctx, dir)
		return err
	})
	return entries, err
}

func (s throttled) Open(ctx context.Context, path string) (file File, err error) {
	err = s.t.Do(ctx, func() error {
		file, err = s.s.Open(ctx, path)
		return err
	})
	if err != nil {
		return nil, err
	}
	return &blockFile{File: throttledFile{file, s.t, ctx}}, nil
}

// blockSize is how much one request reads of a file for a small read. Media tools read in
// pieces of a few kilobytes; asked for one by one they would spend a source's requests a
// second on a file's first megabytes.
const blockSize = 1 << 20

// blockFile answers small reads from the last few blocks it read, each block one request.
// A read of a block or more is asked for as it is.
type blockFile struct {
	File
	mu     sync.Mutex
	blocks [4]struct {
		at   int64
		data []byte
	}
	next int
}

func (f *blockFile) ReadAt(p []byte, off int64) (int, error) {
	size := f.Size()
	if off < 0 || len(p) >= blockSize {
		return f.File.ReadAt(p, off)
	}
	n := 0
	for n < len(p) && off+int64(n) < size {
		at := (off + int64(n)) / blockSize * blockSize
		data, err := f.block(at, size)
		if err != nil {
			return n, err
		}
		n += copy(p[n:], data[off+int64(n)-at:])
	}
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

func (f *blockFile) block(at, size int64) ([]byte, error) {
	f.mu.Lock()
	for _, b := range f.blocks {
		if b.data != nil && b.at == at {
			f.mu.Unlock()
			return b.data, nil
		}
	}
	f.mu.Unlock()
	data := make([]byte, min(blockSize, size-at))
	n, err := f.File.ReadAt(data, at)
	if err != nil && !(errors.Is(err, io.EOF) && n == len(data)) {
		return nil, err
	}
	f.mu.Lock()
	f.blocks[f.next].at, f.blocks[f.next].data = at, data
	f.next = (f.next + 1) % len(f.blocks)
	f.mu.Unlock()
	return data, nil
}

// throttledFile makes each read of an open file a request of its own. The file lives as long
// as the work that opened it, so its reads end with that work's context.
type throttledFile struct {
	File
	t   *Throttle
	ctx context.Context
}

func (f throttledFile) ReadAt(p []byte, off int64) (n int, err error) {
	err = f.t.Do(f.ctx, func() error {
		n, err = f.File.ReadAt(p, off)
		return err
	})
	return n, err
}
