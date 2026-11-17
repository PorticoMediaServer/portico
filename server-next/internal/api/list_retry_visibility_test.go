package api

import (
	"context"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/playqueue"
	"testing"
)

func TestListReplayRechecksCurrentTitleAccess(t *testing.T) {
	th := newTheatre(t)
	s, owner := th.s, th.owner.AccessToken
	var list ListCreated
	th.queue("POST", "/v1/lists", "", ListCreate{Kind: "playlist", Name: "List"}, &list)
	path := "/v1/lists/" + list.ID + "/entries"
	body := ListAdd{Items: []string{th.film}}
	send := func(key string) int {
		r := newRequest("POST", path, owner, body)
		r.Header.Set("Idempotency-Key", key)
		return serve(s, r).Code
	}
	if code := send("addition"); code >= 300 {
		t.Fatal(code)
	}
	main := get[Page[Profile]](t, s, owner, "/v1/profiles").Items[0].ID
	call(t, s, "PUT", "/v1/profiles/"+main+"/restrictions", owner, map[string]any{"maxRatingAge": 6}, nil)
	if code := send("fresh"); code != 404 {
		t.Fatal("fresh", code)
	}
	if code := send("addition"); code != 404 {
		t.Errorf("replayed hidden count: HTTP %d", code)
	}
}

func hideRetryTitle(t *testing.T, th *theatre, id string) {
	t.Helper()
	ctx := context.Background()
	locks, err := th.s.db.MetadataLocks(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := th.s.db.EditMetadata(ctx, id, locks.Revision, db.MetadataEdit{ContentRating: new("R")}); err != nil {
		t.Fatal(err)
	}
	main := get[Page[Profile]](t, th.s, th.owner.AccessToken, "/v1/profiles").Items[0].ID
	if code := status(t, th.s, "PUT", "/v1/profiles/"+main+"/restrictions", th.owner.AccessToken, map[string]any{"maxRatingAge": 13, "allowUnrated": true}); code != 200 {
		t.Fatal(code)
	}
}

func TestListHolderReplayCountsOnlyOriginalVisibleEntries(t *testing.T) {
	th := newTheatre(t)
	owner := th.owner.AccessToken
	show := get[Item](t, th.s, owner, "/v1/items/"+th.eps[0]).RootID
	var list ListCreated
	call(t, th.s, "POST", "/v1/lists", owner, ListCreate{Kind: "playlist", Name: "Show"}, &list)
	send := func() ListAdded {
		r := newRequest("POST", "/v1/lists/"+list.ID+"/entries", owner, ListAdd{Items: []string{show}})
		r.Header.Set("Idempotency-Key", "holder-add")
		w := serve(th.s, r)
		var out ListAdded
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body)
		}
		jsonInto(w, &out)
		return out
	}
	if out := send(); out.Added != 2 {
		t.Fatal(out)
	}
	hideRetryTitle(t, th, th.eps[0])
	if code := status(t, th.s, "POST", "/v1/lists/"+list.ID+"/entries", owner, ListAdd{Items: []string{th.film}}); code != 200 {
		t.Fatal(code)
	}
	if out := send(); out.Added != 1 || out.Already != 0 || out.List.Count != 2 {
		t.Errorf("hidden child in retry counts: %+v", out)
	}
}

func TestSavedQueueRetryCountsCurrentVisibleEntries(t *testing.T) {
	th := newTheatre(t)
	owner := th.owner.AccessToken
	var queue QueueCreated
	if w := call(t, th.s, "POST", "/v1/queues", owner, QueueCreate{Source: playqueue.QueueSource{Items: []string{th.film, th.eps[0]}}}, &queue); w.Code != 201 {
		t.Fatal(w.Code, w.Body)
	}
	send := func() SavedPlaylist {
		r := newRequest("POST", "/v1/queues/"+queue.Queue.ID+"/playlist", owner, QueueSave{Name: "Saved"})
		r.Header.Set("Idempotency-Key", "save-queue")
		w := serve(th.s, r)
		var out SavedPlaylist
		if w.Code != 201 {
			t.Fatal(w.Code, w.Body)
		}
		jsonInto(w, &out)
		return out
	}
	first := send()
	if first.Entries != 2 {
		t.Fatal(first)
	}
	hideRetryTitle(t, th, th.film)
	if out := send(); out.Entries != 1 || out.PlaylistID != first.PlaylistID {
		t.Errorf("hidden queue retry count: %+v", out)
	}
}
