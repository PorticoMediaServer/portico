package playbackv1

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
)

func TestObservationObservedOutputRequiresCurrentDeviceGenerationAndSequence(t *testing.T) {
	db := pausedTestDB(t)
	ctx := context.Background()
	now := time.UnixMilli(1_750_000_000_000)
	s := &Service{DB: db, Now: func() time.Time { return now }}
	caller := pausedCaller()
	r := newReporter(t, s, caller)
	insertPausedSession(t, db, "observed", "vod", "playing", now.UnixMilli(), 0)
	read := func(generation int) *playback.ObservedPlayback {
		t.Helper()
		tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		got, err := playback.ReadObservedPlayback(ctx, tx, "observed", generation, generation)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	actual := &playback.ObservedPlayback{Video: &playback.ObservedVideo{Width: 1920, Height: 1080}, Audio: &playback.ObservedAudioOutput{SampleRate: 44100, Channels: 2}}
	if err := r.report("observed", Observation{Generation: 1, State: "playing", ObservedPlayback: actual}); err != nil {
		t.Fatal(err)
	}
	if got := read(1); !reflect.DeepEqual(got, actual) {
		t.Fatalf("actual sparse output lost: %+v", got)
	}
	wrong := &playback.ObservedPlayback{Screen: &playback.ObservedScreen{DynamicRange: "dolby_vision"}}
	for _, ob := range []Observation{{Seq: 1, Generation: 1, State: "playing", ObservedPlayback: wrong}, {Seq: 2, Generation: 0, State: "playing", ObservedPlayback: wrong}} {
		ob.Epoch = r.epochs["observed"]
		if _, err := s.Observe(ctx, caller, "observed", ob); err != nil {
			t.Fatal(err)
		}
		r.apply("observed")
		if !reflect.DeepEqual(read(1), actual) {
			t.Fatal("replay/stale generation replaced actual output")
		}
	}
	r.seq["observed"] = 2
	controller := caller
	controller.DeviceID = "other-device"
	if _, err := s.Observe(ctx, controller, "observed", Observation{Epoch: r.epochs["observed"], Seq: 3, Generation: 1, State: "playing", ObservedPlayback: wrong}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("controller supplied output: %v", err)
	}
	if err := r.report("observed", Observation{Generation: 1, State: "playing", ObservedPlayback: &playback.ObservedPlayback{}}); err != nil {
		t.Fatal(err)
	}
	if got := read(1); !reflect.DeepEqual(got, &playback.ObservedPlayback{}) {
		t.Fatalf("new snapshot inherited old facts: %+v", got)
	}
	// This unit fixture models the existing presentation replacement. The
	// observation must disappear before any new-generation report arrives.
	if _, err := db.Exec(`UPDATE playback_v1_sessions SET generation=2,last_seq=-1 WHERE id='observed'`); err != nil {
		t.Fatal(err)
	}
	if got := read(2); got != nil {
		t.Fatalf("new presentation inherited old output: %+v", got)
	}
	if got := read(1); got != nil {
		t.Fatalf("obsolete generation remained readable: %+v", got)
	}
	if err := r.report("observed", Observation{Generation: 2, State: "playing", ObservedPlayback: actual}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(read(2), actual) {
		t.Fatal("current-generation report was not stored")
	}
	if err := r.report("observed", Observation{Generation: 2, State: "ended"}); err != nil {
		t.Fatal(err)
	}
	if got := read(2); got != nil {
		t.Fatalf("ended session published output: %+v", got)
	}
}

func TestObservedOutputStoreCannotBypassAcceptedObservationBinding(t *testing.T) {
	db := pausedTestDB(t)
	ctx := context.Background()
	now := time.UnixMilli(1_750_000_000_000)
	insertPausedSession(t, db, "binding", "vod", "playing", now.UnixMilli(), 0)
	if _, err := db.Exec(`UPDATE playback_v1_sessions SET last_seq=5 WHERE id='binding'`); err != nil {
		t.Fatal(err)
	}
	actual := &playback.ObservedPlayback{Audio: &playback.ObservedAudioOutput{SampleRate: 48000}}
	for _, test := range []struct {
		name, device                              string
		sessionGeneration, presentationGeneration int
		seq                                       int64
	}{
		{"not-accepted", "dev1", 1, 1, 6}, {"stale-sequence", "dev1", 1, 1, 4}, {"other-device", "dev2", 1, 1, 5}, {"stale-session", "dev1", 2, 2, 5}, {"unproved-presentation", "dev1", 1, 9, 5},
	} {
		t.Run(test.name, func(t *testing.T) {
			tx, err := db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			if err = playback.StoreObservedPlaybackTx(ctx, tx, "binding", test.device, test.sessionGeneration, test.presentationGeneration, test.seq, now.UnixMilli(), actual); err != nil {
				t.Fatal(err)
			}
			var count int
			if err = tx.QueryRow(`SELECT COUNT(*) FROM playback_observed_output WHERE session_id='binding'`).Scan(&count); err != nil || count != 0 {
				t.Fatalf("unaccepted binding wrote observation: %d %v", count, err)
			}
		})
	}
}

// This models the existing ChannelEngine authority for transaction tests. It
// does not qualify a tuner/provider/runtime/native renderer. The canonical DB
// buffer_ordinal guard is intentionally independent of this callback's answer.
type observedChannelAuthority struct {
	generation   string
	reject       bool
	observations int
}

func (*observedChannelAuthority) Configured() bool { return true }
func (*observedChannelAuthority) StartTx(context.Context, *sql.Tx, identity.Principal, playback.ChannelStart) (playback.LinearSelection, error) {
	panic("not a start test")
}
func (a *observedChannelAuthority) ReadTx(context.Context, *sql.Tx, identity.Principal, string) (playback.ChannelState, error) {
	return playback.ChannelState{Media: playback.LinearMedia{BufferGeneration: a.generation}}, nil
}
func (*observedChannelAuthority) SetIntentTx(context.Context, *sql.Tx, identity.Principal, string, playback.LinearDesired) (playback.LinearDesired, error) {
	panic("not an intent test")
}
func (a *observedChannelAuthority) ObserveTx(context.Context, *sql.Tx, string, string, int64, int64, string) error {
	if a.reject {
		return &playback.ControlFault{Code: "stale_generation", HTTPStatus: 409}
	}
	a.observations++
	return nil
}
func (*observedChannelAuthority) EndTx(context.Context, *sql.Tx, string) (bool, error) {
	return false, nil
}

func TestObservedLiveOutputKeepsSessionAndBufferGenerationsDistinct(t *testing.T) {
	db := pausedTestDB(t)
	ctx := context.Background()
	now := time.UnixMilli(1_750_000_000_000)
	insertPausedSession(t, db, "observed-live", "channel", "playing", now.UnixMilli(), 0)
	_, err := db.Exec(`INSERT INTO playback_channel_sessions(session_id,authority,account_id,profile_id,server_id,role,account_epoch,family_id,kind,source_id,channel_id,selection_json,transport_json,buffer_ordinal,status,stage,created_ms) VALUES('observed-live','local','owner','primary','unit','local',1,'unit','live-source','unit-source','unit-channel','{}','{}',7,'active','engine',?)`, now.UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	authority := &observedChannelAuthority{generation: "7"}
	s := &Service{DB: db, Channels: authority, Now: func() time.Time { return now }}
	r := newReporter(t, s, pausedCaller())
	actual := &playback.ObservedPlayback{Video: &playback.ObservedVideo{Width: 1280, Height: 720}}
	read := func(sessionGeneration, bufferGeneration int) *playback.ObservedPlayback {
		t.Helper()
		tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		got, err := playback.ReadObservedPlayback(ctx, tx, "observed-live", sessionGeneration, bufferGeneration)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	if err = r.report("observed-live", Observation{Generation: 7, State: "playing", ObservedPlayback: actual}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(read(1, 7), actual) || read(7, 7) != nil || read(1, 1) != nil {
		t.Fatal("session/buffer generations were collapsed")
	}
	if view, err := s.Get(ctx, pausedCaller(), "observed-live"); err != nil || view.Presentation.Generation != 7 || !reflect.DeepEqual(view.ObservedPlayback, actual) {
		t.Fatal("live read collapsed buffer/session generations", view, err)
	}

	if _, err = db.Exec(`UPDATE playback_channel_sessions SET buffer_ordinal=8 WHERE session_id='observed-live'`); err != nil {
		t.Fatal(err)
	}
	authority.generation = "8"
	if read(1, 7) != nil || read(1, 8) != nil {
		t.Fatal("new buffer inherited previous output")
	}
	if view, err := s.Get(ctx, pausedCaller(), "observed-live"); err != nil || view.ObservedPlayback != nil {
		t.Fatal("live GET inherited old buffer output", view.ObservedPlayback, err)
	}

	if err = r.report("observed-live", Observation{Generation: 8, State: "playing", ObservedPlayback: actual}); err != nil {
		t.Fatal(err)
	}
	wrong := &playback.ObservedPlayback{Screen: &playback.ObservedScreen{DynamicRange: "hdr10"}}
	if err = r.report("observed-live", Observation{Generation: 7, State: "playing", ObservedPlayback: wrong}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(read(1, 8), actual) {
		t.Fatal("stale buffer changed output")
	}
	authority.reject = true
	if err = r.report("observed-live", Observation{Generation: 8, State: "playing", ObservedPlayback: wrong}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(read(1, 8), actual) {
		t.Fatal("rejected engine observation changed output")
	}
	// Even a stale callback reporting success cannot override the actual
	// persisted buffer tuple. Its old output also becomes unreadable.
	authority.reject = false
	if _, err = db.Exec(`UPDATE playback_channel_sessions SET buffer_ordinal=9 WHERE session_id='observed-live'`); err != nil {
		t.Fatal(err)
	}
	if err = r.report("observed-live", Observation{Generation: 8, State: "playing", ObservedPlayback: wrong}); err != nil {
		t.Fatal(err)
	}
	if read(1, 8) != nil || read(1, 9) != nil {
		t.Fatal("callback bypassed canonical buffer generation")
	}
}

// The authorized session GET is the read side of the observation protocol's
// sparse output snapshot, not a new output observation writer or source plan.
func TestSessionGetPublishesOnlyCurrentObservedOutput(t *testing.T) {
	db := pausedTestDB(t)
	ctx := context.Background()
	now := time.UnixMilli(1_750_000_000_000)
	service := &Service{DB: db, Now: func() time.Time { return now }}
	caller := pausedCaller()
	r := newReporter(t, service, caller)
	insertPausedSession(t, db, "read-output", "vod", "playing", now.UnixMilli(), 0)
	get := func() SessionView {
		t.Helper()
		view, err := service.Get(ctx, caller, "read-output")
		if err != nil {
			t.Fatal(err)
		}
		return view
	}
	if get().ObservedPlayback != nil {
		t.Fatal("unreported output inferred from session/quality/source")
	}
	actual := &playback.ObservedPlayback{Video: &playback.ObservedVideo{Width: 1920, Height: 1080}, Audio: &playback.ObservedAudioOutput{SampleRate: 44100, Channels: 2}}
	if err := r.report("read-output", Observation{Generation: 1, State: "playing", ObservedPlayback: actual}); err != nil {
		t.Fatal(err)
	}
	if view := get(); !reflect.DeepEqual(view.ObservedPlayback, actual) || view.ObservedPlayback.Screen != nil {
		t.Fatal("actual output lost or screen inferred", view.ObservedPlayback)
	}
	controller := caller
	controller.DeviceID = "controller"
	if view, err := service.Get(ctx, controller, "read-output"); err != nil || !reflect.DeepEqual(view.ObservedPlayback, actual) {
		t.Fatal("authorized controller lost current facts", view.ObservedPlayback, err)
	}
	other := caller
	other.Principal.ProfileID = "other-profile"
	if _, err := service.Get(ctx, other, "read-output"); !errors.Is(err, ErrNotFound) {
		t.Fatal("output crossed profile authority", err)
	}
	if err := r.report("read-output", Observation{Generation: 1, State: "playing", ObservedPlayback: &playback.ObservedPlayback{}}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(get().ObservedPlayback, &playback.ObservedPlayback{}) {
		t.Fatal("cleared output inherited old blocks")
	}
	if _, err := db.Exec(`UPDATE playback_v1_sessions SET generation=2,last_seq=-1,revision=revision+1 WHERE id='read-output'`); err != nil {
		t.Fatal(err)
	}
	if get().ObservedPlayback != nil {
		t.Fatal("new presentation inherited previous output")
	}
	if err := r.report("read-output", Observation{Generation: 2, State: "playing", ObservedPlayback: actual}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(get().ObservedPlayback, actual) {
		t.Fatal("new accepted output missing")
	}
	if err := r.report("read-output", Observation{Generation: 2, State: "ended"}); err != nil {
		t.Fatal(err)
	}
	if get().ObservedPlayback != nil {
		t.Fatal("ended session retained output")
	}
}
