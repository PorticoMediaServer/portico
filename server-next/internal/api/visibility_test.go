package api

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// What a profile may not see is absent everywhere, and stays absent whatever changes a title.
// Each change below runs on a freshly seeded server; after it, every kind of restricted profile
// is checked against the rule, worked out here from the stored rows: lists hold exactly what the
// owner's hold less what the rule hides, every total, filter count and card count agrees with
// its list, and a hidden title answers only "not found". A change may also never make a title
// that was hidden visible, and a title made from another (a split-off version, a file that came
// back) is hidden wherever that one was.
//
// A new route or data-layer write that changes titles fails TestEveryTitleChangeIsChecked
// until it has a row here or a reason in noRowNeeded.
var titleChanges = []titleChange{
	{name: "a scan adds titles: a film with a label, a rated show, an episode of a seeded show and an album",
		covers: []string{"db.Import"}, run: func(w *world) map[string]string {
			w.addTitles()
			return nil
		}},
	{name: "a scan moves an episode's file into a show with a higher rating",
		covers: []string{"db.Import"}, run: func(w *world) map[string]string {
			mild, strict := w.addShow("Mild", 8), w.addShow("Strict", 18)
			w.check("before the move")
			w.moveEpisode(mild, strict)
			return nil
		}},
	{name: "a file goes, the trash is emptied, and the file comes back",
		covers: []string{"db.Import", "db.EmptyTrash"}, run: func(w *world) map[string]string {
			return w.goneAndBack()
		}},
	{name: "a scan finds a season folder of a seeded show gone, and the trash is emptied",
		covers: []string{"db.MarkFolderGone", "db.EmptyTrash"}, run: func(w *world) map[string]string {
			w.seasonGone()
			return nil
		}},
	{name: "the probe pass reads a file the scan stored unread",
		covers: []string{"db.StoreProbes"}, run: func(w *world) map[string]string {
			w.probeUnread()
			return nil
		}},
	{name: "a version is split off a title with a label",
		covers: []string{"db.SplitVersion"}, run: func(w *world) map[string]string {
			return w.split()
		},
		fault: "the split-off title loses the owner's labels"},
	{name: "title data takes mood tags off titles and leaves their labels",
		covers: []string{"db.ApplyTitleData", "db.RemoveTitleDataTags"}, run: func(w *world) map[string]string {
			w.moods()
			return nil
		}},
	{name: "a restricted profile marks a show watched and takes it back",
		covers: []string{"PUT /v1/items/{id}/state", "POST /v1/items/state:undo", "db.SetState", "db.UndoState"},
		run: func(w *world) map[string]string {
			w.markAndUndo()
			return nil
		}},
	{name: "the owner marks a show watched that some profiles may not see, and plays a film part-way",
		covers: []string{"db.SetStates", "db.SaveProgress"}, run: func(w *world) map[string]string {
			w.ownerState()
			return nil
		}},
	{name: "the owner narrows a profile's restrictions and a member's libraries",
		covers: []string{"PUT /v1/profiles/{id}/restrictions", "PATCH /v1/admin/members/{id}"}, run: func(w *world) map[string]string {
			w.narrow()
			return nil
		}},
	{name: "a provider rates a show for adults and names its episodes, a film stays unmatched and takes the title in its file",
		covers: []string{"db.ApplyDetails", "db.ApplyChildren", "db.SetUnmatched", "db.UseEmbeddedTitle"}, run: func(w *world) map[string]string {
			w.provider()
			return nil
		}},
	{name: "a folder is taken out of a library",
		covers: []string{"db.RemoveFolder"}, run: func(w *world) map[string]string {
			lib := w.library(db.LibraryMusic)
			if err := w.s.db.RemoveFolder(background, lib.id, lib.path); err != nil {
				w.t.Fatal(err)
			}
			return nil
		}},
	{name: "a library is deleted",
		covers: []string{"db.DeleteLibrary"}, run: func(w *world) map[string]string {
			lib := w.library(db.LibraryShows)
			var id string
			if err := w.raw.QueryRow(`SELECT public_id FROM libraries WHERE id = ?`, lib.id).Scan(&id); err != nil {
				w.t.Fatal(err)
			}
			if err := w.s.db.DeleteLibrary(background, lib.id); err != nil {
				w.t.Fatal(err)
			}
			// Its lists are gone for everyone; nothing of it may be left in any other read.
			w.views = slices.DeleteFunc(w.views, func(view [2]string) bool { return view[0] == id })
			for _, v := range w.viewers {
				if code := status(w.t, w.s, "GET", "/v1/items?library="+id+"&kind=show", v.token, nil); code != 404 {
					w.fail("%s: a list of the deleted library answered %d", v.name, code)
				}
			}
			return nil
		}},
	{name: "a show is deleted with its files, and then one version of a film",
		covers: []string{"db.Deleted"}, run: func(w *world) map[string]string {
			w.deleteTitles()
			return nil
		}},
	{name: "every file of a library is marked to be read again",
		covers: []string{"db.ForgetProbes"}, run: func(w *world) map[string]string {
			if err := w.s.db.ForgetProbes(background, w.library(db.LibraryMovies).id); err != nil {
				w.t.Fatal(err)
			}
			return nil
		}},
}

// noRowNeeded are the routes and data-layer writes that change something titles hang on and
// need no row above, each with the reason.
var noRowNeeded = map[string]string{
	"db.RememberTracks":                       "keeps the audio and subtitle language a profile chose; no list, count or rule reads it",
	"db.RemoveUnusedTitleDataTags":            "deletes mood tags no title carries, so no title changes",
	"PUT /v1/playback/sessions/{id}/progress": "writes the position through db.SaveProgress, which has a row",
	"db.SetSubtitleOffset":                    "keeps a subtitle timing a profile chose; no list, count or rule reads it",
	"PUT /v1/items/{id}/subtitles/offset":     "writes the timing through db.SetSubtitleOffset, which no list, count or rule reads",
	"POST /v1/items/query":                    "a read: the list of GET /v1/items with its filter in the body, checked wherever that list is",
	"POST /v1/items/state:batch":              "marks many titles through db.SetStates, which has a row",
	"POST /v1/jobs":                           "marks everything in a view through db.SetStates, which has a row",
	"DELETE /v1/jobs/{id}":                    "stops such a job; what it had already marked went through db.SetStates",
	"DELETE /v1/admin/libraries/{id}":         "removes the library through db.DeleteLibrary, which has a row",
	"DELETE /v1/admin/libraries/{id}/missing": "empties the trash through db.EmptyTrash, which has a row",
	"DELETE /v1/admin/items/{id}":             "deletes the files and records it through db.Deleted, which has a row",
	"DELETE /v1/admin/versions/{id}":          "deletes one version's files and records it through db.Deleted, which has a row",
	"PATCH /v1/admin/libraries/{id}":          "a library's name, options and folders; a folder taken out goes through db.RemoveFolder, which has a row",
}

// The routes that change nothing a title is or how it is listed. A new route must be added
// here with its reason, or get a row in titleChanges.
func init() {
	for reason, routes := range map[string][]string{
		"sign-in, accounts and their secrets: no title, rating, label or library is written": {
			"POST /v1/setup/owner", "POST /v1/auth/sign-in", "POST /v1/auth/two-factor", "POST /v1/auth/refresh",
			"POST /v1/auth/password", "POST /v1/auth/password-reset/confirm", "POST /v1/auth/codes",
			"POST /v1/auth/codes/{id}/approve", "POST /v1/auth/codes/{id}/redeem", "DELETE /v1/auth/session",
			"POST /v1/me/two-factor", "POST /v1/me/two-factor/confirm", "POST /v1/me/two-factor/recovery-codes",
			"DELETE /v1/me/two-factor", "DELETE /v1/me/sessions", "DELETE /v1/me/sessions/{id}",
			"POST /v1/admin/members/{id}/password-reset", "POST /v1/admin/api-keys", "DELETE /v1/admin/api-keys/{id}",
			"POST /v1/me/tokens", "DELETE /v1/me/tokens/{id}", "POST /v1/me/app-passwords", "DELETE /v1/me/app-passwords/{id}",
		},
		"makes or removes an account, profile or invitation, and no title; a profile's limits are what the narrowing row checks": {
			"POST /v1/auth/invitations/{id}/accept", "POST /v1/admin/invitations", "PATCH /v1/admin/invitations/{id}",
			"DELETE /v1/admin/invitations/{id}", "POST /v1/admin/members", "DELETE /v1/admin/members/{id}",
			"POST /v1/profiles", "PATCH /v1/profiles/{id}", "DELETE /v1/profiles/{id}",
		},
		"chooses or guards a profile; what the profile may see is not touched": {
			"POST /v1/profiles/{id}/select", "PUT /v1/profiles/{id}/pin", "DELETE /v1/profiles/{id}/pin",
		},
		"devices, settings and preferences: nothing about a title": {
			"PATCH /v1/devices/{id}", "DELETE /v1/devices/{id}", "PUT /v1/devices/{id}/capabilities", "PATCH /v1/preferences",
			"PUT /v1/admin/settings/network", "PUT /v1/admin/playback/stream-limits", "DELETE /v1/admin/playback/failures/{id}",
			"POST /v1/system/identity-proof",
		},
		"a play queue or a session points at titles and changes none; a hidden title cannot be started, which every row checks": {
			"POST /v1/queues", "PATCH /v1/queues/{id}", "DELETE /v1/queues/{id}", "POST /v1/queues/{id}/entries",
			"DELETE /v1/queues/{id}/entries/{entryId}", "POST /v1/queues/{id}/entries/{entryId}/move",
			"POST /v1/queues/{id}/advance", "POST /v1/queues/{id}/prepare-next", "POST /v1/queues/{id}/commit-next",
			"POST /v1/queues/{id}/playlist", "POST /v1/playback/sessions", "PATCH /v1/playback/sessions/{id}",
			"DELETE /v1/playback/sessions/{id}",
		},
		"a list a profile keeps points at titles and changes none; no rating, label or library is written": {
			"POST /v1/lists", "PATCH /v1/lists/{id}", "DELETE /v1/lists/{id}", "POST /v1/lists/{id}/entries",
			"PATCH /v1/lists/{id}/entries/{entryId}", "DELETE /v1/lists/{id}/entries/{entryId}", "PUT /v1/lists/{id}/shares",
		},
		"records of what was played or typed, and a profile's picture: no title and no watched mark is written": {
			"DELETE /v1/history", "DELETE /v1/history/{id}", "DELETE /v1/search/history",
			"PUT /v1/profiles/{id}/avatar", "DELETE /v1/profiles/{id}/avatar",
		},
		"where a library reads from, and who may connect: titles change only when a scan runs, and a scan's writes have rows": {
			"POST /v1/admin/libraries", "POST /v1/admin/libraries/{id}/scan", "DELETE /v1/admin/libraries/{id}/scan",
			"POST /v1/admin/remote-sources", "PATCH /v1/admin/remote-sources/{id}", "DELETE /v1/admin/remote-sources/{id}",
			"POST /v1/admin/remote-sources/drive-sign-in", "POST /v1/admin/remote-sources/{id}/test",
			"DELETE /v1/admin/blocked-networks/{id}",
		},
	} {
		for _, route := range routes {
			noRowNeeded[route] = reason
		}
	}
}

// titleChange is one row: something that changes titles, and how to make it happen.
type titleChange struct {
	name   string
	covers []string // the routes ("PUT /v1/...") and data-layer writes ("db.Import") it goes through
	// run makes the change. It may call w.check between steps. It returns the titles it made
	// from others, new id to the id it came from.
	run func(w *world) map[string]string
	// fault is a fault this change still has, in one sentence. The row must then fail, and the
	// test says so when it stops failing, so the note goes when the fix lands.
	fault string
}

var background = context.Background()

func TestVisibilityHoldsAfterEveryTitleChange(t *testing.T) {
	for _, change := range titleChanges {
		t.Run(change.name, func(t *testing.T) {
			w := newWorld(t)
			w.last = w.read()
			w.remember(w.last)
			made := change.run(w)
			w.check("after it")
			w.neverReveals(w.last, made)
			switch {
			case change.fault == "":
				for _, p := range w.problems {
					t.Error(p)
				}
			case len(w.problems) == 0:
				t.Errorf("this change no longer has its fault (%s): take the note off its row", change.fault)
			default:
				t.Logf("still has its known fault (%s):\n%s", change.fault, strings.Join(w.problems, "\n"))
			}
		})
	}
}

// world is a seeded server with the owner and every restricted kind of profile.
type world struct {
	t        *testing.T
	s        *Server
	raw      *sql.DB // the stored rows, read here, so the rule is not judged by the code it checks
	owner    string
	viewers  []profileUnderTest
	views    [][2]string // every library and kind of list, as the owner can browse them
	labels   []db.Tag    // the seeded labels; the restricted profiles block some of them
	problems []string
	last     *facts // what the last check read, to tell what changed since
	// ownerLists are the lists as the owner reads them, read once per check.
	ownerLists map[string][]string
	wasHidden  map[int64]map[string]bool // each profile's titles that were hidden at some check
	n          int                       // makes each added name and file unique
}

type profileUnderTest struct {
	name    string
	token   string
	profile int64
}

func newWorld(t *testing.T) *world {
	path := filepath.Join(t.TempDir(), "main.db")
	s, owner, members := seedServer(t, openTestServer(t, path), 300, 4)
	raw, err := sql.Open("sqlite", "file:"+url.PathEscape(path)+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { raw.Close() })
	w := &world{t: t, s: s, raw: raw, owner: owner}
	profileOf := func(token string) int64 {
		claims, err := s.tokens.CheckAccess(token, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		return claims.ProfileID
	}
	w.viewers = append(w.viewers, profileUnderTest{"the owner", owner, profileOf(owner)})
	// The seeder's members (see seed.Profiles), then the restrictions combined.
	for n, name := range []string{"no limit", "a blocked label", "two libraries", "a rating limit of 13"} {
		w.viewers = append(w.viewers, profileUnderTest{name, members[n], profileOf(members[n])})
	}
	labels, err := s.db.Tags(background, db.TagLabel)
	if err != nil || len(labels) < 2 {
		t.Fatalf("the seeded library has %d labels (%v)", len(labels), err)
	}
	w.labels = labels
	libraries := map[string]int64{}
	all, err := s.db.AllLibraries(background)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range all {
		libraries[l.Kind] = l.ID
	}
	age8, age13 := 8, 13
	for _, p := range []db.SeedProfile{
		{Name: "a rating limit and a label", MaxRatingAge: &age13, BlockedTags: []string{labels[1].PublicID}},
		{Name: "two libraries and a label", Libraries: []int64{libraries[db.LibraryShows], libraries[db.LibraryBooks]},
			BlockedTags: []string{labels[len(labels)-1].PublicID}},
		{Name: "all three", MaxRatingAge: &age8, Libraries: []int64{libraries[db.LibraryMovies], libraries[db.LibraryShows],
			libraries[db.LibraryMusic]}, BlockedTags: []string{labels[0].PublicID}},
		{Name: "a rating limit that allows unrated titles, and a label", MaxRatingAge: &age13, AllowUnrated: true,
			BlockedTags: []string{labels[0].PublicID}},
	} {
		v, err := s.db.CreateSeedProfile(background, p)
		if err != nil {
			t.Fatal(err)
		}
		w.viewers = append(w.viewers, profileUnderTest{p.Name, seededMemberToken(t, s, owner, v), v.ProfileID})
	}
	for _, l := range get[Libraries](t, s, owner, "/v1/libraries").Items {
		for _, v := range get[Library](t, s, owner, "/v1/libraries/"+l.ID).Views {
			w.views = append(w.views, [2]string{l.ID, v.Kind})
		}
	}
	return w
}

func (w *world) fail(format string, args ...any) {
	w.problems = append(w.problems, fmt.Sprintf(format, args...))
}

// neverReveals fails for a title that was hidden from a profile at some point of the change
// and is visible at its end, and for a title made from a hidden one that is not hidden itself.
func (w *world) neverReveals(after *facts, made map[string]string) {
	for _, v := range w.viewers {
		for id := range w.wasHidden[v.profile] {
			if after.items[id] != nil && !after.hidden(v.profile, id) {
				w.fail("%s: %s was hidden and is not after the change", v.name, after.describe(id))
			}
		}
		for id, from := range made {
			if w.wasHidden[v.profile][from] && after.items[id] != nil && !after.hidden(v.profile, id) {
				w.fail("%s: %s, made from a title it may not see, is not hidden", v.name, after.describe(id))
			}
		}
	}
}
