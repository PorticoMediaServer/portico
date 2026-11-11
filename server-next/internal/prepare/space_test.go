package prepare

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/diskspace"
	"github.com/PorticoMediaServer/portico/server-next/internal/settings"
)

// The owner's download space holds copies still waiting to be made, not only the finished
// ones: a member who asks for many copies at once cannot queue more than the space holds.
// Each of these two copies fits the 1 GB space on its own; together they do not.
func TestCopiesWaitingToBeMadeCountAgainstTheSpace(t *testing.T) {
	d, v := filmLibrary(t)
	ctx := context.Background()
	store, _, err := settings.Open(ctx, d, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Write(ctx, 0, Document.Name(), json.RawMessage(`{"spaceGB": 1}`), -1, time.Now()); err != nil {
		t.Fatal(err)
	}
	s, err := New(Options{DB: d, Folder: filepath.Join(t.TempDir(), "prepared"), Settings: store})
	if err != nil {
		t.Fatal(err)
	}
	if !s.Converts() {
		t.Skip("no ffmpeg")
	}
	// A long 1080p film, as far as the estimate goes: 720p Low about 665 MB, 480p about 499 MB.
	v.Height, v.Bitrate, v.DurationMS = 1080, 10_000_000, 2_500_000
	if _, err := s.Want(ctx, v, db.KindMovie, "720p-low"); err != nil {
		t.Fatalf("the first copy was refused: %v", err)
	}
	if _, err := s.Want(ctx, v, db.KindMovie, "480p"); err != ErrSpaceFull {
		t.Fatalf("a second copy that does not fit beside the first waiting one answered %v; want ErrSpaceFull", err)
	}
}

// Whatever the owner's download space, a copy is not made while the disk holding the copies
// and the database has no room for it and two gigabytes more: it waits, and is made once
// there is room.
func TestACopyWaitsForRoomOnTheDisk(t *testing.T) {
	d, film := filmLibrary(t)
	s := service(t, d, t.TempDir(), time.Now)
	if !s.Converts() {
		t.Skip("this machine cannot run the sandboxed encoder")
	}

	defer func() { freeSpace = diskspace.Free }()
	freeSpace = func(string) (int64, error) { return diskspace.ProducerFloor, nil }
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	go s.Run(ctx)
	p, err := s.Want(ctx, film, db.KindMovie, "240p")
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Second)
	if waiting, _ := d.PreparedByID(ctx, p.ID); waiting.State != db.PreparedWaiting {
		t.Fatalf("a copy was started on a disk with no room for it: %s", waiting.State)
	}

	freeSpace = func(string) (int64, error) { return 1 << 40, nil }
	s.kick()
	if made := waitReady(t, d, p.ID); made.State != db.PreparedReady {
		t.Fatalf("the copy was not made once there was room: %+v", made)
	}
}
