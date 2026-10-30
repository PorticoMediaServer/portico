// Package subtitlevideo adapts the existing source, decoder and playback grant
// lifetimes for subtitle renditions. It has no viewer or occurrence authority.
package subtitlevideo

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/childprocess"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/remotemedia"
	"github.com/PorticoMediaServer/portico/server/internal/sourceaccess"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
	"github.com/PorticoMediaServer/portico/server/internal/subtitles"
)

type selectedInput struct {
	binding                                      storage.PlaybackBinding
	bound                                        bool
	selectedSession                              string
	physicalID, incarnation                      string
	configuration                                int64
	item, source, library, path, root, container string
	size, modified, networkRevision              int64
}

func (r *Runtime) selection(ctx context.Context, item, source, session string) (selectedInput, error) {
	var s selectedInput
	s.item = item
	s.source = source
	s.selectedSession = session
	var e error
	if session != "" {
		e = r.db.QueryRowContext(ctx, `SELECT cl.library_id,a.path,CASE WHEN pin.binding='none' THEN COALESCE(physical.root,l.root) ELSE pin.root END,a.container,a.size,a.modified_ns,COALESCE(n.revision,0),COALESCE(physical.id,l.id),COALESCE(physical.incarnation,''),COALESCE(physical.generation,0)
 FROM catalog_entities i JOIN catalog_libraries cl ON cl.id=i.library_id JOIN libraries l ON l.id=cl.library_id
 JOIN catalog_asset_links link ON link.entity_id=i.id JOIN catalog_assets a ON a.id=link.asset_id
 JOIN playback_sessions ps ON ps.id=? AND ps.item_id=i.id AND ps.asset_id=a.token JOIN playback_source_pins pin ON pin.session_id=ps.id
 LEFT JOIN playback_physical_source_pins custody ON custody.session_id=ps.id LEFT JOIN library_sources physical ON physical.id=custody.source_id
 LEFT JOIN library_network_policy n ON n.library_id=cl.library_id WHERE i.public_id=pid_blob(?) AND a.token=? AND a.available=1`, session, item, source).Scan(&s.library, &s.path, &s.root, &s.container, &s.size, &s.modified, &s.networkRevision, &s.physicalID, &s.incarnation, &s.configuration)
	} else {
		e = r.db.QueryRowContext(ctx, `SELECT cl.library_id,a.path,COALESCE(physical.root,l.root),a.container,a.size,a.modified_ns,COALESCE(n.revision,0),COALESCE(physical.id,l.id),COALESCE(physical.incarnation,''),COALESCE(physical.generation,0) FROM catalog_entities i JOIN catalog_libraries cl ON cl.id=i.library_id JOIN libraries l ON l.id=cl.library_id JOIN catalog_asset_links link ON link.entity_id=i.id JOIN catalog_assets a ON a.id=link.asset_id LEFT JOIN inventory_objects obj ON obj.asset_id=a.token AND obj.retired=0 AND obj.source_id IN(SELECT id FROM library_sources WHERE library_id=cl.library_id) LEFT JOIN library_sources physical ON physical.id=obj.source_id LEFT JOIN library_network_policy n ON n.library_id=cl.library_id WHERE i.public_id=pid_blob(?) AND a.token=? AND a.available=1 AND (obj.id IS NULL OR physical.enabled=1 AND obj.root_incarnation=physical.incarnation AND obj.state='available') AND (?='' OR NOT EXISTS(SELECT 1 FROM playback_physical_source_pins WHERE session_id=?) OR EXISTS(SELECT 1 FROM playback_physical_source_pins pin WHERE pin.session_id=? AND pin.source_id=physical.id AND pin.incarnation=physical.incarnation AND pin.configuration_generation=physical.generation)) ORDER BY physical.id LIMIT 1`, item, source, session, session, session).Scan(&s.library, &s.path, &s.root, &s.container, &s.size, &s.modified, &s.networkRevision, &s.physicalID, &s.incarnation, &s.configuration)
	}
	if e != nil {
		return s, e
	}
	if session != "" {
		var ok bool
		e = r.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM playback_sessions ps JOIN playback_source_pins pin ON pin.session_id=ps.id WHERE ps.id=? AND ps.item_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?)) AND ps.asset_id=? AND pin.size=? AND pin.modified_ns=? AND ps.state NOT IN('stopped','ended','failed') AND ps.expires_at>strftime('%Y-%m-%dT%H:%M:%SZ','now'))`, session, item, source, s.size, s.modified).Scan(&ok)
		if e != nil {
			return s, e
		}
		if !ok {
			return s, identity.ErrUnauthorized
		}
		accepted, err := assets.ResolvePlaybackSource(ctx, r.db, session, "")
		if err != nil {
			return s, err
		}
		s.path, s.size, s.modified = accepted.Path, accepted.Size, accepted.ModifiedNS
		if accepted.Binding != nil {
			s.binding, s.bound = *accepted.Binding, true
			s.root = s.binding.Inventory.Root
		}
	}
	return s, nil
}
func (r *Runtime) stillSelected(ctx context.Context, s selectedInput) error {
	now, e := r.selection(ctx, s.item, s.source, s.selectedSession)
	if e != nil {
		return e
	}
	if now != s {
		return subtitles.ErrConflict
	}
	if r.storage.Guard != nil {
		return r.storage.Guard(s.path)
	}
	return nil
}

type localInput struct {
	reader   *storage.ObservedPlaybackReader
	observed storage.ObservedFileObservation
	source   selectedInput
	runtime  *Runtime
	evidence string
	cancel   context.CancelFunc
	mu       sync.Mutex
	closed   bool
	lost     bool
}

func (p *localInput) Evidence() string { return p.evidence }
func (p *localInput) Size() int64      { return p.observed.Size }
func (p *localInput) validate(ctx context.Context) error {
	if p.closed || p.lost {
		return subtitles.ErrUnavailable
	}
	if e := p.runtime.stillSelected(ctx, p.source); e != nil {
		return e
	}
	o, e := p.reader.Observe(ctx)
	if e != nil {
		return e
	}
	if o != p.observed {
		p.lost = true
		return subtitles.ErrConflict
	}
	return nil
}
func (p *localInput) validateOwned(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.validate(ctx)
}
func (p *localInput) readOwned(ctx context.Context, offset, length int64) ([]byte, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if offset < 0 || length < 1 || length > 1<<20 || offset > p.Size()-length {
		return nil, subtitles.ErrInput
	}
	if e := p.validate(ctx); e != nil {
		return nil, e
	}
	data := make([]byte, 0, int(length))
	for int64(len(data)) < length {
		n := min(length-int64(len(data)), int64(storage.ObservedReadLimit))
		b, e := p.reader.ReadExtent(ctx, offset+int64(len(data)), int(n))
		if e != nil {
			return nil, e
		}
		if int64(len(b)) != n {
			return nil, io.ErrUnexpectedEOF
		}
		data = append(data, b...)
	}
	if e := p.validate(ctx); e != nil {
		return nil, e
	}
	return data, nil
}
func (p *localInput) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil
	}
	p.closed = true
	p.cancel()
	return p.reader.Close()
}
func (r *Runtime) local(ctx context.Context, s selectedInput) (subtitles.RenderInput, error) {
	rel, e := filepath.Rel(s.root, s.path)
	if e != nil || !filepath.IsLocal(rel) || rel == "." {
		return nil, subtitles.ErrUnavailable
	}
	life, cancel := context.WithCancel(r.life)
	lease, e := r.roots.Borrow(ctx, sourceaccess.Authority{OwnerID: identity.Token(), RootID: s.physicalID, RootPath: s.root, RelativePath: rel, Revision: fmt.Sprintf("%s:%d:%d", s.source, s.size, s.modified), Lifetime: life, Validate: func(ctx context.Context) error { return r.stillSelected(ctx, s) }})
	if e != nil {
		cancel()
		return nil, e
	}
	var bindings []*storage.PlaybackBinding
	if s.selectedSession != "" {
		var binding *storage.PlaybackBinding
		if s.bound {
			binding = &s.binding
		}
		bindings = append(bindings, binding)
	}
	reader, e := r.storage.OpenObservedPlayback(ctx, lease, bindings...)
	if e != nil {
		lease.Close()
		cancel()
		return nil, e
	}
	observed, e := reader.Observe(ctx)
	if e != nil || !observed.SizeKnown || !observed.ModifiedKnown || observed.Size != s.size || observed.ModifiedNS != s.modified {
		reader.Close()
		cancel()
		return nil, subtitles.ErrConflict
	}
	return &localInput{reader: reader, observed: observed, source: s, runtime: r, evidence: "observed:" + identity.Token(), cancel: cancel}, nil
}

type remoteInput struct {
	descriptor       subtitles.RenderInput
	client           *remotemedia.VersionedClient
	runtime          *Runtime
	source           selectedInput
	digest, evidence string
	mu               sync.Mutex
	closed           bool
}

func (p *remoteInput) Evidence() string { return p.evidence }
func (p *remoteInput) Size() int64      { return p.client.Version().Evidence().Size }
func (p *remoteInput) validate(ctx context.Context) error {
	if p.closed {
		return subtitles.ErrUnavailable
	}
	if e := p.runtime.stillSelected(ctx, p.source); e != nil {
		return e
	}
	if e := p.descriptor.Validate(ctx); e != nil {
		return e
	}
	data, e := p.descriptor.ReadExtent(ctx, 0, p.descriptor.Size())
	if e != nil {
		return e
	}
	if identity.Digest(string(data)) != p.digest {
		return subtitles.ErrConflict
	}
	resp, e := p.client.Open(ctx, http.MethodGet, "bytes=0-0")
	if e != nil {
		return e
	}
	defer resp.Body.Close()
	n, e := io.Copy(io.Discard, io.LimitReader(resp.Body, 2))
	if e == nil && n != 1 {
		return subtitles.ErrConflict
	}
	return e
}
func (p *remoteInput) validateOwned(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.validate(ctx)
}
func (p *remoteInput) readOwned(ctx context.Context, offset, length int64) ([]byte, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if offset < 0 || length < 1 || length > 1<<20 || offset > p.Size()-length {
		return nil, subtitles.ErrInput
	}
	if p.closed {
		return nil, subtitles.ErrUnavailable
	}
	if e := p.runtime.stillSelected(ctx, p.source); e != nil {
		return nil, e
	}
	resp, e := p.client.Open(ctx, http.MethodGet, fmt.Sprintf("bytes=%d-%d", offset, offset+length-1))
	if e != nil {
		return nil, e
	}
	defer resp.Body.Close()
	b, e := io.ReadAll(io.LimitReader(resp.Body, length+1))
	if e != nil {
		return nil, e
	}
	if int64(len(b)) != length {
		return nil, subtitles.ErrConflict
	}
	if e = p.runtime.stillSelected(ctx, p.source); e != nil {
		return nil, e
	}
	return b, nil
}
func (p *remoteInput) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil
	}
	p.closed = true
	p.client.Close()
	return p.descriptor.Close()
}
func (r *Runtime) OpenSubtitleInput(ctx context.Context, item, source, session string) (out subtitles.RenderInput, err error) {
	release, err := childprocess.AcquirePublication(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	defer func() {
		if err != nil || out == nil {
			return
		}
		check, e := childprocess.AcquirePublication(ctx)
		if e != nil {
			out.Close()
			out = nil
			err = e
			return
		}
		check()
	}()

	if invocation, selected := subtitles.PlaybackSourceInvocationFromContext(ctx); selected {
		facts := invocation.Facts()
		if item != facts.ItemID || source != facts.AssetID || session != "" && session != facts.SessionID {
			return nil, subtitles.ErrConflict
		}
		// Extract passes no public session argument. A real selected extraction
		// still retains the exact session and its physical pin, never a new
		// current asset/source selected by path.
		session = facts.SessionID
	}
	s, e := r.selection(ctx, item, source, session)
	if e != nil {
		return nil, e
	}
	// An I02 adapter can provide a retained remote-storage descriptor or direct
	// media input. No path-to-local fallback is attempted for a handled source.
	var local subtitles.RenderInput
	// A positively remote analysis invocation cannot become local merely because
	// its original source disappeared from the adapter's path registry.
	remoteAnalysis := false
	if routing, ok := r.RemoteStorage.(interface {
		AnalysisSource(context.Context) (bool, bool)
	}); ok {
		_, remoteAnalysis = routing.AnalysisSource(ctx)
	}
	if r.RemoteStorage != nil && (remoteAnalysis || r.RemoteStorage.Handles(s.path)) {
		local, e = r.RemoteStorage.Open(ctx, item, source, s.path, s.size, s.modified)
		if e == nil {
			local = &selectedRemoteInput{RenderInput: local, runtime: r, source: s}
		}
	} else {
		local, e = r.local(ctx, s)
	}
	if e != nil {
		return nil, e
	}
	if s.container != "strm" {
		return local, nil
	}
	ok := false
	defer func() {
		if !ok {
			local.Close()
		}
	}()
	if local.Size() < 1 || local.Size() > 65536 {
		return nil, subtitles.ErrCapacity
	}
	data, e := local.ReadExtent(ctx, 0, local.Size())
	if e != nil {
		return nil, e
	}
	locator, e := remotemedia.ParseDescriptor(data)
	if e != nil {
		return nil, e
	}
	policy := remotemedia.Policy{}
	var raw string
	e = r.db.QueryRowContext(ctx, `SELECT approvals_json FROM library_network_policy WHERE library_id=? AND revision=?`, s.library, s.networkRevision).Scan(&raw)
	if errors.Is(e, sql.ErrNoRows) && s.networkRevision == 0 {
		raw = "[]"
		e = nil
	}
	if e != nil {
		return nil, e
	}
	if json.Unmarshal([]byte(raw), &policy.Approvals) != nil {
		return nil, subtitles.ErrInput
	}
	scope := s.library
	object := s.source + ":uri:" + identity.Digest(locator)
	binding, e := remotemedia.ExactLocatorBinding(locator, scope, object)
	if e != nil {
		return nil, e
	}
	client, e := remotemedia.DiscoverVersion(ctx, locator, policy, scope, object, binding)
	if e != nil {
		return nil, e
	}
	digest := identity.Digest(string(data))
	evidence := "remote:" + identity.Digest(client.Version().ID()+":"+digest+":"+strconv.FormatInt(s.networkRevision, 10))
	result := &remoteInput{descriptor: local, client: client, runtime: r, source: s, digest: digest, evidence: evidence}
	if e = result.Validate(ctx); e != nil {
		client.Close()
		return nil, e
	}
	if session != "" {
		var expected string
		if e = r.db.QueryRowContext(ctx, `SELECT evidence FROM subtitle_remote_sessions WHERE session_id=?`, session).Scan(&expected); e != nil || expected != evidence {
			client.Close()
			return nil, subtitles.ErrConflict
		}
	}
	ok = true
	return result, nil
}

// RemoteStorageInput is the deliberately narrow I02 source seam. Inputs must
// retain the exact RemoteObject/version or observed acquisition; URL strings,
// guessed ETags and reopening a changed source do not satisfy this interface.
type RemoteStorageInput interface {
	Handles(string) bool
	Open(context.Context, string, string, string, int64, int64) (subtitles.RenderInput, error)
}

func persistentEvidence(input subtitles.RenderInput) string {
	e := input.Evidence()
	if strings.HasPrefix(e, "remote:") || strings.HasPrefix(e, "strong:") {
		return e
	}
	return ""
}

type selectedRemoteInput struct {
	subtitles.RenderInput
	runtime *Runtime
	source  selectedInput
}

func (p *selectedRemoteInput) validateOwned(ctx context.Context) error {
	if e := p.runtime.stillSelected(ctx, p.source); e != nil {
		return e
	}
	return p.RenderInput.Validate(ctx)
}
func (p *selectedRemoteInput) readOwned(ctx context.Context, offset, length int64) ([]byte, error) {
	if e := p.runtime.stillSelected(ctx, p.source); e != nil {
		return nil, e
	}
	return p.RenderInput.ReadExtent(ctx, offset, length)
}
