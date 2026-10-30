package librarychannels

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

type Candidate struct {
	ItemID      string `json:"itemId"`
	LibraryID   string `json:"libraryId"`
	AssetID     string `json:"assetId"`
	SourceFence string `json:"catalogBinding"`
	DurationMS  int64  `json:"durationMs"`
	Title       string `json:"title"`
	ShowID      string `json:"showId"`
	// ShowTitle is set on a preview sample of an episode: "Some of what plays"
	// names shows, not episodes.
	ShowTitle    string  `json:"showTitle,omitempty"`
	Season       int     `json:"season"`
	Episode      int     `json:"episode"`
	Weight       float64 `json:"weight"`
	SortKey      string  `json:"-"`
	Ordinal      int64   `json:"-"`
	itemEntityID int64
	showEntityID int64
}

func listSQL(v []string, args *[]any) string {
	marks := make([]string, len(v))
	for i, s := range v {
		marks[i] = "?"
		*args = append(*args, s)
	}
	return strings.Join(marks, ",")
}

func intIDsSQL(v []int64, args *[]any) string {
	marks := make([]string, len(v))
	for i, id := range v {
		marks[i] = "?"
		*args = append(*args, id)
	}
	return strings.Join(marks, ",")
}

// idsJSON renders public ids as the JSON array a pid_blob(json_each) set needs.
func idsJSON(v []string) string {
	raw, _ := json.Marshal(v)
	return string(raw)
}

// textWords splits a "mentions" phrase into lower-case words. A plural "s" is
// dropped from longer words so "sharks" also finds "shark".
func textWords(text string) []string {
	out := []string{}
	for _, w := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '\'' }) {
		if len(w) > 3 && strings.HasSuffix(w, "s") && !strings.HasSuffix(w, "ss") {
			w = strings.TrimSuffix(w, "s")
		}
		if w != "" && len(out) < 8 {
			out = append(out, w)
		}
	}
	return out
}

// orderKey is the rule's sequential order as a sortable text key over `i`, `ep`, `se`, `d`.
func orderKey(r Rule) string {
	if r.Query.Order == "least-played" {
		r.Query.Order = "title"
		return "printf('%020d',(SELECT count(*) FROM lc_play_events pe WHERE pe.channel_id=(SELECT channel_id FROM play_history_scope) AND pe.item_id=i.id AND pe.event_id<=(SELECT fence FROM play_history_scope)))||char(31)||" + orderKey(r)
	}
	if r.EpisodeMode == "rotate" {
		// One episode per show in turn: every show's S1E1, then every S1E2, and so on.
		return "printf('%04d',COALESCE(se.number,0))||printf('%05d',COALESCE(ep.number,0))||char(31)||printf('%020d',COALESCE(eps.id,0))||char(31)||printf('%020d',i.id)"
	}
	switch r.Query.Order {
	case "recent":
		return "printf('%020d',9999999999999999-COALESCE(i.added_ms,0))||char(31)||printf('%020d',i.id)"
	case "oldest":
		return "printf('%020d',COALESCE(i.added_ms,0))||char(31)||printf('%020d',i.id)"
	case "year":
		return "printf('%04d',i.year)||char(31)||lower(i.title)||char(31)||printf('%020d',i.id)"
	case "episode":
		return "printf('%020d',COALESCE(eps.id,0))||char(31)||printf('%04d',COALESCE(se.number,0))||printf('%05d',COALESCE(ep.number,0))||printf('%020d',i.id)"
	case "release":
		// Chronological: release date, else the year, oldest first.
		return "COALESCE(NULLIF(d.release_date,''),printf('%04d',i.year))||char(31)||lower(i.title)||char(31)||printf('%020d',i.id)"
	case "rating":
		// Highest community rating first; unrated last.
		return "printf('%09.3f',1000-COALESCE((SELECT max(mr.value) FROM metadata_ratings mr WHERE mr.item_id=i.id AND mr.provider IN('tmdb','imdb')),-1))||char(31)||printf('%020d',i.id)"
	}
	return "i.sort_key||char(31)||printf('%020d',i.id)"
}

// selectionSQL compiles a rule's selection into a predicate over `i`
// (catalog_entities), `ep` (catalog_episodes) and `eps` (an episode's show
// entity) — without the library/kind scope, which callers apply. The browse
// filter applies to the movie, or to the show for an episode.
func selectionSQL(q Query, at time.Time) (string, []any, error) {
	args := []any{}
	crit := []string{}
	if len(q.ShowIDs) > 0 {
		crit = append(crit, "eps.public_id IN (SELECT pid_blob(value) FROM json_each(?) WHERE pid_blob(value) IS NOT NULL)")
		args = append(args, idsJSON(q.ShowIDs))
	}
	if node, e := q.filterNode("query.filter"); e != nil {
		return "", nil, ErrInvalid
	} else if node != nil {
		where, filterArgs, e := catalog.CompileCompactBrowseSelection(node, at)
		if e != nil {
			return "", nil, ErrInvalid
		}
		crit = append(crit, "EXISTS(SELECT 1 FROM catalog_browse_rows e WHERE e.entity_id=(CASE WHEN i.kind=4 THEN eps.id ELSE i.id END) AND "+where+")")
		args = append(args, filterArgs...)
	}
	if len(q.Genres) > 0 {
		lower := []string{}
		for _, g := range q.Genres {
			lower = append(lower, strings.ToLower(g))
		}
		crit = append(crit, "EXISTS(SELECT 1 FROM catalog_term_sources src JOIN catalog_entities term_item ON term_item.id=src.entity_id JOIN catalog_terms mg ON mg.id=src.term_id WHERE term_item.id=i.id AND mg.vocab=1 AND lower(COALESCE(src.label_override,mg.label)) IN ("+listSQL(lower, &args)+"))")
	}
	if q.YearFrom > 0 {
		crit = append(crit, "i.year>=?")
		args = append(args, q.YearFrom)
	}
	if q.YearThrough > 0 {
		crit = append(crit, "i.year<=?")
		args = append(args, q.YearThrough)
	}
	if q.RecentDays > 0 {
		crit = append(crit, "COALESCE(i.added_ms,0)>=?")
		args = append(args, at.UTC().AddDate(0, 0, -q.RecentDays).UnixMilli())
	}
	for _, w := range textWords(q.Text) {
		crit = append(crit, "(instr(lower(i.title),?)>0 OR instr(lower(d.overview),?)>0 OR EXISTS(SELECT 1 FROM catalog_entities sh WHERE sh.id=eps.id AND (instr(lower(sh.title),?)>0 OR instr(lower(sd.overview),?)>0)))")
		args = append(args, w, w, w, w)
	}
	base := "1"
	if len(crit) > 0 {
		base = strings.Join(crit, " AND ")
	} else if len(q.IncludeItemIDs) > 0 {
		base = "0" // only the pinned titles
	}
	where := "(" + base + ")"
	if len(q.IncludeItemIDs) > 0 {
		pins := idsJSON(q.IncludeItemIDs)
		where = "(" + where + " OR i.public_id IN (SELECT pid_blob(value) FROM json_each(?) WHERE pid_blob(value) IS NOT NULL) OR eps.public_id IN (SELECT pid_blob(value) FROM json_each(?) WHERE pid_blob(value) IS NOT NULL))"
		args = append(args, pins, pins)
	}
	if len(q.ExcludeItemIDs) > 0 {
		pins := idsJSON(q.ExcludeItemIDs)
		where += " AND i.public_id NOT IN (SELECT pid_blob(value) FROM json_each(?) WHERE pid_blob(value) IS NOT NULL) AND (eps.public_id IS NULL OR eps.public_id NOT IN (SELECT pid_blob(value) FROM json_each(?) WHERE pid_blob(value) IS NOT NULL))"
		args = append(args, pins, pins)
	}
	return where, args, nil
}

// effectiveKinds adds episodes when a pinned title is a show, so "nothing but
// Malcolm in the Middle" works whatever kinds the rule names. The validated
// kind names are resolved through catalog_kinds at the SQL boundary.
func effectiveKinds(ctx context.Context, tx *sql.Tx, q Query) ([]string, error) {
	kinds := append([]string{}, q.Kinds...)
	if len(kinds) == 0 {
		// A rule of pinned titles only: the pins decide, whatever their kind.
		return []string{"movie", "episode"}, nil
	}
	if len(q.IncludeItemIDs) == 0 || slices.Contains(kinds, "episode") {
		return kinds, nil
	}
	var n int
	if cause := tx.QueryRowContext(ctx, `SELECT count(*) FROM catalog_entities WHERE kind=2 AND retired=0 AND public_id IN (SELECT pid_blob(value) FROM json_each(?) WHERE pid_blob(value) IS NOT NULL)`, idsJSON(q.IncludeItemIDs)).Scan(&n); cause != nil {
		return nil, unavailable(cause)
	}
	if n > 0 {
		kinds = append(kinds, "episode")
	}
	return kinds, nil
}

const candidateSelect = `SELECT pid(i.public_id),cl.library_id,i.title,COALESCE(a.token,''),COALESCE(a.duration,0),COALESCE(a.available,0),COALESCE(pid(eps.public_id),''),COALESCE(se.number,0),COALESCE(ep.number,0),COALESCE(b.status,''),COALESCE(ia.part_index,0),COALESCE(ia.start_seconds,0),ia.end_seconds,
 (SELECT count(*) FROM catalog_asset_links x WHERE x.entity_id=i.id),COALESCE(ro.incarnation,''),COALESCE(ro.revision,0),COALESCE(ao.incarnation,''),COALESCE(ao.revision,0),COALESCE(io.incarnation,''),COALESCE(io.revision,0),COALESCE(asso.incarnation,''),COALESCE(asso.revision,0),`

// candidateFrom reads the compact base tables directly: SQLite cannot flatten
// a LEFT JOIN onto a view that is itself a join, and would materialise every
// episode or asset link once per page (NEW-47).
// `ep` is the episode, `eps` its show's entity, and an item's asset is its
// linked asset with the smallest public token.
const candidateFrom = `
 FROM catalog_entities i JOIN catalog_libraries cl ON cl.id=i.library_id
 LEFT JOIN catalog_item_details d ON d.entity_id=i.id
 LEFT JOIN catalog_asset_links ia ON ia.entity_id=i.id AND ia.asset_id=(SELECT l.asset_id FROM catalog_asset_links l JOIN catalog_assets x ON x.id=l.asset_id WHERE l.entity_id=i.id ORDER BY x.token LIMIT 1)
 LEFT JOIN catalog_assets a ON a.id=ia.asset_id
 LEFT JOIN catalog_episodes ep ON ep.entity_id=i.id LEFT JOIN catalog_entities eps ON eps.id=ep.show_id LEFT JOIN catalog_seasons se ON se.entity_id=ep.season_id
 LEFT JOIN catalog_shows sd ON sd.entity_id=eps.id
 LEFT JOIN episode_asset_boundaries b ON b.item_id=i.id AND b.asset_id=a.token
 LEFT JOIN playback_origin_roots ro ON ro.id=cl.library_id LEFT JOIN playback_origin_assets ao ON ao.id=a.token LEFT JOIN playback_origin_items io ON io.id=i.id LEFT JOIN playback_origin_associations asso ON asso.item_id=i.id AND asso.asset_id=a.token`

// candidatePage reads at most `limit` catalog rows, including rejected rows.
// Rejected durations advance the cursor, never a hidden first-N cap.
//
// A rule without a semantic limit walks the library in id order in chunks of
// `limit` rows ("id:<last id>" cursors): each page costs O(limit) whatever the
// selection matches, so a 1M-item library generates in N/limit bounded pages.
// A rule with a semantic limit ("the 30 most recent") needs its order, so it
// pages by the order key (and so does a generation started before this change).
func candidatePage(ctx context.Context, tx *sql.Tx, r Rule, at time.Time, after string, limit int, history ...playHistorySnapshot) ([]Candidate, string, int, int, error) {
	if node, err := r.Query.filterNode("query.filter"); err != nil {
		return nil, after, 0, 0, ErrInvalid
	} else if node != nil {
		if err := compactcatalog.CheckReadiness(ctx, tx, compactcatalog.DomainBrowseRows); err != nil {
			return nil, after, 0, 0, err
		}
	}
	where, args, e := selectionSQL(r.Query, at)
	if e != nil {
		return nil, after, 0, 0, e
	}
	kinds, e := effectiveKinds(ctx, tx, r.Query)
	if e != nil {
		return nil, after, 0, 0, e
	}
	key := orderKey(r)
	var snapshot playHistorySnapshot
	if len(history) > 0 {
		snapshot = history[0]
	}
	if r.Query.Order == "least-played" && (!validID(snapshot.ChannelID) || snapshot.Fence < 0) {
		return nil, after, 0, 0, ErrInvalid
	}
	scope := []any{}
	scopeSQL := "cl.library_id IN (" + listSQL(r.Query.LibraryIDs, &scope) + ") AND i.kind IN (SELECT id FROM catalog_kinds WHERE name IN (" + listSQL(kinds, &scope) + ")) AND cl.retired=0 AND i.retired=0"
	byID := r.Query.Limit == 0 && (after == "" || strings.HasPrefix(after, "id:")) && !(r.Query.Order == "least-played" && snapshot.Ordered)
	var query string
	var queryArgs []any
	scanned := 0
	next := after
	if byID {
		// The chunk: the next `limit` in-scope item ids, filtered or not.
		var afterID int64
		if raw := strings.TrimPrefix(after, "id:"); raw != "" {
			afterID, _ = strconv.ParseInt(raw, 10, 64)
		}
		chunkArgs := append(append([]any{}, scope...), afterID, limit)
		rows, e := tx.QueryContext(ctx, `SELECT i.id FROM catalog_entities i JOIN catalog_libraries cl ON cl.id=i.library_id WHERE `+scopeSQL+` AND i.id>? ORDER BY i.id LIMIT ?`, chunkArgs...)
		if e != nil {
			return nil, after, 0, 0, unavailable(e)
		}
		ids := []int64{}
		for rows.Next() {
			var id int64
			if cause := rows.Scan(&id); cause != nil {
				rows.Close()
				return nil, after, 0, 0, unavailable(cause)
			}
			ids = append(ids, id)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return nil, after, 0, 0, unavailable(e)
		}
		scanned = len(ids)
		if scanned == 0 {
			return []Candidate{}, after, 0, 0, nil
		}
		next = "id:" + strconv.FormatInt(ids[len(ids)-1], 10)
		queryArgs = []any{}
		query = candidateSelect + key + `,i.id,COALESCE(eps.id,0)` + candidateFrom + ` WHERE i.id IN (` + intIDsSQL(ids, &queryArgs) + `) AND ` + where + ` ORDER BY i.id`
		queryArgs = append(queryArgs, args...)
	} else {
		queryArgs = append(append(append([]any{}, scope...), args...), after, limit)
		query = candidateSelect + key + `,i.id,COALESCE(eps.id,0)` + candidateFrom + ` WHERE ` + scopeSQL + ` AND ` + where + ` AND (` + key + `)>? ORDER BY ` + key + ` LIMIT ?`
	}
	if r.Query.Order == "least-played" {
		query = "WITH play_history_scope(channel_id,fence) AS (VALUES(?,?)) " + query
		queryArgs = append([]any{snapshot.ChannelID, snapshot.Fence}, queryArgs...)
	}
	rows, e := tx.QueryContext(ctx, query, queryArgs...)
	if e != nil {
		return nil, after, 0, 0, unavailable(e)
	}
	defer rows.Close()
	out := []Candidate{}
	unresolved, matched := 0, 0
	weights := map[string]float64{}
	for _, w := range r.Weights {
		weights[w.ItemID] = w.Weight
	}
	for rows.Next() {
		var c Candidate
		var duration, start float64
		var end sql.NullFloat64
		var available, part, alternatives int
		var boundary, ri, ai, ii, assi string
		var rr, ar, ir, assr int64
		if cause := rows.Scan(&c.ItemID, &c.LibraryID, &c.Title, &c.AssetID, &duration, &available, &c.ShowID, &c.Season, &c.Episode, &boundary, &part, &start, &end, &alternatives, &ri, &rr, &ai, &ar, &ii, &ir, &assi, &assr, &c.SortKey, &c.itemEntityID, &c.showEntityID); cause != nil {
			return nil, after, 0, 0, unavailable(cause)
		}
		matched++
		if !byID {
			next = c.SortKey
		}
		// A fixed schedule cannot guess unresolved parts, editions or runtimes. The
		// catalog binding is observed identity, NOT a fabricated strong byte version.
		if c.AssetID == "" || available != 1 || alternatives != 1 || part != 0 || start != 0 || end.Valid || ri == "" || ai == "" || ii == "" || assi == "" || duration <= 0 || duration > 7*24*3600 || math.IsNaN(duration) || math.IsInf(duration, 0) || (c.ShowID != "" && boundary != "whole_source") {
			unresolved++
			continue
		}
		c.DurationMS = int64(math.Round(duration * 1000))
		if c.DurationMS < 1 {
			unresolved++
			continue
		}
		c.SourceFence = digest("catalog-observation-v1", c.ItemID, c.LibraryID, c.AssetID, ri, fmt.Sprint(rr), ai, fmt.Sprint(ar), ii, fmt.Sprint(ir), assi, fmt.Sprint(assr))
		c.Weight = 1
		if w, ok := weights[c.ItemID]; ok {
			c.Weight = w
		}
		out = append(out, c)
	}
	if cause := rows.Err(); cause != nil {
		return nil, after, 0, 0, unavailable(cause)
	}
	if !byID {
		scanned = matched
	}
	return out, next, scanned, unresolved, nil
}

// ValidateCandidateTx fences source replacement before tune/publication. Actual
// playback still acquires and probes the source under N02 observed assurance.
func ValidateCandidateTx(ctx context.Context, tx *sql.Tx, c Candidate) error {
	var ri, ai, ii, assi string
	var rr, ar, ir, assr int64
	var available int
	var duration float64
	e := tx.QueryRowContext(ctx, `SELECT r.incarnation,r.revision,a.incarnation,a.revision,i.incarnation,i.revision,l.incarnation,l.revision,asset.available,asset.duration
 FROM catalog_entities item JOIN catalog_libraries cl ON cl.id=item.library_id
 JOIN catalog_asset_links ia ON ia.entity_id=item.id JOIN catalog_assets asset ON asset.id=ia.asset_id
 JOIN playback_origin_roots r ON r.id=cl.library_id JOIN playback_origin_assets a ON a.id=asset.token
 JOIN playback_origin_items i ON i.id=item.id JOIN playback_origin_associations l ON l.item_id=item.id AND l.asset_id=asset.token
 WHERE item.public_id=pid_blob(?) AND cl.library_id=? AND asset.token=? AND item.retired=0`, c.ItemID, c.LibraryID, c.AssetID).Scan(&ri, &rr, &ai, &ar, &ii, &ir, &assi, &assr, &available, &duration)
	if e != nil || available != 1 || int64(math.Round(duration*1000)) != c.DurationMS || digest("catalog-observation-v1", c.ItemID, c.LibraryID, c.AssetID, ri, fmt.Sprint(rr), ai, fmt.Sprint(ar), ii, fmt.Sprint(ir), assi, fmt.Sprint(assr)) != c.SourceFence {
		return ErrConflict
	}
	return nil
}

type RulePreview struct {
	RuleID        string      `json:"ruleId"`
	Eligible      int         `json:"eligible"`
	Unresolved    int         `json:"unresolved"`
	Sample        []Candidate `json:"sample"`
	SemanticLimit int         `json:"semanticLimit"`
	// DurationMS is the programming the counted matches add up to.
	DurationMS int64 `json:"durationMs"`
	// Complete is false when the preview stopped at its scan budget: the counts
	// are then "at least" (the builder says "10,000+ titles").
	Complete bool `json:"complete"`
	Scanned  int  `json:"scanned"`
}

// Preview budgets: bounded work whatever the library holds (1M items previews in
// the same time as 20k). Generation itself still walks every match.
const (
	previewScanBudget   = 20000
	previewCandidateCap = 2000
)

type ResolvedBlock struct {
	ID       string        `json:"id"`
	RuleID   string        `json:"ruleId"`
	Interval BlockInterval `json:"interval"`
	Priority int           `json:"priority"`
	Overrun  string        `json:"overrun"`
	Anchor   string        `json:"anchor"`
	Fallback string        `json:"fallbackRuleId"`
}
type Preview struct {
	CatalogFence   string          `json:"catalogFence"`
	Rules          []RulePreview   `json:"rules"`
	Blocks         []ResolvedBlock `json:"blocks"`
	Window         BlockInterval   `json:"window"`
	BoundaryPolicy string          `json:"boundaryPolicy"`
	// FirstDay is the first local day as saving would schedule it (the real
	// generator over the previewed candidates, in a transaction rolled back).
	FirstDay []Entry `json:"firstDay"`
	// DurationMS and Complete sum the rules (see RulePreview).
	DurationMS int64 `json:"durationMs"`
	Complete   bool  `json:"complete"`
}

var errPreviewDone = errors.New("preview rolled back")

func resolvedBlocks(c Config, start, end time.Time) ([]ResolvedBlock, error) {
	loc, e := time.LoadLocation(c.Timezone)
	if e != nil {
		return nil, ErrInvalid
	}
	from := start.In(loc).AddDate(0, 0, -1)
	day := time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, time.UTC)
	last := end.In(loc).AddDate(0, 0, 1)
	lastCivil := time.Date(last.Year(), last.Month(), last.Day(), 0, 0, 0, 0, time.UTC)
	out := []ResolvedBlock{}
	for days := 0; !day.After(lastCivil); days++ {
		if days > 11 {
			return nil, ErrInvalid
		}
		for _, b := range c.Blocks {
			match := false
			for _, d := range b.Weekdays {
				if d == int(day.Weekday()) {
					match = true
				}
			}
			if !match {
				continue
			}
			v, e := ResolveBlock(day.Format("2006-01-02"), b.StartMinute, b.EndMinute, c.Timezone)
			if e != nil {
				return nil, e
			}
			if v.End.UTC.After(start) && v.Start.UTC.Before(end) && v.End.UTC.After(v.Start.UTC) {
				out = append(out, ResolvedBlock{b.ID + ":" + day.Format("2006-01-02"), b.RuleID, v, b.Priority, b.Overrun, b.Anchor, b.FallbackRuleID})
			}
		}
		day = day.AddDate(0, 0, 1)
	}
	return out, nil
}
func (s *Store) Preview(ctx context.Context, a Authority, c Config) (Preview, error) {
	return s.preview(ctx, a, c, true)
}

// eligibility is the templates' probe: counts within the scan budget only, in
// a read snapshot (never waiting on the write gate), with no first-day
// schedule. Listing templates must stay cheap on a one-core host: nine full
// previews there took longer than a request's budget (demo, 23 Sep).
func (s *Store) eligibility(ctx context.Context, a Authority, c Config) (Preview, error) {
	return s.preview(ctx, a, c, false)
}

func (s *Store) preview(ctx context.Context, a Authority, c Config, firstDay bool) (Preview, error) {
	out := Preview{Rules: []RulePreview{}, Blocks: []ResolvedBlock{}, FirstDay: []Entry{}, BoundaryPolicy: "gap-forward; repeated-first; half-open UTC intervals", Complete: true}
	c, e0 := Normalize(c)
	if e0 != nil {
		return out, e0
	}
	if e := Validate(c); e != nil {
		return out, e
	}
	observedAt := s.now()
	loc, _ := time.LoadLocation(c.Timezone)
	var e error
	out.Window, e = SevenDayWindow(observedAt.In(loc).Format("2006-01-02"), c.Timezone)
	if e != nil {
		return out, e
	}
	out.Blocks, e = resolvedBlocks(c, out.Window.Start.UTC, out.Window.End.UTC)
	if e != nil {
		return out, e
	}
	// One bounded pass in a write transaction that is always rolled back: count and
	// sample each rule within the scan budget, then let the real generator schedule
	// the first day from the sampled candidates into temporary rows.
	run := s.transaction
	if !firstDay {
		run = s.snapshot
	}
	e = run(ctx, a, true, func(tx *sql.Tx, scope Scope) error {
		if !scope.permits(c) {
			return ErrDenied
		}
		f, e := catalogFence(ctx, tx, c)
		if e != nil {
			return e
		}
		out.CatalogFence = f
		var historyFence int64
		if hasLeastPlayed(c) {
			historyFence, e = playHistoryFence(ctx, tx)
			if e != nil {
				return unavailable(e)
			}
		}
		pools := map[string][]Candidate{}
		for _, r := range c.Rules {
			rp := RulePreview{RuleID: r.ID, Sample: []Candidate{}, SemanticLimit: r.Query.Limit, Complete: true}
			sampledShows := map[string]bool{}
			after := ""
			for {
				limit := CandidateBatch
				if r.Query.Limit > 0 && r.Query.Limit-rp.Eligible < limit {
					limit = r.Query.Limit - rp.Eligible
				}
				if limit <= 0 {
					break
				}
				if rp.Scanned >= previewScanBudget {
					rp.Complete = false
					break
				}
				page, next, n, unresolved, e := candidatePage(ctx, tx, r, observedAt, after, limit, playHistorySnapshot{ChannelID: c.ID, Fence: historyFence, Ordered: true})
				if e != nil {
					return e
				}
				after = next
				rp.Scanned += n
				rp.Unresolved += unresolved
				rp.Eligible += len(page)
				for _, v := range page {
					rp.DurationMS += v.DurationMS
					if len(pools[r.ID]) < previewCandidateCap {
						pools[r.ID] = append(pools[r.ID], v)
					}
					// One sample per show: a TV or anime channel shows which shows
					// play, not eight episodes of the first one.
					if len(rp.Sample) < 8 && (v.ShowID == "" || !sampledShows[v.ShowID]) {
						sample := v
						sample.SourceFence, sample.AssetID = "", ""
						rp.Sample = append(rp.Sample, sample)
						sampledShows[v.ShowID] = v.ShowID != ""
					}
				}
				if n < limit {
					break
				}
			}
			if err := sampleShowTitles(ctx, tx, rp.Sample); err != nil {
				return err
			}
			out.DurationMS += rp.DurationMS
			out.Complete = out.Complete && rp.Complete
			out.Rules = append(out.Rules, rp)
		}
		if !firstDay {
			return errPreviewDone
		}
		day, complete, e := previewFirstDay(ctx, tx, c, pools, observedAt)
		if e != nil {
			return e
		}
		out.FirstDay = day
		out.Complete = out.Complete && complete
		return errPreviewDone
	})
	if e != nil && !errors.Is(e, errPreviewDone) {
		return out, e
	}
	return out, nil
}

// sampleShowTitles names each episode sample's show (one statement).
func sampleShowTitles(ctx context.Context, tx *sql.Tx, samples []Candidate) error {
	shows := []string{}
	for _, v := range samples {
		if v.ShowID != "" {
			shows = append(shows, v.ShowID)
		}
	}
	if len(shows) == 0 {
		return nil
	}
	raw, _ := json.Marshal(shows)
	rows, err := tx.QueryContext(ctx, `SELECT pid(e.public_id),e.title FROM catalog_entities e WHERE e.kind=2 AND e.retired=0 AND e.public_id IN (SELECT pid_blob(value) FROM json_each(?) WHERE pid_blob(value) IS NOT NULL)`, string(raw))
	if err != nil {
		return unavailable(err)
	}
	defer rows.Close()
	titles := map[string]string{}
	for rows.Next() {
		var id, title string
		if err = rows.Scan(&id, &title); err != nil {
			return unavailable(err)
		}
		titles[id] = title
	}
	if err = rows.Err(); err != nil {
		return unavailable(err)
	}
	for i := range samples {
		samples[i].ShowTitle = titles[samples[i].ShowID]
	}
	return nil
}

// previewFirstDay schedules the first local day with the real generator over
// temporary channel, generation and candidate rows (the caller rolls back).
// previewDeadlineMargin is how much of a request's budget a preview leaves for
// answering once its first day stops early.
const previewDeadlineMargin = 1500 * time.Millisecond

func previewFirstDay(ctx context.Context, tx *sql.Tx, c Config, pools map[string][]Candidate, now time.Time) ([]Entry, bool, error) {
	loc, e := time.LoadLocation(c.Timezone)
	if e != nil {
		return nil, false, ErrInvalid
	}
	local := now.In(loc)
	start := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
	end := start.AddDate(0, 0, 1)
	id := "preview-" + digest(c.ID, fmt.Sprint(now.UnixNano()))[:24]
	if _, e = tx.ExecContext(ctx, `INSERT INTO lc_channels(id,revision,config_json,enabled,position,name,state) VALUES(?,1,'{}',0,0,'preview','preview')`, id); e != nil {
		return nil, false, unavailable(e)
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO lc_generations(id,channel_id,config_revision,config_json,catalog_fence,seed,status,phase,base_generation,start_ms,end_ms,boundary_ms,cursor_ms,created_ms) VALUES(?,?,1,'{}','',?,'preview','scheduling','',?,?,?,?,?)`, id, id, c.Seed, start.UnixMilli(), end.UnixMilli(), start.UnixMilli(), start.UnixMilli(), now.UnixMilli()); e != nil {
		return nil, false, unavailable(e)
	}
	stmts := newStmtCache(tx)
	for _, r := range c.Rules {
		for ordinal, v := range pools[r.ID] {
			if e = insertCandidate(ctx, stmts, id, r.ID, int64(ordinal), v); e != nil {
				return nil, false, unavailable(e)
			}
		}
	}
	g := &generation{ID: id, ChannelID: id, Revision: 1, Config: c, Seed: c.Seed, Phase: "scheduling", Start: start.UnixMilli(), End: end.UnixMilli(), Boundary: start.UnixMilli(), Cursor: start.UnixMilli(), Created: now.UnixMilli(), stmts: stmts}
	complete := true
	for n := 0; g.Phase == "scheduling" && n < 2000; n++ {
		// Near the request's deadline, show the part of the day already made
		// (flagged incomplete) rather than fail the whole preview.
		if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) < previewDeadlineMargin {
			complete = false
			break
		}
		if e = scheduleOne(ctx, tx, g); e != nil {
			return nil, false, e
		}
	}
	entries, e := entriesWindow(ctx, tx, id, start.UnixMilli(), end.UnixMilli(), 2000)
	return entries, complete, e
}
