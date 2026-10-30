package playback

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/mediasource"
	"github.com/PorticoMediaServer/portico/server/internal/remotemedia"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
	"sync"
	"time"
)

func adapterError(err error) error {
	if errors.Is(err, storage.ErrRemoteChanged) {
		return errors.Join(mediasource.ErrSourceChanged, err)
	}
	return err
}

// descriptorCapture owns exactly one retained acquisition. Its digest describes
// access configuration only; it is never substituted for remote media identity.
type descriptorCapture struct {
	local            *storage.ObservedPlaybackReader
	object           storage.RemoteObject
	root, id, digest string
	baseline         SourceObservation
	version          mediasource.Version
	sequence         int64
	mu               sync.Mutex
	once             sync.Once
}

func (d *descriptorCapture) Close() error {
	d.once.Do(func() {
		if d.local != nil {
			d.local.Close()
		}
		if d.object != nil {
			d.object.Close()
		}
	})
	return nil
}
func (d *descriptorCapture) observe(ctx context.Context) (SourceObservation, error) {
	if d.local != nil {
		o, err := d.local.Observe(ctx)
		return SourceObservation{RootBindingID: o.RootBindingID, ObjectBindingID: o.ObjectBindingID, Size: KnownInt64{o.SizeKnown, o.Size}, ModifiedNS: KnownInt64{o.ModifiedKnown, o.ModifiedNS}, ChangeToken: o.ChangeToken}, mapObservedStorageError(err)
	}
	o, err := d.object.Observe(ctx)
	return SourceObservation{RootBindingID: d.root, ObjectBindingID: o.ObjectID, Size: KnownInt64{true, o.Size}, ModifiedNS: KnownInt64{o.ModifiedNS != 0, o.ModifiedNS}, ChangeToken: o.Revision}, adapterError(err)
}
func (d *descriptorCapture) read(ctx context.Context, size int64) ([]byte, error) {
	if size < 1 || size > 64<<10 {
		return nil, remotemedia.ErrPolicy
	}
	if d.local != nil {
		return d.local.ReadExtent(ctx, 0, int(size))
	}
	r, err := d.object.OpenRange(ctx, 0, size)
	if err != nil {
		return nil, adapterError(err)
	}
	defer r.Close()
	raw, err := io.ReadAll(io.LimitReader(r, size+1))
	if err == nil && int64(len(raw)) != size {
		err = mediasource.ErrSourceChanged
	}
	return raw, err
}
func (d *descriptorCapture) capture(ctx context.Context) (string, error) {
	before, err := d.observe(ctx)
	if err != nil {
		return "", err
	}
	if !before.Size.Known {
		return "", remotemedia.ErrPolicy
	}
	raw, err := d.read(ctx, before.Size.Value)
	if err != nil {
		return "", err
	}
	defer clear(raw)
	after, err := d.observe(ctx)
	if err != nil {
		return "", err
	}
	if observationsConflict(before, after) {
		return "", mediasource.ErrSourceChanged
	}
	locator, err := remotemedia.ParseDescriptor(raw)
	if err != nil {
		return "", err
	}
	d.id = identity.Token()
	d.baseline = after
	d.digest = identity.Digest(string(raw))
	d.sequence = 1
	if d.object != nil {
		d.version = d.object.Version()
	}
	return locator, nil
}
func (d *descriptorCapture) dependencies(ctx context.Context) ([]PreparedInputDependency, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	current, err := d.observe(ctx)
	if err != nil {
		return nil, err
	}
	if observationsConflict(d.baseline, current) {
		return nil, mediasource.ErrSourceChanged
	}
	raw, err := d.read(ctx, d.baseline.Size.Value)
	if err != nil {
		return nil, err
	}
	same := identity.Digest(string(raw)) == d.digest
	clear(raw)
	if !same {
		return nil, mediasource.ErrSourceChanged
	}
	after, err := d.observe(ctx)
	if err != nil {
		return nil, err
	}
	if observationsConflict(d.baseline, after) {
		return nil, mediasource.ErrSourceChanged
	}
	d.sequence++
	dep := PreparedInputDependency{ID: "access:" + d.id, Role: "access_configuration", Digest: d.digest}
	if d.version.ID() != "" {
		ref := SourceReference{StrongSourceReference, d.version.ID()}
		dep.Reference = InputDependencyReference{ref.Kind, ref.ID}
		dep.Version = d.version
		dep.Provenance = &InputSourceEvidence{Reference: ref}
	} else {
		ref := SourceReference{ObservedSourceReference, d.id}
		dep.Reference = InputDependencyReference{ref.Kind, ref.ID}
		dep.Provenance = &InputSourceEvidence{Reference: ref, ObservationInterval: &ObservationInterval{1, d.sequence}}
		dep.Observation = &ObservedInputSnapshot{AcquisitionID: d.id, Sequence: d.sequence, Continuity: "intact", Observation: after}
	}
	return []PreparedInputDependency{dep}, nil
}

type strmObject struct {
	client     *remotemedia.VersionedClient
	descriptor *descriptorCapture
	check      func(context.Context) error
	life       context.Context
	cancel     context.CancelFunc
	stop       func() bool
	once       sync.Once
}

func newSTRMObject(owner context.Context, client *remotemedia.VersionedClient, descriptor *descriptorCapture, check func(context.Context) error) *strmObject {
	life, cancel := context.WithCancel(owner)
	o := &strmObject{client: client, descriptor: descriptor, check: check, life: life, cancel: cancel}
	// The callback never reads stop while it is being installed.
	o.stop = context.AfterFunc(life, func() { client.Close(); descriptor.Close() })
	return o
}

type strmRangeBody struct {
	io.ReadCloser
	release func()
	once    sync.Once
}

func (b *strmRangeBody) Close() error { err := b.ReadCloser.Close(); b.once.Do(b.release); return err }
func (o *strmObject) Snapshot() storage.Snapshot {
	v := o.client.Version()
	return storage.Snapshot{Size: v.Evidence().Size, ObjectID: identity.Digest(v.Evidence().Object), Revision: v.ID()}
}
func (o *strmObject) Version() mediasource.Version { return o.client.Version() }
func (o *strmObject) Access() string               { return "finite_random" }
func (o *strmObject) Observe(ctx context.Context) (storage.Snapshot, error) {
	r, err := o.OpenRange(ctx, 0, 1)
	if err != nil {
		return storage.Snapshot{}, err
	}
	_, err = io.Copy(io.Discard, r)
	e := r.Close()
	if err == nil {
		err = e
	}
	return o.Snapshot(), err
}
func (o *strmObject) OpenRange(parent context.Context, offset, length int64) (io.ReadCloser, error) {
	if o.life.Err() != nil {
		return nil, mediasource.ErrSourceChanged
	}
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	stop := context.AfterFunc(o.life, cancel)
	release := func() { stop(); cancel() }
	retained := false
	defer func() {
		if !retained {
			release()
		}
	}()
	if offset < 0 || length < 1 || offset > o.Snapshot().Size || length > o.Snapshot().Size-offset {
		return nil, remotemedia.ErrRepresentationRange
	}
	if o.check != nil {
		if err := o.check(ctx); err != nil {
			return nil, err
		}
	}
	if _, err := o.descriptor.dependencies(ctx); err != nil {
		return nil, err
	}
	response, err := o.client.Open(ctx, http.MethodGet, fmt.Sprintf("bytes=%d-%d", offset, offset+length-1))
	if err != nil {
		return nil, err
	}
	retained = true
	return &strmRangeBody{ReadCloser: response.Body, release: release}, nil
}
func (o *strmObject) Close() error {
	o.once.Do(func() {
		o.cancel()
		if o.stop != nil {
			o.stop()
		}
		o.client.Close()
		o.descriptor.Close()
	})
	return nil
}
