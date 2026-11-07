package read

import (
	"context"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"sort"
	"strings"
	"time"
)

// These are explicit browse groupOrder scopes, NOT show-workspace settings or
// defaults. The public groupOrder contract is numeric season (specials last),
// episode number, entity ID; a direct season is episode number, entity ID.
// Workspace episode pages instead tie on public ID and require separate parity.
type RBTVScope uint8

const (
	RBTVShow RBTVScope = iota + 1
	RBTVSeason
)

type RBTVGroup uint8

const (
	RBTVAll RBTVGroup = iota
	RBTVSpecials
	RBTVUnassignedAbsolute
)

type RBTVInput struct {
	View    BrowseInput
	Subject int64
	Scope   RBTVScope
	Group   RBTVGroup
}
type RBTVEpisode struct {
	BrowseItem
	Number, SeasonNumber int64
	Numbering            string
	seasonID             int64
}
type RBTVResult struct {
	Items   []RBTVEpisode
	RawRows int
	fence   browseFence
}
type rbTVWork struct {
	ids                []int64
	seen               map[int64]bool
	items              []RBTVEpisode
	keys               []any
	raw                int
	done               bool
	show, seasonNumber int64
	fence              browseFence
	bytes              int
}

func rbTVPolicy(scope RBTVScope) readPolicyID { return 32410 + readPolicyID(scope) }
func init() {
	for _, scope := range []RBTVScope{RBTVShow, RBTVSeason} {
		scope := scope
		registerReadPolicy(readPolicyRecipe{id: rbTVPolicy(scope), domain: fmt.Sprint("rb-tv-explicit-group-", scope), kinds: []int64{4}, validate: func(q browseQuery, in BrowseInput, f browseFence) (string, error) {
			if f.maximumAge >= 0 || f.allowUnrated == 0 {
				return "", ErrUnqualified
			}
			row, e := rbTVRoot(q, in.policy.subject, scope, in.Library)
			if e != nil {
				return "", e
			}
			return fmt.Sprint(row), nil
		}})
	}
}
func rbTVRoot(q browseQuery, id int64, scope RBTVScope, library int64) ([]any, error) {
	name := "rb.group.tv.show"
	kind := int64(2)
	if scope == RBTVSeason {
		name = "rb.group.tv.season"
		kind = 3
	}
	rows, e := q(name, id)
	if e != nil {
		return nil, e
	}
	if len(rows) != 1 {
		return nil, ErrDenied
	}
	r := rows[0]
	if r[1] != library || r[2] != kind || r[3] != int64(0) || r[13] != library || r[14] != int64(2) || r[15] != int64(0) {
		return nil, ErrDenied
	}
	return r, nil
}
func (r *BrowseReader) RBTVGroupExact(ctx context.Context, req governor.Request, budget time.Duration, out BrowseOutput, in RBTVInput) (RBTVResult, BrowseReadResult, error) {
	if r == nil {
		return RBTVResult{}, BrowseReadResult{}, ErrInput
	}
	return liveRead(ctx, req, budget, out, r.pool, in.View.Now, func(ctx context.Context, req governor.Request, budget time.Duration, at time.Time) (RBTVResult, BrowseReadResult, error) {
		in.View.Now = at
		return r.rbTVGroupExact(ctx, req, budget, out, in)
	})
}
func (r *BrowseReader) rbTVGroupExact(ctx context.Context, req governor.Request, budget time.Duration, out BrowseOutput, in RBTVInput) (RBTVResult, BrowseReadResult, error) {
	if in.Subject <= 0 || in.View.Kind != 4 || in.View.After != nil || in.Scope < RBTVShow || in.Scope > RBTVSeason || in.Group > RBTVUnassignedAbsolute || in.Scope == RBTVSeason && in.Group != RBTVAll {
		return RBTVResult{}, BrowseReadResult{}, ErrInput
	}
	in.View.policy = readPolicyBinding{recipe: rbTVPolicy(in.Scope), subject: in.Subject}
	steps := []readStep[rbTVWork]{func(_ context.Context, q browseQuery, f browseFence, d *rbTVWork) error {
		root, e := rbTVRoot(q, in.Subject, in.Scope, in.View.Library)
		if e != nil {
			return e
		}
		d.show = root[12].(int64)
		if in.Scope == RBTVSeason {
			d.seasonNumber = root[11].(int64)
		}
		d.fence = f
		d.seen = map[int64]bool{}
		d.bytes = 2048 + 2*len(fmt.Sprint(f)) + 2*len(fmt.Sprint(root))
		if d.bytes > 256<<10 {
			return ErrNotReady
		}
		return nil
	}}
	for window := 0; window < 5; window++ {
		window := window
		steps = append(steps, func(ctx context.Context, q browseQuery, f browseFence, d *rbTVWork) error {
			if d.done {
				return nil
			}
			limit := MaxRaw
			if window == 4 {
				limit = 1
			}
			var rows [][]any
			var e error
			if in.Scope == RBTVShow {
				after := int64(0)
				if d.keys != nil {
					after = d.keys[0].(int64)
				}
				rows, e = q("rb.group.tv.episodes", in.Subject, after, int64(limit))
			} else if d.keys == nil {
				rows, e = q("rb.group.tv.season-parts.first", in.Subject, int64(limit))
			} else {
				for level := 1; level >= 0 && len(rows) < limit; level-- {
					args := []any{in.Subject}
					args = append(args, d.keys[:level+1]...)
					args = append(args, int64(limit-len(rows)))
					more, err := q(fmt.Sprintf("rb.group.tv.season-parts.seek%d", level), args...)
					if err != nil {
						return err
					}
					rows = append(rows, more...)
				}
			}
			if e != nil {
				return e
			}
			d.raw += len(rows)
			if d.raw > 256 || window == 4 && len(rows) > 0 {
				return ErrNotReady
			}
			d.done = len(rows) < limit
			for _, v := range rows {
				if e := ctx.Err(); e != nil {
					return e
				}
				if in.Scope == RBTVShow {
					d.keys = []any{v[0]}
				} else {
					d.keys = append([]any(nil), v[9:11]...)
				}
				if v[7] != in.View.Library || v[8] != int64(4) || !admittedItem(v, f) {
					continue
				}
				mode := v[11].(string)
				if mode != "seasonal" && mode != "absolute" && mode != "date" {
					return ErrUnqualified
				}
				if v[13] != d.show {
					return ErrUnqualified
				}
				number := v[9].(int64)
				season := d.seasonNumber
				sid := int64(0)
				if v[12] != nil {
					sid = v[12].(int64)
				}
				if in.Scope == RBTVShow {
					season = 1
					if sid != 0 && !d.seen[sid] {
						if len(d.ids) == 16 {
							return ErrNotReady
						}
						d.ids = append(d.ids, sid)
						d.seen[sid] = true
					}
				}

				item, e := itemValue(v)
				if e != nil {
					return e
				}
				d.bytes += 256 + 2*len(item.Title) + 2*len(mode)
				if d.bytes > 256<<10 {
					return ErrNotReady
				}
				d.items = append(d.items, RBTVEpisode{item, number, season, mode, sid})
			}
			return nil
		})
	}
	for index := 0; index < 16; index++ {
		index := index
		steps = append(steps, func(_ context.Context, q browseQuery, f browseFence, d *rbTVWork) error {
			if index >= len(d.ids) {
				return nil
			}
			sid := d.ids[index]
			row, e := rbTVRoot(q, sid, RBTVSeason, in.View.Library)
			if e != nil {
				return e
			}
			if row[12] != d.show {
				return ErrUnqualified
			}
			d.bytes += 512 + 2*len(fmt.Sprint(row))
			if d.bytes > 256<<10 {
				return ErrNotReady
			}
			for i := range d.items {
				if d.items[i].seasonID == sid {
					d.items[i].SeasonNumber = row[11].(int64)
				}
			}
			return nil
		})
	}
	d, res, e := publishRead(ctx, r.snapshots, req, budget, out, in.View, steps)
	if e != nil {
		return RBTVResult{}, res, e
	}
	if !d.done {
		return RBTVResult{}, res, ErrNotReady
	}
	kept := d.items[:0]
	for _, item := range d.items {
		if in.Group == RBTVSpecials && (item.seasonID == 0 || item.SeasonNumber != 0) {
			continue
		}
		if in.Group == RBTVUnassignedAbsolute && (item.seasonID != 0 || item.Numbering != "absolute") {
			continue
		}
		kept = append(kept, item)
	}
	d.items = kept
	sort.Slice(d.items, func(i, j int) bool {
		a, b := d.items[i], d.items[j]
		if in.View.Descending {
			a, b = b, a
		}
		if in.Scope == RBTVShow {
			if (a.SeasonNumber == 0) != (b.SeasonNumber == 0) {
				return a.SeasonNumber != 0
			}
			if a.SeasonNumber != b.SeasonNumber {
				return a.SeasonNumber < b.SeasonNumber
			}
		}
		if a.Number != b.Number {
			return a.Number < b.Number
		}
		return a.ID < b.ID
	})
	return RBTVResult{d.items, d.raw, d.fence}, res, nil
}

// RBTVDefinitions is a separate inert inventory; frozen group definitions and
// parent goldens are unchanged. Root settings values/revision enter BOTH fences.
func RBTVDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	var defs []stmt.Definition
	add := func(name, sql string, params []stmt.Param, bindings []any, p stmt.Plan) {
		empty := append([]any(nil), bindings...)
		empty[0] = int64(-1)
		defs = append(defs, stmt.Definition{ID: statementID("rb.group.tv." + name), SQL: sql, Owner: "store/read", CallSites: []string{"store/read/rb_group_tv_native.go:BrowseReader.RBTVGroupExact"}, Kind: stmt.Read, Params: params, Cohorts: []stmt.Cohort{{Name: "present", Bindings: bindings}, {Name: "empty", Bindings: empty}}, Plan: p})
	}
	root := `SELECT r.id,r.library_id,r.kind,r.retired,r.public_id,r.metadata_incarnation,r.revision,r.source_revision,r.identity_revision,r.metadata_revision,r.show_id,r.season_number,h.id,h.library_id,h.kind,h.retired,h.public_id,h.metadata_incarnation,h.revision,h.tv_hierarchy_revision,h.show_settings_revision,h.hide_seasons,h.absolute_numbering,h.newest_first,h.show_ranges FROM catalog_entities AS r CROSS JOIN catalog_entities AS h ON h.id=`
	add("show", root+`r.id WHERE r.id=?`, []stmt.Param{i("subject")}, []any{int64(2001)}, stmt.Plan{Hot: true, PrimaryKeys: []string{"r", "h"}})
	add("season", root+`r.show_id WHERE r.id=?`, []stmt.Param{i("subject")}, []any{int64(2002)}, stmt.Plan{Hot: true, PrimaryKeys: []string{"r", "h"}})
	add("episodes", `SELECT e.id,'',e.public_id,e.title,e.retired,e.revision,e.admission_own_age,e.library_id,e.kind,e.episode_number,e.id,e.numbering,e.season_id,e.show_id FROM catalog_entities AS e INDEXED BY catalog_entities_show_fk WHERE show_id IS NOT NULL AND e.show_id=? AND e.id>? ORDER BY e.id LIMIT ?`, []stmt.Param{i("show"), i("after"), i("limit")}, []any{int64(2001), int64(0), int64(64)}, stmt.Plan{Hot: true, Indexes: []string{"catalog_entities_show_fk"}})
	for _, base := range RBGroupDefinitions() {
		name := strings.TrimPrefix(string(base.ID), "read.browse.rb.group.season-episodes.asc.")
		if name == string(base.ID) {
			continue
		}
		base.ID = statementID("rb.group.tv.season-parts." + name)
		base.CallSites = []string{"store/read/rb_group_tv_native.go:BrowseReader.RBTVGroupExact"}
		base.SQL = strings.Replace(base.SQL, " FROM catalog_entities AS e", ",e.numbering,e.season_id,e.show_id FROM catalog_entities AS e", 1)
		defs = append(defs, base)
	}
	return defs
}
