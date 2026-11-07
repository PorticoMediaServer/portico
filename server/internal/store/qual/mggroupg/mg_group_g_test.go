// Package mggroupg is a draft-only, disposable schema control harness. It is not
// imported by the product and does not adopt or modify the embedded schema.
package mggroupg

import (
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
)

//go:embed testdata/candidate.sql
var candidate string

//go:embed testdata/manifest.json
var manifest []byte

const nonce = "01010101010101010101010101010101"
const launch = "02020202020202020202020202020202"
const staleLaunch = "03030303030303030303030303030303"

type fixture struct{ c *sql.Conn }

func native(t *testing.T, ddl string) *fixture {
	t.Helper()
	cfg := sqliteconn.Config{Role: sqliteconn.Writer, Connections: 1, CacheKiB: 1024, Synchronous: sqliteconn.Normal}
	db, settings, err := sqliteconn.Open(t.Context(), filepath.Join(t.TempDir(), "group-g.sqlite"), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	c, err := db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := c.Close(); err != nil {
			t.Error(err)
		}
	})
	if _, err := sqliteconn.Assert(t.Context(), c, cfg); err != nil {
		t.Fatal(err)
	}
	t.Logf("Go=%s engine=%s FK=%d DQS=%d/%d candidate=%x", runtime.Version(), settings.Engine, settings.ForeignKeys, settings.DQSDDL, settings.DQSDML, sha256.Sum256([]byte(ddl)))
	if settings.Engine != "3.53.4" || settings.ForeignKeys != 1 || settings.DQSDDL != 0 || settings.DQSDML != 0 {
		t.Fatal("unqualified engine/policy", settings)
	}
	if _, err := c.ExecContext(t.Context(), ddl); err != nil {
		t.Fatal("candidate load", err)
	}
	f := &fixture{c: c}
	f.exec(t, "INSERT INTO live_source_identities(id) VALUES('source-a'),('source-b')")
	return f
}
func (f *fixture) exec(t *testing.T, q string, args ...any) {
	t.Helper()
	if _, err := f.c.ExecContext(t.Context(), q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}
func (f *fixture) reject(t *testing.T, q string, match string, args ...any) {
	t.Helper()
	_, err := f.c.ExecContext(t.Context(), q, args...)
	if err == nil || !strings.Contains(err.Error(), match) {
		t.Fatalf("expected %q rejection, got %v: %s", match, err, q)
	}
}

// Test-only, deterministic native fixture construction. Defaults are read from
// the candidate rather than bypassed. This supplies rows, not authority, result
// codecs or a production accepting command. No FK/DQS setting is relaxed.
func (f *fixture) insert(t *testing.T, table string, override map[string]any) error {
	t.Helper()
	rows, err := f.c.QueryContext(t.Context(), "PRAGMA main.table_info("+table+")")
	if err != nil {
		t.Fatal(err)
	}
	values := map[string]any{}
	for rows.Next() {
		var cid, required, pk int
		var name, typ string
		var def any
		if err := rows.Scan(&cid, &name, &typ, &required, &def, &pk); err != nil {
			t.Fatal(err)
		}
		if required == 0 || def != nil {
			continue
		}
		switch typ {
		case "INTEGER":
			values[name] = int64(0)
			if strings.Contains(name, "revision") || name == "command_sequence" || name == "final_sequence" || name == "result_planning_estimate" {
				values[name] = int64(1)
			}
		case "BLOB":
			n := 16
			if strings.Contains(name, "digest") {
				n = 32
			}
			values[name] = make([]byte, n)
		case "TEXT":
			values[name] = "value"
			if name == "member_incarnation" || name == "profile_lifetime" || name == "command_member_incarnation" || name == "command_profile_lifetime" {
				values[name] = strings.Repeat("a", 32)
			}
		default:
			t.Fatal("unexpected fixture type", name, typ)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	for k, v := range override {
		values[k] = v
	}
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	args := make([]any, len(keys))
	marks := make([]string, len(keys))
	for i, k := range keys {
		args[i] = values[k]
		marks[i] = "?"
	}
	_, err = f.c.ExecContext(t.Context(), "INSERT INTO "+table+"("+strings.Join(keys, ",")+") VALUES("+strings.Join(marks, ",")+")", args...)
	return err
}
func (f *fixture) guide(t *testing.T, id, source string) {
	t.Helper()
	if err := f.insert(t, "live_generations", map[string]any{"id": id, "source_id": source, "name": "Guide", "tuner_count": int64(1), "channel_count": int64(1), "programme_count": int64(0), "created_at": "2026-11-07T00:00:00Z"}); err != nil {
		t.Fatal(err)
	}
}
func (f *fixture) channel(t *testing.T, id string) {
	t.Helper()
	if err := f.insert(t, "lc_channels", map[string]any{"id": id, "revision": int64(1), "config_json": "{}", "enabled": int64(1), "position": int64(0), "name": "Channel", "state": "pending"}); err != nil {
		t.Fatal(err)
	}
}
func (f *fixture) generation(t *testing.T, id, channel, base string) {
	t.Helper()
	if err := f.insert(t, "lc_generations", map[string]any{"id": id, "channel_id": channel, "config_revision": int64(1), "config_json": "{}", "catalog_fence": "catalogue-fence", "seed": "seed", "status": "pending", "phase": "build", "base_generation": base, "start_ms": int64(0), "end_ms": int64(1000), "boundary_ms": int64(0), "cursor_ms": int64(0), "created_ms": int64(1), "verify_end_ms": int64(0)}); err != nil {
		t.Fatal(err)
	}
}
func recording(id, guide, source string) map[string]any {
	return map[string]any{"id": id, "owner_key": "owner", "authority": "local", "account_id": "account", "profile_id": "profile", "source_id": source, "channel_id": "channel", "programme_id": "programme", "guide_generation": guide, "guide_pin_generation": guide, "programme_json": "{}", "options_json": "{}", "start_ms": int64(0), "end_ms": int64(1000), "priority": int64(1), "revision": int64(1), "state": "scheduled", "created_ms": int64(1), "updated_ms": int64(1)}
}
func (f *fixture) record(t *testing.T, id, guide, source string) {
	t.Helper()
	if err := f.insert(t, "dvr_recordings", recording(id, guide, source)); err != nil {
		t.Fatal(err)
	}
}

func TestGroupGCandidateAndParentBoundary(t *testing.T) {
	var m struct {
		Base      string `json:"base_schema_sha256"`
		Candidate string `json:"candidate_sha256"`
		Objects   []struct {
			Name      string `json:"name"`
			Operation string `json:"operation"`
			Type      string `json:"type"`
		} `json:"objects"`
	}
	if err := json.Unmarshal(manifest, &m); err != nil {
		t.Fatal(err)
	}
	got := sha256.Sum256([]byte(candidate))
	if m.Base != "14f7b58ffe4b7c2ad1682dbb6ba52c028fca956875b16de8e52479b198d793d2" || hex.EncodeToString(got[:]) != m.Candidate {
		t.Fatal("candidate/source pin mismatch")
	}
	f := native(t, candidate)
	allowed := map[string]bool{"live_source_identities": true, "live_generations": true, "lc_channels": true, "lc_generations": true, "lc_entries": true, "live_operation_command_receipts": true, "live_programme_images": true, "dvr_recordings": true}
	for _, o := range m.Objects {
		if o.Type != "table" {
			continue
		}
		rows, err := f.c.QueryContext(t.Context(), "PRAGMA main.foreign_key_list("+o.Name+")")
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var id, seq int
			var parent, from, to, update, del, match string
			if err := rows.Scan(&id, &seq, &parent, &from, &to, &update, &del, &match); err != nil {
				t.Fatal(err)
			}
			if !allowed[parent] {
				t.Fatal("new/changed outside-G parent", o.Name, parent)
			}
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
	}
	var integrity string
	if err := f.c.QueryRowContext(t.Context(), "PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatal(integrity, err)
	}
	rows, err := f.c.QueryContext(t.Context(), "PRAGMA foreign_key_check")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatal("candidate FK check")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}

func TestGroupGTypedPinsAndScope(t *testing.T) {
	f := native(t, candidate)
	cases := []struct {
		name string
		run  func(*testing.T)
	}{
		{"external_head", func(t *testing.T) {
			f.guide(t, "g-a", "source-a")
			if err := f.insert(t, "live_sources", map[string]any{"id": "source-b", "name": "bad", "revision": int64(1), "state": "active", "tuner_count": int64(1), "active_generation": "g-a", "published_at": "now"}); err == nil {
				t.Fatal("cross-source head accepted")
			}
			if err := f.insert(t, "live_sources", map[string]any{"id": "source-a", "name": "good", "revision": int64(1), "state": "active", "tuner_count": int64(1), "active_generation": "g-a", "published_at": "now"}); err != nil {
				t.Fatal(err)
			}
			f.reject(t, "DELETE FROM live_generations WHERE id='g-a'", "FOREIGN KEY")
		}},
		{"lc_head", func(t *testing.T) {
			f.channel(t, "c-a")
			f.generation(t, "l-a", "c-a", "")
			f.exec(t, "UPDATE lc_channels SET active_generation='l-a' WHERE id='c-a'")
			f.reject(t, "DELETE FROM lc_generations WHERE id='l-a'", "FOREIGN KEY")
			f.exec(t, "UPDATE lc_channels SET active_generation='' WHERE id='c-a'")
			f.exec(t, "DELETE FROM lc_generations WHERE id='l-a'")
		}},
		{"pending_base", func(t *testing.T) {
			f.channel(t, "c-a")
			f.generation(t, "l-a", "c-a", "")
			f.exec(t, "UPDATE lc_generations SET status='published' WHERE id='l-a'")
			f.generation(t, "l-b", "c-a", "l-a")
			f.reject(t, "DELETE FROM lc_generations WHERE id='l-a'", "FOREIGN KEY")
			f.exec(t, "UPDATE lc_generations SET status='cancelled' WHERE id='l-b'")
			f.exec(t, "DELETE FROM lc_generations WHERE id='l-a'")
		}},
		{"lc_actual_ref", func(t *testing.T) {
			f.channel(t, "c-a")
			f.channel(t, "c-b")
			f.generation(t, "l-a", "c-a", "")
			f.exec(t, "INSERT INTO lc_entries VALUES('l-a','occurrence','c-a',0,1000,0,'','','','Slate','','',0,'no-eligible-media')")
			if err := f.insert(t, "lc_playback_refs", map[string]any{"playback_id": "session", "channel_id": "c-b", "generation_id": "l-a", "occurrence_id": "occurrence", "end_ms": int64(1000), "lease_until_ms": int64(0)}); err == nil {
				t.Fatal("cross-channel ref accepted")
			}
			if err := f.insert(t, "lc_playback_refs", map[string]any{"playback_id": "session", "channel_id": "c-a", "generation_id": "l-a", "occurrence_id": "occurrence", "end_ms": int64(1000), "lease_until_ms": int64(0)}); err != nil {
				t.Fatal(err)
			}
			f.reject(t, "DELETE FROM lc_entries WHERE generation_id='l-a'", "FOREIGN KEY")
		}},
		{"reminder", func(t *testing.T) {
			f.guide(t, "g-a", "source-a")
			if err := f.insert(t, "live_reminders", map[string]any{"id": "reminder", "owner_key": "owner", "kind": "live-source", "source_id": "source-a", "channel_id": "channel", "generation_id": "g-a", "programme_id": "programme", "revision": int64(1), "start_utc": "2026-11-07T00:00:00Z"}); err != nil {
				t.Fatal(err)
			}
			f.reject(t, "DELETE FROM live_generations WHERE id='g-a'", "FOREIGN KEY")
		}},
		{"recording", func(t *testing.T) {
			f.guide(t, "g-a", "source-a")
			if err := f.insert(t, "dvr_recordings", recording("bad", "g-a", "source-b")); err == nil {
				t.Fatal("cross-source recording pin accepted")
			}
			f.record(t, "recording", "g-a", "source-a")
			f.reject(t, "DELETE FROM live_generations WHERE id='g-a'", "FOREIGN KEY")
			f.exec(t, "UPDATE dvr_recordings SET guide_pin_generation=NULL WHERE id='recording'")
			f.exec(t, "DELETE FROM live_generations WHERE id='g-a'")
			var provenance string
			if err := f.c.QueryRowContext(t.Context(), "SELECT guide_generation FROM dvr_recordings WHERE id='recording'").Scan(&provenance); err != nil || provenance != "g-a" {
				t.Fatal("historical provenance lost", provenance, err)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f.exec(t, "SAVEPOINT pin_case")
			defer f.exec(t, "ROLLBACK TO pin_case; RELEASE pin_case")
			tc.run(t)
		})
	}
}

func TestGroupGPhysicalExitAndSharedReady(t *testing.T) {
	f := native(t, candidate)
	f.guide(t, "g-a", "source-a")
	f.record(t, "recording", "g-a", "source-a")
	f.exec(t, "UPDATE dvr_recordings SET state='recording',claim_generation=3,cancel_generation=1,claim_token='token',owner_instance='instance',allocation_id='allocation',capture_allocation_generation=7,capture_allocation_token='allocation-token',capture_launch_id=X'"+launch+"',capture_source_revision=2,capture_source_fence='source-fence',last_command_sequence=10,acknowledged_command_sequence=9 WHERE id='recording'")
	f.exec(t, "UPDATE dvr_recordings SET state='cancelled',cancel_generation=2,lease_until_ms=0,last_command_sequence=11,acknowledged_command_sequence=10 WHERE id='recording'")
	f.reject(t, "UPDATE dvr_recordings SET guide_pin_generation=NULL WHERE id='recording'", "dvr_capture_pin_debt")
	f.reject(t, "UPDATE dvr_recordings SET last_command_sequence=13,acknowledged_command_sequence=12,capture_writer_retired_sequence=12,capture_physical_exit_sequence=13,capture_exit_launch_id=X'"+staleLaunch+"',capture_exit_claim_generation=2,capture_exit_cancel_generation=2 WHERE id='recording'", "dvr_capture_exit")
	f.reject(t, "UPDATE dvr_recordings SET last_command_sequence=13,acknowledged_command_sequence=12,capture_shared_ready_sequence=13,artifact_id='artifact',artifact_digest='digest',bytes=100,captured_start_ms=0,captured_end_ms=1000 WHERE id='recording'", "dvr_capture_shared_ready")
	f.exec(t, "UPDATE dvr_recordings SET last_command_sequence=13,acknowledged_command_sequence=12,capture_writer_retired_sequence=12,capture_physical_exit_sequence=13,capture_exit_launch_id=X'"+launch+"',capture_exit_claim_generation=3,capture_exit_cancel_generation=2 WHERE id='recording'")
	f.exec(t, "UPDATE dvr_recordings SET last_command_sequence=14,acknowledged_command_sequence=13,capture_shared_ready_sequence=14,artifact_id='artifact',artifact_digest='digest',bytes=100,captured_start_ms=0,captured_end_ms=1000 WHERE id='recording'")
	f.exec(t, "UPDATE dvr_recordings SET guide_pin_generation=NULL WHERE id='recording'")
	f.exec(t, "DELETE FROM live_generations WHERE id='g-a'")
}

func publishReceipt() map[string]any {
	return map[string]any{"request_id": "operation", "command_kind": "publish", "source_id": "source-a", "generation_id": "old-guide", "command_sequence": int64(2), "accepted_acknowledged_sequence": int64(1), "result_reclaim_family": "none", "result_source_revision": int64(1), "result_source_state": "active", "result_source_name": "Guide", "result_tuner_count_mode": "configured", "result_acquisition": "upload", "result_planning_estimate": int64(1), "result_published_at": "2026-11-07T00:00:00Z", "result_channel_count": int64(1), "result_sealed": int64(1)}
}
func TestGroupGClosedReceiptAndRetirement(t *testing.T) {
	f := native(t, candidate)
	row := publishReceipt()
	if err := f.insert(t, "live_operation_command_receipts", row); err != nil {
		t.Fatal(err)
	}
	if err := f.insert(t, "live_operation_command_receipts", row); err == nil {
		t.Fatal("duplicate sequence accepted")
	}
	row["command_sequence"] = int64(3)
	if err := f.insert(t, "live_operation_command_receipts", row); err == nil {
		t.Fatal("operation ID reused under another sequence")
	}
	row["request_id"] = "other"
	row["command_kind"] = "generic-arbitrary-command"
	if err := f.insert(t, "live_operation_command_receipts", row); err == nil {
		t.Fatal("unclosed receipt kind accepted")
	}
	if err := f.insert(t, "live_operation_retirements", map[string]any{"request_id": "operation", "source_id": "source-a", "final_sequence": int64(2), "last_command_sequence": int64(2), "acknowledged_command_sequence": int64(2)}); err != nil {
		t.Fatal(err)
	}
	f.exec(t, "DELETE FROM live_operation_command_receipts WHERE request_id='operation' AND command_sequence<=2")
	var floor int64
	if err := f.c.QueryRowContext(t.Context(), "SELECT final_sequence FROM live_operation_retirements WHERE request_id='operation'").Scan(&floor); err != nil || floor != 2 {
		t.Fatal("replay floor lost with final receipt", floor, err)
	}
}

func stripConstraint(t *testing.T, name string) string {
	t.Helper()
	lines := strings.Split(candidate, "\n")
	count := 0
	var out []string
	for _, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), "CONSTRAINT "+name+" ") {
			count++
			continue
		}
		out = append(out, l)
	}
	if count != 1 {
		t.Fatal("mutant target changed", name, count)
	}
	return strings.Join(out, "\n")
}
func TestGroupGTwoCandidateMutants(t *testing.T) {
	t.Run("M1-unsealed-publish-receipt", func(t *testing.T) {
		for _, mutant := range []bool{false, true} {
			ddl := candidate
			if mutant {
				ddl = stripConstraint(t, "live_receipt_publish")
			}
			f := native(t, ddl)
			row := publishReceipt()
			row["result_sealed"] = int64(0)
			err := f.insert(t, "live_operation_command_receipts", row)
			if !mutant && (err == nil || !strings.Contains(err.Error(), "live_receipt_publish")) {
				t.Fatal("pristine source did not reject target", err)
			}
			if mutant && err != nil {
				t.Fatal("M1 not isolated", err)
			}
			t.Logf("M1 source_mutated=%v target_rejected=%v sha256=%x", mutant, err != nil, sha256.Sum256([]byte(ddl)))
		}
	})
	t.Run("M2-stale-physical-exit", func(t *testing.T) {
		for _, mutant := range []bool{false, true} {
			ddl := candidate
			if mutant {
				ddl = stripConstraint(t, "dvr_capture_exit")
			}
			f := native(t, ddl)
			f.guide(t, "g-a", "source-a")
			f.record(t, "recording", "g-a", "source-a")
			f.exec(t, "UPDATE dvr_recordings SET claim_generation=3,cancel_generation=2,capture_launch_id=X'"+launch+"',last_command_sequence=10,acknowledged_command_sequence=9 WHERE id='recording'")
			_, err := f.c.ExecContext(t.Context(), "UPDATE dvr_recordings SET capture_writer_retired_sequence=9,capture_physical_exit_sequence=10,capture_exit_launch_id=X'"+staleLaunch+"',capture_exit_claim_generation=2,capture_exit_cancel_generation=1 WHERE id='recording'")
			if !mutant && (err == nil || !strings.Contains(err.Error(), "dvr_capture_exit")) {
				t.Fatal("pristine source did not reject target", err)
			}
			if mutant && err != nil {
				t.Fatal("M2 not isolated", err)
			}
			t.Logf("M2 source_mutated=%v target_rejected=%v sha256=%x", mutant, err != nil, sha256.Sum256([]byte(ddl)))
		}
	})
}

func TestGroupGNewReversePlans(t *testing.T) {
	f := native(t, candidate)
	for _, tc := range []struct {
		q, want string
		args    []any
	}{
		{"EXPLAIN QUERY PLAN SELECT id FROM live_allocations INDEXED BY live_allocations_source_state_channel WHERE source_id=? AND state=? AND channel_id>? ORDER BY channel_id,id LIMIT 8", "SEARCH live_allocations USING COVERING INDEX live_allocations_source_state_channel", []any{"source-a", "active", ""}},
		{"EXPLAIN QUERY PLAN SELECT id FROM dvr_recordings INDEXED BY dvr_recordings_share_occurrence WHERE source_id=? AND channel_id=? AND programme_id=? AND start_ms>? ORDER BY start_ms,id LIMIT 8", "SEARCH dvr_recordings USING COVERING INDEX dvr_recordings_share_occurrence", []any{"source-a", "channel", "programme", int64(0)}},
		{"EXPLAIN QUERY PLAN SELECT playback_id FROM lc_playback_refs INDEXED BY lc_playback_refs_generation WHERE generation_id=? AND occurrence_id=? LIMIT 1", "SEARCH lc_playback_refs USING COVERING INDEX lc_playback_refs_generation", []any{"generation", "occurrence"}},
	} {
		rows, err := f.c.QueryContext(t.Context(), tc.q, tc.args...)
		if err != nil {
			t.Fatal(err)
		}
		var details []string
		for rows.Next() {
			var a, b, c int
			var d string
			if err := rows.Scan(&a, &b, &c, &d); err != nil {
				t.Fatal(err)
			}
			details = append(details, d)
		}
		rows.Close()
		if len(details) != 1 || !strings.HasPrefix(details[0], tc.want) || strings.Contains(details[0], "SCAN") || strings.Contains(details[0], "TEMP B-TREE") {
			t.Fatal("wrong selected reverse path", tc.q, details)
		}
		t.Log(details)
	}
}

func TestGroupGGuideAndLCCompleteness(t *testing.T) {
	f := native(t, candidate)
	for _, g := range []string{"g-old", "g-a", "g-b"} {
		f.guide(t, g, "source-a")
	}
	if err := f.insert(t, "live_sources", map[string]any{"id": "source-a", "name": "Guide", "revision": int64(7), "state": "active", "tuner_count": int64(1), "active_generation": "g-old", "published_at": "before"}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a", "b"} {
		if err := f.insert(t, "live_operations", map[string]any{"request_id": id, "input_hash": "input-" + id, "source_id": "source-a", "generation_id": "g-" + id, "expected_revision": int64(7), "expected_generation": "g-old", "status": "staging", "expected_choice_count": int64(1)}); err != nil {
			t.Fatal(err)
		}
		f.reject(t, "UPDATE live_operations SET sealed=1 WHERE request_id=?", "live_operation_seal", id)
		f.exec(t, "INSERT INTO live_channel_versions(generation_id,channel_id,provider_key,name,number,group_name,position,locator_sealed,available_start,available_end) VALUES(?,?,'provider','TV','1','News',0,X'01','','')", "g-"+id, "channel-"+id)
		f.exec(t, "INSERT INTO live_channel_choices VALUES(?,'key','TV','News')", "g-"+id)
		f.exec(t, "UPDATE live_operations SET staged_channel_count=1,staged_choice_count=1,staged_bytes=10,sealed=1 WHERE request_id=?", id)
		result, err := f.c.ExecContext(t.Context(), "UPDATE live_operations SET staged_channel_count=staged_channel_count+1 WHERE request_id=? AND status='staging' AND sealed=0", id)
		if err != nil {
			t.Fatal(err)
		}
		n, err := result.RowsAffected()
		if err != nil || n != 0 {
			t.Fatal("late stage changed sealed owner", n, err)
		}
	}
	for i, id := range []string{"a", "b"} {
		result, err := f.c.ExecContext(t.Context(), "UPDATE live_sources SET active_generation=?,published_at='now' WHERE id='source-a' AND revision=7 AND active_generation='g-old'", "g-"+id)
		if err != nil {
			t.Fatal(err)
		}
		n, err := result.RowsAffected()
		want := int64(1)
		if i == 1 {
			want = 0
		}
		if err != nil || n != want {
			t.Fatal("same-config refresh lost prior-head CAS", n, err)
		}
	}
	f.channel(t, "lc")
	f.generation(t, "schedule", "lc", "")
	f.exec(t, "INSERT INTO lc_entries VALUES('schedule','first','lc',0,500,0,'','','','Slate','','',0,'no-eligible-media'),('schedule','last','lc',500,1000,0,'','','','Slate','','',0,'no-eligible-media')")
	digest := sha256.Sum256([]byte("first:0:500;last:500:1000;"))
	f.exec(t, "UPDATE lc_generations SET entry_count=2,cursor_ms=1000,entry_digest=? WHERE id='schedule'", digest[:])
	f.reject(t, "UPDATE lc_generations SET schedule_sealed=1 WHERE id='schedule'", "lc_schedule_seal")
	// Verify the actual native entry prefix under the retained unique start key.
	// This tiny control has one bounded quantum; accepting code must persist and
	// resume that prefix across quanta, never COUNT/LAG the whole schedule.
	verify := func() (int64, int64, [32]byte) {
		rows, err := f.c.QueryContext(t.Context(), "SELECT occurrence_id,start_ms,end_ms FROM lc_entries INDEXED BY sqlite_autoindex_lc_entries_2 WHERE generation_id='schedule' AND start_ms>-1 ORDER BY start_ms LIMIT 8")
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		count, end := int64(0), int64(0)
		var text strings.Builder
		for rows.Next() {
			var id string
			var start, next int64
			if err := rows.Scan(&id, &start, &next); err != nil {
				t.Fatal(err)
			}
			if start != end {
				return -1, end, [32]byte{}
			}
			text.WriteString(id + ":" + strconv.FormatInt(start, 10) + ":" + strconv.FormatInt(next, 10) + ";")
			end = next
			count++
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return count, end, sha256.Sum256([]byte(text.String()))
	}
	count, end, got := verify()
	if count != 2 || end != 1000 || got != digest {
		t.Fatal("positive complete prefix")
	}
	f.exec(t, "SAVEPOINT missing_entry")
	f.exec(t, "DELETE FROM lc_entries WHERE generation_id='schedule' AND occurrence_id='last'")
	count, end, got = verify()
	if count == 2 && end == 1000 && got == digest {
		t.Fatal("missing native slot accepted by count/time alone")
	}
	f.exec(t, "UPDATE lc_generations SET verified_entry_count=?,verify_after_ms=0,verify_end_ms=?,verified_entry_digest=? WHERE id='schedule'", count, end, got[:])
	f.reject(t, "UPDATE lc_generations SET schedule_sealed=1 WHERE id='schedule'", "lc_schedule_seal")
	f.exec(t, "ROLLBACK TO missing_entry; RELEASE missing_entry")
	f.exec(t, "UPDATE lc_generations SET verified_entry_count=2,verify_after_ms=500,verify_end_ms=1000,verified_entry_digest=?,schedule_sealed=1 WHERE id='schedule'", digest[:])
}
