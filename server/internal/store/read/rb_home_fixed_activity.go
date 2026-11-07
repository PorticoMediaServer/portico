package read

import (
	"github.com/PorticoMediaServer/portico/server/internal/entityid"
	"reflect"
	"slices"
	"strings"
	"time"
)

// Complete SMALL-scope composition. An uncomposed source invalidates the WHOLE
// shelf, rather than relabeling leaf activity or a recent sample as exact.
func homeFixedActivity(frame homeFixedFrame, page HomeFixedPage, row string, _ time.Time) (HomeFixedPage, error) {
	pending := func(reason string) (HomeFixedPage, error) {
		page.Items = nil
		page.Pending = true
		page.PendingReason = reason
		return page, nil
	}
	for _, lib := range frame.Libraries {
		if row == "continue" && (lib.Library.Kind == "tv" || lib.Library.Kind == "anime") {
			return pending("show_container_on_deck_policy_uncomposed")
		}
		if row == "continue" && lib.Library.Kind != "movie" {
			continue
		}
		if row == "continue" && len(lib.OwnerPolicy) != 0 {
			return pending("owner_continue_policy_uncomposed")
		}
		if row == "continue_books" && slices.Contains([]string{"book", "comic", "ebook"}, lib.Library.Kind) {
			return pending("native_reading_publication_resume_uncomposed")
		}
		byID := map[int64]homeFixedEntity{}
		members := map[int64][]homeFixedEntity{}
		for _, x := range lib.Entities {
			byID[x.Identity.ID] = x
			v := x.Row[0]
			pos := -1
			switch x.Identity.Kind {
			case 4:
				pos = 7
			case 7:
				pos = 9
			case 9:
				pos = 10
			}
			if pos >= 0 && v[pos] != nil {
				members[v[pos].(int64)] = append(members[v[pos].(int64)], x)
			}
		}
		if row == "continue" {
			for _, x := range lib.Entities {
				if x.Identity.Kind == 2 || x.Identity.Kind == 4 {
					return pending("show_container_on_deck_policy_uncomposed")
				}
				if x.Identity.Kind != 1 || !homeFixedResume(x, 3000) {
					continue
				}
				p := x.Personal[0]
				page.Items = append(page.Items, HomeFixedCard{Work: x.Identity, Target: x.Identity, ActivityAt: homeFixedStamp(p[11]), Personal: homeFixedPersonal(x), ResumePosition: p[2].(int64), EligibleLeaves: 1})
			}
			continue
		}
		if row == "continue_books" && lib.Library.Kind != "audiobook" {
			continue
		}
		if row == "listen_next" && lib.Library.Kind != "audiobook" && lib.Library.Kind != "music" {
			continue
		}
		active := map[int64]HomeFixedCard{}
		for id, leaves := range members {
			work, ok := byID[id]
			if !ok || !homeFixedWorkMatches(work, leaves) {
				return HomeFixedPage{}, ErrUnqualified
			}
			if work.Row[0][3] != int64(0) {
				continue
			}
			if (work.Identity.Kind == 6 || work.Identity.Kind == 8) && len(work.Personal) == 1 {
				p0 := work.Personal[0]
				if p0[16] != nil || p0[17] != nil || p0[20] != nil || p0[21] != nil {
					return pending("audio_container_removal_policy_uncomposed")
				}
			}
			if work.Identity.Kind == 8 {
				if len(work.Personal) != 1 {
					continue
				}
				p := work.Personal[0]
				if p[15] == nil || p[2].(int64) <= 0 || p[3] != int64(0) || p[4] == int64(1) || p[12] == nil || p[13] != int64(1) || homeFixedDismissed(p) {
					continue
				}
				eligibleLeaves := 0
				for _, leaf := range leaves {
					if leaf.Available {
						eligibleLeaves++
					}
				}
				for _, leaf := range leaves {
					if !reflect.DeepEqual(p[15], leaf.Identity.PublicID[:]) || !homeFixedResume(leaf, 0) || float64(p[2].(int64)) >= leaf.Binding.Binding.DurationSeconds*1000 {
						continue
					}
					active[id] = HomeFixedCard{Work: work.Identity, Target: leaf.Identity, ActivityAt: homeFixedStamp(leaf.Personal[0][11]), Personal: homeFixedPersonal(work), ResumePosition: p[2].(int64), EligibleLeaves: eligibleLeaves}
				}
			} else if work.Identity.Kind == 6 && row == "listen_next" {
				eligible := []homeFixedEntity{}
				var latest *homeFixedEntity
				for _, leaf := range leaves {
					if !leaf.Available {
						continue
					}
					eligible = append(eligible, leaf)
					if len(leaf.Personal) != 1 || leaf.Personal[0][11] == nil || leaf.Personal[0][13] != int64(1) {
						continue
					}
					cmp := 1
					if latest != nil {
						cmp = homeFixedCompareDate(homeFixedStamp(leaf.Personal[0][11]), homeFixedStamp(latest.Personal[0][11]))
					}
					if latest == nil || cmp > 0 || cmp == 0 && leaf.Identity.ID > latest.Identity.ID {
						copy := leaf
						latest = &copy
					}
				}
				if latest == nil {
					continue
				}
				target := *latest
				if latest.Personal[0][12] == nil {
					slices.SortFunc(eligible, homeFixedTrackOrder)
					found := false
					for _, leaf := range eligible {
						if homeFixedTrackOrder(leaf, *latest) > 0 {
							target = leaf
							found = true
							break
						}
					}
					if !found {
						continue
					}
				}
				active[id] = HomeFixedCard{Work: work.Identity, Target: target.Identity, ActivityAt: homeFixedStamp(latest.Personal[0][11]), Personal: homeFixedPersonal(work), EligibleLeaves: len(eligible)}
			}
		}
		for _, card := range active {
			page.Items = append(page.Items, card)
		}
		if row == "listen_next" {
			recent, e := homeFixedRecent(homeFixedFrame{Libraries: []homeFixedLibrary{lib}}, HomeFixedPage{})
			if e != nil {
				return HomeFixedPage{}, e
			}
			for _, card := range recent.Items {
				if card.Work.Kind != 6 && card.Work.Kind != 8 {
					continue
				}
				if _, ok := active[card.Work.ID]; !ok {
					page.Items = append(page.Items, card)
				}
			}
		}
	}
	slices.SortFunc(page.Items, func(a, b HomeFixedCard) int {
		if row == "listen_next" && (a.ActivityAt != "") != (b.ActivityAt != "") {
			if a.ActivityAt != "" {
				return -1
			}
			return 1
		}
		ak, bk := a.ActivityAt, b.ActivityAt
		if ak == "" {
			ak = a.Added
		}
		if bk == "" {
			bk = b.Added
		}
		if c := homeFixedCompareDate(ak, bk); c != 0 {
			return -c
		}
		return -strings.Compare(entityid.Encode(a.Work.PublicID[:]), entityid.Encode(b.Work.PublicID[:]))
	})
	if row == "continue" && len(page.Items) > 40 {
		page.Items = page.Items[:40]
	}
	page.Exact, page.Exhausted = true, true
	return page, nil
}
func homeFixedResume(x homeFixedEntity, margin int64) bool {
	if !x.Available || len(x.Personal) != 1 {
		return false
	}
	p := x.Personal[0]
	return p[2].(int64) > 0 && p[3] == int64(0) && p[4] != int64(1) && p[12] != nil && p[13] == int64(1) && !homeFixedDismissed(p) && p[11] != nil && float64(p[2].(int64))+float64(margin) < x.Binding.Binding.DurationSeconds*1000
}
func homeFixedTrackOrder(a, b homeFixedEntity) int {
	for _, pos := range []int{13, 14} {
		av, bv := int64(0), int64(0)
		if pos == 13 {
			av, bv = 1, 1
		}
		if a.Row[0][pos] != nil {
			av = a.Row[0][pos].(int64)
		}
		if b.Row[0][pos] != nil {
			bv = b.Row[0][pos].(int64)
		}
		if av < bv {
			return -1
		}
		if av > bv {
			return 1
		}
	}
	if a.Identity.ID < b.Identity.ID {
		return -1
	}
	if a.Identity.ID > b.Identity.ID {
		return 1
	}
	return 0
}

func homeFixedDismissed(p []any) bool { return p[14] != nil && p[22] != nil && p[14] == p[22] }
