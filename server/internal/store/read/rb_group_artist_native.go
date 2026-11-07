package read

import (
	"context"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"time"
)

type RBArtistFeed uint8

const (
	RBArtistPrimary RBArtistFeed = iota
	RBArtistCredits
)

type RBArtistInput struct {
	View   BrowseInput
	Artist int64
	Feed   RBArtistFeed
	After  *RBArtistCursor
}
type RBArtistCursor struct {
	fence      browseFence
	artist, id int64
	feed       RBArtistFeed
}
type RBArtistReleaseCandidate struct {
	Member                     BrowseItem
	Album, Year, PrimaryArtist int64
	AlbumKey                   string
}

// Candidates are not a public release order or count. Root must merge/deduplicate
// primary and credited feeds and resolve accepted provider release-type facts.
type RBArtistCandidates struct {
	Items                 []RBArtistReleaseCandidate
	Next                  *RBArtistCursor
	Exhausted             bool
	RawRows, RawPositions int
	fence                 browseFence
}

func rbArtistPolicy(feed RBArtistFeed) readPolicyID { return 32420 + readPolicyID(feed) }
func init() {
	for _, feed := range []RBArtistFeed{RBArtistPrimary, RBArtistCredits} {
		feed := feed
		kind := int64(6)
		if feed == RBArtistCredits {
			kind = 7
		}
		registerReadPolicy(readPolicyRecipe{id: rbArtistPolicy(feed), domain: fmt.Sprint("rb-artist-source-", feed), kinds: []int64{kind}, validate: func(q browseQuery, in BrowseInput, f browseFence) (string, error) {
			if f.maximumAge >= 0 || f.allowUnrated == 0 {
				return "", ErrUnqualified
			}
			rows, e := q("rb.group.artist-native.subject", in.policy.subject)
			if e != nil {
				return "", e
			}
			if len(rows) != 1 || rows[0][1] != in.Library || rows[0][2] != int64(5) || rows[0][3] != int64(0) {
				return "", ErrDenied
			}
			return fmt.Sprint(rows[0]), nil
		}})
	}
}
func (r *BrowseReader) RBArtistSource(ctx context.Context, req governor.Request, budget time.Duration, out BrowseOutput, in RBArtistInput) (RBArtistCandidates, BrowseReadResult, error) {
	if r == nil {
		return RBArtistCandidates{}, BrowseReadResult{}, ErrInput
	}
	return liveRead(ctx, req, budget, out, r.pool, in.View.Now, func(ctx context.Context, req governor.Request, budget time.Duration, at time.Time) (RBArtistCandidates, BrowseReadResult, error) {
		in.View.Now = at
		return r.rbArtistSource(ctx, req, budget, out, in)
	})
}
func (r *BrowseReader) rbArtistSource(ctx context.Context, req governor.Request, budget time.Duration, out BrowseOutput, in RBArtistInput) (RBArtistCandidates, BrowseReadResult, error) {
	kind := int64(6)
	if in.Feed == RBArtistCredits {
		kind = 7
	}
	if in.Artist <= 0 || in.Feed > RBArtistCredits || in.View.Kind != kind || in.View.After != nil || in.View.Descending {
		return RBArtistCandidates{}, BrowseReadResult{}, ErrInput
	}
	if in.After != nil && (in.After.artist != in.Artist || in.After.feed != in.Feed) {
		return RBArtistCandidates{}, BrowseReadResult{}, ErrStale
	}
	in.View.policy = readPolicyBinding{recipe: rbArtistPolicy(in.Feed), subject: in.Artist}
	return publishRead(ctx, r.snapshots, req, budget, out, in.View, []readStep[RBArtistCandidates]{func(ctx context.Context, q browseQuery, f browseFence, d *RBArtistCandidates) error {
		if in.After != nil && in.After.fence != f {
			return ErrStale
		}
		after := int64(0)
		if in.After != nil {
			after = in.After.id
		}
		name := "rb.group.artist-native.primary."
		if in.Feed == RBArtistCredits {
			name = "rb.group.artist-native.credits."
		}
		if in.View.KeepArticles {
			name += "keep"
		} else {
			name += "ignore"
		}
		rows, e := q(name, in.Artist, after, int64(MaxRaw))
		if e != nil {
			return e
		}
		d.fence = f
		d.RawRows = len(rows)
		bytes := 2048 + 2*len(fmt.Sprint(f))
		if bytes > 256<<10 {
			return ErrNotReady
		}
		for _, v := range rows {
			if e := ctx.Err(); e != nil {
				return e
			}
			d.RawPositions++
			d.Next = &RBArtistCursor{f, in.Artist, v[0].(int64), in.Feed}
			if v[7] != in.View.Library || v[8] != kind || !admittedItem(v, f) || v[9] == nil || v[13] != int64(0) || v[14] != in.View.Library || v[15] != int64(6) {
				continue
			}
			item, e := itemValue(v)
			if e != nil {
				return e
			}
			key := v[11].(string)
			bytes += 256 + 2*len(item.Title) + 2*len(key)
			if bytes > 256<<10 {
				return ErrNotReady
			}
			d.Items = append(d.Items, RBArtistReleaseCandidate{item, v[9].(int64), v[10].(int64), v[12].(int64), key})
			if len(d.Items) == in.View.Limit {
				break
			}
		}
		d.Exhausted = d.RawPositions == len(rows) && len(rows) < MaxRaw
		if d.Exhausted {
			d.Next = nil
		}
		return nil
	}})
}
func RBArtistDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	var defs []stmt.Definition
	// Separate purpose, no frozen registry or golden edits.
	for _, d := range RBGroupDefinitions() {
		if d.ID == statementID("rb.group.subject") {
			d.ID = statementID("rb.group.artist-native.subject")
			d.CallSites = []string{"store/read/rb_group_artist_native.go:BrowseReader.RBArtistSource"}
			d.Cohorts[0].Bindings[0] = int64(2003)
			defs = append(defs, d)
		}
	}
	for _, feed := range []RBArtistFeed{RBArtistPrimary, RBArtistCredits} {
		for _, keep := range []bool{false, true} {
			key, variant := "sort_key", "ignore"
			if keep {
				key, variant = "sort_key_keep_articles", "keep"
			}
			name := "primary." + variant
			index := "catalog_entities_artist"
			scope := "artist_id IS NOT NULL AND e.artist_id=? AND e.id>?"
			from := "catalog_entities AS e INDEXED BY " + index
			album := "e.id,e.year,e." + key + ",e.artist_id,e.retired,e.library_id,e.kind"
			pk := []string(nil)
			if feed == RBArtistCredits {
				name = "credits." + variant
				index = "catalog_song_artists_artist"
				scope = "m.artist_id=? AND m.song_id>?"
				from = "catalog_song_artists AS m INDEXED BY " + index + " CROSS JOIN catalog_entities AS e ON e.id=m.song_id LEFT JOIN catalog_entities AS a ON a.id=e.album_id"
				album = "a.id,a.year,a." + key + ",a.artist_id,a.retired,a.library_id,a.kind"
				pk = []string{"e", "a"}
			}
			sql := `SELECT e.id,'',e.public_id,e.title,e.retired,e.revision,e.admission_own_age,e.library_id,e.kind,` + album + " FROM " + from + " WHERE " + scope + " ORDER BY "
			if feed == RBArtistCredits {
				sql += "m.song_id"
			} else {
				sql += "e.id"
			}
			sql += " LIMIT ?"
			defs = append(defs, stmt.Definition{ID: statementID("rb.group.artist-native." + name), SQL: sql, Owner: "store/read", CallSites: []string{"store/read/rb_group_artist_native.go:BrowseReader.RBArtistSource"}, Kind: stmt.Read, Params: []stmt.Param{i("artist"), i("after"), i("limit")}, Cohorts: []stmt.Cohort{{Name: "present", Bindings: []any{int64(2003), int64(0), int64(64)}}, {Name: "empty", Bindings: []any{int64(-1), int64(0), int64(64)}}}, Plan: stmt.Plan{Hot: true, Indexes: []string{index}, PrimaryKeys: pk}})
		}
	}
	return defs
}
