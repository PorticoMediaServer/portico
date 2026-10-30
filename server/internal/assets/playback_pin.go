package assets

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"

	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

type playbackQuery interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type playbackEvidence struct {
	binding        storage.PlaybackBinding
	source, object string
}

func validPlaybackBinding(binding storage.PlaybackBinding) bool {
	r := binding.Inventory
	return binding.ObjectIdentity != "" && r.RootIdentity != "" && filepath.IsAbs(r.Root) && filepath.IsLocal(r.RelativePath) && r.RelativePath != "."
}

// playbackEvidenceSQL is the identity, lifetime and root witness an accepted
// binding must still satisfy. Health is deliberately separate: a source that is
// only offline keeps all of this evidence, so its state can be named honestly.
const playbackEvidenceSQL = `o.size=? AND o.modified_ns=? AND o.retired=0 AND o.state='available' AND s.kind IN('local','remote') AND s.enabled=1 AND s.identity_confirmed=1 AND o.root_incarnation=s.incarnation AND (?='' OR (s.incarnation=? AND s.generation=?))`

// sourcePrefixSQL is the folder fallback the inventory_item_availability view
// already uses for an asset with no inventory object in the item's library.
// ?1 is the library, ?2 the catalogued path. It matches the source's current
// root or, after a root move, the folder the owner configured.
const sourcePrefixSQL = `(substr(?2,1,length(rtrim(s.root,'/\'))+1) IN(rtrim(s.root,'/\')||'/',rtrim(s.root,'/\')||'\') OR substr(?2,1,length(rtrim(s.configured_root,'/\'))+1) IN(rtrim(s.configured_root,'/\')||'/',rtrim(s.configured_root,'/\')||'\'))`

// sourceFallbackSQL classifies the library's configured sources that cover a
// path: explicitly refused, currently usable, or only offline. Offline is not
// part of the view's refusal set, which is why the view keeps advertising the
// file; playback still needs to name it. Every count is read from the source
// row, never written back per item.
const sourceFallbackSQL = `
SELECT COALESCE(SUM(CASE WHEN s.enabled=0 OR s.health IN('root_changed','removing') THEN 1 ELSE 0 END),0),
 COALESCE(SUM(CASE WHEN s.enabled=1 AND s.health NOT IN('offline','root_changed','removing') THEN 1 ELSE 0 END),0),
 COALESCE(SUM(CASE WHEN s.enabled=1 AND s.health='offline' THEN 1 ELSE 0 END),0)
FROM library_sources s
WHERE s.library_id=?1 AND ` + sourcePrefixSQL

// catalogFallbackSQL is the same classification for a read without a session:
// the asset's live linked items name the candidate libraries. Only the existing
// item/library association is used, and only links the catalogue still
// advertises, so an association another library already refuses cannot mask the
// one this read is entitled to. No custody is claimed for the file.
const catalogFallbackSQL = `
SELECT COALESCE(SUM(CASE WHEN s.enabled=0 OR s.health IN('root_changed','removing') THEN 1 ELSE 0 END),0),
 COALESCE(SUM(CASE WHEN s.enabled=1 AND s.health NOT IN('offline','root_changed','removing') THEN 1 ELSE 0 END),0),
 COALESCE(SUM(CASE WHEN s.enabled=1 AND s.health='offline' THEN 1 ELSE 0 END),0)
FROM catalog_asset_links l
JOIN catalog_entities e ON e.id=l.entity_id AND e.retired=0
JOIN catalog_libraries cl ON cl.id=e.library_id AND cl.retired=0
JOIN library_sources s ON s.library_id=cl.library_id
WHERE l.asset_id=(SELECT id FROM catalog_assets WHERE token=?1) AND l.available=1 AND ` + sourcePrefixSQL

// fallbackRefusal turns the three source counts into the one answer playback
// owes. A live matching source keeps the file playable; an explicitly denied,
// root-changed or removing source keeps its existing generic refusal (offline
// never masks it); a source that is only offline is named as such; no matching
// source leaves an unassigned file on its existing size/mtime acquisition.
func fallbackRefusal(refused, usable, offline int) error {
	switch {
	case usable > 0:
		return nil
	case refused > 0:
		return storage.ErrPlaybackSource
	case offline > 0:
		return storage.ErrStorageOffline
	}
	return nil
}

// sourceFallbackRefusal checks one item library's folder fallback.
func sourceFallbackRefusal(ctx context.Context, q playbackQuery, library, path string) error {
	if library == "" || path == "" {
		return nil
	}
	var refused, usable, offline int
	if err := q.QueryRowContext(ctx, sourceFallbackSQL, library, path).Scan(&refused, &usable, &offline); err != nil {
		return err
	}
	return fallbackRefusal(refused, usable, offline)
}

// catalogFallbackRefusal checks the fallback through the asset's live links,
// for a read that has no item of its own (DLNA's authorized media).
func catalogFallbackRefusal(ctx context.Context, q playbackQuery, asset, path string) error {
	var refused, usable, offline int
	if err := q.QueryRowContext(ctx, catalogFallbackSQL, asset, path).Scan(&refused, &usable, &offline); err != nil {
		return err
	}
	return fallbackRefusal(refused, usable, offline)
}

// playbackFallbackTarget returns the item library and catalogued path behind a
// session that has no selected physical source. A remote descriptor is not a
// storage file, so it is skipped: its bytes come from the retained remote
// version, not from the folder the .strm file happens to sit in.
func playbackFallbackTarget(ctx context.Context, q playbackQuery, session string) (library, path string, ok bool, err error) {
	var container string
	err = q.QueryRowContext(ctx, `SELECT cl.library_id,a.path,COALESCE(a.container,'')
 FROM playback_source_pins pin JOIN playback_sessions ps ON ps.id=pin.session_id
 JOIN catalog_entities e ON e.id=ps.item_id JOIN catalog_libraries cl ON cl.id=e.library_id
 JOIN catalog_assets a ON a.token=pin.asset_id
 WHERE pin.session_id=?`, session).Scan(&library, &path, &container)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", false, storage.ErrPlaybackSource
	}
	if err != nil {
		return "", "", false, err
	}
	return library, path, container != "strm", nil
}

// resolvePlaybackEvidence is shared by acceptance and session-less catalogue
// reads. Session byte reads use only persisted evidence, never this selector.
func resolvePlaybackEvidence(ctx context.Context, q playbackQuery, v PlaybackSource, asset, source, incarnation string, generation int64) (*playbackEvidence, error) {
	var evidence playbackEvidence
	binding := &evidence.binding
	var valid, healthy bool
	var kind, health string
	err := q.QueryRowContext(ctx, `SELECT s.id,o.id,s.root,s.root_identity,s.follow_symlinks,o.relative_path,o.object_identity,s.kind,s.health,
 (`+playbackEvidenceSQL+`) AS evidence,
 (`+playbackEvidenceSQL+` AND s.health NOT IN('offline','root_changed','removing')) AS healthy
 FROM inventory_objects o JOIN library_sources s ON s.id=o.source_id
 WHERE o.asset_id=? AND (?='' OR s.id=?)
 ORDER BY (rtrim(s.root,?)||?||o.relative_path=?) DESC,evidence DESC,healthy DESC,(o.retired=0 AND o.state='available' AND o.root_incarnation=s.incarnation) DESC,s.id,o.id LIMIT 1`, v.Size, v.ModifiedNS, source, incarnation, generation, v.Size, v.ModifiedNS, source, incarnation, generation, asset, source, source, string(filepath.Separator), string(filepath.Separator), filepath.Clean(v.Path)).Scan(&evidence.source, &evidence.object, &binding.Inventory.Root, &binding.Inventory.RootIdentity, &binding.Inventory.FollowSymlinks, &binding.Inventory.RelativePath, &binding.ObjectIdentity, &kind, &health, &valid, &healthy)
	if errors.Is(err, sql.ErrNoRows) && source == "" {
		return nil, nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return nil, storage.ErrPlaybackSource
	}
	if err != nil {
		return nil, err
	}
	// Identity, lifetime, root and the exact file witness come first: an offline
	// source is named only when everything else about the binding still holds.
	if !valid || !validPlaybackBinding(*binding) || filepath.Join(binding.Inventory.Root, binding.Inventory.RelativePath) != filepath.Clean(v.Path) {
		return nil, storage.ErrPlaybackSource
	}
	if !healthy {
		if refusal := storage.SourceHealthRefusal(health); refusal != nil {
			return nil, refusal
		}
		return nil, storage.ErrPlaybackSource
	}
	// Provider IDs are not filesystem inodes. Remote bindings retain the managed
	// mount acquisition and size/mtime checks, without claiming object custody.
	binding.Mounted = kind == "remote"
	return &evidence, nil
}

// PinPlaybackBindingTx captures the inventory tuple in the session's acceptance
// transaction, constrained to the physical source selected by that transaction.
// Disc sessions carry their own custody and do not call this function.
func PinPlaybackBindingTx(ctx context.Context, tx *sql.Tx, session string) error {
	var v PlaybackSource
	var asset, source, incarnation string
	var generation int64
	err := tx.QueryRowContext(ctx, `SELECT a.path,pin.size,pin.modified_ns,pin.asset_id,COALESCE(physical.source_id,''),COALESCE(physical.incarnation,''),COALESCE(physical.configuration_generation,0)
 FROM playback_source_pins pin JOIN catalog_assets a ON a.token=pin.asset_id LEFT JOIN playback_physical_source_pins physical ON physical.session_id=pin.session_id WHERE pin.session_id=?`, session).Scan(&v.Path, &v.Size, &v.ModifiedNS, &asset, &source, &incarnation, &generation)
	if err != nil {
		return err
	}
	evidence, err := resolvePlaybackEvidence(ctx, tx, v, asset, source, incarnation, generation)
	if err != nil {
		return err
	}
	if evidence == nil {
		// No inventory object names this file, so the catalogue's folder
		// fallback decides whether it is available at all. Playback admits the
		// same fallback, and authorization plus the item/asset selection are
		// already behind us: a known matching source that is only offline is
		// named here, before a session can be kept.
		library, path, ok, e := playbackFallbackTarget(ctx, tx, session)
		if e != nil {
			return e
		}
		if !ok {
			return nil
		}
		return sourceFallbackRefusal(ctx, tx, library, path)
	}
	if source == "" || source != evidence.source {
		return storage.ErrPlaybackSource
	}
	binding := evidence.binding
	mode := "inventory"
	if binding.Mounted {
		mode = "mounted"
	}
	_, err = tx.ExecContext(ctx, `UPDATE playback_source_pins SET binding=?,source_id=?,inventory_object_id=?,root=?,root_identity=?,relative_path=?,object_identity=?,follow_symlinks=? WHERE session_id=?`, mode, evidence.source, evidence.object, binding.Inventory.Root, binding.Inventory.RootIdentity, binding.Inventory.RelativePath, binding.ObjectIdentity, binding.Inventory.FollowSymlinks, session)
	return err
}
