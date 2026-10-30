package remotesources

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/mediasource"
	"github.com/PorticoMediaServer/portico/server/internal/ownedcapture"
	"github.com/PorticoMediaServer/portico/server/internal/playbackdisc"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// DiscConditionalSource composes the existing direct WebDAV conditional opener
// with the exact selected physical source. Neither the source value nor its
// opaque authority witness is a grant. Original Play and later active readers
// each supply their own mandatory current operation/presentation proof.
type DiscConditionalSource struct{ authority *captureProducer }

// NewDiscConditionalSource uses the same ordinary actor/content authorizer as
// the capture adapter. It does not change the capture producer, allocate bytes,
// require capture cache permission or install any playback consumer.
func NewDiscConditionalSource(options CaptureProducerOptions) (*DiscConditionalSource, error) {
	if options.Sources == nil || options.Sources.db == nil || options.Identities == nil || options.AuthorizeTx == nil {
		return nil, storage.ErrRemoteConfig
	}
	return &DiscConditionalSource{authority: &captureProducer{sources: options.Sources, identities: options.Identities, authorize: options.AuthorizeTx}}, nil
}

type conditionalDiscSelection struct {
	source                            playbackdisc.Source
	libraryRoot, remoteRoot, path     string
	inventoryRelative, object, itemID string
	kind, mountID, networkFingerprint string
	configDigest                      string
	profile                           SourceReadProfile
	scan                              RecordedScanObservation
}

func discConditionalText(v string, limit int) bool {
	return v != "" && len(v) <= limit && utf8.ValidString(v) && strings.IndexFunc(v, unicode.IsControl) < 0
}

func (d *DiscConditionalSource) validTx(ctx context.Context, tx *sql.Tx) error {
	if d == nil || d.authority == nil || ctx == nil || tx == nil {
		return storage.ErrRemoteConfig
	}
	if held := dbwork.Snapshot(ctx); held != nil && held != tx {
		return storage.ErrRemoteConfig
	}
	return ctx.Err()
}

// resolveTx is DB-only. Initial resolution refuses physical ambiguity. A
// continuation restricts the query to the original object and library source,
// so a removed selection cannot be replaced by another copy of the asset.
func (d *DiscConditionalSource) resolveTx(ctx context.Context, tx *sql.Tx, asset, witness string, original *playbackdisc.Source) (conditionalDiscSelection, error) {
	var out conditionalDiscSelection
	if err := d.validTx(ctx, tx); err != nil {
		return out, err
	}
	if !discConditionalText(asset, 256) || !discConditionalText(witness, 256) {
		return out, storage.ErrRemoteConfig
	}
	where := ""
	args := []any{asset}
	if original != nil {
		where = " AND o.id=? AND s.id=? AND r.id=?"
		args = append(args, original.InventoryObjectID, original.LibrarySourceID, original.RemoteSourceID)
	}
	rows, err := tx.QueryContext(ctx, `SELECT o.id,o.revision,o.relative_path,o.size,o.modified_ns,
 s.id,s.library_id,s.root,s.generation,s.incarnation,s.root_identity,
 r.id,r.root,r.kind,r.mount_id,r.config,r.generation,a.path
 FROM inventory_objects o JOIN library_sources s ON s.id=o.source_id
 JOIN remote_sources r ON r.id=s.remote_source_id JOIN catalog_assets a ON a.token=o.asset_id
 WHERE o.asset_id=? AND o.retired=0 AND o.state='available' AND o.unsupported_reason=''
 AND o.root_incarnation=s.incarnation AND s.kind='remote' AND s.enabled=1
 AND s.identity_confirmed=1 AND s.health='healthy' AND r.removed=0 AND a.available=1
 AND o.size=a.size AND o.modified_ns=a.modified_ns
 AND EXISTS(SELECT 1 FROM catalog_asset_links link JOIN catalog_entities item ON item.id=link.entity_id
 JOIN catalog_libraries lib ON lib.id=item.library_id WHERE link.asset_id=a.id AND link.available=1
 AND item.retired=0 AND lib.retired=0 AND lib.library_id=s.library_id)`+where+` ORDER BY o.id LIMIT 2`, args...)
	if err != nil {
		return out, err
	}
	count := 0
	for rows.Next() {
		count++
		if count > 1 {
			rows.Close()
			return out, storage.ErrRemoteChanged
		}
		var config []byte
		if err = rows.Scan(&out.source.InventoryObjectID, &out.source.InventoryRevision, &out.inventoryRelative, &out.source.Size, &out.source.ModifiedNS,
			&out.source.LibrarySourceID, &out.source.LibraryID, &out.libraryRoot, &out.source.LibraryGeneration, &out.source.Incarnation, &out.source.RootIdentity,
			&out.source.RemoteSourceID, &out.remoteRoot, &out.kind, &out.mountID, &config, &out.source.RawConfigurationGeneration, &out.path); err != nil {
			rows.Close()
			return out, err
		}
		if len(config) > 64<<10 {
			rows.Close()
			return out, storage.ErrRemoteConfig
		}
		sum := sha256.Sum256(config)
		out.configDigest = hex.EncodeToString(sum[:])
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	if count != 1 {
		return out, storage.ErrRemoteChanged
	}
	// Only the existing direct WebDAV adapter retains a qualified conditional
	// origin. A range-capable native/wrapped source is not promoted to one.
	if out.kind != "webdav" || out.mountID != "" {
		return out, storage.ErrRemoteRange
	}
	out.source.AssetID, out.source.ReadAuthorityWitness = asset, witness
	if !filepath.IsAbs(out.libraryRoot) || filepath.Clean(out.libraryRoot) != out.libraryRoot ||
		!filepath.IsAbs(out.remoteRoot) || filepath.Clean(out.remoteRoot) != out.remoteRoot ||
		!validObservationObject(filepath.ToSlash(out.inventoryRelative)) ||
		!strings.EqualFold(filepath.Ext(out.inventoryRelative), ".iso") ||
		out.path != filepath.Join(out.libraryRoot, filepath.FromSlash(out.inventoryRelative)) ||
		out.source.Size <= 0 || out.source.ModifiedNS < 0 || out.source.LibraryGeneration < 1 || out.source.RawConfigurationGeneration < 1 {
		return out, storage.ErrRemoteChanged
	}
	relative, err := filepath.Rel(out.remoteRoot, out.path)
	if err != nil || !filepath.IsLocal(relative) || !validObservationObject(filepath.ToSlash(relative)) {
		return out, storage.ErrRemoteChanged
	}
	out.object = filepath.ToSlash(relative)
	// An asset linked to multiple live titles is not an implicit title choice.
	itemRows, err := tx.QueryContext(ctx, `SELECT pid(item.public_id) FROM catalog_asset_links link
 JOIN catalog_entities item ON item.id=link.entity_id JOIN catalog_libraries lib ON lib.id=item.library_id
 WHERE link.asset_id=(SELECT id FROM catalog_assets WHERE token=?) AND link.available=1
 AND item.retired=0 AND lib.retired=0 AND lib.library_id=? ORDER BY item.id LIMIT 2`, asset, out.source.LibraryID)
	if err != nil {
		return out, err
	}
	items := 0
	for itemRows.Next() {
		items++
		if items > 1 {
			itemRows.Close()
			return out, storage.ErrRemoteChanged
		}
		if err = itemRows.Scan(&out.itemID); err != nil {
			itemRows.Close()
			return out, err
		}
	}
	err = itemRows.Err()
	itemRows.Close()
	if err != nil {
		return out, err
	}
	if items != 1 {
		return out, storage.ErrRemoteChanged
	}
	s := d.authority.sources
	out.source.EffectiveConfiguration, err = observationGenerationTx(ctx, tx, out.source.RemoteSourceID)
	if err != nil {
		return out, err
	}
	out.scan, err = readScanObservationTx(ctx, tx, out.source.RemoteSourceID, out.object)
	if err != nil {
		return out, err
	}
	if out.scan.Ref.Generation != out.source.EffectiveConfiguration || out.scan.Ref.Revision < 1 || out.scan.Observation.Facts.Size != out.source.Size {
		return out, storage.ErrRemoteChanged
	}
	b := binding{ID: out.source.RemoteSourceID, Kind: out.kind, Root: out.remoteRoot}
	snapshot, err := observationSnapshot(b, out.source.EffectiveConfiguration, out.scan.Observation.Facts)
	if err != nil || snapshot.ModifiedNS != out.source.ModifiedNS {
		return out, storage.ErrRemoteChanged
	}
	revision, _, err := catalog.ObservationRevision(catalog.LibrarySource{ID: out.source.LibrarySourceID, Kind: "remote", Incarnation: out.source.Incarnation, RootIdentity: out.source.RootIdentity}, snapshot)
	if err != nil || revision != out.source.InventoryRevision ||
		!storage.SameRoot(out.source.RootIdentity, storage.RemoteDirectoryIdentity(out.source.RemoteSourceID, out.libraryRoot)+":generation:"+out.source.EffectiveConfiguration) {
		return out, storage.ErrRemoteChanged
	}
	// Validate the recorded provider validator only. This is not a measured
	// input version: OpenDiscConditional must independently open and match it.
	if _, err = mediasource.NewVersion(mediasource.Evidence{Kind: mediasource.StrongETag, Scope: remoteVersionScope(b, out.source.EffectiveConfiguration), Object: snapshot.ObjectID, Revision: out.scan.Observation.Facts.ProviderETag, Size: out.source.Size}); err != nil {
		return out, storage.ErrRemoteRange
	}
	sequential, err := sourceObjectSequentialTx(ctx, tx, out.source.RemoteSourceID, out.path)
	if err != nil {
		return out, err
	}
	if sequential {
		return out, storage.ErrRemoteRange
	}
	network, err := s.sourceNetworkTx(ctx, tx, out.source.RemoteSourceID, out.remoteRoot)
	if err != nil {
		return out, err
	}
	out.networkFingerprint = network.Fingerprint
	out.profile, err = SourceReadProfileTx(ctx, tx, out.source.RemoteSourceID)
	if err != nil {
		return out, err
	}
	out.source.ProfileRevision = out.profile.Revision
	physical := out.source
	physical.ReadAuthorityWitness = ""
	raw, err := json.Marshal([]any{"portico.disc-conditional-source.policy.v1", physical, out.libraryRoot, out.remoteRoot, out.inventoryRelative,
		out.path, out.itemID, out.configDigest, out.networkFingerprint, out.profile, out.scan.Ref, out.scan.Observation})
	if err != nil {
		return out, err
	}
	sum := sha256.Sum256(raw)
	out.source.ReadPolicyWitness = hex.EncodeToString(sum[:])
	return out, ctx.Err()
}

// ResolveTx proves the original ordinary Play actor/content admission. It
// returns private facts only, makes no source lease/quota rows and does no IO.
func (d *DiscConditionalSource) ResolveTx(ctx context.Context, tx *sql.Tx, principal identity.Principal, assetID, authorityWitness string) (playbackdisc.Source, error) {
	selected, err := d.resolveTx(ctx, tx, assetID, authorityWitness, nil)
	if err != nil {
		return playbackdisc.Source{}, err
	}
	target := captureTarget{itemID: selected.itemID}
	if _, err = d.authority.authorizeTargetTx(ctx, tx, ownedcapture.Request{Principal: principal, AssetID: assetID}, &target); err != nil {
		return playbackdisc.Source{}, err
	}
	if _, err = d.checkSourceTx(ctx, tx, selected.source); err != nil {
		return playbackdisc.Source{}, err
	}
	return selected.source, ctx.Err()
}

// CheckTx is source currentness only. Actual preparation/session authority is
// the caller's separately required proof; neither a source nor its ID grants it.
func (d *DiscConditionalSource) CheckTx(ctx context.Context, tx *sql.Tx, original playbackdisc.Source) error {
	_, err := d.checkSourceTx(ctx, tx, original)
	return err
}

func (d *DiscConditionalSource) checkSourceTx(ctx context.Context, tx *sql.Tx, original playbackdisc.Source) (conditionalDiscSelection, error) {
	current, err := d.resolveTx(ctx, tx, original.AssetID, original.ReadAuthorityWitness, &original)
	if err != nil {
		return conditionalDiscSelection{}, err
	}
	if current.source != original {
		return conditionalDiscSelection{}, storage.ErrRemoteChanged
	}
	return current, ctx.Err()
}

// Open admits a NEW exact SourcePlayback lease under this caller's retained
// lifetime/proof. Later active readers use their independent presentation/pin
// scope, never an expired preparation context or a principal saved in this type.
// The proof must use the supplied transaction and repeat the original scope.
func (d *DiscConditionalSource) Open(ctx context.Context, original playbackdisc.Source, check func(context.Context, *sql.Tx) error) (storage.RemoteObject, error) {
	if d == nil || d.authority == nil || ctx == nil || check == nil || dbwork.Snapshot(ctx) != nil {
		return nil, storage.ErrRemoteConfig
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	view, err := dbwork.BeginSnapshot(ctx, d.authority.sources.db)
	if err != nil {
		return nil, err
	}
	if err = check(view.Context(), view.Tx()); err != nil {
		view.Rollback()
		return nil, err
	}
	selected, err := d.checkSourceTx(view.Context(), view.Tx(), original)
	var lease *SourceReadLease
	if err == nil {
		lease, err = d.authority.sources.CaptureReadSourceTx(ctx, view.Tx(), SourceSessionAdmission{
			Purpose: SourcePlayback, Observation: selected.scan.Ref, OnChanged: func() {},
			CheckTx: func(current context.Context, tx *sql.Tx, binding SourceReadBinding) error {
				if err := check(current, tx); err != nil {
					return err
				}
				if binding.Purpose != SourcePlayback || binding.Observation != selected.scan.Ref || binding.Path != selected.path || binding.Root != selected.remoteRoot ||
					binding.ScanRevision != selected.scan.Observation.ScanRevision || binding.Profile != selected.profile || binding.Size != original.Size || binding.Sequential {
					return storage.ErrRemoteChanged
				}
				if _, err := d.checkSourceTx(current, tx, original); err != nil {
					return err
				}
				if err := check(current, tx); err != nil {
					return err
				}
				return current.Err()
			},
		})
	}
	view.Rollback()
	if err != nil {
		return nil, err
	}
	return lease.OpenDiscConditional(ctx)
}
