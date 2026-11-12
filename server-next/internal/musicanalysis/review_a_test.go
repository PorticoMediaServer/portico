package musicanalysis

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// Review desk A, #2932. A song's file that cannot be opened tonight (the drive is unplugged,
// the network share is asleep) says nothing about the song. It must not be kept as "could not
// be read", because a song kept that way is never tried again until its file changes: one
// night with the music drive away would leave the whole library out of Mixes for good.
func TestReviewAASongWhoseFileCannotBeOpenedNowIsNotKeptAsUnreadable(t *testing.T) {
	s := &Service{}
	song := db.TrackToAnalyse{ItemID: 1, FileID: 1, Path: filepath.Join(t.TempDir(), "drive-not-mounted", "song.flac"), Size: 1 << 20, ModifiedNS: 1, DurationMS: 240_000}
	a, err := s.analyse(context.Background(), nil, nil, "", song)
	if err == nil && a.Failed != "" {
		t.Errorf("a song whose file could not be opened was given the lasting answer %q; it is saved and never tried again", a.Failed)
	}
}

// The pace of this server is not guessed: until a few songs are read there is none, then it
// is the time a song takes divided by the songs read side by side.
func TestThePaceIsMeasuredHere(t *testing.T) {
	s := &Service{}
	for range rateKnown - 1 {
		s.measured(4 * time.Second)
	}
	if _, known := s.PerSong(); known {
		t.Fatal("a pace was given before enough songs were read")
	}
	s.measured(4 * time.Second)
	if per, known := s.PerSong(); !known || per != 4*time.Second/SongsAtOnce {
		t.Fatalf("the pace is %v (%v)", per, known)
	}
}
