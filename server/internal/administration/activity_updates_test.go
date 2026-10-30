package administration

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestActivityCachedUpdateTransitionHintsContainNoReleaseFacts(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, db, _ := newService(t)
	ctx := context.Background()
	reports := []UpdateReport{{State: "current"}, {State: "available", UpdateAvailable: true, Latest: &UpdateBuild{Version: "2.0.0"}}, {State: "unavailable"}}
	for n := 0; n < len(reports)-1; n++ {
		if e := s.publishUpdateTransition(ctx, reports[n], reports[n+1]); e != nil {
			t.Fatal(e)
		}
		if e := s.publishUpdateTransition(ctx, reports[n+1], reports[n+1]); e != nil {
			t.Fatal(e)
		}
	}
	var count int
	if e := db.QueryRow(`SELECT count(*) FROM api_events WHERE audience='admin' AND type='server.updated' AND data=''`).Scan(&count); e != nil || count != 2 {
		t.Fatal(count, e)
	}
}
func TestActivityCachedCheckPublishesAfterBackgroundCompletion(t *testing.T) {
	s, db, _ := newService(t)
	s.Fetch = func(context.Context, string) ([]byte, error) {
		return []byte(`{"channels":{"stable":[{"version":"2.0.0"}]}}`), nil
	}
	ctx := context.Background()
	current := UpdateBuild{Version: "1.0.0"}
	s.CachedUpdates(ctx, current, "stable", "https://feed.invalid")
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		var count int
		if e := db.QueryRow(`SELECT count(*) FROM api_events WHERE type='server.updated' AND audience='admin'`).Scan(&count); e != nil {
			t.Fatal(e)
		}
		if count == 1 {
			out := s.CachedUpdates(ctx, current, "stable", "https://feed.invalid")
			if !out.UpdateAvailable || out.AutoInstall || out.Latest == nil || out.Latest.Version != "2.0.0" {
				t.Fatal(out)
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("cached transition did not reach existing event feed")
}
func TestActivityUpdateFailureTransitionsWithoutInstalling(t *testing.T) {
	s, _, _ := newService(t)
	out := s.cachedUpdateReport(context.Background(), UpdateBuild{Version: "1.0.0"}, "stable", "https://feed.invalid", nil, errors.New("offline"), time.Now())
	if out.State != "unavailable" || out.UpdateAvailable || out.AutoInstall {
		t.Fatal(out)
	}
}
