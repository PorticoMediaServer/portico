package read

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// This cold fallback proves the COMPLETE raw resource universe, including
// foreign/deleted/collection IDs. Above this bound only preparation can answer.
const savedViewDirectoryCapacity = 16
const savedViewDirectoryQueries = 2 + 1 + 1 + savedViewDirectoryCapacity*4

type SavedViewDirectoryInput struct {
	View   BrowseInput
	Pinned bool
	After  *SavedViewDirectoryCursor
}
type SavedViewDirectoryItem struct {
	ID, Name, Summary, Role string
	Revision                int64
	Pinned                  bool
}
type SavedViewDirectoryCursor struct {
	fence   browseFence
	source  [32]byte
	pinned  bool
	limit   int
	key, id string
}
type SavedViewDirectoryPage struct {
	Items                               []SavedViewDirectoryItem
	Next                                *SavedViewDirectoryCursor
	Pending, SourceExhausted, Exhausted bool
	RawRows, RawPositions               int
}
type savedViewPin struct {
	present          bool
	pinned, revision int64
	position         any
}
type savedViewDirectoryFrame struct {
	fence      browseFence
	generation int64
	ids        []string
	access     [][]any
	resources  []savedViewResource
	pins       []savedViewPin
	pending    bool
}

// Derive ONLY from the fresh native principal. This is the canonical persisted
// encoding of [authority,account,profile], not a caller-supplied owner_key and
// never a substitute for resource owner tuple/share authorization. Keeping this
// pure encoding local avoids importing persistence's legacy dbwork graph.
func savedViewPersonalKey(f browseFence) (string, error) {
	if f.token == "" || f.account == "" || f.profile == "" || f.membership == "" || f.lifetime == "" {
		return "", ErrDenied
	}
	raw, e := json.Marshal([3]string{"local", f.account, f.profile})
	if e != nil {
		return "", e
	}
	return "viewer:" + base64.RawURLEncoding.EncodeToString(raw), nil
}
func (s *savedViewSession) directoryFrame(q browseQuery, in BrowseInput) (savedViewDirectoryFrame, error) {
	var frame savedViewDirectoryFrame
	f, e := readSavedViewPrincipal(q, s.input(in))
	if e != nil {
		return frame, e
	}
	frame.fence = f
	generation, e := q("rb.saved.view.generation", int64(1))
	if e != nil {
		return frame, e
	}
	if len(generation) != 1 {
		return frame, ErrUnqualified
	}
	frame.generation = generation[0][0].(int64)
	rows, e := q("rb.saved.view.directory.membership", "")
	if e != nil {
		return frame, e
	}
	if len(rows) > savedViewDirectoryCapacity+1 {
		return frame, ErrUnqualified
	}
	for _, row := range rows {
		frame.ids = append(frame.ids, row[0].(string))
	}
	if len(rows) > savedViewDirectoryCapacity {
		frame.pending = true
		return frame, nil
	}
	key, e := savedViewPersonalKey(f)
	if e != nil {
		return frame, e
	}
	for _, id := range frame.ids {
		facts, e := q("rb.saved.view.candidate", id, f.token)
		if e != nil {
			return frame, e
		}
		if len(facts) != 1 || len(facts[0]) != 7 {
			return frame, ErrStale
		}
		frame.access = append(frame.access, facts[0])
		if facts[0][0] != int64(0) || facts[0][1] != nil || savedViewRowRole(facts[0][2:6]) == "" {
			continue
		}
		resource, e := s.resourceFromFacts(q, f, id, facts[0])
		if e != nil {
			return frame, e
		}
		pinRows, e := q("rb.saved.view.directory.pin", key, "view", id)
		if e != nil {
			return frame, e
		}
		if len(pinRows) > 1 {
			return frame, ErrUnqualified
		}
		pin := savedViewPin{}
		if len(pinRows) == 1 {
			pin = savedViewPin{present: true, pinned: pinRows[0][0].(int64), revision: pinRows[0][1].(int64), position: pinRows[0][2]}
		}
		frame.resources = append(frame.resources, resource)
		frame.pins = append(frame.pins, pin)
	}
	return frame, nil
}
func savedViewDirectoryDigest(frame savedViewDirectoryFrame) ([32]byte, error) {
	h := sha256.New()
	var data [8]byte
	var chunk [1024]byte
	write := func(value any) error {
		switch x := value.(type) {
		case nil:
			h.Write([]byte{'n'})
		case int64:
			h.Write([]byte{'i'})
			binary.BigEndian.PutUint64(data[:], uint64(x))
			h.Write(data[:])
		case string:
			h.Write([]byte{'s'})
			binary.BigEndian.PutUint64(data[:], uint64(len(x)))
			h.Write(data[:])
			for len(x) > 0 {
				n := min(len(x), len(chunk))
				copy(chunk[:], x[:n])
				h.Write(chunk[:n])
				x = x[n:]
			}
		default:
			return ErrUnqualified
		}
		return nil
	}
	write(frame.generation)
	write(int64(len(frame.ids)))
	for n, id := range frame.ids {
		write(id)
		if n < len(frame.access) {
			for _, v := range frame.access[n] {
				if e := write(v); e != nil {
					return [32]byte{}, e
				}
			}
		}
	}
	for n, r := range frame.resources {
		write(r.id)
		write(r.name)
		write(r.summary)
		write(r.definition)
		write(r.role)
		write(r.revision)
		p := frame.pins[n]
		present := int64(0)
		if p.present {
			present = 1
		}
		write(present)
		write(p.pinned)
		write(p.revision)
		if e := write(p.position); e != nil {
			return [32]byte{}, e
		}
	}
	var sum [32]byte
	copy(sum[:], h.Sum(nil))
	return sum, nil
}

// SQLite lower() is bytewise ASCII lower; BINARY ordering includes embedded
// NULs. Pin ordering uses the public printf key, including signed/wide positions.
func savedViewDirectoryKey(name string, pinned bool, position any) string {
	raw := []byte(name)
	for n, c := range raw {
		if c >= 'A' && c <= 'Z' {
			raw[n] = c + ('a' - 'A')
		}
	}
	if !pinned {
		return string(raw)
	}
	p := int64(99999999)
	if position != nil {
		p = position.(int64)
	}
	return fmt.Sprintf("%08d", p) + "\x1f" + string(raw)
}
func savedViewDirectoryPage(frame savedViewDirectoryFrame, in SavedViewDirectoryInput) (SavedViewDirectoryPage, error) {
	p := SavedViewDirectoryPage{RawRows: len(frame.ids), RawPositions: len(frame.ids), Pending: frame.pending, SourceExhausted: !frame.pending}
	if frame.pending {
		if in.After != nil {
			return SavedViewDirectoryPage{}, ErrStale
		}
		return p, nil
	}
	digest, e := savedViewDirectoryDigest(frame)
	if e != nil {
		return SavedViewDirectoryPage{}, e
	}
	if in.After != nil && (in.After.fence != frame.fence || in.After.source != digest || in.After.pinned != in.Pinned || in.After.limit != in.View.Limit) {
		return SavedViewDirectoryPage{}, ErrStale
	}
	type entry struct {
		item SavedViewDirectoryItem
		key  string
	}
	var all []entry
	for n, r := range frame.resources {
		pin := frame.pins[n]
		if in.Pinned && pin.pinned != 1 {
			continue
		}
		key := savedViewDirectoryKey(r.name, in.Pinned, pin.position)
		all = append(all, entry{SavedViewDirectoryItem{ID: r.id, Name: r.name, Summary: r.summary, Role: r.role, Revision: r.revision, Pinned: pin.pinned == 1}, key})
	}
	sort.Slice(all, func(a, b int) bool {
		if all[a].key != all[b].key {
			return all[a].key < all[b].key
		}
		return all[a].item.ID < all[b].item.ID
	})
	for _, value := range all {
		if in.After != nil && (value.key < in.After.key || value.key == in.After.key && value.item.ID <= in.After.id) {
			continue
		}
		if len(p.Items) == in.View.Limit {
			p.Exhausted = false
			return p, nil
		}
		p.Items = append(p.Items, value.item)
		p.Next = &SavedViewDirectoryCursor{fence: frame.fence, source: digest, pinned: in.Pinned, limit: in.View.Limit, key: value.key, id: value.item.ID}
	}
	p.Exhausted = true
	p.Next = nil
	return p, nil
}

// Directory returns exact public order only after a complete bounded membership
// proof. It exposes no sample as an exact list. Over-cap output is explicitly
// pending, empty, non-exhausted, and requires the parent's preparation manager.
func (r *SavedViewReader) Directory(ctx context.Context, request governor.Request, budget time.Duration, out BrowseOutput, in SavedViewDirectoryInput) (SavedViewDirectoryPage, BrowseReadResult, error) {
	if r == nil || in.View.After != nil {
		return SavedViewDirectoryPage{}, BrowseReadResult{}, ErrInput
	}
	return liveRead(ctx, request, budget, out, r.pool, in.View.Now, func(ctx context.Context, request governor.Request, budget time.Duration, at time.Time) (SavedViewDirectoryPage, BrowseReadResult, error) {
		in.View.Now = at
		s, e := newSavedViewSession(r, ctx, request, budget, out, in.View)
		if e != nil {
			return SavedViewDirectoryPage{}, BrowseReadResult{}, e
		}
		for attempt := 0; attempt < 2; attempt++ {
			s.cost.Attempts = attempt + 1
			var initial savedViewDirectoryFrame
			e = s.window(false, savedViewDirectoryQueries, func(q browseQuery) error { var e error; initial, e = s.directoryFrame(q, in.View); return e })
			if e == nil {
				var page SavedViewDirectoryPage
				nameBytes := int64(0)
				for _, r := range initial.resources {
					nameBytes += 2 * int64(len(r.name))
				}
				if e = s.reserve(nameBytes + int64(len(initial.resources))*1024 + 1024); e == nil {
					page, e = savedViewDirectoryPage(initial, in)
				}
				prior := e
				final := s.window(true, savedViewDirectoryQueries, func(q browseQuery) error {
					current, e := s.directoryFrame(q, in.View)
					if e != nil {
						return e
					}
					if !reflect.DeepEqual(current, initial) {
						return ErrStale
					}
					return nil
				})
				e = s.finish(initial.fence, prior, final)
				if e == nil {
					return page, s.result(), nil
				}
			}
			if !errors.Is(e, ErrStale) || ctx.Err() != nil || !time.Now().Before(s.deadline) {
				break
			}
		}
		if errors.Is(e, ErrStale) {
			s.cost.Refusal = &governor.Refusal{Code: governor.Conflict}
			e = errors.Join(e, governor.ErrBusy)
		}
		return SavedViewDirectoryPage{}, s.result(), e
	})
}

func rbSavedViewDirectoryDefinitions() []stmt.Definition {
	s := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	key := func(table, name string, cols ...string) stmt.AutoIndexBinding {
		b := stmt.AutoIndexBinding{Table: table, Name: name, Origin: "pk"}
		for _, c := range cols {
			b.Columns = append(b.Columns, stmt.IndexColumn{Name: c, Collation: "BINARY"})
		}
		return b
	}
	r := key("saved_resources", "sqlite_autoindex_saved_resources_1", "id")
	p := key("saved_pins", "sqlite_autoindex_saved_pins_1", "owner_key", "kind", "resource_id")
	o := key("saved_pin_order", "sqlite_autoindex_saved_pin_order_1", "owner_key", "kind", "resource_id")
	add := func(name, sql string, params []stmt.Param, args []any, proof stmt.Plan) stmt.Definition {
		return stmt.Definition{ID: readStatementID("rb.saved.view.directory." + name), Owner: "store/read", Kind: stmt.Read, CallSites: []string{"store/read/rb_saved_view_directory.go"}, SQL: sql, Params: params, Cohorts: []stmt.Cohort{{Name: "present", Bindings: args}}, Plan: proof}
	}
	return []stmt.Definition{
		add("membership", "SELECT r.id FROM saved_resources AS r INDEXED BY sqlite_autoindex_saved_resources_1 WHERE r.id>=? ORDER BY r.id LIMIT 17", []stmt.Param{s("start")}, []any{""}, stmt.Plan{Hot: true, Indexes: []string{r.Name}, AutoIndexes: []stmt.AutoIndexBinding{r}, Seeks: []string{"id>?"}}),
		add("pin", `SELECT p.pinned,p.revision,o.position FROM saved_pins AS p INDEXED BY sqlite_autoindex_saved_pins_1 LEFT JOIN saved_pin_order AS o INDEXED BY sqlite_autoindex_saved_pin_order_1 ON o.owner_key=p.owner_key AND o.kind=p.kind AND o.resource_id=p.resource_id WHERE p.owner_key=? AND p.kind=? AND p.resource_id=?`, []stmt.Param{s("owner"), s("kind"), s("resource")}, []any{"viewer:fixture", "view", "saved-view"}, stmt.Plan{Hot: true, Indexes: []string{p.Name, o.Name}, AutoIndexes: []stmt.AutoIndexBinding{p, o}, Seeks: []string{"owner_key=? AND kind=? AND resource_id=?"}}),
	}
}
