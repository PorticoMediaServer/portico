package api

import (
	"sync"
	"testing"
)

// Separate People and Streaming saves change only named fields, so neither page can undo
// the other. Null (inherit) and zero (unlimited) stay distinct.
func TestAccountStreamLimitsKeepOtherAccounts(t *testing.T) {
	s := newTestServer(t)
	main := onMainProfile(t, s)
	owner := main.AccessToken
	kept := main.Viewer.AccountID
	ada := addMember(t, s, owner, "ada", "admin")
	bea := addMember(t, s, owner, "bea", "member")
	const path = "/v1/admin/playback/stream-limits"
	original := StreamLimits{Total: 8, PerPerson: 2, People: map[string]int{kept: 5}, EndPausedVideoAfterMs: 60_000}
	if code := status(t, s, "PATCH", path, owner, original); code != 200 {
		t.Fatal(code)
	}
	start := make(chan struct{})
	results := make(chan int, 4)
	var wg sync.WaitGroup
	for _, patch := range []any{
		map[string]any{"people": map[string]int{ada.ID: 3}},
		map[string]any{"people": map[string]int{bea.ID: 4}},
		map[string]int{"total": 12},
		map[string]int{"perPerson": 6},
	} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results <- call(t, s, "PATCH", path, owner, patch, nil).Code
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	for code := range results {
		if code != 200 {
			t.Fatalf("concurrent save answered %d", code)
		}
	}
	got := get[StreamLimits](t, s, owner, path)
	if got.Total != 12 || got.PerPerson != 6 || got.EndPausedVideoAfterMs != 60_000 || got.People[kept] != 5 || got.People[ada.ID] != 3 || got.People[bea.ID] != 4 {
		t.Fatalf("one account's save lost another field: %+v", got)
	}
	var changed StreamLimits
	if w := call(t, s, "PATCH", path, owner, map[string]any{"people": map[string]any{ada.ID: nil, bea.ID: 0}}, &changed); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if _, exists := changed.People[ada.ID]; exists {
		t.Fatal("inherit kept an override")
	}
	if count, exists := changed.People[bea.ID]; !exists || count != 0 || changed.People[kept] != 5 || changed.PerPerson != 6 {
		t.Fatalf("explicit unlimited lost its meaning or another account: %+v", changed)
	}
	// A malformed batch changes nothing, including an otherwise valid entry beside it.
	if code := status(t, s, "PATCH", path, owner, map[string]any{"people": map[string]int{ada.ID: 9, bea.ID: -1}}); code != 400 {
		t.Fatal(code)
	}
	got = get[StreamLimits](t, s, owner, path)
	if _, exists := got.People[ada.ID]; exists || got.People[bea.ID] != 0 {
		t.Fatalf("a refused batch changed limits: %+v", got)
	}
	// Resetting a server-wide field to zero keeps every person's override.
	if code := status(t, s, "PATCH", path, owner, map[string]int{"total": 0, "endPausedVideoAfterMs": 0}); code != 200 {
		t.Fatal(code)
	}
	got = get[StreamLimits](t, s, owner, path)
	if got.Total != 0 || got.EndPausedVideoAfterMs != 0 || got.PerPerson != 6 || got.People[kept] != 5 || got.People[bea.ID] != 0 {
		t.Fatalf("reset lost another field: %+v", got)
	}
	for _, patch := range []any{
		map[string]int{"total": -1, "perPerson": 1},
		map[string]int{"perPerson": -1},
		map[string]int{"endPausedVideoAfterMs": -1},
		map[string]any{"total": 1, "people": map[string]int{"not-an-account": 2}},
	} {
		if code := status(t, s, "PATCH", path, owner, patch); code != 400 {
			t.Fatal(code)
		}
	}
	got = get[StreamLimits](t, s, owner, path)
	if got.Total != 0 || got.PerPerson != 6 || got.People["not-an-account"] != 0 {
		t.Fatalf("refused save changed limits: %+v", got)
	}
	if code := status(t, s, "PUT", path, owner, original); code != 405 {
		t.Fatalf("whole-document replacement still available: %d", code)
	}
	admin := signInAs(t, s, "ada", testPassword, 7).AccessToken
	if code := status(t, s, "PATCH", path, admin, map[string]any{"people": map[string]int{bea.ID: 7}}); code != 403 {
		t.Fatalf("administrator changed an owner-only limit: %d", code)
	}
}
