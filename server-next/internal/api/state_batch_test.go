package api

import (
	"testing"
)

// A device back from a trip sends what it played offline: a finished film is counted once
// however often the batch is sent, an older "unwatched" made before that play does not undo
// it, a change for a title no longer on the server is dropped quietly, and none of it used a
// stream of the account's.
func TestPlaysMadeOfflineArriveLaterAndCountOnce(t *testing.T) {
	th := newTheatre(t)
	plays := []OfflinePlay{{ItemID: th.film, PlayID: "offline-1", PositionMs: hour, PlayedMs: hour, MadeAgoMs: 2 * hour}}
	send := func() StateBatchReply {
		t.Helper()
		var out StateBatchReply
		body := map[string]any{"plays": plays, "edits": []map[string]any{
			{"itemId": th.film, "watched": false, "madeAgoMs": 3 * hour}, {"itemId": "itm_gone", "watched": false}}}
		if w := call(t, th.s, "POST", "/v1/items/state:batch", th.owner.AccessToken, body, &out); w.Code != 200 {
			t.Fatalf("the batch answered %d: %s", w.Code, w.Body)
		}
		return out
	}
	first := send()
	if !first.Plays[0].Finished || len(first.Items) != 2 || first.Items[0].Dropped || !first.Items[1].Dropped {
		t.Fatalf("the batch answered %+v", first)
	}
	send()
	state := th.state(th.film)
	if !state.Watched || state.PlayCount != 1 {
		t.Fatalf("after the batch twice the film is %+v; want watched, played once", state)
	}
	if th.engine.streams() != 0 {
		t.Fatal("reporting offline plays started a stream")
	}
}
