package subtitlevideo

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/mediasource"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
	"github.com/PorticoMediaServer/portico/server/internal/subtitles"
)

// RemoteObject is the structural P04B storage.RemoteObject contract. Keeping this
// local interface lets I04 compile against the immutable baseline; the assembler
// passes RemoteSources.Acquire via a one-line covariant return adapter, not a URL.
type RemoteObject interface {
	Snapshot() storage.Snapshot
	Version() mediasource.Version
	Access() string
	Observe(context.Context) (storage.Snapshot, error)
	OpenRange(context.Context, int64, int64) (io.ReadCloser, error)
	Close() error
}
type RemoteStorageAdapter struct {
	Match   func(string) bool
	Acquire func(context.Context, string, string) (RemoteObject, error)
	// Selected continues a real Select invocation with its retained source
	// admission. Refusal never falls back to a generic path acquisition.
	Selected func(context.Context, string, string, string, int64, int64) (subtitles.RenderInput, error)
	// Analysis never falls through to a generic playback acquisition.
	Analysis func(context.Context, string, string, string, int64, int64) (subtitles.RenderInput, error)
	// Routing discriminator only. Analysis must independently resolve its sealed
	// original invocation; a true result is never byte authority.
	AnalysisInvocation func(context.Context) (analysis, remote bool)
}

func (a RemoteStorageAdapter) Handles(path string) bool { return a.Match != nil && a.Match(path) }
func (a RemoteStorageAdapter) AnalysisSource(ctx context.Context) (analysis, remote bool) {
	if a.AnalysisInvocation == nil {
		return false, false
	}
	return a.AnalysisInvocation(ctx)
}
func (a RemoteStorageAdapter) Open(ctx context.Context, item, source, path string, size, modified int64) (subtitles.RenderInput, error) {
	if analysis, _ := a.AnalysisSource(ctx); analysis {
		if a.Analysis == nil {
			return nil, subtitles.ErrUnavailable
		}
		return a.Analysis(ctx, item, source, path, size, modified)
	}
	if _, selected := subtitles.PlaybackSourceInvocationFromContext(ctx); selected {
		if a.Selected == nil {
			return nil, subtitles.ErrUnavailable
		}
		return a.Selected(ctx, item, source, path, size, modified)
	}
	if a.Acquire == nil {
		return nil, subtitles.ErrUnavailable
	}
	o, e := a.Acquire(ctx, path, "playback")
	if e != nil {
		return nil, e
	}
	good := false
	defer func() {
		if !good {
			o.Close()
		}
	}()
	snap, e := o.Observe(ctx)
	if e != nil {
		return nil, e
	}
	if snap.Directory || snap.Size < 1 || size >= 0 && snap.Size != size || (modified != 0 && snap.ModifiedNS != modified) {
		return nil, subtitles.ErrConflict
	}
	if o.Access() != "finite_random" && o.Access() != "finite_sequential" {
		return nil, subtitles.ErrUnsupported
	}
	evidence := "observed:" + identity.Token()
	if o.Version().ID() != "" {
		evidence = "strong:" + o.Version().ID()
	}
	encoded, e := json.Marshal(snap)
	if e != nil {
		return nil, e
	}
	p := &remoteStorageInput{object: o, size: snap.Size, observation: string(encoded), evidence: evidence}
	if e = p.Validate(ctx); e != nil {
		return nil, e
	}
	good = true
	return p, nil
}

type remoteStorageInput struct {
	object                RemoteObject
	size                  int64
	observation, evidence string
	mu                    sync.Mutex
	closed, lost          bool
}

func (p *remoteStorageInput) Size() int64      { return p.size }
func (p *remoteStorageInput) Evidence() string { return p.evidence }
func (p *remoteStorageInput) validate(ctx context.Context) error {
	if p.closed || p.lost {
		return subtitles.ErrUnavailable
	}
	now, e := p.object.Observe(ctx)
	if e != nil {
		return e
	}
	raw, e := json.Marshal(now)
	if e != nil {
		return e
	}
	if string(raw) != p.observation {
		p.lost = true
		return subtitles.ErrConflict
	}
	return nil
}
func (p *remoteStorageInput) Validate(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.validate(ctx)
}
func (p *remoteStorageInput) ReadExtent(ctx context.Context, offset, length int64) ([]byte, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if offset < 0 || length < 1 || length > 1<<20 || offset > p.size-length {
		return nil, subtitles.ErrInput
	}
	if p.closed || p.lost {
		return nil, subtitles.ErrUnavailable
	}
	if !strings.HasPrefix(p.evidence, "strong:") {
		if e := p.validate(ctx); e != nil {
			return nil, e
		}
	}
	body, e := p.object.OpenRange(ctx, offset, length)
	if e != nil {
		return nil, e
	}
	raw, e := io.ReadAll(io.LimitReader(body, length+1))
	closeErr := body.Close()
	if e != nil {
		return nil, e
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if int64(len(raw)) != length {
		p.lost = true
		return nil, subtitles.ErrConflict
	}
	if !strings.HasPrefix(p.evidence, "strong:") {
		if e = p.validate(ctx); e != nil {
			return nil, e
		}
	}
	return raw, nil
}
func (p *remoteStorageInput) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil
	}
	p.closed = true
	return p.object.Close()
}
