package read

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/searchpolicy"
	"github.com/PorticoMediaServer/portico/server/internal/store/search"
)

// Continuations retain only bounded values. No connection, rows, receipt,
// query-wide seen set or permission survives a read window.
type nativeContinuation struct {
	fence          nativeSearchFence
	term           int
	slot           uint8
	after          int64
	counts         []uint64
	populationDone bool
	expansion      *nativeExpansion
	top            []nativeRankHit
	more           bool
	limit          int
}

// Pending cursors retain rank coordinates only, never a held candidate page.
// Display payload is hydrated on the completing operation under the SAME fence.
type nativeRankHit struct {
	subject           search.ManifestSubject
	id, library, kind int64
	tier, subtier     uint8
}
type nativeExpansion struct {
	source      search.FieldKey
	field       uint16
	direct      bool
	relation    int
	cursor      *ownerCursor
	album       int64
	albumCursor *ownerCursor
	automatic   *nativeAutomaticCursor
}
type nativeSearchFence struct {
	permission                         browseFence
	revision, bank, input, pending     int64
	building                           int64
	profile                            searchpolicy.Profile
	state, engineVersion, engineSource string
}

func cloneNativeContinuation(p *nativeContinuation) *nativeContinuation {
	if p == nil {
		return nil
	}
	n := *p
	n.counts = slices.Clone(p.counts)
	n.top = slices.Clone(p.top)
	if p.expansion != nil {
		e := *p.expansion
		if e.cursor != nil {
			c := *e.cursor
			e.cursor = &c
		}
		if e.albumCursor != nil {
			c := *e.albumCursor
			e.albumCursor = &c
		}
		if e.automatic != nil {
			c := *e.automatic
			e.automatic = &c
		}
		n.expansion = &e
	}
	return &n
}

func nativeReadFence(query browseQuery, in BrowseInput) (nativeSearchFence, SearchDisposition, error) {
	var n nativeSearchFence
	var err error
	if in.Kind == 0 {
		n.permission, err = readLibraryFence(query, in)
	} else {
		n.permission, err = readBrowseFence(query, in)
	}
	if err != nil {
		return n, 0, err
	}
	rows, err := query("full.native.revision", int64(1))
	if err != nil {
		return n, 0, err
	}
	if len(rows) != 1 || len(rows[0]) != 4 {
		return n, SearchUnavailable, nil
	}
	r := rows[0]
	rev, ok := r[0].(int64)
	input, ok2 := r[3].(int64)
	active, ok3 := r[1].(int64)
	if !ok || !ok2 || !ok3 || rev <= 0 || active <= 0 || input < 0 {
		return n, SearchUnavailable, nil
	}
	n.revision, n.bank, n.input = rev, active, input
	if r[2] != nil {
		var ok bool
		n.building, ok = r[2].(int64)
		if !ok {
			return n, 0, ErrUnqualified
		}
	}
	rows, err = query("full.native.bank", "v1")
	if err != nil {
		return n, 0, err
	}
	if len(rows) != 1 || len(rows[0]) != 7 {
		return n, SearchUnavailable, nil
	}
	b := rows[0]
	if b[0] != active || b[1] != "v1" {
		return n, SearchUnavailable, nil
	}
	recipe, a := b[2].(string)
	engine, bok := b[3].(string)
	fingerprint, c := b[4].(string)
	state, d := b[5].(string)
	pending, e := b[6].(int64)
	if !a || !bok || !c || !d || !e || pending < 0 {
		return n, 0, ErrUnqualified
	}
	n.profile = searchpolicy.Profile{RecipeVersion: recipe, EngineVersion: engine, UnicodeFingerprint: fingerprint}
	n.state, n.pending = state, pending
	rows, err = query("full.native.engine", int64(1))
	if err != nil {
		return n, 0, err
	}
	if len(rows) != 1 || len(rows[0]) != 2 {
		return n, SearchUnavailable, nil
	}
	n.engineVersion, a = rows[0][0].(string)
	n.engineSource, bok = rows[0][1].(string)
	if !a || !bok {
		return n, 0, ErrUnqualified
	}
	if searchpolicy.Compare(n.profile, searchpolicy.Engine{Version: n.engineVersion, SourceID: n.engineSource}).State != searchpolicy.Current || n.state != "active" {
		return n, SearchUnavailable, nil
	}
	if n.pending != 0 {
		return n, SearchPending, nil
	}
	return n, SearchResults, nil
}

// nativeWindow is the single LIVE operation's private two-view driver. The
// complete finite recipe declares its call bound, keeping the conservative
// native row floor. It never borrows retained-output memory for active SQL.
func (r *NativeFullSearchReader) nativeWindow(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in FullSearchInput, query search.Query) (page FullSearchPage, cost BrowseReadResult, err error) {
	started := time.Now()
	defer func() { cost.Elapsed = time.Since(started) }()
	if r.snapshots == nil || output.live == nil || in.View.After != nil || in.View.Library <= 0 || in.View.Limit < 1 || in.View.Limit > MaxSearchTitles || in.View.Database == "" || len(in.View.Database) > 256 || in.View.TokenHash == "" || len(in.View.TokenHash) > 256 || in.View.policy != (readPolicyBinding{}) {
		return page, cost, ErrInput
	}
	// One maximum complete candidate proof can take many point reads. This is
	// an explicit recipe requirement, never an implicit grant enlargement.
	if request.Budget.RawRows < 4096 || request.Need[governor.MemoryBytes]-r.snapshots.NativeRowReservation() < 4<<20 || output.Request.Need[governor.MemoryBytes] < 8<<20 {
		return page, cost, governor.ErrSnapshotBudget
	}
	deadline := earliest(request.Deadline, output.Request.Deadline, started.Add(min(budget, time.Second)))
	request.Deadline = deadline
	var initial nativeSearchFence
	var ready bool
	var draft FullSearchPage
	var copies int64
	rawPositions := 0
	calls := 0
	lookup, lookupErr := r.snapshots.ReadForecasted(ctx, request, time.Until(deadline), governor.SnapshotForecast{NativeRowBytes: r.snapshots.NativeRowReservation(), MaxQueries: 4096}, func(scope *governor.ReadScope) error {
		q := func(name string, args ...any) ([][]any, error) {
			calls++
			cost.LookupStatements++
			if e := output.live.checkContext(scope.Context()); e != nil {
				return nil, e
			}
			rows, e := scope.Query(readStatementID(name), args...)
			if e != nil {
				return nil, e
			}
			if strings.HasPrefix(name, "full.") && name != "full.native.revision" && name != "full.native.bank" && name != "full.native.engine" {
				rawPositions += len(rows)
				if rawPositions > 512 {
					return nil, governor.ErrSnapshotBudget
				}
			}
			copies += 4096
			for _, row := range rows {
				copies += 64 + int64(len(row))*32
				for _, v := range row {
					switch v := v.(type) {
					case string:
						copies += int64(len(v)) * 2
					case []byte:
						copies += int64(len(v)) * 3
					}
				}
			}
			// Half of the Go-copy grant remains for reducer/matcher/owner workspace.
			if copies > 2<<20 {
				return nil, governor.ErrSnapshotBudget
			}
			return rows, nil
		}
		var disposition SearchDisposition
		initial, disposition, err = nativeReadFence(q, in.View)
		if err != nil {
			return err
		}
		ready = true
		if in.After != nil && in.After.native != nil && (in.After.native.fence != initial || in.After.native.limit != in.View.Limit) {
			return ErrStale
		}
		if disposition != SearchResults {
			draft = FullSearchPage{Disposition: disposition}
			return nil
		}
		scopeQuery, e := newFullQueryScope(query, initial.bank, initial.revision, initial.input)
		if e != nil {
			return e
		}
		draft, e = nativeAdvance(scope.Context(), q, scopeQuery, initial, in, &calls)
		if errors.Is(e, errFullSearchPending) {
			draft = FullSearchPage{Disposition: SearchPending}
			return nil
		}
		return e
	})
	cost.Lookup = lookup
	cost.LookupWindows = append(cost.LookupWindows, lookup)
	cost.Attempts = 1
	if lookup.Refusal != nil {
		cost.Refusal = lookup.Refusal
		return FullSearchPage{}, cost, governor.ErrBusy
	}
	if !ready {
		return FullSearchPage{}, cost, lookupErr
	}
	if !time.Now().Before(deadline) {
		return FullSearchPage{}, cost, errors.Join(lookupErr, ctx.Err(), context.DeadlineExceeded)
	}
	// The fresh view performs fences ONLY. Cancellation cannot release a page;
	// an observed security denial takes precedence over simultaneous cancellation.
	finalCtx, cancel := context.WithDeadline(context.WithoutCancel(ctx), deadline)
	defer cancel()
	finalIn := in.View
	finalIn.Now = in.View.Now.Add(time.Since(started))
	publication, finalErr := r.snapshots.Read(finalCtx, request, time.Until(deadline), func(scope *governor.ReadScope) error {
		q := func(name string, args ...any) ([][]any, error) {
			cost.PublicationStatements++
			if e := output.live.checkContext(scope.Context()); e != nil {
				return nil, e
			}
			return scope.Query(readStatementID(name), args...)
		}
		current, _, e := nativeReadFence(q, finalIn)
		if e != nil {
			return e
		}
		if current != initial {
			return ErrStale
		}
		return nil
	})
	cost.Publication = publication
	cost.PublicationWindows = append(cost.PublicationWindows, publication)
	if publication.Refusal != nil {
		cost.Refusal = publication.Refusal
		finalErr = governor.ErrBusy
	}
	if errors.Is(finalErr, ErrDenied) || errors.Is(finalErr, ErrUnqualified) {
		return FullSearchPage{}, cost, finalErr
	}
	if errors.Is(finalErr, ErrStale) || errors.Is(lookupErr, ErrStale) {
		cost.Refusal = &governor.Refusal{Code: governor.Conflict}
		return FullSearchPage{}, cost, errors.Join(ErrStale, governor.ErrBusy)
	}
	if finalErr == nil {
		at := in.View.Now.Add(time.Since(started))
		expires, e := time.Parse(time.RFC3339Nano, initial.permission.expiresAt)
		horizon, e2 := time.Parse(time.RFC3339Nano, initial.permission.horizon)
		if e != nil || e2 != nil || !at.Before(expires) || !at.Before(horizon) {
			return FullSearchPage{}, cost, ErrDenied
		}
	}
	if e := errors.Join(lookupErr, finalErr, ctx.Err(), finalCtx.Err(), output.live.checkContext(ctx)); e != nil {
		return FullSearchPage{}, cost, e
	}
	if !time.Now().Before(deadline) {
		return FullSearchPage{}, cost, context.DeadlineExceeded
	}
	draft.Stamp = &FullSearchReadStamp{pool: r.pool, output: output.Grant, fence: initial, query: query.Fold(), started: started, clock: in.View.Now, deadline: deadline}
	return draft, cost, nil
}

func nativeAdvance(ctx context.Context, q browseQuery, scope *fullQueryScope, f nativeSearchFence, in FullSearchInput, calls *int) (FullSearchPage, error) {
	top, err := newFullSearchTop(scope, in.View.Limit, in.After)
	if err != nil {
		return FullSearchPage{}, err
	}
	state := &nativeContinuation{fence: f, slot: 1, counts: make([]uint64, len(scope.terms)), limit: in.View.Limit}
	if in.After != nil && in.After.native != nil {
		state = cloneNativeContinuation(in.After.native)
		if len(state.counts) != len(scope.terms) || state.term < 0 || state.term > len(scope.terms) || state.slot < 1 || state.slot > 6 {
			return FullSearchPage{}, ErrStale
		}
		for _, hit := range state.top {
			item := BrowseItem{}
			if hit.subject == search.ManifestEntity {
				item.ID = hit.id
			}
			top.hits = append(top.hits, FullSearchHit{Subject: hit.subject, ID: hit.id, Library: hit.library, Kind: hit.kind, Tier: hit.tier, PeopleSubtier: hit.subtier, Item: item})
		}
		top.more = state.more
	}
	finish := func() (FullSearchPage, error) {
		for i, hit := range top.hits {
			if hit.Subject == search.ManifestPerson {
				source, err := nativePersonSource(q, scope, hit.ID)
				if err != nil {
					return FullSearchPage{}, err
				}
				if source == nil {
					return FullSearchPage{}, ErrStale
				}
				visible, err := nativePersonVisible(q, scope, in.View, f.permission, hit.ID)
				if err != nil {
					return FullSearchPage{}, err
				}
				if !visible {
					return FullSearchPage{}, ErrStale
				}
				top.hits[i].PersonName = source.title
				continue
			}
			source, err := nativeEntitySource(q, scope, hit.ID)
			if err != nil {
				return FullSearchPage{}, err
			}
			if source == nil || source.kind != hit.Kind {
				return FullSearchPage{}, ErrStale
			}
			allowed, _, err := nativeEligible(q, in.View, f.permission, source)
			if err != nil {
				return FullSearchPage{}, err
			}
			if !allowed {
				return FullSearchPage{}, ErrStale
			}
			top.hits[i].Item, err = itemValue(source.item[:7])
			if err != nil {
				return FullSearchPage{}, err
			}
		}
		page, err := top.finish(true)
		if err == nil && page.Next != nil {
			// The next rank page may reuse exact counts only under this SAME
			// full fence, and starts a fresh candidate stream with no retained top.
			page.Next.native = &nativeContinuation{fence: f, slot: 1, counts: slices.Clone(state.counts), populationDone: true, limit: in.View.Limit}
		}
		return page, err
	}
	for steps := 0; steps < 8 && *calls < 128; steps++ {
		if err := ctx.Err(); err != nil {
			return FullSearchPage{}, err
		}
		if !state.populationDone {
			id, done, e := nativePosting(q, scope.terms[state.term], state.slot, state.after)
			if e != nil {
				return FullSearchPage{}, e
			}
			if done {
				state.slot++
				state.after = 0
				if state.slot == 6 {
					state.term++
					state.slot = 1
					if state.term == len(scope.terms) {
						state.populationDone = true
						state.term = 0
					}
				}
				continue
			}
			source, field, e := nativePostingSource(q, scope, state.slot, id)
			if e != nil {
				return FullSearchPage{}, e
			}
			if source != nil {
				allowed, _, e := nativeEligible(q, in.View, f.permission, source)
				if e != nil {
					return FullSearchPage{}, e
				}
				if allowed {
					proof, first, e := nativeLogicalField(ctx, q, scope, source, field, state.term)
					if e != nil {
						return FullSearchPage{}, e
					}
					if proof.matched[state.term] && first == (fullPostingKey{state.slot, id}) {
						if state.counts[state.term] == ^uint64(0) {
							return FullSearchPage{}, ErrUnqualified
						}
						state.counts[state.term]++
					}
				}
			}
			state.after = id
			continue
		}
		populations := make([]exactTermPopulation, len(scope.terms))
		for i, term := range scope.terms {
			populations[i] = exactTermPopulation{scope, term, state.counts[i]}
		}
		lead, e := chooseLead(scope, populations)
		if e != nil {
			return FullSearchPage{}, e
		}
		if lead.count == 0 {
			return finish()
		}
		leadIndex := slices.Index(scope.terms, lead.term)
		if state.expansion == nil {
			if state.slot == 6 {
				return finish()
			}
			id, done, e := nativePosting(q, lead.term, state.slot, state.after)
			if e != nil {
				return FullSearchPage{}, e
			}
			if done {
				state.slot++
				state.after = 0
				continue
			}
			source, field, e := nativePostingSource(q, scope, state.slot, id)
			if e != nil {
				return FullSearchPage{}, e
			}
			state.after = id
			if source == nil {
				continue
			}
			allowed, _, e := nativeEligible(q, in.View, f.permission, source)
			if e != nil {
				return FullSearchPage{}, e
			}
			if !allowed {
				continue
			}
			proof, first, e := nativeLogicalField(ctx, q, scope, source, field, leadIndex)
			if e != nil {
				return FullSearchPage{}, e
			}
			if !proof.matched[leadIndex] || first != (fullPostingKey{state.slot, id}) {
				continue
			}
			state.expansion = &nativeExpansion{source: proof.key, field: field}
		}
		done, e := nativeExpandOne(ctx, q, scope, f.permission, in.View, lead, state.expansion, top)
		if e != nil {
			return FullSearchPage{}, e
		}
		if done {
			state.expansion = nil
		}
	}
	state.top = nil
	for _, hit := range top.hits {
		state.top = append(state.top, nativeRankHit{hit.Subject, hit.ID, hit.Library, hit.Kind, hit.Tier, hit.PeopleSubtier})
	}
	state.more = top.more
	cursor := &FullSearchCursor{query: scope.query.Fold(), bank: scope.bank, revision: scope.revision, input: scope.input, native: state}
	if in.After != nil {
		cursor.after = in.After.after
	}
	page := FullSearchPage{Disposition: SearchPending, Next: cursor}
	owned, e := ownFullSearchPage(page, in.View.Limit)
	if e != nil {
		return FullSearchPage{}, fmt.Errorf("native pending: %w", e)
	}
	return owned, nil
}
