package api

import "testing"

// Acting on several titles at once: each title is answered for itself, a title the profile
// may not see or a bad edit is refused without stopping the rest, a batch sent twice changes
// nothing more, and an edit queued earlier offline does not undo a later one.
func TestStateBatch(t *testing.T) {
	s, token, limited := seeded(t)
	libraries := get[Libraries](t, s, token, "/v1/libraries")
	films := get[ItemPage](t, s, token, "/v1/items?library="+libraries.Items[0].ID+"&kind=movie&limit=90").Items
	var hidden string
	for _, film := range films {
		if status(t, s, "GET", "/v1/items/"+film.ID, limited, nil) == 404 {
			hidden = film.ID
			break
		}
	}
	if hidden == "" {
		t.Fatal("no film is hidden from the limited profile")
	}
	var visible []string
	for _, film := range films {
		if film.ID != hidden && len(visible) < 2 && status(t, s, "GET", "/v1/items/"+film.ID, limited, nil) == 200 {
			visible = append(visible, film.ID)
		}
	}
	// Sent as JSON objects: a rating's null and absence differ on the wire.
	batch := map[string]any{"items": []map[string]any{
		{"itemId": visible[0], "watched": true},
		{"itemId": hidden, "inList": true},
		{"itemId": visible[1], "rating": 11},
		{"itemId": visible[1], "inList": true},
	}}
	for range 2 {
		var reply StateBatchReply
		if w := call(t, s, "POST", "/v1/items/state:batch", limited, batch, &reply); w.Code != 200 || len(reply.Items) != 4 {
			t.Fatalf("the batch answered %d: %s", w.Code, w.Body)
		}
		got := reply.Items
		if got[0].State == nil || !got[0].State.Watched || got[1].Error == nil || got[1].Error.Code != "not_found" ||
			got[2].Error == nil || got[2].Error.Code != "invalid_request" || got[3].State == nil || !got[3].State.InList {
			t.Fatalf("title by title: %+v", got)
		}
	}
	if mine := get[Item](t, s, limited, "/v1/items/"+visible[0]); !mine.State.Watched || mine.State.PlayCount != 0 {
		t.Fatalf("after the batch twice: %+v", mine.State)
	}
	if theirs := get[Item](t, s, token, "/v1/items/"+visible[0]); theirs.State.Watched {
		t.Fatal("another profile's title was marked")
	}

	// Unwatched, made a minute ago offline and sent now, loses to the watched mark just made.
	late := map[string]any{"items": []map[string]any{{"itemId": visible[0], "watched": false, "madeAgoMs": 60_000}}}
	var reply StateBatchReply
	if call(t, s, "POST", "/v1/items/state:batch", limited, late, &reply); reply.Items[0].State == nil || !reply.Items[0].State.Watched {
		t.Fatalf("an older offline edit won: %+v", reply.Items)
	}
	if code := status(t, s, "POST", "/v1/items/state:batch", limited, map[string]any{"items": []any{}}); code != 400 {
		t.Fatalf("an empty batch answered %d", code)
	}
}
