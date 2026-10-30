package metadata

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/metadataprovider"
	"github.com/PorticoMediaServer/portico/server/internal/supervise"
)

const tvdbTerminalPayloadQuery = `SELECT o.id FROM tvdb_publication_operations o WHERE o.status IN('applied','stale','failed') AND NOT EXISTS(SELECT 1 FROM tvdb_jobs j WHERE j.show_id=o.show_id AND j.lease=o.id AND j.lease_until>?) AND (EXISTS(SELECT 1 FROM tvdb_publication_candidates c WHERE c.operation_id=o.id) OR EXISTS(SELECT 1 FROM tvdb_projection_targets t WHERE t.operation_id=o.id)) ORDER BY o.created_at,o.id LIMIT 128`
const tvdbTerminalDeleteQuery = `DELETE FROM tvdb_publication_operations WHERE id IN(SELECT o.id FROM tvdb_publication_operations o WHERE o.status IN('applied','stale','failed') AND NOT EXISTS(SELECT 1 FROM tvdb_jobs j WHERE j.show_id=o.show_id AND j.lease=o.id AND j.lease_until>?) AND (o.created_at<? OR (SELECT count(*) FROM(SELECT id FROM tvdb_publication_operations LIMIT 4096))>=4096) ORDER BY o.created_at,o.id LIMIT 128)`

// Captured authority shared by the acquisition and fresh-target projection phases.
type tvdbPublication struct {
	ConsentRevision, LookupEpoch                                int64
	ScreenFence                                                 string
	ID, Show, Incarnation, Library, Phase, Status, Title, Order string
	// Show is the public id; ShowID is its integer entity row, used in every
	// INTEGER catalogue reference.
	ShowID                                                  int64
	Policy, Algorithm, Mode, Set, LeaseUntil, After, Digest string
	Generation, JobRevision, Provider, PolicyRevision       int64
	Source, Hierarchy, Selection, Description, SetRevision  int64
	Year, Page, Attempts                                    int
}

func tvdbDigest(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func tvdbStamp(t time.Time) string  { return t.UTC().Format(time.RFC3339) }
func tvdbProblem(code string) error { return &metadataprovider.Error{Provider: "tvdb", Code: code} }
func tvdbOrder(order string) bool {
	switch order {
	case "official", "dvd", "absolute", "default", "alternate", "regional":
		return true
	}
	return false
}

func readTVDBPublication(ctx context.Context, tx *sql.Tx, show string) (tvdbPublication, error) {
	var p tvdbPublication
	p.Show = show
	showID, err := resolveEntity(ctx, tx, show)
	if err != nil {
		return p, err
	}
	p.ShowID = showID
	err = tx.QueryRowContext(ctx, `SELECT h.incarnation,cl.library_id,j.status,e.title,e.year,j.episode_order,j.generation,j.revision,j.provider_id,j.page,j.attempts,j.apply_after,j.lease,j.lease_until,p.incarnation,p.revision,p.algorithm,p.refresh_mode,h.source_revision,h.hierarchy_revision,h.selection_revision,h.descriptive_revision,COALESCE(es.incarnation,''),COALESCE(es.revision,0)
  FROM tvdb_jobs j JOIN catalog_entities e ON e.id=j.show_id JOIN catalog_libraries cl ON cl.id=e.library_id JOIN libraries l ON l.id=cl.library_id JOIN tvdb_publication_heads h ON h.show_id=e.id JOIN tvdb_provider_policies p ON p.library_id=cl.library_id LEFT JOIN tvdb_publication_sets es ON es.show_id=e.id
  WHERE j.show_id=? AND l.kind IN('tv','anime') AND p.enabled=1 AND p.algorithm='tvdb-series-exact-title-year-v1'`+screenTVDBFilter(ctx), showID).Scan(&p.Incarnation, &p.Library, &p.Status, &p.Title, &p.Year, &p.Order, &p.Generation, &p.JobRevision, &p.Provider, &p.Page, &p.Attempts, &p.After, &p.ID, &p.LeaseUntil, &p.Policy, &p.PolicyRevision, &p.Algorithm, &p.Mode, &p.Source, &p.Hierarchy, &p.Selection, &p.Description, &p.Set, &p.SetRevision)
	if err == nil {
		online, epoch, e := onlineLookupPolicyTx(ctx, tx)
		if e != nil {
			return p, e
		}
		if !online {
			return p, sql.ErrNoRows
		}
		p.LookupEpoch = epoch
		if err = tx.QueryRowContext(ctx, `SELECT revision FROM screen_metadata_consent WHERE singleton=1`).Scan(&p.ConsentRevision); err != nil {
			return p, err
		}
	}
	if err == nil && screenTVDBRestricted(ctx) {
		b, e := readScreenBase(ctx, tx, "show", show)
		if e != nil {
			return p, e
		}
		if !b.Available || !b.Enabled || !b.Confirmed {
			return p, sql.ErrNoRows
		}
		p.ScreenFence = screenDigest([]any{b.Configuration, b.ScanRevision, b.PolicyRevision, b.ConsentRevision, b.Confirmed, b.Enabled, b.SourceDigest, b.Selection, b.Provider, b.ProviderID, b.Order, b.Accepted})
	}
	if p.Status == "pending_search" {
		p.Phase = "search"
	} else if p.Status == "pending_episodes" {
		p.Phase = "page"
	} else {
		p.Phase = "projection"
	}
	return p, err
}
func tvdbAuthority(p tvdbPublication) tvdbPublication {
	p.Digest = ""
	if p.Phase == "page" {
		p.Source = 0
		p.Hierarchy = 0
		p.Description = 0
		p.Title = ""
		p.Year = 0
		p.After = ""
	}
	return p
}
func (s *Service) activeTVDBPublication(ctx context.Context, tx *sql.Tx, p tvdbPublication) error {
	if ready, e := publicationSourcesReady(ctx, tx); e != nil {
		return e
	} else if !ready {
		return errTVDBStale
	}
	now := tvdbStamp(s.publicationTime())
	current, err := readTVDBPublication(ctx, tx, p.Show)
	if errors.Is(err, sql.ErrNoRows) {
		return errTVDBStale
	}
	if err != nil {
		return err
	}
	if p.LeaseUntil <= now || tvdbDigest(tvdbAuthority(current)) != p.Digest {
		return errTVDBStale
	}
	var n int
	err = tx.QueryRowContext(ctx, `SELECT count(*) FROM tvdb_publication_operations WHERE id=? AND show_id=? AND phase=? AND input_digest=? AND status IN('claimed','ready')`, p.ID, p.ShowID, p.Phase, p.Digest).Scan(&n)
	if err != nil {
		return err
	}
	if n != 1 {
		return errTVDBStale
	}
	return nil
}
func (s *Service) cleanupTVDBPublications(ctx context.Context, tx *sql.Tx) error {
	stamp := tvdbStamp(s.publicationTime())
	_, err := tx.ExecContext(ctx, `UPDATE tvdb_publication_operations SET status='stale',reason='lease_lost',finished_at=? WHERE id IN(SELECT o.id FROM tvdb_publication_operations o WHERE o.status IN('claimed','ready') AND NOT EXISTS(SELECT 1 FROM tvdb_jobs j WHERE j.show_id=o.show_id AND j.lease=o.id AND j.generation=o.job_generation AND j.revision=o.job_revision AND j.lease_until>?) ORDER BY o.created_at,o.id LIMIT 128)`, stamp, stamp)
	if err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, tvdbTerminalPayloadQuery, stamp)
	if err != nil {
		return err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		for _, table := range []string{"tvdb_publication_candidates", "tvdb_projection_targets"} {
			if _, err = tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE operation_id=?`, id); err != nil {
				return err
			}
		}
	}
	_, err = tx.ExecContext(ctx, tvdbTerminalDeleteQuery, stamp, tvdbStamp(s.publicationTime().Add(-24*time.Hour)))
	return err
}
func (s *Service) claimTVDBAcquisition(ctx context.Context) (*tvdbPublication, error) {
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
	var n int
	stamp := tvdbStamp(s.publicationTime())
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM(SELECT id FROM tvdb_publication_operations WHERE status IN('claimed','ready') LIMIT 8)`).Scan(&n); err != nil {
		return nil, err
	}
	if n >= 8 {
		return nil, gated.Commit()
	}
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM(SELECT id FROM tvdb_publication_operations LIMIT 4096)`).Scan(&n); err != nil {
		return nil, err
	}
	if n >= 4096 {
		return nil, gated.Commit()
	}
	var show string
	err = tx.QueryRowContext(ctx, `SELECT pid(e.public_id) FROM tvdb_jobs j JOIN catalog_entities e ON e.id=j.show_id JOIN catalog_libraries cl ON cl.id=e.library_id JOIN libraries l ON l.id=cl.library_id JOIN tvdb_provider_policies p ON p.library_id=cl.library_id WHERE j.status IN('pending_search','pending_episodes') AND j.next_attempt<=? AND j.lease_until<=? AND COALESCE((SELECT next_attempt FROM metadata_provider_cooldowns WHERE provider='tvdb'),'')<=? AND l.kind IN('tv','anime') AND p.enabled=1 AND p.algorithm='tvdb-series-exact-title-year-v1'`+screenTVDBFilter(ctx)+` ORDER BY j.next_attempt,j.show_id LIMIT 1`, stamp, stamp, stamp).Scan(&show)
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
	if p.Provider == 0 {
		locked, err := repairIdentityLocked(ctx, tx, RepairTarget{"show", show})
		if err != nil {
			return nil, err
		}
		if locked {
			if _, err = tx.ExecContext(ctx, `UPDATE tvdb_jobs SET status='needs_selection',error='owner_identity_locked',revision=revision+1 WHERE show_id=?`, p.ShowID); err != nil {
				return nil, err
			}
			return nil, gated.Commit()
		}
	}
	if p.Generation < 1 || p.JobRevision < 1 || p.Attempts < 0 || p.Attempts > 100 || len(p.Title) > 2048 {
		return nil, tvdbProblem("invalid_job")
	}
	if p.Phase == "page" {
		if p.Provider <= 0 || !tvdbOrder(p.Order) {
			return nil, tvdbProblem("invalid_selection")
		}
		var valid int
		err = tx.QueryRowContext(ctx, `SELECT count(*) FROM tvdb_publication_sets WHERE show_id=? AND incarnation=? AND generation=? AND provider_id=? AND episode_order=? AND policy_incarnation=? AND policy_revision=? AND consent_revision=?`, p.ShowID, p.Set, p.Generation, p.Provider, p.Order, p.Policy, p.PolicyRevision, p.ConsentRevision).Scan(&valid)
		if err != nil {
			return nil, err
		}
		if valid == 1 {
			_, err = s.attestTVDBSet(ctx, tx, p, p.Page, false)
			if err != nil && !errors.Is(err, errTVDBEvidence) {
				return nil, err
			}
			if err != nil {
				valid = 0
			}
		}
		if valid == 0 {
			if _, err = tx.ExecContext(ctx, `DELETE FROM tvdb_projection_coverage WHERE show_id=?`, p.ShowID); err != nil {
				return nil, err
			}
			if _, err = tx.ExecContext(ctx, `DELETE FROM tvdb_publication_sets WHERE show_id=?`, p.ShowID); err != nil {
				return nil, err
			}
			if _, err = tx.ExecContext(ctx, `DELETE FROM tvdb_episode_evidence WHERE show_id=?`, p.ShowID); err != nil {
				return nil, err
			}
			if _, err = tx.ExecContext(ctx, `UPDATE tvdb_jobs SET generation=generation+1,revision=revision+1,page=0,apply_after='',staged_bytes=0,lease='',lease_until='' WHERE show_id=?`, p.ShowID); err != nil {
				return nil, err
			}
			if _, err = tx.ExecContext(ctx, `INSERT INTO tvdb_publication_sets(show_id,generation,provider_id,episode_order,policy_incarnation,policy_revision,consent_revision) SELECT show_id,generation,provider_id,episode_order,?,?,? FROM tvdb_jobs WHERE show_id=?`, p.Policy, p.PolicyRevision, p.ConsentRevision, p.ShowID); err != nil {
				return nil, err
			}
			p, err = readTVDBPublication(ctx, tx, show)
			if err != nil {
				return nil, err
			}
		}
	}
	p.ID = identity.Token()
	p.LeaseUntil = tvdbStamp(s.publicationTime().Add(time.Minute))
	p.Digest = tvdbDigest(tvdbAuthority(p))
	_, err = tx.ExecContext(ctx, `INSERT INTO tvdb_publication_operations(id,show_id,show_incarnation,phase,library_id,query_title,query_year,job_generation,job_revision,lease_until,accepted_provider_id,episode_order,policy_incarnation,policy_revision,algorithm,refresh_mode,source_revision,hierarchy_revision,selection_revision,descriptive_revision,set_incarnation,set_revision,page,after_item,input_digest,status,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,'claimed',?)`, p.ID, p.ShowID, p.Incarnation, p.Phase, p.Library, p.Title, p.Year, p.Generation, p.JobRevision, p.LeaseUntil, p.Provider, p.Order, p.Policy, p.PolicyRevision, p.Algorithm, p.Mode, p.Source, p.Hierarchy, p.Selection, p.Description, p.Set, p.SetRevision, p.Page, p.After, p.Digest, stamp)
	if err != nil {
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
func (s *Service) finishTVDBPublication(ctx context.Context, tx *sql.Tx, p tvdbPublication, status, reason string) error {
	_, err := tx.ExecContext(ctx, `UPDATE tvdb_publication_operations SET status=?,reason=?,finished_at=? WHERE id=? AND input_digest=? AND status IN('claimed','ready')`, status, reason, tvdbStamp(s.publicationTime()), p.ID, p.Digest)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE tvdb_jobs SET lease='',lease_until='' WHERE show_id=? AND lease=?`, p.ShowID, p.ID)
	return err
}
func (s *Service) failTVDBPublication(ctx context.Context, p tvdbPublication, problem error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	gated2, err := dbwork.Begin(ctx, s.db, dbwork.ClassBackgroundMedia)
	if err != nil {
		return err
	}
	tx := gated2.Tx()
	defer gated2.Rollback()
	err = s.activeTVDBPublication(ctx, tx, p)
	if err != nil && !errors.Is(err, errTVDBStale) {
		return err
	}
	if errors.Is(err, errTVDBStale) || errors.Is(problem, errTVDBStale) {
		if err = s.finishTVDBPublication(ctx, tx, p, "stale", "authority_changed"); err != nil {
			return err
		}
		return gated2.Commit()
	}
	if errors.Is(problem, errTVDBEvidence) {
		if _, err = tx.ExecContext(ctx, `UPDATE tvdb_jobs SET status='pending_episodes',page=0,apply_after='',attempts=0,error='',next_attempt='',retry_status='',revision=revision+1 WHERE show_id=?`, p.ShowID); err != nil {
			return err
		}
		if err = s.finishTVDBPublication(ctx, tx, p, "stale", "evidence_changed"); err != nil {
			return err
		}
		return gated2.Commit()
	}
	attempts := p.Attempts + 1
	delay := time.Duration(30*(1<<min(attempts-1, 6))) * time.Second
	status := p.Status
	code := "unavailable"
	var provider *metadataprovider.Error
	if errors.As(problem, &provider) {
		if len(provider.Code) > 0 && len(provider.Code) <= 64 && strings.Trim(provider.Code, "abcdefghijklmnopqrstuvwxyz_") == "" {
			code = provider.Code
		}
		if provider.RetryAfter > delay {
			delay = provider.RetryAfter
		}
		if delay > 24*time.Hour {
			delay = 24 * time.Hour
		}
		if !provider.Retryable() {
			status = "unresolved"
		}
		// A provider-wide cooldown is applied only while this live claim remains valid.
		// Stale errors and cancellation never update it.
		if provider.RetryAfter > 0 {
			_, err = tx.ExecContext(ctx, `INSERT INTO metadata_provider_cooldowns VALUES('tvdb',?) ON CONFLICT(provider) DO UPDATE SET next_attempt=max(next_attempt,excluded.next_attempt)`, tvdbStamp(s.publicationTime().Add(delay)))
			if err != nil {
				return err
			}
		}
	}
	if attempts >= 4 && status == p.Status {
		status = "unavailable"
	}
	_, err = tx.ExecContext(ctx, `UPDATE tvdb_jobs SET status=?,attempts=?,next_attempt=?,error=?,retry_status=?,revision=revision+1 WHERE show_id=?`, status, attempts, tvdbStamp(s.publicationTime().Add(delay)), code, p.Status, p.ShowID)
	if err != nil {
		return err
	}
	if err = s.finishTVDBPublication(ctx, tx, p, "failed", code); err != nil {
		return err
	}
	return gated2.Commit()
}
func (s *Service) runTVDBAcquisition(ctx context.Context, p tvdbPublication) error {
	ctx = s.lookupContext(ctx)
	requestCtx := ctx
	if screenTVDBRestricted(ctx) {
		var cancel context.CancelFunc
		requestCtx, cancel = context.WithCancel(ctx)
		defer cancel()
		supervise.Go("metadata.tvdb.watch", func() {
			tick := time.NewTicker(250 * time.Millisecond)
			defer tick.Stop()
			for {
				select {
				case <-requestCtx.Done():
					return
				case <-tick.C:
					gated3, e := dbwork.BeginSnapshot(requestCtx, s.db)
					if e != nil {
						cancel()
						return
					}
					tx := gated3.Tx()
					e = s.activeTVDBPublication(requestCtx, tx, p)
					gated3.Rollback()
					if e != nil {
						cancel()
						return
					}
				}
			}
		})
	}
	var err error
	if s.tvdb == nil {
		err = tvdbProblem("unavailable")
	} else if p.Phase == "search" {
		var result []metadataprovider.SeriesCandidate
		result, err = s.tvdb.SearchSeries(requestCtx, p.Title, p.Year)
		if err == nil {
			err = s.publishTVDBSearch(ctx, p, result)
		}
	} else if p.Phase == "page" {
		var result metadataprovider.EpisodePage
		result, err = s.tvdb.Episodes(requestCtx, p.Provider, metadataprovider.EpisodeOrder(p.Order), p.Page)
		if err == nil {
			err = s.commitTVDBPage(ctx, p, result)
		}
	} else {
		return tvdbProblem("invalid_phase")
	}
	if err == nil {
		return nil
	}
	return s.failTVDBPublication(ctx, p, err)
}
