package read

import (
	"context"
	"errors"
	"slices"

	"github.com/PorticoMediaServer/portico/server/internal/store/search"
)

var errNativeCandidateBudget = errors.New("native preview candidate budget reached")

// Exact and preview discovery are different types of evidence. Preview never
// manufactures an exact population value to get through the strict reducer.
type nativeSearchLead struct {
	exact   *exactTermPopulation
	preview string
}

func (l nativeSearchLead) term() string {
	if l.exact != nil {
		return l.exact.term
	}
	return l.preview
}
func (l nativeSearchLead) prove(scope *fullQueryScope, key fullResultKey, sources []candidateSource, complete bool) (canonicalCandidateProof, bool, error) {
	if l.exact != nil && l.preview == "" {
		return proveCandidate(scope, key, *l.exact, sources, complete)
	}
	if l.exact == nil && l.preview != "" {
		return provePreviewCandidate(scope, key, l.preview, sources, complete)
	}
	return canonicalCandidateProof{}, false, ErrInput
}

type nativeCandidateWork struct{ used, limit int }

func (w *nativeCandidateWork) consume(n int) error {
	if w == nil || n < 0 {
		return ErrInput
	}
	if w.limit > 0 && n > w.limit-w.used {
		return errNativeCandidateBudget
	}
	w.used += n
	return nil
}

// Preview streams verified conjunctions immediately; no population census
// precedes it. A fixed BINARY discovery term is not called the rarest term.
// Each poll retains at most this window's bounded top20; RS owns bounded per-
// group top8/top40 across polls and can stop without exhausting the library.
func nativePreviewAdvance(ctx context.Context, q browseQuery, scope *fullQueryScope, f nativeSearchFence, in FullSearchInput, calls *int) (FullSearchPage, error) {
	if scope == nil || scope.nativeWork == nil {
		return FullSearchPage{}, ErrInput
	}
	scope.preview = true
	// A preview cursor carries native positions (slot, after, expansion), not a
	// rank, so it is not handed to the top-k as a rank bound. It must still be
	// bound to this exact query, bank, revision and input before any position
	// is reused, as on the exact path.
	if in.After != nil && !in.After.boundTo(scope) {
		return FullSearchPage{}, ErrStale
	}
	top, err := newFullSearchTop(scope, in.View.Limit, nil)
	if err != nil {
		return FullSearchPage{}, err
	}
	state := &nativeContinuation{fence: f, slot: 1, limit: in.View.Limit, preview: true, previewIncomplete: f.pending > 0}
	if in.After != nil {
		if in.After.native == nil || !in.After.native.preview {
			return FullSearchPage{}, ErrStale
		}
		state = cloneNativeContinuation(in.After.native)
		if state.slot < 1 || state.slot > 6 {
			return FullSearchPage{}, ErrStale
		}
		if err := validNativeSeen(state.seen); err != nil {
			return FullSearchPage{}, err
		}
	}
	lead := nativeSearchLead{preview: scope.terms[0]}
	finish := func(exhausted, budget bool) (FullSearchPage, error) {
		// One entity can be found through several postings in different polls;
		// emit it once per walk. The seen keys are bounded by the candidate
		// allowance, and the same bound ends the walk if it would be exceeded.
		fresh := make([]FullSearchHit, 0, len(top.hits))
		for _, hit := range top.hits {
			key := nativeSeenKey{hit.Subject, hit.ID}
			if slices.Contains(state.seen, key) {
				continue
			}
			if len(state.seen) >= NativePreviewCandidates {
				budget = true
				break
			}
			state.seen = append(state.seen, key)
			fresh = append(fresh, hit)
		}
		// The bounded top-k drops matches beyond the page limit inside one window
		// and the walk never revisits them, so such a walk can never claim to be
		// exhaustive (the flag travels in the cursor to later polls).
		if top.more {
			state.previewIncomplete = true
		}
		// A hit dropped at the seen bound makes this window incomplete even on
		// the last poll, so completeness is decided after the loop.
		complete := exhausted && !state.previewIncomplete && !budget
		page := FullSearchPage{Disposition: SearchResults, Hits: fresh, Partial: !complete, Exhausted: complete, CountStatus: SearchCountUnknown}
		if len(page.Hits) > 0 {
			page.CountStatus = SearchCountLowerBound
			page.Count = uint64(len(page.Hits))
		}
		if !exhausted && !budget {
			state.top = nil
			state.more = false
			page.Next = &FullSearchCursor{query: scope.query.Fold(), bank: scope.bank, revision: scope.revision, input: scope.input, people: scope.peopleOnly, native: state}
		}
		return ownFullSearchPage(page, in.View.Limit)
	}
	startCalls := *calls
	for steps := 0; steps < 8 && *calls-startCalls < 128; steps++ {
		if err := ctx.Err(); err != nil {
			return FullSearchPage{}, err
		}
		if scope.nativeWork.limit > 0 && scope.nativeWork.used >= scope.nativeWork.limit {
			return finish(false, true)
		}
		if state.expansion == nil {
			if state.slot == 6 {
				return finish(true, false)
			}
			id, done, e := nativePosting(q, lead.term(), state.slot, state.after)
			if errors.Is(e, errNativeCandidateBudget) {
				return finish(false, true)
			}
			if e != nil {
				return FullSearchPage{}, e
			}
			if done {
				state.slot++
				state.after = 0
				continue
			}
			state.after = id
			source, field, e := nativePostingSource(q, scope, state.slot, id)
			if errors.Is(e, errFullSearchPending) {
				state.previewIncomplete = true
				continue
			}
			if e != nil {
				return FullSearchPage{}, e
			}
			if source == nil {
				continue
			}
			allowed, _, e := nativeEligible(q, in.View, f.permission, source)
			if errors.Is(e, errFullSearchPending) {
				state.previewIncomplete = true
				continue
			}
			if e != nil {
				return FullSearchPage{}, e
			}
			if !allowed {
				continue
			}
			state.expansion = &nativeExpansion{source: source.key, field: field}
		}
		done, e := nativeExpandOne(ctx, q, scope, f.permission, in.View, lead, state.expansion, top)
		if errors.Is(e, errNativeCandidateBudget) {
			return finish(false, true)
		}
		if errors.Is(e, errFullSearchPending) {
			state.previewIncomplete = true
			state.expansion = nil
			continue
		}
		if e != nil {
			return FullSearchPage{}, e
		}
		if done {
			state.expansion = nil
		}
	}
	return finish(false, scope.nativeWork.limit > 0 && scope.nativeWork.used >= scope.nativeWork.limit)
}

// validNativeSeen refuses a continuation whose emitted-key set could not have
// been issued by this reader: over the candidate allowance, a subject that is
// neither entity nor person, a non-positive ID, or a repeated key.
func validNativeSeen(seen []nativeSeenKey) error {
	if len(seen) > NativePreviewCandidates {
		return ErrStale
	}
	unique := make(map[nativeSeenKey]struct{}, len(seen))
	for _, key := range seen {
		if key.id <= 0 || key.subject != search.ManifestEntity && key.subject != search.ManifestPerson {
			return ErrStale
		}
		if _, dup := unique[key]; dup {
			return ErrStale
		}
		unique[key] = struct{}{}
	}
	return nil
}
