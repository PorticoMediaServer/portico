package read

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"time"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

// SavedViewReader is harness-only. DirectoryCandidates is deliberately NOT the
// public lower(name),id directory: that order needs an exact preparation recipe.
// Its View is an actually authorized library anchor, not a synthetic principal.
type SavedViewReader struct {
	snapshots browseSnapshots
	pool      *governor.Snapshots
}

func NewSavedViewReader(s *governor.Snapshots) (*SavedViewReader, error) {
	if s == nil {
		return nil, ErrInput
	}
	return &SavedViewReader{s, s}, nil
}

type SavedViewCandidate struct {
	ID       string
	Revision int64
	Role     string
}
type SavedViewCandidateCursor struct {
	fence      browseFence
	generation int64
	rawID      string
}
type SavedViewCandidateInput struct {
	View  BrowseInput
	After *SavedViewCandidateCursor
}
type SavedViewCandidates struct {
	Items                 []SavedViewCandidate
	Next                  *SavedViewCandidateCursor
	Exhausted             bool
	RawRows, RawPositions int
	// Always true, even at EOF. No name-order promise is made by this source.
	CandidateOnly bool
}

func (r *SavedViewReader) DirectoryCandidates(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in SavedViewCandidateInput) (SavedViewCandidates, BrowseReadResult, error) {
	if r == nil || in.View.After != nil {
		return SavedViewCandidates{}, BrowseReadResult{}, ErrInput
	}
	return liveRead(ctx, request, budget, output, r.pool, in.View.Now, func(ctx context.Context, request governor.Request, budget time.Duration, at time.Time) (SavedViewCandidates, BrowseReadResult, error) {
		in.View.Now = at
		var generation int64
		var raw [][]any
		root := func(q browseQuery) ([]any, error) {
			rows, e := q("rb.saved.view.generation", int64(1))
			if e != nil {
				return nil, e
			}
			if len(rows) != 1 {
				return nil, ErrUnqualified
			}
			generation = rows[0][0].(int64)
			if in.After != nil && generation != in.After.generation {
				return nil, ErrStale
			}
			name, after := "rb.saved.view.candidates.first", ""
			if in.After != nil {
				name = "rb.saved.view.candidates"
				after = in.After.rawID
			}
			ids, e := q(name, after)
			if e != nil {
				return nil, e
			}
			if len(ids) > 1 {
				return nil, ErrUnqualified
			}
			raw = nil
			witness := append([]any{}, rows[0]...)
			if len(ids) == 1 {
				facts, e := q("rb.saved.view.candidate", ids[0][0], in.View.TokenHash)
				if e != nil {
					return nil, e
				}
				if len(facts) != 1 {
					return nil, ErrStale
				}
				raw = [][]any{append(append([]any{}, ids[0]...), facts[0]...)}
				witness = append(witness, raw[0]...)
			}
			return witness, nil
		}
		return publishRead(ctx, r.snapshots, request, budget, output, in.View, []readStep[SavedViewCandidates]{
			func(ctx context.Context, q browseQuery, f browseFence, p *SavedViewCandidates) error {
				if in.After != nil && f != in.After.fence {
					return ErrStale
				}
				return ctx.Err()
			},
			func(ctx context.Context, q browseQuery, f browseFence, p *SavedViewCandidates) error {
				*p = SavedViewCandidates{CandidateOnly: true}
				rows := raw
				if len(rows) > 1 {
					return ErrUnqualified
				}
				p.RawRows = len(rows)
				for _, row := range rows {
					p.RawPositions++
					p.Next = &SavedViewCandidateCursor{fence: f, generation: generation, rawID: row[0].(string)}
					role := savedViewRowRole(row[3:7])
					if row[1].(int64) != 0 || row[2] != nil || role == "" {
						continue
					}
					p.Items = append(p.Items, SavedViewCandidate{ID: row[0].(string), Revision: row[7].(int64), Role: role})
					if len(p.Items) == in.View.Limit {
						return ctx.Err()
					}
				}
				if len(rows) < 1 {
					p.Exhausted = true
					p.Next = nil
				}
				return ctx.Err()
			},
		}, root)
	})
}
func savedViewRowRole(row []any) string {
	if row[0] == nil && row[1] == nil && row[2] == nil {
		return "owner"
	}
	if row[3] == "editor" {
		return "editor"
	}
	if row[3] == "viewer" {
		return "viewer"
	}
	return ""
}

// An execution resource is minted only by the same-snapshot native byte-point
// reader below. Parent composition must read it in BOTH publication snapshots,
// compare the entire value (including definition and principal fence), and use
// the same original LIVE deadline/copy budget. There is intentionally no public
// execution API until that exact-byte publication bridge is integrated.
type savedViewResource struct {
	fence                               browseFence
	id, name, summary, definition, role string
	revision                            int64
}

func readSavedViewResource(scope *governor.ReadScope, f browseFence, id string) (savedViewResource, error) {
	if scope == nil || len(id) > 256 {
		return savedViewResource{}, ErrInput
	}
	if f.token == "" || f.account == "" || f.profile == "" {
		return savedViewResource{}, ErrDenied
	}
	facts, e := scope.Query(readStatementID("rb.saved.view.candidate"), id, f.token)
	if e != nil {
		return savedViewResource{}, e
	}
	if len(facts) != 1 || facts[0][0] != int64(0) || facts[0][1] != nil {
		return savedViewResource{}, ErrDenied
	}
	role := savedViewRowRole(facts[0][2:6])
	if role == "" {
		return savedViewResource{}, ErrDenied
	}
	suffix := "shared"
	args := []any{"local", f.account, f.profile, id}
	if role == "owner" {
		suffix = "owner"
		args = []any{id, "local", f.account, f.profile}
	}
	row, e := scope.QueryExactBytes(readStatementID("rb.saved.view.resource."+suffix+".bytes"), readStatementID("rb.saved.view.resource."+suffix), []int{0, 1, 2}, args...)
	if e != nil {
		return savedViewResource{}, e
	}
	if len(row) != 4 || row[3] != facts[0][6] {
		return savedViewResource{}, ErrStale
	}
	return savedViewResource{fence: f, id: id, name: row[0].(string), summary: row[1].(string), definition: row[2].(string), revision: row[3].(int64), role: role}, nil
}

type savedViewDefinition struct {
	LibraryID string          `json:"libraryId"`
	Pivot     string          `json:"pivot"`
	Query     json.RawMessage `json:"query"`
	Sort      []struct {
		Field     string `json:"field"`
		Direction string `json:"direction"`
		Seed      uint32 `json:"seed,omitempty"`
	} `json:"sort"`
	Presentation string `json:"presentation"`
}

func savedViewExecutionInput(r savedViewResource, in BrowseInput) (BrowseInput, error) {
	if r.fence == (browseFence{}) || r.fence != mustSavedViewInputFence(r, in) {
		return BrowseInput{}, ErrStale
	}
	if len(r.definition) > 32<<10 || !utf8.ValidString(r.definition) || !savedViewUniqueJSON(r.definition) {
		return BrowseInput{}, ErrUnqualified
	}
	var d savedViewDefinition
	dec := json.NewDecoder(bytes.NewBufferString(r.definition))
	dec.DisallowUnknownFields()
	if e := dec.Decode(&d); e != nil {
		return BrowseInput{}, ErrUnqualified
	}
	var extra any
	if e := dec.Decode(&extra); e != io.EOF {
		return BrowseInput{}, ErrUnqualified
	}
	// No query operator, seeded ordering or multi-sort is approximated. These
	// remain the general browse/filter/facet preparation manager's responsibility.
	if d.LibraryID != r.fence.library || d.Pivot != "movies" || in.Kind != 1 || (d.Presentation != "grid" && d.Presentation != "list") || (len(d.Query) > 0 && !bytes.Equal(bytes.TrimSpace(d.Query), []byte("null"))) || len(d.Sort) != 1 || d.Sort[0].Field != "title" || d.Sort[0].Seed != 0 || (d.Sort[0].Direction != "asc" && d.Sort[0].Direction != "desc") {
		return BrowseInput{}, ErrUnqualified
	}
	in.Descending = d.Sort[0].Direction == "desc"
	in.KeepArticles = false
	return in, nil
}

// Compare request identity fields without treating caller sorting as authority.
func mustSavedViewInputFence(r savedViewResource, in BrowseInput) browseFence {
	f := r.fence
	if in.Database != f.database || in.TokenHash != f.token || in.Library != f.libraryID || in.Kind != f.kind {
		return browseFence{}
	}
	return f
}

type savedViewExecutionCursor struct {
	resource savedViewResource
	browse   *BrowseCursor
}

// readSavedViewExecution is a private draft, never a published response. It
// reuses the actual browse authorization kernel and returns raw progress intact.
func readSavedViewExecution(ctx context.Context, q browseQuery, f browseFence, in BrowseInput, r savedViewResource, after *savedViewExecutionCursor) (BrowsePage, *savedViewExecutionCursor, error) {
	if r.fence != f || r.role == "" || r.revision <= 0 {
		return BrowsePage{}, nil, ErrDenied
	}
	in, e := savedViewExecutionInput(r, in)
	if e != nil {
		return BrowsePage{}, nil, e
	}
	if f.descending != in.Descending || f.keepArticles != in.KeepArticles {
		return BrowsePage{}, nil, ErrStale
	}
	if in.After != nil {
		return BrowsePage{}, nil, ErrInput
	}
	if after != nil {
		if after.resource != r {
			return BrowsePage{}, nil, ErrStale
		}
		in.After = after.browse
	}
	if in.After != nil && in.After.fence != f {
		return BrowsePage{}, nil, ErrStale
	}
	page, e := readBrowsePage(ctx, q, in, f)
	if e != nil {
		return BrowsePage{}, nil, e
	}
	var next *savedViewExecutionCursor
	if page.Next != nil {
		next = &savedViewExecutionCursor{r, page.Next}
	}
	return page, next, nil
}

// encoding/json accepts repeated members by overwriting earlier values. Reject
// them before decoding so an unsupported predicate cannot disappear behind null.
func savedViewUniqueJSON(raw string) bool {
	d := json.NewDecoder(bytes.NewBufferString(raw))
	var value func(int) bool
	value = func(depth int) bool {
		if depth > 64 {
			return false
		}
		tok, e := d.Token()
		if e != nil {
			return false
		}
		delim, container := tok.(json.Delim)
		if !container {
			return true
		}
		switch delim {
		case '{':
			seen := map[string]bool{}
			for d.More() {
				key, e := d.Token()
				if e != nil {
					return false
				}
				k, ok := key.(string)
				if !ok || seen[k] {
					return false
				}
				seen[k] = true
				if !value(depth + 1) {
					return false
				}
			}
			end, e := d.Token()
			return e == nil && end == json.Delim('}')
		case '[':
			for d.More() {
				if !value(depth + 1) {
					return false
				}
			}
			end, e := d.Token()
			return e == nil && end == json.Delim(']')
		}
		return false
	}
	if !value(0) {
		return false
	}
	_, e := d.Token()
	return e == io.EOF
}
