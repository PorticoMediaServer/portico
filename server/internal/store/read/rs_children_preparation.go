package read

import (
	"cmp"
	"context"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"slices"
	"strings"
	"sync"
	"time"
)

const rsListeningPreparationBytes int64 = 4 << 20
const rsListeningKeysPerPoll = 4

type rsListeningCandidate struct {
	row   []any
	key   string
	bytes int64
}

// RSListeningWork retains a COMPLETE bounded listening set under the existing
// RB lease. It contains no SQL handle, cursor or independent resource ledger.
// Trusted composition attaches identical polls to this work with a SERVER job
// context; every poll still requires current LIVE/output/authorization. Close
// after the last consumer exits, including on denial, cancellation or overflow.
type RSListeningWork struct {
	mu                          sync.Mutex
	authority                   *governor.Authority
	lease                       *governor.PreparationLease
	owner                       governor.OwnerID
	in                          RSChildrenInput
	fence                       browseFence
	source                      []any
	raw                         []rsListeningCandidate
	index                       int
	bytes                       int64
	bound, done, handed, closed bool
	failure                     error
}

func BeginRSListeningWork(ctx context.Context, a *governor.Authority, owner governor.OwnerID, key governor.JobKey, deadline time.Time, in RSChildrenInput) (*RSListeningWork, *governor.Refusal, error) {
	recipe, ok := rsChildrenRecipeFor(in.Family)
	if ctx == nil || a == nil || !ok || in.Parent <= 0 || in.Family != RSAlbumTracks && in.Family != RSAudiobookFiles || in.Work != nil || in.View.After != nil || in.View.Descending || in.View.KeepArticles || in.View.Library <= 0 || in.View.Kind != recipe.parentKind || in.View.Limit < 1 || in.View.Limit > MaxPage || len(in.View.Database) == 0 || len(in.View.Database) > 256 || len(in.View.TokenHash) == 0 || len(in.View.TokenHash) > 256 || in.After != nil && (len(in.After.sortKey) > 65536 || len(in.After.source) > 32) {
		return nil, nil, ErrInput
	}
	decision := a.BeginPreparation(ctx, governor.PreparationRequest{Key: key, Owner: owner, Deadline: deadline, ReservedBytes: rsListeningPreparationBytes + governor.PreparationMetadataBytes})
	if decision.Refusal != nil {
		return nil, decision.Refusal, governor.ErrBusy
	}
	in.View.Database = strings.Clone(in.View.Database)
	in.View.TokenHash = strings.Clone(in.View.TokenHash)
	if in.After != nil {
		v := *in.After
		v.source = slices.Clone(v.source)
		v.sortKey = strings.Clone(v.sortKey)
		in.After = &v
	}
	return &RSListeningWork{authority: a, lease: decision.Lease, owner: owner, in: in}, nil, nil
}
func (w *RSListeningWork) Close() error {
	if w == nil {
		return ErrInput
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	w.closed = true
	err := w.authority.CancelPreparation(w.lease)
	w.raw = nil
	w.source = nil
	w.in = RSChildrenInput{}
	return errors.Join(err, w.authority.RetirePreparation(w.lease))
}
func sameListeningInput(a, b RSChildrenInput) bool {
	a.View.Now = b.View.Now
	a.Work = nil
	b.Work = nil
	if a.View != b.View || a.Parent != b.Parent || a.Family != b.Family || (a.After == nil) != (b.After == nil) {
		return false
	}
	if a.After == nil {
		return true
	}
	x, y := a.After, b.After
	return x.fence == y.fence && x.parent == y.parent && x.family == y.family && x.limit == y.limit && x.first == y.first && x.second == y.second && x.id == y.id && x.sortKey == y.sortKey && equalSource(x.source, y.source)
}
func (r *RSChildrenReader) preparedListeningWindow(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in RSChildrenInput, recipe rsChildrenRecipe, root rootRead, source *[]any) (RSChildrenPage, BrowseReadResult, error) {
	w := in.Work
	if w == nil {
		return RSChildrenPage{}, BrowseReadResult{}, ErrInput
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed || w.handed || w.owner != request.Owner || output.Authority != w.authority || !sameListeningInput(w.in, in) {
		return RSChildrenPage{}, BrowseReadResult{}, ErrInput
	}
	if w.failure != nil {
		return RSChildrenPage{}, BrowseReadResult{}, w.failure
	}
	if err := w.authority.CheckPreparation(w.lease); err != nil {
		return RSChildrenPage{}, BrowseReadResult{}, err
	}
	windows := &rsByteWindow{source: r.snapshots, preflightCalls: 8}
	var selected []int
	var rootCalls int
	originalRoot := root
	root = func(q browseQuery) ([]any, error) {
		if err := w.authority.CheckPreparation(w.lease); err != nil {
			return nil, err
		}
		rootCalls++
		if rootCalls > 2 {
			return nil, ErrStale
		}
		row, err := originalRoot(q)
		if err != nil {
			return nil, err
		}
		if w.bound && !equalSource(w.source, row) {
			return nil, ErrStale
		}
		return row, nil
	}
	steps := []readStep[RSChildrenPage]{func(ctx context.Context, q browseQuery, f browseFence, p *RSChildrenPage) error {
		if err := w.authority.CheckPreparation(w.lease); err != nil {
			return err
		}
		if f.maximumAge >= 0 || f.allowUnrated == 0 {
			return ErrUnqualified
		}
		if in.After != nil && (in.After.fence != f || in.After.parent != in.Parent || in.After.family != in.Family || in.After.limit != in.View.Limit) {
			return ErrStale
		}
		if w.bound {
			if w.fence != f || !equalSource(w.source, *source) {
				return ErrStale
			}
			return nil
		}
		rows, err := q(rsChildrenName(recipe.name+".first"), in.Parent, int64(RSListeningChildCap+1))
		if err != nil {
			return err
		}
		p.RawRows = len(rows)
		if len(rows) > RSListeningChildCap {
			return ErrUnqualified
		}
		// The RB reservation funds every retained row/key/copy between polls. The
		// separately admitted output keeps its original1/4 sort workspace bound.
		w.bytes = int64(len(rows)) * 128
		if w.bytes > min(rsListeningPreparationBytes/2, output.Request.Need[governor.MemoryBytes]/4) {
			return governor.ErrSnapshotBudget
		}
		w.raw = make([]rsListeningCandidate, len(rows))
		for n, row := range rows {
			w.raw[n].row = row
		}
		w.source = slices.Clone(*source)
		w.fence = f
		w.bound = true
		return ctx.Err()
	}}
	for chunk := 0; chunk < rsListeningKeysPerPoll/2; chunk++ {
		steps = append(steps, func(ctx context.Context, q browseQuery, _ browseFence, _ *RSChildrenPage) error {
			if err := w.authority.CheckPreparation(w.lease); err != nil {
				return err
			}
			selected = nil
			maxBytes := int64(1)
			for n := w.index; n < min(w.index+2, len(w.raw)); n++ {
				c := &w.raw[n]
				rows, err := q("rs.children."+recipe.name+".sort_octets", c.row[0], c.row[2], in.Parent)
				if err != nil {
					return err
				}
				if len(rows) != 1 {
					return ErrStale
				}
				bytes, err := rsPointBytes(rows[0])
				if err != nil {
					return err
				}
				cap := min(rsListeningPreparationBytes/2, output.Request.Need[governor.MemoryBytes]/4)
				if bytes > (cap-w.bytes)/2 {
					return governor.ErrSnapshotBudget
				}
				w.bytes += 2 * bytes
				c.bytes = bytes
				selected = append(selected, n)
				maxBytes = max(maxBytes, bytes)
			}
			return windows.arm(maxBytes, 4)
		}, func(ctx context.Context, q browseQuery, _ browseFence, _ *RSChildrenPage) error {
			if err := w.authority.CheckPreparation(w.lease); err != nil {
				return err
			}
			for _, n := range selected {
				c := &w.raw[n]
				rows, err := q("rs.children."+recipe.name+".sort_key", c.row[0], c.row[2], in.Parent)
				if err != nil {
					return err
				}
				if len(rows) != 1 {
					return ErrStale
				}
				key := rows[0][0].(string)
				if int64(len(key)) != c.bytes {
					return ErrStale
				}
				c.key = key
				w.index++
			}
			return ctx.Err()
		})
	}
	steps = append(steps, func(ctx context.Context, _ browseQuery, f browseFence, p *RSChildrenPage) error {
		p.PreparedRows = len(w.raw)
		if w.index < len(w.raw) {
			p.Pending = true
			p.Preparing = true
			return nil
		}
		if !w.done {
			slices.SortFunc(w.raw, rsListeningCandidateCompare)
			w.done = true
		}
		for _, c := range w.raw {
			row := c.row
			if a := in.After; a != nil {
				order := cmp.Compare(row[17].(int64), a.first.Value)
				if order == 0 {
					order = cmp.Compare(row[18].(int64), a.second.Value)
				}
				if order == 0 {
					order = rsListeningKeyCompare(c.key, a.sortKey)
				}
				if order == 0 {
					order = cmp.Compare(row[0].(int64), a.id)
				}
				if order <= 0 {
					continue
				}
			}
			p.RawPositions++
			p.Next = &RSChildrenCursor{fence: f, parent: in.Parent, family: in.Family, limit: in.View.Limit, first: rsNumber(row[17]), second: rsNumber(row[18]), id: row[0].(int64), sortKey: strings.Clone(c.key), source: slices.Clone(w.source)}
			if row[2].(int64) != in.View.Library || row[3].(int64) != recipe.childKind || row[4].(int64) != 0 {
				continue
			}
			public, ok := row[1].([]byte)
			if !ok || len(public) != 16 {
				return ErrUnqualified
			}
			item := RSChild{ID: row[0].(int64), Kind: row[3].(int64), Revision: row[5].(int64), Disc: rsNumber(row[12]), Track: rsNumber(row[13]), Part: rsNumber(row[14]), Season: rsNumber(row[15]), Episode: rsNumber(row[16])}
			copy(item.PublicID[:], public)
			p.Items = append(p.Items, item)
			if len(p.Items) == in.View.Limit {
				p.Pending = true
				return ctx.Err()
			}
		}
		p.Exhausted = true
		p.Next = nil
		return ctx.Err()
	})
	page, result, err := publishRead(ctx, windows, request, budget, output, in.View, steps, root)
	if err != nil {
		w.failure = err
		_ = w.authority.CancelPreparation(w.lease)
		return RSChildrenPage{}, result, err
	}
	if !page.Preparing {
		if err = w.authority.ReadyPreparation(w.lease); err == nil {
			err = w.authority.HandoffPreparation(w.lease)
		}
		if err != nil {
			w.failure = err
			return RSChildrenPage{}, result, err
		}
		w.handed = true
	}
	return page, result, nil
}
func rsListeningCandidateCompare(a, b rsListeningCandidate) int {
	for _, pair := range [][2]int64{{a.row[17].(int64), b.row[17].(int64)}, {a.row[18].(int64), b.row[18].(int64)}} {
		if c := cmp.Compare(pair[0], pair[1]); c != 0 {
			return c
		}
	}
	if c := rsListeningKeyCompare(a.key, b.key); c != 0 {
		return c
	}
	return cmp.Compare(a.row[0].(int64), b.row[0].(int64))
}
