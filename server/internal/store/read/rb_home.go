package read

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/entityid"
	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

const HomeRecentMaximum = 100
const rbHomePolicy readPolicyID = 300
const rbHomeMemberWindows = 1

func init() {
	// This purpose admits a container only to the Home business verifier below.
	// The default catalogue recipe remains closed. Each emitted container needs
	// a currently allowed, available leaf; restricted chronology is leaf-derived.
	registerReadPolicy(readPolicyRecipe{id: rbHomePolicy, domain: "rb-home", kinds: []int64{1, 2, 3}, validate: func(_ browseQuery, in BrowseInput, _ browseFence) (string, error) {
		if in.policy.subject != 0 {
			return "", ErrInput
		}
		return "", nil
	}})
}

type HomeSource struct{ Library, Kind int64 }
type HomeTargetInput struct {
	View BrowseInput
	Work int64
}
type HomeRecentCard struct {
	Work, Target                  BrowseItem
	Library, WorkKind, TargetKind int64
	Added                         string
	// The complete bounded source was examined. This is not a provider edition
	// equivalence proof, an art choice, or playback authorization.
	EligibleLeaves int
}
type HomeRecentInput struct {
	View    BrowseInput
	Sources []HomeSource
}
type HomeRecentRow struct {
	Items []HomeRecentCard
	// Exhausted means the selected shelf (at most100 works), not the libraries.
	Exhausted bool
	RawRows   int
}

// HomeRecentTarget proves a work's complete bounded leaf set before choosing
// show/season/episode navigation. A full final raw window fails closed rather
// than guessing the delivery or leaking a hidden child's timestamp.
func (r *BrowseReader) HomeRecentTarget(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in HomeTargetInput) (HomeRecentCard, BrowseReadResult, error) {
	if r == nil {
		return HomeRecentCard{}, BrowseReadResult{}, ErrInput
	}
	return liveRead(ctx, request, budget, output, r.pool, in.View.Now, func(ctx context.Context, request governor.Request, budget time.Duration, at time.Time) (HomeRecentCard, BrowseReadResult, error) {
		in.View.Now = at
		return r.homeRecentTarget(ctx, request, budget, output, in)
	})
}
func homeView(in BrowseInput) BrowseInput {
	in.policy = readPolicyBinding{recipe: rbHomePolicy}
	return in
}
func validHomeView(in BrowseInput) bool { return in.After == nil && !in.Descending && !in.KeepArticles }
func (r *BrowseReader) homeRecentTarget(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in HomeTargetInput) (HomeRecentCard, BrowseReadResult, error) {
	if in.Work <= 0 || !validHomeView(in.View) || in.View.Kind < 1 || in.View.Kind > 3 {
		return HomeRecentCard{}, BrowseReadResult{}, ErrInput
	}
	in.View = homeView(in.View)
	var source []any
	managed := false
	root := func(q browseQuery) ([]any, error) {
		rows, e := q("rb.home.item", in.Work, in.View.Library, in.View.Kind)
		if e != nil {
			return nil, e
		}
		if len(rows) != 1 {
			return nil, ErrDenied
		}
		source = rows[0]
		envelope, e := homeLibraryRows(q, in.View.TokenHash, in.View.Library)
		if e != nil {
			return nil, e
		}
		managed = envelope[0][12] != nil
		all := append([]any(nil), rows[0]...)
		for _, row := range envelope {
			all = append(all, row...)
		}
		return all, nil
	}
	return publishRead(ctx, r.snapshots, request, budget, output, in.View, []readStep[HomeRecentCard]{
		func(_ context.Context, _ browseQuery, _ browseFence, _ *HomeRecentCard) error { return nil },
		func(ctx context.Context, q browseQuery, f browseFence, d *HomeRecentCard) error {
			card, _, e := homeTarget(ctx, q, f, source, managed)
			if e != nil {
				return e
			}
			if card == nil {
				return ErrDenied
			}
			*d = *card
			return nil
		},
	}, root)
}

// HomeRecentRow composes an exact top100 over an explicitly selected set of
// small source streams. It deliberately refuses a non-exhausted source window.
// Large/sparse streams belong to the parent's resumable preparation, which can
// use ChronologyWindow and HomeRecentTarget without truncating their domain.
// Up to three libraries fit the fresh shared publication fence's eight calls.
// This is an entity-work draft: provider edition equivalence is still pending.
func (r *BrowseReader) HomeRecentRow(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in HomeRecentInput) (HomeRecentRow, BrowseReadResult, error) {
	if r == nil {
		return HomeRecentRow{}, BrowseReadResult{}, ErrInput
	}
	return liveRead(ctx, request, budget, output, r.pool, in.View.Now, func(ctx context.Context, request governor.Request, budget time.Duration, at time.Time) (HomeRecentRow, BrowseReadResult, error) {
		in.View.Now = at
		return r.homeRecentRow(ctx, request, budget, output, in)
	})
}
func (r *BrowseReader) homeRecentRow(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in HomeRecentInput) (HomeRecentRow, BrowseReadResult, error) {
	if !validHomeView(in.View) || len(in.Sources) == 0 || len(in.Sources) > 8 {
		return HomeRecentRow{}, BrowseReadResult{}, ErrInput
	}
	sources := slices.Clone(in.Sources)
	slices.SortFunc(sources, func(a, b HomeSource) int {
		if a.Library < b.Library {
			return -1
		}
		if a.Library > b.Library {
			return 1
		}
		return int(a.Kind - b.Kind)
	})
	var libraries []int64
	for n, s := range sources {
		if s.Library <= 0 || s.Kind != 1 && s.Kind != 2 || n > 0 && sources[n-1] == s {
			return HomeRecentRow{}, BrowseReadResult{}, ErrInput
		}
		if !slices.Contains(libraries, s.Library) {
			libraries = append(libraries, s.Library)
		}
	}
	if len(libraries) > 3 {
		return HomeRecentRow{}, BrowseReadResult{}, ErrUnqualified
	}
	in.View = homeView(in.View)
	in.View.Library = sources[0].Library
	in.View.Kind = sources[0].Kind
	return r.homeRecentCompose(ctx, request, budget, output, in, sources, libraries)
}

// Two bounded stages let source cardinality determine the number of target
// windows. Both use the merged publication attempt, share ONE copy counter and
// original deadline, and retry the WHOLE composition at most once.
func (r *BrowseReader) homeRecentCompose(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in HomeRecentInput, sources []HomeSource, libraries []int64) (page HomeRecentRow, result BrowseReadResult, err error) {
	started := time.Now()
	defer func() { result.Elapsed = time.Since(started) }()
	if ctx == nil || budget <= 0 {
		return page, result, ErrInput
	}
	deadline := started.Add(min(budget, time.Second))
	if request.Deadline.Before(deadline) {
		deadline = request.Deadline
	}
	request.Deadline = deadline
	var copied int64
	var principal publicationPrincipalPin
	for n := 0; n < 2; n++ {
		current := in
		current.View.Now = in.View.Now.Add(time.Since(started))
		var one BrowseReadResult
		page, one, err = r.homeRecentAttempt(ctx, request, time.Until(deadline), output, current, sources, libraries, &copied, &principal)
		mergeHomeReadResult(&result, one)
		result.Attempts = n + 1
		if !errors.Is(err, ErrStale) || ctx.Err() != nil || !time.Now().Before(deadline) {
			break
		}
	}
	if errors.Is(err, ErrStale) {
		result.Refusal = &governor.Refusal{Code: governor.Conflict}
		err = errors.Join(err, governor.ErrBusy)
	}
	return
}

func (r *BrowseReader) homeRecentAttempt(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in HomeRecentInput, sources []HomeSource, libraries []int64, copied *int64, principal *publicationPrincipalPin) (HomeRecentRow, BrowseReadResult, error) {
	started := time.Now()
	deadline := homeMinTime(request.Deadline, started.Add(budget))
	type candidate struct {
		id     int64
		source HomeSource
	}
	var candidates []candidate
	secondary := map[int64][]any{}
	managed := map[int64]bool{}
	fences := map[int64]browseFence{}
	var initial browseFence
	var rootImage []any
	root := func(q browseQuery) ([]any, error) {
		var all []any
		for _, library := range libraries {
			rows, e := homeLibraryRows(q, in.View.TokenHash, library)
			if e != nil {
				return nil, e
			}
			secondary[library] = rows[0]
			managed[library] = rows[0][12] != nil
			for _, row := range rows {
				all = append(all, row...)
			}
		}
		rootImage = all
		return all, nil
	}
	steps := []readStep[HomeRecentRow]{func(_ context.Context, _ browseQuery, f browseFence, _ *HomeRecentRow) error {
		initial = f
		fences[in.View.Library] = f
		for _, library := range libraries[1:] {
			other, e := homeLibraryFence(f, library, secondary[library])
			if e != nil {
				return e
			}
			fences[library] = other
		}
		return nil
	}}
	for _, source := range sources {
		steps = append(steps, func(ctx context.Context, q browseQuery, _ browseFence, d *HomeRecentRow) error {
			clock := DateAdded
			if source.Kind == 2 {
				clock = ContentAdded
			}
			rows, e := q("rb.chronology."+string(clock)+".desc.first", source.Library, source.Kind, int64(MaxRaw))
			if e != nil {
				return e
			}
			d.RawRows += len(rows)
			if len(rows) == MaxRaw {
				return ErrUnqualified
			}
			for _, row := range rows {
				if row[4].(int64) == 0 {
					candidates = append(candidates, candidate{row[0].(int64), source})
				}
			}
			if len(candidates) > 252 {
				return ErrUnqualified
			}
			return ctx.Err()
		})
	}
	sourcePage, result, e := publishPinnedAttempt(ctx, r.snapshots, request, time.Until(deadline), output, in.View, steps, copied, principal, root)
	if e != nil {
		return HomeRecentRow{}, result, e
	}
	baseline := append([]any(nil), rootImage...)
	steps = []readStep[HomeRecentRow]{func(_ context.Context, _ browseQuery, f browseFence, d *HomeRecentRow) error {
		if f != initial || !equalSource(rootImage, baseline) {
			return ErrStale
		}
		d.RawRows = sourcePage.RawRows
		return nil
	}}
	// Pack only complete targets within the existing eight-call quantum.
	// Movies use one bounded joined point, shows at most three calls. The largest
	// declared batch is eight movie existence proofs (<=512 raw rows); a lower
	// caller row budget still refuses before its own ceiling is exceeded.
	for start := 0; start < len(candidates); {
		end, calls := start, 0
		for end < len(candidates) {
			cost := 1
			if candidates[end].source.Kind == 2 {
				cost = 3
			}
			if calls+cost > 8 {
				break
			}
			calls += cost
			end++
		}
		batch := candidates[start:end]
		steps = append(steps, func(ctx context.Context, q browseQuery, _ browseFence, d *HomeRecentRow) error {
			for _, c := range batch {
				f := fences[c.source.Library]
				f.kind = c.source.Kind
				if c.source.Kind == 1 {
					card, raw, e := homeMovieTarget(ctx, q, f, c.id, managed[c.source.Library])
					d.RawRows += raw
					if e != nil {
						return e
					}
					if card != nil {
						d.Items = append(d.Items, *card)
					}
					continue
				}
				rows, e := q("rb.home.item", c.id, c.source.Library, c.source.Kind)
				if e != nil {
					return e
				}
				if len(rows) != 1 {
					return ErrStale
				}
				card, raw, e := homeTarget(ctx, q, f, rows[0], managed[c.source.Library])
				d.RawRows += len(rows) + raw
				if e != nil {
					return e
				}
				if card != nil {
					d.Items = append(d.Items, *card)
				}
			}
			return ctx.Err()
		})
		start = end
	}
	steps = append(steps, func(ctx context.Context, _ browseQuery, _ browseFence, d *HomeRecentRow) error {
		sortHomeCards(d.Items)
		if len(d.Items) > HomeRecentMaximum {
			d.Items = d.Items[:HomeRecentMaximum]
		}
		d.Exhausted = true
		return ctx.Err()
	})
	view := in.View
	view.Now = view.Now.Add(time.Since(started))
	page, targetResult, e := publishPinnedAttempt(ctx, r.snapshots, request, time.Until(deadline), output, view, steps, copied, principal, root)
	mergeHomeReadResult(&result, targetResult)
	return page, result, e
}

func homeMinTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
func mergeHomeReadResult(result *BrowseReadResult, one BrowseReadResult) {
	if len(result.PublicationWindows) == 0 && !result.Publication.Started.IsZero() {
		result.PublicationWindows = append(result.PublicationWindows, result.Publication)
	}
	result.Lookup = one.Lookup
	result.Publication = one.Publication
	result.LookupStatements += one.LookupStatements
	result.PublicationStatements += one.PublicationStatements
	result.LookupWindows = append(result.LookupWindows, one.LookupWindows...)
	if len(one.PublicationWindows) > 0 {
		result.PublicationWindows = append(result.PublicationWindows, one.PublicationWindows...)
	} else if !one.Publication.Started.IsZero() {
		result.PublicationWindows = append(result.PublicationWindows, one.Publication)
	}
	if one.Refusal != nil {
		result.Refusal = one.Refusal
	}
}

func homeLibraryFence(f browseFence, library int64, row []any) (browseFence, error) {
	name := row[0].(string)
	allowed := func(raw string) bool {
		var a []string
		if json.Unmarshal([]byte(raw), &a) != nil {
			return false
		}
		return slices.Contains(a, name)
	}
	if f.role != "owner" && !allowed(f.memberLibraries) || f.profileRestricted && !allowed(f.profileLibraries) {
		return browseFence{}, ErrDenied
	}
	if row[8].(int64) != 0 || row[6] != row[7] {
		return browseFence{}, ErrUnqualified
	}
	f.libraryID = library
	f.library = name
	f.bridgeRoot = row[1].(string)
	f.root = row[9].(string)
	f.dataRevision = row[2].(int64)
	f.orderRevision = row[3].(int64)
	f.metadataRevision = row[4].(int64)
	f.viewerRevision = row[5].(int64)
	f.ageRevision = row[6].(int64)
	b, ok := row[10].([]byte)
	if !ok || len(b) != 16 {
		return browseFence{}, ErrUnqualified
	}
	copy(f.originIncarnation[:], b)
	f.originRevision = row[11].(int64)
	return f, nil
}

// homeTarget consumes the entire bounded leaf source. No chronology ordering
// or target choice depends on a rejected leaf. Nil means a proven empty work.
func homeTarget(ctx context.Context, q browseQuery, f browseFence, source []any, managed bool) (*HomeRecentCard, int, error) {
	kind, library := source[7].(int64), source[8].(int64)
	if library != f.libraryID || source[4].(int64) != 0 {
		return nil, 0, nil
	}
	work, e := itemValue(source)
	if e != nil {
		return nil, 0, e
	}
	card := HomeRecentCard{Work: work, Target: work, Library: library, WorkKind: kind, TargetKind: kind}
	if kind == 1 {
		if !admittedItem(source, f) {
			return nil, 0, nil
		}
		available, raw, e := homeAssets(q, f.library, work.ID, 0, false, managed)
		if e != nil {
			return nil, raw, e
		}
		if !available {
			return nil, raw, nil
		}
		if source[1] != nil {
			card.Added = source[1].(string)
		}
		card.EligibleLeaves = 1
		return &card, raw, nil
	}
	if kind != 2 && kind != 3 {
		return nil, 0, ErrUnqualified
	}
	var leaves [][]any
	seen := map[int64]bool{}
	after := int64(0)
	raw := 0
	exhausted := false
	name := "rb.home.members.show"
	if kind == 3 {
		name = "rb.home.members.season"
	}
	for n := 0; n < rbHomeMemberWindows; n++ {
		rows, err := q(name, f.library, work.ID, after, int64(MaxRaw))
		if err != nil {
			return nil, raw, err
		}
		raw += len(rows)
		for _, row := range rows {
			if err := ctx.Err(); err != nil {
				return nil, raw, err
			}
			after = row[0].(int64)
			if row[7].(int64) != 4 || row[8].(int64) != library || !admittedItem(row, f) {
				continue
			}
			available, e := homeAvailable(row[14], row[15], row[16:23], row[23], managed)
			if e != nil {
				return nil, raw, e
			}
			if !available {
				continue
			}
			if !seen[row[0].(int64)] {
				leaves = append(leaves, row)
				seen[row[0].(int64)] = true
			}
		}
		if len(rows) < MaxRaw {
			exhausted = true
			break
		}
	}
	if !exhausted {
		return nil, raw, ErrUnqualified
	}
	if len(leaves) == 0 {
		return nil, raw, nil
	}
	card.EligibleLeaves = len(leaves)
	// Accepted dates must be valid before a successful chronology assertion.
	for _, row := range leaves {
		if row[1] != nil {
			if _, e := time.Parse(time.RFC3339Nano, row[1].(string)); e != nil {
				return nil, raw, ErrUnqualified
			}
		}
	}
	slices.SortFunc(leaves, func(a, b []any) int {
		if a[1] == nil && b[1] != nil {
			return 1
		}
		if a[1] != nil && b[1] == nil {
			return -1
		}
		if a[1] != nil {
			at, _ := time.Parse(time.RFC3339Nano, a[1].(string))
			bt, _ := time.Parse(time.RFC3339Nano, b[1].(string))
			if at.After(bt) {
				return -1
			}
			if at.Before(bt) {
				return 1
			}
		}
		if a[0].(int64) > b[0].(int64) {
			return -1
		}
		if a[0].(int64) < b[0].(int64) {
			return 1
		}
		return 0
	})
	if f.maximumAge >= 0 || f.allowUnrated == 0 {
		if leaves[0][1] != nil {
			card.Added = leaves[0][1].(string)
		}
	} else if source[11] != nil {
		card.Added = source[11].(string)
	}
	if leaves[0][1] == nil {
		return &card, raw, nil
	}
	first := leaves[0]
	last, _ := time.Parse(time.RFC3339Nano, first[1].(string))
	count := 0
	for _, row := range leaves {
		if row[1] == nil {
			break
		}
		at, _ := time.Parse(time.RFC3339Nano, row[1].(string))
		if count > 0 && last.Sub(at) > time.Hour {
			break
		}
		if count > 0 && row[9] != first[9] {
			return &card, raw, nil
		}
		count++
		last = at
	}
	if count == 1 {
		target, e := itemValue(first)
		if e != nil {
			return nil, raw, e
		}
		card.Target = target
		card.TargetKind = 4
	} else if first[9] != nil {
		rows, err := q("rb.home.item", first[9].(int64), library, int64(3))
		if err != nil {
			return nil, raw, err
		}
		if len(rows) == 1 && rows[0][4].(int64) == 0 && rows[0][10] == first[10] {
			target, e := itemValue(rows[0])
			if e != nil {
				return nil, raw, e
			}
			card.Target = target
			card.TargetKind = 3
		}
	}
	return &card, raw, nil
}

func sortHomeCards(cards []HomeRecentCard) {
	slices.SortFunc(cards, func(a, b HomeRecentCard) int {
		if a.Added > b.Added {
			return -1
		}
		if a.Added < b.Added {
			return 1
		}
		return -strings.Compare(entityid.Encode(a.Work.PublicID[:]), entityid.Encode(b.Work.PublicID[:]))
	})
}

// homeAssets never treats a capped unsuccessful probe as proof of absence.
// Progress is milliseconds; linked asset/chapter boundaries are seconds.
func homeAssets(q browseQuery, library string, item, position int64, progress bool, managed bool) (bool, int, error) {
	rows, e := q("rb.home.assets", library, item, int64(MaxRaw))
	if e != nil {
		return false, 0, e
	}
	available := false
	maxDuration := float64(0)
	for _, row := range rows {
		ok, e := homeAvailable(row[1], row[2], row[6:13], row[13], managed)
		if e != nil {
			return false, len(rows), e
		}
		if !ok {
			continue
		}
		available = true
		duration := row[5].(float64)
		if row[4] != nil {
			duration = row[4].(float64)
		}
		duration -= row[3].(float64)
		if duration > maxDuration {
			maxDuration = duration
		}
	}
	if progress && len(rows) == MaxRaw || !available && len(rows) == MaxRaw {
		return false, len(rows), ErrUnqualified
	}
	if progress && maxDuration > 0 && float64(position) >= maxDuration*1000 {
		return false, len(rows), nil
	}
	return available, len(rows), nil
}

func homeLibraryRows(q browseQuery, token string, library int64) ([][]any, error) {
	rows, e := q("rb.home.library", token, library)
	if e != nil {
		return nil, e
	}
	if len(rows) == 0 {
		return nil, ErrDenied
	}
	if len(rows) == MaxRaw {
		return nil, ErrUnqualified
	}
	return rows, nil
}
func homeAvailable(asset, retired any, source []any, recording any, managed bool) (bool, error) {
	if recording != nil {
		return false, ErrUnqualified
	}
	if retired != nil {
		return false, nil
	}
	if source[0] != nil {
		return source[0].(int64) == 0 && source[1] == nil && source[2] == nil && source[3] == nil && source[4] != nil && source[4].(int64) == 1 && source[5] != nil && source[6] != nil, nil
	}
	if managed {
		return false, ErrUnqualified
	}
	return asset != nil && asset.(int64) != 0, nil
}

// A movie needs one current eligible available version, not a complete version
// count. The point LIMIT caps joined raw rows; a successful existence proof is
// exact even at that cap. An unsuccessful full window fails closed.
func homeMovieTarget(ctx context.Context, q browseQuery, f browseFence, id int64, managed bool) (*HomeRecentCard, int, error) {
	rows, e := q("rb.home.movie", f.library, id, f.libraryID, int64(MaxRaw))
	if e != nil {
		return nil, 0, e
	}
	if len(rows) == 0 {
		return nil, 0, ErrStale
	}
	if !admittedItem(rows[0], f) {
		return nil, len(rows), nil
	}
	for _, row := range rows {
		if e := ctx.Err(); e != nil {
			return nil, len(rows), e
		}
		available, e := homeAvailable(row[14], row[15], row[16:23], row[23], managed)
		if e != nil {
			return nil, len(rows), e
		}
		if !available {
			continue
		}
		item, e := itemValue(row)
		if e != nil {
			return nil, len(rows), e
		}
		card := &HomeRecentCard{Work: item, Target: item, Library: f.libraryID, WorkKind: 1, TargetKind: 1, EligibleLeaves: 1}
		if row[1] != nil {
			card.Added = row[1].(string)
		}
		return card, len(rows), nil
	}
	if len(rows) == MaxRaw {
		return nil, len(rows), ErrUnqualified
	}
	return nil, len(rows), nil
}
