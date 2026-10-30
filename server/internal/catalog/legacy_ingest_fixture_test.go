package catalog

// Legacy direct-ingest fixture adapter. Production scan, media and playback
// writers publish through receipt-bearing FI1 source adapters instead. Keeping
// this fixture local avoids an ungated alternate canonical writer in the app.
import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
	"github.com/PorticoMediaServer/portico/server/internal/subtitles"
)

func (s *Service) CommitMovie(ctx context.Context, job, library, path string, f assets.Facts) error {
	return s.CommitMedia(ctx, job, library, path, f)
}
func (s *Service) CommitMedia(ctx context.Context, job, library, path string, f assets.Facts) error {
	var size, modified int64
	var revision string
	var e error
	if s.storage != nil {
		var observed storage.Snapshot
		observed, e = s.storage.Stat(ctx, library, path)
		size, modified = observed.Size, observed.ModifiedNS
		revision = observed.Revision
	} else {
		var info os.FileInfo
		info, e = os.Stat(path)
		if e == nil {
			size, modified = info.Size(), info.ModTime().UnixNano()
		}
	}
	if e != nil {
		return e
	}
	if f.ObservedRevision != "" && revision != f.ObservedRevision {
		return storage.ErrRemoteChanged
	}
	if f.ObservedModifiedNS != 0 && (f.ObservedSize != size || f.ObservedModifiedNS != modified) {
		return errors.New("source changed during probe; retry the scan")
	}
	gated3, e := dbwork.Begin(ctx, s.db, dbwork.ClassFrom(ctx, dbwork.ClassInteractive))
	if e != nil {
		return e
	}
	tx := gated3.Tx()
	defer gated3.Rollback()
	var status string
	if e = tx.QueryRow(`SELECT status FROM jobs WHERE id=?`, job).Scan(&status); e != nil {
		return e
	}
	if status != "running" {
		return context.Canceled
	}
	if f.InventoryOnly && s.storage != nil && s.storage.IsRemote(path) {
		var prior string
		var associated bool
		var known int64
		e = tx.QueryRowContext(ctx, `SELECT a.id,r.revision,EXISTS(SELECT 1 FROM catalog_asset_links l INDEXED BY catalog_asset_links_asset JOIN catalog_entities i ON i.id=l.entity_id JOIN catalog_libraries cl ON cl.id=i.library_id WHERE l.asset_id=a.id AND cl.library_id=?) FROM catalog_assets a JOIN remote_object_refs r ON r.path=a.path WHERE a.path=? AND a.size=? AND a.modified_ns=?`, library, path, size, modified).Scan(&known, &prior, &associated)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		if e == nil && prior == revision && associated {
			if e = s.storage.Remote.RecordReference(ctx, tx, path, revision); e != nil {
				return e
			}
			if e = compactcatalog.SetAssetAvailableTx(ctx, tx, known, true); e != nil {
				return e
			}
			if _, e = tx.ExecContext(ctx, `UPDATE scan_queue SET status='done' WHERE job_id=? AND path=? AND status='pending'`, job, path); e != nil {
				return e
			}
			if _, e = tx.ExecContext(ctx, `UPDATE jobs SET processed=processed+1 WHERE id=?`, job); e != nil {
				return e
			}
			return gated3.Commit()
		}
	}
	if s.storage != nil && s.storage.IsRemote(path) {
		if e = s.storage.Remote.RecordReference(ctx, tx, path, revision); e != nil {
			return e
		}
	}
	assetID, aid, e := compactcatalog.UpsertAssetTx(ctx, tx, compactcatalog.Asset{Path: path, Size: size, ModifiedNS: modified, Container: f.Container, VideoCodec: f.VideoCodec, AudioCodec: f.AudioCodec, Width: f.Width, Height: f.Height, Duration: f.Duration})
	if e != nil {
		return e
	}
	if e = subtitles.Persist(tx, aid, size, modified, f.SubtitleInventory); e != nil {
		return e
	}
	if e = assets.PersistStreams(tx, aid, size, modified, f.Streams); e != nil {
		return e
	}
	if e = assets.PersistChapters(tx, aid, size, modified, f); e != nil {
		return e
	}
	var kind, root string
	if e = tx.QueryRow(`SELECT kind,root FROM libraries WHERE id=?`, library).Scan(&kind, &root); e != nil {
		return e
	}
	if kind == "book" || kind == "comic" {
		if _, e = attachReadingTx(ctx, tx, library, kind, aid, path); e != nil {
			return e
		}
		return gated3.Commit()
	}
	if kind != "music" && kind != "audiobook" && f.VideoCodec == "" && f.Container != "strm" && !f.InventoryOnly {
		return errors.New("video library source has no timed video stream")
	}
	if kind == "music" || kind == "audiobook" {
		if e = s.commitAudio(ctx, tx, library, kind, root, aid, path, f); e != nil {
			return e
		}
	} else if kind == "tv" || kind == "anime" {
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		plan := ParseEpisode(relative, kind)
		if e = s.commitEpisodes(tx, library, aid, filepath.Base(path), plan, false); e != nil {
			return e
		}
	} else {
		item, err := commitMovieItem(ctx, tx, library, assetID, path)
		if err != nil {
			return err
		}
		if f.ArtworkKey != "" {
			if e = compactcatalog.SetFieldsTx(ctx, tx, item, compactcatalog.Automatic, map[string]any{"poster_url": "local:" + f.ArtworkKey}); e != nil {
				return e
			}
		}
	}
	if len(f.ArtworkExtra) > 0 && (kind == "movie" || kind == "tv" || kind == "anime") {
		if e = s.commitAssetSidecars(ctx, tx, library, kind, aid, f.ArtworkExtra); e != nil {
			return e
		}
	}
	if _, e = tx.Exec(`UPDATE scan_queue SET status='done' WHERE job_id=? AND path=? AND status='pending'`, job, path); e != nil {
		return e
	}
	if _, e = tx.Exec(`UPDATE jobs SET processed=processed+1 WHERE id=?`, job); e != nil {
		return e
	}
	return gated3.Commit()
}
