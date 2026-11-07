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

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/readfixture"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

const mpPreparedSeed = `UPDATE direct_memberships SET role='owner' WHERE account_id='a';
INSERT INTO prepared_media_jobs(id,item_id,asset_id,library_id,profile_id,target_id,selection_json,source_revision,principal_json,state,phase,generation,bytes,revision,created_ms,updated_ms)
VALUES('mp-job',1,'asset','movies','portable-v3','prepared-library','{}','source-rev','{}','running','encoding',2,4096,3,1000,2000)`

func mpPreparedSource(t *testing.T) *browseFixture {
	t.Helper()
	f := newForegroundSource(t)
	f.exec(t, mpPreparedSeed)
	return f
}
func mpPreparedFixture(t *testing.T) (*browseFixture, *ForegroundReader) {
	t.Helper()
	f := composeBrowseFixture(t, mpPreparedSource(t), PreparedJobHarnessDefinitions())
	return f, &ForegroundReader{snapshots: f.snapshots, pool: f.snapshots}
}
func TestMPPreparedStatus(t *testing.T) {
	f, r := mpPreparedFixture(t)
	in := PreparedJobInput{View: f.input, Job: "mp-job", Item: 1}
	got, result, err := r.preparedJob(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if err != nil || got.ID != "mp-job" || got.Item.ID != 1 || got.State != "running" || got.Phase != "encoding" || got.Generation != 2 || got.Bytes != 4096 || got.Revision != 3 || got.SourceRevision != "source-rev" || got.Profile != "portable-v3" || result.LookupStatements != 7 || result.PublicationStatements != 7 {
		t.Fatal(got, result, err)
	}
	for _, id := range []string{"", strings.Repeat("s", 129), "bad\x00id", "\xff"} {
		in.Job = id
		got, _, err = r.preparedJob(t.Context(), foregroundRequest(), time.Second, f.output, in)
		if !errors.Is(err, ErrInput) || got != (PreparedJobStatus{}) {
			t.Fatal("input", got, err)
		}
	}
}
func TestMPPreparedAuthority(t *testing.T) {
	for _, mutation := range []string{
		`UPDATE direct_memberships SET role='member' WHERE account_id='a'`,
		`UPDATE prepared_media_jobs SET library_id='other'`,
		`UPDATE prepared_media_jobs SET item_id=2`,
		`UPDATE catalog_entities SET retired=1 WHERE id=1`,
		`UPDATE authorization_session_families SET revoked=1`,
	} {
		t.Run(mutation, func(t *testing.T) {
			f, r := mpPreparedFixture(t)
			f.exec(t, mutation)
			got, _, err := r.preparedJob(t.Context(), foregroundRequest(), time.Second, f.output, PreparedJobInput{View: f.input, Job: "mp-job", Item: 1})
			if !errors.Is(err, ErrDenied) || got != (PreparedJobStatus{}) {
				t.Fatal("leak", got, err)
			}
		})
	}
}
func TestMPPreparedPublication(t *testing.T) {
	for _, tc := range []struct {
		name, mutation string
		denied         bool
	}{
		{"progress", `UPDATE prepared_media_jobs SET bytes=8192`, false},
		{"generation", `UPDATE prepared_media_jobs SET generation=3`, false},
		{"cancel", `UPDATE prepared_media_jobs SET state='cancelling',phase='cancellation requested'`, false},
		{"lease", `UPDATE prepared_media_jobs SET job_lease_generation=1`, false},
		{"owner retired", `UPDATE direct_memberships SET role='member' WHERE account_id='a'`, true},
		{"revoked", `UPDATE authorization_session_families SET revoked=1`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, r := mpPreparedFixture(t)
			r.snapshots = &foregroundBoundary{Snapshots: f.snapshots, mutate: func(phase int) {
				if phase == 1 {
					f.exec(t, tc.mutation)
				}
			}}
			got, result, err := r.preparedJob(t.Context(), foregroundRequest(), time.Second, f.output, PreparedJobInput{View: f.input, Job: "mp-job", Item: 1})
			if tc.denied {
				if !errors.Is(err, ErrDenied) || got != (PreparedJobStatus{}) {
					t.Fatal("publication leak", got, err)
				}
				return
			}
			if err != nil || result.Attempts != 2 {
				t.Fatal("retry", got, result, err)
			}
			if tc.name == "progress" && got.Bytes != 8192 || tc.name == "generation" && got.Generation != 3 || tc.name == "cancel" && got.State != "cancelling" {
				t.Fatal("stale", got)
			}
		})
	}
}
func TestMPPreparedCancellationAndLiveConsumer(t *testing.T) {
	f, r := mpPreparedFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	r.snapshots = &browseBoundarySnapshots{Snapshots: f.snapshots, afterBusiness: cancel}
	got, _, err := r.preparedJob(ctx, foregroundRequest(), time.Second, f.output, PreparedJobInput{View: f.input, Job: "mp-job", Item: 1})
	if !errors.Is(err, context.Canceled) || got != (PreparedJobStatus{}) {
		t.Fatal("cancel", got, err)
	}
	live := composeReadFixture(t, mpPreparedSource(t), PreparedJobHarnessDefinitions(), readfixture.LiveConfig(time.Now), false)
	reader, _ := NewForegroundReader(live.snapshots)
	q, request, output := liveRequests("mp-job-live")
	consumed := false
	_, err = ConsumeForeground(t.Context(), live.governor, live.authority, live.snapshots, q, output, func(ctx context.Context, out BrowseOutput) error {
		got, _, err := reader.PreparedJob(ctx, request, time.Second, out, PreparedJobInput{View: live.input, Job: "mp-job", Item: 1})
		if err != nil {
			return err
		}
		if got.Revision != 3 {
			t.Fatal(got)
		}
		assertLiveUsage(t, live, 1, 0)
		consumed = true
		return nil
	})
	if err != nil || !consumed {
		t.Fatal("live", err)
	}
	assertLiveUsage(t, live, 0, 0)
}

var recordMPPreparedPlans = flag.Bool("record-mp-prepared-plans", false, "record MP queue native plans")

func TestMPPreparedPlans(t *testing.T) {
	f := mpPreparedSource(t)
	defs := mpPreparedDefinitions()
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
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(readfixture.ForegroundHash(t)+mpPreparedSeed)))
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
	path := "testdata/mp-prepared-plans.json.gz"
	if *recordMPPreparedPlans {
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
