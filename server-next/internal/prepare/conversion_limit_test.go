package prepare

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/settings"
)

func TestStreamingLimitKeepsCopiesWaitingUntilTheirTurn(t *testing.T) {
	d, film, wanting := filmLibrary(t)
	s := service(t, d, t.TempDir(), time.Now)
	ctx := context.Background()
	// Stand in for copies already running: changing the limit must leave them alone.
	for id := int64(-4); id < 0; id++ {
		s.running[id] = &making{}
	}
	save := func(limit int) {
		t.Helper()
		for len(s.wake) > 0 {
			<-s.wake
		}
		change := json.RawMessage(fmt.Sprintf(`{"backgroundConversionsAtOnce":%d}`, limit))
		if _, err := s.Settings.Write(ctx, 0, settings.Streaming.Name(), change, -1, time.Now()); err != nil {
			t.Fatal(err)
		}
		select {
		case <-s.wake:
		default:
			t.Fatal("saving the Streaming limit did not wake waiting copies")
		}
	}
	first, err := s.Want(ctx, film, db.KindMovie, "240p")
	if err != nil {
		t.Fatal(err)
	}
	wanting(first)
	save(1)
	s.fill(ctx)
	if p, _ := d.PreparedByID(ctx, first.ID); p.State != db.PreparedWaiting {
		t.Fatalf("a copy held by the limit is %s, want Waiting", p.State)
	}
	// Four now permits this copy beside three others, without a hidden Downloads cap.
	delete(s.running, -4)
	save(4)
	s.fill(ctx)
	if p := waitReady(t, d, first.ID); p.State != db.PreparedReady {
		t.Fatalf("raising the Streaming limit did not make the waiting copy: %+v", p)
	}
	// No limit must admit another copy even with four already occupying the queue.
	s.mu.Lock()
	s.running[-4] = &making{}
	s.mu.Unlock()
	film.Height = 1080
	second, err := s.Want(ctx, film, db.KindMovie, "480p")
	if err != nil {
		t.Fatal(err)
	}
	wanting(second)
	save(0)
	s.fill(ctx)
	if p := waitReady(t, d, second.ID); p.State != db.PreparedReady {
		t.Fatalf("No limit did not make the waiting copy: %+v", p)
	}
}
