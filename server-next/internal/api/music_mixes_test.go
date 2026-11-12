package api

import (
	"context"
	"encoding/binary"
	"math"
	"strconv"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/playqueue"
	"github.com/PorticoMediaServer/portico/server-next/internal/sonic"
)

// sounds gives the songs of a library analyses that put each named song near one of two
// directions of the sound space, with the style scores of that direction: the first
// "soft", the second "dance". The rest of the library is read too, near neither.
func sounds(t *testing.T, th *theatre, near map[string]int) {
	t.Helper()
	ctx := context.Background()
	lib := th.libs[db.LibraryMusic]
	public := map[int64]string{}
	tracks, err := th.s.db.TracksToAnalyse(ctx, lib.ID, sonic.ModelVersion, 0, 10000)
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]int64, len(tracks))
	for n, tr := range tracks {
		ids[n] = tr.ItemID
	}
	if public, err = th.s.db.VisibleItems(ctx, db.Viewer{UserID: 1, ProfileID: 1}, ids); err != nil {
		t.Fatal(err)
	}
	var batch []db.TrackAnalysis
	for n, tr := range tracks {
		direction, ok := near[public[tr.ItemID]]
		if !ok {
			direction = 2
		}
		var v [sonic.Dimensions]float64
		v[direction] = 1
		v[3+n%7] = 0.05 // a little of each song's own
		var norm float64
		for _, x := range v {
			norm += x * x
		}
		embedding := make([]byte, sonic.Dimensions*4)
		for i, x := range v {
			binary.LittleEndian.PutUint32(embedding[i*4:], math.Float32bits(float32(x/math.Sqrt(norm))))
		}
		tags := make([]byte, sonic.TagCount*4)
		style := map[int]int{0: 30, 1: 38}[direction] // soft and dance in the model's own order
		if direction < 2 {
			binary.LittleEndian.PutUint32(tags[style*4:], math.Float32bits(0.9))
		}
		batch = append(batch, db.TrackAnalysis{Track: tr, Model: sonic.ModelVersion, Embedding: embedding, Tags: tags})
	}
	if err := th.s.db.SaveTrackAnalyses(ctx, batch, th.s.now()); err != nil {
		t.Fatal(err)
	}
}

// listenTo records finished plays of a song by the owner's main profile.
func listenTo(t *testing.T, th *theatre, song string, times int) {
	t.Helper()
	for n := range times {
		batch := []db.Progress{{Viewer: db.Viewer{UserID: 1, ProfileID: 1}, ItemID: song, PlayID: song + strconv.Itoa(n), PositionMS: 180_000,
			WatchedMS: 180_000, At: th.s.now()}}
		if _, _, err := th.s.db.SavePlays(context.Background(), db.User, batch); err != nil {
			t.Fatal(err)
		}
	}
}

// Mixes for you are made from the songs the profile has listened to, grouped by how they
// sound and named by their strongest style; none show until enough has been played and the
// music is analysed; a mix plays as a queue of its songs; and a profile kept from the music
// gets none.
func TestMixesAreMadeFromWhatTheProfileListensTo(t *testing.T) {
	th := newTheatre(t)
	_, quiet := th.album("Quiet", 6)
	_, loud := th.album("Loud", 6)
	near := map[string]int{}
	for _, s := range quiet {
		near[s] = 0
	}
	for _, s := range loud {
		near[s] = 1
	}
	if got := get[Page[MusicMix]](t, th.s, th.owner.AccessToken, "/v1/music/mixes"); len(got.Items) != 0 {
		t.Fatalf("mixes before any analysis or listening: %+v", got.Items)
	}
	sounds(t, th, near)
	for _, s := range quiet[:3] {
		listenTo(t, th, s, 2)
	}
	if got := get[Page[MusicMix]](t, th.s, th.owner.AccessToken, "/v1/music/mixes"); len(got.Items) != 0 {
		t.Fatalf("mixes after six plays of three songs: %+v", got.Items)
	}
	for _, s := range append(quiet[3:], loud...) {
		listenTo(t, th, s, 2)
	}
	mixes := get[Page[MusicMix]](t, th.s, th.owner.AccessToken, "/v1/music/mixes").Items
	styles := map[string]int{}
	for _, m := range mixes {
		styles[m.Style] = m.Songs
		if len(m.Artists) == 0 || m.Artists[0].Name != "Band" || len(m.Covers) == 0 || m.ID == "" {
			t.Fatalf("a mix reads %+v", m)
		}
	}
	if len(mixes) != 2 || styles["soft"] != 6 || styles["dance"] != 6 {
		t.Fatalf("the mixes are %+v", mixes)
	}

	var made QueueCreated
	if w := call(t, th.s, "POST", "/v1/queues", th.owner.AccessToken, QueueCreate{Source: playqueue.QueueSource{Mix: mixes[0].ID}}, &made); w.Code != 201 ||
		made.Queue.Total != mixes[0].Songs {
		t.Fatalf("playing a mix answered %d: %+v", w.Code, made.Queue)
	}
	if w := call(t, th.s, "POST", "/v1/queues", th.owner.AccessToken, QueueCreate{Source: playqueue.QueueSource{Mix: "mix_nothing"}}, nil); w.Code != 404 {
		t.Fatalf("a mix the profile does not have answered %d: %s", w.Code, w.Body)
	}

	var kid Profile
	call(t, th.s, "POST", "/v1/profiles", th.owner.AccessToken, ProfileCreate{Name: "Kid"}, &kid)
	if code := status(t, th.s, "PUT", "/v1/profiles/"+kid.ID+"/restrictions", th.owner.AccessToken, Restrictions{AllLibraries: false, Libraries: []string{}}); code != 200 {
		t.Fatalf("keeping the kid from every library answered %d", code)
	}
	var onKid Session
	call(t, th.s, "POST", "/v1/profiles/"+kid.ID+"/select", signIn(t, th.s, 1).Session.AccessToken, ProfileSelect{}, &onKid)
	if got := get[Page[MusicMix]](t, th.s, onKid.AccessToken, "/v1/music/mixes"); len(got.Items) != 0 {
		t.Fatalf("a profile kept from the music has mixes %+v", got.Items)
	}
}

// A radio from a song plays, after the song, the songs that sound closest to it first; one
// from a song the model has not read goes by genre as before.
func TestARadioPlaysWhatSoundsClosest(t *testing.T) {
	th := newTheatre(t)
	_, quiet := th.album("Quiet", 6)
	_, loud := th.album("Loud", 6)
	near := map[string]int{}
	for _, s := range quiet {
		near[s] = 0
	}
	for _, s := range loud {
		near[s] = 1
	}
	// Before anything is analysed the radio goes by genre: it still plays.
	if before := th.order(th.create(QueueCreate{Source: playqueue.QueueSource{Radio: loud[0]}}).Queue.ID); len(before) < 2 {
		t.Fatalf("a radio before any analysis plays %v", before)
	}
	sounds(t, th, near)
	made := th.create(QueueCreate{Source: playqueue.QueueSource{Radio: quiet[0]}})
	order := th.order(made.Queue.ID)
	if len(order) < 7 || order[0] != quiet[0] {
		t.Fatalf("the radio plays %v", order)
	}
	for n, id := range order[1:6] {
		if near[id] != 0 {
			t.Fatalf("entry %d of the radio from a quiet song is %s, not one of the quiet songs: %v", n+1, id, order)
		}
	}
	for _, id := range order[1:] {
		if id == quiet[0] {
			t.Fatalf("the radio picked the song it started from again: %v", order)
		}
	}
}

// Save as playlist on a mix makes a new playlist of its songs, in the mix's order, without
// touching any queue; another profile's mix is not found.
func TestAMixIsSavedAsAPlaylistWithoutTouchingTheQueue(t *testing.T) {
	th := newTheatre(t)
	_, quiet := th.album("Quiet", 6)
	_, loud := th.album("Loud", 6)
	near := map[string]int{}
	for _, s := range quiet {
		near[s] = 0
	}
	for _, s := range loud {
		near[s] = 1
	}
	sounds(t, th, near)
	for _, s := range append(quiet, loud...) {
		listenTo(t, th, s, 2)
	}
	owner := th.owner.AccessToken
	mixes := get[Page[MusicMix]](t, th.s, owner, "/v1/music/mixes").Items
	if len(mixes) == 0 {
		t.Fatal("no mixes")
	}
	playing := th.create(QueueCreate{Source: playqueue.QueueSource{Items: []string{quiet[0]}}})
	var saved List
	if w := call(t, th.s, "POST", "/v1/music/mixes/"+mixes[0].ID+"/playlist", owner, MusicMixSave{Name: "Mellow mix"}, &saved); w.Code != 201 ||
		saved.Name != "Mellow mix" || saved.Kind != "playlist" {
		t.Fatalf("saving the mix answered %d: %s", w.Code, w.Body)
	}
	entries := get[ItemPage](t, th.s, owner, "/v1/items?list="+saved.ID+"&limit=200").Items
	if len(entries) != mixes[0].Songs {
		t.Fatalf("the playlist holds %d of the mix's %d songs", len(entries), mixes[0].Songs)
	}
	var queue QueueCreated
	call(t, th.s, "GET", "/v1/queues/"+playing.Queue.ID, owner, nil, &queue.Queue)
	if queue.Queue.Total != 1 {
		t.Fatalf("saving the mix changed the queue playing: %+v", queue.Queue)
	}
	if code := status(t, th.s, "POST", "/v1/music/mixes/mix_nothing/playlist", owner, MusicMixSave{Name: "Nothing"}); code != 404 {
		t.Fatalf("a mix the profile does not have answered %d", code)
	}
	var kid Profile
	call(t, th.s, "POST", "/v1/profiles", owner, ProfileCreate{Name: "Kid"}, &kid)
	var onKid Session
	call(t, th.s, "POST", "/v1/profiles/"+kid.ID+"/select", signIn(t, th.s, 1).Session.AccessToken, ProfileSelect{}, &onKid)
	if code := status(t, th.s, "POST", "/v1/music/mixes/"+mixes[0].ID+"/playlist", onKid.AccessToken, MusicMixSave{Name: "Not mine"}); code != 404 {
		t.Fatalf("another profile's mix answered %d", code)
	}
}
