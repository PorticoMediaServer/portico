package playback

import (
	"context"
	"errors"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/mediasource"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// SourceRootResolver is implemented by the actual root registration owner. It
// verifies current authorized origin/association/path/configuration before lending
// a duplicated real directory FD. A path or catalog revision cannot fabricate it.
type SourceRootResolver func(context.Context, SourcePreparationInput) (*storage.RootLease, error)
type observedLocalInputPreparer struct {
	storage *storage.Client
	resolve SourceRootResolver
}

// NewObservedLocalInputPreparer selects ordinary acquisition before IO. It cannot
// satisfy strict pins and never retries a failed strong attempt as observed.
func NewObservedLocalInputPreparer(c *storage.Client, resolve SourceRootResolver) InputPreparer {
	return &observedLocalInputPreparer{c, resolve}
}
func (p *observedLocalInputPreparer) PrepareInput(ctx context.Context, in SourcePreparationInput) (*PreparedInput, error) {
	if !validSourcePreparation(in) || in.Selection.Kind != "local_file" {
		return nil, ErrPreparedInput
	}
	if in.Selection.ExpectedSourceVersionID != "" || in.Selection.ExpectedDescriptorVersionID != "" {
		return nil, mediasource.ErrSourceChanged
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if p.storage == nil || p.resolve == nil {
		return nil, storage.ErrRootLease
	}
	lease, err := p.resolve(ctx, in)
	if err != nil {
		if lease != nil {
			lease.Close()
		}
		return nil, err
	}
	if lease == nil {
		return nil, storage.ErrRootLease
	}
	// This checks the resolver's returned path association, not authorization or
	// physical containment. The root owner and the actual helper enforce those.
	if !filepath.IsAbs(in.Selection.Path) || filepath.Clean(filepath.Join(lease.RootPath, lease.RelativePath)) != filepath.Clean(in.Selection.Path) {
		lease.Close()
		return nil, storage.ErrRootLease
	}
	reader, err := p.storage.OpenObservedPlayback(ctx, lease)
	if err != nil {
		return nil, err
	}
	return newObservedInput(ctx, in, "finite_random", &observedLocalDriver{reader: reader})
}

type observedLocalDriver struct {
	reader *storage.ObservedPlaybackReader
}

func mapObservedStorageError(err error) error {
	if errors.Is(err, storage.ErrObservedSourceLost) || errors.Is(err, storage.ErrRootLease) {
		return errors.Join(ErrObservedContinuityLost, err)
	}
	return err
}
func (d *observedLocalDriver) Observe(ctx context.Context) (SourceObservation, error) {
	o, err := d.reader.Observe(ctx)
	if err != nil {
		return SourceObservation{}, mapObservedStorageError(err)
	}
	return SourceObservation{RootBindingID: o.RootBindingID, ObjectBindingID: o.ObjectBindingID, Size: KnownInt64{o.SizeKnown, o.Size}, ModifiedNS: KnownInt64{o.ModifiedKnown, o.ModifiedNS}, ChangeToken: o.ChangeToken}, nil
}
func (d *observedLocalDriver) Dependencies(ctx context.Context) ([]PreparedInputDependency, error) {
	return nil, ctx.Err()
}
func (d *observedLocalDriver) ReadExtent(ctx context.Context, offset, length int64) ([]byte, error) {
	if length < 1 || length > maxObservedExtent {
		return nil, ErrPreparedInput
	}
	data := make([]byte, 0, int(length))
	for int64(len(data)) < length {
		n := min(length-int64(len(data)), int64(storage.ObservedReadLimit))
		b, err := d.reader.ReadExtent(ctx, offset+int64(len(data)), int(n))
		if err != nil {
			return nil, mapObservedStorageError(err)
		}
		if int64(len(b)) != n {
			return nil, ErrObservedContinuityLost
		}
		data = append(data, b...)
	}
	return data, nil
}
func (d *observedLocalDriver) Close() error { return d.reader.Close() }
