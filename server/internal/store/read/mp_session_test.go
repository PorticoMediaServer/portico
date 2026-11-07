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

const mpSessionSeed = `INSERT INTO playback_v1_sessions(id,device_id,account_id,profile_id,authority,start_key,start_digest,kind,role,state,item_id,request,revision,generation,media_session_id,media_generation,presentation,start_position_ms,position_ms,last_seq,lease_expires_ms,created_ms,updated_ms)
VALUES('mp-session','d','a','p','local','mp-start','digest','vod','local','playing',1,'{}',3,2,'physical-session',4,'{}',500,1500,7,2000000000000,1000,2000);
INSERT INTO profile_restrictions(profile_id,maximum_age,allow_unrated) VALUES('p',12,1)`

func mpSessionSource(t *testing.T) *browseFixture {
	t.Helper()
	f := newForegroundSource(t)
	f.exec(t, mpSessionSeed)
	return f
}
func mpSessionFixture(t *testing.T) (*browseFixture, *ForegroundReader) {
	t.Helper()
	f := composeBrowseFixture(t, mpSessionSource(t), MediaSessionHarnessDefinitions())
	return f, &ForegroundReader{snapshots: f.snapshots, pool: f.snapshots}
}
func TestMPSessionHead(t *testing.T) {
	f, r := mpSessionFixture(t)
	in := MediaSessionInput{View: f.input, Session: "mp-session", Item: 1}
	got, result, err := r.sessionHead(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if err != nil || got.ID != "mp-session" || got.Item.ID != 1 || got.Revision != 3 || got.Generation != 2 || got.MediaGeneration != 4 || got.PositionMS != 1500 || got.LastSequence != 7 || result.LookupStatements != 6 || result.PublicationStatements != 6 {
		t.Fatal(got, result, err)
	}
	for _, id := range []string{"", strings.Repeat("s", 129), "bad\x00id", "\xff"} {
		in.Session = id
		got, _, err = r.sessionHead(t.Context(), foregroundRequest(), time.Second, f.output, in)
		if !errors.Is(err, ErrInput) || got != (MediaSessionHead{}) {
			t.Fatal("input", got, err)
		}
	}
}
func TestMPSessionOwnershipAndVisibility(t *testing.T) {
	for _, mutation := range []string{
		`UPDATE playback_v1_sessions SET device_id='other'`,
		`UPDATE playback_v1_sessions SET account_id='b'`,
		`UPDATE playback_v1_sessions SET profile_id='q'`,
		`UPDATE playback_v1_sessions SET authority='hosted'`,
		`UPDATE playback_v1_sessions SET group_id='group'`,
		`UPDATE playback_v1_sessions SET transfer_id='transfer'`,
		`UPDATE catalog_entities SET retired=1 WHERE id=1`,
		`UPDATE catalog_entities SET admission_own_age=21 WHERE id=1`,
		`UPDATE authorization_session_families SET revoked=1`,
	} {
		t.Run(mutation, func(t *testing.T) {
			f, r := mpSessionFixture(t)
			f.exec(t, mutation)
			got, _, err := r.sessionHead(t.Context(), foregroundRequest(), time.Second, f.output, MediaSessionInput{View: f.input, Session: "mp-session", Item: 1})
			if !errors.Is(err, ErrDenied) || got != (MediaSessionHead{}) {
				t.Fatal("leak", got, err)
			}
		})
	}
}
func TestMPSessionPublication(t *testing.T) {
	for _, tc := range []struct {
		name, mutation string
		denied         bool
	}{
		{"occurrence", `UPDATE playback_v1_sessions SET generation=generation+1`, false},
		{"position", `UPDATE playback_v1_sessions SET position_ms=2500`, false},
		{"title", `UPDATE catalog_entities SET title='new title' WHERE id=1`, false},
		{"revoked", `UPDATE authorization_session_families SET revoked=1`, true},
		{"hidden", `UPDATE catalog_entities SET admission_own_age=21 WHERE id=1`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, r := mpSessionFixture(t)
			r.snapshots = &foregroundBoundary{Snapshots: f.snapshots, mutate: func(phase int) {
				if phase == 1 {
					f.exec(t, tc.mutation)
				}
			}}
			got, result, err := r.sessionHead(t.Context(), foregroundRequest(), time.Second, f.output, MediaSessionInput{View: f.input, Session: "mp-session", Item: 1})
			if tc.denied {
				if !errors.Is(err, ErrDenied) || got != (MediaSessionHead{}) {
					t.Fatal("publication leak", got, err)
				}
				return
			}
			if err != nil || result.Attempts != 2 {
				t.Fatal("retry", got, result, err)
			}
			if tc.name == "occurrence" && got.Generation != 3 || tc.name == "position" && got.PositionMS != 2500 || tc.name == "title" && got.Item.Title != "new title" {
				t.Fatal("stale", got)
			}
		})
	}
}
func TestMPSessionCancellationAndLiveConsumer(t *testing.T) {
	f, r := mpSessionFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	r.snapshots = &browseBoundarySnapshots{Snapshots: f.snapshots, afterBusiness: cancel}
	got, _, err := r.sessionHead(ctx, foregroundRequest(), time.Second, f.output, MediaSessionInput{View: f.input, Session: "mp-session", Item: 1})
	if !errors.Is(err, context.Canceled) || got != (MediaSessionHead{}) {
		t.Fatal("cancel", got, err)
	}
	live := composeReadFixture(t, mpSessionSource(t), MediaSessionHarnessDefinitions(), readfixture.LiveConfig(time.Now), false)
	reader, _ := NewForegroundReader(live.snapshots)
	q, request, output := liveRequests("mp-session-live")
	consumed := false
	_, err = ConsumeForeground(t.Context(), live.governor, live.authority, live.snapshots, q, output, func(ctx context.Context, out BrowseOutput) error {
		got, _, err := reader.SessionHead(ctx, request, time.Second, out, MediaSessionInput{View: live.input, Session: "mp-session", Item: 1})
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

var recordMPSessionPlans = flag.Bool("record-mp-session-plans", false, "record MP queue native plans")

func TestMPSessionPlans(t *testing.T) {
	f := mpSessionSource(t)
	defs := mpSessionDefinitions()
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
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(readfixture.ForegroundHash(t)+mpSessionSeed)))
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
	path := "testdata/mp-session-plans.json.gz"
	if *recordMPSessionPlans {
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
