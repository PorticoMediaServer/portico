package mediaanalysis

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/segmentdetect"
	"github.com/PorticoMediaServer/portico/server/internal/segmentmarkers"
)

// The owner's view of intro and credits detection: what the server is doing,
// and, per show or film, what it found, with everything the existing marker
// edit (Mutate) needs to approve, adjust, dismiss or add a marker.

// SegmentCounts counts files by detection state.
type SegmentCounts struct {
	Pending     int64 `json:"pending"`
	Complete    int64 `json:"complete"`
	Failed      int64 `json:"failed"`
	Unsupported int64 `json:"unsupported"`
}

// SegmentLibraryStatus is one video library's detection choice and progress.
type SegmentLibraryStatus struct {
	LibraryID string        `json:"libraryId"`
	Name      string        `json:"name"`
	ItemKind  string        `json:"itemKind"` // "episode" or "movie"
	Choice    string        `json:"choice"`   // SegmentLibraryChoices
	Mode      string        `json:"mode"`     // effective
	Files     SegmentCounts `json:"files"`
}

// SegmentMarkerCounts counts the detections viewers can see, by trust.
type SegmentMarkerCounts struct {
	Automatic int64 `json:"automatic"` // skipped without asking when a viewer chooses to
	Suggested int64 `json:"suggested"` // offered as a button; worth an owner's look
	Approved  int64 `json:"approved"`
	Dismissed int64 `json:"dismissed"`
}

// SegmentFailure is a file detection gave up on.
type SegmentFailure struct {
	ItemID    string `json:"itemId"`
	Title     string `json:"title"`
	State     string `json:"state"`
	ErrorCode string `json:"errorCode"`
	UpdatedMS int64  `json:"updatedMS"`
}

// SegmentStatus is the server-level picture. It is counted from indexes, never
// by reading every marker: per-title marker counts are in the title list.
type SegmentStatus struct {
	Mode            string                 `json:"mode"`
	Modes           []string               `json:"modes"`
	Choices         []string               `json:"choices"`
	Libraries       []SegmentLibraryStatus `json:"libraries"`
	Files           SegmentCounts          `json:"files"`
	ShowsWaiting    int64                  `json:"showsWaiting"`
	CatchUpComplete bool                   `json:"catchUpComplete"`
	LastFullPassMS  int64                  `json:"lastFullPassMS"`
	Failures        []SegmentFailure       `json:"failures"`
}

func countFiles(ctx context.Context, tx *sql.Tx, where string, args ...any) (SegmentCounts, error) {
	var c SegmentCounts
	rows, err := tx.QueryContext(ctx, `SELECT state,COUNT(*) FROM segment_files WHERE `+where+` GROUP BY state`, args...)
	if err != nil {
		return c, err
	}
	defer rows.Close()
	for rows.Next() {
		var state string
		var n int64
		if err = rows.Scan(&state, &n); err != nil {
			return c, err
		}
		switch state {
		case "pending":
			c.Pending = n
		case "complete":
			c.Complete = n
		case "failed":
			c.Failed = n
		case "unsupported":
			c.Unsupported = n
		}
	}
	return c, rows.Err()
}

func segmentTrustWithPolicy(policy segmentmarkers.ConfidencePolicy, approved, edited, deleted bool, confidence float64, provenance string) string {
	switch {
	case deleted && edited:
		return "dismissed"
	case deleted:
		return ""
	case approved:
		return "approved"
	case policy.AutomaticSafe(false, confidence, provenance):
		return "automatic"
	case policy.ViewerVisible(false, confidence, provenance):
		return "suggested"
	}
	return ""
}

// SegmentStatus reports the server's detection work. The caller has
// established that the reader is the owner.
func (s *Service) SegmentStatus(ctx context.Context, authorize func(*sql.Tx) error) (SegmentStatus, error) {
	out := SegmentStatus{Modes: []string{SegmentModeOff, SegmentModeStandard, SegmentModeThorough}, Choices: SegmentLibraryChoices, Libraries: []SegmentLibraryStatus{}, Failures: []SegmentFailure{}}
	gated, err := dbwork.BeginSnapshot(ctx, s.db)
	if err != nil {
		return out, err
	}
	defer gated.Rollback()
	tx := gated.Tx()
	if authorize != nil {
		if err = authorize(tx); err != nil {
			return out, err
		}
	}
	libraries, server, err := segmentLibrariesTx(ctx, tx)
	if err != nil {
		return out, err
	}
	out.Mode = server
	for _, l := range libraries {
		status := SegmentLibraryStatus{LibraryID: l.ID, ItemKind: l.Kind, Choice: l.Choice, Mode: l.Mode}
		if err = tx.QueryRowContext(ctx, `SELECT name FROM libraries WHERE id=?`, l.ID).Scan(&status.Name); err != nil {
			return out, err
		}
		if status.Files, err = countFiles(ctx, tx, `library_id=?`, l.ID); err != nil {
			return out, err
		}
		out.Libraries = append(out.Libraries, status)
	}
	if out.Files, err = countFiles(ctx, tx, `1`); err != nil {
		return out, err
	}
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM segment_shows WHERE dirty=1`).Scan(&out.ShowsWaiting); err != nil {
		return out, err
	}
	c, err := readSegmentCatchup(ctx, tx)
	if err != nil {
		return out, err
	}
	out.CatchUpComplete, out.LastFullPassMS = c.Settled, c.PassMS
	rows, err := tx.QueryContext(ctx, `SELECT COALESCE((SELECT pid(i.public_id) FROM catalog_assets a JOIN catalog_asset_links ia ON ia.asset_id=a.id JOIN catalog_entities i ON i.id=ia.entity_id WHERE a.token=o.asset_id ORDER BY ia.part_index,ia.entity_id LIMIT 1),''),COALESCE((SELECT i.title FROM catalog_assets a JOIN catalog_asset_links ia ON ia.asset_id=a.id JOIN catalog_entities i ON i.id=ia.entity_id WHERE a.token=o.asset_id ORDER BY ia.part_index,ia.entity_id LIMIT 1),''),f.state,f.error_code,f.updated_ms FROM segment_files f JOIN inventory_objects o ON o.id=f.object_id WHERE f.state IN('failed','unsupported') ORDER BY f.updated_ms DESC LIMIT 10`)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var f SegmentFailure
		if err = rows.Scan(&f.ItemID, &f.Title, &f.State, &f.ErrorCode, &f.UpdatedMS); err != nil {
			rows.Close()
			return out, err
		}
		out.Failures = append(out.Failures, f)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return out, err
	}
	return out, gated.Commit()
}

// SegmentTitleSummary is one show or film in a library's detection list.
type SegmentTitleSummary struct {
	ID      string              `json:"id"`
	Title   string              `json:"title"`
	Kind    string              `json:"kind"` // "show" or "movie"
	Files   SegmentCounts       `json:"files"`
	Markers SegmentMarkerCounts `json:"markers"`
}

// SegmentTitlePage is one page of a library's shows or films.
type SegmentTitlePage struct {
	LibraryID  string                `json:"libraryId"`
	Titles     []SegmentTitleSummary `json:"titles"`
	NextCursor string                `json:"nextCursor"`
}

func encodeTitleCursor(sortKey string, id int64) string {
	return base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf("%d\x00%s", id, sortKey)))
}

func decodeTitleCursor(v string) (string, int64, error) {
	if v == "" {
		return "", 0, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(v)
	if err != nil || len(raw) > 4096 {
		return "", 0, ErrInput
	}
	id, key, ok := strings.Cut(string(raw), "\x00")
	var n int64
	if _, err = fmt.Sscan(id, &n); !ok || err != nil || n < 0 {
		return "", 0, ErrInput
	}
	return key, n, nil
}

// titleObjectsSQL selects the measured files of a show (by show_id) or a
// film (through its files); parameter ?1 is the title's entity id.
const titleObjectsSQL = `SELECT f.object_id,f.source_revision,f.state FROM segment_files f WHERE f.show_id=?1 AND ?1>0
 UNION ALL SELECT f.object_id,f.source_revision,f.state FROM segment_files f JOIN inventory_objects o ON o.id=f.object_id JOIN catalog_assets a ON a.token=o.asset_id JOIN catalog_asset_links ia ON ia.asset_id=a.id WHERE ia.entity_id=?1 AND f.show_id=0`

func titleSummaryTx(ctx context.Context, tx *sql.Tx, entity int64, policy segmentmarkers.ConfidencePolicy) (SegmentCounts, SegmentMarkerCounts, error) {
	var files SegmentCounts
	var markers SegmentMarkerCounts
	rows, err := tx.QueryContext(ctx, `SELECT t.state,m.approved,m.edited,m.deleted,m.confidence,m.provenance FROM (`+titleObjectsSQL+`) t LEFT JOIN analysis_markers m ON m.object_id=t.object_id AND m.source_revision=t.source_revision ORDER BY t.object_id LIMIT 65536`, entity)
	if err != nil {
		return files, markers, err
	}
	defer rows.Close()
	for rows.Next() {
		var state string
		var approved, edited, deleted sql.NullBool
		var confidence sql.NullFloat64
		var provenance sql.NullString
		if err = rows.Scan(&state, &approved, &edited, &deleted, &confidence, &provenance); err != nil {
			return files, markers, err
		}
		if !provenance.Valid || strings.HasPrefix(provenance.String, "owner:") {
			continue
		}
		switch segmentTrustWithPolicy(policy, approved.Bool, edited.Bool, deleted.Bool, confidence.Float64, provenance.String) {
		case "automatic":
			markers.Automatic++
		case "suggested":
			markers.Suggested++
		case "approved":
			markers.Approved++
		case "dismissed":
			markers.Dismissed++
		}
	}
	if err = rows.Err(); err != nil {
		return files, markers, err
	}
	rows.Close()
	frows, err := tx.QueryContext(ctx, `SELECT state,COUNT(*) FROM (`+titleObjectsSQL+`) GROUP BY state`, entity)
	if err != nil {
		return files, markers, err
	}
	defer frows.Close()
	for frows.Next() {
		var state string
		var n int64
		if err = frows.Scan(&state, &n); err != nil {
			return files, markers, err
		}
		switch state {
		case "pending":
			files.Pending = n
		case "complete":
			files.Complete = n
		case "failed":
			files.Failed = n
		case "unsupported":
			files.Unsupported = n
		}
	}
	return files, markers, frows.Err()
}

// SegmentTitles pages a video library's shows (or films) by title.
func (s *Service) SegmentTitles(ctx context.Context, authorize func(*sql.Tx) error, library, cursor string, limit int) (SegmentTitlePage, error) {
	out := SegmentTitlePage{LibraryID: library, Titles: []SegmentTitleSummary{}}
	if limit < 1 || limit > 100 {
		limit = 50
	}
	key, after, err := decodeTitleCursor(cursor)
	if err != nil {
		return out, err
	}
	gated, err := dbwork.BeginSnapshot(ctx, s.db)
	if err != nil {
		return out, err
	}
	defer gated.Rollback()
	tx := gated.Tx()
	if authorize != nil {
		if err = authorize(tx); err != nil {
			return out, err
		}
	}
	policy, err := segmentmarkers.ReadConfidencePolicy(ctx, tx)
	if err != nil {
		return out, err
	}
	var exists bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM libraries WHERE id=? AND kind IN('tv','anime','movie'))`, library).Scan(&exists); err != nil {
		return out, err
	}
	if !exists {
		return out, sql.ErrNoRows
	}
	rows, err := tx.QueryContext(ctx, `SELECT i.id,pid(i.public_id),i.title,k.name,i.sort_key FROM catalog_entities i JOIN catalog_libraries l ON l.id=i.library_id JOIN catalog_kinds k ON k.id=i.kind
 WHERE l.library_id=? AND k.name IN('show','movie') AND i.retired=0 AND (i.sort_key>? OR i.sort_key=? AND i.id>?) ORDER BY i.sort_key,i.id LIMIT ?`, library, key, key, after, limit+1)
	if err != nil {
		return out, err
	}
	type row struct {
		id  int64
		sum SegmentTitleSummary
		key string
	}
	var page []row
	for rows.Next() {
		var r row
		if err = rows.Scan(&r.id, &r.sum.ID, &r.sum.Title, &r.sum.Kind, &r.key); err != nil {
			rows.Close()
			return out, err
		}
		page = append(page, r)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return out, err
	}
	if len(page) > limit {
		page = page[:limit]
		last := page[len(page)-1]
		out.NextCursor = encodeTitleCursor(last.key, last.id)
	}
	for _, r := range page {
		if r.sum.Files, r.sum.Markers, err = titleSummaryTx(ctx, tx, r.id, policy); err != nil {
			return out, err
		}
		out.Titles = append(out.Titles, r.sum)
	}
	return out, gated.Commit()
}

// SegmentReviewMarker is one stored marker as the owner reviews it.
type SegmentReviewMarker struct {
	ID            string  `json:"id"`
	Kind          string  `json:"kind"`
	StartUS       string  `json:"startUS"`
	EndUS         string  `json:"endUS"`
	Confidence    float64 `json:"confidence"`
	Provenance    string  `json:"provenance"`
	Basis         string  `json:"basis"` // season, chapter, picture, owner, earlier
	Approved      bool    `json:"approved"`
	Edited        bool    `json:"edited"`
	AutomaticSafe bool    `json:"automaticSafe"`
	ViewerVisible bool    `json:"viewerVisible"`
}

// SegmentReviewEntry is one episode (or the film) with its markers and the
// target the marker edit takes.
type SegmentReviewEntry struct {
	ItemID          string                `json:"itemId"`
	Title           string                `json:"title"`
	Number          int                   `json:"number"`
	DurationUS      string                `json:"durationUS"`
	State           string                `json:"state"` // a segment_files state, or "not_queued"
	ErrorCode       string                `json:"errorCode"`
	SourceID        string                `json:"sourceId"`
	SourceRevision  string                `json:"sourceRevision"`
	MappingRevision string                `json:"mappingRevision"`
	MarkerRevision  int64                 `json:"markerRevision"`
	Markers         []SegmentReviewMarker `json:"markers"`
}

// SegmentReviewSeason groups a show's episodes.
type SegmentReviewSeason struct {
	Number  int                  `json:"number"` // -1: numbered across the show
	Title   string               `json:"title"`
	Entries []SegmentReviewEntry `json:"entries"`
}

// SegmentTitleReview is everything the owner reviews for one show or film.
type SegmentTitleReview struct {
	ID        string                `json:"id"`
	Title     string                `json:"title"`
	Kind      string                `json:"kind"`
	LibraryID string                `json:"libraryId"`
	Mode      string                `json:"mode"`
	Seasons   []SegmentReviewSeason `json:"seasons"`
}

// Actor is the history name of an owner's batch change, as Mutate records it.
func Actor(authority, account, profile string) string { return token(authority, account, profile) }

func markerBasis(provenance string) string {
	switch {
	case strings.HasPrefix(provenance, "owner:"):
		return "owner"
	case segmentmarkers.MeasuredProvenance(provenance):
		return "season"
	case provenance == segmentdetect.ChapterProvenance:
		return "chapter"
	case provenance == segmentdetect.VisualCreditsProvenance:
		return "picture"
	}
	return "earlier"
}

type reviewFile struct {
	entity                int64
	itemID, title, season string
	seasonNumber, number  int
	token                 string
	state, code           string
}

func titleTx(ctx context.Context, tx *sql.Tx, id string) (entity int64, title, kind, library string, err error) {
	err = tx.QueryRowContext(ctx, `SELECT i.id,i.title,k.name,l.library_id FROM catalog_entities i JOIN catalog_kinds k ON k.id=i.kind JOIN catalog_libraries l ON l.id=i.library_id WHERE i.public_id=pid_blob(?) AND k.name IN('show','movie') AND i.retired=0`, id).Scan(&entity, &title, &kind, &library)
	return
}

// reviewFilesTx lists a title's playable entries: a show's episodes in season
// and number order (the first available version of each), or the film.
func reviewFilesTx(ctx context.Context, tx *sql.Tx, entity int64, kind string) ([]reviewFile, error) {
	query := `SELECT e.id,pid(e.public_id),e.title,COALESCE(se.title,''),COALESCE(se.number,-1),ep.number,a.token,COALESCE(f.state,'not_queued'),COALESCE(f.error_code,'')
 FROM catalog_episodes ep JOIN catalog_entities e ON e.id=ep.entity_id LEFT JOIN catalog_seasons se ON se.entity_id=ep.season_id
 JOIN catalog_asset_links ia ON ia.entity_id=e.id AND ia.part_index=0 JOIN catalog_assets a ON a.id=ia.asset_id AND a.available=1
 LEFT JOIN inventory_objects o ON o.asset_id=a.token AND o.retired=0 AND o.state='available' LEFT JOIN segment_files f ON f.object_id=o.id
 WHERE ep.show_id=? AND e.retired=0 ORDER BY 5,6,e.id,a.token LIMIT 4097`
	if kind == "movie" {
		query = `SELECT e.id,pid(e.public_id),e.title,'',0,0,a.token,COALESCE(f.state,'not_queued'),COALESCE(f.error_code,'')
 FROM catalog_entities e JOIN catalog_asset_links ia ON ia.entity_id=e.id AND ia.part_index=0 JOIN catalog_assets a ON a.id=ia.asset_id AND a.available=1
 LEFT JOIN inventory_objects o ON o.asset_id=a.token AND o.retired=0 AND o.state='available' LEFT JOIN segment_files f ON f.object_id=o.id
 WHERE e.id=? ORDER BY a.token LIMIT 64`
	}
	rows, err := tx.QueryContext(ctx, query, entity)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []reviewFile
	seen := map[int64]bool{}
	for rows.Next() {
		var f reviewFile
		if err = rows.Scan(&f.entity, &f.itemID, &f.title, &f.season, &f.seasonNumber, &f.number, &f.token, &f.state, &f.code); err != nil {
			return nil, err
		}
		if seen[f.entity] {
			continue
		}
		seen[f.entity] = true
		out = append(out, f)
	}
	if len(out) > 4096 {
		return nil, ErrBudget
	}
	return out, rows.Err()
}

// SegmentTitle is the review document for one show or film.
func (s *Service) SegmentTitle(ctx context.Context, authorize func(*sql.Tx) error, id string) (SegmentTitleReview, error) {
	out := SegmentTitleReview{ID: id, Seasons: []SegmentReviewSeason{}}
	gated, err := dbwork.BeginSnapshot(ctx, s.db)
	if err != nil {
		return out, err
	}
	defer gated.Rollback()
	tx := gated.Tx()
	if authorize != nil {
		if err = authorize(tx); err != nil {
			return out, err
		}
	}
	entity, title, kind, library, err := titleTx(ctx, tx, id)
	if err != nil {
		return out, err
	}
	out.Title, out.Kind, out.LibraryID = title, kind, library
	if out.Mode, err = SegmentLibraryModeTx(ctx, tx, library); err != nil {
		return out, err
	}
	policy, err := segmentmarkers.ReadConfidencePolicy(ctx, tx)
	if err != nil {
		return out, err
	}
	files, err := reviewFilesTx(ctx, tx, entity, kind)
	if err != nil {
		return out, err
	}
	for _, f := range files {
		entry := SegmentReviewEntry{ItemID: f.itemID, Title: f.title, Number: f.number, State: f.state, ErrorCode: f.code, Markers: []SegmentReviewMarker{}}
		v, err := resolveSource(ctx, tx, Access{LibraryID: library, ItemID: f.itemID}, Target{SourceID: f.token})
		if err == nil && !v.NeedsProbe {
			entry.SourceID, entry.SourceRevision, entry.MappingRevision, entry.DurationUS = v.ID, v.Revision, v.MappingRevision, v.DurationUS
			if err = tx.QueryRowContext(ctx, `SELECT revision FROM analysis_marker_sets WHERE object_id=? AND source_revision=?`, v.ObjectID, v.Revision).Scan(&entry.MarkerRevision); err != nil && !errors.Is(err, sql.ErrNoRows) {
				return out, err
			}
			if entry.MarkerRevision == 0 {
				entry.MarkerRevision = 1 // the set's first revision, created by the first edit
			}
			rows, err := tx.QueryContext(ctx, `SELECT id,kind,start_us,end_us,confidence,provenance,approved,edited FROM analysis_markers WHERE object_id=? AND source_revision=? AND source_binding=? AND deleted=0 AND end_us>? AND start_us<? ORDER BY start_us,id LIMIT 513`, v.ObjectID, v.Revision, v.SourceBinding, v.Start, v.End)
			if err != nil {
				return out, err
			}
			for rows.Next() {
				var m SegmentReviewMarker
				var start, end int64
				if err = rows.Scan(&m.ID, &m.Kind, &start, &end, &m.Confidence, &m.Provenance, &m.Approved, &m.Edited); err != nil {
					rows.Close()
					return out, err
				}
				m.StartUS, m.EndUS = fmt.Sprint(max(start, v.Start)-v.Start), fmt.Sprint(min(end, v.End)-v.Start)
				m.Basis = markerBasis(m.Provenance)
				m.AutomaticSafe = policy.AutomaticSafe(m.Approved, m.Confidence, m.Provenance)
				m.ViewerVisible = segmentmarkers.ViewerKind(m.Kind) && policy.ViewerVisible(m.Approved, m.Confidence, m.Provenance) && (out.Mode != SegmentModeOff || m.Approved)
				entry.Markers = append(entry.Markers, m)
			}
			rows.Close()
			if err = rows.Err(); err != nil {
				return out, err
			}
		} else if err != nil && !errors.Is(err, sql.ErrNoRows) && !errors.Is(err, ErrClock) && !errors.Is(err, ErrConflict) {
			return out, err
		}
		if n := len(out.Seasons); n == 0 || out.Seasons[n-1].Number != f.seasonNumber {
			out.Seasons = append(out.Seasons, SegmentReviewSeason{Number: f.seasonNumber, Title: f.season, Entries: []SegmentReviewEntry{}})
		}
		last := &out.Seasons[len(out.Seasons)-1]
		last.Entries = append(last.Entries, entry)
	}
	return out, gated.Commit()
}

// SegmentApprove approves every detected marker of the given kinds, in one
// season (season >= -1) or the whole title (season < -1), that the owner has
// not already approved, adjusted or dismissed. Each file's marker revision
// moves once and its history records the owner's batch.
func (s *Service) SegmentApprove(ctx context.Context, authorize func(*sql.Tx) error, actor, id string, season int, kinds []string) (int, error) {
	allowed := map[string]bool{}
	for _, k := range kinds {
		if !segmentmarkers.ViewerKind(k) {
			return 0, ErrInput
		}
		allowed[k] = true
	}
	if len(allowed) == 0 {
		return 0, ErrInput
	}
	gated, err := dbwork.Begin(ctx, s.db, dbwork.ClassInteractive)
	if err != nil {
		return 0, err
	}
	defer gated.Rollback()
	tx := gated.Tx()
	if authorize != nil {
		if err = authorize(tx); err != nil {
			return 0, err
		}
	}
	entity, _, kind, library, err := titleTx(ctx, tx, id)
	if err != nil {
		return 0, err
	}
	files, err := reviewFilesTx(ctx, tx, entity, kind)
	if err != nil {
		return 0, err
	}
	approved := 0
	for _, f := range files {
		if season >= -1 && f.seasonNumber != season {
			continue
		}
		v, err := resolveSource(ctx, tx, Access{LibraryID: library, ItemID: f.itemID}, Target{SourceID: f.token})
		if errors.Is(err, sql.ErrNoRows) || errors.Is(err, ErrClock) || errors.Is(err, ErrConflict) || err == nil && v.NeedsProbe {
			continue
		}
		if err != nil {
			return 0, err
		}
		var ids []string
		rows, err := tx.QueryContext(ctx, `SELECT id,kind,provenance FROM analysis_markers WHERE object_id=? AND source_revision=? AND source_binding=? AND deleted=0 AND edited=0 AND approved=0 AND start_us>=? AND end_us<=?`, v.ObjectID, v.Revision, v.SourceBinding, v.Start, v.End)
		if err != nil {
			return 0, err
		}
		for rows.Next() {
			var mid, mkind, provenance string
			if err = rows.Scan(&mid, &mkind, &provenance); err != nil {
				rows.Close()
				return 0, err
			}
			if allowed[mkind] && !strings.HasPrefix(provenance, "owner:") {
				ids = append(ids, mid)
			}
		}
		rows.Close()
		if err = rows.Err(); err != nil {
			return 0, err
		}
		if len(ids) == 0 {
			continue
		}
		for _, mid := range ids {
			if _, err = tx.ExecContext(ctx, `UPDATE analysis_markers SET approved=1,edited=1 WHERE id=?`, mid); err != nil {
				return 0, err
			}
		}
		if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO analysis_marker_sets(object_id,source_revision) VALUES(?,?)`, v.ObjectID, v.Revision); err != nil {
			return 0, err
		}
		var next int64
		if err = tx.QueryRowContext(ctx, `UPDATE analysis_marker_sets SET revision=revision+1 WHERE object_id=? AND source_revision=? RETURNING revision`, v.ObjectID, v.Revision).Scan(&next); err != nil {
			return 0, err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO analysis_marker_history(object_id,source_revision,revision,actor,mutation_json,created_ms) VALUES(?,?,?,?,?,?)`, v.ObjectID, v.Revision, next, actor, jsonString(map[string]any{"action": "approve_detected", "markerIds": ids}), nowMS()); err != nil {
			return 0, err
		}
		approved += len(ids)
	}
	return approved, gated.Commit()
}

// SegmentRedetect measures a title's files again and compares them afresh.
// Markers the owner approved, adjusted, dismissed or made are kept. A show can
// hold thousands of files, so the requeue runs in bounded batches, each in its
// own short re-authorized transaction (SEG2). A requeue is idempotent: one that
// is interrupted is finished by asking again.
func (s *Service) SegmentRedetect(ctx context.Context, authorize func(*sql.Tx) error, id string) (int, error) {
	total, after := 0, ""
	for {
		n, last, done, err := s.redetectBatch(ctx, authorize, id, after)
		total += n
		if err != nil || done {
			return total, err
		}
		after = last
	}
}

// redetectBatchSize bounds the files one redetect transaction requeues: each
// file changes about two rows, so 64 stays inside an interactive write plan.
const redetectBatchSize = 64

func (s *Service) redetectBatch(ctx context.Context, authorize func(*sql.Tx) error, id, after string) (int, string, bool, error) {
	gated, err := dbwork.Begin(ctx, s.db, dbwork.ClassInteractive)
	if err != nil {
		return 0, "", false, err
	}
	defer gated.Rollback()
	tx := gated.Tx()
	if authorize != nil {
		if err = authorize(tx); err != nil {
			return 0, "", false, err
		}
	}
	entity, _, _, _, err := titleTx(ctx, tx, id)
	if err != nil {
		return 0, "", false, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT object_id FROM (`+titleObjectsSQL+`) WHERE object_id>?2 ORDER BY object_id LIMIT ?3`, entity, after, redetectBatchSize)
	if err != nil {
		return 0, "", false, err
	}
	var batch []string
	for rows.Next() {
		var object string
		if err = rows.Scan(&object); err != nil {
			rows.Close()
			return 0, "", false, err
		}
		batch = append(batch, object)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, "", false, err
	}
	var n int64
	if len(batch) > 0 {
		result, err := tx.ExecContext(ctx, `UPDATE segment_files SET state='pending',attempt=0,next_ms=0,error_code='',match_digest='',updated_ms=? WHERE object_id IN(SELECT value FROM json_each(?))`, nowMS(), jsonString(batch))
		if err != nil {
			return 0, "", false, err
		}
		n, _ = result.RowsAffected()
	}
	done := len(batch) < redetectBatchSize
	if done {
		if err = markShowDirtyIfShow(ctx, tx, entity); err != nil {
			return 0, "", false, err
		}
	}
	last := ""
	if len(batch) > 0 {
		last = batch[len(batch)-1]
	}
	return int(n), last, done, gated.Commit()
}

func markShowDirtyIfShow(ctx context.Context, tx *sql.Tx, entity int64) error {
	var show bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM segment_files WHERE show_id=?)`, entity).Scan(&show); err != nil || !show {
		return err
	}
	return markShowDirty(ctx, tx, entity)
}
