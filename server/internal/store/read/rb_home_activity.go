package read

import (
	"bytes"
	"context"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

type HomeActivityFamily string

const (
	HomeContinueActivity  HomeActivityFamily = "continue"
	HomeListeningActivity HomeActivityFamily = "listen_next"
	HomeBookActivity      HomeActivityFamily = "continue_books"
)

type HomeActivityInput struct {
	View   BrowseInput
	Family HomeActivityFamily
	After  *HomeActivityCursor
}
type HomeActivityCursor struct {
	fence  browseFence
	family HomeActivityFamily
	at     string
	public [16]byte
}
type HomeActivityCard struct {
	Work, Target         BrowseItem
	WorkKind, TargetKind int64
	State                UserState
}
type HomeActivityPage struct {
	Items                 []HomeActivityCard
	Next                  *HomeActivityCursor
	Exhausted             bool
	RawRows, RawPositions int
}

// HomeActivityWindow composes current unfinished leaf activity into its work
// and resume target. It reuses RD2's sparse personal_current_activity streams
// and state decoder. It is a SOURCE WINDOW, not a claim that on-deck, album
// continuation, recently-added fallback or native Reading rows are complete.
func (r *BrowseReader) HomeActivityWindow(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in HomeActivityInput) (HomeActivityPage, BrowseReadResult, error) {
	if r == nil {
		return HomeActivityPage{}, BrowseReadResult{}, ErrInput
	}
	return liveRead(ctx, request, budget, output, r.pool, in.View.Now, func(ctx context.Context, request governor.Request, budget time.Duration, at time.Time) (HomeActivityPage, BrowseReadResult, error) {
		in.View.Now = at
		return r.homeActivityWindow(ctx, request, budget, output, in)
	})
}
func (r *BrowseReader) homeActivityWindow(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in HomeActivityInput) (HomeActivityPage, BrowseReadResult, error) {
	valid := in.Family == HomeContinueActivity && (in.View.Kind == 1 || in.View.Kind == 4) || in.Family == HomeListeningActivity && in.View.Kind == 7 || in.Family == HomeBookActivity && in.View.Kind == 9
	if !valid || !validHomeView(in.View) {
		return HomeActivityPage{}, BrowseReadResult{}, ErrUnqualified
	}
	var candidates []ContinueItem
	managed := false
	root := func(q browseQuery) ([]any, error) {
		rows, e := homeLibraryRows(q, in.View.TokenHash, in.View.Library)
		if e != nil {
			return nil, e
		}
		managed = rows[0][12] != nil
		var all []any
		for _, row := range rows {
			all = append(all, row...)
		}
		return all, nil
	}
	steps := []readStep[HomeActivityPage]{func(ctx context.Context, q browseQuery, f browseFence, d *HomeActivityPage) error {
		candidates = nil
		if in.After != nil && (in.After.fence != f || in.After.family != in.Family) {
			return ErrStale
		}
		var rows [][]any
		var e error
		if in.After == nil {
			rows, e = q("foreground.continue.first", f.profile, int64(MaxRaw))
		} else {
			rows, e = q("foreground.continue.tail", f.profile, in.After.at, in.After.public[:], int64(MaxRaw))
			if e == nil && len(rows) < MaxRaw {
				var next [][]any
				next, e = q("foreground.continue.next", f.profile, in.After.at, int64(MaxRaw-len(rows)))
				rows = append(rows, next...)
			}
		}
		if e != nil {
			return e
		}
		d.RawRows = len(rows)
		for _, row := range rows {
			if e := ctx.Err(); e != nil {
				return e
			}
			public, ok := row[1].([]byte)
			if !ok || len(public) != 16 {
				return ErrUnqualified
			}
			cursor := &HomeActivityCursor{fence: f, family: in.Family, at: row[0].(string)}
			copy(cursor.public[:], public)
			d.Next = cursor
			d.RawPositions++
			if row[10] == nil || row[17].(int64) != in.View.Library || row[18].(int64) != in.View.Kind || !admittedItem(row[10:17], f) {
				continue
			}
			item, e := itemValue(row[10:17])
			if e != nil {
				return e
			}
			if item.PublicID != cursor.public {
				return ErrUnqualified
			}
			state := stateValue(row[2:10])
			if state.ResumePosition <= 0 || state.Completed {
				continue
			}
			candidates = append(candidates, ContinueItem{BrowseItem: item, State: state})
			if len(candidates) == in.View.Limit {
				break
			}
		}
		d.Exhausted = d.RawPositions == len(rows) && len(rows) < MaxRaw
		if d.Exhausted {
			d.Next = nil
		}
		return nil
	}}
	for n := 0; n < MaxPage; n++ {
		steps = append(steps, func(ctx context.Context, q browseQuery, f browseFence, d *HomeActivityPage) error {
			if n >= len(candidates) {
				return nil
			}
			candidate := candidates[n]
			rows, e := q("rb.home.item", candidate.ID, in.View.Library, in.View.Kind)
			if e != nil {
				return e
			}
			if len(rows) != 1 || !admittedItem(rows[0], f) {
				return nil
			}
			source := rows[0]
			leaf, e := itemValue(source)
			if e != nil {
				return e
			}
			if leaf.PublicID != candidate.PublicID {
				return ErrStale
			}
			stateRows, e := q("rb.home.state", f.profile, leaf.ID)
			if e != nil {
				return e
			}
			if len(stateRows) != 1 {
				return nil
			}
			state := stateValue(stateRows[0])
			s := stateRows[0]
			if !bytes.Equal(s[8].([]byte), leaf.PublicID[:]) || state.ResumePosition <= 0 || state.Completed || state.ResumeUnit != 0 || s[9] == "ended" || s[10] != nil && s[10] == s[11] {
				return nil
			}
			card := HomeActivityCard{Work: leaf, Target: leaf, WorkKind: in.View.Kind, TargetKind: in.View.Kind, State: state}
			parent, kind := any(nil), int64(0)
			switch in.View.Kind {
			case 4:
				parent, kind = source[10], 2
			case 7:
				parent, kind = source[12], 6
			case 9:
				parent, kind = source[13], 8
			}
			if kind != 0 {
				if parent == nil {
					return ErrUnqualified
				}
				parents, e := q("rb.home.item", parent, in.View.Library, kind)
				if e != nil {
					return e
				}
				if len(parents) != 1 || parents[0][4].(int64) != 0 {
					return nil
				}
				card.Work, e = itemValue(parents[0])
				if e != nil {
					return e
				}
				card.WorkKind = kind
				if in.View.Kind == 9 {
					book, e := q("rb.home.state", f.profile, card.Work.ID)
					if e != nil {
						return e
					}
					if len(book) != 1 || book[0][12] == nil || !bytes.Equal(book[0][12].([]byte), leaf.PublicID[:]) {
						return nil
					}
					card.State = stateValue(book[0])
					if card.State.ResumePosition <= 0 || card.State.Completed || card.State.ResumeUnit != 0 {
						return nil
					}
				}
			}
			position := card.State.ResumePosition
			if in.View.Kind != 9 {
				if position > int64(^uint64(0)>>1)-3000 {
					return nil
				}
				position += 3000
			}
			available, raw, e := homeAssets(q, f.library, leaf.ID, position, true, managed)
			d.RawRows += raw
			if e != nil {
				return e
			}
			if !available {
				return nil
			}
			d.Items = append(d.Items, card)
			return ctx.Err()
		})
	}
	return publishRead(ctx, r.snapshots, request, budget, output, in.View, steps, root)
}

func rbHomeActivityDefinitions() []stmt.Definition {
	p := func(n string, t stmt.Type) stmt.Param { return stmt.Param{Name: n, Type: t} }

	return []stmt.Definition{{ID: readStatementID("rb.home.state"), Owner: "store/read", Kind: stmt.Read, CallSites: []string{"store/read/rb_home_activity.go:BrowseReader.HomeActivityWindow"}, SQL: `SELECT p.revision,p.resume_position,p.resume_unit,p.play_count,p.favorite,p.watchlisted,p.watched,p.resume_completed,p.item_public_id,p.activity_state,p.resume_playback_id,p.dismissed_playback_id,p.resume_item_public_id FROM personal_items AS p INDEXED BY personal_current_live WHERE p.profile_id=? AND p.item_id=?`, Params: []stmt.Param{p("profile", stmt.Text), p("item", stmt.Integer)}, Cohorts: []stmt.Cohort{{Name: "present", Bindings: []any{"p", int64(1)}}, {Name: "empty", Bindings: []any{"missing", int64(1)}}}, Plan: stmt.Plan{Hot: true, Indexes: []string{"personal_current_live"}, Seeks: []string{"profile_id=? AND item_id=?"}}}}
}
