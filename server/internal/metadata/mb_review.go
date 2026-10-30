package metadata

import (
	"context"
	"database/sql"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/requestinput"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/entityid"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

var ErrMBConflict = errors.New("MusicBrainz metadata state changed; refresh before selecting")

type MBCandidate struct {
	Confidence float64  `json:"confidence"`
	Reasons    []string `json:"reasons"`
	Decision   string   `json:"decision"`
	ID         string   `json:"id"`
	Title      string   `json:"title"`
	Artist     string   `json:"artist"`
	Edition    string   `json:"edition"`
	ObservedAt string   `json:"observedAt"`
}

// MBPublished describes committed provider evidence, separately from current search
// candidates and local display values. Track counts are bounded server aggregates.
type MBPublished struct {
	Details               *MusicPublishedDetails `json:"details,omitempty"`
	ReconciliationPending bool                   `json:"reconciliationPending"`
	ReleaseID             string                 `json:"releaseId"`
	ReleaseGroupID        string                 `json:"releaseGroupId"`
	RecordingID           string                 `json:"recordingId"`
	TrackID               string                 `json:"trackId"`
	Title                 string                 `json:"title"`
	Artist                string                 `json:"artist"`
	Date                  string                 `json:"date"`
	Country               string                 `json:"country"`
	ObservedAt            string                 `json:"observedAt"`
	TrackTitle            string                 `json:"trackTitle"`
	TrackStatus           string                 `json:"trackStatus"`
	TotalTracks           int                    `json:"totalTracks"`
	MatchedTracks         int                    `json:"matchedTracks"`
	PendingTracks         int                    `json:"pendingTracks"`
	ReviewTracks          int                    `json:"reviewTracks"`
}
type MBState struct {
	Policy      *MusicPolicy           `json:"policy,omitempty"`
	Observation *MusicMatchObservation `json:"observation,omitempty"`
	Published   *MBPublished           `json:"published,omitempty"`
	ServerID    string                 `json:"serverId"`
	ViewerFence string                 `json:"viewerFence"`
	LibraryID   string                 `json:"libraryId"`
	Kind        string                 `json:"kind"`
	EntityID    string                 `json:"entityId"`
	Revision    int64                  `json:"revision"`
	Status      string                 `json:"status"`
	SelectedID  string                 `json:"selectedId"`
	Manual      bool                   `json:"manual"`
	Attempts    int                    `json:"attempts"`
	NextAttempt string                 `json:"nextAttempt"`
	Error       string                 `json:"error"`
	Candidates  []MBCandidate          `json:"candidates"`
	Actions     []string               `json:"actions"`
	Attribution string                 `json:"attribution"`
}
type MBActor struct{ Authority, AccountID, ProfileID string }

type MBSelection struct {
	Actor            MBActor `json:"-"`
	ExpectedRevision int64   `json:"expectedRevision"`
	ProviderID       string  `json:"providerId"`
}

func (s *Service) MusicBrainzState(kind, id string) (MBState, error) {
	out := MBState{Kind: kind, EntityID: id, Candidates: []MBCandidate{}, Actions: []string{}, Attribution: "Music metadata provided by MusicBrainz."}
	gated, e := dbwork.BeginSnapshot(context.Background(), s.db)
	if e != nil {
		return out, e
	}
	tx := gated.Tx()
	defer gated.Rollback()
	// The public id resolves once; every statement below seeks on the integer
	// entity id. Unknown or malformed ids read as not found, as before.
	entity, e := entityid.Resolve(context.Background(), tx, id)
	if e != nil {
		if errors.Is(e, entityid.ErrNotFound) {
			return out, sql.ErrNoRows
		}
		return out, e
	}
	e = tx.QueryRow(`SELECT revision,status,selected_id,manual,attempts,next_attempt,error FROM mb_jobs WHERE kind=? AND entity_id=?`, kind, entity).Scan(&out.Revision, &out.Status, &out.SelectedID, &out.Manual, &out.Attempts, &out.NextAttempt, &out.Error)
	if e != nil {
		return out, e
	}
	rows, e := tx.Query(`SELECT provider_id,title,artist,edition,observed_at,confidence,decision FROM mb_candidates WHERE kind=? AND entity_id=? ORDER BY confidence DESC,provider_id LIMIT 25`, kind, entity)
	if e != nil {
		return out, e
	}
	for rows.Next() {
		var c MBCandidate
		c.Reasons = []string{}
		if e = rows.Scan(&c.ID, &c.Title, &c.Artist, &c.Edition, &c.ObservedAt, &c.Confidence, &c.Decision); e != nil {
			rows.Close()
			return out, e
		}
		out.Candidates = append(out.Candidates, c)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	for n := range out.Candidates {
		rows, e := tx.Query(`SELECT reason FROM mb_candidate_reasons WHERE kind=? AND entity_id=? AND provider_id=? ORDER BY ordinal LIMIT 16`, kind, entity, out.Candidates[n].ID)
		if e != nil {
			return out, e
		}
		for rows.Next() {
			var reason string
			if e = rows.Scan(&reason); e != nil {
				rows.Close()
				return out, e
			}
			out.Candidates[n].Reasons = append(out.Candidates[n].Reasons, reason)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return out, e
		}
	}
	if out.Published, e = mbPublished(tx, kind, id); e != nil {
		return out, e
	}
	if len(out.Candidates) > 0 {
		out.Actions = append(out.Actions, "select")
	}
	if out.Status == "unavailable" || out.Status == "unresolved" || out.Status == "needs_selection" {
		out.Actions = append(out.Actions, "retry")
	}
	library, e := musicLibrary(tx, kind, id)
	if e != nil {
		return out, e
	}
	out.LibraryID = library
	policy, e := s.musicPolicy(tx, library)
	if e != nil {
		return out, e
	}
	out.Policy = &policy
	observation := MusicMatchObservation{}
	e = tx.QueryRow(`SELECT fingerprint_status,provider_error,confidence,margin,strong_signals,algorithm FROM music_match_observations WHERE kind=? AND entity_id=?`, kind, entity).Scan(&observation.FingerprintStatus, &observation.ProviderError, &observation.Confidence, &observation.Margin, &observation.StrongSignals, &observation.Algorithm)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return out, e
	}
	if e == nil {
		out.Observation = &observation
		if observation.FingerprintStatus == "matched" {
			out.Attribution += " Audio fingerprint lookup provided by AcoustID (MusicBrainz recording identifiers)."
		}
	}
	out.Actions = append(out.Actions, "search", "policy")
	return out, gated.Commit()
}
func (s *Service) SelectMusicBrainz(kind, id string, m MBSelection, authorize func(*sql.Tx) error) error {
	s.resetMBMutationPick()
	if !mbIdentifier.MatchString(m.ProviderID) || m.ExpectedRevision < 1 || m.Actor.Authority == "" || m.Actor.AccountID == "" || m.Actor.ProfileID == "" {
		return requestinput.New("observed providerId and expectedRevision are required")
	}
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
	target := RepairTarget{Kind: kind, ID: id}
	if kind == "song" {
		target.Kind = "item"
	}
	before, ent, e := readRepairSnapshot(context.Background(), tx, target)
	if e != nil {
		return e
	}
	base, e := repairRevision(context.Background(), tx, target, before, ent)
	if e != nil {
		return e
	}
	if e = s.selectMusicBrainzTx(tx, kind, id, m); e != nil {
		return e
	}
	if e = recordRepair(context.Background(), tx, target, before, base, "identify", m.Actor.Authority+":"+m.Actor.AccountID+":"+m.Actor.ProfileID); e != nil {
		return e
	}
	if authorize != nil {
		if e = authorize(tx); e != nil {
			return e
		}
	}
	return gated2.Commit()
}

func (s *Service) selectMusicBrainzTx(tx *sql.Tx, kind, id string, m MBSelection) error {
	var e error

	if !mbIdentifier.MatchString(m.ProviderID) || m.ExpectedRevision < 1 || m.Actor.Authority == "" || m.Actor.AccountID == "" || m.Actor.ProfileID == "" {
		return requestinput.New("observed providerId and expectedRevision are required")
	}
	entity, e := entityid.Resolve(context.Background(), tx, id)
	if e != nil {
		if errors.Is(e, entityid.ErrNotFound) {
			return sql.ErrNoRows
		}
		return e
	}
	var revision, generation int64
	if e = tx.QueryRow(`SELECT revision,generation FROM mb_jobs WHERE kind=? AND entity_id=?`, kind, entity).Scan(&revision, &generation); e != nil {
		return e
	}
	if revision != m.ExpectedRevision {
		return ErrMBConflict
	}
	m.ProviderID = strings.ToLower(m.ProviderID)
	var queryDigest string
	if e = tx.QueryRow(`SELECT query_digest FROM mb_candidates WHERE kind=? AND entity_id=? AND provider_id=?`, kind, entity, m.ProviderID).Scan(&queryDigest); e != nil {
		return requestinput.New("select an observed MusicBrainz candidate")
	}
	base, e := readMBBase(context.Background(), tx, kind, id)
	if e != nil {
		return ErrMBConflict
	}
	digest := publicationDigest(base)
	if digest != queryDigest {
		return ErrMBConflict
	}
	var prior string
	q := `SELECT COALESCE((SELECT recording_revision FROM mb_song_links WHERE item_id=?),'')`
	if kind == "album" {
		q = `SELECT COALESCE((SELECT release_revision FROM mb_album_links WHERE album_id=?),'')`
	}
	if e = tx.QueryRow(q, entity).Scan(&prior); e != nil {
		return e
	}
	now := s.publicationTime().Format(time.RFC3339)
	if _, e = tx.Exec(`UPDATE mb_selection_receipts SET status='superseded',finished_at=? WHERE kind=? AND entity_id=? AND status='pending'`, now, kind, entity); e != nil {
		return e
	}
	// Retain the newest 127 compact prior decisions plus the latest applied
	// receipt. Pending selection is never trimmed and the new receipt is added
	// only in this same transaction. Actor/query/prior identity remain original.
	if _, e = tx.Exec(`DELETE FROM mb_selection_receipts WHERE kind=? AND entity_id=? AND status<>'pending' AND id IN(SELECT id FROM mb_selection_receipts WHERE kind=? AND entity_id=? AND status<>'pending' ORDER BY created_at DESC,id DESC LIMIT -1 OFFSET 127) AND id<>COALESCE((SELECT id FROM mb_selection_receipts WHERE kind=? AND entity_id=? AND status='applied' ORDER BY created_at DESC,id DESC LIMIT 1),'')`, kind, entity, kind, entity, kind, entity); e != nil {
		return e
	}
	if _, e = tx.Exec(`INSERT INTO mb_selection_receipts(id,kind,entity_id,job_generation,actor_authority,actor_account_id,actor_profile_id,candidate_id,query_digest,prior_revision_id,status,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,'pending',?)`, identity.Token(), kind, entity, generation+1, m.Actor.Authority, m.Actor.AccountID, m.Actor.ProfileID, m.ProviderID, queryDigest, prior, now); e != nil {
		return e
	}
	if _, e = tx.Exec(`UPDATE mb_jobs SET status='pending',selected_id=?,manual=1,review_search=0,attempts=0,next_attempt='',error='',revision=revision+1,generation=generation+1,lease='',lease_until='' WHERE kind=? AND entity_id=?`, m.ProviderID, kind, entity); e != nil {
		return e
	}
	return nil
}

func (s *Service) RetryMusicBrainz(kind, id string, revision int64, authorize func(*sql.Tx) error) error {
	s.resetMBMutationPick()
	gated3, e := dbwork.Begin(context.Background(), s.db, dbwork.ClassBackgroundMedia)
	if e != nil {
		return e
	}
	tx := gated3.Tx()
	defer gated3.Rollback()
	if authorize != nil {
		if e = authorize(tx); e != nil {
			return e
		}
	}
	var current int64
	var status string
	entity, e := entityid.Resolve(context.Background(), tx, id)
	if e != nil {
		if errors.Is(e, entityid.ErrNotFound) {
			return sql.ErrNoRows
		}
		return e
	}
	if e = tx.QueryRow(`SELECT revision,status FROM mb_jobs WHERE kind=? AND entity_id=?`, kind, entity).Scan(&current, &status); e != nil {
		return e
	}
	if revision != current {
		return ErrMBConflict
	}
	if status != "unavailable" && status != "unresolved" && status != "needs_selection" {
		return requestinput.New("retry requires an unresolved or failed job")
	}
	if _, e = tx.Exec(`UPDATE mb_jobs SET status='pending',attempts=0,next_attempt='',error='',revision=revision+1,generation=generation+1,lease='',lease_until='' WHERE kind=? AND entity_id=?`, kind, entity); e != nil {
		return e
	}
	return gated3.Commit()
}

// Search alternatives is a review intent, not permission to replace a manual
// selection. Existing cached/public metadata remains until another selection.
func (s *Service) SearchMusicBrainzAlternatives(kind, id string, revision int64, authorize func(*sql.Tx) error) error {
	s.resetMBMutationPick()
	gated4, e := dbwork.Begin(context.Background(), s.db, dbwork.ClassBackgroundMedia)
	if e != nil {
		return e
	}
	tx := gated4.Tx()
	defer gated4.Rollback()
	if authorize != nil {
		if e = authorize(tx); e != nil {
			return e
		}
	}
	var current int64
	entity, e := entityid.Resolve(context.Background(), tx, id)
	if e != nil {
		if errors.Is(e, entityid.ErrNotFound) {
			return sql.ErrNoRows
		}
		return e
	}
	if e = tx.QueryRow(`SELECT revision FROM mb_jobs WHERE kind=? AND entity_id=?`, kind, entity).Scan(&current); e != nil {
		return e
	}
	if current != revision {
		return ErrMBConflict
	}
	if _, e = tx.Exec(`UPDATE mb_jobs SET review_search=1,status='pending',attempts=0,next_attempt='',error='',revision=revision+1,generation=generation+1,lease='',lease_until='' WHERE kind=? AND entity_id=?`, kind, entity); e != nil {
		return e
	}
	return gated4.Commit()
}

func mbPublished(tx *sql.Tx, kind, id string) (*MBPublished, error) {
	p := &MBPublished{}
	// The public id resolves once; the joins below seek on integer keys. The
	// mb_* link and evidence tables stay as they are.
	entity, e := entityid.Resolve(context.Background(), tx, id)
	if e != nil {
		if errors.Is(e, entityid.ErrNotFound) {
			return nil, sql.ErrNoRows
		}
		return nil, e
	}
	if kind == "album" {
		var revision string
		e = tx.QueryRow(`SELECT r.provider_id,r.release_group_id,r.title,r.artist,r.date,r.country,l.observed_at,l.release_revision FROM mb_album_links l JOIN mb_release_evidence r ON r.revision_id=l.release_revision AND r.provider_id=l.release_id AND r.sealed=1 WHERE l.album_id=?`, entity).Scan(&p.ReleaseID, &p.ReleaseGroupID, &p.Title, &p.Artist, &p.Date, &p.Country, &p.ObservedAt, &revision)
		if errors.Is(e, sql.ErrNoRows) {
			return nil, nil
		}
		if e != nil {
			return nil, e
		}
		e = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM mb_album_reconciliations c JOIN mb_album_heads h ON h.album_id=c.album_id WHERE c.album_id=? AND (c.status='pending' OR c.membership_revision<>h.membership_revision))`, entity).Scan(&p.ReconciliationPending)
		if e != nil {
			return nil, e
		}
		e = tx.QueryRow(`SELECT count(*),COALESCE(sum(CASE WHEN j.status='pending' OR (? AND NOT(j.status='matched' AND COALESCE(l.release_revision,'')=? AND l.release_status='matched')) THEN 1 ELSE 0 END),0),COALESCE(sum(CASE WHEN j.status='matched' AND l.release_revision=? AND l.release_status='matched' THEN 1 ELSE 0 END),0) FROM catalog_songs s LEFT JOIN mb_jobs j ON j.kind='song' AND j.entity_id=s.entity_id LEFT JOIN mb_song_links l ON l.item_id=s.entity_id WHERE s.album_id=?`, p.ReconciliationPending, revision, revision, entity).Scan(&p.TotalTracks, &p.PendingTracks, &p.MatchedTracks)
		if e != nil {
			return nil, e
		}
		p.ReviewTracks = p.TotalTracks - p.PendingTracks - p.MatchedTracks
	} else {
		e = tx.QueryRow(`SELECT COALESCE(l.release_id,''),COALESCE(re.release_group_id,''),COALESCE(l.recording_id,''),CASE WHEN l.release_status='matched' THEN COALESCE(l.track_id,'') ELSE '' END,COALESCE(r.title,''),COALESCE(r.artist,''),l.observed_at,COALESCE(t.title,''),l.release_status FROM mb_song_links l LEFT JOIN mb_recording_evidence r ON r.revision_id=l.recording_revision AND r.sealed=1 LEFT JOIN mb_release_evidence re ON re.revision_id=l.release_revision AND re.sealed=1 LEFT JOIN mb_release_tracks t ON t.release_revision=l.release_revision AND t.track_id=l.track_id WHERE l.item_id=?`, entity).Scan(&p.ReleaseID, &p.ReleaseGroupID, &p.RecordingID, &p.TrackID, &p.Title, &p.Artist, &p.ObservedAt, &p.TrackTitle, &p.TrackStatus)
		if errors.Is(e, sql.ErrNoRows) {
			return nil, nil
		}
	}
	if e == nil {
		p.Details, e = musicPublishedDetails(tx, kind, id)
	}
	return p, e
}
