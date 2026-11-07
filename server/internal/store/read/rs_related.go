package read

import (
	"context"
	"errors"
	"math"
	"slices"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

const (
	rsRelatedFeatures         = 256
	rsRelatedCandidates       = 2048
	rsRelatedBytes      int64 = 4 << 20 // includes state, clone/DTO overlap and Go map slack
	rsRelatedAlgorithm        = 1
)

var ErrRelatedCapacity = errors.New("related exact work exceeds the admitted recipe")

// RelatedKey is source identity, not a provider ID, label or permission proof.
// Kind: 0 More like, 1 normalized term, 2 current credited person, 3 authored
// collection, 4 direct parent, 5 show, 6 artist, 7 local identity generator.
// Credit ordinals refer to Image; the current sealed head is checked separately.
type RelatedKey struct {
	Kind               uint8
	ID, Image, Ordinal int64
}
type RelatedInput struct {
	View    BrowseInput
	Item    int64
	Row     *RelatedKey // nil requests the title's matrix; Similar always selects More like
	After   *RelatedCursor
	Windows int // 1..64 bounded business quanta per poll; no deadline extension
}
type RelatedCursor struct {
	fence     browseFence
	source    []any
	item      int64
	row       RelatedKey
	shared    int
	quality   float64
	id        int64
	algorithm int
}

// RelatedEntry is a hydrated normalized recommendation, not a final card copy.
// Text/art projection uses its public identity/revisions and the existing
// exact-byte card points below. Only selected cards copy variable text.
type RelatedEntry struct {
	ID                             int64
	Title                          string
	PublicID                       [16]byte
	Kind, Revision, Year           int64
	Quality                        float64
	Shared                         int
	Watched, Favorite, Watchlisted bool
	PersonalRevision               int64
	Reasons                        []RelatedKey
}
type RelatedRow struct {
	Key       RelatedKey
	Entries   []RelatedEntry
	Total     int // exact eligible selected-set size, never the candidate prefix
	Exhausted bool
	Next      *RelatedCursor
}
type RelatedPage struct {
	Pending      bool
	Rows         []RelatedRow
	RawPositions int // this poll, including denied, off-head and duplicate rows
	Algorithm    int
}

type relatedEntity struct{ row []any }

func (e relatedEntity) id() int64    { return e.row[0].(int64) }
func (e relatedEntity) kind() int64  { return e.row[3].(int64) }
func (e relatedEntity) image() int64 { return relatedInt(e.row[14]) }
func (e relatedEntity) work() int64 {
	switch e.kind() {
	case 3, 4:
		if n := relatedInt(e.row[19]); n > 0 {
			return n
		}
	case 7:
		if n := relatedInt(e.row[20]); n > 0 {
			return n
		}
	case 9:
		if n := relatedInt(e.row[21]); n > 0 {
			return n
		}
	}
	return e.id()
}
func relatedInt(v any) int64 {
	if v == nil {
		return 0
	}
	return v.(int64)
}
func (e relatedEntity) eligible(f browseFence) bool {
	if len(e.row) != 33 || e.row[2] != f.libraryID || e.row[4] != int64(0) {
		return false
	}
	if relatedInt(e.row[7]) != 0 && (f.maximumAge >= 0 || f.allowUnrated == 0) {
		return false
	}
	if e.row[6] == nil {
		return f.allowUnrated != 0
	}
	return f.maximumAge < 0 || e.row[6].(int64) <= f.maximumAge
}
func (e relatedEntity) negative() bool {
	return relatedInt(e.row[24]) != 0 || e.row[25] != nil && e.row[25].(int64) <= 4
}
func relatedEntityRead(q browseQuery, profile string, id int64) (relatedEntity, error) {
	rows, err := q("rs.related.entity", profile, id)
	if err != nil {
		return relatedEntity{}, err
	}
	if len(rows) == 0 {
		return relatedEntity{}, nil
	}
	if len(rows) != 1 || len(rows[0]) != 33 {
		return relatedEntity{}, ErrUnqualified
	}
	e := relatedEntity{rows[0]}
	if len(e.row[1].([]byte)) != 16 || len(e.row[10].([]byte)) != 16 || math.IsNaN(e.row[9].(float64)) || math.IsInf(e.row[9].(float64), 0) {
		return relatedEntity{}, ErrUnqualified
	}
	return e, nil
}

type relatedFeature struct {
	key                      RelatedKey
	mode                     uint8
	provider                 bool
	person                   int64
	providerName, providerID string
}
type relatedCandidate struct {
	entity  relatedEntity
	bits    [4]uint64
	visible bool
}
type relatedProbe struct {
	id, asset, child int64
	members          []int64
	exhausted        bool
	entity           relatedEntity
	stage            int
}
type relatedState struct {
	fence                       browseFence
	source                      []any
	roots                       []relatedEntity
	features                    []relatedFeature
	candidates                  map[int64]*relatedCandidate
	phase, rootIndex, enumStage int
	after                       int64
	featureIndex                int
	position                    [3]int64
	raw                         [][]any
	rawIndex                    int
	// credit normalization is a bounded sequence of PK comparisons, never a
	// GROUP BY over a broad credited name. All comparisons are resumable.
	normalizeI, normalizeJ int
	duplicate              []bool
	ids                    []int64
	hydrate                int
	candidateID            int64
	probe                  []relatedProbe
	probeSeen              map[int64]bool
	current                *relatedCandidate
	cards                  map[int64]string
	cardIDs                []int64
	cardIndex              int
	cardBytes              int64
	sourceByte             bool
	waitByte               bool
	done                   bool
	rawTotal               int
}

// RelatedWork is RB-owned preparation payload. It owns neither a scheduler nor
// a cache/ledger. Trusted composition holds this object until Close, attaches
// identical polls to the same object, and supplies a SERVER job context.
// The single RB lease charges all retained source/reducer data across polls.
// No SQL cursor, snapshot, grant or variable metadata value is retained here.
type RelatedWork struct {
	mu             sync.Mutex
	authority      *governor.Authority
	lease          *governor.PreparationLease
	in             RelatedInput
	similar        bool
	owner          governor.OwnerID
	state          relatedState
	closed, handed bool
}

// BeginRelatedWork uses RB's existing F-04 authority, conservatively declaring
// global source/personal dependencies. It cannot run with phase1a's profile.
// Ready and handoff do not refund retained memory; Close attests actual exit.
func BeginRelatedWork(ctx context.Context, a *governor.Authority, owner governor.OwnerID, key governor.JobKey, deadline time.Time, in RelatedInput, similar bool) (*RelatedWork, *governor.Refusal, error) {
	if a == nil || ctx == nil || !validRelated(in) {
		return nil, nil, ErrInput
	}
	decision := a.BeginPreparation(ctx, governor.PreparationRequest{Key: key, Owner: owner, Deadline: deadline, ReservedBytes: rsRelatedBytes + governor.PreparationMetadataBytes})
	if decision.Refusal != nil {
		return nil, decision.Refusal, governor.ErrBusy
	}
	in.View.After = nil
	if in.Row != nil {
		v := *in.Row
		in.Row = &v
	}
	if in.After != nil {
		v := *in.After
		v.source = slices.Clone(v.source)
		in.After = &v
	}
	return &RelatedWork{authority: a, lease: decision.Lease, in: in, similar: similar, owner: owner}, nil, nil
}
func (w *RelatedWork) Close() error {
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
	w.state = relatedState{}
	w.in = RelatedInput{}
	return errors.Join(err, w.authority.RetirePreparation(w.lease))
}
func validRelated(in RelatedInput) bool {
	return in.Item > 0 && in.Windows >= 1 && in.Windows <= 64 && in.View.After == nil && !in.View.Descending && !in.View.KeepArticles && in.View.Limit >= 1 && in.View.Limit <= MaxPage
}
func sameRelatedInput(a, b RelatedInput) bool {
	a.View.Now = b.View.Now
	if a.View != b.View || a.Item != b.Item {
		return false
	}
	if (a.Row == nil) != (b.Row == nil) || a.Row != nil && *a.Row != *b.Row {
		return false
	}
	if (a.After == nil) != (b.After == nil) {
		return false
	}
	if a.After != nil {
		x, y := a.After, b.After
		if x.fence != y.fence || x.item != y.item || x.row != y.row || x.shared != y.shared || x.quality != y.quality || x.id != y.id || x.algorithm != y.algorithm || !equalSource(x.source, y.source) {
			return false
		}
	}
	return true
}

// Related advances the complete title-related matrix or one row. Pending
// contains NO entries/total/cursor. The same separately charged Work resumes;
// exhausting a source budget never masquerades as an exact selected set.
func (r *ForegroundReader) Related(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in RelatedInput, w *RelatedWork) (RelatedPage, BrowseReadResult, error) {
	return r.relatedPublic(ctx, request, budget, output, in, w, false)
}
func (r *ForegroundReader) relatedPublic(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in RelatedInput, w *RelatedWork, similar bool) (RelatedPage, BrowseReadResult, error) {
	if r == nil || w == nil || !validRelated(in) {
		return RelatedPage{}, BrowseReadResult{}, ErrInput
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if request.Owner != w.owner || w.closed || w.handed || w.similar != similar || !sameRelatedInput(w.in, in) || output.Authority != w.authority {
		return RelatedPage{}, BrowseReadResult{}, ErrInput
	}
	return liveRead(ctx, request, budget, output, r.pool, in.View.Now, func(ctx context.Context, request governor.Request, budget time.Duration, now time.Time) (RelatedPage, BrowseReadResult, error) {
		in.View.Now = now
		return r.related(ctx, request, budget, output, in, w)
	})
}
func (r *ForegroundReader) related(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in RelatedInput, w *RelatedWork) (RelatedPage, BrowseReadResult, error) {
	if err := w.authority.CheckPreparation(w.lease); err != nil {
		return RelatedPage{}, BrowseReadResult{}, err
	}
	// The shared publication driver can retry a NEW whole capture. On drift a
	// partially retained job is invalidated instead; no stale prefix is reused.
	var rootCalls int
	var roots []relatedEntity
	root := func(q browseQuery) ([]any, error) {
		if err := w.authority.CheckPreparation(w.lease); err != nil {
			return nil, err
		}
		e, err := relatedEntityRead(q, "", in.Item)
		if err != nil {
			return nil, err
		}
		if e.row == nil {
			return nil, ErrDenied
		}
		v := []relatedEntity{e}
		if e.work() != e.id() {
			e, err = relatedEntityRead(q, "", e.work())
			if err != nil {
				return nil, err
			}
			if e.row == nil {
				return nil, ErrDenied
			}
			v = append(v, e)
		}
		var flat []any
		for _, e := range v {
			flat = append(flat, e.row...)
		}
		rootCalls++
		if rootCalls == 1 {
			roots = v
		} else if rootCalls > 2 {
			return nil, ErrStale
		}
		return flat, nil
	}
	byteWindows := &rsByteWindow{source: r.snapshots}
	w.state.sourceByte = false
	if w.state.phase == 5 {
		w.state.phase = 4
	}
	startRaw := w.state.rawTotal
	steps := []readStep[RelatedPage]{func(ctx context.Context, q browseQuery, f browseFence, p *RelatedPage) error {
		if err := w.authority.CheckPreparation(w.lease); err != nil {
			return err
		}
		var flat []any
		for _, e := range roots {
			if !e.eligible(f) {
				return ErrDenied
			}
			flat = append(flat, e.row...)
		}
		if roots[0].kind() != f.kind {
			return ErrDenied
		}
		if w.state.candidates == nil {
			w.state = relatedState{phase: -1, fence: f, source: flat, roots: roots, candidates: make(map[int64]*relatedCandidate)}
		} else if w.state.fence != f || !equalSource(w.state.source, flat) {
			return ErrStale
		}
		if in.After != nil && (in.After.algorithm != rsRelatedAlgorithm || in.After.fence != f || in.After.item != in.Item || !equalSource(in.After.source, flat)) {
			return ErrStale
		}
		return ctx.Err()
	}}
	for n := 0; n < in.Windows; n++ {
		steps = append(steps, func(ctx context.Context, q browseQuery, f browseFence, p *RelatedPage) error {
			for n := 0; n < 2 && !w.state.done; n++ {
				if err := errors.Join(ctx.Err(), w.authority.CheckPreparation(w.lease)); err != nil {
					return err
				}
				if w.state.phase >= 4 {
					if err := relatedCardQuantum(q, &w.state, in, w.similar, byteWindows); err != nil {
						return err
					}
				} else if err := relatedQuantum(q, &w.state, byteWindows); err != nil {
					return err
				}
				if w.state.waitByte {
					w.state.waitByte = false
					break
				}
			}
			return nil
		})
	}
	// Projection is bounded by six rows * forty entries * 256 numeric reasons.
	steps = append(steps, func(ctx context.Context, q browseQuery, f browseFence, p *RelatedPage) error {
		p.Pending = !w.state.done
		p.RawPositions = w.state.rawTotal - startRaw
		p.Algorithm = rsRelatedAlgorithm
		if p.Pending {
			return nil
		}
		rows, err := relatedRows(&w.state, in, w.similar)
		if err != nil {
			return err
		}
		p.Rows = rows
		return nil
	})
	page, result, err := publishRead(ctx, byteWindows, request, budget, output, in.View, steps, root)
	if err != nil {
		w.state = relatedState{}
		_ = w.authority.CancelPreparation(w.lease)
		return RelatedPage{}, result, err
	}
	if err = w.authority.CheckPreparation(w.lease); err != nil {
		return RelatedPage{}, result, err
	}
	if !page.Pending {
		if err = w.authority.ReadyPreparation(w.lease); err == nil {
			err = w.authority.HandoffPreparation(w.lease)
		}
		if err != nil {
			return RelatedPage{}, result, err
		}
		w.handed = true
	}
	return page, result, nil
}
