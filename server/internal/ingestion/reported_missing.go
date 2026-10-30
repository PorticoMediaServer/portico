package ingestion

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

type reportedMissing struct{ source, incarnation, identity, object, revision string }

// Inspect outside the writer. Only a known file, absent inside its verified
// root with a readable parent, supplies evidence of removal. Offline roots
// and unknown paths remain ordinary queued notices.
func (s *Service) removedPathEvidence(ctx context.Context, paths []string) ([]reportedMissing, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,configured_root,root,incarnation,root_identity,follow_symlinks FROM library_sources WHERE enabled=1 AND health NOT IN('root_changed','removing') AND NOT EXISTS(SELECT 1 FROM dvr_private_libraries d WHERE d.library_id=library_sources.library_id) LIMIT 4096`)
	if err != nil {
		return nil, err
	}
	type root struct {
		id, configured, path, incarnation, identity string
		follow                                      bool
	}
	roots := []root{}
	for rows.Next() {
		var r root
		if err = rows.Scan(&r.id, &r.configured, &r.path, &r.incarnation, &r.identity, &r.follow); err != nil {
			rows.Close()
			return nil, err
		}
		roots = append(roots, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	out := []reportedMissing{}
	// An inconclusive or slow proof leaves the ordinary queued scan in charge.
	proof, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	for _, raw := range paths {
		path, ok := cleanReportedPath(raw)
		if !ok {
			continue
		}
		for _, r := range roots {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if proof.Err() != nil {
				return out, nil
			}
			relative, inside := relativeTo(r.path, path)
			if !inside {
				relative, inside = relativeTo(r.configured, path)
			}
			if !inside || relative == "." || r.identity == "" {
				continue
			}
			var evidence reportedMissing
			err = s.db.QueryRowContext(proof, `SELECT id,revision FROM inventory_objects WHERE source_id=? AND relative_path=? AND retired=0 AND root_incarnation=? AND state='available'`, r.id, relative, r.incarnation).Scan(&evidence.object, &evidence.revision)
			if proof.Err() != nil {
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				return out, nil
			}
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return nil, err
			}
			req := storage.InventoryRequest{Root: r.path, RootIdentity: r.identity, RelativePath: relative, FollowSymlinks: r.follow}
			var absent bool
			if s.storage != nil {
				absent, err = s.storage.InventoryAbsence(proof, r.id, req)
			} else {
				absent, err = storage.LocalInventoryAbsence(req)
			}
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if proof.Err() != nil {
				return out, nil
			}
			if err != nil || !absent {
				continue
			}
			evidence.source, evidence.incarnation, evidence.identity = r.id, r.incarnation, r.identity
			out = append(out, evidence)
			if len(out) >= MaxReportedPaths {
				return out, nil
			}
		}
	}
	return out, nil
}
func applyReportedMissingTx(ctx context.Context, tx *sql.Tx, evidence []reportedMissing) error {
	for _, v := range evidence {
		var current bool
		err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM inventory_objects o JOIN library_sources s ON s.id=o.source_id WHERE o.id=? AND o.revision=? AND o.retired=0 AND o.root_incarnation=? AND s.incarnation=? AND s.root_identity=? AND s.enabled=1 AND s.health NOT IN('root_changed','removing'))`, v.object, v.revision, v.incarnation, v.incarnation, v.identity).Scan(&current)
		if err != nil {
			return err
		}
		if !current {
			continue
		}
		if _, err = tx.ExecContext(ctx, `UPDATE library_sources SET health='ready' WHERE id=? AND health='offline'`, v.source); err != nil {
			return err
		}
		if err = catalog.MarkInventoryMissingTx(ctx, tx, v.source, "path-report:"+v.revision, v.object, time.Now()); err != nil {
			return err
		}
		var active, relative string
		err = tx.QueryRowContext(ctx, `SELECT a.job_id,o.relative_path FROM inventory_source_active a JOIN inventory_objects o ON o.source_id=a.source_id WHERE a.source_id=? AND o.id=?`, v.source, v.object).Scan(&active, &relative)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil {
			// The run may still hold an older listing page in memory or replay.
			if err = catalog.InventoryIssueTx(ctx, tx, active, storage.InventoryIssue{Path: relative, Reason: "reported_file_removed", Protect: true}); err != nil {
				return err
			}
		}
		if _, err = tx.ExecContext(ctx, `UPDATE library_revisions SET revision=revision+1 WHERE library_id=(SELECT library_id FROM library_sources WHERE id=?)`, v.source); err != nil {
			return err
		}
	}
	return nil
}
