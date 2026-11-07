package read

import (
	"fmt"
	"strings"
)

// Route order is Stream, Provider, Kind, ProviderID, Evidence (BINARY strings).
// Mapping coordinates identify a namespace even when several namespaces map to
// the same owner. Stream 1=local, 2=provider, 3=explicit include. Parent chooses
// the minimum route for an owner/work; repeated raw routes are not new matches.
type nativeAutomaticRoute struct {
	Stream                               uint8
	Provider, Kind, ProviderID, Evidence string
}
type nativeAutomaticOwner struct {
	Collection int64
	Route      nativeAutomaticRoute
}
type nativeAutomaticPosition struct {
	Work  int64
	Route nativeAutomaticRoute
}
type nativeAutoValue struct {
	I    int64
	S    string
	Text bool
}
type nativeAutoKey struct {
	V [3]nativeAutoValue
	N int
}

// Values only, fixed depth. Work==0 on a non-exhausted result means a charged
// raw mapping/source/rejected position; callers MUST persist the returned cursor.
// Parent binds this cursor to its current revision/publication/policy fence.
type nativeAutomaticCursor struct {
	Library, Collection int64
	Mapping             nativeAutoKey
	HaveMapping         bool
	Phase               uint8
	Evidence            nativeAutoKey
	Origin              nativeAutoKey
	Local               nativeAutoKey
	Override            nativeAutoKey
}

func nativeAutoQuery(q browseQuery) browseQuery {
	rows, bytes := 0, 0
	return func(name string, args ...any) ([][]any, error) {
		r, e := q("full.native.auto."+name, args...)
		if e != nil {
			return nil, e
		}
		rows += len(r)
		bytes += len(r) * 64
		for _, row := range r {
			bytes += len(row) * 32
			for _, v := range row {
				if s, ok := v.(string); ok {
					bytes += len(s) * 2
				}
			}
		}
		if rows > 512 || bytes > 256<<10 {
			return nil, errFullSearchPending
		}
		if len(r) > 1 {
			return nil, ErrUnqualified
		}
		return r, nil
	}
}
func nativeAutoPoint(q browseQuery, name string, args ...any) ([]any, error) {
	r, e := q(name, args...)
	if e != nil || len(r) == 0 {
		return nil, e
	}
	return r[0], nil
}
func nativeAutoSeek(q browseQuery, name string, prefix []any, after nativeAutoKey) (nativeAutoKey, bool, error) {
	s := nativeAutoStreams[name]
	read := func(suffix string, args []any) (nativeAutoKey, bool, error) {
		r, e := nativeAutoPoint(q, name+suffix, args...)
		if e != nil || r == nil {
			return nativeAutoKey{}, false, e
		}
		if len(r) != len(s.keys) {
			return nativeAutoKey{}, false, ErrUnqualified
		}
		k := nativeAutoKey{N: len(r)}
		for i, v := range r {
			switch v := v.(type) {
			case int64:
				if s.types[i] != "integer" {
					return nativeAutoKey{}, false, ErrUnqualified
				}
				k.V[i].I = v
			case string:
				if s.types[i] != "text" {
					return nativeAutoKey{}, false, ErrUnqualified
				}
				k.V[i] = nativeAutoValue{S: v, Text: true}
			default:
				return nativeAutoKey{}, false, ErrUnqualified
			}
		}
		return k, true, nil
	}
	if after.N == 0 {
		return read(".first", prefix)
	}
	if after.N != len(s.keys) {
		return nativeAutoKey{}, false, ErrInput
	}
	for i := after.N - 1; i >= 0; i-- {
		args := append([]any(nil), prefix...)
		for j := 0; j <= i; j++ {
			v := after.V[j]
			if v.Text {
				args = append(args, v.S)
			} else {
				args = append(args, v.I)
			}
		}
		k, ok, e := read(fmt.Sprintf(".tail%d", i), args)
		if e != nil || ok {
			if ok && !nativeAutoKeyAfter(k, after) {
				return k, false, ErrUnqualified
			}
			return k, ok, e
		}
	}
	return nativeAutoKey{}, false, nil
}
func nativeAutoLive(q browseQuery, library, owner int64) (bool, error) {
	r, e := nativeAutoPoint(q, "owner", owner, library)
	return r != nil, e
}
func nativeAutoMap(q browseQuery, library int64, provider, id string) (int64, error) {
	r, e := nativeAutoPoint(q, "map", library, provider, id)
	if e != nil || r == nil {
		return 0, e
	}
	if len(r) != 2 {
		return 0, ErrUnqualified
	}
	if r[0] == nil {
		return 0, nil
	}
	owner, a := r[0].(int64)
	removed, b := r[1].(int64)
	if !a || !b {
		return 0, ErrUnqualified
	}
	if removed != 0 {
		return 0, nil
	}
	ok, e := nativeAutoLive(q, library, owner)
	if !ok {
		return 0, e
	}
	return owner, e
}
func nativeAutoLess(a, b nativeAutomaticRoute) bool {
	if a.Stream != b.Stream {
		return a.Stream < b.Stream
	}
	as := [4]string{a.Provider, a.Kind, a.ProviderID, a.Evidence}
	bs := [4]string{b.Provider, b.Kind, b.ProviderID, b.Evidence}
	for i := range as {
		if as[i] != bs[i] {
			return as[i] < bs[i]
		}
	}
	return false
}

// A complete inverse proof or a typed refusal: never a truncated owner list.
func nativeAutomaticOwners(query browseQuery, library int64, libraryName string, work int64) ([]nativeAutomaticOwner, error) {
	if library <= 0 || work <= 0 || libraryName == "" {
		return nil, ErrInput
	}
	q := nativeAutoQuery(query)
	r, e := nativeAutoPoint(q, "work", work, library)
	if e != nil || r == nil {
		return nil, e
	}
	if len(r) != 1 {
		return nil, ErrUnqualified
	}
	local, ok := r[0].(string)
	if !ok {
		return nil, ErrUnqualified
	}
	var out []nativeAutomaticOwner
	bytes := 0
	add := func(owner int64, route nativeAutomaticRoute) error {
		if owner == 0 {
			return nil
		}
		for i := range out {
			if out[i].Collection == owner {
				if nativeAutoLess(route, out[i].Route) {
					out[i].Route = route
				}
				return nil
			}
		}
		bytes += 64 + len(route.Provider) + len(route.Kind) + len(route.ProviderID) + len(route.Evidence)
		if len(out) == 512 || bytes > 128<<10 {
			return errFullSearchPending
		}
		out = append(out, nativeAutomaticOwner{owner, route})
		return nil
	}
	source := func(route nativeAutomaticRoute) error {
		owner, e := nativeAutoMap(q, library, route.Provider, route.ProviderID)
		if e != nil || owner == 0 {
			return e
		}
		o, e := nativeAutoPoint(q, "override", owner, work)
		if e != nil {
			return e
		}
		if o != nil {
			return nil
		}
		return add(owner, route)
	}
	if local != "" {
		e = source(nativeAutomaticRoute{Stream: 1, Provider: "nfo", Kind: "collection", ProviderID: local})
	} else {
		o, err := nativeAutoPoint(q, "origin", work)
		if err != nil {
			return nil, err
		}
		if o != nil {
			if len(o) != 2 {
				return nil, ErrUnqualified
			}
			provider, a := o[0].(string)
			ev, b := o[1].(string)
			if a && b {
				accepted, err := nativeAutoAccepted(q, library, libraryName, work, provider, ev)
				if err != nil {
					return nil, err
				}
				if accepted {
					var key nativeAutoKey
					for {
						k, yes, err := nativeAutoSeek(q, "collections", []any{ev, "collection"}, key)
						if err != nil {
							return nil, err
						}
						if !yes {
							break
						}
						key = k
						id := k.V[0].S
						c, err := nativeAutoPoint(q, "collection", ev, id)
						if err != nil {
							return nil, err
						}
						if nativeAutoCollection(c, provider) && id != "" {
							if err = source(nativeAutomaticRoute{2, provider, "collection", id, ev}); err != nil {
								return nil, err
							}
						}
					}
				}
			}
		}
	}
	if e != nil {
		return nil, e
	}
	var key nativeAutoKey
	for {
		k, yes, err := nativeAutoSeek(q, "inverse", []any{work}, key)
		if err != nil {
			return nil, err
		}
		if !yes {
			break
		}
		key = k
		owner := k.V[0].I
		o, err := nativeAutoPoint(q, "override", owner, work)
		if err != nil {
			return nil, err
		}
		if len(o) != 1 || o[0] != int64(1) {
			continue
		}
		var mk nativeAutoKey
		for {
			m, yes, err := nativeAutoSeek(q, "maps", []any{owner}, mk)
			if err != nil {
				return nil, err
			}
			if !yes {
				break
			}
			mk = m
			if m.V[0].I != library {
				continue
			}
			p, id := m.V[1].S, m.V[2].S
			live, err := nativeAutoMap(q, library, p, id)
			if err != nil {
				return nil, err
			}
			if live == owner {
				if err = add(owner, nativeAutomaticRoute{3, p, "collection", id, ""}); err != nil {
					return nil, err
				}
			}
		}
	}
	return out, nil
}

// Each call advances at most one raw mapping, evidence, origin, local, or
// override position. Work==0 is a progress position, not an empty membership.
func nativeAutomaticNext(query browseQuery, library int64, libraryName string, collection int64, after *nativeAutomaticCursor) (nativeAutomaticPosition, *nativeAutomaticCursor, bool, error) {
	var zero nativeAutomaticPosition
	if library <= 0 || collection <= 0 || libraryName == "" {
		return zero, nil, false, ErrInput
	}
	c := nativeAutomaticCursor{Library: library, Collection: collection}
	if after != nil {
		c = *after
		if c.Library != library || c.Collection != collection || c.Phase > 4 {
			return zero, nil, false, ErrInput
		}
	}
	q := nativeAutoQuery(query)
	if !c.HaveMapping {
		m, ok, e := nativeAutoSeek(q, "maps", []any{collection}, c.Mapping)
		if e != nil {
			return zero, nil, false, e
		}
		if !ok {
			return zero, nil, true, nil
		}
		c.Mapping = m
		c.HaveMapping = true
		c.Phase = 0
		return zero, &c, false, nil
	}
	m := c.Mapping
	p, id := m.V[1].S, m.V[2].S
	owner, e := nativeAutoMap(q, library, p, id)
	if e != nil {
		return zero, nil, false, e
	}
	if m.V[0].I != library || owner != collection {
		c.HaveMapping = false
		return zero, &c, false, nil
	}
	route := nativeAutomaticRoute{Provider: p, Kind: "collection", ProviderID: id}
	finish := func(work int64, stream uint8, ev string) (nativeAutomaticPosition, *nativeAutomaticCursor, bool, error) {
		route.Stream = stream
		route.Evidence = ev
		return nativeAutomaticPosition{work, route}, &c, false, nil
	}
	check := func(work int64, override bool) (int64, error) {
		r, e := nativeAutoPoint(q, "work", work, library)
		if e != nil || r == nil {
			return 0, e
		}
		o, e := nativeAutoPoint(q, "override", collection, work)
		if e != nil {
			return 0, e
		}
		if override {
			if len(o) == 1 && o[0] == int64(1) {
				return work, nil
			}
			return 0, nil
		}
		if o != nil {
			return 0, nil
		}
		return work, nil
	}
	if c.Phase == 0 {
		c.Phase = 1
		if p == "nfo" && id != "" {
			k, ok, e := nativeAutoSeek(q, "local", []any{library, id}, c.Local)
			if e != nil {
				return zero, nil, false, e
			}
			if ok {
				c.Phase = 0
				c.Local = k
				w, e := check(k.V[0].I, false)
				if e != nil {
					return zero, nil, false, e
				}
				return finish(w, 1, "")
			}
		}
		return zero, &c, false, nil
	}
	if c.Phase == 1 {
		k, ok, e := nativeAutoSeek(q, "evidence", []any{p, "collection", id}, c.Evidence)
		if e != nil {
			return zero, nil, false, e
		}
		if !ok {
			c.Phase = 3
			return zero, &c, false, nil
		}
		c.Evidence = k
		c.Origin = nativeAutoKey{}
		c.Phase = 2
		return zero, &c, false, nil
	}
	if c.Phase == 2 {
		ev := c.Evidence.V[0].S
		k, ok, e := nativeAutoSeek(q, "origins", []any{ev}, c.Origin)
		if e != nil {
			return zero, nil, false, e
		}
		if !ok {
			c.Phase = 1
			return zero, &c, false, nil
		}
		c.Origin = k
		w := int64(0)
		if k.V[1].S == "relations" && id != "" {
			r, e := nativeAutoPoint(q, "collection", ev, id)
			if e != nil {
				return zero, nil, false, e
			}
			if nativeAutoCollection(r, p) {
				a, e := nativeAutoAccepted(q, library, libraryName, k.V[0].I, p, ev)
				if e != nil {
					return zero, nil, false, e
				}
				if a {
					w, e = check(k.V[0].I, false)
					if e != nil {
						return zero, nil, false, e
					}
				}
			}
		}
		return finish(w, 2, ev)
	}
	if c.Phase == 3 {
		k, ok, e := nativeAutoSeek(q, "over", []any{collection}, c.Override)
		if e != nil {
			return zero, nil, false, e
		}
		if !ok {
			c.Phase = 4
			return zero, &c, false, nil
		}
		c.Override = k
		w, e := check(k.V[0].I, true)
		if e != nil {
			return zero, nil, false, e
		}
		return finish(w, 3, "")
	}
	c.HaveMapping = false
	c.Local = nativeAutoKey{}
	c.Evidence = nativeAutoKey{}
	c.Origin = nativeAutoKey{}
	c.Override = nativeAutoKey{}
	return zero, &c, false, nil
}

// Lexicographic BINARY comparison mirrors the disjoint SQL equality tails.
func nativeAutoKeyAfter(k, after nativeAutoKey) bool {
	for i := 0; i < k.N; i++ {
		a, b := k.V[i], after.V[i]
		if a.Text != b.Text {
			return false
		}
		if a.Text {
			if a.S != b.S {
				return a.S > b.S
			}
		} else if a.I != b.I {
			return a.I > b.I
		}
	}
	return false
}

// SQLite R21 trim(name) has the default ASCII SPACE trim set. Preserve that
// exact predicate outside the closed SQL grammar; Unicode TrimSpace would drop
// valid legacy membership for a tab/non-ASCII-space name.
func nativeAutoCollection(row []any, provider string) bool {
	if len(row) != 2 || row[0] != provider {
		return false
	}
	name, ok := row[1].(string)
	return ok && strings.Trim(name, " ") != ""
}

// libraryName is the same-ReadScope common fence's canonical bridge value.
// Seek the declared UNIQUE text key, then compare the returned logical numeric
// library ID in Go. No hidden rowid witness or authority is introduced.
func nativeAutoAccepted(q browseQuery, library int64, libraryName string, work int64, provider, evidence string) (bool, error) {
	row, err := nativeAutoPoint(q, "accepted", libraryName, work, library, provider, evidence)
	if err != nil || row == nil {
		return false, err
	}
	if len(row) != 2 || row[0] != work || row[1] != library {
		return false, ErrUnqualified
	}
	return true, nil
}
