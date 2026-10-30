package remotesources

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/mediaanalysis"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
	"github.com/PorticoMediaServer/portico/server/internal/subtitles"
)

// readAnalysisSourceTx proves one actual analysis invocation's physical object;
// it never borrows a playback session or discovers a latest asset by path.
func (s *Service) readAnalysisSourceTx(ctx context.Context, tx *sql.Tx, f mediaanalysis.ReadFacts) (playbackSourceSnapshot, error) {
	out := playbackSourceSnapshot{source: f.Source, object: f.ObjectID, revision: f.ObjectRevision, size: f.Size, modified: f.ModifiedNS}
	if !f.Consent.Remote || !f.Consent.Allowed || f.Source.Kind != "remote" || !f.Source.Enabled || !f.Source.IdentityConfirmed || f.Size < 1 {
		return out, storage.ErrRemoteChanged
	}
	var relative, assetPath string
	err := tx.QueryRowContext(ctx, `SELECT o.relative_path,a.path,COALESCE(network.revision,0)
 FROM inventory_objects o JOIN catalog_assets a ON a.token=o.asset_id
 JOIN catalog_asset_links link ON link.asset_id=a.id
 JOIN catalog_entities item ON item.id=link.entity_id
 JOIN catalog_libraries library ON library.id=item.library_id
 LEFT JOIN library_network_policy network ON network.library_id=library.library_id
 WHERE o.id=? AND o.revision=? AND o.source_id=? AND o.asset_id=? AND o.root_incarnation=?
 AND o.retired=0 AND o.state='available' AND o.size=? AND o.modified_ns=?
 AND a.available=1 AND a.size=o.size AND a.modified_ns=o.modified_ns
 AND link.available=1 AND item.retired=0 AND library.retired=0
 AND item.public_id=pid_blob(?) AND library.library_id=?`, f.ObjectID, f.ObjectRevision, f.Source.ID, f.AssetID, f.Source.Incarnation, f.Size, f.ModifiedNS, f.ItemID, f.Source.LibraryID).
		Scan(&relative, &assetPath, &out.networkRevision)
	if err != nil {
		return out, err
	}
	// Disc acquisition belongs to an actual Play invocation. Advanced consent
	// cannot turn discovery/background analysis into an origin disc fetch.
	if strings.EqualFold(filepath.Ext(relative), ".iso") {
		return out, subtitles.ErrUnsupported
	}
	if !filepath.IsAbs(f.Source.ResolvedPath) || filepath.Clean(f.Source.ResolvedPath) != f.Source.ResolvedPath || !validObservationObject(filepath.ToSlash(relative)) {
		return out, storage.ErrRemoteChanged
	}
	out.file = filepath.Join(f.Source.ResolvedPath, relative)
	if out.file != assetPath {
		return out, storage.ErrRemoteChanged
	}
	recorded, snapshot, err := s.ScanSnapshotTx(ctx, tx, out.file)
	if err != nil {
		return out, err
	}
	out.observation, out.scan = recorded.Ref, snapshot
	if recorded.Ref.SourceID != f.Consent.SourceID || snapshot.Directory || snapshot.Size != f.Size || snapshot.ModifiedNS != f.ModifiedNS || recorded.Observation.Facts.ValidateForRange() != nil {
		return out, storage.ErrRemoteChanged
	}
	revision, _, err := catalog.ObservationRevision(f.Source, snapshot)
	if err != nil || revision != f.ObjectRevision || !storage.SameRoot(f.Source.RootIdentity, storage.RemoteDirectoryIdentity(recorded.Ref.SourceID, f.Source.ResolvedPath)+":generation:"+recorded.Ref.Generation) {
		return out, storage.ErrRemoteChanged
	}
	if err = tx.QueryRowContext(ctx, `SELECT root FROM remote_sources WHERE id=? AND removed=0`, recorded.Ref.SourceID).Scan(&out.remoteRoot); err != nil {
		return out, err
	}
	out.profile, err = SourceReadProfileTx(ctx, tx, recorded.Ref.SourceID)
	if err != nil {
		return out, err
	}
	sequential, err := sourceObjectSequentialTx(ctx, tx, recorded.Ref.SourceID, out.file)
	if err != nil {
		return out, err
	}
	if sequential {
		return out, storage.ErrRemoteRange
	}
	return out, nil
}

// OpenMediaAnalysisInput is only a continuation of a sealed actual analysis job.
// Unsupported range sources refuse; there is no playback-purpose/spool fallback.
func (s *Service) OpenMediaAnalysisInput(ctx context.Context, item, asset, path string, size, modified int64) (subtitles.RenderInput, error) {
	i, ok := mediaanalysis.ReadInvocationFromContext(ctx)
	if !ok || s == nil || s.db == nil || dbwork.Snapshot(ctx) != nil {
		return nil, subtitles.ErrUnavailable
	}
	f := i.Facts()
	if f.ItemID != item || f.AssetID != asset || f.Size != size || f.ModifiedNS != modified || i.Lifetime() == nil {
		return nil, subtitles.ErrConflict
	}
	life, cancel := context.WithCancel(i.Lifetime())
	view, err := dbwork.BeginSnapshot(life, s.db)
	if err != nil {
		cancel()
		return nil, err
	}
	if err = i.CheckTx(life, view.Tx()); err != nil {
		view.Rollback()
		cancel()
		return nil, err
	}
	captured, err := s.readAnalysisSourceTx(life, view.Tx(), f)
	if err == nil && captured.file != path {
		err = storage.ErrRemoteChanged
	}
	var lease *SourceReadLease
	if err == nil {
		lease, err = s.CaptureReadSourceTx(life, view.Tx(), SourceSessionAdmission{Purpose: SourceMediaAnalysis, Observation: captured.observation, OnChanged: i.Retire,
			CheckTx: func(check context.Context, tx *sql.Tx, b SourceReadBinding) error {
				if b.Purpose != SourceMediaAnalysis || b.Observation != captured.observation || b.Path != captured.file || b.Root != captured.remoteRoot || b.ScanRevision != captured.scan.ChangeToken || b.Profile != captured.profile || b.Size != captured.size || b.Sequential {
					return storage.ErrRemoteChanged
				}
				if err := i.CheckTx(check, tx); err != nil {
					return err
				}
				now, err := s.readAnalysisSourceTx(check, tx, f)
				if err != nil {
					return err
				}
				if now != captured {
					return storage.ErrRemoteChanged
				}
				return nil
			}})
	}
	view.Rollback()
	if err != nil {
		cancel()
		return nil, err
	}
	stop := context.AfterFunc(lease.life, i.Retire)
	closeLease := func() error { stop(); cancel(); return lease.Close() }
	if err = i.Retain(lease.CheckTx, closeLease); err != nil {
		closeLease()
		return nil, err
	}
	reader, err := lease.OpenRange(ctx)
	if err != nil {
		closeLease()
		return nil, err
	}
	return &subtitlePlaybackInput{reader: reader, size: captured.size, evidence: "observed:" + captured.revision}, nil
}
