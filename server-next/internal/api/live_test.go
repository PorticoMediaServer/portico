package api

import (
	"context"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/librarychannels"
)

// A Library Channel is seen only by a profile that may see every library it draws from, and a
// restricted profile never learns of a hidden title through its guide, On now or tuning in:
// such a programme shows only that it is not available, and joining the channel then plays
// the slate.
func TestARestrictedProfileNeverLearnsOfAHiddenTitleThroughALibraryChannel(t *testing.T) {
	s, owner, members := seededWith(t, 600, 4)
	films := libraryOfKind(t, s, owner, "movies")
	shows := libraryOfKind(t, s, owner, "shows")
	filmsOnly := librarychannels.Selection{Libraries: []string{films}, Kinds: []string{"movie"}}
	movie := makeChannel(t, s, owner, "Movies", librarychannels.Builder{Selection: filmsOnly})
	tv := makeChannel(t, s, owner, "TV", librarychannels.Builder{Selection: librarychannels.Selection{Libraries: []string{shows},
		Kinds: []string{"show"}}})
	// A film channel whose weekly block draws from the shows: it follows both libraries.
	mixed := makeChannel(t, s, owner, "Evenings", librarychannels.Builder{Selection: filmsOnly, Blocks: []librarychannels.Block{{
		ID: "cartoons", Days: []string{"sat"}, From: "08:00", To: "12:00", Selection: &librarychannels.Selection{Libraries: []string{shows},
			Kinds: []string{"show"}}}}})

	everything, labelled, filmsAndMusic, thirteen := members[0], members[1], members[2], members[3]
	names := func(token string) map[string]bool {
		out := map[string]bool{}
		for _, c := range get[Page[Channel]](t, s, token, "/v1/live/channels").Items {
			out[c.ID] = true
		}
		return out
	}
	if seen := names(everything); !seen[movie.ID] || !seen[tv.ID] || !seen[mixed.ID] {
		t.Fatalf("a profile that sees every library is shown %v", seen)
	}
	if seen := names(filmsAndMusic); !seen[movie.ID] || seen[tv.ID] || seen[mixed.ID] {
		t.Fatalf("a profile without the shows library is shown %v", seen)
	}
	for _, path := range []string{"/v1/live/channels/" + tv.ID + "/state", "/v1/live/channels/" + mixed.ID + "/state"} {
		if code := status(t, s, "PUT", path, filmsAndMusic, ChannelMarks{Favorite: true}); code != 404 {
			t.Errorf("marking a channel the profile may not see answered %d", code)
		}
	}
	if code := status(t, s, "POST", "/v1/playback/sessions", filmsAndMusic, PlaybackStart{Channel: tv.ID}); code != 404 {
		t.Errorf("joining a channel the profile may not see answered %d", code)
	}

	stretch := "/v1/live/guide?from=" + s.now().UTC().Format(time.RFC3339) + "&to=" + s.now().Add(20*time.Hour).UTC().Format(time.RFC3339)
	full := map[string]string{} // the owner's guide: each programme's title by channel and start
	for _, row := range get[Guide](t, s, owner, stretch).Items {
		for _, p := range row.Programmes {
			full[row.Channel.ID+p.StartsAt.String()] = p.ItemID
		}
	}
	for _, who := range []struct {
		name, token string
		someHidden  bool
	}{{"rated 13", thirteen, true}, {"blocked label", labelled, false}} {
		hidden := 0
		for _, row := range get[Guide](t, s, who.token, stretch).Items {
			for _, p := range row.Programmes {
				item, ok := full[row.Channel.ID+p.StartsAt.String()]
				if !ok {
					t.Fatalf("%s: a programme the owner's guide does not have: %+v", who.name, p)
				}
				may := item != "" && status(t, s, "GET", "/v1/items/"+item, who.token, nil) == 200
				switch {
				case p.NotAvailable:
					hidden++
					if may || p.Title != "" || p.ItemID != "" || p.Summary != "" || p.Rating != "" || p.Season != nil {
						t.Fatalf("%s: a programme the profile may see is hidden, or a hidden one says %+v", who.name, p)
					}
				case p.ItemID != "" && (!may || p.ItemID != item):
					t.Fatalf("%s: the guide names %s (%s), which the profile may not open", who.name, p.ItemID, p.Title)
				}
			}
		}
		if who.someHidden && hidden == 0 {
			t.Fatalf("%s: nothing hidden in 20 hours of the guide; the seed should rate some films above 13", who.name)
		}
		for _, on := range get[Page[OnNow]](t, s, who.token, "/v1/live/now").Items {
			for _, p := range []*Programme{on.Now, on.Next} {
				if p != nil && p.ItemID != "" {
					if code := status(t, s, "GET", "/v1/items/"+p.ItemID, who.token, nil); code != 200 {
						t.Fatalf("%s: On now names %s, which the profile may not open", who.name, p.ItemID)
					}
				}
			}
		}
		// Joining: either the title on now, which the profile may open, or the slate.
		v := viewerOfToken(t, s, who.token)
		tuned, err := s.channels.Tune(context.Background(), v, movie.ID)
		if _, slate := librarychannels.IsSlate(err); err != nil && !slate {
			t.Fatal(err)
		}
		if err == nil {
			if code := status(t, s, "GET", "/v1/items/"+tuned.ItemID, who.token, nil); code != 200 {
				t.Fatalf("%s: tuning in joins %s, which the profile may not open", who.name, tuned.ItemID)
			}
		}
	}
}

// A library the channel draws from that no longer exists hides the channel from everyone but
// the owner.
func TestAChannelWithAMissingLibraryIsSeenOnlyByTheOwner(t *testing.T) {
	seesAll := db.LiveViewer{LiveTV: true, Libraries: map[string]bool{"lib_a": true, "lib_b": true}}
	for _, c := range []struct {
		viewer    db.LiveViewer
		libraries []string
		want      bool
	}{
		{seesAll, []string{"lib_a"}, true},
		{seesAll, []string{"lib_a", "lib_gone"}, false},
		{seesAll, nil, false},
		{db.LiveViewer{Libraries: seesAll.Libraries}, []string{"lib_a"}, false},
		{db.LiveViewer{Owner: true}, []string{"lib_a", "lib_gone"}, true},
	} {
		if got := librarychannels.Visible(c.viewer, c.libraries); got != c.want {
			t.Errorf("Visible(%+v, %v) = %v, want %v", c.viewer, c.libraries, got, c.want)
		}
	}
}

func viewerOfToken(t *testing.T, s *Server, token string) db.Viewer {
	t.Helper()
	claims, err := s.tokens.CheckAccess(token, s.now())
	if err != nil {
		t.Fatal(err)
	}
	return db.Viewer{UserID: claims.UserID, ProfileID: claims.ProfileID}
}

// Joining a Library Channel plays the title on now from where the programme is, through the
// ordinary player, and saves no place in it for the viewer.
func TestJoiningALibraryChannelStartsWhereTheProgrammeIs(t *testing.T) {
	th := newTheatre(t)
	token := th.owner.AccessToken
	ch := makeChannel(t, th.s, token, "Films", *th.filmChannel())
	rows := programmes(t, th.s, ch.ID, th.clock.Now(), th.clock.Now().Add(time.Hour))
	if len(rows) == 0 || rows[0].ItemID == 0 {
		t.Fatalf("nothing is on: %+v", rows)
	}
	claims, err := th.s.tokens.CheckAccess(token, th.clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	th.clock.Set(rows[0].StartsAt.Add(25 * time.Minute))
	token, _ = th.s.tokens.Access(claims, th.clock.Now(), th.clock.Now().Add(time.Hour))
	code, session := th.start(token, "join", PlaybackStart{Channel: ch.ID})
	if code != 201 || session.Channel == nil || session.Channel.ID != ch.ID || session.ItemID != th.film {
		t.Fatalf("joining answered %d %+v", code, session)
	}
	if got := session.Presentation.StartPositionMs; got != (25 * time.Minute).Milliseconds() {
		t.Fatalf("joined %d ms in, want 25 minutes", got)
	}
	// A seek past live goes to live: 25 minutes is as far as the programme has got.
	var sought PlaybackSession
	if w := call(t, th.s, "PATCH", "/v1/playback/sessions/"+session.ID, token, PlaybackChange{Seek: &PlaybackSeek{PositionMs: (50 * time.Minute).Milliseconds(),
		ID: "ahead"}}, &sought); w.Code != 200 || sought.Presentation.StartPositionMs > (25*time.Minute).Milliseconds() {
		t.Fatalf("a seek past live answered %d at %d ms", w.Code, sought.Presentation.StartPositionMs)
	}
	if code := th.report(token, session.ID, "playing", (59 * time.Minute).Milliseconds()); code != 200 {
		t.Fatalf("progress answered %d", code)
	}
	if code := status(t, th.s, "DELETE", "/v1/playback/sessions/"+session.ID, token, nil); code != 204 {
		t.Fatalf("stop answered %d", code)
	}
	if state := th.state(th.film); state.PositionMS != 0 || state.Watched {
		t.Fatalf("watching a channel to the end left a place or a watched mark in the film: %+v", state)
	}
	// The play is in the owner's record of plays, and not in the viewer's own History.
	records, err := th.s.db.PlayRecords(context.Background(), 1, 5)
	if err != nil || len(records) != 1 || !records[0].HiddenFromProfile {
		t.Fatalf("the owner's play record holds %+v (%v)", records, err)
	}
}
