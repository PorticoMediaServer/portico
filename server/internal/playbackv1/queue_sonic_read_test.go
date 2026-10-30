package playbackv1

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/sonic"
)

func TestSonicReadCapturesBeforeProjectionAndKeepsProfileAndCastAuthority(t *testing.T) {
	c, s, caller, q := sonicQueueFixture(t)
	analyses, projections := 0, 0
	s.SmoothOrder = func(ctx context.Context, tx *sql.Tx, actor identity.Principal, ids []string) (catalog.MusicSmoothOrder, error) {
		analyses++
		if tx == nil || actor.Viewer != caller.Principal.Viewer {
			t.Fatal("window analysis lost admitted actor", tx, actor)
		}
		return catalog.MusicSmoothOrder{Positions: []int64{0, 4, 3, 2, 1}, BySound: true, Model: &sonic.Identity{ID: sonic.ModelID, Version: sonic.ModelVersion, SHA256: sonic.ModelSHA256}}, nil
	}
	selectWindow := func(l *layout) ([]int64, error) { return l.sonicWindowsForRange(0, 5) }
	project := func(l *layout) error {
		projections++
		if got := l.sonicWindows[0]; got.model == nil || len(got.ordinals) != 5 {
			t.Fatal("response projected before a real capture", got)
		}
		if l.orderToSource(1) == l.ordinaryOrderToSource(1) {
			t.Fatal("response did not use the captured permutation")
		}
		return nil
	}
	for replay := 0; replay < 2; replay++ {
		if err := s.withMusicLayout(context.Background(), caller, q.id, selectWindow, project); err != nil {
			t.Fatal(err)
		}
	}
	if analyses != 1 || projections != 2 {
		t.Fatal("read retries projected early or repeated analysis", analyses, projections)
	}
	foreign := caller
	foreign.Principal.ProfileID = "other"
	if err := s.withMusicLayout(context.Background(), foreign, q.id, selectWindow, project); !errors.Is(err, ErrNotFound) {
		t.Fatal("another profile reached the captured window", err)
	}
	cast := caller
	cast.Principal.Grant = identity.GrantCastTarget
	if err := s.withMusicLayout(context.Background(), cast, q.id, selectWindow, project); !errors.Is(err, identity.ErrForbidden) {
		t.Fatal("unpaired Cast authority reached a window", err)
	}
	var count int
	if err := c.DB.QueryRow(`SELECT count(*) FROM queue_v1_sonic_windows`).Scan(&count); err != nil || count != 1 || projections != 2 || analyses != 1 {
		t.Fatal("refused reader changed the capture or projection", count, projections, analyses, err)
	}
}
