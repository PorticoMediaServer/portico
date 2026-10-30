package metadata

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// A target contains the original concrete authority and its resolved provider
// result. The operation's result digest binds both; publish never recaptures an
// item into an older operation.
type tvdbProjectionTarget struct {
	Item, Show                 int64
	Incarnation, Library       string
	Source, Identity, Metadata int64
	Season                     sql.NullInt64
	SeasonNumber               sql.NullInt64
	Numbering                  string
	Number                     int
	Local                      string
	Accepted                   int64
	Status                     string
	Provider                   int64
	Title, Overview, Payload   string
}

func tvdbTargetAuthority(v tvdbProjectionTarget) tvdbProjectionTarget {
	v.Status = ""
	v.Provider = 0
	v.Title = ""
	v.Overview = ""
	v.Payload = ""
	return v
}

// tvdbProjectionTargets is how many episodes one projection page stages and
// one apply transaction publishes. Publishing an episode changes about six
// rows, so 24 keep a page inside a background write plan.
const tvdbProjectionTargets = 24

const tvdbTargetColumns = `e.entity_id,h.incarnation,cl.library_id,e.show_id,h.source_revision,h.identity_revision,h.metadata_revision,e.season_id,se.number,e.numbering,e.number,e.local_identity_status,COALESCE(pe.provider_id,link.provider_id,0)`
const tvdbTargetJoins = ` FROM catalog_episodes e JOIN catalog_entities i ON i.id=e.entity_id JOIN catalog_libraries cl ON cl.id=i.library_id JOIN metadata_publication_heads h ON h.item_id=i.id LEFT JOIN catalog_seasons se ON se.entity_id=e.season_id LEFT JOIN provider_evidence pe ON pe.item_id=i.id AND pe.provider='tvdb' LEFT JOIN tvdb_episode_links link ON link.item_id=i.id `

type tvdbScanner interface{ Scan(...any) error }

func scanTVDBTarget(row tvdbScanner, v *tvdbProjectionTarget) error {
	return row.Scan(&v.Item, &v.Incarnation, &v.Library, &v.Show, &v.Source, &v.Identity, &v.Metadata, &v.Season, &v.SeasonNumber, &v.Numbering, &v.Number, &v.Local, &v.Accepted)
}
func (s *Service) resolveTVDBTarget(ctx context.Context, tx *sql.Tx, p tvdbPublication, v *tvdbProjectionTarget) error {
	v.Status = "not_found"
	if v.Local == "manual" {
		v.Status = "manual_preserved"
		return nil
	}
	if (v.Numbering == "absolute") != (p.Order == "absolute") {
		v.Status = "order_mismatch"
		return nil
	}
	var conflict bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM provider_evidence WHERE item_id=? AND provider<>'tvdb') OR EXISTS(SELECT 1 FROM provider_evidence e JOIN tvdb_episode_links l ON l.item_id=e.item_id WHERE e.item_id=? AND e.provider='tvdb' AND e.provider_id>0 AND l.provider_id>0 AND e.provider_id<>l.provider_id)`, v.Item, v.Item).Scan(&conflict); err != nil {
		return err
	}
	if conflict {
		v.Status = "identity_conflict"
		return nil
	}
	where := `e.season_number=? AND e.number=?`
	args := []any{p.ShowID, p.Generation, p.Set, v.SeasonNumber, v.Number}
	if p.Order == "absolute" {
		where = `e.absolute_number=?`
		args = []any{p.ShowID, p.Generation, p.Set, v.Number}
	}
	rows, err := tx.QueryContext(ctx, `SELECT e.provider_id,e.title,e.overview,e.payload FROM tvdb_episode_evidence e JOIN tvdb_evidence_page_ownership o ON o.show_id=e.show_id AND o.generation=e.generation AND o.provider_id=e.provider_id WHERE e.show_id=? AND e.generation=? AND o.set_incarnation=? AND `+where+` LIMIT 2`, args...)
	if err != nil {
		return err
	}
	count := 0
	for rows.Next() {
		count++
		if err = rows.Scan(&v.Provider, &v.Title, &v.Overview, &v.Payload); err != nil {
			rows.Close()
			return err
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if count > 1 {
		v.Status = "ambiguous"
	} else if count == 1 {
		v.Status = "matched"
		if v.Accepted > 0 && v.Accepted != v.Provider {
			v.Status = "identity_conflict"
		}
	}
	if v.Status != "matched" {
		v.Provider = 0
		v.Title = ""
		v.Overview = ""
		v.Payload = ""
	}
	return nil
}
func (s *Service) stageTVDBProjection(ctx context.Context, tx *sql.Tx, p tvdbPublication) ([]tvdbProjectionTarget, error) {
	rows, err := tx.QueryContext(ctx, `SELECT `+tvdbTargetColumns+tvdbTargetJoins+` WHERE e.show_id=? AND e.entity_id>CAST(? AS INTEGER) AND i.kind=4 ORDER BY e.entity_id LIMIT ?`, p.ShowID, p.After, tvdbProjectionTargets)
	if err != nil {
		return nil, err
	}
	targets := []tvdbProjectionTarget{}
	for rows.Next() {
		var v tvdbProjectionTarget
		if err = scanTVDBTarget(rows, &v); err != nil {
			rows.Close()
			return nil, err
		}
		if v.Library != p.Library {
			rows.Close()
			return nil, tvdbProblem("invalid_target_scope")
		}
		targets = append(targets, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	bytes := 0
	for ordinal := range targets {
		v := &targets[ordinal]
		if err = s.resolveTVDBTarget(ctx, tx, p, v); err != nil {
			return nil, err
		}
		bytes += len(v.Payload)
		if len(v.Payload) > 1<<20 || bytes > 16<<20 {
			return nil, tvdbProblem("evidence_capacity")
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO tvdb_projection_targets(operation_id,ordinal,item_id,item_incarnation,source_revision,identity_revision,metadata_revision,library_id,show_id,season_id,season_number,numbering,number,local_identity_status,accepted_provider_id,result_status,result_provider_id,title,overview,evidence_payload) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, p.ID, ordinal, v.Item, v.Incarnation, v.Source, v.Identity, v.Metadata, v.Library, v.Show, v.Season, v.SeasonNumber, v.Numbering, v.Number, v.Local, v.Accepted, v.Status, v.Provider, v.Title, v.Overview, v.Payload)
		if err != nil {
			return nil, err
		}
	}
	return targets, nil
}
func readTVDBTargets(ctx context.Context, tx *sql.Tx, p tvdbPublication) ([]tvdbProjectionTarget, error) {
	rows, err := tx.QueryContext(ctx, `SELECT ordinal,item_id,item_incarnation,library_id,show_id,source_revision,identity_revision,metadata_revision,season_id,season_number,numbering,number,local_identity_status,accepted_provider_id,result_status,result_provider_id,title,overview,evidence_payload FROM tvdb_projection_targets WHERE operation_id=? ORDER BY ordinal LIMIT ?`, p.ID, tvdbProjectionTargets+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []tvdbProjectionTarget{}
	bytes := 0
	for rows.Next() {
		var v tvdbProjectionTarget
		var ordinal int
		if err = rows.Scan(&ordinal, &v.Item, &v.Incarnation, &v.Library, &v.Show, &v.Source, &v.Identity, &v.Metadata, &v.Season, &v.SeasonNumber, &v.Numbering, &v.Number, &v.Local, &v.Accepted, &v.Status, &v.Provider, &v.Title, &v.Overview, &v.Payload); err != nil {
			return nil, err
		}
		bytes += len(v.Payload)
		if ordinal != len(out) || len(out) >= tvdbProjectionTargets || bytes > 16<<20 {
			return nil, errTVDBStale
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Service) claimTVDBProjection(ctx context.Context) (*tvdbPublication, error) {
	gated, err := dbwork.Begin(ctx, s.db, dbwork.ClassBackgroundMedia)
	if err != nil {
		return nil, err
	}
	tx := gated.Tx()
	defer gated.Rollback()
	if ready, e := publicationSourcesReady(ctx, tx); e != nil || !ready {
		return nil, e
	}
	if err = s.cleanupTVDBPublications(ctx, tx); err != nil {
		return nil, err
	}
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM(SELECT id FROM tvdb_publication_operations WHERE status IN('claimed','ready') LIMIT 8)`).Scan(&count); err != nil {
		return nil, err
	}
	if count >= 8 {
		return nil, gated.Commit()
	}
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM(SELECT id FROM tvdb_publication_operations LIMIT 4096)`).Scan(&count); err != nil {
		return nil, err
	}
	if count >= 4096 {
		return nil, gated.Commit()
	}
	stamp := tvdbStamp(s.publicationTime())
	var show string
	err = tx.QueryRowContext(ctx, `SELECT pid(e.public_id) FROM tvdb_jobs j JOIN catalog_entities e ON e.id=j.show_id JOIN catalog_libraries cl ON cl.id=e.library_id JOIN libraries l ON l.id=cl.library_id JOIN tvdb_provider_policies p ON p.library_id=cl.library_id WHERE j.status='pending_apply' AND j.next_attempt<=? AND j.lease_until<=? AND l.kind IN('tv','anime') AND p.enabled=1 AND p.algorithm='tvdb-series-exact-title-year-v1'`+screenTVDBFilter(ctx)+` ORDER BY j.next_attempt,j.show_id LIMIT 1`, stamp, stamp).Scan(&show)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, gated.Commit()
	}
	if err != nil {
		return nil, err
	}
	p, err := readTVDBPublication(ctx, tx, show)
	if err != nil {
		return nil, err
	}
	if p.Generation < 1 || p.JobRevision < 1 || p.Attempts < 0 || p.Attempts > 100 || len(p.Title) > 2048 {
		return nil, tvdbProblem("invalid_job")
	}
	var valid bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM tvdb_publication_sets WHERE show_id=? AND incarnation=? AND generation=? AND provider_id=? AND episode_order=? AND policy_incarnation=? AND policy_revision=? AND consent_revision=? AND complete=1)`, p.ShowID, p.Set, p.Generation, p.Provider, p.Order, p.Policy, p.PolicyRevision, p.ConsentRevision).Scan(&valid); err != nil {
		return nil, err
	}
	if valid {
		_, err = s.attestTVDBSet(ctx, tx, p, p.Page+1, true)
		if err != nil && !errors.Is(err, errTVDBEvidence) {
			return nil, err
		}
		valid = err == nil
	}
	if !valid {
		// This is normal invalid-current-cache recovery, with no older-format path.
		if _, err = tx.ExecContext(ctx, `UPDATE tvdb_jobs SET status='pending_episodes',page=0,revision=revision+1,apply_after='',lease='',lease_until='' WHERE show_id=?`, p.ShowID); err != nil {
			return nil, err
		}
		return nil, gated.Commit()
	}
	after := ""
	err = tx.QueryRowContext(ctx, `SELECT after_item FROM tvdb_projection_coverage WHERE show_id=? AND set_incarnation=? AND set_revision=? AND source_revision=? AND hierarchy_revision=? AND selection_revision=? AND policy_incarnation=? AND policy_revision=? AND consent_revision=?`, p.ShowID, p.Set, p.SetRevision, p.Source, p.Hierarchy, p.Selection, p.Policy, p.PolicyRevision, p.ConsentRevision).Scan(&after)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if p.After != after {
		if _, err = tx.ExecContext(ctx, `UPDATE tvdb_jobs SET apply_after=?,revision=revision+1 WHERE show_id=?`, after, p.ShowID); err != nil {
			return nil, err
		}
		p, err = readTVDBPublication(ctx, tx, show)
		if err != nil {
			return nil, err
		}
	}
	p.ID = identity.Token()
	p.LeaseUntil = tvdbStamp(s.publicationTime().Add(time.Minute))
	p.Digest = tvdbDigest(tvdbAuthority(p))
	_, err = tx.ExecContext(ctx, `INSERT INTO tvdb_publication_operations(id,show_id,show_incarnation,phase,library_id,query_title,query_year,job_generation,job_revision,lease_until,accepted_provider_id,episode_order,policy_incarnation,policy_revision,algorithm,refresh_mode,source_revision,hierarchy_revision,selection_revision,descriptive_revision,set_incarnation,set_revision,page,after_item,input_digest,status,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,'claimed',?)`, p.ID, p.ShowID, p.Incarnation, p.Phase, p.Library, p.Title, p.Year, p.Generation, p.JobRevision, p.LeaseUntil, p.Provider, p.Order, p.Policy, p.PolicyRevision, p.Algorithm, p.Mode, p.Source, p.Hierarchy, p.Selection, p.Description, p.Set, p.SetRevision, p.Page, p.After, p.Digest, stamp)
	if err != nil {
		return nil, err
	}
	targets, err := s.stageTVDBProjection(ctx, tx, p)
	if err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE tvdb_publication_operations SET status='ready',result_digest=? WHERE id=?`, tvdbDigest(targets), p.ID); err != nil {
		return nil, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE tvdb_jobs SET lease=?,lease_until=? WHERE show_id=? AND generation=? AND revision=? AND lease_until<=?`, p.ID, p.LeaseUntil, p.ShowID, p.Generation, p.JobRevision, stamp)
	if err != nil {
		return nil, err
	}
	if n, err := result.RowsAffected(); err != nil {
		return nil, err
	} else if n != 1 {
		return nil, errTVDBStale
	}
	if err = gated.Commit(); err != nil {
		return nil, err
	}
	return &p, nil
}
func (s *Service) publishTVDBProjection(ctx context.Context, p tvdbPublication) error {
	if p.Phase != "projection" {
		return tvdbProblem("invalid_phase")
	}
	gated2, err := dbwork.Begin(ctx, s.db, dbwork.ClassBackgroundMedia)
	if err != nil {
		return err
	}
	tx := gated2.Tx()
	defer gated2.Rollback()
	if err = s.activeTVDBPublication(ctx, tx, p); err != nil {
		return err
	}
	var complete bool
	if err = tx.QueryRowContext(ctx, `SELECT complete FROM tvdb_publication_sets WHERE show_id=? AND incarnation=?`, p.ShowID, p.Set).Scan(&complete); err != nil {
		return err
	}
	if !complete {
		return errTVDBStale
	}
	if _, err = s.attestTVDBSet(ctx, tx, p, p.Page+1, true); err != nil {
		return err
	}
	targets, err := readTVDBTargets(ctx, tx, p)
	if err != nil {
		return err
	}
	var digest string
	if err = tx.QueryRowContext(ctx, `SELECT result_digest FROM tvdb_publication_operations WHERE id=? AND status='ready'`, p.ID).Scan(&digest); err != nil {
		return err
	}
	if tvdbDigest(targets) != digest {
		return errTVDBStale
	}
	for _, v := range targets {
		var current tvdbProjectionTarget
		err = scanTVDBTarget(tx.QueryRowContext(ctx, `SELECT `+tvdbTargetColumns+tvdbTargetJoins+` WHERE e.entity_id=? AND i.kind=4`, v.Item), &current)
		if errors.Is(err, sql.ErrNoRows) {
			return errTVDBStale
		}
		if err != nil {
			return err
		}
		if tvdbDigest(tvdbTargetAuthority(current)) != tvdbDigest(tvdbTargetAuthority(v)) {
			return errTVDBStale
		}
	}
	// All target authority is checked before any canonical write. Successful writes
	// intentionally advance concrete metadata/identity revisions in this transaction.
	for _, v := range targets {
		if v.Status == "matched" {
			fields := map[string]any{"overview": v.Overview, "ordering_basis": p.Order}
			if v.Title != "" {
				fields["title"] = v.Title
			}
			if p.Mode == "fill_missing" {
				var title, overview string
				if err = tx.QueryRowContext(ctx, `SELECT e.title,COALESCE(d.overview,'') FROM catalog_entities e LEFT JOIN catalog_item_details d ON d.entity_id=e.id WHERE e.id=?`, v.Item).Scan(&title, &overview); err != nil {
					return err
				}
				if title != "" {
					delete(fields, "title")
				}
				if overview != "" {
					delete(fields, "overview")
				}
			}
			if err = compactcatalog.SetFieldsTx(ctx, tx, v.Item, compactcatalog.Automatic, fields); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `INSERT INTO provider_evidence VALUES(?,'tvdb',?,?,?) ON CONFLICT(item_id,provider) DO UPDATE SET provider_id=excluded.provider_id,payload=excluded.payload,observed_at=excluded.observed_at`, v.Item, v.Provider, v.Payload, tvdbStamp(s.publicationTime())); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `INSERT INTO metadata_details VALUES(?,'tvdb',?,?,?) ON CONFLICT(item_id,provider) DO UPDATE SET provider_id=excluded.provider_id,source_url=excluded.source_url,observed_at=excluded.observed_at`, v.Item, strconv.FormatInt(v.Provider, 10), "https://thetvdb.com/dereferrer/episode/"+strconv.FormatInt(v.Provider, 10), tvdbStamp(s.publicationTime())); err != nil {
				return err
			}
			// The episode page carried its still; the artwork worker picks it up.
			if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO artwork_dirty(kind,entity_id) VALUES('item',?)`, v.Item); err != nil {
				return err
			}
		}
		// An unresolved mapping changes its status, not a previously accepted identity.
		if _, err = tx.ExecContext(ctx, `INSERT INTO tvdb_episode_links VALUES(?,?,?,?) ON CONFLICT(item_id) DO UPDATE SET provider_id=CASE WHEN excluded.status='matched' THEN excluded.provider_id ELSE tvdb_episode_links.provider_id END,status=excluded.status,observed_at=excluded.observed_at`, v.Item, v.Provider, v.Status, tvdbStamp(s.publicationTime())); err != nil {
			return err
		}
	}
	after := p.After
	if len(targets) > 0 {
		after = strconv.FormatInt(targets[len(targets)-1].Item, 10)
	}
	complete = len(targets) < tvdbProjectionTargets
	status := "pending_apply"
	if complete {
		status = "complete"
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO tvdb_projection_coverage(show_id,set_incarnation,set_revision,source_revision,hierarchy_revision,selection_revision,policy_incarnation,policy_revision,consent_revision,after_item,complete) VALUES(?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(show_id) DO UPDATE SET set_incarnation=excluded.set_incarnation,set_revision=excluded.set_revision,source_revision=excluded.source_revision,hierarchy_revision=excluded.hierarchy_revision,selection_revision=excluded.selection_revision,policy_incarnation=excluded.policy_incarnation,policy_revision=excluded.policy_revision,consent_revision=excluded.consent_revision,after_item=excluded.after_item,complete=excluded.complete`, p.ShowID, p.Set, p.SetRevision, p.Source, p.Hierarchy, p.Selection, p.Policy, p.PolicyRevision, p.ConsentRevision, after, complete); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE tvdb_jobs SET status=?,apply_after=?,attempts=0,error='',retry_status='',next_attempt='',revision=revision+1 WHERE show_id=?`, status, after, p.ShowID); err != nil {
		return err
	}
	if err = s.finishTVDBPublication(ctx, tx, p, "applied", ""); err != nil {
		return err
	}
	return gated2.Commit()
}
