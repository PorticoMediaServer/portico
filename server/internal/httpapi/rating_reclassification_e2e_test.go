package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/downloads"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/playbackv1"
)

// Real router/authority/selection consumers, one persistent restricted profile,
// and the production maintenance owners. Rating changes never rescan a file or
// manually rebuild a class after initial preparation. The 7+ prelude warms the
// caches before the requested 18+ -> 7+ transition.
func TestRatingReclassificationReachesEveryRestrictedSurfaceWithoutRescan(t *testing.T) {
	f := newV1Fixture(t, 0)
	member, safeFilmID := f.member()
	c := f.catalogTest
	safeFilm := f.names["member-film"]
	var movieLibrary string
	var movies int64
	if err := f.db.QueryRow(`SELECT e.library_id,l.library_id FROM catalog_entities e JOIN catalog_libraries l ON l.id=e.library_id WHERE e.id=?`, safeFilm.ID).Scan(&movies, &movieLibrary); err != nil {
		t.Fatal(err)
	}
	target := c.Movie(movies, filepath.Join(f.root, "RatingTransit.mp4"), "RatingTransit", 2026)
	c.Genres(target.ID, "fixture", []string{"RatingTransitGenre"})
	collection := c.Collection(movies, "RatingTransitCollection", safeFilm, target)
	tv := c.Library("rating-tv", "Rating TV", "tv", filepath.Join(f.root, "tv"))
	show := c.Show(tv, "Rating Transit Show", 2026)
	season := c.Season(show, 1)
	var ratedEpisode catalogtest.Item
	// Downloads capture twenty members per turn. Put the changing member
	// before the cursor, with enough safe members to require a second turn.
	for i := 1; i <= 22; i++ {
		ep := c.Episode(show, season, i, filepath.Join(f.root, "tv", fmt.Sprintf("episode-%02d.mp4", i)))
		if i == 1 {
			ratedEpisode = ep
		}
	}
	books := c.Library("rating-books", "Rating Books", "audiobook", filepath.Join(f.root, "books"))
	for i, title := range []string{"Safe Book", "RatingTransitBook"} {
		book := c.Book(books, title, "RatingTransitAuthor")
		part := c.BookFile(book, 1, filepath.Join(f.root, "books", fmt.Sprintf("part-%d.m4b", i)))
		if i == 1 {
			c.Attributes(part.ID, "contentRating", "RatingTransitAge")
		}
	}
	for _, item := range []catalogtest.Item{target, ratedEpisode} {
		c.Attributes(item.ID, "contentRating", "RatingTransitAge")
	}
	ctx := context.Background()
	for {
		more, err := compactcatalog.ClassifyPendingRatings(ctx, f.db, func(string) (int, bool) { return 7, true })
		if err != nil {
			t.Fatal(err)
		}
		if !more {
			break
		}
	}
	c.Exec(`INSERT INTO profile_restrictions(profile_id,maximum_age,allow_unrated,allow_downloads) VALUES('member-profile',12,1,1) ON CONFLICT(profile_id) DO UPDATE SET maximum_age=12,allow_unrated=1,allow_downloads=1`)
	allowed, _ := json.Marshal([]string{movieLibrary, "rating-tv", "rating-books"})
	c.Exec(`UPDATE direct_memberships SET allowed_libraries=? WHERE account_id='member'`, string(allowed))
	c.Drain()
	for i := 0; ; i++ {
		more, err := f.cat.RefreshListeningGroups(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !more {
			break
		}
		if i > 50 {
			t.Fatal("initial listening groups did not settle")
		}
	}
	c.Drain()
	ceiling := 12
	restrictions := identity.ContentRestrictions{MaximumAge: &ceiling}
	for _, library := range []string{movieLibrary, "rating-tv", "rating-books"} {
		if err := f.cat.RebuildVisibilityClass(ctx, library, restrictions); err != nil {
			t.Fatal(err)
		}
	}
	c.Drain()
	generations := map[int64]int64{}
	classRows, e := f.db.Query(`SELECT id,active_generation FROM compact_visibility_classes`)
	if e != nil {
		t.Fatal(e)
	}
	for classRows.Next() {
		var id, generation int64
		if e = classRows.Scan(&id, &generation); e != nil {
			classRows.Close()
			t.Fatal(e)
		}
		generations[id] = generation
	}
	if e = classRows.Err(); e != nil {
		classRows.Close()
		t.Fatal(e)
	}
	classRows.Close()
	offline, err := downloads.New(downloads.Options{DB: f.db})
	if err != nil {
		t.Fatal(err)
	}
	defer offline.Close()
	f.deps.Downloads = offline
	f.handler = New(f.deps)
	p, err := f.id.Authenticate(member)
	if err != nil {
		t.Fatal(err)
	}
	var device string
	if err = f.db.QueryRow(`SELECT b.device_id FROM authorization_family_tokens t JOIN identity_device_families b ON b.family_id=t.family_id WHERE t.token_hash=?`, p.Hash).Scan(&device); err != nil {
		t.Fatal(err)
	}
	workerCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{}, 2)
	go func() { compactcatalog.NewWorker(f.db).Run(workerCtx); done <- struct{}{} }()
	go func() { f.cat.RunVisibilityRebuilder(workerCtx); done <- struct{}{} }()
	defer func() { cancel(); <-done; <-done }()
	call := func(path string, out any) string {
		t.Helper()
		w := f.callAs(member, "GET", path, nil, nil, 200, out)
		return w.Body.String()
	}
	checkReads := func(visible bool) {
		t.Helper()
		deadline := time.Now().Add(15 * time.Second)
		last := ""
		for {
			ok := true
			paths := []struct{ path, marker string }{
				{"/v1/libraries/" + movieLibrary + "/content?view=movies&limit=12", target.Public},
				{"/v1/search?q=RatingTransit&group=movies&limit=12", target.Public},
				{"/v1/home/rows/new_on_server?limit=12", target.Public},
				{"/v1/libraries/" + movieLibrary + "/categories", "RatingTransitGenre"},
				{"/v1/collections/" + collection.Public + "/items?limit=12", target.Public},
			}
			for _, probe := range paths {
				w := f.raw("GET", probe.path, member, nil, nil)
				if w.Code != 200 || strings.Contains(w.Body.String(), probe.marker) != visible {
					ok = false
					last = fmt.Sprintf("%s status=%d body=%s", probe.path, w.Code, w.Body.String())
				}
			}
			// The actual browse POST as well as the content projection above.
			w := f.raw("POST", "/v1/libraries/"+movieLibrary+"/browse", member, nil, map[string]any{"pivot": "movies", "limit": 12})
			if w.Code != 200 || strings.Contains(w.Body.String(), target.Public) != visible || !strings.Contains(w.Body.String(), safeFilmID) {
				ok = false
				last = fmt.Sprintf("browse status=%d body=%s", w.Code, w.Body.String())
			}
			var page catalog.ContentEnvelope
			w = f.raw("GET", "/v1/libraries/rating-books/content?view=authors&limit=12", member, nil, nil)
			found := false
			want := 1
			if visible {
				want = 2
			}
			if w.Code == 200 && json.Unmarshal(w.Body.Bytes(), &page) == nil {
				for _, section := range page.Sections {
					for _, entry := range section.Entries {
						if entry.Title == "RatingTransitAuthor" && entry.Count != nil && *entry.Count == want {
							found = true
						}
					}
				}
			}
			if !found {
				ok = false
				last = fmt.Sprintf("listening group status=%d want=%d body=%s", w.Code, want, w.Body.String())
			}
			if ok {
				return
			}
			if time.Now().After(deadline) {
				t.Fatal("rating publication did not settle through production workers: " + last)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	queue := func(key string, want int64) playbackv1.QueueReply {
		t.Helper()
		var out playbackv1.QueueReply
		f.callAs(member, "POST", "/v1/queues", map[string]string{"Idempotency-Key": key}, map[string]any{"segments": []any{map[string]any{"source": containerSource("show", show.Public)}}}, 201, &out)
		if out.Queue.Total != want {
			t.Fatalf("queue count=%d want=%d", out.Queue.Total, want)
		}
		return out
	}
	submit := func(operation string) downloads.RequestView {
		t.Helper()
		var out downloads.RequestView
		in := downloads.ContainerRequest{OperationID: operation, Target: downloads.ContainerTarget{Kind: "show", ID: show.Public}, DeviceID: device, Quality: "original", Policy: downloads.EpisodePolicy{Episodes: "all"}}
		f.callAs(member, "POST", "/v1/downloads/requests", nil, in, 202, &out)
		return out
	}
	finishCapture := func(id string, want int) downloads.RequestView {
		t.Helper()
		var out downloads.RequestView
		for i := 0; i < 20; i++ {
			if e := offline.Advance(ctx); e != nil {
				t.Fatal(e)
			}
			call("/v1/downloads/requests/"+id+"?limit=12", &out)
			if out.TotalKnown {
				break
			}
		}
		if !out.TotalKnown || out.Total != want {
			t.Fatalf("download selection=%+v want=%d", out, want)
		}
		return out
	}
	checkReads(true)
	warmQueue := queue("rating-warm-queue-0001", 22)
	warmDownload := finishCapture(submit("rating-warm-download").RequestID, 22)
	if len(warmDownload.Items) == 0 || warmDownload.Items[0].ItemID != ratedEpisode.Public {
		t.Fatal("download positive control did not include the changing first episode")
	}
	// Warm the persisted queue's duration summary, then require it to change
	// despite identical members, predicate text, arguments and raw revisions.
	var warm playbackv1.QueueReply
	call("/v1/queues/"+warmQueue.Queue.ID, &warm)
	if warm.Queue.TotalDurationMs == nil || !warm.Queue.DurationKnown {
		t.Fatalf("duration positive control: %+v", warm.Queue)
	}
	setAge := func(age int) {
		t.Helper()
		if _, e := dbwork.ExecWrite(ctx, f.db, dbwork.ClassBackgroundMedia, `UPDATE content_rating_ages SET minimum_age=? WHERE value_key='ratingtransitage'`, age); e != nil {
			t.Fatal(e)
		}
	}
	setAge(18)
	checkReads(false)
	queue("rating-hidden-queue-01", 21)
	var hidden playbackv1.QueueReply
	call("/v1/queues/"+warmQueue.Queue.ID, &hidden)
	if hidden.Queue.TotalDurationMs == nil || *hidden.Queue.TotalDurationMs >= *warm.Queue.TotalDurationMs || hidden.Queue.DurationKnown {
		t.Fatalf("cached queue duration survived 18+ policy: warm=%+v hidden=%+v", warm.Queue, hidden.Queue)
	}
	var hiddenDownload downloads.RequestView
	call("/v1/downloads/requests/"+warmDownload.RequestID+"?limit=12", &hiddenDownload)
	if hiddenDownload.Total != 21 {
		t.Fatalf("old download selection retained hidden member: %+v", hiddenDownload)
	}
	partial := submit("rating-partial-download")
	if err = offline.Advance(ctx); err != nil {
		t.Fatal(err)
	}
	var captured int
	if err = f.db.QueryRow(`SELECT count(*) FROM download_request_items WHERE request_id=?`, partial.RequestID).Scan(&captured); err != nil || captured != 20 {
		t.Fatalf("partial selection=%d, want twenty safe members before age changes: %v", captured, err)
	}
	setAge(7)
	checkReads(true)
	queue("rating-restored-queue1", 22)
	restored := finishCapture(partial.RequestID, 22)
	if len(restored.Items) == 0 || restored.Items[0].ItemID != ratedEpisode.Public {
		t.Fatal("classification fence did not restart capture before the previously hidden first member")
	}
	var after playbackv1.QueueReply
	call("/v1/queues/"+warmQueue.Queue.ID, &after)
	if after.Queue.TotalDurationMs == nil || *after.Queue.TotalDurationMs != *warm.Queue.TotalDurationMs || !after.Queue.DurationKnown {
		t.Fatalf("queue summary did not recover after 7+: %+v", after.Queue)
	}
	for id, before := range generations {
		var after int64
		if err = f.db.QueryRow(`SELECT active_generation FROM compact_visibility_classes WHERE id=?`, id).Scan(&after); err != nil || after != before {
			t.Fatalf("rating change replaced rather than incrementally refreshed class %d: %d -> %d (%v)", id, before, after, err)
		}
	}
	var scans int
	if err = f.db.QueryRow(`SELECT count(*) FROM inventory_runs`).Scan(&scans); err != nil || scans != 0 {
		t.Fatalf("test rescanned media: %d runs (%v)", scans, err)
	}
}
