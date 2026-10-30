package catalog

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/reportwindow"
)

// The server's play history (migration 0007): the owner's record of what was
// played here, by whom, on which device and when. A viewer's own History
// (personal_history) is a different thing — theirs to pause and clear — and this
// record does neither: an owner who wants less keeps less (playHistoryDays).

// recordPlay writes or updates the history row of one playback, in the playback
// transaction. A row begins once the play has really started (or finished), so a
// title opened and closed again leaves nothing behind. counted reports that this
// report is the one that completed the play. Personal counts are independent.
func recordPlay(tx *sql.Tx, v identity.Viewer, playbackID string, entityID, kind int64, now time.Time, positionMs int64, duration float64, started, completed, share bool, states ...string) (counted bool, err error) {
	return recordPlayEvidence(tx, v, playbackID, entityID, kind, now, positionMs, duration, started, completed, share, nil, states...)
}

// PlayedTime is watched wall time an observation apply has already proved
// (playbackv1 observations): the credited delta and the server receipt span it
// is spread over. It replaces the inference between two reports, so a seek,
// a slow rate or a long gap neither zeroes nor halves time actually played.
type PlayedTime struct {
	FromMs, ToMs, WatchedMs int64
}

// recordPlayEvidence is recordPlay with optional proved played time. Nil keeps
// the inferred rule: a short positive advance between two reports.
func recordPlayEvidence(tx *sql.Tx, v identity.Viewer, playbackID string, entityID, kind int64, now time.Time, positionMs int64, duration float64, started, completed, share bool, played *PlayedTime, states ...string) (counted bool, err error) {
	nowMs := now.UnixMilli()
	var id int64
	var was bool
	var previous, previousPosition int64
	var previousState string
	state := "playing"
	if len(states) > 0 {
		state = states[0]
	}
	err = tx.QueryRow(`SELECT id,completed,updated_ms,last_position_ms,last_state FROM play_history WHERE playback_id=?`, playbackID).Scan(&id, &was, &previous, &previousPosition, &previousState)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if !started && !completed {
			return false, nil
		}
		if err = insertPlay(tx, v, playbackID, entityID, kind, nowMs, positionMs, duration, completed); err != nil {
			return false, err
		}
		if played != nil && played.WatchedMs > 0 {
			if err = tx.QueryRow(`SELECT id FROM play_history WHERE playback_id=?`, playbackID).Scan(&id); err != nil {
				return false, err
			}
			if err = addPlayedTime(tx, id, *played); err != nil {
				return false, err
			}
		}
	case err != nil:
		return false, err
	default:
		watched := int64(0)
		if played != nil {
			if watched = max(played.WatchedMs, 0); watched > 0 {
				if err = recordPlayedInterval(tx, id, min(played.FromMs, played.ToMs-1), played.ToMs, watched); err != nil {
					return false, err
				}
			}
		} else {
			elapsed := nowMs - previous
			advance := positionMs - previousPosition
			if previousState == "playing" && elapsed > 0 && elapsed <= 60000 && advance > 0 && advance <= elapsed*4+2000 {
				watched = min(elapsed, advance)
			}
			if watched > 0 {
				if err = recordPlayedInterval(tx, id, previous, nowMs, watched); err != nil {
					return false, err
				}
			}
		}
		if _, err = tx.Exec(`UPDATE play_history SET updated_ms=max(updated_ms,?),position_ms=max(position_ms,?),completed=max(completed,?),watched_ms=watched_ms+?,last_position_ms=?,last_state=? WHERE id=?`, nowMs, positionMs, completed, watched, positionMs, state, id); err != nil {
			return false, err
		}
	}
	if _, err = tx.Exec(`UPDATE play_history SET last_position_ms=?,last_state=?,location=COALESCE(NULLIF(location,''),(SELECT location FROM playback_v1_sessions WHERE media_session_id=? LIMIT 1),'') WHERE playback_id=?`, positionMs, state, playbackID, playbackID); err != nil {
		return false, err
	}
	if !completed || was {
		return false, nil
	}
	if !share {
		return true, nil
	}
	_, err = tx.Exec(`INSERT INTO play_counts(item_id,plays,last_played_ms) VALUES(?,1,?) ON CONFLICT(item_id) DO UPDATE SET plays=plays+1,last_played_ms=max(play_counts.last_played_ms,excluded.last_played_ms)`, entityID, nowMs)
	return err == nil, err
}

// insertPlay copies in every name the row will be read by. It runs once per
// playback, so the lookups are a handful of primary-key reads.
func insertPlay(tx *sql.Tx, v identity.Viewer, playbackID string, entityID, kind int64, nowMs, positionMs int64, duration float64, completed bool) error {
	var title, library, parent string
	var season, episode sql.NullInt64
	parentSQL := `''`
	switch kind {
	case int64(compactcatalog.Episode):
		parentSQL = `COALESCE((SELECT show.title FROM catalog_episodes ep JOIN catalog_entities show ON show.id=ep.show_id WHERE ep.entity_id=e.id),'')`
	case int64(compactcatalog.Track):
		parentSQL = `COALESCE((SELECT artist.title FROM catalog_songs song JOIN catalog_albums album ON album.entity_id=song.album_id JOIN catalog_entities artist ON artist.id=album.artist_id WHERE song.entity_id=e.id),'')`
	case int64(compactcatalog.Part):
		parentSQL = `COALESCE((SELECT book.title FROM catalog_book_files file JOIN catalog_entities book ON book.id=file.book_id WHERE file.entity_id=e.id),'')`
	}
	if err := tx.QueryRow(`SELECT e.title,cl.library_id,`+parentSQL+`,
	 (SELECT se.number FROM catalog_episodes ep JOIN catalog_seasons se ON se.entity_id=ep.season_id WHERE ep.entity_id=e.id),
	 (SELECT ep.number FROM catalog_episodes ep WHERE ep.entity_id=e.id)
	 FROM catalog_entities e JOIN catalog_libraries cl ON cl.id=e.library_id WHERE e.id=?`, entityID).Scan(&title, &library, &parent, &season, &episode); err != nil {
		return err
	}
	// Who: the account's name and, for any profile but its first, the profile's.
	user, profile := v.AccountID, ""
	if v.Authority == "local" {
		var name sql.NullString
		var profileName sql.NullString
		var primary sql.NullBool
		if err := tx.QueryRow(`SELECT a.username,p.name,p.is_primary FROM accounts a LEFT JOIN direct_profiles p ON p.account_id=a.id AND p.id=? WHERE a.id=?`, v.ProfileID, v.AccountID).Scan(&name, &profileName, &primary); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if name.String != "" {
			user = name.String
		}
		if profileName.Valid && !primary.Bool {
			profile = profileName.String
		}
	}
	// Where: the device the playing session was signed in on.
	var device, deviceName, platform string
	if err := tx.QueryRow(`SELECT d.id,d.name,d.platform FROM playback_sessions ps JOIN authorization_family_tokens t ON t.token_hash=ps.session_hash
	 JOIN identity_device_families f ON f.family_id=t.family_id JOIN identity_devices d ON d.id=f.device_id WHERE ps.id=?`, playbackID).Scan(&device, &deviceName, &platform); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	// How it was delivered: whether the server converted the picture or the
	// sound for this play, read from the session's stored decision while the
	// session still exists (sessions end after thirty days; the history stays).
	var converted bool
	if err := tx.QueryRow(`SELECT COALESCE((SELECT CASE WHEN json_valid(v.presentation) AND (json_extract(v.presentation,'$.audioRender.mode')='converted'
	 OR json_extract(v.presentation,'$.decision.video.action')='transcode' OR json_extract(v.presentation,'$.decision.audio.action')='transcode') THEN 1 ELSE 0 END
	 FROM playback_v1_sessions v WHERE v.media_session_id=? LIMIT 1),0)`, playbackID).Scan(&converted); err != nil {
		return err
	}
	_, err := tx.Exec(`INSERT INTO play_history(playback_id,authority,account_id,profile_id,user_name,profile_name,item_id,library_id,kind,title,parent_title,season,episode,device_id,device_name,platform,started_ms,updated_ms,position_ms,duration_ms,completed,converted)
	 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		playbackID, v.Authority, v.AccountID, v.ProfileID, user, profile, entityID, library, kind, title, parent, season, episode, device, deviceName, platform, nowMs, nowMs, positionMs, positionMillis(duration), completed, converted)
	return err
}

// countPersonalPlay records one completion per occurrence independently of the
// owner's count and the clearable History list. Pausing and resuming history
// during an occurrence can therefore neither lose nor duplicate its count.
func countPersonalPlay(tx *sql.Tx, profile, playbackID string, entityID int64) (bool, error) {
	result, err := tx.Exec(`INSERT OR IGNORE INTO personal_completed_plays(profile_id,playback_id,item_id) VALUES(?,?,?)`, profile, playbackID, entityID)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	if err != nil || n == 0 {
		return false, err
	}
	_, err = tx.Exec(`INSERT INTO personal_play_counts(profile_id,item_id,plays) VALUES(?,?,1) ON CONFLICT(profile_id,item_id) DO UPDATE SET plays=plays+1`, profile, entityID)
	return err == nil, err
}

// PlayHistoryEntry is one play, as the owner reads it.
type PlayHistoryEntry struct {
	WatchedMs int64  `json:"watchedMs"`
	Location  string `json:"location"`
	ID        string `json:"id"`
	User      string `json:"user"`
	Profile   string `json:"profile,omitempty"`
	// AccountID lets a client filter to "this person" from a row.
	AccountID string `json:"accountId"`
	Kind      string `json:"kind"`
	Title     string `json:"title"`
	// ParentTitle is the show of an episode, the artist of a song, the book of an
	// audiobook file; Season and Episode are an episode's numbers.
	ParentTitle string `json:"parentTitle,omitempty"`
	Season      *int64 `json:"season,omitempty"`
	Episode     *int64 `json:"episode,omitempty"`
	// ItemID is the title's id while it is still in the catalogue.
	ItemID          string  `json:"itemId,omitempty"`
	LibraryID       string  `json:"libraryId"`
	Device          string  `json:"device,omitempty"`
	Platform        string  `json:"platform,omitempty"`
	StartedAt       string  `json:"startedAt"`
	PositionSeconds float64 `json:"positionSeconds"`
	DurationSeconds float64 `json:"durationSeconds"`
	Completed       bool    `json:"completed"`
}

type PlayHistoryPage struct {
	reportwindow.Interval
	Entries []PlayHistoryEntry `json:"entries"`
	// Total counts the plays the filters match; sent with the first page only.
	Total      *int64 `json:"total,omitempty"`
	NextCursor string `json:"nextCursor"`
}

// HistoryAudience is the live principal asking for the owner's play history.
// Watch history from a removed profile is retained for its owner and shown as
// "Name (removed)". Only the server owner's main profile, checked live in the
// same read, may see those rows; a nil audience sees live profiles only.
type HistoryAudience struct {
	AccountID string
	ProfileID string
	Epoch     int
}

// PlayHistoryQuery filters the history. Every field is optional.
type PlayHistoryQuery struct {
	// Audience decides whether retained plays of removed profiles are visible.
	// The HTTP edge sets it from the authenticated owner; no query or body does.
	Audience  *HistoryAudience
	TimeZone  string
	Until     string
	AccountID string
	LibraryID string
	Media     string
	Search    string
	Sort      string
	Direction string
	// Period is one of HistoryPeriods' names ("24h", "7d", "30d", "90d", "all").
	Period string
	Cursor string
	Limit  int
}

// PlayHistoryMax is the most rows one page lists.
const PlayHistoryMax = 200

// PlayHistory lists plays newest first. Each filter has an index that yields its
// rows already in order, so a page reads a page of rows however long the history
// is; the total is one count over the same index range, asked for once.
func (s *Service) PlayHistory(ctx context.Context, q PlayHistoryQuery) (PlayHistoryPage, error) {
	if dbwork.Snapshot(ctx) == nil {
		var out PlayHistoryPage
		e := dbwork.WithReadSnapshot(ctx, s.db, func(snapshot context.Context) error { var e error; out, e = s.PlayHistory(snapshot, q); return e })
		return out, e
	}

	r, err := s.historyInterval(ctx, q)
	out := PlayHistoryPage{Interval: r, Entries: []PlayHistoryEntry{}}
	if err != nil {
		return out, err
	}
	if q.Limit == 0 {
		q.Limit = 50
	}
	if q.Period == "" {
		q.Period = "all"
	}
	if q.Limit < 1 || q.Limit > PlayHistoryMax || len(q.AccountID) > 128 || len(q.LibraryID) > 128 {
		return out, ErrCursor
	}
	custom := q.Sort != "" && q.Sort != "started" || q.Direction == "asc"
	sortExpressions := map[string]string{"title": "lower(h.title)", "person": "lower(h.user_name)", "player": "lower(h.device_name)", "when": "printf('%020d',h.started_ms)", "started": "printf('%020d',h.started_ms)", "watched": "printf('%020d'," + historyWatched(r) + ")", "location": "h.location"}
	sortExpression := sortExpressions[q.Sort]
	if q.Sort == "" {
		sortExpression = sortExpressions["started"]
	}
	if sortExpression == "" || q.Direction != "" && q.Direction != "asc" && q.Direction != "desc" {
		return out, ErrCursor
	}
	order, compare := "DESC", "<"
	if q.Direction == "asc" {
		order, compare = "ASC", ">"
	}
	afterSort, beforeID := "", ""
	if custom && q.Cursor != "" {
		raw, e := base64.RawURLEncoding.DecodeString(q.Cursor)
		var values []string
		if e != nil || len(raw) > 2048 || json.Unmarshal(raw, &values) != nil || len(values) != 2 {
			return out, ErrCursor
		}
		afterSort = values[0]
		beforeID = values[1]
	}
	tx, done, err := historyRead(ctx, s.db)
	if err != nil {
		return out, err
	}
	defer done()
	seesRemoved, err := historySeesRemoved(ctx, tx, q.Audience)
	if err != nil {
		return out, err
	}
	filter, args, err := historyFilter(q, r, seesRemoved)
	if err != nil {
		return out, err
	}
	page, pageArgs := filter, append([]any{}, args...)
	if q.Cursor == "" {
		var total int64
		if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM play_history h`+filter, args...).Scan(&total); err != nil {
			return out, err
		}
		out.Total = &total
	} else if !custom {
		started, id, ok := strings.Cut(q.Cursor, ":")
		startedMs, startedErr := strconv.ParseInt(started, 10, 64)
		rowID, idErr := strconv.ParseInt(id, 10, 64)
		if !ok || startedErr != nil || idErr != nil {
			return out, ErrCursor
		}
		if page == "" {
			page = ` WHERE `
		} else {
			page += ` AND `
		}
		page += `(h.started_ms,h.id)<(?,?)`
		pageArgs = append(pageArgs, startedMs, rowID)
	}
	if custom && q.Cursor != "" {
		page += " AND (" + sortExpression + "," + "h.id)" + compare + "(?,?)"
		rowID, e := strconv.ParseInt(beforeID, 10, 64)
		if e != nil {
			return out, ErrCursor
		}
		pageArgs = append(pageArgs, afterSort, rowID)
	}
	ordering := "h.started_ms DESC,h.id DESC"
	if custom {
		ordering = sortExpression + " " + order + ",h.id " + order
	}
	rows, err := tx.QueryContext(ctx, `SELECT h.id,h.user_name,`+historyProfileLabel()+`,h.account_id,h.kind,h.title,h.parent_title,h.season,h.episode,
	 COALESCE((SELECT pid(e.public_id) FROM catalog_entities e WHERE e.id=h.item_id AND e.retired=0),''),
	 h.library_id,h.device_name,h.platform,h.started_ms,h.position_ms,h.duration_ms,h.completed,`+historyWatched(r)+`,h.location,`+sortExpression+`
	 FROM play_history h`+page+` ORDER BY `+ordering+` LIMIT ?`, append(pageArgs, q.Limit+1)...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	var lastStarted, lastID int64
	lastSort := ""
	for rows.Next() {
		var row PlayHistoryEntry
		var id, kind, started, position, duration int64
		var sortValue string
		var season, episode sql.NullInt64
		if err = rows.Scan(&id, &row.User, &row.Profile, &row.AccountID, &kind, &row.Title, &row.ParentTitle, &season, &episode, &row.ItemID, &row.LibraryID, &row.Device, &row.Platform, &started, &position, &duration, &row.Completed, &row.WatchedMs, &row.Location, &sortValue); err != nil {
			return out, err
		}
		if len(out.Entries) == q.Limit {
			out.NextCursor = strconv.FormatInt(lastStarted, 10) + ":" + strconv.FormatInt(lastID, 10)
			if custom {
				raw, _ := json.Marshal([]string{lastSort, strconv.FormatInt(lastID, 10)})
				out.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
			}
			break
		}
		lastStarted, lastID = started, id
		lastSort = sortValue
		row.ID = strconv.FormatInt(id, 10)
		row.Kind, _ = compactcatalog.Kind(kind).Name()
		if kind == 0 {
			row.Kind = "live"
		}
		if season.Valid {
			row.Season = &season.Int64
		}
		if episode.Valid {
			row.Episode = &episode.Int64
		}
		row.StartedAt = time.UnixMilli(started).UTC().Format("2006-01-02T15:04:05.000Z")
		row.PositionSeconds, row.DurationSeconds = float64(position)/1000, float64(duration)/1000
		out.Entries = append(out.Entries, row)
	}
	return out, rows.Err()
}

// PlaySummary is the owner's viewing statistics for a period, read from the
// play history: the same plays the Play history page lists, so the two always
// agree, and kept for as long as the history is (forever unless the owner sets
// playHistoryDays).
type PlaySummary struct {
	reportwindow.Interval
	Plays  int64 `json:"plays"`
	People int64 `json:"people"`
	// WatchedMs is accepted played time inside the same interval as the table.
	WatchedMs int64 `json:"watchedMs"`
	// Converted counts the plays the server converted (picture or sound).
	Converted  int64       `json:"converted"`
	MostPlayed []PlayCount `json:"mostPlayed"`
	MostActive []PlayCount `json:"mostActive"`
}

// PlayCount is a name and its plays in the period.
type PlayCount struct {
	Name  string `json:"name"`
	Plays int64  `json:"plays"`
}

// PlaySummary totals the plays of a period ("24h", "7d", "30d", "90d", "all").
// A bounded period is a range over the started index; "all" reads the history
// once, which an owner asks for and the server does not do on its own.
func (s *Service) PlaySummary(ctx context.Context, q PlayHistoryQuery) (PlaySummary, error) {
	if dbwork.Snapshot(ctx) == nil {
		var out PlaySummary
		e := dbwork.WithReadSnapshot(ctx, s.db, func(snapshot context.Context) error { var e error; out, e = s.PlaySummary(snapshot, q); return e })
		return out, e
	}

	r, err := s.historyInterval(ctx, q)
	out := PlaySummary{Interval: r, MostPlayed: []PlayCount{}, MostActive: []PlayCount{}}
	if err != nil {
		return out, err
	}
	tx, done, err := historyRead(ctx, s.db)
	if err != nil {
		return out, err
	}
	defer done()
	seesRemoved, err := historySeesRemoved(ctx, tx, q.Audience)
	if err != nil {
		return out, err
	}
	filter, args, err := historyFilter(q, r, seesRemoved)
	if err != nil {
		return out, err
	}
	if err = tx.QueryRowContext(ctx, `SELECT count(*),count(DISTINCT authority||'/'||account_id),COALESCE(sum(`+historyWatched(r)+`),0),COALESCE(sum(converted),0) FROM play_history h`+filter, args...).Scan(&out.Plays, &out.People, &out.WatchedMs, &out.Converted); err != nil {
		return out, err
	}
	top := func(name string) ([]PlayCount, error) {
		rows, e := tx.QueryContext(ctx, `SELECT `+name+` AS name,count(*) AS plays FROM play_history h`+filter+` GROUP BY name ORDER BY plays DESC,name COLLATE NOCASE LIMIT 5`, args...)
		if e != nil {
			return nil, e
		}
		defer rows.Close()
		list := []PlayCount{}
		for rows.Next() {
			var v PlayCount
			if e = rows.Scan(&v.Name, &v.Plays); e != nil {
				return nil, e
			}
			if v.Name != "" {
				list = append(list, v)
			}
		}
		return list, rows.Err()
	}

	// An episode counts for its show and an audiobook file for its book; a film or a song is itself.
	if out.MostPlayed, err = top(`CASE WHEN kind IN(` + strconv.Itoa(int(compactcatalog.Episode)) + `,` + strconv.Itoa(int(compactcatalog.Part)) + `) AND parent_title<>'' THEN parent_title ELSE title END`); err != nil {
		return out, err
	}
	label := historyProfileLabel()
	out.MostActive, err = top(`CASE WHEN ` + label + `<>'' THEN h.user_name||' · '||` + label + ` ELSE h.user_name END`)
	return out, err
}

// countEntryPlays says how many times the viewer has played each song on a page
// of entries to the end (the Songs table's Plays column). One query for the
// page; a song never played carries no count.
func (s *Service) countEntryPlays(profile string, entries []ContentEntry) error {
	ids := []string{}
	for _, entry := range entries {
		if entry.Kind == "song" {
			ids = append(ids, entry.ID)
		}
	}
	if len(ids) == 0 || profile == "" {
		return nil
	}
	rows, err := s.read().Query(`SELECT pid(e.public_id),c.plays FROM (SELECT DISTINCT value FROM json_each(?)) requested
	 CROSS JOIN catalog_entities e ON e.public_id=pid_blob(requested.value)
	 CROSS JOIN personal_play_counts c ON c.profile_id=? AND c.item_id=e.id`, idsJSON(ids), profile)
	if err != nil {
		return err
	}
	defer rows.Close()
	plays := map[string]int{}
	for rows.Next() {
		var id string
		var n int
		if err = rows.Scan(&id, &n); err != nil {
			return err
		}
		plays[id] = n
	}
	if err = rows.Err(); err != nil {
		return err
	}
	for index := range entries {
		if n, ok := plays[entries[index].ID]; ok && entries[index].Kind == "song" {
			count := n
			entries[index].Plays = &count
		}
	}
	return nil
}

// serverWidePlays reports whether the owner has turned on activity shared across
// profiles (home.communityActivityEnabled): the one setting that decides whether
// any viewer sees what the whole server plays.
func (s *Service) serverWidePlays() bool {
	var on sql.NullBool
	if err := s.read().QueryRow(`SELECT settings."home.communityActivityEnabled" FROM server_settings AS settings WHERE settings.singleton=1`).Scan(&on); err != nil {
		return false
	}
	return on.Valid && on.Bool
}
