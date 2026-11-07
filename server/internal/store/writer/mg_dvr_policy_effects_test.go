package writer

import (
	"context"
	"database/sql/driver"
	"errors"
	"math"
	"strings"
	"testing"
)

func TestMGDVRPolicyNormalizationAndTemplateBounds(t *testing.T) {
	got, err := freezeMGDVRDefaults(mgDVRDefaultsInput{prePaddingMS: math.MaxInt64, postPaddingMS: -1, keepMode: "keep-count", keepCount: 1001})
	if err != nil || got != (mgDVRDefaultsDesired{1800, 0, 0, 1000, "keep-watching"}) {
		t.Fatal(got, err)
	}
	got, err = freezeMGDVRDefaults(mgDVRDefaultsInput{prePaddingMS: 1999, postPaddingMS: 7200001, keepMode: "keep-days", keepDays: 4000, conflict: "stop-watching"})
	if err != nil || got != (mgDVRDefaultsDesired{1, 7200, 3650, 0, "stop-watching"}) {
		t.Fatal(got, err)
	}
	for _, in := range []mgDVRDefaultsInput{{keepMode: "unknown"}, {keepMode: "keep-count", keepCount: 0}, {keepMode: "keep-days", keepDays: -1}, {keepMode: "keep-all", conflict: "evict"}, {keepMode: "keep-all", showTemplate: "{title}"}, {keepMode: "keep-all", movieTemplate: "{title}"}} {
		if _, err := freezeMGDVRDefaults(in); !errors.Is(err, ErrInvalid) {
			t.Fatal(in, err)
		}
	}
	for _, s := range []string{"", "{series}/Season {season:02}/{title}", "{title} ({year})", "{channelNumber:02}/{date} - {time}", "literal"} {
		if !mgDVRRecordingTemplate(s) {
			t.Fatal("valid retained template", s)
		}
	}
	for _, s := range []string{"/{title}", "{title}/", "../{title}", "x/./{title}", "x//{title}", "{unknown}", "{title:02}", "{episode:2}", "{title}/a:b", "{title}/a\\b", "{title}\n", "{title}\x00", "{title}/}", string([]byte{255}), strings.Repeat("x", 401)} {
		if mgDVRRecordingTemplate(s) {
			t.Fatal("unsafe retained template", s)
		}
	}
	storage := mgDVRStorageDesired{3650, 100000, mgDVRPolicyMaxBytes, 1}
	if got, err := freezeMGDVRStorage(storage); err != nil || got != storage {
		t.Fatal(got, err)
	}
	for _, v := range []mgDVRStorageDesired{{-1, 0, 0, 0}, {3651, 0, 0, 0}, {0, 100001, 0, 0}, {0, 0, -1, 0}, {0, 0, 0, mgDVRPolicyMaxBytes + 1}} {
		if _, err := freezeMGDVRStorage(v); !errors.Is(err, ErrInvalid) {
			t.Fatal(v, err)
		}
	}
	ds := mgDVRPolicyDescriptors()
	if len(ds) != 2 || ds[0].ID != 845 || ds[1].ID != 846 || ds[0].Durability != Normal || ds[1].Durability != Normal {
		t.Fatal(ds)
	}
	if _, err := buildCommandDescriptors(commandRegistration{owner: "MG", descriptors: ds}); err != nil {
		t.Fatal(err)
	}
}
func TestMGDVRPolicyNativeCASAndReadback(t *testing.T) {
	w, before, storage := mgDVRPolicyFixture(t)
	turn := mgDVRPolicyTurn(t, w)
	observed, err := readMGDVRDefaults(t.Context(), turn)
	if err != nil || observed != before {
		t.Fatal(observed, err)
	}
	observedStorage, err := readMGDVRStorage(t.Context(), turn)
	if err != nil || observedStorage != storage {
		t.Fatal(observedStorage, err)
	}
	desired, err := freezeMGDVRDefaults(mgDVRDefaultsInput{prePaddingMS: 90001, postPaddingMS: 300999, keepMode: "keep-days", keepDays: 30, conflict: "stop-watching"})
	if err != nil {
		t.Fatal(err)
	}
	next, err := setMGDVRDefaults(t.Context(), turn, before, desired)
	if err != nil || next.revision != 8 || next.beforeSeconds != 90 || next.afterSeconds != 300 || next.retentionDays != 30 || next.episodeLimit != 0 || next.conflict != "stop-watching" || next.showTemplate != before.showTemplate || next.movieTemplate != before.movieTemplate {
		t.Fatal(next, err)
	}
	// Numeric policy publication is not a fake readiness certificate: no actual
	// retirement/cleanup operation is admitted or performed by this component.
	policy := mgDVRStorageDesired{30, 10, 1024, 8192}
	nextStorage, err := setMGDVRStorage(t.Context(), turn, storage, policy)
	if err != nil || nextStorage != (mgDVRStorageSnapshot{12, policy}) {
		t.Fatal(nextStorage, err)
	}
	mgDVRPolicyCommit(t, turn)
	var revision, claim, lease int64
	var options, state, token string
	if err := w.db.QueryRowContext(t.Context(), `SELECT revision,options_json,state,claim_generation,claim_token,lease_until_ms FROM dvr_recordings WHERE id='policy-marker'`).Scan(&revision, &options, &state, &claim, &token, &lease); err != nil || revision != 42 || state != "recording" || claim != 2 || token != "held-token" || lease != 1 || options != `{"beforeMs":1000,"afterMs":2000,"retentionDays":0,"episodeLimit":0,"priority":0}` {
		t.Fatal("recording rewritten", revision, options, state, claim, token, lease, err)
	}
	turn = mgDVRPolicyTurn(t, w)
	if got, err := setMGDVRStorage(t.Context(), turn, nextStorage, mgDVRStorageDesired{}); err != nil || got.revision != 13 || got.policy != (mgDVRStorageDesired{}) {
		t.Fatal(got, err)
	}
	mgDVRPolicyCommit(t, turn)
}
func TestMGDVRPolicyStaleMissingAndInsideValidation(t *testing.T) {
	w, before, storage := mgDVRPolicyFixture(t)
	// Same revision but different actual scalar/template fields is also stale.
	if _, err := w.db.ExecContext(t.Context(), `UPDATE dvr_defaults SET movie_folder_template='{date}/{title}' WHERE singleton=1`); err != nil {
		t.Fatal(err)
	}
	if _, err := w.db.ExecContext(t.Context(), `UPDATE dvr_storage_policy SET floor_bytes=1 WHERE singleton=1`); err != nil {
		t.Fatal(err)
	}
	turn := mgDVRPolicyTurn(t, w)
	desired := mgDVRDefaultsDesired{60, 180, 0, 0, "keep-watching"}
	if got, err := setMGDVRDefaults(t.Context(), turn, before, desired); !errors.Is(err, ErrConflict) || got != (mgDVRDefaultsSnapshot{}) {
		t.Fatal(got, err)
	}
	if got, err := setMGDVRStorage(t.Context(), turn, storage, mgDVRStorageDesired{}); !errors.Is(err, ErrConflict) || got != (mgDVRStorageSnapshot{}) {
		t.Fatal(got, err)
	}
	// A raw value cannot bypass the freezer's public scalar bounds.
	invalid := desired
	invalid.beforeSeconds = 1801
	if _, err := setMGDVRDefaults(t.Context(), turn, before, invalid); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := setMGDVRStorage(t.Context(), turn, storage, mgDVRStorageDesired{episodeLimit: 100001}); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	maximum := before
	maximum.revision = math.MaxInt64
	if _, err := setMGDVRDefaults(t.Context(), turn, maximum, desired); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	if _, err := w.db.ExecContext(t.Context(), `DELETE FROM dvr_defaults;DELETE FROM dvr_storage_policy`); err != nil {
		t.Fatal(err)
	}
	turn = mgDVRPolicyTurn(t, w)
	if _, err := readMGDVRDefaults(t.Context(), turn); !errors.Is(err, ErrConflict) {
		t.Fatal("fabricated absent defaults", err)
	}
	if _, err := readMGDVRStorage(t.Context(), turn); !errors.Is(err, ErrConflict) {
		t.Fatal("fabricated absent storage", err)
	}
}
func TestMGDVRPolicyCanceledResultsAreEmpty(t *testing.T) {
	w, before, storage := mgDVRPolicyFixture(t)
	turn := mgDVRPolicyTurn(t, w)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if got, err := setMGDVRDefaults(ctx, turn, before, mgDVRDefaultsDesired{60, 180, 0, 0, "keep-watching"}); !errors.Is(err, context.Canceled) || got != (mgDVRDefaultsSnapshot{}) {
		t.Fatal(got, err)
	}
	if got, err := setMGDVRStorage(ctx, turn, storage, mgDVRStorageDesired{}); !errors.Is(err, context.Canceled) || got != (mgDVRStorageSnapshot{}) {
		t.Fatal(got, err)
	}
}

var mgDVRPolicyInjectedReadback = errors.New("injected DVR policy readback failure")

type mgDVRPolicyFaultConnection struct {
	writerDriverConn
	table   string
	updated bool
}

func (c *mgDVRPolicyFaultConnection) QueryContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	if c.updated && strings.HasPrefix(q, "SELECT ") && strings.Contains(q, " FROM "+c.table+" WHERE singleton=") {
		return nil, mgDVRPolicyInjectedReadback
	}
	rows, err := c.writerDriverConn.QueryContext(ctx, q, args)
	if err == nil && strings.HasPrefix(q, "UPDATE "+c.table+" SET ") {
		c.updated = true
	}
	return rows, err
}
func TestMGDVRPolicyReadbackFailureRequiresWholeRollback(t *testing.T) {
	for _, table := range []string{"dvr_defaults", "dvr_storage_policy"} {
		t.Run(table, func(t *testing.T) {
			w, before, storage := mgDVRPolicyFixture(t, func(c driver.Conn) driver.Conn {
				return &mgDVRPolicyFaultConnection{writerDriverConn: c.(writerDriverConn), table: table}
			})
			turn := mgDVRPolicyTurn(t, w)
			if table == "dvr_defaults" {
				if got, err := setMGDVRDefaults(t.Context(), turn, before, mgDVRDefaultsDesired{120, 300, 7, 0, "stop-watching"}); !errors.Is(err, mgDVRPolicyInjectedReadback) || got != (mgDVRDefaultsSnapshot{}) {
					t.Fatal(got, err)
				}
			} else {
				if got, err := setMGDVRStorage(t.Context(), turn, storage, mgDVRStorageDesired{7, 10, 1024, 8192}); !errors.Is(err, mgDVRPolicyInjectedReadback) || got != (mgDVRStorageSnapshot{}) {
					t.Fatal(got, err)
				}
			}
			if _, err := turn.transaction.rollback(nil); err != nil {
				t.Fatal(err)
			}
			// Different read projections avoid the injected readback and inspect actual
			// post-rollback native facts without changing connection/writer ownership.
			if table == "dvr_defaults" {
				var revision, beforeSeconds int64
				if err := w.db.QueryRowContext(t.Context(), `SELECT revision,before_seconds FROM dvr_defaults WHERE singleton IN(1)`).Scan(&revision, &beforeSeconds); err != nil || revision != before.revision || beforeSeconds != before.beforeSeconds {
					t.Fatal("defaults survived rollback", revision, beforeSeconds, err)
				}
			} else {
				var revision, floor int64
				if err := w.db.QueryRowContext(t.Context(), `SELECT revision,floor_bytes FROM dvr_storage_policy WHERE singleton IN(1)`).Scan(&revision, &floor); err != nil || revision != storage.revision || floor != storage.policy.floorBytes {
					t.Fatal("storage survived rollback", revision, floor, err)
				}
			}
		})
	}
}
