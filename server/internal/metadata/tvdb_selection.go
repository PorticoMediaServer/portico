package metadata

import (
	"context"
	"database/sql"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/requestinput"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/entityid"
	"github.com/PorticoMediaServer/portico/server/internal/metadataprovider"
)

var ErrTVDBConflict = errors.New("TVDB metadata state changed; refresh before selecting")

type TVDBCandidate struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Year       int    `json:"year"`
	Overview   string `json:"overview"`
	ObservedAt string `json:"observedAt"`
}
type TVDBState struct {
	ServerID    string          `json:"serverId"`
	ViewerFence string          `json:"viewerFence"`
	LibraryID   string          `json:"libraryId"`
	ShowID      string          `json:"showId"`
	Revision    int64           `json:"revision"`
	Status      string          `json:"status"`
	ProviderID  int64           `json:"providerId"`
	Order       string          `json:"order"`
	Page        int             `json:"page"`
	Attempts    int             `json:"attempts"`
	NextAttempt string          `json:"nextAttempt"`
	Error       string          `json:"error"`
	Candidates  []TVDBCandidate `json:"candidates"`
	Orders      []string        `json:"orders"`
	Actions     []string        `json:"actions"`
	Attribution string          `json:"attribution"`
}
type TVDBSelection struct {
	Actor            MBActor `json:"-"`
	ExpectedRevision int64   `json:"expectedRevision"`
	ProviderID       int64   `json:"providerId"`
	Order            string  `json:"order"`
	// Automatic marks the matcher's own selection (a show it matched with
	// TheTVDB): the episode job starts, and nothing is recorded as the owner's.
	Automatic bool `json:"-"`
}

func (s *Service) TVDBState(show string) (TVDBState, error) {
	out := TVDBState{ShowID: show, Candidates: []TVDBCandidate{}, Actions: []string{}, Orders: []string{"official", "dvd", "absolute", "default", "alternate", "regional"}, Attribution: "TV metadata provided by TheTVDB."}
	entity, e := entityid.Resolve(context.Background(), s.db, show)
	if errors.Is(e, entityid.ErrNotFound) {
		return out, sql.ErrNoRows
	}
	if e != nil {
		return out, e
	}
	e = s.db.QueryRow(`SELECT revision,status,provider_id,episode_order,page,attempts,next_attempt,error FROM tvdb_jobs WHERE show_id=?`, entity).Scan(&out.Revision, &out.Status, &out.ProviderID, &out.Order, &out.Page, &out.Attempts, &out.NextAttempt, &out.Error)
	if e != nil {
		return out, e
	}
	rows, e := s.db.Query(`SELECT provider_id,name,year,overview,observed_at FROM tvdb_series_candidates WHERE show_id=? ORDER BY provider_id LIMIT 25`, entity)
	if e != nil {
		return out, e
	}
	for rows.Next() {
		var v TVDBCandidate
		if e = rows.Scan(&v.ID, &v.Name, &v.Year, &v.Overview, &v.ObservedAt); e != nil {
			rows.Close()
			return out, e
		}
		out.Candidates = append(out.Candidates, v)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	var after int64
	if e = s.db.QueryRow(`SELECT revision FROM tvdb_jobs WHERE show_id=?`, entity).Scan(&after); e != nil {
		return out, e
	}
	if after != out.Revision {
		return out, ErrTVDBConflict
	}
	if len(out.Candidates) > 0 {
		out.Actions = append(out.Actions, "select")
	}
	if out.Status == "unavailable" || out.Status == "unresolved" || out.Status == "needs_selection" {
		out.Actions = append(out.Actions, "retry")
	}
	return out, nil
}
func (s *Service) SelectTVDB(show string, body TVDBSelection, authorize func(*sql.Tx) error) error {
	switch metadataprovider.EpisodeOrder(body.Order) {
	case metadataprovider.Official, metadataprovider.DVD, metadataprovider.Absolute, metadataprovider.Default, metadataprovider.Alternate, metadataprovider.Regional:
	default:
		return requestinput.New("explicit valid TVDB ordering is required")
	}
	if body.ProviderID <= 0 || body.ExpectedRevision < 1 {
		return requestinput.New("providerId and expectedRevision are required")
	}
	gated, e := dbwork.Begin(context.Background(), s.db, dbwork.ClassBackgroundMedia)
	if e != nil {
		return e
	}
	tx := gated.Tx()
	defer gated.Rollback()
	if authorize != nil {
		if e = authorize(tx); e != nil {
			return e
		}
	}
	target := RepairTarget{Kind: "show", ID: show}
	before, ent, e := readRepairSnapshot(context.Background(), tx, target)
	if e != nil {
		return e
	}
	base, e := repairRevision(context.Background(), tx, target, before, ent)
	if e != nil {
		return e
	}
	if e = s.selectTVDBTx(tx, show, body); e != nil {
		return e
	}
	if e = recordRepair(context.Background(), tx, target, before, base, "identify", body.Actor.Authority+":"+body.Actor.AccountID+":"+body.Actor.ProfileID); e != nil {
		return e
	}
	if authorize != nil {
		if e = authorize(tx); e != nil {
			return e
		}
	}
	return gated.Commit()
}

func (s *Service) selectTVDBTx(tx *sql.Tx, show string, body TVDBSelection) error {
	var e error

	switch metadataprovider.EpisodeOrder(body.Order) {
	case metadataprovider.Official, metadataprovider.DVD, metadataprovider.Absolute, metadataprovider.Default, metadataprovider.Alternate, metadataprovider.Regional:
	default:
		return requestinput.New("explicit valid TVDB ordering is required")
	}
	if body.ProviderID <= 0 || body.ExpectedRevision < 1 {
		return requestinput.New("providerId and expectedRevision are required")
	}
	entity, e := resolveEntity(context.Background(), tx, show)
	if e != nil {
		return e
	}
	var revision int64
	if e = tx.QueryRow(`SELECT revision FROM tvdb_jobs WHERE show_id=?`, entity).Scan(&revision); e != nil {
		return e
	}
	if revision != body.ExpectedRevision {
		return ErrTVDBConflict
	}
	var selected int
	if e = tx.QueryRow(`SELECT count(*) FROM tvdb_series_candidates WHERE show_id=? AND provider_id=?`, entity, body.ProviderID).Scan(&selected); e != nil {
		return e
	}
	if selected != 1 {
		return requestinput.New("select an observed TVDB candidate")
	}
	// Keep the last valid episode projection while replacement pages are acquired.
	// New publication is fenced by the selection generation; no placeholder clearing.
	for _, table := range []string{"tvdb_projection_coverage", "tvdb_publication_sets", "tvdb_episode_evidence"} {
		if _, e = tx.Exec(`DELETE FROM `+table+` WHERE show_id=?`, entity); e != nil {
			return e
		}
	}
	if _, e = tx.Exec(`UPDATE tvdb_publication_operations SET status='stale',reason='owner_selection',finished_at=? WHERE show_id=? AND status IN('claimed','ready')`, tvdbStamp(s.publicationTime()), entity); e != nil {
		return e
	}
	if _, e = tx.Exec(`UPDATE tvdb_jobs SET provider_id=?,episode_order=?,status='pending_episodes',generation=generation+1,revision=revision+1,page=0,apply_after='',staged_bytes=0,attempts=0,next_attempt='',error='',lease='',lease_until='' WHERE show_id=?`, body.ProviderID, body.Order, entity); e != nil {
		return e
	}
	// Reconcile every owner entrypoint with the screen matching intent in this
	// transaction; the remote worker still requires current policy and consent.
	// A different show's links go now; the screen publication that follows
	// (itself a caller, with the identity already on record) keeps its own.
	if e = retireScreenLinks(context.Background(), tx, "show", entity, metadataprovider.ScreenID{Provider: "tvdb", Type: "show", ID: intString(body.ProviderID)}); e != nil {
		return e
	}
	if !body.Automatic {
		if _, e = tx.Exec(`UPDATE screen_metadata_work SET provider='tvdb',provider_type='show',provider_id=?,episode_order=?,selection_mode='owner',selection_revision=selection_revision+1,generation=generation+1,revision=revision+1,status='pending',requested_provider='',query_override='',lease='',lease_until='',next_attempt='',error='' WHERE target_kind='show' AND target_id=?`, intString(body.ProviderID), body.Order, entity); e != nil {
			return e
		}
		// An explicit provider choice is an owner identity lock, also when invoked
		// by the pre-existing review API rather than the generic repair controller.
		if _, e = tx.Exec(`INSERT INTO metadata_relationship_decisions(kind,entity_id,relationship,value_json,source,locked,actor,observed_at) VALUES('show',?,'identity','[]','owner_lock',1,?,?) ON CONFLICT(kind,entity_id,relationship) DO UPDATE SET locked=1,actor=excluded.actor,observed_at=excluded.observed_at`, entity, body.Actor.Authority+":"+body.Actor.AccountID+":"+body.Actor.ProfileID, tvdbStamp(s.publicationTime())); e != nil {
			return e
		}
	}
	return compactcatalog.SetFieldsTx(context.Background(), tx, entity, compactcatalog.Automatic, map[string]any{"provider_match_status": "matched"})
}

func (s *Service) RetryTVDB(show string, revision int64, authorize func(*sql.Tx) error) error {
	gated2, e := dbwork.Begin(context.Background(), s.db, dbwork.ClassBackgroundMedia)
	if e != nil {
		return e
	}
	tx := gated2.Tx()
	defer gated2.Rollback()
	if authorize != nil {
		if e = authorize(tx); e != nil {
			return e
		}
	}
	entity, e := resolveEntity(context.Background(), tx, show)
	if e != nil {
		return e
	}
	var current int64
	var providerID int64
	var order, status, retryStatus string
	if e = tx.QueryRow(`SELECT revision,provider_id,episode_order,status,retry_status FROM tvdb_jobs WHERE show_id=?`, entity).Scan(&current, &providerID, &order, &status, &retryStatus); e != nil {
		return e
	}
	if current != revision {
		return ErrTVDBConflict
	}
	if status != "unavailable" && status != "unresolved" && status != "needs_selection" {
		return requestinput.New("retry requires a failed or unresolved job; select a candidate to refresh completed metadata")
	}
	status = "pending_search"
	if providerID > 0 && tvdbOrder(order) {
		status = "pending_episodes"
		if retryStatus == "pending_apply" {
			status = "pending_apply"
		}
	}
	if _, e = tx.Exec(`UPDATE tvdb_jobs SET status=?,revision=revision+1,attempts=0,next_attempt='',error='',lease='',lease_until='' WHERE show_id=?`, status, entity); e != nil {
		return e
	}
	return gated2.Commit()
}
