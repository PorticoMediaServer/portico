package webhooks

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestPausedWebhookDoesNotStarveHealthy(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, db := open(t)
	ctx := context.Background()
	badReceiver := newReceiver(t)
	badReceiver.answer(503)
	goodReceiver := newReceiver(t)
	goodReceiver.answer(204)
	bad, err := s.Create(ctx, nil, Create{Name: "Bad", URL: badReceiver.server.URL, Events: []string{ServerStarted}})
	if err != nil {
		t.Fatal(err)
	}
	good, err := s.Create(ctx, nil, Create{Name: "Good", URL: goodReceiver.server.URL, Events: []string{ServerStarted}})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UnixMilli()
	for i := 0; i < 65; i++ {
		if _, err := db.Exec(`INSERT INTO webhook_deliveries(id,webhook_id,type,data,next_attempt_ms,created_ms) VALUES(?,?,?,'{}',?,?)`, fmt.Sprintf("bad-%03d", i), bad.Webhook.ID, ServerStarted, now-2000, now-2000); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO webhook_deliveries(id,webhook_id,type,data,next_attempt_ms,created_ms) VALUES('good',?,?,'{}',?,?)`, good.Webhook.ID, ServerStarted, now-1000, now-1000); err != nil {
		t.Fatal(err)
	}
	d := NewDispatcher(s)
	d.Step(ctx) // Bad fails and pauses; its other old rows occupy the batch.
	d.Step(ctx) // Healthy destination must remain usable during that pause.
	t.Logf("bad requests=%d healthy requests=%d", badReceiver.count(), goodReceiver.count())
	if goodReceiver.count() != 1 {
		t.Errorf("healthy webhook was starved by the paused destination: received=%d", goodReceiver.count())
	}
}

func TestWebhookRetryPreservesEventOrder(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, db := open(t)
	ctx := context.Background()
	r := newReceiver(t)
	r.answer(503)
	w, err := s.Create(ctx, nil, Create{Name: "Ordering", URL: r.server.URL, Events: []string{PlaybackStarted, PlaybackStopped}})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for i, event := range []string{PlaybackStarted, PlaybackStopped} {
		if _, err := db.Exec(`INSERT INTO webhook_deliveries(id,webhook_id,type,data,next_attempt_ms,created_ms) VALUES(?,?,?,'{}',?,?)`, fmt.Sprintf("evt-%d", i), w.Webhook.ID, event, now.UnixMilli()-2000+int64(i), now.UnixMilli()-2000+int64(i)); err != nil {
			t.Fatal(err)
		}
	}
	d := NewDispatcher(s)
	d.Step(ctx) // First event fails; retry due after 30 sec, pause only 15 sec.
	s.Now = func() time.Time { return now.Add(16 * time.Second) }
	r.answer(204)
	d.Step(ctx)
	t.Logf("requests=%d last event=%s", r.count(), r.last().header.Get(HeaderEvent))
	if r.count() != 1 {
		t.Errorf("later event delivered before older retry: %s", r.last().header.Get(HeaderEvent))
	}
	s.Now = func() time.Time { return now.Add(31 * time.Second) }
	d.Step(ctx)
	if r.count() != 3 {
		t.Fatalf("retry and next event were not delivered: requests=%d", r.count())
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.seen[1].header.Get(HeaderEvent) != PlaybackStarted || r.seen[2].header.Get(HeaderEvent) != PlaybackStopped {
		t.Fatal("retry did not preserve event order")
	}
	if r.seen[0].header.Get(HeaderEventID) != r.seen[1].header.Get(HeaderEventID) {
		t.Fatal("retry changed event id")
	}
	if !verify(w.Secret, r.seen[1].header, r.seen[1].body) {
		t.Fatal("retry signature is invalid")
	}
}

func TestWebhookSameMillisecondEventsKeepInsertionOrder(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, db := open(t)
	ctx := context.Background()
	r := newReceiver(t)
	w, err := s.Create(ctx, nil, Create{Name: "Ordering", URL: r.server.URL, Events: []string{PlaybackStarted, PlaybackStopped}})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UnixMilli() - 1000
	for i, event := range []string{PlaybackStarted, PlaybackStopped} {
		id := []string{"z-first", "a-second"}[i]
		if _, err := db.Exec(`INSERT INTO webhook_deliveries(id,webhook_id,type,data,next_attempt_ms,created_ms) VALUES(?,?,?,'{}',?,?)`, id, w.Webhook.ID, event, now, now); err != nil {
			t.Fatal(err)
		}
	}
	NewDispatcher(s).Step(ctx)
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.seen) != 2 || r.seen[0].header.Get(HeaderEvent) != PlaybackStarted || r.seen[1].header.Get(HeaderEvent) != PlaybackStopped {
		t.Fatalf("equal timestamps reordered events: %+v", r.seen)
	}
}
