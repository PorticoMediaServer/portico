package metadata

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// Relationship provenance follows normalized IDs. Display-name equality is never
// identity evidence. Hierarchy edits belong to the existing Identify publishers.
func readRepairRelationships(ctx context.Context, tx *sql.Tx, t RepairTarget) ([]RepairRelationship, error) {
	out := []RepairRelationship{}
	entity, err := resolveEntity(ctx, tx, t.ID)
	if err != nil {
		return out, err
	}
	queries := []struct {
		q    string
		args []any
	}{}
	add := func(q string, args ...any) {
		queries = append(queries, struct {
			q    string
			args []any
		}{q, args})
	}
	switch t.Kind {
	case "item":
		add(`SELECT 'episode_show','show',pid(sh.public_id),sh.title,e.ordering_basis,0,CASE WHEN e.local_identity_status='manual' THEN 'manual' ELSE 'scanner' END,e.local_identity_status='manual','','','','' FROM catalog_episodes e JOIN catalog_entities sh ON sh.id=e.show_id WHERE e.entity_id=?`, entity)
		add(`SELECT 'track_album','album',pid(a.public_id),a.title,'track',COALESCE(s.track_number,0),'embedded',0,'','','','' FROM catalog_songs s JOIN catalog_entities a ON a.id=s.album_id WHERE s.entity_id=?`, entity)
		add(`SELECT 'book_part','book',pid(b.public_id),b.title,'part',COALESCE(f.part_number,0),'embedded',0,'','','','' FROM catalog_book_files f JOIN catalog_entities b ON b.id=f.book_id WHERE f.entity_id=?`, entity)
		add(`SELECT 'credit',CASE WHEN c.person_id IS NULL THEN 'scoped_credit' ELSE 'person' END,COALESCE(pp.token,c.credit_id),COALESCE(NULLIF(c.credited_name,''),pp.name,c.credit_id),rl.label,c.ord,c.provider,0,'',c.credit_id,c.provider,dl.label FROM catalog_credits c LEFT JOIN catalog_people pp ON pp.id=c.person_id LEFT JOIN catalog_credit_labels rl ON rl.id=c.role_id LEFT JOIN catalog_credit_labels dl ON dl.id=c.department_id WHERE c.entity_id=? ORDER BY c.ord`, entity)
		add(`SELECT 'genre','genre',ts.source_id,COALESCE(ts.label_override,t.label),'',0,ts.provider,0,'',ts.source_id,ts.provider,'' FROM catalog_entity_terms et JOIN catalog_terms t ON t.id=et.term_id JOIN catalog_term_sources ts ON ts.entity_id=et.entity_id AND ts.term_id=et.term_id WHERE et.entity_id=? AND t.vocab=1 ORDER BY ts.provider,ts.source_id`, entity)
	case "album":
		add(`SELECT 'album_artist','artist',pid(a.public_id),a.title,'album artist',0,'embedded',0,'','','','' FROM catalog_albums b JOIN catalog_entities a ON a.id=b.artist_id WHERE b.entity_id=?`, entity)
	case "season":
		add(`SELECT 'season_show','show',pid(s.public_id),s.title,'season',n.number,'scanner',0,'','','','' FROM catalog_seasons n JOIN catalog_entities s ON s.id=n.show_id WHERE n.entity_id=?`, entity)
	}
	for _, q := range queries {
		rows, err := tx.QueryContext(ctx, q.q, q.args...)
		if err != nil {
			return out, err
		}
		for rows.Next() {
			var r RepairRelationship
			if err = rows.Scan(&r.Kind, &r.TargetKind, &r.TargetID, &r.Label, &r.Role, &r.Ordinal, &r.Source, &r.Locked, &r.Observed, &r.RecordID, &r.Provider, &r.Department); err != nil {
				rows.Close()
				return out, err
			}
			out = append(out, r)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return out, err
		}
	}
	if t.Kind == "item" {
		provenance, e := tx.QueryContext(ctx, `SELECT c.ord,COALESCE(c.source_row_id,''),COALESCE(c.owner_entry_id,''),c.source_status,CASE WHEN o.operation IN('edit','add') THEN 'manual' ELSE c.provider END FROM catalog_credits c LEFT JOIN credit_owner_entries o ON o.entry_id=c.owner_entry_id WHERE c.entity_id=? ORDER BY c.ord`, entity)
		if e != nil {
			return out, e
		}
		for provenance.Next() {
			var ordinal int
			var source, owner, status, provenanceSource string
			if e = provenance.Scan(&ordinal, &source, &owner, &status, &provenanceSource); e != nil {
				provenance.Close()
				return out, e
			}
			for n := range out {
				if out[n].Kind == "credit" && out[n].Ordinal == ordinal {
					out[n].SourceRowID = source
					out[n].OwnerEntryID = owner
					out[n].SourceStatus = status
					out[n].Source = provenanceSource
				}
			}
		}
		e = provenance.Err()
		provenance.Close()
		if e != nil {
			return out, e
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT relationship,source,locked,observed_at FROM metadata_relationship_decisions WHERE kind=? AND entity_id=?`, t.Kind, entity)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var class, source, observed string
		var locked bool
		if err = rows.Scan(&class, &source, &locked, &observed); err != nil {
			return out, err
		}
		for n := range out {
			if out[n].Kind == class {
				out[n].Locked = locked
				out[n].Observed = observed
				if source == "manual" && (class != "credit" || out[n].SourceRowID == "" && out[n].OwnerEntryID == "") {
					out[n].Source = "manual"
				}
			}
		}
	}
	return out, rows.Err()
}

type RepairCascade struct {
	ID        string `json:"id"`
	Intent    string `json:"intent"`
	Status    string `json:"status"`
	Processed int    `json:"processed"`
	Failed    int    `json:"failed"`
	Completed int    `json:"completed"`
	Skipped   int    `json:"skipped"`
	Pending   int    `json:"pending"`
}

func readRepairCascades(ctx context.Context, tx *sql.Tx, t RepairTarget) ([]RepairCascade, error) {
	out := []RepairCascade{}
	entity, err := resolveEntity(ctx, tx, t.ID)
	if err != nil {
		return out, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT c.id,c.intent,c.status,c.processed,c.failed,(SELECT count(*) FROM metadata_repair_cascade_items WHERE operation_id=c.id AND status='complete'),(SELECT count(*) FROM metadata_repair_cascade_items WHERE operation_id=c.id AND status='skipped'),(SELECT count(*) FROM metadata_repair_cascade_items WHERE operation_id=c.id AND status='queued') FROM metadata_repair_cascades c WHERE c.kind=? AND c.entity_id=? ORDER BY c.created_at DESC,c.id DESC LIMIT 10`, t.Kind, entity)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var c RepairCascade
		if err = rows.Scan(&c.ID, &c.Intent, &c.Status, &c.Processed, &c.Failed, &c.Completed, &c.Skipped, &c.Pending); err != nil {
			return out, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
func descendantQuery(t RepairTarget, entity int64) (string, []any, error) {
	switch t.Kind {
	case "item":
		return `SELECT id FROM catalog_entities WHERE id=?`, []any{entity}, nil
	case "show":
		return `SELECT entity_id AS id FROM catalog_episodes WHERE show_id=?`, []any{entity}, nil
	case "season":
		return `SELECT entity_id AS id FROM catalog_episodes WHERE season_id=?`, []any{entity}, nil
	case "album":
		return `SELECT entity_id AS id FROM catalog_songs WHERE album_id=?`, []any{entity}, nil
	case "artist":
		return `SELECT s.entity_id AS id FROM catalog_songs s JOIN catalog_albums a ON a.entity_id=s.album_id WHERE a.artist_id=? UNION SELECT song_id AS id FROM catalog_song_artists WHERE artist_id=?`, []any{entity, entity}, nil
	case "book":
		return `SELECT entity_id AS id FROM catalog_book_files WHERE book_id=?`, []any{entity}, nil
	default:
		return "", nil, ErrRepairInput
	}
}

type RepairPreview struct {
	Revision       string       `json:"revision"`
	Descendants    int          `json:"descendants"`
	LockedFields   int          `json:"lockedFields"`
	SelectedImages int          `json:"selectedImages"`
	Message        string       `json:"message"`
	Merge          *MergeReview `json:"merge,omitempty"`
}
type MergeReview struct {
	Target     RepairTarget   `json:"target"`
	Allowed    bool           `json:"allowed"`
	Blockers   []string       `json:"blockers"`
	References map[string]int `json:"references"`
}

func (s *Service) RepairPreview(ctx context.Context, t RepairTarget, merge *RepairTarget, authorize func(*sql.Tx) error) (RepairPreview, error) {
	out := RepairPreview{Message: "Descendants are processed in durable pages. Each item keeps its identity, locks, files and personal state. A failed item retains its previous metadata."}
	gated, err := dbwork.BeginSnapshot(ctx, s.db)
	if err != nil {
		return out, err
	}
	tx := gated.Tx()
	defer gated.Rollback()
	if authorize == nil {
		return out, ErrRepairInput
	}
	if err = authorize(tx); err != nil {
		return out, err
	}
	snap, ent, err := readRepairSnapshot(ctx, tx, t)
	if err != nil {
		return out, err
	}
	if readingRepairKind(ent.itemKind) {
		return out, ErrRepairInput
	}
	out.Revision, err = repairRevision(ctx, tx, t, snap, ent)
	if err != nil {
		return out, err
	}
	entity, err := resolveEntity(ctx, tx, t.ID)
	if err != nil {
		return out, err
	}
	q, args, err := descendantQuery(t, entity)
	if err != nil {
		return out, err
	}
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM (`+q+`)`, args...).Scan(&out.Descendants); err != nil {
		return out, err
	}
	for _, f := range snap.Fields {
		if f.Locked {
			out.LockedFields++
		}
	}
	out.SelectedImages = len(snap.Artwork)
	if merge != nil {
		other, err := readRepairEntity(ctx, tx, *merge)
		if err != nil {
			return out, err
		}
		m := &MergeReview{Target: *merge, References: map[string]int{}, Blockers: []string{}}
		if merge.Kind != t.Kind || other.library != ent.library || merge.ID == t.ID {
			m.Blockers = append(m.Blockers, "Choose two distinct entities of the same kind in one library.")
		}
		// Review all actual FK references, not a partial hard-coded deletion list.
		// Publication is deliberately blocked until every reference has a reconciler;
		// review must never pretend moving file links is a lossless catalog merge.
		if t.Kind == "item" && merge.Kind == "item" {
			otherEntity, e := resolveEntity(ctx, tx, merge.ID)
			if e != nil {
				return out, e
			}
			rows, e := tx.QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
			if e != nil {
				return out, e
			}
			tables := []string{}
			for rows.Next() {
				var n string
				if e = rows.Scan(&n); e != nil {
					rows.Close()
					return out, e
				}
				tables = append(tables, n)
			}
			e = rows.Err()
			rows.Close()
			if e != nil {
				return out, e
			}
			for _, table := range tables {
				rows, e = tx.QueryContext(ctx, `SELECT "from" FROM pragma_foreign_key_list(?) WHERE "table"='catalog_entities'`, table)
				if e != nil {
					return out, e
				}
				cols := []string{}
				for rows.Next() {
					var col string
					if e = rows.Scan(&col); e != nil {
						rows.Close()
						return out, e
					}
					cols = append(cols, col)
				}
				e = rows.Err()
				rows.Close()
				if e != nil {
					return out, e
				}
				for _, col := range cols {
					var n int
					e = tx.QueryRowContext(ctx, fmt.Sprintf(`SELECT count(*) FROM %q WHERE %q IN (?,?)`, table, col), entity, otherEntity).Scan(&n)
					if e != nil {
						return out, e
					}
					if n > 0 {
						m.References[table+"."+col] = n
					}
				}
			}
		}
		m.Blockers = append(m.Blockers, "Merge publication is blocked: this catalog has no closed reconciliation/undo registry for all playback, Saved, queue and provider-history references. No references or media will be moved by this review.")
		out.Merge = m
	}
	return out, gated.Commit()
}
func queueRepairCascade(ctx context.Context, tx *sql.Tx, t RepairTarget, library, base, actor, intent, now string) error {
	if intent != "repair_assets" && intent != "refresh_unlocked" {
		return ErrRepairInput
	}
	entity, err := resolveEntity(ctx, tx, t.ID)
	if err != nil {
		return err
	}
	if _, _, err := descendantQuery(t, entity); err != nil {
		return err
	}
	var active bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM metadata_repair_cascades WHERE kind=? AND entity_id=? AND status IN('pending','queued'))`, t.Kind, entity).Scan(&active); err != nil {
		return err
	}
	if active {
		return errors.New("a descendant operation is already queuing; cancel it before starting another")
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM metadata_repair_cascades WHERE kind=? AND entity_id=? AND status NOT IN('pending','queued') AND NOT EXISTS(SELECT 1 FROM metadata_repair_cascade_items i WHERE i.operation_id=metadata_repair_cascades.id AND i.status='queued') AND id NOT IN(SELECT id FROM metadata_repair_cascades WHERE kind=? AND entity_id=? ORDER BY created_at DESC,id DESC LIMIT 9)`, t.Kind, entity, t.Kind, entity); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO metadata_repair_cascades(id,kind,entity_id,library_id,actor,intent,base_revision,created_at) VALUES(?,?,?,?,?,?,?,?)`, identity.Token(), t.Kind, entity, library, actor, intent, base, now)
	return err
}
func (s *Service) RepairCascadeStep(ctx context.Context) error {
	if err := s.reconcileRepairCascades(ctx); err != nil {
		return err
	}
	gated2, err := dbwork.Begin(ctx, s.db, dbwork.ClassBackgroundMedia)
	if err != nil {
		return err
	}
	tx := gated2.Tx()
	defer gated2.Rollback()
	var id, after, intent, actor string
	var t RepairTarget
	var root int64
	err = tx.QueryRowContext(ctx, `SELECT id,kind,entity_id,after_item,intent,actor FROM metadata_repair_cascades WHERE status='pending' ORDER BY created_at,id LIMIT 1`).Scan(&id, &t.Kind, &root, &after, &intent, &actor)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	t.ID = entityPublic(ctx, tx, root)
	if t.ID == "" {
		return sql.ErrNoRows
	}
	q, args, err := descendantQuery(t, root)
	if err != nil {
		return err
	}
	args = append(args, after)
	rows, err := tx.QueryContext(ctx, `SELECT id FROM (`+q+`) WHERE id>CAST(? AS INTEGER) ORDER BY id LIMIT 64`, args...)
	if err != nil {
		return err
	}
	ids := []int64{}
	for rows.Next() {
		var item int64
		if err = rows.Scan(&item); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, item := range ids {
		target := RepairTarget{"item", entityPublic(ctx, tx, item)}
		if target.ID == "" {
			return sql.ErrNoRows
		}
		snap, ent, e := readRepairSnapshot(ctx, tx, target)
		if e != nil {
			return e
		}
		base, e := repairRevision(ctx, tx, target, snap, ent)
		if e != nil {
			return e
		}
		receipt, status, reason, e := s.admitRepairCascade(ctx, tx, target, snap, intent, actor+"#cascade:"+id)
		if e != nil {
			return e
		}
		if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO metadata_repair_cascade_items VALUES(?,?,?,?,?)`, id, item, base, status, reason); err != nil {
			return err
		}
		if status == "queued" {
			if _, err = tx.ExecContext(ctx, `INSERT INTO metadata_repair_work_receipts(operation_id,item_id,receipt) VALUES(?,?,?)`, id, item, screenRaw(receipt)); err != nil {
				return err
			}
		}

		after = strconv.FormatInt(item, 10)
	}
	status := "pending"
	if len(ids) < 64 {
		status = "queued"
	}
	_, err = tx.ExecContext(ctx, `UPDATE metadata_repair_cascades SET after_item=?,processed=processed+?,status=?,failed=(SELECT count(*) FROM metadata_repair_cascade_items WHERE operation_id=? AND status IN('failed','superseded')) WHERE id=? AND status='pending'`, after, len(ids), status, id, id)
	if err != nil {
		return err
	}
	return gated2.Commit()
}
