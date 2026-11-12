package metadata_test

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/metadata"
)

// pictureStore keeps what the picture pass stores, by hash.
type pictureStore struct {
	mu  sync.Mutex
	put map[string][]byte
}

func (p *pictureStore) Put(id string, data []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.put[id] = data
	return nil
}

// A picture pass against a picture server in trouble stops after a few pictures in a row,
// instead of asking for every picture recorded (back to back when it answers 503, for days at
// one timeout a picture when it never answers); every picture stays recorded, and the next
// pass, once the server is back, fetches them all.
func TestAPictureServerInTroubleStopsThePass(t *testing.T) {
	defer metadata.SetPictureTimeout(100 * time.Millisecond)()
	for _, trouble := range []string{"503", "no answer"} {
		t.Run(trouble, func(t *testing.T) {
			f := newFixture(t, db.LibraryMovies, "The Matrix (1999).mkv", "The Matrix Reloaded (2003).mkv",
				"The Matrix Revolutions (2003).mkv", "Heat (1995).mkv")
			f.s.Pictures = &pictureStore{put: map[string][]byte{}}
			if _, err := f.s.MatchLibrary(ctx, f.lib.ID); err != nil {
				t.Fatal(err)
			}
			recorded, err := f.d.PicturesToFetch(ctx, f.lib.ID, 0, 100)
			if err != nil || len(recorded) < 8 {
				t.Fatalf("pictures recorded by the lookup: %d, %v; want at least 8", len(recorded), err)
			}
			switch trouble {
			case "503":
				f.tmdb.PicturesDown.Store(true)
			default:
				f.tmdb.PicturesHang.Store(true)
			}
			stored, err := f.s.FetchPictures(ctx, f.lib.ID, 100)
			if !errors.Is(err, metadata.ErrPictureHostDown) || stored != 0 {
				t.Fatalf("a pass against a picture server in trouble: %d stored, %v; want none and ErrPictureHostDown", stored, err)
			}
			if asked := f.tmdb.PictureRequests.Load(); asked != 5 {
				t.Fatalf("the pass asked for %d of %d pictures; want it to stop after 5 in a row", asked, len(recorded))
			}
			if still, err := f.d.PicturesToFetch(ctx, f.lib.ID, 0, 100); err != nil || len(still) != len(recorded) {
				t.Fatalf("pictures still recorded: %d, %v; want all %d", len(still), err, len(recorded))
			}
			f.tmdb.PicturesDown.Store(false)
			f.tmdb.PicturesHang.Store(false)
			if stored, err := f.s.FetchPictures(ctx, f.lib.ID, 100); err != nil || stored != len(recorded) {
				t.Fatalf("the next pass, with the server back: %d stored, %v; want all %d", stored, err, len(recorded))
			}
		})
	}
}
