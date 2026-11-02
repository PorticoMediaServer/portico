package ingestion

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"path/filepath"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
	workerloop "github.com/PorticoMediaServer/portico/server/internal/worker"
)

const pathPresenceWindow = 16

// A source's configured path survives moves between machines and filesystems.
// Physical identities still fence an individual open, but are not durable
// admission policy. This worker never lists the catalogue's content again or
// queues a scan: it only rebinds present files' physical observations.
type pathPresence struct {
	s       *Service
	after   string
	current *pathPresenceWalk
}

type pathPresenceWalk struct {
	source    catalog.LibrarySource
	after     string
	fence     string
	present   int
	examined  int
	uncertain bool
}

type presenceObject struct {
	id, path, revision, incarnation, object string
	size, modified                          int64
}

func (s *Service) runPathPresence(ctx context.Context) {
	p := &pathPresence{s: s}
	workerloop.Run(ctx, "ingestion.path-presence", nil, p.step)
}

func (p *pathPresence) step(ctx context.Context) time.Duration {
	ctx = dbwork.WithClass(ctx, dbwork.ClassMaintenance)
	if err := p.advance(ctx); err != nil {
		if errors.Is(err, errVerificationStale) {
			p.current = nil
		}
		if ctx.Err() == nil {
			log.Printf("Source presence: %v", err)
		}
		return 15 * time.Second
	}
	if p.current == nil && p.after == "" {
		return time.Minute
	}
	return 10 * time.Millisecond
}

func (p *pathPresence) advance(ctx context.Context) error {
	if p.current == nil {
		var id string
		err := p.s.db.QueryRowContext(ctx, `SELECT id FROM library_sources s WHERE id>? AND enabled=1 AND kind='local' AND remote_source_id IS NULL AND health!='removing'
 AND NOT(health='root_changed' AND identity_confirmed=0)
 AND NOT EXISTS(SELECT 1 FROM inventory_source_active a WHERE a.source_id=s.id)
 AND NOT EXISTS(SELECT 1 FROM dvr_private_libraries d WHERE d.library_id=s.library_id)
 ORDER BY id LIMIT 1`, p.after).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			p.after = ""
			return nil
		}
		if err != nil {
			return err
		}
		p.after = id
		source, err := p.s.catalog.LibrarySource(ctx, id)
		if err != nil {
			return err
		}
		if p.s.storage != nil && p.s.storage.IsRemote(source.ResolvedPath) {
			return nil
		}
		check, cancel := context.WithTimeout(ctx, 2*time.Second)
		var root storage.Snapshot
		if p.s.storage != nil {
			root, err = p.s.storage.InspectRoot(check, source.ResolvedPath)
		} else {
			root, err = storage.LocalInventoryDirectory(storage.InventoryRequest{Root: source.ResolvedPath, RelativePath: "."})
		}
		cancel()
		if err != nil {
			// A transport failure says nothing about individual files.
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, storage.ErrBusy) {
				return err
			}
			return p.health(ctx, source, "offline", source.IdentityConfirmed, source.RootIdentity)
		}
		observed := storage.RootIdentity(root)
		if observed == "" || !root.Directory {
			log.Printf("Source %s: root inspection did not prove a readable directory (code %s)", source.ID, root.ErrorCode)
			return p.health(ctx, source, "offline", source.IdentityConfirmed, source.RootIdentity)
		}
		if observed == source.RootIdentity && (source.Health == "ready" || source.Health == "healthy") {
			var incarnation string
			if source.Generation <= 1 || source.LastCompleteAt != "" {
				return nil
			}
			err := p.s.db.QueryRowContext(ctx, `SELECT root_incarnation FROM inventory_objects INDEXED BY inventory_objects_source WHERE source_id=? AND retired=0 ORDER BY id LIMIT 1`, source.ID).Scan(&incarnation)
			if errors.Is(err, sql.ErrNoRows) || err == nil && incarnation == source.Incarnation {
				return nil
			}
			if err != nil {
				return err
			}
		}
		if observed != source.RootIdentity {
			log.Printf("Source %s: filesystem identity changed at the configured path; checking presence", source.ID)
		}
		// Degraded remains usable and makes an interrupted presence walk resume
		// after restart. No incarnation or configuration generation is changed.
		if err := p.health(ctx, source, "degraded", true, observed); err != nil {
			return err
		}
		source.RootIdentity, source.Health = observed, "degraded"
		var fence string
		err = p.s.db.QueryRowContext(ctx, `SELECT id FROM inventory_objects INDEXED BY inventory_objects_source WHERE source_id=? AND retired=0 ORDER BY id DESC LIMIT 1`, source.ID).Scan(&fence)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		p.current = &pathPresenceWalk{source: source, fence: fence}
	}
	w := p.current
	rows, err := p.s.db.QueryContext(ctx, `SELECT id,relative_path,revision,root_incarnation,object_identity,size,modified_ns FROM inventory_objects INDEXED BY inventory_objects_source
 WHERE source_id=? AND retired=0 AND id>? AND id<=? ORDER BY id LIMIT ?`, w.source.ID, w.after, w.fence, pathPresenceWindow)
	if err != nil {
		return err
	}
	var objects []presenceObject
	for rows.Next() {
		var o presenceObject
		if err := rows.Scan(&o.id, &o.path, &o.revision, &o.incarnation, &o.object, &o.size, &o.modified); err != nil {
			rows.Close()
			return err
		}
		objects = append(objects, o)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, o := range objects {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		r := storage.InventoryRequest{Root: w.source.ResolvedPath, RootIdentity: w.source.RootIdentity, RelativePath: o.path, FollowSymlinks: w.source.FollowSymlinks}
		check, cancel := context.WithTimeout(ctx, 2*time.Second)
		var observed storage.Snapshot
		if p.s.storage != nil {
			observed, err = p.s.storage.InventoryStat(check, "presence:"+w.source.ID, r)
		} else {
			observed, err = storage.LocalInventoryStat(r)
		}
		cancel()
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, storage.ErrBusy) {
			return err
		}
		if err == nil && !observed.Directory {
			w.present++
			if err := p.accept(ctx, w.source, o, observed); err != nil {
				return err
			}
		} else {
			// An absence check only distinguishes attention from an inconclusive
			// outage. Neither answer ever marks a catalogue object missing.
			check, cancel = context.WithTimeout(ctx, 2*time.Second)
			var absent bool
			if p.s.storage != nil {
				absent, err = p.s.storage.InventoryAbsence(check, "presence:"+w.source.ID, r)
			} else {
				absent, err = storage.LocalInventoryAbsence(r)
			}
			cancel()
			w.uncertain = w.uncertain || err != nil || !absent
		}
		w.examined++
		w.after = o.id
	}
	if len(objects) == pathPresenceWindow {
		return nil
	}
	health, confirmed := "ready", true
	if w.examined > 0 && w.present == 0 {
		health = "offline"
		if !w.uncertain && p.differentMedia(ctx, w) {
			health, confirmed = "root_changed", false
		}
	}
	if err := p.health(ctx, w.source, health, confirmed, w.source.RootIdentity); err != nil {
		return err
	}
	p.current = nil
	return nil
}

func (p *pathPresence) health(ctx context.Context, source catalog.LibrarySource, health string, confirmed bool, rootID string) error {
	return dbwork.WithWriteTx(ctx, p.s.db, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE library_sources SET health=?,identity_confirmed=?,root_identity=? WHERE id=? AND generation=? AND configured_root=? AND root=? AND enabled=1 AND NOT EXISTS(SELECT 1 FROM inventory_source_active a WHERE a.source_id=library_sources.id)`, health, confirmed, rootID, source.ID, source.Generation, source.Path, source.ResolvedPath)
		if err != nil {
			return err
		}
		if n, _ := result.RowsAffected(); n != 1 {
			return errVerificationStale
		}
		return nil
	})
}

func (p *pathPresence) accept(ctx context.Context, source catalog.LibrarySource, o presenceObject, v storage.Snapshot) error {
	v.ObjectIdentity = storage.StableObjectIdentity(v.ObjectIdentity, source.RootIdentity)
	if o.incarnation == source.Incarnation && o.object == v.ObjectIdentity {
		return nil
	}
	// A presence check can carry existing content analysis only when its file
	// size and modification time still match. It never fabricates new facts.
	if o.size != v.Size || o.modified != v.ModifiedNS {
		return nil
	}
	revision, evidence, err := catalog.ObservationRevision(source, v)
	if err != nil {
		return err
	}
	return dbwork.WithWriteTx(dbwork.WithCatalogIngest(ctx), p.s.db, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE inventory_objects SET root_incarnation=?,object_identity=?,revision=?,evidence_json=?,analysis_revision=CASE WHEN analysis_revision=revision THEN ? ELSE analysis_revision END
 WHERE id=? AND source_id=? AND revision=? AND retired=0 AND EXISTS(SELECT 1 FROM library_sources s WHERE s.id=? AND s.generation=? AND s.configured_root=? AND s.root=? AND s.root_identity=? AND s.enabled=1 AND NOT EXISTS(SELECT 1 FROM inventory_source_active a WHERE a.source_id=s.id))`, source.Incarnation, v.ObjectIdentity, revision, evidence, revision, o.id, source.ID, o.revision, source.ID, source.Generation, source.Path, source.ResolvedPath, source.RootIdentity)
		return err
	})
}

// A bounded root page can positively identify different media. Exhaustion or
// inconclusive nested folders retain offline/assume-present rather than guess.
func (p *pathPresence) differentMedia(ctx context.Context, w *pathPresenceWalk) bool {
	r := storage.InventoryRequest{Root: w.source.ResolvedPath, RootIdentity: w.source.RootIdentity, RelativePath: ".", Limit: 32, FollowSymlinks: w.source.FollowSymlinks}
	check, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	var page storage.InventoryPage
	var err error
	if p.s.storage != nil {
		page, err = p.s.storage.InventoryPage(check, "presence:"+w.source.ID, r)
	} else {
		page, err = storage.LocalInventoryPage(r)
	}
	if err != nil {
		return false
	}
	for _, entry := range page.Entries {
		if entry.Directory || !strings.Contains("|.mp4|.mkv|.avi|.mov|.m4v|.mp3|.flac|.m4a|.m4b|.epub|.pdf|.cbz|.cbr|", "|"+strings.ToLower(filepath.Ext(entry.Name))+"|") {
			continue
		}
		var known bool
		if err := p.s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM inventory_objects WHERE source_id=? AND retired=0 AND relative_path=?)`, w.source.ID, entry.Name).Scan(&known); err == nil && !known {
			return true
		}
	}
	return false
}
