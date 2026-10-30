package livechannels

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/text/collate"
	"golang.org/x/text/language"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/thirdparty/sqlite"
)

// Directory ordering is identical on every client: English Unicode collation,
// numeric channel prefixes (including decimal/exponent prefixes), then stable ids.
// Unlike SQLite CAST, a nonnumeric number sorts after numeric channel numbers.
var channelNumericPrefix = regexp.MustCompile(`^[+-]?(?:[0-9]+\.?[0-9]*|\.[0-9]+)(?:[eE][+-]?[0-9]+)?`)
var directoryCollators = sync.Pool{New: func() any { return collate.New(language.English) }}

func init() {
	sqlite.MustRegisterCollationUtf8("portico_guide_name", func(a, b string) int {
		c := directoryCollators.Get().(*collate.Collator)
		n := c.CompareString(a, b)
		directoryCollators.Put(c)
		return n
	})
	sqlite.MustRegisterDeterministicScalarFunction("portico_channel_number", 1, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		s, _ := args[0].(string)
		prefix := channelNumericPrefix.FindString(strings.TrimSpace(s))
		if prefix == "" {
			return nil, nil
		}
		n, err := strconv.ParseFloat(prefix, 64)
		if err != nil || math.IsNaN(n) || math.IsInf(n, 0) {
			return nil, nil
		}
		return n, nil
	})
}

var ErrDirectoryAnchorNotFound = errors.New("The channel is not available in this view.")

var ErrChannelIdentifierConflict = errors.New("Two authorized channel sources use the same channel identifier. The server owner must change the conflicting Library Channel identifier.")

type DirectoryRow struct {
	Channel    Channel     `json:"channel"`
	Source     GuideSource `json:"source"`
	Revision   int64       `json:"revision"`
	Position   int         `json:"position"`
	LogoItemID string      `json:"-"`
}

// DirectoryAuthority is a source-level policy projection inside this snapshot.
// It is separate from Authority, whose callback can additionally restrict
// individual channels. HTTP's source membership policy is source-level.
type DirectoryScope struct {
	Owner              bool
	Fence              string
	AllowedLiveSources []string
	LibraryRows        []DirectoryRow
	Viewer             Owner
}
type DirectoryAuthority func(context.Context, *sql.Tx) (DirectoryScope, error)
type DirectoryQuery struct {
	Kind                string `json:"kind"`
	SourceID            string `json:"sourceId"`
	Search              string `json:"search"`
	Group               string `json:"group"`
	FavoritesOnly       bool   `json:"favorites"`
	IncludeHidden       bool   `json:"includeHidden"`
	IncludeServerHidden bool   `json:"includeServerHidden"`
	Sort                string `json:"sort"`
	Offset              int    `json:"offset"`
	Limit               int    `json:"limit"`
	Revision            string `json:"-"`
	AnchorChannelID     string `json:"-"`
	AnchorSourceID      string `json:"-"`
	AnchorProvenance    string `json:"-"`
	Direction           string `json:"-"`
	DeliveryAvailable   bool   `json:"-"`
}
type Directory struct {
	ViewerFence string        `json:"viewerFence"`
	Revision    string        `json:"revision"`
	Total       int           `json:"total"`
	Offset      int           `json:"offset"`
	Limit       int           `json:"limit"`
	Channels    []Channel     `json:"channels"`
	Sources     []GuideSource `json:"sources"`
}
type GuideGroupCount struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}
type SourceSummary struct {
	GuideSource
	Position           int               `json:"position"`
	RecordAvailable    bool              `json:"recordAvailable"`
	GuideDays          int               `json:"guideDays"`
	ChannelCount       int               `json:"channelCount"`
	Groups             []string          `json:"groups"`
	GroupCounts        []GuideGroupCount `json:"groupCounts"`
	FavoriteCount      int               `json:"favoriteCount"`
	UnavailableReasons map[string]int    `json:"unavailableReasons"`
}
type DirectorySummary struct {
	ViewerFence string          `json:"viewerFence"`
	Revision    string          `json:"revision"`
	Sources     []SourceSummary `json:"sources"`
}

// Every live row stays in SQLite. Only the <=64 library configurations cross
// the Go boundary to apply their selected-library policy. Count, source summary
// and global sort/paging use this same authorized, filtered relation.
// A profile's own hidden mark reads as 0: only the owner hides channels, for
// everyone (admin_channel_numbers.hidden). Older per-profile marks have no
// control left to undo them, so they are ignored rather than obeyed.
const directoryEligibleSQL = `WITH candidates AS (
 SELECT c.channel_id,s.id source_id,'live-source' provenance,c.name,COALESCE(NULLIF(n.number,''),c.number) number,c.group_name,s.active_generation generation,'' logo_path,
 COALESCE(p.favorite,0) favorite,0 hidden,COALESCE(p.revision,0) preference_revision,COALESCE(n.hidden,0) server_hidden,
 s.name source_name,s.published_at,c.available_start,c.available_end,COALESCE(r.state,'manual') refresh_state,0 source_position,c.position channel_position,s.revision+COALESCE(n.updated_ms,0) config_revision
 FROM live_sources s JOIN live_channel_versions c ON c.generation_id=s.active_generation
 LEFT JOIN live_channel_preferences p ON p.authority=? AND p.account_id=? AND p.profile_id=? AND p.source_id=s.id AND p.channel_id=c.channel_id
 LEFT JOIN live_remote_configs r ON r.source_id=s.id
 LEFT JOIN admin_channel_numbers n ON n.source_id=s.id AND n.channel_id=c.channel_id
 WHERE s.state='active' AND (?=1 OR COALESCE(n.hidden,0)=0) AND s.id IN (SELECT value FROM json_each(?))
 UNION ALL
 SELECT json_extract(value,'$.channel.id'),json_extract(value,'$.channel.sourceId'),'library-channel',json_extract(value,'$.channel.name'),json_extract(value,'$.channel.number'),json_extract(value,'$.channel.group'),json_extract(value,'$.channel.generation'),COALESCE(json_extract(value,'$.channel.logoPath'),''),
 json_extract(value,'$.channel.favorite'),json_extract(value,'$.channel.hidden'),json_extract(value,'$.channel.preferenceRevision'),0,json_extract(value,'$.source.name'),json_extract(value,'$.source.publishedAt'),json_extract(value,'$.source.availableStart'),json_extract(value,'$.source.availableEnd'),json_extract(value,'$.source.refreshState'),json_extract(value,'$.position'),json_extract(value,'$.position'),json_extract(value,'$.revision') FROM json_each(?)
), eligible AS (SELECT * FROM candidates WHERE (?='all' OR provenance=?)
 AND (?='' OR source_id=? OR (?='library' AND provenance='library-channel'))
 AND (?='' OR instr(lower(name||' '||group_name),lower(?))>0)
 AND (?='' OR group_name=?) AND (?=0 OR favorite=1) AND (?=1 OR hidden=0)) `

func directoryArgs(scope DirectoryScope, q DirectoryQuery) []any {
	live, _ := json.Marshal(scope.AllowedLiveSources)
	library, _ := json.Marshal(scope.LibraryRows)
	return []any{scope.Viewer.Authority, scope.Viewer.AccountID, scope.Viewer.ProfileID, q.IncludeServerHidden && scope.Owner, string(live), string(library), q.Kind, q.Kind, q.SourceID, q.SourceID, q.SourceID, q.Search, q.Search, q.Group, q.Group, q.FavoritesOnly, q.IncludeHidden}
}
func validDirectoryQuery(q DirectoryQuery) bool {
	if q.Direction != "" {
		if (q.Direction != "next" && q.Direction != "previous") || q.AnchorChannelID == "" || len(q.AnchorChannelID) > 256 || q.AnchorSourceID == "" || len(q.AnchorSourceID) > 256 || (q.AnchorProvenance != string(LiveSource) && q.AnchorProvenance != string(LibraryChannel)) || q.Offset != 0 || q.Limit != 1 {
			return false
		}
	} else if q.AnchorChannelID != "" || q.AnchorSourceID != "" || q.AnchorProvenance != "" {
		return false
	}
	return (q.Kind == "all" || q.Kind == string(LiveSource) || q.Kind == string(LibraryChannel)) && (q.Sort == "server" || q.Sort == "number" || q.Sort == "name" || q.Sort == "favorites") && q.Offset >= 0 && q.Offset <= MaxSources*MaxChannels+64 && q.Limit >= 1 && q.Limit <= 50 && len(q.SourceID) <= 256 && len(q.Search) <= 120 && len(q.Group) <= 120 && len(q.Revision) <= 64
}

// The revision binds the entire filtered directory, not its current page. A
// changed source, owner channel map, preference or policy cannot mix channel positions across pages.
func (s *Store) directoryRevision(ctx context.Context, tx *sql.Tx, scope DirectoryScope, q DirectoryQuery) (string, error) {
	h := sha256.New()
	q.Offset = 0
	q.Limit = 0
	q.Revision = ""
	raw, _ := json.Marshal(q)
	fmt.Fprintf(h, "%d:%s%d:%s", len(scope.Fence), scope.Fence, len(raw), raw)
	library, _ := json.Marshal(scope.LibraryRows)
	h.Write(library)
	// SaveChannelMap advances this document atomically for every override write,
	// including deletion. A max(updated_ms) over remaining overrides misses clears
	// and same-millisecond changes. Read one revision per authorized source instead.
	rows, err := tx.QueryContext(ctx, `SELECT s.id,s.revision,s.state,s.active_generation,s.name,COALESCE(a.revision,0) FROM live_sources s LEFT JOIN admin_documents a ON a.scope='live-source:'||s.id WHERE s.id IN (SELECT value FROM json_each(?)) ORDER BY s.id`, mustJSON(scope.AllowedLiveSources))
	if err != nil {
		return "", err
	}
	for rows.Next() {
		var id, state, generation, name string
		var revision, ownerRevision int64
		if err = rows.Scan(&id, &revision, &state, &generation, &name, &ownerRevision); err != nil {
			rows.Close()
			return "", err
		}
		fmt.Fprintf(h, "%d:%s:%d:%d:%s:%d:%s:%d:%s:%d", len(id), id, revision, len(state), state, len(generation), generation, len(name), name, ownerRevision)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return "", err
	}
	rows, err = tx.QueryContext(ctx, `SELECT source_id,channel_id,favorite,hidden,revision FROM live_channel_preferences WHERE authority=? AND account_id=? AND profile_id=? ORDER BY source_id,channel_id`, scope.Viewer.Authority, scope.Viewer.AccountID, scope.Viewer.ProfileID)
	if err != nil {
		return "", err
	}
	for rows.Next() {
		var source, channel string
		var favorite, hidden, revision int64
		if err = rows.Scan(&source, &channel, &favorite, &hidden, &revision); err != nil {
			rows.Close()
			return "", err
		}
		fmt.Fprintf(h, "%d:%s:%d:%s:%d:%d:%d", len(source), source, len(channel), channel, favorite, hidden, revision)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return "", err
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}
func mustJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

func (s *Store) directorySnapshot(ctx context.Context, a DirectoryAuthority, work func(*sql.Tx, DirectoryScope) error) error {
	if a == nil {
		return ErrDenied
	}
	gated, err := dbwork.BeginSnapshot(ctx, s.db)
	if err != nil {
		return ErrUnavailable
	}
	defer gated.Rollback()
	tx := gated.Tx()
	scope, err := a(ctx, tx)
	if err != nil {
		return err
	}
	if scope.Fence == "" || len(scope.AllowedLiveSources) > MaxSources || len(scope.LibraryRows) > 64 {
		return ErrDenied
	}
	if err = work(tx, scope); err != nil {
		return err
	}
	current, err := a(ctx, tx)
	if err != nil {
		return err
	}
	if current.Fence != scope.Fence {
		return ErrDenied
	}
	if err = gated.Commit(); err != nil {
		return ErrUnavailable
	}
	return nil
}

func (s *Store) ChannelDirectory(ctx context.Context, a DirectoryAuthority, q DirectoryQuery) (Directory, error) {
	out := Directory{Offset: q.Offset, Limit: q.Limit, Channels: []Channel{}, Sources: []GuideSource{}}
	if !validDirectoryQuery(q) {
		return out, ErrInvalid
	}
	err := s.directorySnapshot(ctx, a, func(tx *sql.Tx, scope DirectoryScope) error {
		if q.IncludeServerHidden && !scope.Owner {
			return ErrDenied
		}
		out.ViewerFence = scope.Fence
		var err error
		out.Revision, err = s.directoryRevision(ctx, tx, scope, q)
		if err != nil {
			return err
		}
		if q.Revision != "" && q.Revision != out.Revision {
			return ErrCursor
		}
		args := directoryArgs(scope, q)
		if len(scope.LibraryRows) > 0 {
			var distinct int
			if err = tx.QueryRowContext(ctx, directoryEligibleSQL+`SELECT count(*),count(DISTINCT channel_id) FROM eligible`, args...).Scan(&out.Total, &distinct); err != nil {
				return err
			}
			if distinct != out.Total {
				return ErrChannelIdentifierConflict
			}
		} else if err = tx.QueryRowContext(ctx, directoryEligibleSQL+`SELECT count(*) FROM eligible`, args...).Scan(&out.Total); err != nil {
			return err
		}
		order := directoryOrder(q.Sort)
		if q.Direction != "" {
			anchor, neighbor, err := directoryNeighborOffset(ctx, tx, scope, q, order)
			if err != nil {
				return err
			}
			out.Offset = anchor
			if neighbor < 0 {
				return nil
			}
			out.Offset = neighbor
		}

		rows, err := tx.QueryContext(ctx, directoryEligibleSQL+`SELECT channel_id,source_id,provenance,name,number,group_name,generation,logo_path,favorite,hidden,preference_revision,server_hidden,source_name,published_at,available_start,available_end,refresh_state FROM eligible ORDER BY `+order+` LIMIT ? OFFSET ?`, append(args, q.Limit, out.Offset)...)
		if err != nil {
			return err
		}
		sources := map[string]bool{}
		for rows.Next() {
			var c Channel
			var src GuideSource
			if err = rows.Scan(&c.ID, &c.SourceID, &c.Provenance, &c.Name, &c.Number, &c.Group, &c.Generation, &c.LogoPath, &c.Favorite, &c.Hidden, &c.PreferenceRevision, &c.ServerHidden, &src.Name, &src.PublishedAt, &src.AvailableStart, &src.AvailableEnd, &src.RefreshState); err != nil {
				rows.Close()
				return err
			}
			c.Programmes = []Programme{}
			c.TuneUnavailableReason = "delivery-unavailable"
			c.RecordUnavailableReason = "recording-unavailable"
			if c.Provenance == LibraryChannel {
				c.RecordUnavailableReason = "not-recordable"
			}
			out.Channels = append(out.Channels, c)
			sourceKey := string(c.Provenance) + "\x00" + c.SourceID
			if !sources[sourceKey] {
				src.ID = c.SourceID
				src.Generation = c.Generation
				src.Provenance = c.Provenance
				out.Sources = append(out.Sources, src)
				sources[sourceKey] = true
			}
		}
		err = rows.Err()
		rows.Close()
		if err == nil {
			err = s.directoryMarks(ctx, tx, scope, out.Channels)
		}
		return err
	})
	return out, err
}

func (s *Store) ChannelSourceSummary(ctx context.Context, a DirectoryAuthority, includeHidden bool, now time.Time, includeServerHidden ...bool) (DirectorySummary, error) {
	out := DirectorySummary{Sources: []SourceSummary{}}
	q := DirectoryQuery{Kind: "all", Sort: "server", Limit: 50, IncludeHidden: includeHidden, IncludeServerHidden: len(includeServerHidden) > 0 && includeServerHidden[0]}
	err := s.directorySnapshot(ctx, a, func(tx *sql.Tx, scope DirectoryScope) error {
		if q.IncludeServerHidden && !scope.Owner {
			return ErrDenied
		}
		out.ViewerFence = scope.Fence
		var err error
		out.Revision, err = s.directoryRevision(ctx, tx, scope, q)
		if err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, directoryEligibleSQL+`SELECT provenance,CASE WHEN provenance='library-channel' THEN 'library' ELSE source_id END,CASE WHEN provenance='library-channel' THEN 'Library Channels' ELSE source_name END,min(generation),min(published_at),min(NULLIF(available_start,'')),max(NULLIF(available_end,'')),CASE WHEN sum(refresh_state IN ('degraded','credentials-required','stale','source-failure'))>0 THEN 'degraded' WHEN sum(refresh_state='refreshing')>0 THEN 'refreshing' ELSE min(refresh_state) END,count(*),sum(favorite),json_group_array(group_name)
  FROM eligible GROUP BY provenance,CASE WHEN provenance='library-channel' THEN 'library' ELSE source_id END ORDER BY CASE provenance WHEN 'live-source' THEN 0 ELSE 1 END,source_name,source_id`, directoryArgs(scope, q)...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var v SourceSummary
			var groups string
			var start, end sql.NullString
			if err = rows.Scan(&v.Provenance, &v.ID, &v.Name, &v.Generation, &v.PublishedAt, &start, &end, &v.RefreshState, &v.ChannelCount, &v.FavoriteCount, &groups); err != nil {
				rows.Close()
				return err
			}
			v.AvailableStart = start.String
			v.AvailableEnd = end.String
			v.Position = len(out.Sources)
			v.Groups = []string{}
			v.GroupCounts = []GuideGroupCount{}
			v.UnavailableReasons = map[string]int{}
			counts := map[string]int{}
			var names []string
			if err = json.Unmarshal([]byte(groups), &names); err != nil {
				rows.Close()
				return err
			}
			for _, name := range names {
				if name != "" {
					counts[name]++
				}
			}
			for name, count := range counts {
				v.GroupCounts = append(v.GroupCounts, GuideGroupCount{name, count})
			}
			sortSummaryGroups(v.GroupCounts)
			for _, g := range v.GroupCounts {
				v.Groups = append(v.Groups, g.Name)
			}
			v.GuideDays = GuideDays([]GuideSource{v.GuideSource}, now)
			out.Sources = append(out.Sources, v)
		}
		err = rows.Err()
		rows.Close()
		return err
	})
	return out, err
}
func sortSummaryGroups(groups []GuideGroupCount) {
	c := directoryCollators.Get().(*collate.Collator)
	defer directoryCollators.Put(c)
	sort.Slice(groups, func(i, j int) bool { return c.CompareString(groups[i].Name, groups[j].Name) < 0 })
}

func directoryOrder(sort string) string {
	order := `CASE provenance WHEN 'live-source' THEN 0 ELSE 1 END,source_name,source_id,channel_position,channel_id`
	numeric := `(portico_channel_number(number) IS NULL),portico_channel_number(number),name COLLATE portico_guide_name,channel_id`
	if sort == "favorites" {
		return `favorite DESC,` + numeric + `,source_id,provenance`
	}
	if sort == "number" {
		return `CASE provenance WHEN 'live-source' THEN 0 ELSE 1 END,CASE WHEN provenance='live-source' THEN source_name ELSE '' END,CASE WHEN provenance='live-source' THEN source_id ELSE '' END,` + numeric
	}
	if sort == "name" {
		return `name COLLATE portico_guide_name,` + numeric
	}
	return order
}

// The anchor is found before excluding unavailable rows. A channel that lost
// tune availability still steps to its actual neighbor in the current view.
// Only metadata is ranked; no full lineup is allocated or sent to the client.
func directoryNeighborOffset(ctx context.Context, tx *sql.Tx, scope DirectoryScope, q DirectoryQuery, order string) (int, int, error) {
	ranked := directoryEligibleSQL + `, ranked AS (SELECT channel_id,source_id,provenance,generation,row_number() OVER (ORDER BY ` + order + `)-1 ordinal FROM eligible) `
	args := directoryArgs(scope, q)
	anchor := -1
	err := tx.QueryRowContext(ctx, ranked+`SELECT ordinal FROM ranked WHERE channel_id=? AND source_id=? AND provenance=?`, append(args, q.AnchorChannelID, q.AnchorSourceID, q.AnchorProvenance)...).Scan(&anchor)
	if err == sql.ErrNoRows {
		return -1, -1, ErrDirectoryAnchorNotFound
	}
	if err != nil {
		return -1, -1, err
	}
	if !q.DeliveryAvailable {
		return anchor, -1, nil
	}
	comparison, sortDirection := ">", "ASC"
	if q.Direction == "previous" {
		comparison, sortDirection = "<", "DESC"
	}
	neighbor := -1
	err = tx.QueryRowContext(ctx, ranked+`SELECT ordinal FROM ranked WHERE generation<>'' AND NOT(channel_id=? AND source_id=? AND provenance=?) ORDER BY CASE WHEN ordinal`+comparison+`? THEN 0 ELSE 1 END,ordinal `+sortDirection+` LIMIT 1`, append(args, q.AnchorChannelID, q.AnchorSourceID, q.AnchorProvenance, anchor)...).Scan(&neighbor)
	if err == sql.ErrNoRows {
		return anchor, -1, nil
	}
	return anchor, neighbor, err
}

// GuidePage pins the directory and observation time across horizontal paging.
type GuidePage struct {
	Directory
	ObservedAt string `json:"observedAt"`
	ExpiresAt  string `json:"expiresAt"`
	NextCursor string `json:"nextCursor"`
}
type GuidePageQuery struct {
	Directory DirectoryQuery
	Cursor    string
	At        time.Time
	OnNow     bool
}
type GuideReader func(context.Context, GuideQuery) (Guide, error)
type guidePageCursor struct {
	Revision, Fence, Binding string
	Offset                   int
	At, Until                int64
}

func (s *Store) signGuideValue(v any, domain string) string {
	raw, _ := json.Marshal(v)
	mac := hmac.New(sha256.New, s.ContinuationKey(domain))
	mac.Write(raw)
	return base64.RawURLEncoding.EncodeToString(raw) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
func (s *Store) readGuideValue(raw, domain string, v any) error {
	if len(raw) > 4096 {
		return ErrCursor
	}
	payload, signature, ok := strings.Cut(raw, ".")
	if !ok {
		return ErrCursor
	}
	data, e := base64.RawURLEncoding.DecodeString(payload)
	if e != nil {
		return ErrCursor
	}
	sig, e := base64.RawURLEncoding.DecodeString(signature)
	if e != nil {
		return ErrCursor
	}
	mac := hmac.New(sha256.New, s.ContinuationKey(domain))
	mac.Write(data)
	if !hmac.Equal(sig, mac.Sum(nil)) || json.Unmarshal(data, v) != nil {
		return ErrCursor
	}
	return nil
}
func (s *Store) NowNextPage(ctx context.Context, a DirectoryAuthority, read GuideReader, q GuidePageQuery) (GuidePage, error) {
	out := GuidePage{}
	if read == nil || q.Directory.Offset != 0 {
		return out, ErrInvalid
	}
	if q.At.IsZero() {
		q.At = time.Now()
	}
	now := q.At.UTC().Truncate(time.Second)
	query := q.Directory
	query.Sort = "favorites"
	query.Revision = ""
	b := query
	b.Limit = query.Limit
	binding := mustJSON(struct {
		Q     DirectoryQuery
		OnNow bool
	}{b, q.OnNow})
	c := guidePageCursor{Binding: binding, At: now.Unix(), Until: now.Add(time.Minute).Unix()}
	if q.Cursor != "" {
		if e := s.readGuideValue(q.Cursor, "guide-now-next", &c); e != nil {
			return out, e
		}
		if c.Binding != binding || c.Offset < 0 || now.Unix() >= c.Until {
			return out, ErrCursor
		}
		query.Revision = c.Revision
		query.Offset = c.Offset
	}
	directory, e := s.ChannelDirectory(ctx, a, query)
	if e != nil {
		return out, e
	}
	if c.Fence != "" && c.Fence != directory.ViewerFence {
		return out, ErrCursor
	}
	out.Directory = directory
	at := time.Unix(c.At, 0).UTC()
	for _, kind := range []Provenance{LiveSource, LibraryChannel} {
		// Split by source as channel identifiers can overlap between live providers.
		sources := map[string][]string{}
		for _, ch := range directory.Channels {
			if ch.Provenance == kind {
				sources[ch.SourceID] = append(sources[ch.SourceID], ch.ID)
			}
		}
		for source, ids := range sources {
			end := at.Add(14 * 24 * time.Hour)
			if q.OnNow {
				end = at.Add(time.Second)
			}
			guide, err := read(ctx, GuideQuery{Viewer: Owner{}, Start: at, End: end, Timezone: "UTC", Kind: kind, SourceID: source, ChannelIDs: ids, Limit: 50, Compact: !q.OnNow})
			if err != nil {
				return GuidePage{}, err
			}
			for i := range out.Channels {
				ch := &out.Channels[i]
				if ch.SourceID != source || ch.Provenance != kind {
					continue
				}
				for _, gch := range guide.Channels {
					if gch.ID != ch.ID {
						continue
					}
					for _, p := range gch.Programmes {
						start, _ := time.Parse(time.RFC3339Nano, p.Start)
						end, _ := time.Parse(time.RFC3339Nano, p.End)
						if q.OnNow && (start.After(at) || !end.After(at)) {
							continue
						}
						ch.Programmes = append(ch.Programmes, p)
						if end.Unix() > c.At && end.Unix() < c.Until {
							c.Until = end.Unix()
						}
					}
				}
			}
		}
	}
	query.Offset = 0
	query.Limit = 1
	query.Revision = directory.Revision
	if _, e = s.ChannelDirectory(ctx, a, query); e != nil {
		return GuidePage{}, e
	}
	out.ObservedAt = at.Format(time.RFC3339)
	out.ExpiresAt = time.Unix(c.Until, 0).UTC().Format(time.RFC3339)
	if directory.Offset+len(out.Channels) < directory.Total {
		c.Offset = directory.Offset + len(out.Channels)
		c.Revision = directory.Revision
		c.Fence = directory.ViewerFence
		out.NextCursor = s.signGuideValue(c, "guide-now-next")
	}
	return out, nil
}

// AiringProjection runs under the same snapshot and must remove inaccessible
// library entries, applying current item policy before any search result leaves it.
type Airing struct {
	Channel   Channel   `json:"channel"`
	Programme Programme `json:"programme"`
}
type AiringGroup struct {
	Key     string   `json:"key"`
	Title   string   `json:"title"`
	Airings []Airing `json:"airings"`
}
type AiringsPage struct {
	Groups      []AiringGroup `json:"groups"`
	NextCursor  string        `json:"nextCursor"`
	Revision    string        `json:"revision"`
	ViewerFence string        `json:"viewerFence"`
	Start       string        `json:"start"`
	End         string        `json:"end"`
	Timezone    string        `json:"timezone"`
	Today       string        `json:"today"`
}
type AiringsQuery struct {
	Search, SeriesID, Title, ChannelID, SourceID, Kind, Cursor string
	Start, End                                                 time.Time
	Limit                                                      int
	ProgrammeAllowed                                           func(string) bool
}
type AiringProjection func(context.Context, *sql.Tx, []Airing) ([]Airing, error)
type airingsCursor struct {
	Binding, Revision, Fence, After, AfterKey string
	Expires                                   int64
}

// The eligible directory is shared with normal guide paging. Search never uses
// a source's existence as permission, and never projects a provider locator.
const airingCandidatesSQL = `, airings AS (
 SELECT e.channel_id,e.source_id,e.provenance,e.name,e.number,e.group_name,e.generation,p.id,p.title,p.start_utc,p.end_utc,p.lineage,COALESCE(m.series_id,'') series_id,COALESCE(m.episode_id,'') episode_id,COALESCE(m.new_evidence,'unknown') new_evidence,COALESCE(m.description,'') description,COALESCE(m.facts,'{}') facts,
 COALESCE((SELECT '/v1/guide/programme-images/'||i.generation_id||'/'||i.programme_id||'?sd='||img.small_width||'x'||img.small_height||'.'||img.small_bytes||'.'||substr(img.small_digest,1,16)||'&ld='||img.large_width||'x'||img.large_height||'.'||img.large_bytes||'.'||substr(img.large_digest,1,16) FROM live_programme_icons i JOIN live_programme_images img ON img.url=i.url WHERE i.generation_id=p.generation_id AND i.programme_id=p.id),'') image
 FROM eligible e JOIN live_programmes p ON e.provenance='live-source' AND p.generation_id=e.generation AND p.channel_id=e.channel_id LEFT JOIN live_programme_metadata m ON m.generation_id=p.generation_id AND m.id=p.id
 UNION ALL
 SELECT e.channel_id,e.source_id,e.provenance,e.name,e.number,e.group_name,e.generation,l.occurrence_id,l.title,strftime('%Y-%m-%dT%H:%M:%SZ',l.start_ms/1000,'unixepoch'),strftime('%Y-%m-%dT%H:%M:%SZ',l.end_ms/1000,'unixepoch'),'generated','','','unknown','',json_object('subtitle',COALESCE((SELECT title FROM catalog_entities WHERE id=l.item_id),'')),''
 FROM eligible e JOIN lc_entries l ON e.provenance='library-channel' AND l.generation_id=e.generation AND l.channel_id=e.channel_id
 ) `

func (s *Store) Airings(ctx context.Context, a DirectoryAuthority, project AiringProjection, q AiringsQuery) (AiringsPage, error) {
	out := AiringsPage{Groups: []AiringGroup{}, Timezone: time.Local.String(), Today: time.Now().In(time.Local).Format("2006-01-02")}
	if q.Start.IsZero() || !q.End.After(q.Start) || q.End.Sub(q.Start) > 14*24*time.Hour || q.Limit < 1 || q.Limit > 50 || len(q.Search) > 120 || len(q.SeriesID) > 512 || len(q.Title) > 512 || len(q.ChannelID) > 256 {
		return out, ErrInvalid
	}
	out.Start = q.Start.UTC().Format(time.RFC3339)
	out.End = q.End.UTC().Format(time.RFC3339)
	dq := DirectoryQuery{Kind: q.Kind, SourceID: q.SourceID, Sort: "server", Limit: 50}
	if dq.Kind == "" {
		dq.Kind = "all"
	}
	if !validDirectoryQuery(dq) {
		return out, ErrInvalid
	}
	binding := mustJSON([]any{out.Start, out.End, q.Search, q.SeriesID, q.Title, q.ChannelID, q.SourceID, dq.Kind, q.Limit})
	c := airingsCursor{Binding: binding, Expires: time.Now().Add(15 * time.Minute).Unix()}
	if q.Cursor != "" {
		if e := s.readGuideValue(q.Cursor, "guide-airings", &c); e != nil {
			return out, e
		}
		if c.Binding != binding || c.Expires <= time.Now().Unix() {
			return out, ErrCursor
		}
	}
	err := s.directorySnapshot(ctx, a, func(tx *sql.Tx, scope DirectoryScope) error {
		revision, e := s.directoryRevision(ctx, tx, scope, dq)
		if e != nil {
			return e
		}
		if q.Cursor != "" && (revision != c.Revision || scope.Fence != c.Fence) {
			return ErrCursor
		}
		out.Revision = revision
		out.ViewerFence = scope.Fence
		args := directoryArgs(scope, dq)
		args = append(args, out.End, out.Start, q.Search, q.Search, q.SeriesID, q.SeriesID, q.Title, q.Title, q.ChannelID, q.ChannelID, c.After, c.After, c.AfterKey)
		rows, e := tx.QueryContext(ctx, directoryEligibleSQL+airingCandidatesSQL+`SELECT channel_id,source_id,provenance,name,number,group_name,generation,id,title,start_utc,end_utc,lineage,series_id,episode_id,new_evidence,description,facts,image FROM airings WHERE start_utc<? AND end_utc>? AND (?='' OR instr(lower(title||' '||COALESCE(json_extract(facts,'$.subtitle'),'')),lower(?))>0) AND (?='' OR series_id=?) AND (?='' OR lower(trim(title))=lower(trim(?))) AND (?='' OR channel_id=?) AND (start_utc>? OR(start_utc=? AND provenance||':'||source_id||':'||id>?)) ORDER BY start_utc,provenance||':'||source_id||':'||id LIMIT 257`, args...)
		if e != nil {
			return e
		}
		batch := []Airing{}
		facts := []string{}
		for rows.Next() {
			var v Airing
			var f string
			p := &v.Programme
			ch := &v.Channel
			if e = rows.Scan(&ch.ID, &ch.SourceID, &ch.Provenance, &ch.Name, &ch.Number, &ch.Group, &ch.Generation, &p.ID, &p.Title, &p.Start, &p.End, &p.Lineage, &p.SeriesID, &p.EpisodeID, &p.NewEvidence, &p.Description, &f, &p.Image); e != nil {
				rows.Close()
				return e
			}
			p.ChannelID = ch.ID
			p.ApplyFacts(f)
			ch.Programmes = []Programme{}
			batch = append(batch, v)
			facts = append(facts, f)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		count := 0
		groups := map[string]int{}
		for i, v := range batch {
			if i == 256 {
				out.NextCursor = s.signGuideValue(c, "guide-airings")
				break
			}
			visible := []Airing{v}
			if v.Channel.Provenance == LibraryChannel {
				if project == nil {
					visible = nil
				} else {
					visible, e = project(ctx, tx, visible)
					if e != nil {
						return e
					}
				}
			}
			rating := ""
			if v.Programme.Rating != nil {
				rating = v.Programme.Rating.Value
			}
			if q.ProgrammeAllowed != nil && !q.ProgrammeAllowed(rating) {
				visible = nil
			}
			if len(visible) > 0 && count == q.Limit {
				out.NextCursor = s.signGuideValue(c, "guide-airings")
				break
			}
			c.After = v.Programme.Start
			c.AfterKey = string(v.Channel.Provenance) + ":" + v.Channel.SourceID + ":" + v.Programme.ID
			c.Revision = revision
			c.Fence = scope.Fence
			if len(visible) == 0 {
				continue
			}
			v = visible[0]
			key := v.Programme.SeriesID
			if key == "" {
				key = "title:" + strings.ToLower(strings.TrimSpace(v.Programme.Title))
			}
			g, ok := groups[key]
			if !ok {
				g = len(out.Groups)
				groups[key] = g
				out.Groups = append(out.Groups, AiringGroup{Key: key, Title: v.Programme.Title, Airings: []Airing{}})
			}
			out.Groups[g].Airings = append(out.Groups[g].Airings, v)
			count++
		}
		return nil
	})
	if err != nil {
		return AiringsPage{}, err
	}
	return out, nil
}

type Reminder struct {
	ID       string `json:"id"`
	Revision int64  `json:"revision"`
	Airing   Airing `json:"airing"`
	Due      bool   `json:"due"`
}
type ReminderInput struct {
	SourceID         string     `json:"sourceId"`
	ChannelID        string     `json:"channelId"`
	Generation       string     `json:"generation"`
	ProgrammeID      string     `json:"programmeId"`
	Kind             Provenance `json:"kind"`
	ExpectedRevision int64      `json:"expectedRevision"`
}
type ReminderPage struct {
	Items      []Reminder `json:"items"`
	NextCursor string     `json:"nextCursor"`
	Revision   string     `json:"revision"`
}

func scanAiring(rows interface{ Scan(...any) error }) (Airing, error) {
	var v Airing
	var f string
	p := &v.Programme
	ch := &v.Channel
	e := rows.Scan(&ch.ID, &ch.SourceID, &ch.Provenance, &ch.Name, &ch.Number, &ch.Group, &ch.Generation, &p.ID, &p.Title, &p.Start, &p.End, &p.Lineage, &p.SeriesID, &p.EpisodeID, &p.NewEvidence, &p.Description, &f, &p.Image)
	p.ChannelID = ch.ID
	p.ApplyFacts(f)
	ch.Programmes = []Programme{}
	return v, e
}

const airingColumns = `channel_id,source_id,provenance,name,number,group_name,generation,id,title,start_utc,end_utc,lineage,series_id,episode_id,new_evidence,description,facts,image`

func resolveReminderTx(ctx context.Context, tx *sql.Tx, scope DirectoryScope, project AiringProjection, in ReminderInput, allowed func(string) bool) (Airing, error) {
	q := DirectoryQuery{Kind: string(in.Kind), SourceID: in.SourceID, Sort: "server", Limit: 50}
	args := directoryArgs(scope, q)
	args = append(args, in.ChannelID, in.Generation, in.ProgrammeID)
	v, e := scanAiring(tx.QueryRowContext(ctx, directoryEligibleSQL+airingCandidatesSQL+`SELECT `+airingColumns+` FROM airings WHERE channel_id=? AND generation=? AND id=?`, args...))
	if errors.Is(e, sql.ErrNoRows) {
		return v, ErrDenied
	}
	if e != nil {
		return v, e
	}
	if in.Kind == LibraryChannel {
		if project == nil {
			return Airing{}, ErrDenied
		}
		values, e := project(ctx, tx, []Airing{v})
		if e != nil {
			return Airing{}, e
		}
		if len(values) != 1 {
			return Airing{}, ErrDenied
		}
		v = values[0]
	}
	rating := ""
	if v.Programme.Rating != nil {
		rating = v.Programme.Rating.Value
	}
	if allowed != nil && !allowed(rating) {
		return Airing{}, ErrDenied
	}
	return v, nil
}
func (s *Store) SaveReminder(ctx context.Context, a DirectoryAuthority, project AiringProjection, in ReminderInput, allowed func(string) bool) (Reminder, error) {
	out := Reminder{}
	if in.ExpectedRevision < 0 || !opaque(in.Generation) || !opaque(in.SourceID) && in.Kind != LibraryChannel || (in.ChannelID == "" || len(in.ChannelID) > 256) || (in.ProgrammeID == "" || len(in.ProgrammeID) > 256) || (in.Kind != LiveSource && in.Kind != LibraryChannel) {
		return out, ErrInvalid
	}
	// A write transaction supplies the same directory authority as every read.
	e := dbwork.WithWriteTxContext(ctx, s.db, dbwork.ClassSecurityFence, func(ctx context.Context, tx *sql.Tx) error {
		scope, e := a(ctx, tx)
		if e != nil {
			return e
		}
		if !scope.Viewer.Valid() || scope.Fence == "" {
			return ErrDenied
		}
		v, e := resolveReminderTx(ctx, tx, scope, project, in, allowed)
		if e != nil {
			return e
		}
		start, _ := time.Parse(time.RFC3339Nano, v.Programme.Start)
		if !start.After(time.Now()) {
			return ErrConflict
		}
		key := id("reminder", scope.Viewer.Key(), string(in.Kind), in.SourceID, in.ChannelID, in.Generation, in.ProgrammeID)
		if in.ExpectedRevision == 0 {
			_, e = tx.ExecContext(ctx, `INSERT INTO live_reminders(id,owner_key,kind,source_id,channel_id,generation_id,programme_id,revision,start_utc) VALUES(?,?,?,?,?,?,?,1,?) ON CONFLICT(id) DO NOTHING`, key, scope.Viewer.Key(), in.Kind, in.SourceID, in.ChannelID, in.Generation, in.ProgrammeID, v.Programme.Start)
		} else {
			result, err := tx.ExecContext(ctx, `UPDATE live_reminders SET revision=revision+1 WHERE id=? AND owner_key=? AND revision=?`, key, scope.Viewer.Key(), in.ExpectedRevision)
			e = err
			if e == nil {
				n, _ := result.RowsAffected()
				if n != 1 {
					return ErrConflict
				}
			}
		}
		if e != nil {
			return e
		}
		if e = tx.QueryRowContext(ctx, `SELECT revision FROM live_reminders WHERE id=?`, key).Scan(&out.Revision); e != nil {
			return e
		}
		out.ID = key
		out.Airing = v
		after, e := a(ctx, tx)
		if e != nil {
			return e
		}
		if after.Fence != scope.Fence {
			return ErrDenied
		}
		return nil
	})
	return out, e
}
func (s *Store) DeleteReminder(ctx context.Context, a DirectoryAuthority, key string, revision int64) error {
	if len(key) != 64 || revision < 1 {
		return ErrInvalid
	}
	return dbwork.WithWriteTxContext(ctx, s.db, dbwork.ClassSecurityFence, func(ctx context.Context, tx *sql.Tx) error {
		scope, e := a(ctx, tx)
		if e != nil {
			return e
		}
		if !scope.Viewer.Valid() || scope.Fence == "" {
			return ErrDenied
		}
		result, e := tx.ExecContext(ctx, `DELETE FROM live_reminders WHERE id=? AND owner_key=? AND revision=?`, key, scope.Viewer.Key(), revision)
		if e != nil {
			return e
		}
		n, _ := result.RowsAffected()
		if n != 1 {
			return ErrConflict
		}
		return nil
	})
}
func (s *Store) Reminders(ctx context.Context, a DirectoryAuthority, project AiringProjection, cursor string, limit int, allowed func(string) bool) (ReminderPage, error) {
	out := ReminderPage{Items: []Reminder{}}
	if limit < 1 || limit > 50 {
		return out, ErrInvalid
	}
	e := s.directorySnapshot(ctx, a, func(tx *sql.Tx, scope DirectoryScope) error {
		if !scope.Viewer.Valid() {
			return ErrDenied
		}
		q := DirectoryQuery{Kind: "all", Sort: "server", Limit: 50}
		revision, e := s.directoryRevision(ctx, tx, scope, q)
		if e != nil {
			return e
		}
		var state string
		if e = tx.QueryRowContext(ctx, `SELECT COALESCE(group_concat(id||':'||revision,'|'),'') FROM(SELECT id,revision FROM live_reminders WHERE owner_key=? ORDER BY id)`, scope.Viewer.Key()).Scan(&state); e != nil {
			return e
		}
		revision = id(revision, scope.Viewer.Key(), state)
		out.Revision = revision
		c := airingsCursor{Binding: fmt.Sprint(limit), Revision: revision, Fence: scope.Fence, Expires: time.Now().Add(15 * time.Minute).Unix()}
		if cursor != "" {
			if e = s.readGuideValue(cursor, "guide-reminders", &c); e != nil {
				return e
			}
			if c.Binding != fmt.Sprint(limit) || c.Revision != revision || c.Fence != scope.Fence || c.Expires <= time.Now().Unix() {
				return ErrCursor
			}
		}
		rows, e := tx.QueryContext(ctx, `SELECT id,revision,kind,source_id,channel_id,generation_id,programme_id FROM live_reminders WHERE owner_key=? AND id>? ORDER BY id LIMIT 257`, scope.Viewer.Key(), c.AfterKey)
		if e != nil {
			return e
		}
		type saved struct {
			key   string
			rev   int64
			input ReminderInput
		}
		values := []saved{}
		for rows.Next() {
			var v saved
			if e = rows.Scan(&v.key, &v.rev, &v.input.Kind, &v.input.SourceID, &v.input.ChannelID, &v.input.Generation, &v.input.ProgrammeID); e != nil {
				rows.Close()
				return e
			}
			values = append(values, v)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		for i, v := range values {
			if i == 256 || len(out.Items) == limit {
				out.NextCursor = s.signGuideValue(c, "guide-reminders")
				break
			}
			c.AfterKey = v.key
			airing, e := resolveReminderTx(ctx, tx, scope, project, v.input, allowed)
			if errors.Is(e, ErrDenied) {
				continue
			}
			if e != nil {
				return e
			}
			end, _ := time.Parse(time.RFC3339Nano, airing.Programme.End)
			if !end.After(time.Now()) {
				continue
			}
			start, _ := time.Parse(time.RFC3339Nano, airing.Programme.Start)
			out.Items = append(out.Items, Reminder{ID: v.key, Revision: v.rev, Airing: airing, Due: !start.After(time.Now())})
		}
		return nil
	})
	if e != nil {
		return ReminderPage{}, e
	}
	return out, nil
}
