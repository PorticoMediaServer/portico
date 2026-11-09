package read

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

// ErrUOSourceNotLanded marks a personal source whose writer is not merged yet.
// The read refuses by name instead of deriving a value from an unrelated flag or
// timestamp that no producer ever proved.
var ErrUOSourceNotLanded = errors.New("uo source producer not landed")

// UOProducerPending names the packet task that must land before a source may be
// read. It is a refusal, never a queued request, a retry hint or authority.
type UOProducerPending struct {
	Task, Source string
}

func (e *UOProducerPending) Error() string {
	return fmt.Sprintf("uo source not landed: %s %s", e.Task, e.Source)
}
func (e *UOProducerPending) Is(target error) bool { return target == ErrUOSourceNotLanded }

// UOActivityFamily selects exactly one closed personal time-ordered stream. The
// caller can never supply a table, column, predicate or profile id.
type UOActivityFamily uint8

const (
	// UOActivityRecent reads personal_current_recent (last_played_at).
	UOActivityRecent UOActivityFamily = iota + 1
	// UOActivityProgress reads personal_current_activity (activity_at).
	UOActivityProgress
	// UOActivityCompleted reads personal_current_completed (last_completed_at).
	UOActivityCompleted
	// UOActivityHistory reads the immutable actor/item/time play_history stream.
	UOActivityHistory
)

func (f UOActivityFamily) valid() bool {
	return f >= UOActivityRecent && f <= UOActivityHistory
}

// activityName is the closed statement suffix. It never reaches SQL.
func (f UOActivityFamily) activityName() string {
	switch f {
	case UOActivityRecent:
		return "recent"
	case UOActivityProgress:
		return "activity"
	case UOActivityCompleted:
		return "completed"
	case UOActivityHistory:
		return "history"
	}
	return ""
}

// activityColumn is the closed time column of a current personal row.
func (f UOActivityFamily) activityColumn() string {
	switch f {
	case UOActivityRecent:
		return "last_played_at"
	case UOActivityProgress:
		return "activity_at"
	case UOActivityCompleted:
		return "last_completed_at"
	}
	return ""
}

// activityIndex is the closed sparse index owned by this family.
func (f UOActivityFamily) activityIndex() string {
	switch f {
	case UOActivityRecent:
		return "personal_current_recent"
	case UOActivityProgress:
		return "personal_current_activity"
	case UOActivityCompleted:
		return "personal_current_completed"
	case UOActivityHistory:
		return "play_history_admin_user"
	}
	return ""
}

// UOActivityInput is a bounded personal time window for the current viewing
// token's own profile. EffectiveWatched asks for the draft container overlay,
// whose explicit-watched writer (UO-05) is not landed, so it is refused by name.
type UOActivityInput struct {
	View             BrowseInput
	Family           UOActivityFamily
	After            *UOActivityCursor
	EffectiveWatched bool
}

// UOActivityCursor is a values-only keyset position. Its private fence, family
// and generation head stop another viewer, family or clear generation from
// continuing this stream.
type UOActivityCursor struct {
	fence  browseFence
	family UOActivityFamily
	head   UOActivityHead
	at     string
	public [16]byte
	id     int64
}

// sameBindings is the keyset continuation shared by the exact octet preflight
// and its matching payload. A current personal row seeks on (time, public id);
// the immutable history row has no public id, so it seeks on (occurred_at, id).
// The two statements have deliberately different third parameter types, and
// binding the wrong one fails every continuation in the registry.
func (c *UOActivityCursor) sameBindings(family UOActivityFamily, token string, limit int64) []any {
	if family == UOActivityHistory {
		return []any{token, c.at, c.id, limit}
	}
	return []any{token, c.at, c.public[:], limit}
}

// UOActivityHead is the current personal generation root. It is a numeric
// witness, never a permission or a historical receipt.
type UOActivityHead struct {
	Exists                   bool
	Revision                 int64
	ActivityGeneration       int64
	RecommendationGeneration int64
	HistoryCutoff            int64
	ActionSequence           int64
}

// UOActivityItem carries only personal state the current profile already owns.
// Rating preserves the distinction between unrated (nil) and a rating of zero.
// History rows use OccurrenceID and accept no catalogue title claim.
type UOActivityItem struct {
	BrowseItem
	At                 string
	ActivityGeneration int64
	AcceptedSequence   int64
	OccurrenceID       int64
	Watched            bool
	ResumeCompleted    bool
	Favorite           bool
	Watchlisted        bool
	NotInterested      bool
	Rating             *int64
	ResumePosition     int64
	ResumeUnit         int64
	PlayCount          int64
	Position           int64
	PlayedMS           int64
	Completed          bool
}

type UOActivityPage struct {
	Items []UOActivityItem
	// Exhausted proves the whole remaining raw stream was consumed. An empty
	// non-exhausted page is pending work, never a zero-match answer.
	Exhausted             bool
	Next                  *UOActivityCursor
	Head                  UOActivityHead
	RawRows, RawPositions int
	Statements            int
}

// UOActivityWindow reads at most64 raw positions per turn and delivers at most
// the caller's bounded page limit. Every statement is scoped by the current
// viewing token's family/profile and by the current library/kind fence; rows
// from another profile, library or kind consume a raw position and are never
// returned. Harness-only: ConsumeForeground must keep output charged through the
// consumer's return.
func (r *ForegroundReader) UOActivityWindow(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in UOActivityInput) (UOActivityPage, BrowseReadResult, error) {
	if r == nil {
		return UOActivityPage{}, BrowseReadResult{}, ErrInput
	}
	if in.EffectiveWatched {
		return UOActivityPage{}, BrowseReadResult{}, &UOProducerPending{Task: "UO-05", Source: "effective watched overlay"}
	}
	return liveRead(ctx, request, budget, output, r.pool, in.View.Now, func(ctx context.Context, request governor.Request, budget time.Duration, at time.Time) (UOActivityPage, BrowseReadResult, error) {
		in.View.Now = at
		return r.activityWindow(ctx, request, budget, output, in)
	})
}

func (r *ForegroundReader) activityWindow(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in UOActivityInput) (page UOActivityPage, result BrowseReadResult, err error) {
	if r == nil || !in.Family.valid() || in.View.After != nil || in.View.Descending || in.View.KeepArticles ||
		in.View.Library <= 0 || in.View.Kind <= 0 || request.Budget.RawRows < MaxRaw {
		return UOActivityPage{}, BrowseReadResult{}, ErrInput
	}
	if in.EffectiveWatched {
		return UOActivityPage{}, BrowseReadResult{}, &UOProducerPending{Task: "UO-05", Source: "effective watched overlay"}
	}
	base := "foreground.uo.activity." + in.Family.activityName()
	// The shared publication helper re-reads this exact generation root in its
	// fresh final snapshot. The shared fence plus this head root already use the
	// eight calls of one snapshot, so the exact octet preflight and its matching
	// payload each carry a window of their own. Every payload row is still
	// compared with the octets reserved before it was read.
	var head UOActivityHead
	var sizes, sizesTail, rows [][]any
	root := func(q browseQuery) ([]any, error) {
		head = UOActivityHead{}
		heads, e := q("read.uo.activity.head", in.View.TokenHash)
		if e != nil {
			return nil, e
		}
		if len(heads) != 1 || len(heads[0]) != 6 {
			return nil, ErrDenied
		}
		head, e = uoActivityHeadValue(heads[0])
		if e != nil {
			return nil, e
		}
		// The head is part of the same snapshot even when it is absent.
		headRows := []any{int64(0), int64(0), int64(0), int64(0), int64(0), int64(0)}
		if head.Exists {
			headRows[0] = int64(1)
		}
		headRows[1], headRows[2], headRows[3], headRows[4], headRows[5] = head.Revision, head.ActivityGeneration, head.RecommendationGeneration, head.HistoryCutoff, head.ActionSequence
		return headRows, nil
	}
	allSizes := func() [][]any {
		out := append([][]any(nil), sizes...)
		return append(out, sizesTail...)
	}
	steps := []readStep[UOActivityPage]{
		func(ctx context.Context, q browseQuery, f browseFence, _ *UOActivityPage) error {
			if in.After != nil && (in.After.fence != f || in.After.family != in.Family || in.After.head != head) {
				return ErrStale
			}
			var e error
			sizes, sizesTail = nil, nil
			if in.After == nil {
				sizes, e = q(base+".first.bytes", in.View.TokenHash, int64(MaxRaw+1))
			} else {
				sizes, e = q(base+".same.bytes", in.After.sameBindings(in.Family, in.View.TokenHash, int64(MaxRaw+1))...)
			}
			if e != nil {
				return e
			}
			if in.After == nil || len(sizes) >= MaxRaw+1 {
				return uoActivityReserve(output, sizes)
			}
			return nil
		},
		func(ctx context.Context, q browseQuery, _ browseFence, _ *UOActivityPage) error {
			if in.After == nil || len(sizesTail) > 0 || len(sizes) >= MaxRaw+1 {
				return nil
			}
			tail, e := q(base+".older.bytes", in.View.TokenHash, in.After.at, int64(MaxRaw+1-len(sizes)))
			if e != nil {
				return e
			}
			sizesTail = tail
			return uoActivityReserve(output, allSizes())
		},
		func(ctx context.Context, q browseQuery, f browseFence, draft *UOActivityPage) error {
			var sameRows, tailRows [][]any
			var e error
			if in.After == nil {
				sameRows, e = q(base+".first", in.View.TokenHash, int64(len(sizes)))
			} else {
				sameRows, e = q(base+".same", in.After.sameBindings(in.Family, in.View.TokenHash, int64(len(sizes)+len(sizesTail)))...)
				if e == nil && len(sizesTail) > 0 {
					tailRows, e = q(base+".older", in.View.TokenHash, in.After.at, int64(len(sizesTail)))
				}
			}
			if e != nil {
				return e
			}
			rows = append(append([][]any(nil), sameRows...), tailRows...)
			sizes = allSizes()
			if len(rows) != len(sizes) {
				return ErrStale
			}
			draft.Head = head
			draft.RawRows = len(rows)
			for i, row := range rows {
				if e := ctx.Err(); e != nil {
					return e
				}
				check := uoActivityCheckSizes
				if in.Family == UOActivityHistory {
					check = uoActivityHistoryCheckSizes
				}
				if e := check(row, sizes[i]); e != nil {
					return e
				}
				cursor, e := uoActivityCursorValue(in.Family, row)
				if e != nil {
					return e
				}
				cursor.fence, cursor.head = f, head
				draft.Next = cursor
				draft.RawPositions++
				item, ok, e := uoActivityItem(in, f, head, row)
				if e != nil {
					return e
				}
				if !ok {
					continue
				}
				draft.Items = append(draft.Items, item)
				if len(draft.Items) == in.View.Limit {
					break
				}
			}
			draft.Exhausted = draft.RawPositions == len(rows) && len(rows) < MaxRaw+1
			if draft.Exhausted {
				draft.Next = nil
			}
			return nil
		},
	}
	page, result, err = publishRead(ctx, r.snapshots, request, budget, output, in.View, steps, root)
	if err == nil {
		page.Statements = result.LookupStatements + result.PublicationStatements
	}
	return page, result, err
}

func uoActivityHeadValue(row []any) (UOActivityHead, error) {
	if len(row) != 6 {
		return UOActivityHead{}, ErrUnqualified
	}
	profile, ok := row[0].(string)
	if !ok || len(profile) > 128 {
		return UOActivityHead{}, ErrUnqualified
	}
	var out UOActivityHead
	out.Exists = profile != ""
	for i, dst := range []*int64{&out.Revision, &out.ActivityGeneration, &out.RecommendationGeneration, &out.HistoryCutoff, &out.ActionSequence} {
		n, ok := row[i+1].(int64)
		if !ok || n < 0 {
			return UOActivityHead{}, ErrUnqualified
		}
		*dst = n
	}
	if out.Exists && (out.ActivityGeneration <= 0 || out.RecommendationGeneration <= 0) {
		return UOActivityHead{}, ErrUnqualified
	}
	return out, nil
}

// uoActivityReserve charges the actual bounded payload octets before any payload
// query. It is prospective custody inside the caller's existing output grant.
func uoActivityReserve(output BrowseOutput, sizes [][]any) error {
	var total int64
	for _, row := range sizes {
		if len(row) < 2 {
			return ErrUnqualified
		}
		for _, v := range row {
			if n, ok := v.(int64); ok {
				if n < 0 {
					return ErrUnqualified
				}
				total += n
			}
		}
	}
	limit := output.Request.Need[governor.MemoryBytes] / 2
	if limit <= 0 || 8192+int64(len(sizes))*1024+2*total > limit {
		return governor.ErrSnapshotBudget
	}
	return nil
}

// uoActivityCheckSizes compares the reserved lengths with the payload row. The
// projection is closed per family, so the comparison points are fixed.
func uoActivityCheckSizes(row, size []any) error {
	if len(size) != 3 {
		return ErrUnqualified
	}
	public, ok := row[0].([]byte)
	if !ok || len(public) != 16 {
		return ErrUnqualified
	}
	got, ok := size[0].([]byte)
	if !ok || !bytes.Equal(public, got) {
		return ErrStale
	}
	if row[1] != size[1] {
		return ErrStale
	}
	// A personal row may legally outlive its catalogue entity: the projection is
	// a LEFT JOIN, so an orphan carries a NULL title (and NULL library/kind, which
	// the item decoder skips). Compare against the same-snapshot NULL rather than
	// failing the whole stream as stale.
	title, titleOK := row[4].(string)
	sizeTitle, sizeOK := size[2].(int64)
	if !titleOK && size[2] != nil {
		return ErrUnqualified
	}
	if titleOK != sizeOK || titleOK && int64(len(title)) != sizeTitle {
		return ErrStale
	}
	return nil
}

// uoActivityHistoryCheckSizes compares the reserved event stream octets with the
// fixed history projection.
func uoActivityHistoryCheckSizes(row, size []any) error {
	if len(size) != 3 {
		return ErrUnqualified
	}
	id, ok := row[0].(int64)
	if !ok || id != size[0].(int64) {
		return ErrStale
	}
	at, ok := row[1].(string)
	if !ok || int64(len(at)) != size[1].(int64) {
		return ErrStale
	}
	event, ok := row[2].(string)
	if !ok || int64(len(event)) != size[2].(int64) {
		return ErrStale
	}
	return nil
}

func uoActivityCursorValue(f UOActivityFamily, row []any) (*UOActivityCursor, error) {
	cursor := &UOActivityCursor{family: f}
	if f == UOActivityHistory {
		id, ok := row[0].(int64)
		if !ok || id <= 0 {
			return nil, ErrUnqualified
		}
		at, ok := row[1].(string)
		if !ok || at == "" {
			return nil, ErrUnqualified
		}
		cursor.id, cursor.at = id, at
		return cursor, nil
	}
	public, ok := row[0].([]byte)
	if !ok || len(public) != 16 {
		return nil, ErrUnqualified
	}
	copy(cursor.public[:], public)
	cursor.at = row[17].(string)
	return cursor, nil
}

// uoActivityItem applies eligibility AFTER the raw ordered position. Hidden,
// retired, other-library, other-kind, other-generation and retired-lifetime
// rows never become items while still advancing the continuation.
func uoActivityItem(in UOActivityInput, f browseFence, head UOActivityHead, row []any) (UOActivityItem, bool, error) {
	if in.Family == UOActivityHistory {
		return uoActivityHistoryItem(in, f, head, row)
	}
	if len(row) != 21 {
		return UOActivityItem{}, false, ErrUnqualified
	}
	if row[19] == nil || row[20] == nil {
		return UOActivityItem{}, false, nil
	}
	library, ok := row[19].(int64)
	if !ok {
		return UOActivityItem{}, false, ErrUnqualified
	}
	kind, ok := row[20].(int64)
	if !ok {
		return UOActivityItem{}, false, ErrUnqualified
	}
	if library != in.View.Library || kind != in.View.Kind {
		return UOActivityItem{}, false, nil
	}
	if !admittedItem(row[1:8], f) {
		return UOActivityItem{}, false, nil
	}
	item, e := itemValue(row[1:8])
	if e != nil {
		return UOActivityItem{}, false, e
	}
	public, ok := row[0].([]byte)
	if !ok || !bytes.Equal(public, item.PublicID[:]) {
		return UOActivityItem{}, false, ErrUnqualified
	}
	generation, ok := row[8].(int64)
	if !ok || generation <= 0 || row[9] == nil || row[17] == nil || row[18] == nil {
		return UOActivityItem{}, false, ErrUnqualified
	}
	// A clear generation hides current rows stamped with an older generation.
	if head.Exists && generation != head.ActivityGeneration {
		return UOActivityItem{}, false, nil
	}
	out := UOActivityItem{
		BrowseItem:         item,
		At:                 row[17].(string),
		ActivityGeneration: generation,
		Watched:            row[9].(int64) != 0,
		ResumePosition:     row[10].(int64),
		ResumeUnit:         row[11].(int64),
		PlayCount:          row[12].(int64),
		Favorite:           row[13].(int64) != 0,
		Watchlisted:        row[14].(int64) != 0,
		NotInterested:      row[15].(int64) != 0,
		ResumeCompleted:    row[18].(int64) != 0,
	}
	if row[16] != nil {
		rating, ok := row[16].(int64)
		if !ok || rating < 0 {
			return UOActivityItem{}, false, ErrUnqualified
		}
		out.Rating = &rating
	}
	if out.At == "" {
		return UOActivityItem{}, false, ErrUnqualified
	}
	return out, true, nil
}

func uoActivityHistoryItem(in UOActivityInput, f browseFence, head UOActivityHead, row []any) (UOActivityItem, bool, error) {
	if len(row) != 14 {
		return UOActivityItem{}, false, ErrUnqualified
	}
	id, ok := row[0].(int64)
	if !ok || id <= 0 {
		return UOActivityItem{}, false, ErrUnqualified
	}
	at, ok := row[1].(string)
	if !ok || at == "" {
		return UOActivityItem{}, false, ErrUnqualified
	}
	event, ok := row[2].(string)
	if !ok || len(event) == 0 || len(event) > 32 {
		return UOActivityItem{}, false, ErrUnqualified
	}
	public, ok := row[3].([]byte)
	if !ok || len(public) != 16 {
		return UOActivityItem{}, false, nil
	}
	kind, ok := row[4].(int64)
	if !ok {
		return UOActivityItem{}, false, ErrUnqualified
	}
	if kind != in.View.Kind {
		return UOActivityItem{}, false, nil
	}
	library, ok := row[13].(string)
	if !ok || library != f.library {
		return UOActivityItem{}, false, nil
	}
	member, ok := row[11].(string)
	if !ok || member != f.membership {
		return UOActivityItem{}, false, nil
	}
	lifetime, ok := row[12].(string)
	if !ok || lifetime != f.lifetime {
		return UOActivityItem{}, false, nil
	}
	var out UOActivityItem
	for i, dst := range []*int64{&out.Position, &out.PlayedMS} {
		n, ok := row[5+i].(int64)
		if !ok || n < 0 {
			return UOActivityItem{}, false, ErrUnqualified
		}
		*dst = n
	}
	completed, ok := row[7].(int64)
	if !ok || completed < 0 {
		return UOActivityItem{}, false, ErrUnqualified
	}
	out.Completed = completed != 0
	for i, dst := range []*int64{&out.ActivityGeneration, &out.AcceptedSequence} {
		n, ok := row[8+i].(int64)
		if !ok || n < 0 {
			return UOActivityItem{}, false, ErrUnqualified
		}
		*dst = n
	}
	if out.ActivityGeneration <= 0 {
		return UOActivityItem{}, false, nil
	}
	// A clear generation hides rows from every older generation and everything
	// before the retained clear boundary. Without a head row there is no clear
	// witness and the current rows stay visible.
	if head.Exists && (out.ActivityGeneration != head.ActivityGeneration || id < head.HistoryCutoff) {
		return UOActivityItem{}, false, nil
	}
	out.BrowseItem = BrowseItem{PublicID: [16]byte{}, Revision: out.AcceptedSequence}
	copy(out.BrowseItem.PublicID[:], public)
	out.At = at
	out.OccurrenceID = id
	if row[10] != nil {
		subject, ok := row[10].(int64)
		if !ok || subject < 0 {
			return UOActivityItem{}, false, ErrUnqualified
		}
		out.BrowseItem.ID = subject
	}
	return out, true, nil
}

// uoActivityWatchedFact is one values-only watched intent fact. The fold is a
// pure helper for the draft container overlay; it neither authorizes a read nor
// manufactures a producer. The overlay's explicit-intent writer (UO-05) is not
// landed, so the public activity window refuses that request by name.
type uoActivityWatchedFact struct {
	subject            int64
	public             [16]byte
	generation, action int64
	watched            bool
}

type uoActivityAncestorIntent struct {
	uoActivityWatchedFact
	// Maximum accepted membership along this <=2-edge typed ancestry path.
	membership, watermark int64
}

type uoActivityEffectiveWatched struct {
	watched         bool
	action, subject int64
}

// foldUOActivityWatched preserves the explicit unwatch action order. A late
// member is ineligible, not an implicit unwatch. An undo is a new parent action
// and cannot erase a later explicit child action. Watched is never inferred from
// a flag, a timestamp or manufactured play time.
func foldUOActivityWatched(subject int64, public [16]byte, generation int64, leaf uoActivityWatchedFact, ancestors []uoActivityAncestorIntent) (uoActivityEffectiveWatched, error) {
	if subject <= 0 || public == [16]byte{} || generation <= 0 || len(ancestors) > 2 {
		return uoActivityEffectiveWatched{}, ErrInput
	}
	if leaf.subject != subject || leaf.public != public || leaf.generation < 0 || leaf.action < 0 {
		return uoActivityEffectiveWatched{}, ErrUnqualified
	}
	out := uoActivityEffectiveWatched{subject: subject}
	if leaf.generation == generation {
		out.watched, out.action = leaf.watched, leaf.action
	}
	seen := map[int64]bool{subject: true}
	for _, a := range ancestors {
		if a.subject <= 0 || a.public == [16]byte{} || seen[a.subject] || a.generation <= 0 || a.action < 0 || a.membership <= 0 || a.watermark < 0 {
			return uoActivityEffectiveWatched{}, ErrUnqualified
		}
		seen[a.subject] = true
		if a.generation != generation || a.action == 0 || a.membership > a.watermark {
			continue
		}
		if a.action == out.action && a.action != 0 {
			return uoActivityEffectiveWatched{}, ErrUnqualified
		}
		if a.action > out.action {
			out = uoActivityEffectiveWatched{a.watched, a.action, a.subject}
		}
	}
	return out, nil
}
