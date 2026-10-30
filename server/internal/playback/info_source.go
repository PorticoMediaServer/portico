package playback

// Bounded source-input adoption for the first-play stream-detail refresh.
//
// This file owns the playback side of the normalized source input: one exact
// descriptor-bound playback probe of one catalogue asset, staged as the
// existing stream families (1..3) and accepted through the sole
// compactcatalog.PublishInfoSource owner. Nothing here writes canonical facts,
// drains the publication worker, or opens a transaction of its own.
//
// What the observation proves
//
//   - the exact catalogue asset row (id, token, path, size, modified, container
//     and availability) that the probe read;
//   - the exact physical custody entry: one live, unretired inventory object of
//     an enabled, identity-confirmed local source whose object identity, size,
//     modified time, root incarnation and revision are the observed ones;
//   - a descriptor opened through the same playback binding the session reader
//     uses, so the object identity comes from an acquired handle rather than a
//     pathname lookup; the path is reopened after the probe and must still name
//     the same object;
//   - the probe revision: the asset's current stream detail_version was still
//     below assets.StreamDetailVersion when the probe ran and has not moved.
//
// The provider never claims the refresh completed. PublishInfoSource returns an
// explicit accepted-pending receipt while the accepted body waits for the
// source41 owner, or an existing typed defer (busy/deferred) when the bounded
// slots or the 32 MiB logical ledger refuse the input. Only an applied receipt
// means the technical children are canonical.
//
// Session scope
//
// refreshStreamDetail runs before create() inserts the playback session, so no
// session or source-pin row exists to fence against at this call site. The
// admission is therefore the exact physical observation above, re-proven in
// every staging transaction and at the final acceptance seal; a session-scoped
// refresh would need its own pin witness and is deliberately not invented here.

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// detailRefreshPublishTimeout bounds bounded staging and acceptance after the
// probe. The deadline is terminal for this producer, so PublishInfoSource
// abandons only its own unaccepted body; an accepted input always finishes.
const detailRefreshPublishTimeout = 15 * time.Second

// playbackDetailObservation is one exact immutable physical observation. Every
// field is either read from a point row or observed on an acquired descriptor
// before the probe; none is inferred from a query result set.
type playbackDetailObservation struct {
	AssetID          int64
	Token            string
	Path             string
	Size             int64
	ModifiedNS       int64
	Container        string
	DetailVersion    int
	ObjectID         string
	ObjectRevision   string
	ObjectIdentity   string
	SourceID         string
	SourceGeneration int64
	RootIncarnation  string
	RootIdentity     string
	Root             string
	RelativePath     string
	FollowSymlinks   bool
	// descriptor is the identity observed on a descriptor acquired before the
	// probe. It is not part of the durable witness, but the same object must be
	// acquired again after the probe before any fact is published.
	descriptor string
}

func (o playbackDetailObservation) binding() *storage.PlaybackBinding {
	return &storage.PlaybackBinding{
		Inventory: storage.InventoryRequest{
			Root:           o.Root,
			RootIdentity:   o.RootIdentity,
			RelativePath:   o.RelativePath,
			FollowSymlinks: o.FollowSymlinks,
		},
		ObjectIdentity: o.ObjectIdentity,
	}
}

// acquire opens the exact object behind the observation through the playback
// binding and returns the identity of the acquired descriptor. A file without
// inventory custody, a replaced object or a root that no longer matches the
// recorded evidence is simply not observed.
func (o playbackDetailObservation) acquire() (string, error) {
	file, err := storage.OpenLocalPlaybackFile(o.Path, o.Size, o.ModifiedNS, o.binding())
	if err != nil {
		return "", err
	}
	defer file.Close()
	return storage.FileObjectIdentity(file)
}

// stillAcquired proves the pathname the probe read still resolves to the exact
// descriptor observed before it. The probe is path-based, so this second
// acquisition is the only way to detect a replacement that happened during it.
func (o playbackDetailObservation) stillAcquired() error {
	identity, err := o.acquire()
	if err != nil {
		return err
	}
	if o.descriptor == "" || identity != o.descriptor {
		return storage.ErrPlaybackSource
	}
	return nil
}

// bindDescriptor acquires the exact object behind the observation through the
// playback binding and remembers the descriptor identity. A file without
// inventory custody, a replaced object or a root that no longer matches the
// recorded evidence is simply not observed.
func (o *playbackDetailObservation) bindDescriptor() error {
	identity, err := o.acquire()
	if err != nil {
		return err
	}
	o.descriptor = identity
	return nil
}

// observeDetailRefresh reads the point evidence one refresh speaks for. ok is
// false whenever no exact descriptor-bound observation is available: the file
// is already at the current detail version, is a remote descriptor, is gone or
// unlinked, or has no live inventory custody. That is a truthful no-op, not a
// deferral. No file IO happens here; the caller binds the descriptor after its
// storage guard has accepted the path.
func (s *Service) observeDetailRefresh(ctx context.Context, token string) (playbackDetailObservation, bool, error) {
	var o playbackDetailObservation
	o.Token = token
	var available int
	err := dbwork.QueryRow(ctx, s.db, `SELECT a.id,a.path,a.container,a.size,a.modified_ns,a.available,COALESCE((SELECT f.detail_version FROM asset_stream_facts f WHERE f.asset_id=a.token AND f.size=a.size AND f.modified_ns=a.modified_ns),0)
 FROM catalog_assets a WHERE a.token=?`, token).Scan(&o.AssetID, &o.Path, &o.Container, &o.Size, &o.ModifiedNS, &available, &o.DetailVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return o, false, nil
	}
	if err != nil {
		return o, false, err
	}
	if available != 1 || o.Container == "strm" || o.DetailVersion >= assets.StreamDetailVersion {
		return o, false, nil
	}
	var sourceIncarnation, health, kind string
	var enabled, confirmed int
	var objectSize, objectModified int64
	err = dbwork.QueryRow(ctx, s.db, `SELECT o.id,COALESCE(o.object_identity,''),o.revision,o.root_incarnation,o.size,o.modified_ns,
 s.id,s.generation,s.incarnation,COALESCE(s.root_identity,''),s.root,o.relative_path,s.follow_symlinks,s.enabled,s.identity_confirmed,s.kind,s.health
 FROM inventory_objects o JOIN library_sources s ON s.id=o.source_id
 WHERE o.asset_id=? AND o.size=? AND o.modified_ns=? AND o.retired=0 AND o.state='available'
 ORDER BY o.id LIMIT 1`, token, o.Size, o.ModifiedNS).Scan(
		&o.ObjectID, &o.ObjectIdentity, &o.ObjectRevision, &o.RootIncarnation, &objectSize, &objectModified,
		&o.SourceID, &o.SourceGeneration, &sourceIncarnation, &o.RootIdentity, &o.Root, &o.RelativePath, &o.FollowSymlinks,
		&enabled, &confirmed, &kind, &health)
	if errors.Is(err, sql.ErrNoRows) {
		// No inventory object names this file: there is no exact descriptor
		// custody to witness, so playback leaves the refresh to the scanner.
		return o, false, nil
	}
	if err != nil {
		return o, false, err
	}
	if o.ObjectID == "" || o.ObjectIdentity == "" || o.ObjectRevision == "" || o.SourceID == "" ||
		o.RootIncarnation == "" || o.RootIncarnation != sourceIncarnation || o.RootIdentity == "" ||
		o.RelativePath == "" || o.Root == "" || objectSize != o.Size || objectModified != o.ModifiedNS ||
		kind != "local" || enabled != 1 || confirmed != 1 || health == "root_changed" || health == "removing" {
		return o, false, nil
	}
	return o, true, nil
}

// playbackDetailFenceTx re-proves the observation in every staging transaction
// and at the final acceptance seal. It is point-only, performs no file or
// network IO while the writer is held, and fails closed with the existing
// terminal drift error: a body that was started against this exact observation
// is abandoned by PublishInfoSource, never finished against a moved source.
func playbackDetailFenceTx(ctx context.Context, tx *sql.Tx, o playbackDetailObservation) error {
	drift := func(reason string) error {
		return fmt.Errorf("%w: playback detail refresh: %s", compactcatalog.ErrInfoInputDrift, reason)
	}
	var assetID, size, modified int64
	var path, container string
	var available int
	var detailVersion int
	err := tx.QueryRowContext(ctx, `SELECT a.id,a.path,a.container,a.size,a.modified_ns,a.available,
 COALESCE((SELECT f.detail_version FROM asset_stream_facts f WHERE f.asset_id=a.token AND f.size=a.size AND f.modified_ns=a.modified_ns),0)
 FROM catalog_assets a WHERE a.token=?`, o.Token).Scan(&assetID, &path, &container, &size, &modified, &available, &detailVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return drift("asset is gone")
	}
	if err != nil {
		return err
	}
	if assetID != o.AssetID || path != o.Path || container != o.Container || size != o.Size || modified != o.ModifiedNS {
		return drift("asset stat moved")
	}
	if available != 1 {
		return drift("asset is unavailable")
	}
	var selected int
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_asset_links l JOIN catalog_entities e ON e.id=l.entity_id AND e.retired=0 WHERE l.asset_id=? AND l.available=1)`, o.AssetID).Scan(&selected); err != nil {
		return err
	}
	if selected != 1 {
		return drift("original selection is gone")
	}
	if detailVersion != o.DetailVersion || detailVersion >= assets.StreamDetailVersion {
		return drift("probe revision moved")
	}
	var objectID, objectRevision, objectIdentity, rootIncarnation string
	var sourceID, sourceIncarnation, rootIdentity, root, relative, kind, health string
	var generation int64
	var objectSize, objectModified int64
	var follow, enabled, confirmed int
	err = tx.QueryRowContext(ctx, `SELECT o.id,o.revision,COALESCE(o.object_identity,''),o.root_incarnation,o.size,o.modified_ns,
 s.id,s.generation,s.incarnation,COALESCE(s.root_identity,''),s.root,o.relative_path,s.follow_symlinks,s.enabled,s.identity_confirmed,s.kind,s.health
 FROM inventory_objects o JOIN library_sources s ON s.id=o.source_id
 WHERE o.asset_id=? AND o.size=? AND o.modified_ns=? AND o.retired=0 AND o.state='available'
 ORDER BY o.id LIMIT 1`, o.Token, o.Size, o.ModifiedNS).Scan(
		&objectID, &objectRevision, &objectIdentity, &rootIncarnation, &objectSize, &objectModified,
		&sourceID, &generation, &sourceIncarnation, &rootIdentity, &root, &relative, &follow, &enabled, &confirmed, &kind, &health)
	if errors.Is(err, sql.ErrNoRows) {
		return drift("physical custody is gone")
	}
	if err != nil {
		return err
	}
	if objectID != o.ObjectID || objectRevision != o.ObjectRevision || objectIdentity != o.ObjectIdentity ||
		rootIncarnation != o.RootIncarnation || rootIncarnation != sourceIncarnation || objectSize != o.Size || objectModified != o.ModifiedNS ||
		sourceID != o.SourceID || generation != o.SourceGeneration || rootIdentity != o.RootIdentity ||
		root != o.Root || relative != o.RelativePath || (follow == 1) != o.FollowSymlinks ||
		kind != "local" || enabled != 1 || confirmed != 1 || health == "root_changed" || health == "removing" {
		return drift("physical custody moved")
	}
	return nil
}

// playbackDetailInputFingerprint binds the public facts and every technical
// byte the stream families install. Two probes that differ in any of them are
// different source frames, so an identical retry can coalesce while a hidden
// technical change never collapses into it.
func playbackDetailInputFingerprint(f assets.Facts) ([32]byte, error) {
	raw, err := json.Marshal([]any{f.Container, f.VideoCodec, f.AudioCodec, f.Width, f.Height, f.Duration})
	if err != nil {
		return [32]byte{}, err
	}
	for _, stream := range f.Streams {
		encoded, err := json.Marshal(stream)
		if err != nil {
			return [32]byte{}, err
		}
		breadth, err := json.Marshal(stream.Detail.StreamBreadth)
		if err != nil {
			return [32]byte{}, err
		}
		raw = append(raw, encoded...)
		raw = append(raw, stream.EncodeDetail()...)
		raw = append(raw, breadth...)
	}
	return sha256.Sum256(raw), nil
}

// playbackDetailWitnessFingerprint binds the physical snapshot itself: the
// exact asset stat, the probe revision and the descriptor custody the input
// speaks for. The witness is immutable, so a different snapshot (even of the
// same asset) never merges into this input's family manifests.
func playbackDetailWitnessFingerprint(o playbackDetailObservation, facts [32]byte) ([32]byte, error) {
	raw, err := json.Marshal([]any{
		o.AssetID, o.Token, o.Path, o.Size, o.ModifiedNS, o.Container, o.DetailVersion,
		o.ObjectID, o.ObjectRevision, o.ObjectIdentity, o.SourceID, o.SourceGeneration,
		o.RootIncarnation, o.RootIdentity, o.Root, o.RelativePath, o.FollowSymlinks,
	})
	if err != nil {
		return [32]byte{}, err
	}
	raw = append(raw, facts[:]...)
	return sha256.Sum256(raw), nil
}

// playbackInfoFamilyMask is the same changed-family contract the catalogue
// producers use: a parent-witness family stays outside the mask because the
// phase-2 materializer must not replace canonical parent rows.
func playbackInfoFamilyMask(families []compactcatalog.InfoSourceFamily) int64 {
	var mask int64
	for _, family := range families {
		if family.Parent {
			continue
		}
		mask |= 1 << uint(family.Number-1)
	}
	return mask
}

// detailRefreshAlreadyApplied reports whether this exact observation is already
// the canonical stream frame. PublishInfoSource would otherwise open an empty
// zero-family input when another family's change occupies a header slot, which
// would queue a successor that publishes nothing and advances the asset epoch
// for no fact. A read snapshot proves the same two point rows and the same
// family builder before any body is claimed.
func (s *Service) detailRefreshAlreadyApplied(ctx context.Context, o playbackDetailObservation, facts assets.Facts) (bool, int64, error) {
	var empty bool
	var epoch int64
	err := dbwork.WithReadSnapshot(ctx, s.db, func(c context.Context) error {
		tx := dbwork.Snapshot(c)
		if tx == nil {
			return compactcatalog.ErrInfoInvariant
		}
		if err := playbackDetailFenceTx(c, tx, o); err != nil {
			return err
		}
		families, err := assets.BuildInfoStreams(c, tx, o.AssetID, o.Token, o.Size, o.ModifiedNS, facts.Streams)
		if err != nil {
			return err
		}
		empty = len(families) == 0
		return tx.QueryRowContext(c, `SELECT info_epoch FROM catalog_assets WHERE id=? AND token=?`, o.AssetID, o.Token).Scan(&epoch)
	})
	return empty, epoch, err
}

// publishDetailRefresh admits one exact probe as the existing stream families
// (1..3) and returns PublishInfoSource's truthful receipt. It never writes a
// canonical row and never runs the publication worker: accepted-pending is the
// best this producer can honestly report.
func (s *Service) publishDetailRefresh(ctx context.Context, o playbackDetailObservation, facts assets.Facts) (compactcatalog.InfoAcceptReceipt, error) {
	if facts.Streams == nil {
		return compactcatalog.InfoAcceptReceipt{}, fmt.Errorf("%w: playback detail refresh observed no streams", compactcatalog.ErrInfoInputIncomplete)
	}
	inputFingerprint, err := playbackDetailInputFingerprint(facts)
	if err != nil {
		return compactcatalog.InfoAcceptReceipt{}, err
	}
	witnessFingerprint, err := playbackDetailWitnessFingerprint(o, inputFingerprint)
	if err != nil {
		return compactcatalog.InfoAcceptReceipt{}, err
	}
	if applied, epoch, err := s.detailRefreshAlreadyApplied(ctx, o, facts); err != nil || applied {
		if err != nil {
			return compactcatalog.InfoAcceptReceipt{}, err
		}
		// The exact canonical stream frame already exists; this observation is
		// applied without opening a body that would publish no family.
		return compactcatalog.InfoAcceptReceipt{InfoInputReceipt: compactcatalog.InfoInputReceipt{State: compactcatalog.InfoInputApplied, ExpectedEpoch: epoch}}, nil
	}
	// Staging is background media work even when a viewer's first play triggered
	// it: the probe is already done, and the bounded publication must not take a
	// playback-start lane for its 8-row quanta.
	ctx = dbwork.WithClass(ctx, dbwork.ClassBackgroundMedia)
	check := func(ctx context.Context, tx *sql.Tx) error {
		return playbackDetailFenceTx(ctx, tx, o)
	}
	prepare := func(ctx context.Context, tx *sql.Tx) (compactcatalog.InfoInputSpec, []compactcatalog.InfoSourceFamily, error) {
		// PublishInfoSource runs check in this same transaction immediately
		// before prepare, so the fence is not repeated here.
		families, err := assets.BuildInfoStreams(ctx, tx, o.AssetID, o.Token, o.Size, o.ModifiedNS, facts.Streams)
		if err != nil {
			return compactcatalog.InfoInputSpec{}, nil, err
		}
		if len(families) == 0 {
			// The canonical frame became current between the pre-flight read and
			// this write. Never claim an empty input: it would publish no family
			// and still advance the asset epoch. Drift is terminal, so a body that
			// already exists for this producer is abandoned instead of resumed.
			return compactcatalog.InfoInputSpec{}, nil, fmt.Errorf("%w: playback detail refresh became canonical before staging", compactcatalog.ErrInfoInputDrift)
		}
		var epoch int64
		if err = tx.QueryRowContext(ctx, `SELECT info_epoch FROM catalog_assets WHERE id=? AND token=?`, o.AssetID, o.Token).Scan(&epoch); err != nil {
			return compactcatalog.InfoInputSpec{}, nil, err
		}
		witness := compactcatalog.InfoWitness{
			Kind:             compactcatalog.InfoWitnessPlayback,
			SourceID:         o.SourceID,
			SourceGeneration: o.SourceGeneration,
			RootIncarnation:  o.RootIncarnation,
			ObjectID:         o.ObjectID,
			ObjectRevision:   o.ObjectRevision,
			Fingerprint:      witnessFingerprint,
		}
		spec := compactcatalog.InfoInputSpec{
			AssetID:       o.AssetID,
			Token:         o.Token,
			ExpectedEpoch: epoch,
			FamilyMask:    playbackInfoFamilyMask(families),
			Fingerprint:   inputFingerprint,
			Witnesses:     []compactcatalog.InfoWitness{witness},
		}
		return spec, families, nil
	}
	return compactcatalog.PublishInfoSource(ctx, s.db, check, prepare)
}
