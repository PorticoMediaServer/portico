package catalog

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/reportwindow"
	"strconv"
	"strings"
)

const dashboardKind = `CASE h.kind WHEN 1 THEN 'movies' WHEN 4 THEN 'tv' WHEN 7 THEN 'music' WHEN 9 THEN 'audiobooks' WHEN 0 THEN 'live' ELSE 'other' END`

// historyProfileLabel is the name a play is attributed to. A removed profile
// keeps its last display name in direct_profiles and its plays stay in the
// owner's history, marked "(removed)"; a live profile keeps the name the play
// was recorded under. A play without a retained profile row is unchanged.
func historyProfileLabel() string {
	return `CASE WHEN h.authority<>'local' THEN h.profile_name ELSE COALESCE((SELECT CASE WHEN dp.deleted=1 THEN dp.name||' (removed)' ELSE h.profile_name END
	 FROM direct_profiles dp WHERE dp.account_id=h.account_id AND dp.id=h.profile_id),h.profile_name) END`
}

// historySeesRemoved says whether this audience is the live server owner's main
// profile, the one principal allowed to read retained plays of removed
// profiles. An admin, a member, another profile of the owner's account or a
// stale principal is not. It reads the projection's own transaction, so the
// authorization and the rows it guards share one boundary.
func historySeesRemoved(ctx context.Context, tx *sql.Tx, audience *HistoryAudience) (bool, error) {
	if tx == nil {
		return false, sql.ErrTxDone
	}
	if audience == nil || audience.AccountID == "" || audience.ProfileID == "" {
		return false, nil
	}
	var allowed bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM accounts a
	 JOIN direct_memberships m ON m.account_id=a.id
	 JOIN direct_profiles main ON main.account_id=a.id AND main.id=a.profile_id
	 WHERE a.id=? AND a.epoch=? AND a.profile_id=? AND main.is_primary=1 AND main.deleted=0
	 AND m.role='owner' AND m.disabled=0)`, audience.AccountID, audience.Epoch, audience.ProfileID).Scan(&allowed)
	return allowed, err
}

// historyRemovedFilter withholds retained plays of removed profiles from every
// caller except the owner's main profile. Only a local profile can be removed;
// a hosted row that happens to carry the same account and profile ids keeps its
// stored attribution and is never hidden. Live-profile rows are unaffected.
func historyRemovedFilter(seesRemoved bool) string {
	if seesRemoved {
		return ""
	}
	return `NOT EXISTS(SELECT 1 FROM direct_profiles dp WHERE h.authority='local' AND dp.account_id=h.account_id AND dp.id=h.profile_id AND dp.deleted=1)`
}

// This filter is shared with the full history table. Person is always the
// server account id, never a name or a profile id.
func historyFilter(q PlayHistoryQuery, interval reportwindow.Interval, seesRemoved bool) (string, []any, error) {
	if len(q.AccountID) > 128 || len(q.LibraryID) > 128 || len(q.Search) > 200 {
		return "", nil, ErrCursor
	}
	where, args := []string{"h.started_ms<?"}, []any{interval.UntilMS()}
	if interval.Period != "all" {
		where = append(where, "h.started_ms>=?")
		args = append(args, interval.FromMS())
	}
	for _, f := range []struct{ key, value string }{{"h.account_id", q.AccountID}, {"h.library_id", q.LibraryID}} {
		if f.value != "" {
			where = append(where, f.key+"=?")
			args = append(args, f.value)
		}
	}
	if q.Media != "" && q.Media != "all" {
		if !strings.Contains("|movies|tv|music|audiobooks|live|other|", "|"+q.Media+"|") {
			return "", nil, ErrCursor
		}
		where = append(where, "("+dashboardKind+")=?")
		args = append(args, q.Media)
	}
	if q.Search != "" {
		where = append(where, `(instr(lower(h.title),lower(?))>0 OR instr(lower(h.user_name),lower(?))>0 OR instr(lower(h.device_name),lower(?))>0)`)
		args = append(args, q.Search, q.Search, q.Search)
	}
	if retained := historyRemovedFilter(seesRemoved); retained != "" {
		where = append(where, retained)
	}
	return " WHERE " + strings.Join(where, " AND "), args, nil
}

// Report composers borrow the request's one read transaction. Ordinary calls
// still own a bounded read; they never create a persistent report snapshot.
func historyRead(ctx context.Context, db *sql.DB) (*sql.Tx, func(), error) {
	if tx := dbwork.Snapshot(ctx); tx != nil {
		return tx, func() {}, nil
	}
	return dbwork.BeginRead(ctx, db)
}
func (s *Service) historyInterval(ctx context.Context, q PlayHistoryQuery) (reportwindow.Interval, error) {
	period := q.Period
	if period == "" {
		period = "all"
	}
	switch period {
	case "today", "12h", "24h", "7d", "30d", "90d", "1y", "all":
	default:
		return reportwindow.Interval{}, ErrCursor
	}
	r, e := reportwindow.Resolve(period, q.TimeZone, q.Until, -1)
	if e != nil {
		return r, ErrCursor
	}
	_, _, e = historyFilter(q, r, false)
	if e != nil {
		return r, e
	}
	if period == "all" {
		first, e := reportwindow.FirstFact(ctx, s.db, r.UntilMS())
		if e != nil {
			return r, e
		}
		r, e = reportwindow.Resolve(period, q.TimeZone, q.Until, first)
	}
	return r, e
}

// Both numbers come only from the validated interval, never SQL input text.
func historyWatched(r reportwindow.Interval) string {
	from := r.FromMS() - r.FromMS()%reportwindow.Grain.Milliseconds()
	return fmt.Sprintf(`COALESCE((SELECT sum(t.watched_ms) FROM play_history_time t WHERE t.history_id=h.id AND t.bucket_ms>=%d AND t.bucket_ms<%d),0)`, from, r.UntilMS())
}

type DashboardKindTime struct {
	Kind      string `json:"kind"`
	Plays     int64  `json:"plays"`
	WatchedMs int64  `json:"watchedMs"`
}
type DashboardPerson struct {
	AccountID string              `json:"accountId"`
	Username  string              `json:"username"`
	Profiles  []string            `json:"profiles"`
	Plays     int64               `json:"plays"`
	WatchedMs int64               `json:"watchedMs"`
	Kinds     []DashboardKindTime `json:"kinds"`
}
type DashboardPeople struct {
	reportwindow.Interval
	Items      []DashboardPerson `json:"items"`
	NextCursor string            `json:"nextCursor"`
}
type DashboardBucket struct {
	Start   string              `json:"start"`
	End     string              `json:"end"`
	Partial bool                `json:"partial"`
	Kinds   []DashboardKindTime `json:"kinds"`
}
type DashboardTop struct {
	ItemID           string `json:"itemId"`
	Title            string `json:"title"`
	Plays            int64  `json:"plays"`
	People           int64  `json:"people"`
	CountUnit        string `json:"countUnit"`
	PosterRevision   string `json:"posterRevision,omitempty"`
	BackdropRevision string `json:"backdropRevision,omitempty"`
}
type DashboardHistory struct {
	reportwindow.Bounds
	Buckets []DashboardBucket `json:"buckets"`
	// Hours Monday..Sunday, 0..23, actual played milliseconds.
	BusiestTimes      [][]int64      `json:"busiestTimes"`
	QuietestStartHour *int           `json:"quietestStartHour"`
	Top               []DashboardTop `json:"top"`
	TopKind           string         `json:"topKind"`
}

func (s *Service) DashboardPeople(ctx context.Context, q PlayHistoryQuery) (DashboardPeople, error) {
	if dbwork.Snapshot(ctx) == nil {
		var out DashboardPeople
		e := dbwork.WithReadSnapshot(ctx, s.db, func(snapshot context.Context) error { var e error; out, e = s.DashboardPeople(snapshot, q); return e })
		return out, e
	}

	r, e := s.historyInterval(ctx, q)
	out := DashboardPeople{Interval: r, Items: []DashboardPerson{}}
	if e != nil {
		return out, e
	}
	if q.Limit == 0 {
		q.Limit = 12
	}
	if q.Limit < 1 || q.Limit > 100 {
		return out, ErrCursor
	}
	afterTime, afterID := int64(1<<62), ""
	if q.Cursor != "" {
		t, id, ok := strings.Cut(q.Cursor, ":")
		var e error
		afterTime, e = strconv.ParseInt(t, 10, 64)
		if !ok || e != nil || afterTime < 0 || len(id) > 128 {
			return out, ErrCursor
		}
		afterID = id
	}
	tx, done, e := historyRead(ctx, s.db)
	if e != nil {
		return out, e
	}
	defer done()
	seesRemoved, e := historySeesRemoved(ctx, tx, q.Audience)
	if e != nil {
		return out, e
	}
	filter, args, e := historyFilter(q, r, seesRemoved)
	if e != nil {
		return out, e
	}
	rows, e := tx.QueryContext(ctx, `WITH ranked AS (
 SELECT account_id,COALESCE((SELECT a.username FROM accounts a WHERE a.id=h.account_id),max(user_name)) username,count(*) plays,sum(`+historyWatched(r)+`) watched FROM play_history h`+filter+` GROUP BY account_id
 ) SELECT account_id,username,plays,watched FROM ranked WHERE watched<? OR watched=? AND account_id>? ORDER BY watched DESC,account_id LIMIT ?`, append(args, afterTime, afterTime, afterID, q.Limit+1)...)
	if e != nil {
		return out, e
	}
	for rows.Next() {
		var p DashboardPerson
		if e = rows.Scan(&p.AccountID, &p.Username, &p.Plays, &p.WatchedMs); e != nil {
			rows.Close()
			return out, e
		}
		p.Profiles = []string{}
		p.Kinds = []DashboardKindTime{}
		out.Items = append(out.Items, p)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	if len(out.Items) > q.Limit {
		out.Items = out.Items[:q.Limit]
		p := out.Items[len(out.Items)-1]
		out.NextCursor = fmt.Sprintf("%d:%s", p.WatchedMs, p.AccountID)
	}
	// Bounded account page. Kinds/profiles are read only for those accounts.
	for i := range out.Items {
		p := &out.Items[i]
		rows, e = tx.QueryContext(ctx, `SELECT `+dashboardKind+`,count(*),sum(`+historyWatched(r)+`) FROM play_history h`+filter+` AND account_id=? GROUP BY `+dashboardKind, append(append([]any{}, args...), p.AccountID)...)
		if e != nil {
			return out, e
		}
		for rows.Next() {
			var k DashboardKindTime
			if e = rows.Scan(&k.Kind, &k.Plays, &k.WatchedMs); e != nil {
				rows.Close()
				return out, e
			}
			p.Kinds = append(p.Kinds, k)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return out, e
		}
		rows, e = tx.QueryContext(ctx, `WITH named AS (
	 SELECT `+historyProfileLabel()+` profile_name,h.user_name FROM play_history h`+filter+` AND h.account_id=?)
	 SELECT DISTINCT profile_name FROM named WHERE profile_name<>'' AND profile_name<>user_name ORDER BY profile_name LIMIT 8`, append(append([]any{}, args...), p.AccountID)...)
		if e != nil {
			return out, e
		}
		for rows.Next() {
			var name string
			if e = rows.Scan(&name); e != nil {
				rows.Close()
				return out, e
			}
			p.Profiles = append(p.Profiles, name)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return out, e
		}
	}
	return out, nil
}

func (s *Service) DashboardHistory(ctx context.Context, q PlayHistoryQuery, topKind string) (DashboardHistory, error) {
	if dbwork.Snapshot(ctx) == nil {
		var out DashboardHistory
		e := dbwork.WithReadSnapshot(ctx, s.db, func(snapshot context.Context) error {
			var e error
			out, e = s.DashboardHistory(snapshot, q, topKind)
			return e
		})
		return out, e
	}

	r, e := s.historyInterval(ctx, q)
	out := DashboardHistory{Bounds: r.Bounds, Buckets: []DashboardBucket{}, Top: []DashboardTop{}, TopKind: topKind, BusiestTimes: make([][]int64, 7)}
	if e != nil {
		return out, e
	}
	for i := range out.BusiestTimes {
		out.BusiestTimes[i] = make([]int64, 24)
	}
	if topKind == "" {
		topKind = "movies"
		out.TopKind = topKind
	}
	if topKind != "movies" && topKind != "tv" && topKind != "music" {
		return out, ErrCursor
	}
	tx, done, e := historyRead(ctx, s.db)
	if e != nil {
		return out, e
	}
	defer done()
	seesRemoved, e := historySeesRemoved(ctx, tx, q.Audience)
	if e != nil {
		return out, e
	}
	filter, args, e := historyFilter(q, r, seesRemoved)
	if e != nil {
		return out, e
	}
	for _, b := range r.Buckets {
		out.Buckets = append(out.Buckets, DashboardBucket{Start: b.Start, End: b.End, Partial: b.Partial, Kinds: []DashboardKindTime{}})
	}
	from := r.FromMS() - r.FromMS()%reportwindow.Grain.Milliseconds()
	// Read actual fifteen-minute event-time facts. Absent reports remain absent; no elapsed-session
	// lifetime or completion percentage is substituted for watched time.
	rows, e := tx.QueryContext(ctx, `SELECT t.bucket_ms,`+dashboardKind+`,sum(t.watched_ms) FROM play_history_time t CROSS JOIN play_history h ON h.id=t.history_id`+filter+` AND t.bucket_ms>=? AND t.bucket_ms<? GROUP BY t.bucket_ms,`+dashboardKind+` ORDER BY t.bucket_ms`, append(append([]any{}, args...), from, r.UntilMS())...)
	if e != nil {
		return out, e
	}
	for rows.Next() {
		var at, ms int64
		var kind string
		if e = rows.Scan(&at, &kind, &ms); e != nil {
			rows.Close()
			return out, e
		}
		t := r.Local(at)
		index, ok := r.Index(max(at, r.FromMS()))
		if !ok {
			continue
		}
		kinds := &out.Buckets[index].Kinds
		found := false
		for i := range *kinds {
			if (*kinds)[i].Kind == kind {
				(*kinds)[i].WatchedMs += ms
				found = true
			}
		}
		if !found {
			*kinds = append(*kinds, DashboardKindTime{Kind: kind, WatchedMs: ms})
		}
		out.BusiestTimes[(int(t.Weekday())+6)%7][t.Hour()] += ms
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	rows, e = tx.QueryContext(ctx, `SELECT (h.started_ms/900000)*900000,`+dashboardKind+`,count(*) FROM play_history h`+filter+` GROUP BY h.started_ms/900000,`+dashboardKind, args...)
	if e != nil {
		return out, e
	}
	for rows.Next() {
		var at, plays int64
		var kind string
		if e = rows.Scan(&at, &kind, &plays); e != nil {
			rows.Close()
			return out, e
		}
		index, ok := r.Index(max(at, r.FromMS()))
		if !ok {
			continue
		}
		found := false
		kinds := &out.Buckets[index].Kinds
		for i := range *kinds {
			if (*kinds)[i].Kind == kind {
				(*kinds)[i].Plays += plays
				found = true
			}
		}
		if !found {
			*kinds = append(*kinds, DashboardKindTime{Kind: kind, Plays: plays})
		}
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	var totalHeat int64
	best := int64(1 << 62)
	quiet := 0
	for h := 0; h <= 21; h++ {
		var sum int64
		for _, day := range out.BusiestTimes {
			for k := h; k < h+3; k++ {
				sum += day[k]
			}
		}
		totalHeat += sum
		if sum < best {
			best = sum
			quiet = h
		}
	}
	if totalHeat > 0 {
		out.QuietestStartHour = &quiet
	}
	// Only the top six are joined to catalogue artwork. Music ranks albums,
	// counting track plays; TV ranks shows, counting episode plays.
	target := `h.item_id`
	unit := "movie_plays"
	if topKind == "tv" {
		target = `(SELECT ep.show_id FROM catalog_episodes ep WHERE ep.entity_id=h.item_id)`
		unit = "episode_plays"
	}
	if topKind == "music" {
		target = `(SELECT song.album_id FROM catalog_songs song WHERE song.entity_id=h.item_id)`
		unit = "track_plays"
	}
	rows, e = tx.QueryContext(ctx, `WITH ranked AS (
 SELECT `+target+` target,count(*) plays,count(DISTINCT h.account_id) people FROM play_history h`+filter+` AND (`+dashboardKind+`)=? GROUP BY target ORDER BY plays DESC,people DESC,target LIMIT 6)
 SELECT COALESCE(pid(i.public_id),''),COALESCE(i.title,''),r.plays,r.people,
 COALESCE((SELECT a.digest FROM artwork_selections a WHERE a.entity_id=i.id AND a.role IN('poster','cover','square') AND a.kind=? AND a.subject='' LIMIT 1),''),
 COALESCE((SELECT a.digest FROM artwork_selections a WHERE a.entity_id=i.id AND a.role='backdrop' AND a.kind=? AND a.subject='' LIMIT 1),'')
 FROM ranked r JOIN catalog_entities i ON i.id=r.target ORDER BY r.plays DESC,r.people DESC,r.target`, append(append([]any{}, args...), topKind, map[string]string{"movies": "item", "tv": "show", "music": "album"}[topKind], map[string]string{"movies": "item", "tv": "show", "music": "album"}[topKind])...)
	if e != nil {
		return out, e
	}
	defer rows.Close()
	for rows.Next() {
		var v DashboardTop
		if e = rows.Scan(&v.ItemID, &v.Title, &v.Plays, &v.People, &v.PosterRevision, &v.BackdropRevision); e != nil {
			return out, e
		}
		v.CountUnit = unit
		out.Top = append(out.Top, v)
	}
	return out, rows.Err()
}
