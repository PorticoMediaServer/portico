package playbackv1

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// Real evidence crosses the started threshold once, then returns to the
// checkpoint budget. History, resume and sequence must all commit together.
func TestObservationHistoryActivationAndRoutineBudgets(t *testing.T) {
	f := newObservationFixture(t)
	personal := catalog.New(f.catalog.DB)
	f.service.Playback.PersonalProgress = personal.AcceptPlaybackProgressEvidence
	id := f.session.ID
	f.catalog.Exec(`UPDATE playback_sessions SET duration=60 WHERE id=(SELECT media_session_id FROM playback_v1_sessions WHERE id=?)`, id)
	epoch := f.claim(id, "")
	t.Cleanup(func() {
		if err := f.service.CloseObservations(context.Background()); err != nil {
			t.Error(err)
		}
	})
	profile := identity.PersonalKey(f.caller.Principal.Viewer)
	rows, err := f.catalog.DB.Query("EXPLAIN QUERY PLAN "+observationHistoryWitnessSQL, profile, id)
	if err != nil {
		t.Fatal(err)
	}
	plans := []string{}
	for rows.Next() {
		var a, b, c int
		var detail string
		if err := rows.Scan(&a, &b, &c, &detail); err != nil {
			t.Fatal(err)
		}
		plans = append(plans, detail)
		if strings.Contains(detail, "SCAN ") {
			t.Errorf("unbounded history witness: %s", detail)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	if len(plans) == 0 {
		t.Fatal("missing witness plans")
	}
	for seq := int64(1); seq <= 5; seq++ {
		f.clock.add(time.Second)
		_, err := f.service.Observe(context.Background(), f.caller, id, Observation{Epoch: epoch, Seq: seq, Generation: f.generation, State: "playing", PositionMs: seq * 1000, MonotonicMs: seq * 1000, PlayClockMs: seq * 1000, Rate: 1})
		if err != nil {
			t.Fatal(err)
		}
		dbwork.ResetPlans()
		if err := f.service.FlushObservations(context.Background()); err != nil {
			t.Fatal(err)
		}
		measured := dbwork.Plans()
		budget := observationBatchPlan(1)
		if seq == 3 {
			budget = observationActivationPlan(1, 1)
		}
		cost := measured.Largest
		if measured.Measured != 1 || measured.Overruns != 0 || cost.Rows == 0 || cost.Rows > budget.Rows || cost.Pages > budget.Pages || cost.VMOps == 0 || cost.VMOps > budget.VMOps {
			t.Fatalf("seq%d cost%+v budget%+v stats%+v", seq, cost, budget, measured)
		}
		var durableSeq, position, resume int64
		var history int
		if err := f.catalog.DB.QueryRow(`SELECT last_seq,position_ms FROM playback_v1_sessions WHERE id=?`, id).Scan(&durableSeq, &position); err != nil {
			t.Fatal(err)
		}
		if err := f.catalog.DB.QueryRow(`SELECT position FROM progress WHERE profile_id=? AND playback_id=(SELECT media_session_id FROM playback_v1_sessions WHERE id=?)`, profile, id).Scan(&resume); err != nil {
			t.Fatal(err)
		}
		if err := f.catalog.DB.QueryRow(`SELECT count(*) FROM personal_history WHERE profile_id=? AND id=(SELECT media_session_id FROM playback_v1_sessions WHERE id=?)`, profile, id).Scan(&history); err != nil {
			t.Fatal(err)
		}
		wantHistory := 0
		wantResume := int64(0)
		if seq >= 3 {
			wantHistory = 1
			wantResume = seq * 1000
		}
		if durableSeq != seq || position != seq*1000 || history != wantHistory || resume != wantResume {
			t.Fatalf("seq%d durable%d position%d history%d resume%d", seq, durableSeq, position, history, resume)
		}
		t.Logf("seq%d %s rows%d pages%d VM%d", seq, budget.Name, cost.Rows, cost.Pages, cost.VMOps)
	}
}

func TestObservationMixedActivationPlanKeepsRoutineUnits(t *testing.T) {
	for sessions := 1; sessions <= observationBatch; sessions++ {
		routine := observationBatchPlan(sessions)
		if got := observationActivationPlan(sessions, 0); got != routine {
			t.Fatalf("routine declaration changed: %+v / %+v", got, routine)
		}
		for activations := 1; activations <= sessions; activations++ {
			got := observationActivationPlan(sessions, activations)
			if got.Rows != routine.Rows || got.VMOps != routine.VMOps || got.Pages != int64(32*(sessions-activations)+96*activations) || got.Why == "" || got.Name == routine.Name {
				t.Fatalf("%d/%d: %+v", activations, sessions, got)
			}
		}
	}
}
