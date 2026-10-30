package subtitles

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// ValidationReceipt describes subtitle discovery only. No media facts,
// enrichment, artwork or scan policy are changed by this operation.
type ValidationReceipt struct {
	Sources int `json:"sources"`
}

const maxValidationSources = 32

type validationSource struct {
	Source
	object, revision, source, incarnation, identity, relative string
	generation                                                int64
	evidence                                                  string
	embedded                                                  []Track
	origin                                                    int64
	known                                                     bool
	inventory                                                 *Inventory
}
type validationIO struct {
	stat     func(context.Context, validationSource) (storage.Snapshot, error)
	discover func(context.Context, validationSource) (*Inventory, error)
}

// ValidateItem rereads only exact-basename subtitle sidecars, using embedded
// tracks from an unchanged scanned local asset. It never calls the extractor.
// All source reads finish before one authorized publication transaction.
func (s *Service) ValidateItem(ctx context.Context, p identity.Principal, item string) (ValidationReceipt, error) {
	if s.storage == nil {
		return ValidationReceipt{}, ErrUnavailable
	}
	io := validationIO{
		stat: func(ctx context.Context, v validationSource) (storage.Snapshot, error) {
			return s.storage.InventoryStat(ctx, v.source, storage.InventoryRequest{Root: v.root, RootIdentity: v.identity, RelativePath: v.relative, FollowSymlinks: false})
		},
		discover: func(ctx context.Context, v validationSource) (*Inventory, error) {
			return Discover(ctx, s.storage, v.source, v.path, v.embedded, v.origin, v.known)
		},
	}
	return s.validateItem(ctx, p, item, io)
}
func (s *Service) validateItem(ctx context.Context, p identity.Principal, item string, io validationIO) (ValidationReceipt, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	read, p, err := s.readTx(ctx, p, item)
	if err != nil {
		return ValidationReceipt{}, err
	}
	tx := read.Tx()
	rows, err := tx.QueryContext(ctx, `SELECT DISTINCT a.token FROM catalog_entities e JOIN catalog_asset_links l ON l.entity_id=e.id JOIN catalog_assets a ON a.id=l.asset_id WHERE e.public_id=pid_blob(?) AND a.video_codec<>'' AND a.available=1 AND l.available=1 ORDER BY a.token LIMIT ?`, item, maxValidationSources+1)
	if err != nil {
		read.Rollback()
		return ValidationReceipt{}, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			break
		}
		ids = append(ids, id)
	}
	err = errors.Join(err, rows.Err(), rows.Close())
	if err != nil {
		read.Rollback()
		return ValidationReceipt{}, err
	}
	if len(ids) == 0 {
		read.Rollback()
		return ValidationReceipt{}, ErrUnavailable
	}
	if len(ids) > maxValidationSources {
		read.Rollback()
		return ValidationReceipt{}, ErrCapacity
	}
	sources := make([]validationSource, 0, len(ids))
	for _, id := range ids {
		v, e := s.validationSource(ctx, tx, item, id)
		if e != nil {
			read.Rollback()
			return ValidationReceipt{}, e
		}
		v.embedded, v.origin, v.known, e = storedValidationTracks(ctx, tx, id, v.size, v.modified)
		if e != nil {
			read.Rollback()
			return ValidationReceipt{}, e
		}
		sources = append(sources, v)
	}
	read.Rollback()
	for i := range sources {
		v := &sources[i]
		before, e := io.stat(ctx, *v)
		if e != nil || before.Size != v.size || before.ModifiedNS != v.modified {
			return ValidationReceipt{}, ErrConflict
		}
		inventory, e := io.discover(ctx, *v)
		if e != nil {
			return ValidationReceipt{}, e
		}
		if inventory == nil || inventory.Status != "known" {
			return ValidationReceipt{}, ErrUnavailable
		}
		if len(inventory.Tracks) > MaxTracks {
			return ValidationReceipt{}, ErrCapacity
		}
		after, e := io.stat(ctx, *v)
		if e != nil || after.Size != before.Size || after.ModifiedNS != before.ModifiedNS || after.ObjectIdentity != before.ObjectIdentity || after.Revision != before.Revision {
			return ValidationReceipt{}, ErrConflict
		}
		inventory.SourceEvidence = v.evidence
		v.inventory = inventory
	}
	write, p, err := s.tx(ctx, p, item)
	if err != nil {
		return ValidationReceipt{}, err
	}
	defer write.Rollback()
	for _, v := range sources {
		now, e := s.validationSource(ctx, write.Tx(), item, v.ID)
		if e != nil {
			return ValidationReceipt{}, e
		}
		if now.root != v.root || now.relative != v.relative || now.path != v.path || now.size != v.size || now.modified != v.modified || now.facts != v.facts || now.InventoryRevision != v.InventoryRevision || now.object != v.object || now.revision != v.revision || now.source != v.source || now.generation != v.generation || now.incarnation != v.incarnation || now.identity != v.identity || now.evidence != v.evidence {
			return ValidationReceipt{}, ErrConflict
		}
		if e = Persist(write.Tx(), v.ID, v.size, v.modified, v.inventory); e != nil {
			return ValidationReceipt{}, e
		}
	}
	if err = write.Commit(); err != nil {
		return ValidationReceipt{}, err
	}
	return ValidationReceipt{Sources: len(sources)}, nil
}
func (s *Service) validationSource(ctx context.Context, tx *sql.Tx, item, id string) (validationSource, error) {
	src, err := sourceQuery(ctx, tx, item, id)
	if err != nil {
		return validationSource{}, err
	}
	v := validationSource{Source: src}
	if !src.Available || src.container == "strm" || s.storage.IsRemote(src.path) {
		return v, ErrUnsupported
	}
	// Require an exact native source witness. Legacy/uninventoried, disc and
	// symlink-following sources cannot silently fall back to a media probe.
	err = tx.QueryRowContext(ctx, `SELECT o.id,o.revision,s.id,s.generation,s.incarnation,s.root_identity,o.relative_path,COALESCE(ev.evidence,'') FROM inventory_objects o JOIN library_sources s ON s.id=o.source_id LEFT JOIN subtitle_inventory_evidence ev ON ev.asset_id=o.asset_id WHERE o.asset_id=? AND o.retired=0 AND o.state='available' AND o.root_incarnation=s.incarnation AND o.size=? AND o.modified_ns=? AND s.enabled=1 AND s.kind='local' AND s.follow_symlinks=0 AND s.identity_confirmed=1 AND s.root_identity<>'' AND s.health NOT IN('root_changed','removing') ORDER BY s.id LIMIT 1`, id, src.size, src.modified).Scan(&v.object, &v.revision, &v.source, &v.generation, &v.incarnation, &v.identity, &v.relative, &v.evidence)
	if errors.Is(err, sql.ErrNoRows) {
		return v, ErrUnavailable
	}
	if err != nil {
		return v, err
	}
	if !filepath.IsLocal(v.relative) || filepath.Clean(filepath.Join(src.root, v.relative)) != filepath.Clean(src.path) {
		return v, ErrConflict
	}
	return v, nil
}
func storedValidationTracks(ctx context.Context, q query, id string, size, modified int64) ([]Track, int64, bool, error) {
	var origin int64
	var known bool
	var status string
	err := q.QueryRowContext(ctx, `SELECT origin_us,timing_known,status FROM asset_subtitle_facts WHERE asset_id=? AND size=? AND modified_ns=?`, id, size, modified).Scan(&origin, &known, &status)
	if errors.Is(err, sql.ErrNoRows) || err == nil && status != "known" {
		return nil, 0, false, ErrUnavailable
	}
	if err != nil {
		return nil, 0, false, err
	}
	rows, err := q.QueryContext(ctx, `SELECT format,language,title,is_default,is_forced,reason,stream_index,hearing_impaired FROM asset_subtitles WHERE asset_id=? AND origin='embedded' ORDER BY stream_index LIMIT ?`, id, MaxTracks+1)
	if err != nil {
		return nil, 0, false, err
	}
	defer rows.Close()
	tracks := []Track{}
	for rows.Next() {
		t := Track{Origin: "embedded"}
		if err = rows.Scan(&t.Format, &t.Language, &t.Title, &t.Default, &t.Forced, &t.Reason, &t.StreamIndex, &t.HearingImpaired); err != nil {
			return nil, 0, false, err
		}
		tracks = append(tracks, t)
	}
	if err = rows.Err(); err != nil {
		return nil, 0, false, err
	}
	if len(tracks) > MaxTracks {
		return nil, 0, false, ErrCapacity
	}
	return tracks, origin, known, nil
}
