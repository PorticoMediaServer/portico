package remotesources

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/ownedcapture"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

func TestCaptureRetainedLifeFollowsSourceRetirementWithoutProcess(t *testing.T) {
	s := &Service{}
	profile, _ := SourceReadPreset("once")
	profile.SourceID, profile.Revision = "source", 7
	life, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := s.trackCaptureLife("source", profile, 3, life, cancel); err != nil {
		t.Fatal(err)
	}
	// A same-profile replay preserves the original reader. No provider process
	// or progressive source lease is opened to receive this cancellation.
	s.stopReadLeases("source", storage.ErrRemoteChanged, &profile)
	s.retireCaptureSettings("source", 3)
	if life.Err() != nil || len(s.serveLeases) != 0 || len(s.readLeases) != 0 {
		t.Fatal("same policy retired the reader or allocated provider custody")
	}
	s.retireCaptureSettings("source", 4)
	if !errors.Is(life.Err(), context.Canceled) {
		t.Fatal("committed settings change left retained helper lifetime live")
	}
}

func TestCaptureRetainedLifeFollowsProfileAndShutdown(t *testing.T) {
	for _, all := range []bool{false, true} {
		s := &Service{}
		profile, _ := SourceReadPreset("once")
		profile.SourceID, profile.Revision = "source", 7
		life, cancel := context.WithCancel(context.Background())
		if err := s.trackCaptureLife("source", profile, 3, life, cancel); err != nil {
			t.Fatal(err)
		}
		if all {
			s.stopReadLeases("", storage.ErrRemoteOffline, nil)
		} else {
			next := profile
			next.Revision++
			s.stopReadLeases("source", storage.ErrRemoteChanged, &next)
		}
		if life.Err() == nil {
			t.Fatal("retirement left the original retained reader live")
		}
		cancel()
	}
}

func TestCaptureQuotaCountsPendingPrefixAndRetainedCharge(t *testing.T) {
	if captureCeilingAllows(20, 10, 60, 11, 100) {
		t.Fatal("pending prefix or full retained charge escaped the ceiling")
	}
	if !captureCeilingAllows(20, 10, 60, 10, 100) {
		t.Fatal("exact remaining quota refused")
	}
	if captureCeilingAllows(math.MaxInt64, 1, 1, 1, math.MaxInt64) {
		t.Fatal("overflow granted quota")
	}
}

func TestCaptureAdmitRefusesOuterWriterBeforeSourceWork(t *testing.T) {
	// No source or identity service exists: the original transaction must be
	// refused before resolving a target, reserving space or opening bytes.
	p := &captureProducer{}
	ctx := dbwork.WithWrite(context.Background(), new(sql.Tx), dbwork.ClassPlaybackStart)
	_, err := p.Admit(ctx, "0123456789012345678901234567890123456789012345678901234567890123", ownedcapture.Request{}, ownedcapture.Destination{})
	if !errors.Is(err, ownedcapture.ErrInput) {
		t.Fatalf("outer writer was not refused: %v", err)
	}
}
