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
	s.processes.Limit = 4
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
}

func TestNoOwnerCapLeavesExcessCopiesWaitingForThePool(t *testing.T) {
	d, film, wanting := filmLibrary(t)
	s := service(t, d, t.TempDir(), time.Now)
	ctx := context.Background()
	s.processes.Limit = 1
	// One copy occupies the pool; the default owner value is zero, not another cap.
	s.running[-1] = &making{}
	film.Height = 1080
	var queued []db.Prepared
	for _, recipe := range []string{"240p", "480p"} {
		p, err := s.Want(ctx, film, db.KindMovie, recipe)
		if err != nil {
			t.Fatal(err)
		}
		wanting(p)
		queued = append(queued, p)
	}
	s.fill(ctx)
	for _, p := range queued {
		if current, _ := d.PreparedByID(ctx, p.ID); current.State != db.PreparedWaiting {
			t.Fatalf("a copy beyond pool capacity is %s, want Waiting", current.State)
		}
	}
	delete(s.running, -1)
	work, cancel := context.WithCancel(ctx)
	defer cancel()
	stopped := make(chan struct{})
	go func() { s.Run(work); close(stopped) }()
	for _, p := range queued {
		if ready := waitReady(t, d, p.ID); ready.State != db.PreparedReady {
			t.Fatalf("a copy did not start when the pool had room: %+v", ready)
		}
	}
	cancel()
	<-stopped
}
