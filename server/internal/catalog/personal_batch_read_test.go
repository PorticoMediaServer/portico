package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/personalstate"
)

func TestPersonalBatchMatchesIndividualStateAndSnapshots(t *testing.T) {
	sizes := []int{8}
	if os.Getenv("PORTICO_PERFORMANCE_TIER") == "release" || os.Getenv("PORTICO_PERFORMANCE_TIER") == "deep" {
		sizes = []int{8, 500}
	}
	for _, n := range sizes {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			s, _, _, v, _, _, names := bulkFixture(t, n)
			yes := true
			no := false
			progress := 45.25
			for i, m := range []PersonalMutation{{Favorite: &yes}, {Watchlisted: &yes}, {Rating: json.RawMessage(`8`)}, {Watched: &no, ProgressSeconds: &progress}} {
				m.OperationID = fmt.Sprint("batch-", i)
				m.ExpectedRevision = int64(i)
				if _, err := s.SetPersonal("a", v.Profile, names["ep000000"].Public, m, nil); err != nil {
					t.Fatal(err)
				}
			}

			ids := make([]int64, n)
			for i := range ids {
				ids[i] = names[fmt.Sprintf("ep%06d", i)].ID
			}
			ctx, trace := dbwork.TraceStatements(context.Background())
			err := dbwork.WithWriteTx(ctx, s.db, dbwork.ClassInteractive, func(tx *sql.Tx) error {
				states, err := readPersonalBatch(ctx, tx, v.Profile, ids)
				if err != nil {
					return err
				}
				batchTrace := trace()
				reads := 0
				for _, q := range batchTrace {
					if strings.HasPrefix(q, "SELECT e.id,") {
						reads++
					}
				}
				if reads != 1 {
					t.Fatalf("batch used %d statements for %d items", reads, n)
				}
				for _, id := range ids {
					want, err := readPersonalStateByID(tx, v.Profile, id, personalstate.SQL("?", "?"))
					if err != nil {
						return err
					}
					if !reflect.DeepEqual(states[id], want) {
						t.Fatalf("batch differs for %d: got %+v want %+v", id, states[id], want)
					}
				}
				if err := snapshotPersonalBatch(ctx, tx, v.Profile, ids, states); err != nil {
					return err
				}
				for _, id := range ids {
					var raw string
					if err := tx.QueryRow(`SELECT state FROM personal_snapshots WHERE profile_id=? AND item_id=? AND revision=?`, v.Profile, id, states[id].Revision).Scan(&raw); err != nil {
						return err
					}
					var state PersonalState
					if err := json.Unmarshal([]byte(raw), &state); err != nil {
						return err
					}
					if !reflect.DeepEqual(state, states[id]) {
						t.Fatalf("snapshot differs for %d", id)
					}
				}
				other, err := readPersonalBatch(ctx, tx, "unrelated-profile", ids)
				if err != nil {
					return err
				}
				for _, state := range other {
					if state.Favorite || state.Revision != 0 || state.ProgressSeconds != 0 {
						t.Fatal("profile leaked")
					}
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestContainerActionsUseConstantStatementBatches(t *testing.T) {
	sizes := []int{8}
	switch os.Getenv("PORTICO_PERFORMANCE_TIER") {
	case "release":
		sizes = []int{8, 500}
	case "deep":
		sizes = []int{8, 500, 5000}
	}
	for _, n := range sizes {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			s, scheduler, p, v, access, _, names := bulkFixture(t, n)
			ctx, trace := dbwork.TraceQueryPlans(context.Background())
			yes := true
			dbwork.WriteGate().ResetPeak()
			start := time.Now()
			marked, err := s.SetContainerPersonal(ctx, p, "show", names["show"].Public, ContainerPersonalMutation{Watched: &yes}, scheduler, access)
			if err != nil {
				t.Fatal(err)
			}
			count := len(trace())
			if count > 32 {
				t.Fatalf("mark issued %d statements for %d members", count, n)
			}
			t.Logf("members=%d mark=%s statements=%d gate_ms=%d", n, time.Since(start), count, dbwork.WriteGate().Stats().MaxHeldMilli)
			ctx, trace = dbwork.TraceQueryPlans(context.Background())
			start = time.Now()
			dbwork.WriteGate().ResetPeak()
			if _, err := s.UndoContainerPersonal(ctx, p, "show", names["show"].Public, ContainerUndoMutation{ExpectedRevision: marked.Revision, UndoToken: marked.UndoToken}, access); err != nil {
				t.Fatal(err)
			}
			if count := len(trace()); count > 32 {
				t.Fatalf("undo issued %d statements for %d members", count, n)
			}
			counts, err := s.ContainerPersonalCounts(v, "show", names["show"].Public)
			if err != nil || counts.WatchedCount != 0 {
				t.Fatal(counts, err)
			}
			t.Logf("members=%d undo=%s statements=%d gate_ms=%d", n, time.Since(start), len(trace()), dbwork.WriteGate().Stats().MaxHeldMilli)
		})
	}
}
