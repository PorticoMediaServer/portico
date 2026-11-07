package read

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/entityid"
	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

const homeFixedMaximumEntities = 32

// IDs and native state only; this harness does not invent a public ContentEntry,
// artwork, file eligibility for a container, or playback authorization.
type HomeFixedIdentity struct {
	ID, Library, Kind, Revision int64
	PublicID                    [16]byte
}
type HomeFixedCard struct {
	Work, Target      HomeFixedIdentity
	Added, ActivityAt string
	Personal          PersonalState
	ResumePosition    int64
	EligibleLeaves    int
}
type HomeFixedPage struct {
	Items                     []HomeFixedCard
	Exact, Exhausted, Pending bool
	PendingReason             string
	RawPositions, RawBindings int
}
type HomeFixedInput struct {
	View  BrowseInput
	RowID string
}
type HomeFixedReader struct{ native *SavedViewReader }

func NewHomeFixedReader(pool *governor.Snapshots) (*HomeFixedReader, error) {
	r, e := NewSavedViewReader(pool)
	if e != nil {
		return nil, e
	}
	return &HomeFixedReader{r}, nil
}

type homeFixedEntity struct {
	Identity      HomeFixedIdentity
	Row, Personal [][]any
	Binding       homeBindingResult
	BindingTrace  [][][]any
	Available     bool
}
type homeFixedLibrary struct {
	Library                 HomeManifestLibrary
	Fence                   browseFence
	Membership, OwnerPolicy [][]any
	Entities                []homeFixedEntity
}
type homeFixedFrame struct {
	Policy                    homeManifestSourceFrame
	Manifest                  homeManifestFrame
	Libraries                 []homeFixedLibrary
	RawPositions, RawBindings int
	Pending                   string
}

func (r *HomeFixedReader) Row(ctx context.Context, request governor.Request, budget time.Duration, out BrowseOutput, in HomeFixedInput) (HomeFixedPage, BrowseReadResult, error) {
	if r == nil || r.native == nil || in.View.After != nil || in.View.Descending || in.View.KeepArticles {
		return HomeFixedPage{}, BrowseReadResult{}, ErrInput
	}
	if !slices.Contains([]string{"new_on_server", "continue", "listen_next", "continue_books"}, in.RowID) {
		return HomeFixedPage{}, BrowseReadResult{}, ErrUnqualified
	}
	return liveRead(ctx, request, budget, out, r.native.pool, in.View.Now, func(ctx context.Context, request governor.Request, budget time.Duration, at time.Time) (HomeFixedPage, BrowseReadResult, error) {
		in.View.Now = at
		s, e := newSavedViewSession(r.native, ctx, request, budget, out, in.View)
		if e != nil {
			return HomeFixedPage{}, BrowseReadResult{}, e
		}
		// Typed containers/maps/copies retain one structural hold across both attempts.
		if e = s.reserve(384 << 10); e != nil {
			return HomeFixedPage{}, s.result(), e
		}
		for attempt := 0; attempt < 2; attempt++ {
			s.cost.Attempts = attempt + 1
			var first homeFixedFrame
			e = s.window(false, 2048, func(q browseQuery) error { var e error; first, e = readHomeFixedFrame(s, q, in); return e })
			if e == nil {
				page, prior := composeHomeFixed(first, in.RowID, s.input(in.View).Now)
				final := s.window(true, 2048, func(q browseQuery) error {
					current, e := readHomeFixedFrame(s, q, in)
					if e != nil {
						return e
					}
					if !reflect.DeepEqual(first, current) {
						return ErrStale
					}
					return nil
				})
				e = s.finish(first.Manifest.Principal, prior, final)
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
		return HomeFixedPage{}, s.result(), e
	})
}
func readHomeFixedFrame(s *savedViewSession, q browseQuery, in HomeFixedInput) (frame homeFixedFrame, err error) {
	frame.Policy, err = homeManifestSourceFrameRead(s, q, in.View)
	frame.Manifest = frame.Policy.Manifest
	if err != nil {
		return
	}
	if frame.Manifest.Pending {
		frame.Pending = "library_membership_preparation"
		return
	}
	visible := false
	doc := homeManifestSourceProject(frame.Policy, s.input(in.View).Now).Manifest
	for _, row := range doc.Rows {
		if row.ID == in.RowID {
			visible = true
			break
		}
	}
	if !visible {
		return frame, ErrDenied
	}
	if len(frame.Manifest.Libraries) > 4 {
		frame.Pending = "library_scope_preparation"
		return
	}
	for _, lib := range frame.Manifest.Libraries {
		l := homeFixedLibrary{Library: lib}
		if lib.CatalogueState != "current" {
			frame.Pending = "library_catalogue_unprepared"
			return
		}
		view := s.input(in.View)
		view.Library, view.Kind = lib.NativeID, 1
		// Actual merged principal and native library fence; kind1 selects the common
		// leaf envelope only. No container is admitted without eligible native members.
		l.Fence, err = readHomeFixedLibraryFence(q, view, lib.NativeID)
		if err != nil {
			return
		}
		l.Membership, err = q("rb.home.fixed.members", lib.NativeID, int64(0), int64(homeFixedMaximumEntities+1))
		if err != nil {
			return
		}
		frame.RawPositions += len(l.Membership)
		if frame.RawPositions > homeFixedMaximumEntities {
			frame.Pending = "complete_membership_budget"
			frame.Libraries = append(frame.Libraries, l)
			return
		}
		l.OwnerPolicy, err = q("rb.home.fixed.owner.policy", "library:"+lib.ID)
		if err != nil {
			return
		}
		for _, id := range l.Membership {
			x := homeFixedEntity{}
			x.Row, err = q("rb.home.fixed.entity", id[0], lib.NativeID)
			if err != nil {
				return
			}
			if len(x.Row) != 1 {
				return frame, ErrStale
			}
			v := x.Row[0]
			b, ok := v[1].([]byte)
			if !ok || len(b) != 16 {
				return frame, ErrUnqualified
			}
			x.Identity = HomeFixedIdentity{ID: v[0].(int64), Library: lib.NativeID, Kind: v[2].(int64), Revision: v[4].(int64)}
			copy(x.Identity.PublicID[:], b)
			if v[19] == int64(1) {
				frame.Pending = "invalid_chronology_domain"
			}
			for _, pos := range []int{16, 17} {
				if v[pos] != nil {
					if _, e := time.Parse(time.RFC3339Nano, v[pos].(string)); e != nil {
						frame.Pending = "invalid_chronology_domain"
					}
				}
			}
			personalSizes, e := q("rb.home.fixed.personal.bytes", frame.Manifest.Principal.profile, x.Identity.ID)
			if e != nil {
				return frame, e
			}
			var personalTotal int64
			for _, row := range personalSizes {
				for _, v := range row {
					if v != nil {
						n, ok := v.(int64)
						if !ok || n < 0 || n > s.nativeLimit-personalTotal {
							return frame, governor.ErrSnapshotBudget
						}
						personalTotal += n
					}
				}
			}
			if 1024+3*personalTotal > s.copyLimit-s.copied {
				return frame, governor.ErrSnapshotBudget
			}
			x.Personal, err = q("rb.home.fixed.personal", frame.Manifest.Principal.profile, x.Identity.ID)
			if err != nil {
				return
			}
			if len(x.Personal) > 1 {
				return frame, ErrUnqualified
			}
			if len(x.Personal) == 1 {
				p := x.Personal[0]
				if !reflect.DeepEqual(p[1], b) {
					return frame, ErrUnqualified
				}
				if p[18] != nil && p[18].(int64) > 64 || len(homeFixedStamp(p[16])) > 64 || len(homeFixedStamp(p[17])) > 64 {
					frame.Pending = "invalid_activity_domain"
				}
				if p[11] != nil {
					if p[23] == nil {
						frame.Pending = "activity_state_unresolved"
					}
					if _, e := time.Parse(time.RFC3339Nano, p[11].(string)); e != nil {
						frame.Pending = "invalid_activity_domain"
					}
				}
			}
			if x.Identity.Kind == 1 {
				var external [][]any
				external, err = q("rb.home.fixed.external", x.Identity.ID)
				if err != nil {
					return
				}
				if len(external) != 0 {
					frame.Pending = "provider_edition_grouping_preparation"
				}
			}
			if homeFixedAdmitted(x, l.Fence) && slices.Contains([]int64{1, 4, 7, 9}, x.Identity.Kind) {
				// Only the compiled helper's closed recipe is composed. Trace custody
				// includes rejected links and objects, not merely the selected binding.
				trace := func(name string, args ...any) ([][]any, error) {
					part := strings.TrimPrefix(name, "rb.home.binding.")
					sizes, e := q("rb.home.fixed.binding."+part+".bytes", args...)
					if e != nil {
						return nil, e
					}
					var total int64
					for _, row := range sizes {
						for _, v := range row {
							if v == nil {
								continue
							}
							n, ok := v.(int64)
							if !ok || n < 0 || n > s.nativeLimit-total {
								return nil, governor.ErrSnapshotBudget
							}
							total += n
						}
					}
					if 4096+3*total > s.copyLimit-s.copied {
						return nil, governor.ErrSnapshotBudget
					}
					rows, e := q(name, args...)
					if len(rows) != len(sizes) {
						return nil, ErrStale
					}
					x.BindingTrace = append(x.BindingTrace, rows)
					return rows, e
				}
				x.Binding, err = readHomeWholePlayableBinding(trace, lib.NativeID, x.Identity.ID)
				frame.RawBindings += x.Binding.Progress.ItemRows + x.Binding.Progress.LinkRows + x.Binding.Progress.ObjectRows
				if err == nil {
					x.Available = true
				} else if errors.Is(err, errHomeBindingUnresolved) {
					// A complete empty link range or one explicitly unavailable link is known
					// unavailable. Invalid roots, ambiguous parts, stale inventory and missing
					// source proof remain unresolved for the WHOLE shelf.
					unavailable := len(x.BindingTrace) >= 2 && len(x.BindingTrace[1]) == 0
					if len(x.BindingTrace) >= 2 && len(x.BindingTrace[1]) == 1 {
						link := x.BindingTrace[1][0]
						unavailable = link[6] == int64(0) || link[16] == int64(0)
					}
					if !unavailable {
						frame.Pending = "whole_playable_binding_unresolved"
					}
					err = nil
				} else if errors.Is(err, errHomeBindingBudget) {
					frame.Pending = "whole_playable_binding_budget"
					err = nil
				} else {
					return
				}
			}
			l.Entities = append(l.Entities, x)
		}
		frame.Libraries = append(frame.Libraries, l)
	}
	return
}
func homeFixedAdmitted(x homeFixedEntity, f browseFence) bool {
	v := x.Row[0]
	return v[3] == int64(0) && (v[5] != nil && (f.maximumAge < 0 || v[5].(int64) <= f.maximumAge) || v[5] == nil && f.allowUnrated != 0)
}
func homeFixedStamp(v any) string {
	if v == nil {
		return ""
	}
	return v.(string)
}
func homeFixedCompareDate(a, b string) int {
	if a == "" {
		if b == "" {
			return 0
		}
		return -1
	}
	if b == "" {
		return 1
	}
	at, _ := time.Parse(time.RFC3339Nano, a)
	bt, _ := time.Parse(time.RFC3339Nano, b)
	return at.Compare(bt)
}
func homeFixedPersonal(x homeFixedEntity) PersonalState {
	out := PersonalState{PublicID: x.Identity.PublicID}
	if len(x.Personal) == 0 {
		return out
	}
	p := x.Personal[0]
	out.UserState = UserState{Revision: p[0].(int64), ResumePosition: p[2].(int64), ResumeUnit: p[3].(int64), Completed: p[4] == int64(1), Watched: p[5] == int64(1), PlayCount: p[6].(int64), Favorite: p[7] == int64(1), Watchlisted: p[8] == int64(1)}
	out.NotInterested = p[9] == int64(1)
	if p[10] != nil {
		v := p[10].(int64)
		out.Rating = &v
	}
	return out
}
func composeHomeFixed(frame homeFixedFrame, row string, at time.Time) (HomeFixedPage, error) {
	page := HomeFixedPage{RawPositions: frame.RawPositions, RawBindings: frame.RawBindings}
	if frame.Pending != "" {
		page.Pending = true
		page.PendingReason = frame.Pending
		return page, nil
	}
	switch row {
	case "new_on_server":
		return homeFixedRecent(frame, page)
	case "continue", "listen_next", "continue_books":
		return homeFixedActivity(frame, page, row, at)
	}
	return HomeFixedPage{}, ErrUnqualified
}
func homeFixedRecent(frame homeFixedFrame, page HomeFixedPage) (HomeFixedPage, error) {
	for _, lib := range frame.Libraries {
		byID := map[int64]homeFixedEntity{}
		groups := map[int64][]homeFixedEntity{}
		for _, x := range lib.Entities {
			byID[x.Identity.ID] = x
			if !x.Available {
				continue
			}
			v := x.Row[0]
			work := x.Identity.ID
			switch x.Identity.Kind {
			case 4:
				if v[7] == nil {
					return HomeFixedPage{}, ErrUnqualified
				}
				work = v[7].(int64)
			case 7:
				if v[9] == nil {
					return HomeFixedPage{}, ErrUnqualified
				}
				work = v[9].(int64)
			case 9:
				if v[10] == nil {
					return HomeFixedPage{}, ErrUnqualified
				}
				work = v[10].(int64)
			}
			groups[work] = append(groups[work], x)
		}
		for id, leaves := range groups {
			work, ok := byID[id]
			if !ok {
				return HomeFixedPage{}, ErrUnqualified
			}
			if !homeFixedWorkMatches(work, leaves) {
				return HomeFixedPage{}, ErrUnqualified
			}
			if work.Row[0][3] != int64(0) {
				continue
			}
			slices.SortFunc(leaves, func(a, b homeFixedEntity) int {
				if c := homeFixedCompareDate(homeFixedStamp(a.Row[0][16]), homeFixedStamp(b.Row[0][16])); c != 0 {
					return -c
				}
				if a.Identity.ID > b.Identity.ID {
					return -1
				}
				if a.Identity.ID < b.Identity.ID {
					return 1
				}
				return 0
			})
			card := HomeFixedCard{Work: work.Identity, Target: work.Identity, EligibleLeaves: len(leaves), Personal: homeFixedPersonal(work)}
			card.Added = homeFixedStamp(leaves[0].Row[0][16])
			if work.Identity.Kind == 2 || work.Identity.Kind == 3 {
				// Only unrestricted viewers may use the maintained work chronology. Hidden
				// children never contribute a restricted viewer's date or delivery group.
				if lib.Fence.maximumAge < 0 && lib.Fence.allowUnrated != 0 {
					card.Added = homeFixedStamp(work.Row[0][17])
				}
				first := leaves[0]
				count := 0
				season := first.Row[0][8]
				last := homeFixedStamp(first.Row[0][16])
				singleSeason := true
				for _, leaf := range leaves {
					stamp := homeFixedStamp(leaf.Row[0][16])
					if stamp == "" {
						break
					}
					a, _ := time.Parse(time.RFC3339Nano, last)
					b, _ := time.Parse(time.RFC3339Nano, stamp)
					if count > 0 && a.Sub(b) > time.Hour {
						break
					}
					if leaf.Row[0][8] != season {
						singleSeason = false
					}
					count++
					last = stamp
				}
				if count == 1 {
					card.Target = first.Identity
				} else if count > 1 && singleSeason && season != nil {
					if target, ok := byID[season.(int64)]; ok && target.Identity.Kind == 3 && target.Row[0][3] == int64(0) && target.Row[0][7] == work.Identity.ID {
						card.Target = target.Identity
					}
				}
			}
			page.Items = append(page.Items, card)
		}
	}
	slices.SortFunc(page.Items, func(a, b HomeFixedCard) int {
		if c := homeFixedCompareDate(a.Added, b.Added); c != 0 {
			return -c
		}
		return -strings.Compare(entityid.Encode(a.Work.PublicID[:]), entityid.Encode(b.Work.PublicID[:]))
	})
	if len(page.Items) > 100 {
		page.Items = page.Items[:100]
	}
	page.Exact, page.Exhausted = true, true
	return page, nil
}

func homeFixedWorkMatches(work homeFixedEntity, leaves []homeFixedEntity) bool {
	for _, leaf := range leaves {
		expected := leaf.Identity.Kind
		switch expected {
		case 4:
			expected = 2
		case 7:
			expected = 6
		case 9:
			expected = 8
		}
		if expected != work.Identity.Kind || leaf.Identity.Library != work.Identity.Library {
			return false
		}
	}
	return true
}
