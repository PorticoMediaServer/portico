package metadata

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/metadataprovider"
)

func mbFinalTypicalFixture(t *testing.T) (*Service, mbJob, mbReleaseEvidence, mbBuildProof) {
	t.Helper()
	db, _ := mbLaneDB(t)
	s := New(db, "")
	j := mbStagingJob(t, s, "album")
	value := mbLaneReleaseFixture()
	sample := value.Media[0].Tracks[0]
	value.Media[0].Tracks = nil
	value.ArtistCredit = sample.ArtistCredit
	for n := 1; n <= 12; n++ {
		track := sample
		track.ID = fmt.Sprintf("00000600-0000-0000-0000-%012x", n)
		track.Position = n
		track.Number = fmt.Sprint(n)
		track.Recording.ID = fmt.Sprintf("00000500-0000-0000-0000-%012x", n)
		value.Media[0].Tracks = append(value.Media[0].Tracks, track)
	}
	stage, e := stageMBRelease(metadataprovider.ReleaseLookup{RequestedID: value.ID, Release: value})
	if e != nil {
		t.Fatal(e)
	}
	proof, e := s.mbStageRelease(context.Background(), j, stage, "now")
	if e != nil {
		t.Fatal(e)
	}
	if e = s.mbAttachStaged(context.Background(), j, proof); e != nil {
		t.Fatal(e)
	}
	return s, j, stage, proof
}

func TestMBStagingTypicalReleaseFinalWriterHold(t *testing.T) {
	s, j, stage, proof := mbFinalTypicalFixture(t)
	dbwork.ResetPlans()
	dbwork.WriteGate().ResetPeak()
	started := time.Now()
	if e := s.mbPublishAlbumReady(context.Background(), j, stage, proof, "now"); e != nil {
		t.Fatal(e)
	}
	elapsed := time.Since(started)
	held := dbwork.WriteGate().Stats().MaxHeldMilli
	cost := dbwork.Plans().Largest
	t.Logf("typical12-track complete final incl adoption: elapsed=%s peak-held=%dms rows=%d pages=%d VM=%d overruns=%d", elapsed, held, cost.Rows, cost.Pages, cost.VMOps, dbwork.Plans().Overruns)
	if held >= uint64(mbFinalOrdinaryHold/time.Millisecond) {
		t.Fatal("typical release exceeded250ms", elapsed, held)
	}
	if dbwork.Plans().Overruns != 0 {
		t.Fatal(dbwork.Plans())
	}
	assertMBAccounting(t, s.db)
}

// Queue the actual publisher first, then every user class, behind a held
// writer. Gate-observed waiting tickets establish order without timing guesses.
// Every queued user must acquire/finish before this ClassBackgroundMedia final.
func TestMBStagingFinalWaitsForEveryQueuedUser(t *testing.T) {
	s, j, stage, proof := mbFinalTypicalFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	held, e := dbwork.Begin(ctx, s.db, dbwork.ClassInteractive)
	if e != nil {
		t.Fatal(e)
	}
	defer held.Rollback()
	final := make(chan error, 1)
	go func() { final <- s.mbPublishAlbumReady(ctx, j, stage, proof, "now") }()
	wait := func(class dbwork.Class) {
		t.Helper()
		for dbwork.WriteGate().Waiting()[class.String()] < 1 {
			select {
			case <-ctx.Done():
				t.Fatal("writer never queued", class, dbwork.WriteGate().Waiting())
			case <-time.After(time.Millisecond):
			}
		}
	}
	wait(dbwork.ClassBackgroundMedia)
	type userWriter struct {
		class dbwork.Class
		write *dbwork.Write
		err   error
	}
	users := make(chan userWriter, 6)
	var classes []dbwork.Class
	for _, class := range dbwork.Classes() {
		if class.Background() {
			continue
		}
		classes = append(classes, class)
		go func(class dbwork.Class) {
			w, e := dbwork.Begin(ctx, s.db, class)
			if w != nil {
				defer w.Rollback()
			}
			users <- userWriter{class: class, write: w, err: e}
			// The receiver commits; cancellation also releases any acquired write
			// if an earlier assertion terminates this test before receipt.
			<-ctx.Done()
		}(class)
		wait(class)
	}
	if e = held.Rollback(); e != nil {
		t.Fatal(e)
	}
	for _, class := range classes {
		select {
		case user := <-users:
			if user.err != nil {
				t.Fatal(user.err)
			}
			if user.class != class {
				user.write.Rollback()
				t.Fatalf("user order %s, want %s", user.class, class)
			}
			if dbwork.WriteGate().Waiting()[dbwork.ClassBackgroundMedia.String()] != 1 {
				user.write.Rollback()
				t.Fatal("final overtook a queued user")
			}
			select {
			case e := <-final:
				user.write.Rollback()
				t.Fatal("final completed before queued users", e)
			default:
			}
			if _, e = user.write.Tx().ExecContext(ctx, `SELECT 1`); e != nil {
				user.write.Rollback()
				t.Fatal(e)
			}
			if e = user.write.Commit(); e != nil {
				t.Fatal(e)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	select {
	case e := <-final:
		if e != nil {
			t.Fatal(e)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	t.Logf("actual final waited behind all%d queued user classes, in gate priority order", len(classes))
	assertMBAccounting(t, s.db)
}

type mbTimingLogCapture struct {
	bytes.Buffer
	gateHeld bool
}

func (w *mbTimingLogCapture) Write(p []byte) (int, error) {
	if dbwork.WriteGate().Held() {
		w.gateHeld = true
	}
	return w.Buffer.Write(p)
}

// A synthetic elapsed clock exercises both ceilings without holding any real
// writer for hundreds of milliseconds. Reporting still follows actual rollback.
func TestMBStagingFinalTimingLogsAndAssertsAfterGateRelease(t *testing.T) {
	db, _ := mbLaneDB(t)
	capture := &mbTimingLogCapture{}
	previous := log.Writer()
	log.SetOutput(capture)
	defer log.SetOutput(previous)
	for _, tc := range []struct {
		name          string
		elapsed       time.Duration
		tracks        int
		panicExpected bool
	}{
		{"ordinary-fast", time.Millisecond, 12, false},
		{"ordinary-over250", 251 * time.Millisecond, 12, true},
		{"extreme-over250", 300 * time.Millisecond, mbEvidenceTracks, false},
		{"extreme-over500", 501 * time.Millisecond, mbEvidenceTracks, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			capture.Reset()
			capture.gateHeld = false
			w, e := dbwork.Begin(context.Background(), db, dbwork.ClassBackgroundMedia)
			if e != nil {
				t.Fatal(e)
			}
			timer := mbFinalStepTiming{kind: "album", newTracks: tc.tracks, oldTracks: tc.tracks}
			timer.start()
			timer.started = timer.started.Add(-tc.elapsed)
			var recovered any
			func() {
				defer func() { recovered = recover() }()
				defer timer.report()
				defer func() { w.Rollback(); timer.stop() }()
			}()
			if (recovered != nil) != tc.panicExpected {
				t.Fatal("timing assertion mismatch", recovered)
			}
			lines := strings.Count(capture.String(), "MusicBrainz final publication duration=")
			want := 0
			if tc.elapsed > mbFinalOrdinaryHold {
				want = 1
			}
			if lines != want || capture.gateHeld {
				t.Fatalf("loglines=%d want=%d gateheld=%v log=%s", lines, want, capture.gateHeld, capture.String())
			}
			if want == 1 && (!strings.Contains(capture.String(), fmt.Sprintf("new_tracks=%d old_tracks=%d", tc.tracks, tc.tracks))) {
				t.Fatal(capture.String())
			}
		})
	}
}
