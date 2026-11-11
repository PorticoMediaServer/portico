package api

import (
	"context"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/librarychannels"
)

// A profile limited by library: the owner narrows a films-and-shows channel to films while an
// episode is on, so a films-only profile now sees the channel, and the episode left on the air
// must say only that it is not available, and joining must play the slate.
func TestReviewLibraryLimitedProfileAndTheProgrammeLeftOnAfterAnEdit(t *testing.T) {
	s, owner, members := seededWith(t, 600, 4)
	ctx := context.Background()
	films := libraryOfKind(t, s, owner, "movies")
	shows := libraryOfKind(t, s, owner, "shows")
	both := librarychannels.Builder{Selection: librarychannels.Selection{Libraries: []string{films, shows},
		Kinds: []string{"movie", "show"}}}
	ch := makeChannel(t, s, owner, "Both", both)
	rows := programmes(t, s, ch.ID, s.now(), s.now().Add(72*time.Hour))
	var ids []int64
	for _, r := range rows {
		ids = append(ids, r.ItemID)
	}
	titles, err := s.db.ProgrammeTitles(ctx, ids)
	if err != nil {
		t.Fatal(err)
	}
	var episodeStart time.Time
	for _, r := range rows {
		if titles[r.ItemID].ShowID != 0 {
			episodeStart = r.StartsAt
			break
		}
	}
	if episodeStart.IsZero() {
		t.Skip("no episode in three days of the schedule")
	}
	filmsOnly := members[2]
	ownerClaims, err := s.tokens.CheckAccess(owner, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	memberClaims, err := s.tokens.CheckAccess(filmsOnly, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	clock := episodeStart.Add(time.Minute)
	s.setClock(func() time.Time { return clock })
	owner, _ = s.tokens.Access(ownerClaims, clock, clock.Add(time.Hour))
	filmsOnly, _ = s.tokens.Access(memberClaims, clock, clock.Add(time.Hour))

	narrowed := librarychannels.Builder{Selection: librarychannels.Selection{Libraries: []string{films}, Kinds: []string{"movie"}}}
	if w := call(t, s, "PATCH", "/v1/admin/library-channels/"+ch.ID, owner, LibraryChannelSave{Builder: &narrowed}, nil); w.Code != 200 {
		t.Fatalf("narrowing answered %d: %s", w.Code, w.Body)
	}
	if err := s.channels.Schedule(ctx, ch.ID); err != nil {
		t.Fatal(err)
	}
	seen := false
	for _, c := range get[Page[Channel]](t, s, filmsOnly, "/v1/live/channels").Items {
		seen = seen || c.ID == ch.ID
	}
	if !seen {
		t.Fatal("the films-only profile does not see the narrowed channel")
	}
	stretch := "/v1/live/guide?from=" + clock.UTC().Format(time.RFC3339) + "&to=" + clock.Add(20*time.Hour).UTC().Format(time.RFC3339)
	hidden := 0
	for _, row := range get[Guide](t, s, filmsOnly, stretch).Items {
		if row.Channel.ID != ch.ID {
			continue
		}
		for _, p := range row.Programmes {
			if p.NotAvailable {
				hidden++
				if p.Title != "" || p.ItemID != "" || p.Summary != "" {
					t.Fatalf("a hidden programme says %+v", p)
				}
				continue
			}
			if p.ItemID != "" && status(t, s, "GET", "/v1/items/"+p.ItemID, filmsOnly, nil) != 200 {
				t.Fatalf("the guide names %s (%s), which the films-only profile may not open", p.ItemID, p.Title)
			}
		}
	}
	if hidden == 0 {
		t.Fatal("the episode left on the air is not hidden from the films-only profile")
	}
	for _, on := range get[Page[OnNow]](t, s, filmsOnly, "/v1/live/now").Items {
		if on.Channel.ID == ch.ID && (on.Now == nil || !on.Now.NotAvailable) {
			t.Fatalf("On now shows %+v", on.Now)
		}
	}
	if _, err := s.channels.Tune(ctx, viewerOfToken(t, s, filmsOnly), ch.ID); err == nil {
		t.Fatal("joining played the episode")
	} else if _, slate := librarychannels.IsSlate(err); !slate {
		t.Fatal(err)
	}
}
