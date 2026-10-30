package catalog

import (
	"context"
	"database/sql"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// One file's accepted tags, chapter/cue observations and artwork publish under
// one inventory revision. Splitting them would expose a mixed file baseline.
// This is a per-file budget, measured by dbwork; the interactive default stays
// unchanged. Pathological chapter/cue fan-out remains an overrun to investigate.
var audioFilePublicationPlan = dbwork.WritePlan{Name: "audio-file-publication", Rows: 1024, Pages: 256, VMOps: 500_000, Why: "one file's bounded tags, chapter/cue facts, artwork and current targets must publish atomically with its inventory revision"}

// persistAudioFileMetadata records only what was observed before sidecars were
// applied, in the same transaction and revision fence as the full analysis.
func persistAudioFileMetadata(ctx context.Context, tx *sql.Tx, asset, revision string, size, modified int64, f assets.Facts) error {
	b := f.AudioFileMetadata
	if b == nil {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO audio_file_metadata VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(asset_id) DO UPDATE SET source_revision=excluded.source_revision,size=excluded.size,modified_ns=excluded.modified_ns,chapter_status=excluded.chapter_status,artwork_key=excluded.artwork_key,issue=excluded.issue,artwork_issue=excluded.artwork_issue`, asset, revision, size, modified, b.ChapterStatus, b.ArtworkKey, b.Issue, b.ArtworkIssue); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM audio_file_tags WHERE asset_id=?`, asset); err != nil {
		return err
	}
	for field, value := range b.Tags {
		if _, err := tx.ExecContext(ctx, `INSERT INTO audio_file_tags VALUES(?,?,?)`, asset, field, value); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM audio_file_chapters WHERE asset_id=?`, asset); err != nil {
		return err
	}
	for kind, chapters := range map[string][]assets.Chapter{"chapter": b.Chapters, "cue": b.MusicCueTracks} {
		for n, chapter := range chapters {
			if _, err := tx.ExecContext(ctx, `INSERT INTO audio_file_chapters VALUES(?,?,?,?,?,?)`, asset, kind, n, chapter.Title, chapter.Start, chapter.End); err != nil {
				return err
			}
		}
	}

	return nil
}

// InventoryAudioFileMetadata returns a file-owned baseline only for the exact
// analysed inventory revision. Older analyses without a baseline must probe.
// Technical facts are read for projection, never republished by the refresh.
func (s *Service) InventoryAudioFileMetadata(ctx context.Context, object, revision string) (*assets.Facts, error) {
	snapshot, err := dbwork.BeginSnapshot(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer snapshot.Rollback()
	tx := snapshot.Tx()

	var f assets.Facts
	var asset, library, artworkIssue string
	err = tx.QueryRowContext(ctx, `SELECT a.token,src.library_id,a.audio_codec,a.duration,b.chapter_status,b.artwork_key,b.issue,b.artwork_issue
 FROM inventory_objects o JOIN library_sources src ON src.id=o.source_id
 JOIN catalog_assets a ON a.token=o.asset_id AND a.size=o.size AND a.modified_ns=o.modified_ns
 JOIN audio_file_metadata b ON b.asset_id=a.token AND b.source_revision=o.revision AND b.size=o.size AND b.modified_ns=o.modified_ns
 WHERE o.id=? AND o.revision=? AND o.analysis_revision=o.revision AND o.retired=0 AND o.state='available'`, object, revision).Scan(&asset, &library, &f.AudioCodec, &f.Duration, &f.ChapterStatus, &f.ArtworkKey, &f.LocalMetadataIssue, &artworkIssue)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	f.AudioFileMetadata = &assets.AudioFileMetadata{ArtworkIssue: artworkIssue}
	f.Tags, f.TagSources = map[string]string{}, map[string]string{}
	rows, err := tx.QueryContext(ctx, `SELECT field,value FROM audio_file_tags WHERE asset_id=? ORDER BY field`, asset)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var field, value string
		if err = rows.Scan(&field, &value); err != nil {
			rows.Close()
			return nil, err
		}
		f.Tags[field], f.TagSources[field] = value, "embedded"
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	rows, err = tx.QueryContext(ctx, `SELECT kind,title,start_seconds,end_seconds FROM audio_file_chapters WHERE asset_id=? ORDER BY kind,ordinal`, asset)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var c assets.Chapter
		var kind string
		if err = rows.Scan(&kind, &c.Title, &c.Start, &c.End); err != nil {
			rows.Close()
			return nil, err
		}
		if kind == "cue" {
			f.MusicCueTracks = append(f.MusicCueTracks, c)
		} else {
			f.Chapters = append(f.Chapters, c)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	rows, err = tx.QueryContext(ctx, `SELECT field,value,source FROM audio_local_evidence WHERE library_id=? AND asset_id=? AND source='embedded' AND source_revision=? ORDER BY ordinal`, library, asset, revision)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var v assets.AudioTagEvidence
		if err = rows.Scan(&v.Field, &v.Value, &v.Source); err != nil {
			rows.Close()
			return nil, err
		}
		f.AudioEvidence = append(f.AudioEvidence, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	return &f, snapshot.Commit()
}

// Shared by full analysis and sidecar refresh: owner-linked versions keep their
// identity, while ordinary audio uses the existing grouping and owner policy.
func (s *Service) commitInventoryAudioTx(ctx context.Context, tx *sql.Tx, source LibrarySource, asset, kind, path string, f assets.Facts) error {
	var linked bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM inventory_version_links WHERE library_id=? AND asset_id=?)`, source.LibraryID, asset).Scan(&linked); err != nil {
		return err
	}
	if !linked {
		return s.commitAudio(ctx, tx, source.LibraryID, kind, source.ResolvedPath, asset, path, f)
	}
	if err := persistAudioEvidence(tx, source.LibraryID, asset, f); err != nil {
		return err
	}
	var item int64
	err := tx.QueryRowContext(ctx, `SELECT i.id FROM catalog_assets a JOIN catalog_asset_links l INDEXED BY catalog_asset_links_asset ON l.asset_id=a.id
 JOIN catalog_entities i ON i.id=l.entity_id JOIN catalog_kinds k ON k.id=i.kind AND k.playable=1 AND k.listening=1 JOIN catalog_libraries cl ON cl.id=i.library_id
 WHERE a.token=? AND cl.library_id=? LIMIT 1`, asset, source.LibraryID).Scan(&item)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if item != 0 {
		// A manual version link keeps naming/grouping, but the file's current
		// cover still participates in complete artwork observation.
		if f.ArtworkKey != "" {
			if err := compactcatalog.SetFieldsTx(ctx, tx, item, compactcatalog.Automatic, map[string]any{"poster_url": "local:" + f.ArtworkKey}); err != nil {
				return err
			}
			if err := commitSidecars(ctx, tx, source.LibraryID, []sidecarCarrier{{"item", item, "poster", f.ArtworkKey}}); err != nil {
				return err
			}
		}
		// Retain unchanged group/artist images this file already supplied.
		// Resolving new group identities here would override the manual link.
		rows, err := tx.QueryContext(ctx, `SELECT kind,entity_id,role,art_key FROM inventory_artwork_sources WHERE asset_id=? AND kind<>'item'`, asset)
		if err != nil {
			return err
		}
		kept := []sidecarCarrier{}
		for rows.Next() {
			var c sidecarCarrier
			if err = rows.Scan(&c.kind, &c.entity, &c.role, &c.key); err != nil {
				break
			}
			if f.ArtworkExtra[c.kind+"/"+c.role] == c.key {
				kept = append(kept, c)
			}
		}
		if err == nil {
			err = rows.Err()
		}
		closeErr := rows.Close()
		if err == nil {
			err = closeErr
		}
		if err != nil {
			return err
		}
		if err = commitSidecars(ctx, tx, source.LibraryID, kept); err != nil {
			return err
		}
		return compactcatalog.ProjectAudioLocalGenresItem(ctx, tx, source.LibraryID, item)
	}
	return nil
}
