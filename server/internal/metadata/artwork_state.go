package metadata

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"github.com/PorticoMediaServer/portico/server/internal/artworkref"
	"regexp"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/entityid"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

var artworkDigestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

func artDigest(s string) bool { return artworkDigestPattern.MatchString(s) }

// artworkEntity resolves a repair target's public id to its integer entity id
// for the artwork tables' INTEGER entity references. An unknown id reads as no
// row, the same not-found path readRepairEntity returns for unknown targets
// (its Scan surfaces sql.ErrNoRows).
func artworkEntity(ctx context.Context, tx *sql.Tx, id string) (int64, error) {
	entity, err := entityid.Resolve(ctx, tx, id)
	if errors.Is(err, entityid.ErrNotFound) {
		return 0, sql.ErrNoRows
	}
	return entity, err
}

type ArtworkChoice struct {
	Role        string `json:"role"`
	Subject     string `json:"subject"`
	CandidateID string `json:"candidateId"`
	Digest      string `json:"digest"`
	Locked      bool   `json:"locked"`
	Revision    int64  `json:"revision"`
	Attribution string `json:"attribution"`
	URL         string `json:"url"`
}
type ArtworkCandidate struct {
	ID       string  `json:"id"`
	Role     string  `json:"role"`
	Subject  string  `json:"subject"`
	Provider string  `json:"provider"`
	ImageID  string  `json:"imageId"`
	Locale   string  `json:"locale"`
	Rank     float64 `json:"rank"`
	// Votes is how many people rated the image at the provider (TMDB's
	// vote_count); null when the provider publishes no count.
	Votes       *int64 `json:"votes"`
	Attribution string `json:"attribution"`
	Observed    string `json:"observedAt"`
	Current     bool   `json:"current"`
	PreviewURL  string `json:"previewUrl,omitempty"`
}
type ArtworkJob struct {
	ID          string `json:"id"`
	Role        string `json:"role"`
	Subject     string `json:"subject"`
	Status      string `json:"status"`
	Error       string `json:"error"`
	Attempts    int    `json:"attempts"`
	NextAttempt string `json:"nextAttempt"`
}
type ArtworkState struct {
	Candidates []ArtworkCandidate `json:"candidates"`
	Jobs       []ArtworkJob       `json:"jobs"`
}

func artworkPath(t RepairTarget, role, subject, digest string) string {
	path := "/v1/metadata/" + url.PathEscape(t.Kind) + "/" + url.PathEscape(t.ID) + "/art/" + url.PathEscape(role)
	q := url.Values{}
	if subject != "" {
		q.Set("subject", subject)
	}
	if digest != "" {
		q.Set("v", digest)
	}
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	return path
}

// describedArtworkPath is artworkPath for a published source, carrying both
// slots' descriptors so a client can choose one without another request.
func describedArtworkPath(t RepairTarget, role, subject, source string, d artworkref.Descriptor) string {
	path := "/v1/metadata/" + url.PathEscape(t.Kind) + "/" + url.PathEscape(t.ID) + "/art/" + url.PathEscape(role) + "?" + d.Query(source)
	if subject != "" {
		path += "&subject=" + url.QueryEscape(subject)
	}
	return path
}
func readArtworkChoices(ctx context.Context, tx *sql.Tx, t RepairTarget) ([]ArtworkChoice, error) {
	out := []ArtworkChoice{}
	entity, err := artworkEntity(ctx, tx, t.ID)
	if err != nil {
		return out, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT a.role,a.subject,a.candidate_id,a.digest,a.locked,a.revision,c.attribution,`+artworkref.Columns+` FROM artwork_selections a JOIN artwork_candidates c ON c.id=a.candidate_id JOIN artwork_sources src ON src.digest=a.digest JOIN artwork_blobs sb ON sb.digest=src.small_blob JOIN artwork_blobs lb ON lb.digest=src.large_blob WHERE a.kind=? AND a.entity_id=? ORDER BY a.role,a.subject LIMIT 200`, t.Kind, entity)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var a ArtworkChoice
		var d artworkref.Descriptor
		if err = rows.Scan(append([]any{&a.Role, &a.Subject, &a.CandidateID, &a.Digest, &a.Locked, &a.Revision, &a.Attribution}, d.ScanTargets()...)...); err != nil {
			return out, err
		}
		a.URL = describedArtworkPath(t, a.Role, a.Subject, a.Digest, d)
		out = append(out, a)
	}
	return out, rows.Err()
}
func readArtworkState(ctx context.Context, tx *sql.Tx, t RepairTarget) (ArtworkState, error) {
	out := ArtworkState{Candidates: []ArtworkCandidate{}, Jobs: []ArtworkJob{}}
	fence, err := artworkFence(ctx, tx, t)
	if err != nil {
		return out, err
	}
	entity, err := artworkEntity(ctx, tx, t.ID)
	if err != nil {
		return out, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT c.id,c.role,c.subject,c.provider,c.image_id,c.locale,c.rank,c.votes,c.attribution,c.observed_at,c.source_fence=?,COALESCE(p.digest,'') FROM artwork_candidates c LEFT JOIN artwork_previews p ON p.candidate_id=c.id AND p.source_fence=c.source_fence WHERE c.kind=? AND c.entity_id=? ORDER BY (c.source_fence=?) DESC,c.role,c.rank DESC,c.id LIMIT 200`, fence, t.Kind, entity, fence)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var c ArtworkCandidate
		var preview string
		var votes sql.NullInt64
		if err = rows.Scan(&c.ID, &c.Role, &c.Subject, &c.Provider, &c.ImageID, &c.Locale, &c.Rank, &votes, &c.Attribution, &c.Observed, &c.Current, &preview); err != nil {
			rows.Close()
			return out, err
		}
		if votes.Valid {
			c.Votes = &votes.Int64
		}
		if preview != "" && c.Current {
			c.PreviewURL = artworkPath(t, c.Role, c.Subject, preview) + "&candidate=" + c.ID
		}
		out.Candidates = append(out.Candidates, c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	rows, err = tx.QueryContext(ctx, `SELECT id,CASE WHEN preview=1 THEN 'preview:'||role ELSE role END AS role,subject,status,error,attempts,next_attempt FROM artwork_jobs WHERE kind=? AND entity_id=? UNION ALL SELECT provider,'discovery','',status,error,attempts,next_attempt FROM artwork_discovery WHERE kind=? AND entity_id=? ORDER BY role,subject LIMIT 201`, t.Kind, entity, t.Kind, entity)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var j ArtworkJob
		if err = rows.Scan(&j.ID, &j.Role, &j.Subject, &j.Status, &j.Error, &j.Attempts, &j.NextAttempt); err != nil {
			return out, err
		}
		out.Jobs = append(out.Jobs, j)
	}
	return out, rows.Err()
}

// Only physical-source and selected-identity authority participates here. A
// publication of artwork or an unrelated title edit does not invalidate itself.
// Role-specific selection revisions independently fence concurrent owner choices.
func artworkFence(ctx context.Context, tx *sql.Tx, t RepairTarget) (string, error) {
	ent, err := readArtworkTarget(ctx, tx, t)
	if err != nil {
		return "", err
	}
	entity, err := artworkEntity(ctx, tx, t.ID)
	if err != nil {
		return "", err
	}
	authority, err := integratedArtworkAuthority(ctx, tx, t, ent.library)
	if err != nil {
		return "", err
	}
	online, epoch, err := onlineLookupPolicyTx(ctx, tx)
	if err != nil {
		return "", err
	}
	var artworkPolicyRevision int64
	if err = tx.QueryRowContext(ctx, `SELECT artwork_policy_revision FROM metadata_runtime_settings WHERE singleton=1`).Scan(&artworkPolicyRevision); err != nil {
		return "", err
	}
	parts := []any{t, ent.library, authority, online, epoch, artworkPolicyRevision}
	var head string
	reading := t.Kind == "item" && readingRepairKind(ent.itemKind)
	if reading {
		// Ebook/comic entities deliberately have neither AV publication heads
		// nor automatic provider artwork heads. Use their real source lineage;
		// reading an editor must not create an unrelated publication engine.
		head, err = readingArtworkSourceFence(ctx, tx, entity)
	} else {
		err = tx.QueryRowContext(ctx, `SELECT incarnation||':'||revision FROM artwork_entity_heads WHERE kind=? AND entity_id=?`, t.Kind, entity).Scan(&head)
	}
	if err != nil {
		return "", err
	}
	parts = append(parts, head)
	for _, policy := range []struct{ table, where string }{{"metadata_provider_policies", " AND provider='tmdb'"}, {"mb_provider_policies", ""}, {"tvdb_provider_policies", ""}} {
		var value string
		e := tx.QueryRowContext(ctx, `SELECT incarnation||':'||revision||':'||enabled FROM `+policy.table+` WHERE library_id=?`+policy.where, ent.library).Scan(&value)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return "", e
		}
		parts = append(parts, policy.table, value)
	}
	var stamp string
	if t.Kind == "item" && !reading {
		err = tx.QueryRowContext(ctx, `SELECT h.incarnation||':'||h.source_revision||':'||h.identity_revision||':'||COALESCE((SELECT selected_id||':'||generation FROM mb_jobs WHERE kind='song' AND entity_id=h.item_id),'')||':'||COALESCE((SELECT j.provider_id||':'||j.generation FROM catalog_episodes ep JOIN tvdb_jobs j ON j.show_id=ep.show_id WHERE ep.entity_id=h.item_id),'') FROM metadata_publication_heads h WHERE h.item_id=?`, entity).Scan(&stamp)
	} else if t.Kind == "album" {
		err = tx.QueryRowContext(ctx, `SELECT al.local_key||':'||e.title||':'||COALESCE(a.title,'')||':'||COALESCE(j.selected_id,'')||':'||COALESCE(j.generation,0)||':'||COALESCE(l.release_revision,'') FROM catalog_albums al JOIN catalog_entities e ON e.id=al.entity_id LEFT JOIN catalog_entities a ON a.id=al.artist_id LEFT JOIN mb_jobs j ON j.kind='album' AND j.entity_id=al.entity_id LEFT JOIN mb_album_links l ON l.album_id=al.entity_id WHERE al.entity_id=?`, entity).Scan(&stamp)
	} else if t.Kind == "show" {
		err = tx.QueryRowContext(ctx, `SELECT sh.local_key||':'||e.title||':'||COALESCE(j.provider_id,0)||':'||COALESCE(j.generation,0)||':'||COALESCE(j.episode_order,'') FROM catalog_shows sh JOIN catalog_entities e ON e.id=sh.entity_id LEFT JOIN tvdb_jobs j ON j.show_id=sh.entity_id WHERE sh.entity_id=?`, entity).Scan(&stamp)
	} else {
		stamp = head
	}
	if err != nil {
		return "", err
	}
	if t.Kind == "album" {
		var v string
		if err = tx.QueryRowContext(ctx, `SELECT incarnation||':'||revision||':'||membership_revision FROM mb_album_heads WHERE album_id=?`, entity).Scan(&v); err != nil {
			return "", err
		}
		parts = append(parts, v)
	}
	if t.Kind == "show" {
		var v string
		if err = tx.QueryRowContext(ctx, `SELECT incarnation||':'||source_revision||':'||hierarchy_revision||':'||selection_revision FROM tvdb_publication_heads WHERE show_id=?`, entity).Scan(&v); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return "", err
		}
		parts = append(parts, v)
	}
	parts = append(parts, stamp)
	return publicationDigest(parts), nil
}
func insertArtworkCandidate(ctx context.Context, tx *sql.Tx, t RepairTarget, role, subject, provider, imageID, origin, locale, attribution, fence, now string, rank float64) (string, error) {
	return insertArtworkCandidateVotes(ctx, tx, t, role, subject, provider, imageID, origin, locale, attribution, fence, now, rank, -1)
}

// insertArtworkCandidateVotes records a candidate with the provider's vote
// count; votes < 0 means the provider publishes none (stored as NULL).
func insertArtworkCandidateVotes(ctx context.Context, tx *sql.Tx, t RepairTarget, role, subject, provider, imageID, origin, locale, attribution, fence, now string, rank float64, votes int) (string, error) {
	if !validArtworkRole(role) || len(subject) > 160 || len(imageID) > 256 || len(origin) > 4096 {
		return "", ErrRepairInput
	}
	var count sql.NullInt64
	if votes >= 0 {
		count = sql.NullInt64{Int64: int64(votes), Valid: true}
	}
	entity, err := artworkEntity(ctx, tx, t.ID)
	if err != nil {
		return "", err
	}
	id := publicationDigest([]string{t.Kind, t.ID, role, subject, provider, imageID})
	_, err = tx.ExecContext(ctx, `INSERT INTO artwork_candidates(id,kind,entity_id,role,subject,provider,image_id,origin,locale,rank,attribution,source_fence,observed_at,votes) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET origin=excluded.origin,locale=excluded.locale,rank=excluded.rank,votes=excluded.votes,attribution=excluded.attribution,source_fence=excluded.source_fence,observed_at=excluded.observed_at`, id, t.Kind, entity, role, subject, provider, imageID, origin, locale, rank, attribution, fence, now, count)
	return id, err
}
func queueArtworkCandidate(ctx context.Context, tx *sql.Tx, t RepairTarget, candidate, actor, now string, locked, force bool) error {
	entity, err := artworkEntity(ctx, tx, t.ID)
	if err != nil {
		return err
	}
	var role, subject, origin, provider, fence string
	if err := tx.QueryRowContext(ctx, `SELECT role,subject,origin,provider,source_fence FROM artwork_candidates WHERE id=? AND kind=? AND entity_id=?`, candidate, t.Kind, entity).Scan(&role, &subject, &origin, &provider, &fence); err != nil {
		return err
	}
	current, err := artworkFence(ctx, tx, t)
	if err != nil {
		return err
	}
	if current != fence {
		return ErrRepairConflict
	}
	var rev int64
	var selectedLocked bool
	err = tx.QueryRowContext(ctx, `SELECT revision,locked FROM artwork_selections WHERE kind=? AND entity_id=? AND role=? AND subject=?`, t.Kind, entity, role, subject).Scan(&rev, &selectedLocked)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if selectedLocked && !force {
		return nil
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO artwork_jobs(id,kind,entity_id,role,subject,candidate_id,origin,provider,source_fence,selection_revision,actor,locked,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(kind,entity_id,role,subject,preview) DO UPDATE SET id=excluded.id,candidate_id=excluded.candidate_id,origin=excluded.origin,provider=excluded.provider,source_fence=excluded.source_fence,selection_revision=excluded.selection_revision,status='pending',attempts=0,next_attempt='',lease='',lease_until='',error='',size_refresh=0,actor=excluded.actor,locked=excluded.locked,created_at=excluded.created_at`, identity.Token(), t.Kind, entity, role, subject, candidate, origin, provider, fence, rev, actor, locked, now)
	return err
}
func (s *Service) repairArtworkTx(ctx context.Context, tx *sql.Tx, t RepairTarget, m RepairCommand, actor, now string) error {
	entity, err := artworkEntity(ctx, tx, t.ID)
	if err != nil {
		return err
	}
	switch m.Action {
	case "select_artwork":
		if !m.Confirm || m.CandidateID == "" {
			return ErrRepairInput
		}
		return queueArtworkCandidate(ctx, tx, t, m.CandidateID, actor, now, true, true)
	case "artwork_lock":
		if !validArtworkRole(m.Role) || m.Locked == nil {
			return ErrRepairInput
		}
		_, err := tx.ExecContext(ctx, `UPDATE artwork_selections SET locked=?,revision=revision+1 WHERE kind=? AND entity_id=? AND role=? AND subject=?`, *m.Locked, t.Kind, entity, m.Role, m.Subject)
		return err
	case "repair_assets":
		choices, err := readArtworkChoices(ctx, tx, t)
		if err != nil {
			return err
		}
		fence, err := artworkFence(ctx, tx, t)
		if err != nil {
			return err
		}
		for _, a := range choices {
			// Repair is the explicit exception permitting a retained prior identity's
			// selected bytes to be reacquired without rematching or reselecting identity.
			// A generated frame keeps the source capture it was made from; only
			// the episode runtime can authorize a new bounded decode for it.
			updated, err := tx.ExecContext(ctx, `UPDATE artwork_candidates SET source_fence=? WHERE id=? AND provider<>'generated'`, fence, a.CandidateID)
			if err != nil {
				return err
			}
			n, err := updated.RowsAffected()
			if err != nil {
				return err
			}
			if n == 0 {
				continue
			}
			if err = queueArtworkCandidate(ctx, tx, t, a.CandidateID, actor, now, a.Locked, true); err != nil {
				return err
			}
		}
		if len(choices) == 0 {
			if _, err = tx.ExecContext(ctx, `UPDATE artwork_discovery SET status='pending',attempts=0,next_attempt='',lease='',lease_until='' WHERE kind=? AND entity_id=?`, t.Kind, entity); err != nil {
				return err
			}
			_, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO artwork_dirty VALUES(?,?)`, t.Kind, entity)
		}
		return err
	}
	return ErrRepairInput
}
func restoreArtworkChoices(ctx context.Context, tx *sql.Tx, t RepairTarget, choices []ArtworkChoice, actor, now string) error {
	entity, err := artworkEntity(ctx, tx, t.ID)
	if err != nil {
		return err
	}
	prior, err := readArtworkChoices(ctx, tx, t)
	if err != nil {
		return err
	}
	kept := map[string]bool{}
	for _, v := range choices {
		kept[v.Role+":"+v.Subject] = true
	}
	for _, v := range prior {
		if !kept[v.Role+":"+v.Subject] {
			if _, err = tx.ExecContext(ctx, `DELETE FROM artwork_selections WHERE kind=? AND entity_id=? AND role=? AND subject=?`, t.Kind, entity, v.Role, v.Subject); err != nil {
				return err
			}
		}
	}
	for _, a := range choices {
		var published bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM artwork_sources WHERE digest=?)`, a.Digest).Scan(&published); err != nil {
			return err
		}
		if !published {
			return fmt.Errorf("retained artwork needs repair before restore")
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO artwork_selections VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT(kind,entity_id,role,subject) DO UPDATE SET candidate_id=excluded.candidate_id,digest=excluded.digest,locked=excluded.locked,revision=artwork_selections.revision+1,actor=excluded.actor,observed_at=excluded.observed_at`, t.Kind, entity, a.Role, a.Subject, a.CandidateID, a.Digest, a.Locked, 1, actor, now)
		if err != nil {
			return err
		}
	}
	// Fence queued choices as well as published ones: restoring cannot be undone
	// later by an already running acquisition from the superseded owner command.
	_, err = tx.ExecContext(ctx, `UPDATE artwork_jobs SET status='stale',lease='',lease_until='',error='owner_restore' WHERE kind=? AND entity_id=? AND status IN('pending','running','retry')`, t.Kind, entity)
	return err
}
func artworkProvider(origin string) string {
	if strings.HasPrefix(origin, "local:") {
		return "local"
	}
	u, _ := url.Parse(origin)
	if u != nil {
		if u.Hostname() == "image.tmdb.org" {
			return "tmdb"
		}
		if u.Hostname() == "artworks.thetvdb.com" {
			return "tvdb"
		}
		if u.Hostname() == "upload.wikimedia.org" {
			return "commons"
		}
	}
	return ""
}
