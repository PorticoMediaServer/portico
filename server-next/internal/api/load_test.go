package api

import "testing"

// The owner reads the server's load live and over 24 hours, as 24 hours with the current one
// last and partial. An administrator cannot read it, and a range the server does not keep is
// refused.
func TestOwnerReadsServerLoad(t *testing.T) {
	s := newTestServer(t)
	owner := onMainProfile(t, s).AccessToken
	addMember(t, s, owner, "ada", "admin")
	admin := signInAs(t, s, "ada", testPassword, 1).AccessToken
	var live, day Load
	if w := call(t, s, "GET", "/v1/admin/load", owner, nil, &live); w.Code != 200 || live.Period != "live" || live.Until.Sub(live.From) != liveWindow {
		t.Fatalf("the live load answered %d: %s", w.Code, w.Body)
	}
	if w := call(t, s, "GET", "/v1/admin/load?range=24h", owner, nil, &day); w.Code != 200 || len(day.Buckets) != 24 || !day.Buckets[23].Partial || day.Buckets[0].Partial {
		t.Fatalf("the day's load answered %d: %s", w.Code, w.Body)
	}
	if code := status(t, s, "GET", "/v1/admin/load", admin, nil); code != 403 {
		t.Errorf("an administrator read the load: %d", code)
	}
	if code := status(t, s, "GET", "/v1/admin/load?range=7d", owner, nil); code != 400 {
		t.Errorf("a range the server does not keep answered %d", code)
	}
}
