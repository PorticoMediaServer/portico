// Package producerinput retains immutable source overlaps for one uncommitted
// production job. Its loopback endpoint is never a public viewer media route.
package producerinput

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/mediaartifact"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
	"github.com/PorticoMediaServer/portico/server/internal/privateoutput"
	"github.com/PorticoMediaServer/portico/server/internal/supervise"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const chunkBytes int64 = 1 << 20

var ErrInput = errors.New("producer input unavailable")
var ErrRange = errors.New("producer input range invalid")

// BorrowedInput is satisfied by coordinator PreparationInputBorrow. Its
// error-bearing Metadata method deliberately excludes an escaped raw PreparedInput.
type BorrowedInput interface {
	Metadata() (playback.PreparedInputMetadata, error)
	Validate(context.Context) (playback.InputValidation, error)
	ReadExtent(context.Context, int64, int64) (playback.ObservedExtent, error)
}
type ChunkEvidence struct {
	Offset, Length int64
	Object         mediaartifact.Object
	Source         playback.InputSourceEvidence
}
type Evidence struct {
	Reference playback.SourceReference
	Final     playback.InputValidation
	Chunks    []ChunkEvidence
}
type chunk struct {
	done     chan struct{}
	evidence ChunkEvidence
	err      error
}
type Bridge struct {
	output              *privateoutput.Route
	cleanupError        error
	chunkSize           int64
	mu                  sync.Mutex
	source              BorrowedInput
	reference           playback.SourceReference
	size                int64
	owner, life         context.Context
	cancel              context.CancelFunc
	stopOwner           func() bool
	server              *http.Server
	listener            net.Listener
	path, url, cache    string
	store               *mediaartifact.Store
	chunks              map[int64]*chunk
	acquisition         chan struct{}
	requests            chan struct{}
	handlers            sync.WaitGroup
	servers             sync.WaitGroup
	closing, finalizing bool
	failure             error
	closeOnce           sync.Once
	done                chan struct{}
}

// New must remain inside the coordinator borrow callback until Shutdown. It
// takes ownership of listener even on failure. cacheParent is a trusted
// private generated-cache directory; a fresh job directory prevents shared
// artifact collection from deleting another producer's retained input.
func New(owner context.Context, input BorrowedInput, cacheParent string, listener net.Listener) (*Bridge, error) {
	if input == nil {
		if listener != nil {
			listener.Close()
		}
		return nil, ErrInput
	}
	return newBridge(owner, input, cacheParent, listener, chunkBytes)
}

// NewPair serves one private token and retained chunk set on both canonical
// loopback families. The caller must separately retain endpoint reservations
// through any decoder's actual physical exit. Both listeners transfer ownership.
func NewPair(owner context.Context, input BorrowedInput, cacheParent string, ipv4, ipv6 *net.TCPListener) (*Bridge, error) {
	if ipv4 == nil || ipv6 == nil {
		if ipv4 != nil {
			ipv4.Close()
		}
		if ipv6 != nil {
			ipv6.Close()
		}
		return nil, ErrInput
	}
	return newBridgeSet(owner, input, cacheParent, []net.Listener{ipv4, ipv6}, chunkBytes)
}
func newBridge(owner context.Context, input BorrowedInput, cacheParent string, listener net.Listener, chunkSize int64) (*Bridge, error) {
	return newBridgeSet(owner, input, cacheParent, []net.Listener{listener}, chunkSize)
}
func newBridgeSet(owner context.Context, input BorrowedInput, cacheParent string, listeners []net.Listener, chunkSize int64) (*Bridge, error) {
	adopted := false
	defer func() {
		if !adopted {
			for _, listener := range listeners {
				if listener != nil {
					listener.Close()
				}
			}
		}
	}()
	if len(listeners) < 1 || len(listeners) > 2 {
		return nil, ErrInput
	}
	listener := listeners[0]
	if chunkSize <= 0 || chunkSize > chunkBytes || owner == nil || owner.Err() != nil || input == nil || listener == nil {
		return nil, ErrInput
	}
	addr, ok := listener.Addr().(*net.TCPAddr)
	if !ok || !addr.IP.Equal(net.IPv4(127, 0, 0, 1)) || addr.Port <= 0 {
		return nil, ErrInput
	}
	if len(listeners) == 2 {
		if listeners[1] == nil {
			return nil, ErrInput
		}
		second, ok := listeners[1].Addr().(*net.TCPAddr)
		if !ok || !second.IP.Equal(net.IPv6loopback) || second.IP.To4() != nil || second.Zone != "" || second.Port != addr.Port {
			return nil, ErrInput
		}
	}
	meta, metadataErr := input.Metadata()
	if metadataErr != nil {
		return nil, metadataErr
	}
	if (meta.Reference.Kind != playback.ObservedSourceReference && meta.Reference.Kind != playback.StrongSourceReference) || meta.Reference.ID == "" || !meta.Length.Known || meta.Length.Value <= 0 {
		return nil, ErrInput
	}
	if !filepath.IsAbs(cacheParent) {
		return nil, ErrInput
	}
	canonical, err := filepath.EvalSymlinks(cacheParent)
	if err != nil || canonical != cacheParent {
		return nil, ErrInput
	}
	info, err := os.Lstat(cacheParent)
	if err != nil || !info.IsDir() {
		return nil, ErrInput
	}
	cache, err := os.MkdirTemp(cacheParent, "producer-input-")
	if err != nil {
		return nil, err
	}
	store, err := mediaartifact.New(cache)
	if err != nil {
		os.RemoveAll(cache)
		return nil, err
	}
	var secret [32]byte
	if _, err = rand.Read(secret[:]); err != nil {
		store.Close()
		os.RemoveAll(cache)
		return nil, err
	}
	life, cancel := context.WithCancel(context.Background())
	b := &Bridge{chunkSize: chunkSize, source: input, reference: meta.Reference, size: meta.Length.Value, owner: owner, life: life, cancel: cancel, listener: listener, cache: cache, store: store, chunks: map[int64]*chunk{}, acquisition: make(chan struct{}, 2), requests: make(chan struct{}, 8), done: make(chan struct{})}
	b.servers.Add(len(listeners))
	b.path = "/input/" + hex.EncodeToString(secret[:])
	b.url = "http://" + addr.String() + b.path
	b.server = &http.Server{Handler: b, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 5 * time.Second, MaxHeaderBytes: 4096, BaseContext: func(net.Listener) context.Context { return life }}
	b.mu.Lock()
	b.stopOwner = context.AfterFunc(owner, func() { b.Close() })
	b.mu.Unlock()
	adopted = true
	for _, endpoint := range listeners {
		served := endpoint
		supervise.Go("producerinput.bridge.serve", func() {
			defer b.servers.Done()
			if err := b.server.Serve(served); err != nil && !errors.Is(err, http.ErrServerClosed) {
				b.fail(err)
			}
		})
	}
	if owner.Err() != nil {
		b.Close()
		return nil, ErrInput
	}
	return b, nil
}

// URL is a private capability for the owned producer. Never log or publish it.
func (b *Bridge) URL() string           { return b.url }
func (b *Bridge) Done() <-chan struct{} { return b.done }
func (b *Bridge) active() error {
	if b.owner.Err() != nil || b.life.Err() != nil {
		return ErrInput
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closing || b.failure != nil {
		return ErrInput
	}
	return nil
}
func (b *Bridge) fail(err error) {
	b.mu.Lock()
	if b.failure == nil {
		b.failure = err
	}
	b.mu.Unlock()
	b.Close()
}
func (b *Bridge) Close() error {
	b.closeOnce.Do(func() {
		b.mu.Lock()
		b.closing = true
		if b.stopOwner != nil {
			b.stopOwner()
		}
		b.mu.Unlock()
		b.cancel()
		b.server.Close()
		// Actual handlers own their cache descriptors until they exit. Cancellation
		// never acknowledges that physical IO has finished or deletes its storage.
		supervise.Go("producerinput.bridge.drain", func() {
			b.servers.Wait()
			b.handlers.Wait()
			err := errors.Join(b.store.Close(), os.RemoveAll(b.cache))
			b.mu.Lock()
			b.cleanupError = err
			b.mu.Unlock()
			close(b.done)
		})
	})
	return nil
}
func (b *Bridge) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/output/") {
		b.mu.Lock()
		out := b.output
		if out == nil || b.closing || b.finalizing || b.owner.Err() != nil {
			b.mu.Unlock()
			http.Error(w, "output retired", 410)
			return
		}
		b.handlers.Add(1)
		b.mu.Unlock()
		defer b.handlers.Done()
		select {
		case b.requests <- struct{}{}:
			defer func() { <-b.requests }()
		default:
			http.Error(w, "output busy", 503)
			return
		}
		out.ServeHTTP(w, r)
		return
	}

	if subtle.ConstantTimeCompare([]byte(r.URL.Path), []byte(b.path)) != 1 || r.URL.RawQuery != "" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", 405)
		return
	}
	if len(r.Header.Values("Range")) > 1 || len(r.Header.Get("Range")) > 128 {
		http.Error(w, "range invalid", 416)
		return
	}
	b.mu.Lock()
	if b.closing || b.finalizing || b.owner.Err() != nil {
		b.mu.Unlock()
		http.Error(w, "input unavailable", 503)
		return
	}
	b.handlers.Add(1)
	b.mu.Unlock()
	defer b.handlers.Done()
	select {
	case b.requests <- struct{}{}:
		defer func() { <-b.requests }()
	default:
		http.Error(w, "input busy", 503)
		return
	}
	start, length, partial, err := parseRange(r.Header.Get("Range"), b.size)
	if err != nil {
		w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", b.size))
		http.Error(w, "range invalid", 416)
		return
	}
	validation, err := b.source.Validate(r.Context())
	if err != nil || validation.Evidence.Reference != b.reference || b.active() != nil {
		if r.Context().Err() == nil {
			b.fail(ErrInput)
		}
		http.Error(w, "input unavailable", 503)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Length", strconv.FormatInt(length, 10))
	status := http.StatusOK
	if partial {
		status = http.StatusPartialContent
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, start+length-1, b.size))
	}
	if r.Method == http.MethodHead {
		w.WriteHeader(status)
		return
	}
	// Acquire the first closed chunk before committing successful response headers.
	first, err := b.obtain(r.Context(), start/b.chunkSize)
	if err != nil {
		w.Header().Del("Content-Length")
		http.Error(w, "input unavailable", 503)
		return
	}
	w.WriteHeader(status)
	end := start + length
	for position := start; position < end; {
		index := position / b.chunkSize
		entry := first
		if index != start/b.chunkSize {
			entry, err = b.obtain(r.Context(), index)
			if err != nil {
				panic(http.ErrAbortHandler)
			}
		}
		if b.active() != nil {
			panic(http.ErrAbortHandler)
		}
		reader, e := b.store.Open(r.Context(), entry.Object)
		if e != nil {
			if r.Context().Err() == nil {
				b.fail(e)
			}
			panic(http.ErrAbortHandler)
		}
		offset := position - entry.Offset
		n := min(end-position, entry.Length-offset)
		_, e = io.CopyN(w, io.NewSectionReader(reader, offset, n), n)
		reader.Close()
		if e != nil {
			if r.Context().Err() == nil {
				b.fail(e)
			}
			return
		}
		position += n
	}
}
func parseRange(raw string, size int64) (start, length int64, partial bool, err error) {
	if raw == "" {
		return 0, size, false, nil
	}
	if !strings.HasPrefix(raw, "bytes=") || strings.Contains(raw, ",") {
		return 0, 0, false, ErrRange
	}
	values := strings.Split(strings.TrimPrefix(raw, "bytes="), "-")
	if len(values) != 2 {
		return 0, 0, false, ErrRange
	}
	parse := func(value string) (int64, error) {
		if value == "" || strings.IndexFunc(value, func(r rune) bool { return r < '0' || r > '9' }) >= 0 {
			return 0, ErrRange
		}
		return strconv.ParseInt(value, 10, 64)
	}
	if values[0] == "" {
		suffix, e := parse(values[1])
		if e != nil || suffix <= 0 {
			return 0, 0, false, ErrRange
		}
		suffix = min(suffix, size)
		return size - suffix, suffix, true, nil
	}
	first, e := parse(values[0])
	if e != nil || first >= size {
		return 0, 0, false, ErrRange
	}
	last := size - 1
	if values[1] != "" {
		last, e = parse(values[1])
		if e != nil || last < first {
			return 0, 0, false, ErrRange
		}
		last = min(last, size-1)
	}
	return first, last - first + 1, true, nil
}
func (b *Bridge) obtain(ctx context.Context, index int64) (ChunkEvidence, error) {
	b.mu.Lock()
	entry := b.chunks[index]
	owner := entry == nil
	if owner {
		entry = &chunk{done: make(chan struct{})}
		b.chunks[index] = entry
	}
	b.mu.Unlock()
	if owner {
		// The admitted handler owns acquisition through actual completion. Request
		// disconnect alone cannot discard/replace an overlap another waiter needs.
		entry.evidence, entry.err = b.acquire(index)
		close(entry.done)
		if entry.err != nil {
			b.fail(entry.err)
		}
	}
	select {
	case <-entry.done:
		if entry.err != nil || b.active() != nil {
			return ChunkEvidence{}, ErrInput
		}
		return entry.evidence, nil
	case <-ctx.Done():
		return ChunkEvidence{}, ctx.Err()
	case <-b.life.Done():
		return ChunkEvidence{}, ErrInput
	}
}
func (b *Bridge) acquire(index int64) (ChunkEvidence, error) {
	if b.active() != nil || index < 0 || index > (b.size-1)/b.chunkSize {
		return ChunkEvidence{}, ErrInput
	}
	select {
	case b.acquisition <- struct{}{}:
		defer func() { <-b.acquisition }()
	case <-b.life.Done():
		return ChunkEvidence{}, ErrInput
	}
	offset := index * b.chunkSize
	length := min(b.chunkSize, b.size-offset)
	ctx, cancel := context.WithTimeout(b.life, 30*time.Second)
	defer cancel()
	extent, err := b.source.ReadExtent(ctx, offset, length)
	if err != nil {
		return ChunkEvidence{}, err
	}
	if extent.Evidence.Reference != b.reference || !validEvidence(extent.Evidence) || int64(len(extent.Bytes)) != length || b.active() != nil {
		return ChunkEvidence{}, ErrInput
	}
	writer, err := b.store.Begin(b.chunkSize)
	if err != nil {
		return ChunkEvidence{}, err
	}
	defer writer.Abort()
	if _, err = writer.Write(extent.Bytes); err != nil {
		return ChunkEvidence{}, err
	}
	object, err := writer.Seal(ctx)
	if err != nil {
		return ChunkEvidence{}, err
	}
	if extent.Evidence.ObservationInterval != nil {
		interval := *extent.Evidence.ObservationInterval
		extent.Evidence.ObservationInterval = &interval
	}
	return ChunkEvidence{Offset: offset, Length: length, Object: object, Source: extent.Evidence}, nil
}

// Finalize is called after the actual producer exits successfully. It prevents
// new producer requests, drains existing handlers, then revalidates source
// continuity. Returned closed-chunk provenance must survive output publication.
func (b *Bridge) Finalize(ctx context.Context) (Evidence, error) {
	b.mu.Lock()
	b.finalizing = true
	b.mu.Unlock()
	b.server.Close()
	drained := make(chan struct{})
	supervise.Go("producerinput.bridge.evidence-drain", func() {
		b.servers.Wait()
		b.handlers.Wait()
		close(drained)
	})
	select {
	case <-drained:
	case <-ctx.Done():
		return Evidence{}, ctx.Err()
	}
	if b.active() != nil {
		return Evidence{}, ErrInput
	}
	validation, err := b.source.Validate(ctx)
	if err != nil || validation.Evidence.Reference != b.reference || !validEvidence(validation.Evidence) {
		b.fail(ErrInput)
		return Evidence{}, ErrInput
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.owner.Err() != nil || b.life.Err() != nil || b.closing {
		return Evidence{}, ErrInput
	}
	result := Evidence{Reference: b.reference, Final: validation}
	for _, entry := range b.chunks {
		if entry.err != nil {
			return Evidence{}, ErrInput
		}
		copy := entry.evidence
		if copy.Source.ObservationInterval != nil {
			interval := *copy.Source.ObservationInterval
			copy.Source.ObservationInterval = &interval
		}
		result.Chunks = append(result.Chunks, copy)
	}
	sort.Slice(result.Chunks, func(i, j int) bool { return result.Chunks[i].Offset < result.Chunks[j].Offset })
	return result, nil
}

// Shutdown reports cleanup outcome after actual handlers release their leases.
func (b *Bridge) Shutdown(ctx context.Context) error {
	b.Close()
	select {
	case <-b.done:
		b.mu.Lock()
		defer b.mu.Unlock()
		return b.cleanupError
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Strong conditional bytes have no observed-acquisition interval. Preserve that
// discriminant through the existing immutable chunk bridge rather than inventing
// an observation or downgrading provider evidence.
func validEvidence(e playback.InputSourceEvidence) bool {
	if e.Reference.ID == "" {
		return false
	}
	if e.Reference.Kind == playback.StrongSourceReference {
		return e.ObservationInterval == nil
	}
	return e.Reference.Kind == playback.ObservedSourceReference && e.ObservationInterval != nil && e.ObservationInterval.First > 0 && e.ObservationInterval.Last >= e.ObservationInterval.First
}

// AttachOutput must precede the decoder launch. It cannot replace an existing
// sink; output handlers join the same physical drain as input handlers.
func (b *Bridge) AttachOutput(handler http.Handler) (string, error) {
	out, e := privateoutput.New(handler)
	if e != nil {
		return "", e
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closing || b.finalizing || b.output != nil || b.owner.Err() != nil {
		return "", ErrInput
	}
	b.output = out
	return strings.TrimSuffix(b.url, b.path) + out.Prefix, nil
}
