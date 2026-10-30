package remotesources

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/mediaanalysis"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// readEpisodeTargetTx supplies the exact same current physical target to the
// factual range projection and retained operation authority. It opens no source.
func readEpisodeTargetTx(ctx context.Context, tx *sql.Tx, t EpisodeTarget) (episodeAuthoritySnapshot, error) {
	var out episodeAuthoritySnapshot
	if tx == nil || t.LibraryID == "" || t.LibrarySourceID == "" || t.ObjectID == "" || t.SourceRevision == "" || t.EpisodeID == "" {
		return out, ErrEpisodeAuthority
	}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	s := &out.Source
	var relative string
	err := tx.QueryRowContext(ctx, `SELECT s.id,s.library_id,s.kind,s.root,s.generation,s.incarnation,s.root_identity,s.enabled,s.identity_confirmed,o.relative_path,o.size,o.modified_ns FROM library_sources s JOIN inventory_objects o ON o.source_id=s.id JOIN catalog_assets a ON a.token=o.asset_id WHERE s.id=? AND s.library_id=? AND s.enabled=1 AND o.id=? AND o.revision=? AND o.root_incarnation=s.incarnation AND o.retired=0 AND o.state='available' AND a.available=1 AND a.video_codec!='' AND EXISTS(SELECT 1 FROM catalog_asset_links al JOIN catalog_entities e ON e.id=al.entity_id JOIN catalog_libraries l ON l.id=e.library_id JOIN catalog_kinds k ON k.id=e.kind WHERE al.asset_id=a.id AND al.available=1 AND pid(e.public_id)=? AND k.name='episode' AND e.retired=0 AND l.retired=0 AND l.library_id=s.library_id)`, t.LibrarySourceID, t.LibraryID, t.ObjectID, t.SourceRevision, t.EpisodeID).Scan(&s.ID, &s.LibraryID, &s.Kind, &s.ResolvedPath, &s.Generation, &s.Incarnation, &s.RootIdentity, &s.Enabled, &s.IdentityConfirmed, &relative, &out.Size, &out.ModifiedNS)
	if err != nil {
		return out, err
	}
	if !s.IdentityConfirmed || s.Generation < 1 || s.Incarnation == "" || s.RootIdentity == "" || !filepath.IsAbs(s.ResolvedPath) || filepath.Clean(s.ResolvedPath) != s.ResolvedPath || !validObservationObject(filepath.ToSlash(relative)) || out.Size < 0 {
		return out, ErrEpisodeAuthority
	}
	out.File = filepath.Join(s.ResolvedPath, relative)
	return out, nil
}

// EpisodeRangesSupportedTx is a factual, DB-only projection for the existing
// artwork command. It is not actor, mode, scan-tier or scheduling admission.
// A served ETag is not evidence that the upstream supports bounded ranges.
func (r EpisodeAuthorityResolver) EpisodeRangesSupportedTx(ctx context.Context, tx *sql.Tx, target EpisodeTarget) (bool, error) {
	if r.Sources == nil || r.Sources.db == nil {
		return false, ErrEpisodeAuthority
	}
	captured, err := readEpisodeTargetTx(ctx, tx, target)
	if err != nil {
		return false, err
	}
	switch captured.Source.Kind {
	case "local":
		return r.LocalStorage != nil && filepath.IsAbs(r.LocalStorage.Binary) && r.Sources.mounts != nil, nil
	case "remote":
		if err = r.readEpisodeRemoteTx(ctx, tx, target, &captured); err != nil {
			if errors.Is(err, storage.ErrRemoteRange) || errors.Is(err, mediaanalysis.ErrUnsupported) {
				return false, nil
			}
			return false, err
		}
		var support string
		if err = tx.QueryRowContext(ctx, `SELECT range_support FROM remote_sources WHERE id=? AND removed=0`, captured.Observation.SourceID).Scan(&support); err != nil {
			return false, err
		}
		// "conditional" is written only after the existing real origin range
		// test. Unknown/sequential and fixture-only labels grant no support.
		return support == "conditional", nil
	default:
		return false, ErrEpisodeAuthority
	}
}

// readEpisodeRemoteTx resolves the actual observation/catalog revision and
// current source profile. It is shared by availability and retained authority;
// none of these source facts replaces the latter's actual actor/job checks.
func (r EpisodeAuthorityResolver) readEpisodeRemoteTx(ctx context.Context, tx *sql.Tx, t EpisodeTarget, out *episodeAuthoritySnapshot) error {
	if assets.DiscImage(out.File) {
		return mediaanalysis.ErrUnsupported
	}
	s := &out.Source
	var err error
	b, object, ok := r.Sources.find(out.File)
	if !ok || b.Removed || !validObservationObject(object) {
		return storage.ErrRemoteChanged
	}
	var root, support, code string
	if err = tx.QueryRowContext(ctx, `SELECT root,range_support,error_code FROM remote_sources WHERE id=? AND removed=0`, b.ID).Scan(&root, &support, &code); err != nil {
		return err
	}
	if root != b.Root {
		return storage.ErrRemoteChanged
	}
	if support == "unsupported" || code == "range_unsupported" {
		return storage.ErrRemoteRange
	}
	var itemCode string
	err = tx.QueryRowContext(ctx, `SELECT code FROM remote_analysis_status WHERE path=?`, out.File).Scan(&itemCode)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if itemCode == "range_unsupported" {
		return storage.ErrRemoteRange
	}
	out.RemoteGeneration, err = observationGenerationTx(ctx, tx, b.ID)
	if err != nil {
		return err
	}
	// Remote catalogue revisions include the source incarnation, but deliberately
	// do not encode a remote root's identity. Check that independent admission
	// against the actual source/path/generation namespace as well.
	expectedRoot := storage.RemoteDirectoryIdentity(b.ID, s.ResolvedPath) + ":generation:" + out.RemoteGeneration
	if !storage.SameRoot(s.RootIdentity, expectedRoot) {
		return storage.ErrRemoteChanged
	}
	out.Profile, err = SourceReadProfileTx(ctx, tx, b.ID)
	if err != nil {
		return err
	}
	observation, e := readScanObservationTx(ctx, tx, b.ID, object)
	if e != nil {
		return e
	}
	if observation.Ref.Generation != out.RemoteGeneration || observation.Observation.Facts.Size != out.Size || observation.Observation.Facts.ValidateForRange() != nil {
		return storage.ErrRemoteChanged
	}
	transport, e := observationSnapshot(b, out.RemoteGeneration, observation.Observation.Facts)
	if e != nil {
		return e
	}
	objectRevision, _, e := catalog.ObservationRevision(*s, transport)
	if e != nil {
		return e
	}
	if transport.Revision != observation.Observation.ScanRevision || objectRevision != t.SourceRevision {
		return storage.ErrRemoteChanged
	}
	out.Observation = observation.Ref
	return nil
}
