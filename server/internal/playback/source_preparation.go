package playback

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"github.com/PorticoMediaServer/portico/server/internal/mediasource"
	"github.com/PorticoMediaServer/portico/server/internal/remotemedia"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
	"strconv"
	"strings"
	"sync"
)

// These are private preparation-worker contracts, not public request schemas.
// The domain selects every field under current authorized inventory/policy and
// commits results only after rechecking this fence, source inventory and authority.
type SourcePreparationFence struct {
	PlaybackID, PresentationID                                     string
	OwnershipRevision, DesiredRevision, Generation, PolicyRevision int64
}
type SourcePreparationSelection struct {
	Kind                                                              string // local_file or strm, explicitly selected from source inventory
	ItemID, AssetID, LibraryID, RootID, Path, ExpectedSourceVersionID string
	ExpectedDescriptorVersionID                                       string
	InventorySize, InventoryModifiedNS, NetworkPolicyRevision         int64
}
type SourcePreparationInput struct {
	Fence     SourcePreparationFence
	Selection SourcePreparationSelection
}

// Only byte-identity facts have been established by this stage. Codec/timing facts
// remain unknown until a version-bound probe completes; this is never attachable.
type strongInputMetadata struct {
	Fence                                                     SourcePreparationFence
	AssetID, ItemID, LibraryID, RootID                        string
	Version                                                   mediasource.Version
	Access, FactsStatus                                       string
	Size                                                      int64
	InventorySize, InventoryModifiedNS, NetworkPolicyRevision int64
	DescriptorDigest                                          string              // protected access dependency, not media byte identity
	DescriptorVersion                                         mediasource.Version // local STRM dependency; zero for local media
}

type strongInputPreparer struct {
	local  *storage.Client
	remote *Remote
}

// NewStrongInputPreparer acquires current strong input through actual storage and
// STRM policy adapters. It neither
// queries user intent nor starts a probe/encoder or allocates a viewer-count slot.
func NewStrongInputPreparer(local *storage.Client, remote *Remote) InputPreparer {
	return &strongInputPreparer{local: local, remote: remote}
}

func validSourcePreparation(in SourcePreparationInput) bool {
	f, s := in.Fence, in.Selection
	if f.PlaybackID == "" || f.PresentationID == "" || f.OwnershipRevision <= 0 || f.DesiredRevision <= 0 || f.Generation <= 0 || f.PolicyRevision <= 0 {
		return false
	}
	if s.ItemID == "" || s.AssetID == "" || s.LibraryID == "" || s.RootID == "" || s.Path == "" || s.InventorySize < 0 || s.NetworkPolicyRevision < 0 {
		return false
	}
	return s.Kind == "local_file" || s.Kind == "strm"
}

func (p *strongInputPreparer) PrepareInput(ctx context.Context, in SourcePreparationInput) (*PreparedInput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !validSourcePreparation(in) {
		return nil, mediasource.ErrEvidence
	}
	s := in.Selection
	meta := strongInputMetadata{Fence: in.Fence, AssetID: s.AssetID, ItemID: s.ItemID, LibraryID: s.LibraryID, RootID: s.RootID, Access: "finite_random", FactsStatus: "unknown", InventorySize: s.InventorySize, InventoryModifiedNS: s.InventoryModifiedNS, NetworkPolicyRevision: s.NetworkPolicyRevision}
	var remote *remotemedia.VersionedClient
	var descriptor storage.VersionedDescriptor
	var err error
	if s.Kind == "local_file" {
		if p.local == nil {
			return nil, mediasource.ErrIdentityRequired
		}
		meta.Version, err = p.local.DiscoverPlaybackVersion(ctx, s.Path)
		if err != nil {
			return nil, err
		}
		e := meta.Version.Evidence()
		// local_revision v1 encodes mtime:0:0 (the ctime fields are retired). Comparing the
		// selected inventory snapshot prevents using stale metadata for new bytes.
		parts := strings.Split(e.Revision, ":")
		if len(parts) != 3 {
			return nil, mediasource.ErrEvidence
		}
		modified, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			return nil, mediasource.ErrEvidence
		}
		if e.Scope != s.RootID || e.Size != s.InventorySize || modified != s.InventoryModifiedNS {
			return nil, mediasource.ErrSourceChanged
		}
	} else {
		if p.remote == nil {
			return nil, mediasource.ErrIdentityRequired
		}
		remote, descriptor, err = p.remote.prepareVersionedSource(ctx, s)
		if err != nil {
			return nil, err
		}
		meta.Version = remote.Version()
		meta.DescriptorVersion, meta.DescriptorDigest = descriptor.Version, descriptor.Digest
	}
	if s.ExpectedSourceVersionID != "" && s.ExpectedSourceVersionID != meta.Version.ID() {
		if remote != nil {
			remote.Close()
		}
		return nil, mediasource.ErrSourceChanged
	}
	if err := ctx.Err(); err != nil {
		if remote != nil {
			remote.Close()
		}
		return nil, err
	}
	meta.Size = meta.Version.Evidence().Size
	// The preparation deadline is not the returned resource lifetime. The domain
	// owns Close on supersession/Stop/failed commit; each read also uses its caller's
	// generation-fenced context. No resource access begins merely by retaining this.
	life, cancel := context.WithCancel(context.Background())
	sourceStorage := p.local
	if remote != nil {
		sourceStorage = p.remote.io
	}
	return newStrongPreparedInput(in, &strongInputSource{metadata: meta, path: s.Path, local: sourceStorage, remote: remote, life: life, cancel: cancel})
}

// prepareVersionedSource wires the existing STRM descriptor, library network policy
// and approved transport into the reviewed conditional adapter. Generic STRM gets
// an exact-URI object identity; it cannot infer signed URL equivalence from ETag.
func (r *Remote) prepareVersionedSource(ctx context.Context, s SourcePreparationSelection) (*remotemedia.VersionedClient, storage.VersionedDescriptor, error) {
	if r.io == nil || r.db == nil {
		return nil, storage.VersionedDescriptor{}, mediasource.ErrIdentityRequired
	}
	descriptor, err := r.io.ReadVersionedDescriptor(ctx, s.Path)
	if err != nil {
		return nil, storage.VersionedDescriptor{}, err
	}
	if descriptor.Version.Evidence().Scope != s.RootID || descriptor.Version.Evidence().Size != s.InventorySize || descriptor.ModifiedNS != s.InventoryModifiedNS {
		return nil, storage.VersionedDescriptor{}, mediasource.ErrSourceChanged
	}
	if s.ExpectedDescriptorVersionID != "" && s.ExpectedDescriptorVersionID != descriptor.Version.ID() {
		return nil, storage.VersionedDescriptor{}, mediasource.ErrSourceChanged
	}
	locator, err := remotemedia.ParseDescriptor([]byte(descriptor.Data))
	if err != nil {
		return nil, storage.VersionedDescriptor{}, err
	}
	var revision int64
	var raw string
	err = r.db.QueryRowContext(ctx, `SELECT revision,approvals_json FROM library_network_policy WHERE library_id=?`, s.LibraryID).Scan(&revision, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		revision, raw, err = 0, "[]", nil
	}
	if err != nil {
		return nil, storage.VersionedDescriptor{}, err
	}
	if revision != s.NetworkPolicyRevision {
		return nil, storage.VersionedDescriptor{}, ErrSourcePolicyChanged
	}
	policy := remotemedia.Policy{Resolver: r.resolver}
	if err := json.Unmarshal([]byte(raw), &policy.Approvals); err != nil {
		return nil, storage.VersionedDescriptor{}, remotemedia.ErrPolicy
	}
	locatorDigest := sha256.Sum256([]byte(locator))
	object := s.AssetID + ":uri:" + hex.EncodeToString(locatorDigest[:])
	binding, err := remotemedia.ExactLocatorBinding(locator, s.RootID, object)
	if err != nil {
		return nil, storage.VersionedDescriptor{}, err
	}
	client, err := remotemedia.DiscoverVersion(ctx, locator, policy, s.RootID, object, binding)
	if err != nil {
		return nil, storage.VersionedDescriptor{}, err
	}
	return client, descriptor, nil
}

var ErrSourcePolicyChanged = errors.New("source network policy changed during preparation")

// strongInputSource is owned by a presentation preparation/production resource lease,
// not by a viewer credential. It has immutable metadata and no occurrence mutation
// methods. Close cancels outstanding range reads and prevents future acquisition.
type strongInputSource struct {
	backend             storage.RemoteObject
	dependencies        func(context.Context) ([]PreparedInputDependency, error)
	initialDependencies []PreparedInputDependency
	metadata            strongInputMetadata
	path                string
	local               *storage.Client
	remote              *remotemedia.VersionedClient
	life                context.Context
	cancel              context.CancelFunc
	once                sync.Once
}

func (p *strongInputSource) Metadata() strongInputMetadata { return p.metadata }
func (p *strongInputSource) Close() error {
	p.once.Do(func() {
		p.cancel()
		if p.backend != nil {
			p.backend.Close()
		}
		if p.remote != nil {
			p.remote.Close()
		}
	})
	return nil
}
func (p *strongInputSource) readContext(parent context.Context) (context.Context, func(), error) {
	if err := p.life.Err(); err != nil {
		return nil, nil, err
	}
	ctx, cancel := context.WithCancel(parent)
	stop := context.AfterFunc(p.life, cancel)
	return ctx, func() { stop(); cancel() }, nil
}

// OpenRange returns exactly a finite requested extent; callers own Close and must
// pass the current job/media authority context. It does not refresh viewer leases.
func (p *strongInputSource) OpenRange(parent context.Context, offset, length int64) (io.ReadCloser, error) {
	if offset < 0 || length <= 0 || offset > p.metadata.Size || length > p.metadata.Size-offset {
		return nil, remotemedia.ErrRepresentationRange
	}
	ctx, release, err := p.readContext(parent)
	if err != nil {
		return nil, err
	}
	if p.dependencies != nil {
		if _, err := p.dependencies(ctx); err != nil {
			release()
			return nil, err
		}
	}
	if p.backend != nil {
		body, err := p.backend.OpenRange(ctx, offset, length)
		if err != nil {
			release()
			return nil, err
		}
		return &preparedReadCloser{Reader: body, close: func() error { err := body.Close(); release(); return err }}, nil
	}
	if p.remote != nil {
		resp, err := p.remote.Open(ctx, http.MethodGet, fmt.Sprintf("bytes=%d-%d", offset, offset+length-1))
		if err != nil {
			release()
			return nil, err
		}
		return &preparedReadCloser{Reader: resp.Body, close: func() error { err := resp.Body.Close(); release(); return err }}, nil
	}
	reader, err := p.local.OpenVersionedPlayback(ctx, p.path, p.metadata.Version)
	if err != nil {
		release()
		return nil, err
	}
	if _, err := reader.Seek(offset, io.SeekStart); err != nil {
		reader.Close()
		release()
		return nil, err
	}
	return &preparedReadCloser{Reader: io.LimitReader(reader, length), close: func() error { err := reader.Close(); release(); return err }}, nil
}

// OpenDescriptor is local-only. Remote production must use a version-bound bridge
// or stable-copy strategy; it must not reopen the original provider URL directly.
func (p *strongInputSource) OpenDescriptor(ctx context.Context) (*os.File, error) {
	if p.remote != nil || p.backend != nil {
		return nil, mediasource.ErrIdentityRequired
	}
	bound, release, err := p.readContext(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	return p.local.OpenVersionedPlaybackDescriptor(bound, p.path, p.metadata.Version)
}

func (p *strongInputSource) ValidateDescriptor(ctx context.Context, file *os.File) error {
	if p.remote != nil || p.backend != nil {
		return mediasource.ErrIdentityRequired
	}
	bound, release, err := p.readContext(ctx)
	if err != nil {
		return err
	}
	defer release()
	return p.local.ValidatePlaybackVersion(bound, file, p.metadata.Version)
}

// ValidateDescriptorDependency must run before committing or producing from a
// prepared STRM dependency. It verifies the actual descriptor version and bytes;
// it does not replace domain authorization/policy checks or remote conditional
// byte reads. Local media has no separate descriptor dependency.
func (p *strongInputSource) ValidateDescriptorDependency(parent context.Context) error {
	ctx, release, err := p.readContext(parent)
	if err != nil {
		return err
	}
	defer release()
	if p.dependencies != nil {
		_, err := p.dependencies(ctx)
		return err
	}
	if p.remote == nil {
		return nil
	}
	descriptor, err := p.local.ReadVersionedDescriptor(ctx, p.path)
	if err != nil {
		return err
	}
	if err := p.metadata.DescriptorVersion.Match(descriptor.Version.Evidence()); err != nil {
		return err
	}
	if descriptor.Digest != p.metadata.DescriptorDigest {
		return mediasource.ErrSourceChanged
	}
	return nil
}

type preparedReadCloser struct {
	io.Reader
	close func() error
	once  sync.Once
	err   error
}

func (r *preparedReadCloser) Close() error { r.once.Do(func() { r.err = r.close() }); return r.err }
