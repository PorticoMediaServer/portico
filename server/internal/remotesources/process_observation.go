package remotesources

import (
	"context"
	"encoding/json"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/rcloneprogram"
	"github.com/PorticoMediaServer/portico/server/internal/remotemedia"
)

// observeProcessFile obtains backend facts and served preconditions separately.
// Neither a generated served ETag nor equal upstream metadata is an immutable
// provider version. Hashes are requested only when rclone reports them cheap.
func observeProcessFile(ctx context.Context, process *rcloneprogram.ServeProcess, object string, check func(context.Context) error) (remotemedia.FileObservation, error) {
	report := process.PacerObservation()
	metadata, err := process.Metadata(ctx)
	if err != nil {
		return remotemedia.FileObservation{}, err
	}
	observer, err := process.ObservationEndpoint(ctx)
	if err != nil {
		return remotemedia.FileObservation{}, err
	}
	fresh, err := metadata.Stat(ctx, object, check)
	if err != nil {
		return remotemedia.FileObservation{}, err
	}
	served, err := observer.Stat(ctx, object, check)
	if err != nil {
		return remotemedia.FileObservation{}, err
	}
	if fresh.Directory || served.Directory || fresh.Size != served.Size {
		return remotemedia.FileObservation{}, remotemedia.ErrObservationChanged
	}
	id, generation := process.Source()
	facts := remotemedia.FileObservation{SourceID: id, Incarnation: generation, Object: object, ObjectID: fresh.ID, Size: fresh.Size, ProviderHashes: fresh.Hashes, ServeETag: served.ETag}
	if !fresh.Modified.IsZero() && time.Unix(0, fresh.Modified.UnixNano()).Equal(fresh.Modified) {
		facts.ModifiedKnown, facts.ModifiedNS = true, fresh.Modified.UnixNano()
	}
	facts, err = facts.Canonical()
	if err == nil {
		process.BackendObserved(report.Sequence)
	}
	return facts, err
}

// observationSnapshot is transport scan evidence. Catalog.ObservationRevision
// separately hashes this snapshot with the library source/root incarnation;
// that catalog object revision must never be confused with this ChangeToken.
func observationSnapshot(b binding, generation string, facts remotemedia.FileObservation) (storage.Snapshot, error) {
	facts, err := facts.Canonical()
	if err != nil || facts.Root != "" || facts.SourceID != b.ID || facts.Incarnation != generation || !validObservationObject(facts.Object) {
		return storage.Snapshot{}, storage.ErrRemoteChanged
	}
	raw, err := json.Marshal(facts)
	if err != nil {
		return storage.Snapshot{}, err
	}
	file := filepath.Join(b.Root, filepath.FromSlash(facts.Object))
	value := storage.Snapshot{Path: file, Name: filepath.Base(file), Size: facts.Size, ModifiedNS: facts.ModifiedNS, ObjectID: facts.ObjectID, Revision: identity.Digest(string(raw))}
	if value.ObjectID == "" {
		value.ObjectID = "path:" + identity.Digest(facts.Object)
	}
	return inventorySnapshot(b, generation, value), nil
}
