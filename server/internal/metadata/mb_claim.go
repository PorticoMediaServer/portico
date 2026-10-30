package metadata

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

type mbLocalArtist struct{ ID, Name string }
type mbBase struct {
	LookupEpoch                            int64
	Music                                  musicInputs
	Library, Incarnation                   string
	Revision                               int64
	PolicyIncarnation, Algorithm           string
	PolicyRevision                         int64
	Enabled                                bool
	Title, Artist                          string
	Artists                                []mbLocalArtist
	ItemIncarnation                        string
	ItemSource, ItemIdentity, ItemMetadata int64
	Album, AlbumIncarnation                string
	AlbumRevision, MembershipRevision      int64
	// Entity and AlbumID are the integer catalogue ids of the job entity and
	// its album. Album stays the public id: cross-lane readers (publication
	// operations, review) still take it as a string.
	Entity, AlbumID                                            int64
	RecordingRevision, RecordingID, ReleaseRevision, ReleaseID string
	Sources                                                    []observedSource
	Hints                                                      map[string]string
	HintProblem                                                string
	Disc, Position                                             sql.NullInt64
}

func readMBSources(ctx context.Context, tx *sql.Tx, entity int64) ([]observedSource, error) {
	rows, e := tx.QueryContext(ctx, `SELECT a.token,l.part_index,l.start_seconds,l.end_seconds,a.path,a.size,a.modified_ns,a.available FROM catalog_asset_links l JOIN catalog_assets a ON a.id=l.asset_id WHERE l.entity_id=? ORDER BY l.part_index,a.id LIMIT 129`, entity)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []observedSource{}
	for rows.Next() {
		var v observedSource
		if e = rows.Scan(&v.Asset, &v.Part, &v.Start, &v.End, &v.Path, &v.Size, &v.Modified, &v.Available); e != nil {
			return nil, e
		}
		if len(out) >= 128 || math.IsNaN(v.Start) || math.IsInf(v.Start, 0) || (v.End.Valid && (math.IsNaN(v.End.Float64) || math.IsInf(v.End.Float64, 0))) {
			return nil, errPublicationInput
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func readMBHints(ctx context.Context, tx *sql.Tx, entity int64) (map[string]string, string, error) {
	// From the entity's own files to their evidence rows (an index seek each):
	// the evidence tables are never read as a whole.
	rows, e := tx.QueryContext(ctx, `WITH owned AS MATERIALIZED (
 SELECT a.token,a.size,a.modified_ns,cl.library_id FROM catalog_asset_links l JOIN catalog_assets a ON a.id=l.asset_id
 JOIN catalog_entities e ON e.id=l.entity_id JOIN catalog_libraries cl ON cl.id=e.library_id WHERE l.entity_id=?)
 SELECT DISTINCT field,lower(trim(value)) FROM (
 SELECT t.field,t.value FROM owned o CROSS JOIN audio_tag_evidence t ON t.library_id=o.library_id AND t.asset_id=o.token
 AND t.field IN('musicbrainz_trackid','musicbrainz_albumid','musicbrainz_releasetrackid')
 UNION ALL SELECT x.field,x.value FROM owned o CROSS JOIN audio_local_evidence x INDEXED BY audio_local_field ON x.library_id=o.library_id AND x.asset_id=o.token
 AND x.field IN('musicbrainz_trackid','musicbrainz_albumid','musicbrainz_releasetrackid')
 WHERE x.size=o.size AND x.modified_ns=o.modified_ns AND NOT EXISTS(SELECT 1 FROM inventory_objects io WHERE io.asset_id=x.asset_id AND io.retired=0 AND io.revision<>x.source_revision)
 ) LIMIT 7`, entity)
	if e != nil {
		return nil, "", e
	}
	defer rows.Close()
	values := map[string]string{}
	problem := ""
	for rows.Next() {
		var field, value string
		if e = rows.Scan(&field, &value); e != nil {
			return nil, "", e
		}
		if !mbIdentifier.MatchString(value) {
			problem = "invalid_tag_id"
		}
		if old := values[field]; old != "" && old != value {
			problem = "conflicting_tag_ids"
		}
		values[field] = value
	}
	return values, problem, rows.Err()
}
func readMBBase(ctx context.Context, tx *sql.Tx, kind, id string) (mbBase, error) {
	var subject int64
	if err := tx.QueryRowContext(ctx, `SELECT e.id FROM catalog_entities e JOIN catalog_libraries cl ON cl.id=e.library_id AND cl.retired=0 WHERE e.public_id=pid_blob(?)`, id).Scan(&subject); err != nil {
		return mbBase{}, err
	}
	if err := compactcatalog.RequireEntityAcquirableTx(ctx, tx, subject); err != nil {
		if errors.Is(err, compactcatalog.ErrEntityMutating) {
			return mbBase{}, errTVDBStale
		}
		return mbBase{}, err
	}
	b := mbBase{Sources: []observedSource{}, Artists: []mbLocalArtist{}, Hints: map[string]string{}}
	var e error
	if kind == "album" {
		e = tx.QueryRowContext(ctx, `SELECT cl.library_id,h.incarnation,h.revision,e.title,ea.title,h.membership_revision,COALESCE(l.release_revision,''),COALESCE(l.release_id,''),e.id FROM catalog_entities e JOIN catalog_albums ab ON ab.entity_id=e.id JOIN mb_album_heads h ON h.album_id=e.id JOIN catalog_entities ea ON ea.id=ab.artist_id AND ea.kind=5 AND ea.library_id=e.library_id JOIN catalog_libraries cl ON cl.id=e.library_id AND cl.retired=0 JOIN libraries lib ON lib.id=cl.library_id AND lib.kind='music' LEFT JOIN mb_album_links l ON l.album_id=e.id WHERE e.public_id=pid_blob(?) AND e.kind=6`, id).Scan(&b.Library, &b.Incarnation, &b.Revision, &b.Title, &b.Artist, &b.MembershipRevision, &b.ReleaseRevision, &b.ReleaseID, &b.Entity)
		if e != nil {
			return b, e
		}
		b.Album = id
		b.AlbumID = b.Entity
		b.AlbumIncarnation = b.Incarnation
		b.AlbumRevision = b.Revision
		var hint string
		e = tx.QueryRowContext(ctx, `SELECT s.hint_id,s.problem FROM mb_album_hint_scans s JOIN catalog_entities e ON e.id=s.album_id WHERE e.public_id=pid_blob(?) AND s.incarnation=? AND s.base_revision=? AND s.membership_revision=? AND s.status='complete'`, id, b.Incarnation, b.Revision, b.MembershipRevision).Scan(&hint, &b.HintProblem)
		if e != nil {
			return b, e
		}
		if hint != "" {
			b.Hints["musicbrainz_albumid"] = hint
		}
	} else if kind == "song" {
		// Matching searches with the song's own title, never an owner's display
		// override: a locked owner title keeps the automatic value beside it.
		e = tx.QueryRowContext(ctx, `SELECT cl.library_id,h.incarnation,h.revision,COALESCE((SELECT o.automatic_value FROM metadata_owner_fields o WHERE o.kind='item' AND o.entity_id=e.id AND o.field='title' AND o.locked=1 AND o.automatic_value<>''),e.title),ph.incarnation,ph.source_revision,ph.identity_revision,ph.metadata_revision,pid(ae.public_id),ah.incarnation,ah.revision,ah.membership_revision,cs.disc_number,cs.track_number,COALESCE(sl.recording_revision,''),COALESCE(sl.recording_id,''),COALESCE(al.release_revision,''),COALESCE(al.release_id,''),e.id,ae.id FROM catalog_entities e JOIN catalog_songs cs ON cs.entity_id=e.id JOIN catalog_libraries cl ON cl.id=e.library_id AND cl.retired=0 JOIN libraries lib ON lib.id=cl.library_id AND lib.kind='music' JOIN mb_song_heads h ON h.item_id=e.id JOIN metadata_publication_heads ph ON ph.item_id=e.id JOIN catalog_entities ae ON ae.id=cs.album_id AND ae.kind=6 JOIN mb_album_heads ah ON ah.album_id=ae.id LEFT JOIN mb_song_links sl ON sl.item_id=e.id LEFT JOIN mb_album_links al ON al.album_id=ae.id WHERE e.public_id=pid_blob(?) AND e.kind=7`, id).Scan(&b.Library, &b.Incarnation, &b.Revision, &b.Title, &b.ItemIncarnation, &b.ItemSource, &b.ItemIdentity, &b.ItemMetadata, &b.Album, &b.AlbumIncarnation, &b.AlbumRevision, &b.MembershipRevision, &b.Disc, &b.Position, &b.RecordingRevision, &b.RecordingID, &b.ReleaseRevision, &b.ReleaseID, &b.Entity, &b.AlbumID)
		if e != nil {
			return b, e
		}
		// The artist id flows into mb_operation_artists.artist_id (TEXT) and
		// cross-lane readers, so it stays the public id via pid().
		rows, e := tx.QueryContext(ctx, `SELECT pid(ae.public_id),ae.title FROM catalog_song_artists sa JOIN catalog_entities ae ON ae.id=sa.artist_id AND ae.kind=5 JOIN catalog_entities se ON se.id=sa.song_id JOIN catalog_libraries cl ON cl.id=se.library_id WHERE se.public_id=pid_blob(?) AND cl.library_id=? ORDER BY ae.id LIMIT 129`, id, b.Library)
		if e != nil {
			return b, e
		}
		names := []string{}
		for rows.Next() {
			var v mbLocalArtist
			if e = rows.Scan(&v.ID, &v.Name); e != nil {
				rows.Close()
				return b, e
			}
			if len(b.Artists) >= 128 {
				rows.Close()
				return b, errPublicationInput
			}
			b.Artists = append(b.Artists, v)
			names = append(names, v.Name)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return b, e
		}
		b.Artist = strings.Join(names, " & ")
		if b.Sources, e = readMBSources(ctx, tx, b.Entity); e != nil {
			return b, e
		}
		if b.Hints, b.HintProblem, e = readMBHints(ctx, tx, b.Entity); e != nil {
			return b, e
		}
	} else {
		return b, errPublicationInput
	}
	if b.Music, e = readMusicInputs(ctx, tx, kind, id, b.Library); e != nil {
		return b, e
	}
	if b.Music.Problem != "" && b.HintProblem == "" {
		b.HintProblem = b.Music.Problem
	}
	e = tx.QueryRowContext(ctx, `SELECT incarnation,revision,enabled,algorithm FROM mb_provider_policies WHERE library_id=?`, b.Library).Scan(&b.PolicyIncarnation, &b.PolicyRevision, &b.Enabled, &b.Algorithm)
	if e == nil {
		online, epoch, err := onlineLookupPolicyTx(ctx, tx)
		if err != nil {
			return b, err
		}
		b.Enabled = b.Enabled && online
		b.LookupEpoch = epoch
	}
	return b, e
}

// Album hint collection is bounded independently from provider work. A changed
// local generation restarts coverage; no current claim scans an entire album.
func advanceMBAlbumHints(ctx context.Context, tx *sql.Tx, id string) (bool, error) {
	var incarnation string
	var revision, membership int64
	e := tx.QueryRowContext(ctx, `SELECT h.incarnation,h.revision,h.membership_revision FROM mb_album_heads h JOIN catalog_entities e ON e.id=h.album_id WHERE e.public_id=pid_blob(?)`, id).Scan(&incarnation, &revision, &membership)
	if e != nil {
		return false, e
	}
	var oldInc, cursor, hint, problem, status string
	var oldRev, oldMembership int64
	e = tx.QueryRowContext(ctx, `SELECT s.incarnation,s.base_revision,s.membership_revision,s.cursor,s.hint_id,s.problem,s.status FROM mb_album_hint_scans s JOIN catalog_entities e ON e.id=s.album_id WHERE e.public_id=pid_blob(?)`, id).Scan(&oldInc, &oldRev, &oldMembership, &cursor, &hint, &problem, &status)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return false, e
	}
	if e != nil || oldInc != incarnation || oldRev != revision || oldMembership != membership {
		cursor, hint, problem, status = "", "", "", "pending"
	}
	if status == "complete" {
		return true, nil
	}
	// The cursor is opaque; it now holds an integer entity id as text.
	var cursorID int64
	if cursor != "" {
		cursorID, _ = strconv.ParseInt(cursor, 10, 64)
	}
	rows, e := tx.QueryContext(ctx, `SELECT cs.entity_id FROM catalog_songs cs JOIN catalog_entities e ON e.id=cs.album_id WHERE e.public_id=pid_blob(?) AND cs.entity_id>? ORDER BY cs.entity_id LIMIT 101`, id, cursorID)
	if e != nil {
		return false, e
	}
	ids := []int64{}
	for rows.Next() {
		var item int64
		if e = rows.Scan(&item); e != nil {
			rows.Close()
			return false, e
		}
		ids = append(ids, item)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return false, e
	}
	complete := len(ids) <= 100
	if len(ids) > 100 {
		ids = ids[:100]
	}
	for _, item := range ids {
		if _, e = readMBSources(ctx, tx, item); e != nil {
			return false, e
		}
		values, p, e := readMBHints(ctx, tx, item)
		if e != nil {
			return false, e
		}
		if p != "" {
			problem = p
		}
		if v := values["musicbrainz_albumid"]; v != "" {
			if hint != "" && hint != v {
				problem = "conflicting_tag_ids"
			}
			hint = v
		}
		cursor = strconv.FormatInt(item, 10)
	}
	status = "pending"
	if complete {
		status = "complete"
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO mb_album_hint_scans(album_id,incarnation,base_revision,membership_revision,cursor,hint_id,problem,status) SELECT e.id,?,?,?,?,?,?,? FROM catalog_entities e WHERE e.public_id=pid_blob(?) ON CONFLICT(album_id) DO UPDATE SET incarnation=excluded.incarnation,base_revision=excluded.base_revision,membership_revision=excluded.membership_revision,cursor=excluded.cursor,hint_id=excluded.hint_id,problem=excluded.problem,status=excluded.status`, incarnation, revision, membership, cursor, hint, problem, status, id)
	return complete, e
}

// mbClaimable is the condition a job must meet to be claimed: due, unleased,
// the provider not cooling down, and its library's MusicBrainz policy enabled.
// mb_jobs.entity_id is the integer catalogue id; the album/song branches join
// it to catalog_entities (kinds 6/7) and the library chain.
const mbClaimable = `NOT EXISTS(WITH RECURSIVE ancestors(id,depth) AS (SELECT mb_jobs.entity_id,0 UNION ALL SELECT e.parent_id,a.depth+1 FROM ancestors a JOIN catalog_entities e ON e.id=a.id WHERE e.parent_id IS NOT NULL AND a.depth<4) SELECT 1 FROM ancestors a JOIN catalog_entity_mutations m ON (m.entity_id=a.id OR m.target_id=a.id) WHERE m.status<>'terminal') AND status='pending' AND next_attempt<=? AND lease_until<=? AND COALESCE((SELECT next_attempt FROM metadata_provider_cooldowns WHERE provider='musicbrainz'),'')<=? AND ((mb_jobs.kind='album' AND EXISTS(SELECT 1 FROM catalog_entities e JOIN catalog_albums ab ON ab.entity_id=e.id JOIN catalog_libraries cl ON cl.id=e.library_id AND cl.retired=0 JOIN libraries l ON l.id=cl.library_id AND l.kind='music' JOIN mb_provider_policies p ON p.library_id=l.id AND p.enabled=1 AND p.algorithm='mb-exact-identity-v1' WHERE e.kind=6 AND e.id=mb_jobs.entity_id)) OR (mb_jobs.kind='song' AND EXISTS(SELECT 1 FROM catalog_entities e JOIN catalog_songs cs ON cs.entity_id=e.id JOIN catalog_libraries cl ON cl.id=e.library_id AND cl.retired=0 JOIN libraries l ON l.id=cl.library_id AND l.kind='music' JOIN mb_provider_policies p ON p.library_id=l.id AND p.enabled=1 AND p.algorithm='mb-exact-identity-v1' WHERE e.kind=7 AND e.id=mb_jobs.entity_id)))`

// claimMB leases the next due job. Finding it can mean passing over every job
// of a library whose policy is off, so the search runs on a background read
// connection, outside the write gate, in mb_jobs_due order; the gated
// transaction then re-checks that one job by its key. Searching under the gate
// held it for 10-25 s per claim on a million-song library, and every sign-in
// waited behind it.
func (s *Service) claimMB(ctx context.Context) (*mbJob, error) {
	now := s.publicationTime()
	stamp := now.Format(time.RFC3339)
	read := dbwork.ReadHandle(dbwork.WithClass(ctx, dbwork.ClassBackgroundMedia), s.db)
	// The throttle counts live provider operations rather than leased job rows.
	// The one MusicBrainz loop mints exactly one claimed operation in the same
	// gated transaction that sets the job lease, and every step finishes its
	// operation before the next claim, so the two counts agree whenever it
	// matters. A cancelled step can leave a claimed operation whose job lease
	// was already released; that orphan counts here until its lease expires and
	// cleanupMusicBrainz reaps it, erring toward throttling for at most one
	// lease. Counting operations keeps the probe on the bounded operations
	// table through mb_operations_status (O(active) index entries) instead of
	// scanning every mb_jobs row (O(jobs) on every claim of a million-song
	// library, which is what starved the gate before the search moved out).
	var active int
	if e := read.QueryRowContext(ctx, `SELECT count(*) FROM mb_publication_operations WHERE status IN('claimed','staged') AND lease_until>?`, stamp).Scan(&active); e != nil {
		return nil, e
	}
	if active >= 8 {
		return nil, nil
	}
	// While MusicBrainz asks us to wait, no row can be claimed; asking the
	// claim query anyway would read the whole pending backlog (every song of
	// the library) once per step only to find that out.
	var cooldown string
	if e := read.QueryRowContext(ctx, `SELECT COALESCE((SELECT next_attempt FROM metadata_provider_cooldowns WHERE provider='musicbrainz'),'')`).Scan(&cooldown); e != nil {
		return nil, e
	}
	if cooldown > stamp {
		return nil, nil
	}
	var kind, entity string
	// j.id stays the public id (it flows into RepairTarget and cross-lane
	// readers); the integer entity_id never leaves SQL here.
	kind, entity, e := s.pickMBMutationSafe(ctx, stamp)
	if e == nil && kind == "" {
		return nil, nil
	}
	if errors.Is(e, sql.ErrNoRows) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	gated, e := dbwork.Begin(ctx, s.db, dbwork.ClassBackgroundMedia)
	if e != nil {
		return nil, e
	}
	tx := gated.Tx()
	defer gated.Rollback()
	if ready, problem := publicationSourcesReady(ctx, tx); problem != nil || !ready {
		return nil, problem
	}
	j := &mbJob{}
	e = tx.QueryRowContext(ctx, `SELECT mb_jobs.kind,(SELECT pid(public_id) FROM catalog_entities WHERE id=mb_jobs.entity_id),mb_jobs.selected_id,mb_jobs.manual,mb_jobs.review_search,mb_jobs.generation,mb_jobs.attempts FROM mb_jobs WHERE mb_jobs.kind=? AND mb_jobs.entity_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?)) AND `+mbClaimable, kind, entity, stamp, stamp, stamp).Scan(&j.kind, &j.id, &j.selected, &j.manual, &j.review, &j.generation, &j.attempts)
	if errors.Is(e, sql.ErrNoRows) {
		// Claimed, finished or disabled since the search; the next step looks again.
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	if j.selected == "" && !j.review {
		kind := j.kind
		if kind == "song" {
			kind = "item"
		}
		locked, err := repairIdentityLocked(ctx, tx, RepairTarget{kind, j.id})
		if err != nil {
			return nil, err
		}
		if locked {
			if _, err = tx.ExecContext(ctx, `UPDATE mb_jobs SET status='needs_selection',error='owner_identity_locked',revision=revision+1 WHERE kind=? AND entity_id=?`, j.kind, j.id); err != nil {
				return nil, err
			}
			return nil, gated.Commit()
		}
	}
	if j.kind == "album" {
		complete, e := advanceMBAlbumHints(ctx, tx, j.id)
		if e != nil {
			return nil, e
		}
		if !complete {
			return nil, gated.Commit()
		}
	}
	if j.base, e = readMBBase(ctx, tx, j.kind, j.id); e != nil {
		if errors.Is(e, errPublicationInput) || errors.Is(e, sql.ErrNoRows) {
			// Claim owns this current transaction; an unsupported local graph
			// must not repeatedly occupy the oldest due position.
			if _, e = tx.ExecContext(ctx, `UPDATE mb_jobs SET status='needs_selection',error='unsupported_local_inputs',revision=revision+1 WHERE kind=? AND entity_id=? AND generation=?`, j.kind, j.id, j.generation); e != nil {
				return nil, e
			}
			return nil, gated.Commit()
		}
		return nil, e
	}
	if !j.base.Enabled || j.base.Algorithm != "mb-exact-identity-v1" {
		return nil, nil
	}
	j.title, j.artist = j.base.Title, j.base.Artist
	j.lease = identity.Token()
	j.operation = identity.Token()
	j.until = now.Add(90 * time.Second).Format(time.RFC3339)
	j.digest = publicationDigest(j.base)
	columns := []string{"id", "kind", "entity_id", "incarnation", "job_generation", "lease", "lease_until", "library_id", "policy_incarnation", "policy_revision", "algorithm", "base_revision", "item_incarnation", "item_source_revision", "item_identity_revision", "item_metadata_revision", "album_id", "album_incarnation", "album_revision", "album_membership_revision", "accepted_recording_revision", "accepted_release_revision", "selected_id", "manual", "review_search", "query_title", "query_artist", "query_digest", "qualification", "status", "created_at"}
	values := []any{j.operation, j.kind, j.base.Entity, j.base.Incarnation, j.generation, j.lease, j.until, j.base.Library, j.base.PolicyIncarnation, j.base.PolicyRevision, j.base.Algorithm, j.base.Revision, j.base.ItemIncarnation, j.base.ItemSource, j.base.ItemIdentity, j.base.ItemMetadata, j.base.AlbumID, j.base.AlbumIncarnation, j.base.AlbumRevision, j.base.MembershipRevision, nullableMB(j.base.RecordingRevision), nullableMB(j.base.ReleaseRevision), j.selected, j.manual, j.review, j.title, j.artist, j.digest, "catalog_observation", "claimed", stamp}
	var receipts int
	if e = tx.QueryRowContext(ctx, `SELECT count(*) FROM mb_publication_operations`).Scan(&receipts); e != nil {
		return nil, e
	}
	if receipts >= 4096 {
		return nil, nil
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO mb_publication_operations(`+strings.Join(columns, ",")+`) VALUES(`+strings.TrimSuffix(strings.Repeat("?,", len(values)), ",")+`)`, values...); e != nil {
		return nil, e
	}
	for n, v := range j.base.Sources {
		if _, e = tx.ExecContext(ctx, `INSERT INTO mb_operation_sources VALUES(?,?,?,?,?,?,?,?,?,?)`, j.operation, n, v.Asset, v.Part, v.Start, v.End, v.Path, v.Size, v.Modified, v.Available); e != nil {
			return nil, e
		}
	}
	for n, v := range j.base.Artists {
		if _, e = tx.ExecContext(ctx, `INSERT INTO mb_operation_artists VALUES(?,?,?,?)`, j.operation, n, v.ID, v.Name); e != nil {
			return nil, e
		}
	}
	if _, e = tx.ExecContext(ctx, `UPDATE mb_jobs SET lease=?,lease_until=? WHERE kind=? AND entity_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?)) AND generation=?`, j.lease, j.until, j.kind, j.id, j.generation); e != nil {
		return nil, e
	}
	return j, gated.Commit()
}
func (s *Service) activeMB(ctx context.Context, tx *sql.Tx, j mbJob) error {
	if e := mbAccountingProducerTx(ctx, tx, j); e != nil {
		return e
	}
	if ready, e := publicationSourcesReady(ctx, tx); e != nil {
		return e
	} else if !ready {
		return errTVDBStale
	}
	if e := ctx.Err(); e != nil {
		return e
	}
	var n int
	e := tx.QueryRowContext(ctx, `SELECT count(*) FROM mb_jobs JOIN mb_publication_operations o ON o.id=? AND o.kind=mb_jobs.kind AND o.entity_id=mb_jobs.entity_id WHERE mb_jobs.kind=? AND mb_jobs.entity_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?)) AND mb_jobs.generation=? AND mb_jobs.lease=? AND mb_jobs.lease_until>? AND o.job_generation=mb_jobs.generation AND o.lease=mb_jobs.lease AND o.status IN('claimed','staged') AND o.query_digest=?`, j.operation, j.kind, j.id, j.generation, j.lease, s.publicationTime().Format(time.RFC3339), j.digest).Scan(&n)
	if e != nil {
		return e
	}
	if n != 1 {
		return errTVDBStale
	}
	b, e := readMBBase(ctx, tx, j.kind, j.id)
	if e != nil {
		if errors.Is(e, sql.ErrNoRows) {
			return errTVDBStale
		}
		return e
	}
	if !b.Enabled || publicationDigest(b) != j.digest {
		return errTVDBStale
	}
	return nil
}

// The initial full dependency fence acquires this transaction's catalog view.
// Own publication writes may change those heads, so the final fence checks the
// still-owned generation/lease and a fresh clock after all expensive work.
// Concurrent catalog changes cannot commit between the read/write transaction
// and this check. Never substitute the earlier claim or staging timestamp.
func (s *Service) commitMBCurrent(ctx context.Context, gatedArg *dbwork.Write, j mbJob) error {
	tx := gatedArg.Tx()
	if e := ctx.Err(); e != nil {
		gatedArg.Rollback()
		return e
	}
	var entity int64
	if e := tx.QueryRowContext(ctx, `SELECT e.id FROM catalog_entities e JOIN catalog_libraries cl ON cl.id=e.library_id AND cl.retired=0 WHERE e.public_id=pid_blob(?)`, j.id).Scan(&entity); e != nil {
		gatedArg.Rollback()
		return errTVDBStale
	}
	if e := compactcatalog.RequireMBPublicationMutationCurrentTx(ctx, tx, entity, j.operation); e != nil {
		gatedArg.Rollback()
		if errors.Is(e, compactcatalog.ErrEntityMutating) {
			return errTVDBStale
		}
		return e
	}
	var current int
	e := tx.QueryRowContext(ctx, `SELECT count(*) FROM mb_jobs JOIN mb_publication_operations o ON o.id=? AND o.kind=mb_jobs.kind AND o.entity_id=mb_jobs.entity_id WHERE mb_jobs.kind=? AND mb_jobs.entity_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?)) AND mb_jobs.generation=? AND mb_jobs.lease=? AND mb_jobs.lease_until>? AND o.job_generation=mb_jobs.generation AND o.lease=mb_jobs.lease AND o.query_digest=?`, j.operation, j.kind, j.id, j.generation, j.lease, s.publicationTime().Format(time.RFC3339), j.digest).Scan(&current)
	if e != nil {
		gatedArg.Rollback()
		return e
	}
	if current != 1 {
		gatedArg.Rollback()
		return errTVDBStale
	}
	return gatedArg.Commit()
}
