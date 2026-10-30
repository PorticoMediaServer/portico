package remotesources

import (
	"context"
	"database/sql"
	"io"
	"path/filepath"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
	"github.com/PorticoMediaServer/portico/server/internal/subtitles"
)

// playbackSourceSnapshot is recorded source evidence, not an immutable-byte
// claim. The selected playback invocation supplies actor/session authority;
// this snapshot additionally binds the actual physical source and observation.
type playbackSourceSnapshot struct {
	source                             catalog.LibrarySource
	object, revision, file, remoteRoot string
	size, modified, networkRevision    int64
	observation                        ObservationRef
	scan                               storage.Snapshot
	profile                            SourceReadProfile
}

// readPlaybackSourceTx never lists, stats or opens an origin. Requiring the
// actual playback physical pin prevents a matching path or size from choosing
// a different available source for the same asset.
func (s *Service) readPlaybackSourceTx(ctx context.Context, tx *sql.Tx, facts subtitles.PlaybackSourceFacts) (playbackSourceSnapshot, error) {
	var out playbackSourceSnapshot
	if s == nil || tx == nil || facts.ItemID == "" || facts.AssetID == "" || facts.SessionID == "" {
		return out, subtitles.ErrUnavailable
	}
	var relative, assetPath string
	physical := &out.source
	err := tx.QueryRowContext(ctx, `SELECT physical.id,physical.library_id,physical.kind,physical.root,physical.generation,physical.incarnation,physical.root_identity,physical.enabled,physical.identity_confirmed,
 object.id,object.revision,object.relative_path,object.size,object.modified_ns,a.path,COALESCE(network.revision,0)
 FROM playback_physical_source_pins pin
 JOIN library_sources physical ON physical.id=pin.source_id AND physical.incarnation=pin.incarnation AND physical.generation=pin.configuration_generation
 JOIN inventory_objects object ON object.source_id=physical.id AND object.root_incarnation=physical.incarnation
 JOIN catalog_assets a ON a.token=object.asset_id
 JOIN catalog_asset_links link ON link.asset_id=a.id
 JOIN catalog_entities item ON item.id=link.entity_id
 JOIN catalog_libraries library ON library.id=item.library_id AND library.library_id=physical.library_id
 LEFT JOIN library_network_policy network ON network.library_id=physical.library_id
 WHERE pin.session_id=? AND item.public_id=pid_blob(?) AND a.token=? AND physical.kind='remote'
 AND physical.enabled=1 AND physical.identity_confirmed=1 AND object.retired=0 AND object.state='available'
 AND a.available=1 AND link.available=1 AND item.retired=0 AND library.retired=0
 AND a.size=? AND a.modified_ns=?`, facts.SessionID, facts.ItemID, facts.AssetID, facts.SourceSize, facts.SourceModifiedNS).Scan(
		&physical.ID, &physical.LibraryID, &physical.Kind, &physical.ResolvedPath, &physical.Generation, &physical.Incarnation, &physical.RootIdentity, &physical.Enabled, &physical.IdentityConfirmed,
		&out.object, &out.revision, &relative, &out.size, &out.modified, &assetPath, &out.networkRevision)
	if err != nil {
		return out, err
	}
	if physical.Generation < 1 || physical.Incarnation == "" || physical.RootIdentity == "" || out.object == "" || out.revision == "" ||
		!filepath.IsAbs(physical.ResolvedPath) || filepath.Clean(physical.ResolvedPath) != physical.ResolvedPath || !validObservationObject(filepath.ToSlash(relative)) ||
		out.size < 1 || out.size != facts.SourceSize || out.modified != facts.SourceModifiedNS {
		return out, storage.ErrRemoteChanged
	}
	out.file = filepath.Join(physical.ResolvedPath, relative)
	if assetPath != out.file {
		return out, storage.ErrRemoteChanged
	}
	recorded, snapshot, err := s.ScanSnapshotTx(ctx, tx, out.file)
	if err != nil {
		return out, err
	}
	out.observation, out.scan = recorded.Ref, snapshot
	if snapshot.Directory || snapshot.Size != out.size || snapshot.ModifiedNS != out.modified || recorded.Observation.Facts.ValidateForRange() != nil {
		return out, storage.ErrRemoteChanged
	}
	revision, _, err := catalog.ObservationRevision(*physical, snapshot)
	if err != nil || revision != out.revision {
		return out, storage.ErrRemoteChanged
	}
	if !storage.SameRoot(physical.RootIdentity, storage.RemoteDirectoryIdentity(recorded.Ref.SourceID, physical.ResolvedPath)+":generation:"+recorded.Ref.Generation) {
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

// OpenSubtitlePlaybackInput is only the continuation of a real sealed Select
// invocation. It grants neither generic extraction nor analysis. Unknown or
// sequential sources refuse in the DB snapshot, before provider/process I/O.
func (s *Service) OpenSubtitlePlaybackInput(ctx context.Context, item, asset, path string, size, modified int64) (subtitles.RenderInput, error) {
	invocation, ok := subtitles.PlaybackSourceInvocationFromContext(ctx)
	if !ok || s == nil || s.db == nil {
		return nil, subtitles.ErrUnavailable
	}
	facts := invocation.Facts()
	if facts.ItemID != item || facts.AssetID != asset || facts.SourceSize != size || facts.SourceModifiedNS != modified {
		return nil, subtitles.ErrConflict
	}
	if invocation.Lifetime() == nil {
		return nil, subtitles.ErrUnavailable
	}
	// The logical source lease remains with Select through final publication;
	// the extraction range below retains its shorter child cancellation.
	life, cancel := context.WithCancel(invocation.Lifetime())
	snapshot, err := dbwork.BeginSnapshot(ctx, s.db)
	if err != nil {
		cancel()
		return nil, err
	}
	if err = invocation.CheckTx(life, snapshot.Tx()); err != nil {
		snapshot.Rollback()
		cancel()
		return nil, err
	}
	captured, err := s.readPlaybackSourceTx(life, snapshot.Tx(), facts)
	if err == nil && captured.file != path {
		err = storage.ErrRemoteChanged
	}
	var lease *SourceReadLease
	if err == nil {
		lease, err = s.CaptureReadSourceTx(life, snapshot.Tx(), SourceSessionAdmission{
			Purpose: SourcePlayback, Observation: captured.observation, OnChanged: cancel,
			CheckTx: func(check context.Context, tx *sql.Tx, binding SourceReadBinding) error {
				if binding.Purpose != SourcePlayback || binding.Observation != captured.observation || binding.Path != captured.file ||
					binding.Root != captured.remoteRoot || binding.ScanRevision != captured.scan.ChangeToken || binding.Profile != captured.profile || binding.Size != captured.size || binding.Sequential {
					return storage.ErrRemoteChanged
				}
				now, err := s.readPlaybackSourceTx(check, tx, facts)
				if err != nil {
					return err
				}
				if now != captured {
					return storage.ErrRemoteChanged
				}
				return invocation.CheckTx(check, tx)
			},
		})
	}
	// OpenRange may start real provider work, and must run after SQL release.
	snapshot.Rollback()
	if err != nil {
		cancel()
		return nil, err
	}
	closeLease := func() error { cancel(); return lease.Close() }
	if err = invocation.Retain(lease.CheckTx, closeLease); err != nil {
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

type subtitlePlaybackInput struct {
	reader   *SourceRange
	size     int64
	evidence string
}

func (i *subtitlePlaybackInput) Size() int64                        { return i.size }
func (i *subtitlePlaybackInput) Evidence() string                   { return i.evidence }
func (i *subtitlePlaybackInput) Validate(ctx context.Context) error { return i.reader.Check(ctx) }

// Closing the decoder's child range must retain the invocation's lease until
// final selection/resource publication has performed its same-Tx recheck.
func (i *subtitlePlaybackInput) Close() error { return i.reader.Close() }
func (i *subtitlePlaybackInput) ReadExtent(ctx context.Context, offset, length int64) ([]byte, error) {
	if offset < 0 || length < 1 || length > 1<<20 || offset > i.size-length {
		return nil, subtitles.ErrInput
	}
	data := make([]byte, int(length))
	n, err := i.reader.ReadAt(ctx, data, offset)
	if err != nil || n != len(data) {
		clear(data)
		if err == nil {
			err = io.ErrUnexpectedEOF
		}
		return nil, err
	}
	return data, nil
}
