package read

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/readfixture"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

const mpQueueSeed = `INSERT INTO queues_v1(id,owner_kind,owner_id,authority,account_id,profile_id,revision,current_entry_id,current_position,repeat,shuffle_on,shuffle_seed,shuffle_lap,shuffle_domain,shuffle_first,session_id,automatic_advances,custody_generation,created_ms,updated_ms)
VALUES('mp-queue','device','d','local','a','p',3,'snapshot:4',4,'all',1,19,2,10,4,'mp-session',2,7,1000,2000)`

func mpQueueSource(t *testing.T) *browseFixture {
	t.Helper()
	f := newForegroundSource(t)
	f.exec(t, mpQueueSeed)
	return f
}
func mpQueueFixture(t *testing.T) (*browseFixture, *MediaReader) {
	t.Helper()
	f := composeBrowseFixture(t, mpQueueSource(t), MediaHarnessDefinitions())
	return f, &MediaReader{snapshots: f.snapshots, pool: f.snapshots}
}
func TestMPQueueHead(t *testing.T) {
	f, r := mpQueueFixture(t)
	in := MediaQueueInput{View: f.input, Queue: "mp-queue"}
	got, result, err := r.queueHead(t.Context(), foregroundRequest(), time.Second, f.output, in)
	want := MediaQueueHead{ID: "mp-queue", Revision: 3, CustodyGeneration: 7, CurrentEntry: "snapshot:4", Position: 4, Repeat: "all", Shuffle: true, ShuffleSeed: 19, ShuffleLap: 2, ShuffleDomain: 10, ShuffleFirst: 4, Session: "mp-session", AutomaticAdvances: 2, CreatedMS: 1000, UpdatedMS: 2000}
	if err != nil || got != want || result.LookupStatements != 6 || result.PublicationStatements != 6 || len(result.LookupWindows) != 1 {
		t.Fatalf("queue %+v result %+v error %v", got, result, err)
	}
	for _, id := range []string{"", strings.Repeat("q", 129), "bad\x00id", "\xff"} {
		in.Queue = id
		got, _, err = r.queueHead(t.Context(), foregroundRequest(), time.Second, f.output, in)
		if !errors.Is(err, ErrInput) || got != (MediaQueueHead{}) {
			t.Fatal("invalid queue ID", err)
		}
	}
}
func TestMPQueueOwnerFence(t *testing.T) {
	for _, mutation := range []string{
		`UPDATE queues_v1 SET owner_id='foreign'`,
		`UPDATE queues_v1 SET owner_kind='group'`,
		`UPDATE queues_v1 SET account_id='b'`,
		`UPDATE queues_v1 SET profile_id='q'`,
		`UPDATE queues_v1 SET authority='hosted'`,
		`UPDATE authorization_session_families SET revoked=1`,
	} {
		t.Run(mutation, func(t *testing.T) {
			f, r := mpQueueFixture(t)
			f.exec(t, mutation)
			got, _, err := r.queueHead(t.Context(), foregroundRequest(), time.Second, f.output, MediaQueueInput{View: f.input, Queue: "mp-queue"})
			if !errors.Is(err, ErrDenied) || got != (MediaQueueHead{}) {
				t.Fatal("owner leaked", got, err)
			}
		})
	}
}
func TestMPQueuePublication(t *testing.T) {
	for _, tc := range []struct {
		name, mutation string
		denied         bool
	}{
		{"revision", `UPDATE queues_v1 SET revision=revision+1`, false},
		{"custody", `UPDATE queues_v1 SET custody_generation=custody_generation+1`, false},
		{"unversioned entry", `UPDATE queues_v1 SET current_entry_id='snapshot:8'`, false},
		{"revoked", `UPDATE authorization_session_families SET revoked=1`, true},
		{"foreign device", `UPDATE queues_v1 SET owner_id='foreign'`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, r := mpQueueFixture(t)
			r.snapshots = &foregroundBoundary{Snapshots: f.snapshots, mutate: func(phase int) {
				if phase == 1 {
					f.exec(t, tc.mutation)
				}
			}}
			got, result, err := r.queueHead(t.Context(), foregroundRequest(), time.Second, f.output, MediaQueueInput{View: f.input, Queue: "mp-queue"})
			if tc.denied {
				if !errors.Is(err, ErrDenied) || got != (MediaQueueHead{}) {
					t.Fatal("publication leaked", got, err)
				}
				return
			}
			if err != nil || result.Attempts != 2 {
				t.Fatal("no complete retry", result, err)
			}
			if tc.name == "revision" && got.Revision != 4 || tc.name == "custody" && got.CustodyGeneration != 8 || tc.name == "unversioned entry" && got.CurrentEntry != "snapshot:8" {
				t.Fatal("stale queue", got)
			}
		})
	}
	f, r := mpQueueFixture(t)
	r.snapshots = &foregroundBoundary{Snapshots: f.snapshots, mutate: func(int) { f.exec(t, `UPDATE queues_v1 SET revision=revision+1`) }}
	got, result, err := r.queueHead(t.Context(), foregroundRequest(), time.Second, f.output, MediaQueueInput{View: f.input, Queue: "mp-queue"})
	if !errors.Is(err, ErrStale) || !errors.Is(err, governor.ErrBusy) || result.Attempts != 2 || got != (MediaQueueHead{}) {
		t.Fatal("unbounded churn", got, result, err)
	}
}
func TestMPQueueCancellationAndAdmission(t *testing.T) {
	f, r := mpQueueFixture(t)
	in := MediaQueueInput{View: f.input, Queue: "mp-queue"}
	_, request, _ := liveRequests("mp-no-admission")
	got, result, err := r.QueueHead(t.Context(), request, time.Second, BrowseOutput{}, in)
	if !errors.Is(err, ErrInput) || result.LookupStatements != 0 || got != (MediaQueueHead{}) {
		t.Fatal("missing admission", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	r.snapshots = &browseBoundarySnapshots{Snapshots: f.snapshots, afterBusiness: cancel}
	got, _, err = r.queueHead(ctx, foregroundRequest(), time.Second, f.output, in)
	if !errors.Is(err, context.Canceled) || got != (MediaQueueHead{}) {
		t.Fatal("cancel leaked", got, err)
	}
}

var recordMPQueuePlans = flag.Bool("record-mp-queue-plans", false, "record MP queue native plans")

func TestMPQueuePlans(t *testing.T) {
	f := mpQueueSource(t)
	defs := mpQueueDefinitions()
	for _, d := range defs {
		for _, c := range d.Cohorts {
			rows, err := f.conn.QueryContext(t.Context(), d.SQL, c.Bindings...)
			if err != nil {
				t.Fatal(err)
			}
			present := rows.Next()
			err = rows.Err()
			rows.Close()
			if err != nil || present != (c.Name == "present") {
				t.Fatal("cohort", d.ID, c.Name, err)
			}
		}
	}
	catalog, err := stmt.Describe(defs)
	if err != nil {
		t.Fatal(err)
	}
	contract, err := schema.ReadContract("INT-INDEX-POLICY.json")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := plan.ReadIndexPolicy(contract)
	if err != nil {
		t.Fatal(err)
	}
	cfg := browseReaderConfig()
	db, _, err := sqliteconn.Open(t.Context(), f.path, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	conn, err := db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(readfixture.ForegroundHash(t)+mpQueueSeed)))
	var captures []plan.Golden
	for _, statistics := range []plan.Statistics{plan.Unanalyzed, plan.Analyzed} {
		if statistics == plan.Analyzed {
			f.exec(t, "ANALYZE")
		}
		golden, err := plan.Capture(t.Context(), conn, catalog, browsePlanFixture(cfg, hash, statistics), policy)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = stmt.New(defs, golden.Receipt()); err != nil {
			t.Fatal(err)
		}
		if len(golden.Manifest) != 1 || len(golden.Plans) != 2 {
			t.Fatal("incomplete capture")
		}
		captures = append(captures, golden)
	}
	actual, err := json.MarshalIndent(captures, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := "testdata/mp-queue-plans.json.gz"
	if *recordMPQueuePlans {
		var buf bytes.Buffer
		z := gzip.NewWriter(&buf)
		if _, err = z.Write(actual); err != nil {
			t.Fatal(err)
		}
		if err = z.Close(); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(path, buf.Bytes(), 0644); err != nil {
			t.Fatal(err)
		}
	}
	expected, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	z, err := gzip.NewReader(bytes.NewReader(expected))
	if err != nil {
		t.Fatal(err)
	}
	expected, err = io.ReadAll(z)
	if err != nil {
		t.Fatal(err)
	}
	if err = z.Close(); err != nil {
		t.Fatal(err)
	}
	if err = plan.CompareJSON(expected, actual); err != nil {
		t.Fatal(err)
	}
}

func TestMPQueueLiveConsumer(t *testing.T) {
	f := composeReadFixture(t, mpQueueSource(t), MediaHarnessDefinitions(), readfixture.LiveConfig(time.Now), false)
	r, err := NewMediaReader(f.snapshots)
	if err != nil {
		t.Fatal(err)
	}
	q, request, output := liveRequests("mp-live")
	consumed := false
	_, err = ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, q, output, func(ctx context.Context, out BrowseOutput) error {
		got, result, err := r.QueueHead(ctx, request, time.Second, out, MediaQueueInput{View: f.input, Queue: "mp-queue"})
		if err != nil {
			return err
		}
		if got.Revision != 3 || result.AdmissionElapsed <= 0 {
			t.Fatal("missing live value", got, result)
		}
		assertLiveUsage(t, f, 1, 0)
		consumed = true
		return nil
	})
	if err != nil || !consumed {
		t.Fatal("consumer", err)
	}
	assertLiveUsage(t, f, 0, 0)
}
