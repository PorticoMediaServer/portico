package writer

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"encoding/hex"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

type mpRepeatFixture struct {
	w         *Writer
	e         *executor
	cap       *mpWorkerDisclosure
	command   *mpRepeatCommand
	authority *governor.Authority
}

// mpWorkerRequireCanonicalF proves that the fixture ran on the adopted full F
// schema: change018 parents, their reverse indexes and the queue control columns.
// The previous selected-subset overlay is removed, so absence here is fatal.
func mpWorkerRequireCanonicalF(t *testing.T, e *executor) {
	t.Helper()
	for _, table := range []string{"mp_queue_command_receipts", "mp_next_stop_obligations"} {
		var n int
		if err := e.db.QueryRowContext(t.Context(), `SELECT count(*) FROM sqlite_schema WHERE type='table' AND name=?`, table).Scan(&n); err != nil || n != 1 {
			t.Fatal("canonical F table missing", table, n, err)
		}
	}
	for _, column := range []struct{ table, name string }{
		{"playback_sessions", "mp_command_incarnation"},
		{"playback_v1_prepared", "mp_media_generation"},
		{"queues_v1", "mp_receipt_reclaim_due_ms"},
	} {
		var n int
		if err := e.db.QueryRowContext(t.Context(), `SELECT count(*) FROM pragma_table_xinfo('`+column.table+`') WHERE name=?`, column.name).Scan(&n); err != nil || n != 1 {
			t.Fatal("canonical F column missing", column.table, column.name, n, err)
		}
	}
	for _, index := range []string{"mp_queue_command_owner", "mp_next_stop_media_fk", "mp_queue_receipt_due"} {
		var n int
		if err := e.db.QueryRowContext(t.Context(), `SELECT count(*) FROM sqlite_schema WHERE type='index' AND name=?`, index).Scan(&n); err != nil || n != 1 {
			t.Fatal("canonical F index missing", index, n, err)
		}
	}
	var foreignKeys int
	if err := e.db.QueryRowContext(t.Context(), `PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil || foreignKeys != 1 {
		t.Fatal("canonical F fixture requires enforced foreign keys", foreignKeys, err)
	}
}

func mpWorkerFixture(t *testing.T, next bool) mpRepeatFixture {
	t.Helper()
	w, pd, a := wcProgressCompositionFixture(t)
	e := w.effective.executor
	// The adopted change018 F parents are the only fixture DDL. A missing parent
	// is a canonical-bootstrap failure, never a reason to recreate schema here.
	mpWorkerRequireCanonicalF(t, e)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := e.db.ExecContext(t.Context(), q, args...); err != nil {
			t.Fatal(q, err)
		}
	}
	s := pd.scope
	if err := pd.Close(t.Context()); err != nil {
		t.Fatal(err)
	} // WC only seeds auth; MP owns no WC preparation grant.
	exec(`INSERT INTO identity_devices(id,account_id,installation_id,name,platform,app,app_version,first_seen,last_seen) VALUES('mp-device',?,'mp-install','Fixture','test','test','1','2026-11-08T00:00:00Z','2026-11-08T00:00:00Z')`, s.accountID)
	exec(`UPDATE authorization_session_families SET device_id='mp-device' WHERE id=?`, s.familyID)
	inc := [16]byte{71}
	exec(`INSERT INTO queues_v1(id,owner_kind,owner_id,authority,account_id,profile_id,revision,custody_generation,created_ms,updated_ms,mp_command_incarnation,mp_owner_member_incarnation,mp_owner_profile_lifetime) VALUES('mp-queue','device','mp-device','local',?,?,3,7,100,100,?,?,?)`, s.accountID, s.profileID, inc[:], s.memberBirth, s.profileBirth)
	if next {
		// Reuse actual canonical library/subject facts, with one exact physical
		// occurrence. Synthetic source bytes are never opened or given exit proof.
		exec(`INSERT INTO catalog_assets(id,token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration,library_id) VALUES(8001,'mp-asset','mp.mkv',100,1,'matroska','h264','aac',1920,1080,1,?)`, s.libraryID)
		exec(`INSERT INTO playback_sessions(id,session_hash,account_id,profile_id,item_id,asset_id,generation,state,grant_hash,grant_token,expires_at,request_id,duration,mp_execution_id) VALUES('mp-media','hash',?,?,?,'mp-asset',2,'prepared','mp-grant','opaque','2099-01-01T00:00:00Z','mp-start',1,'mp-execution')`, s.accountID, s.profileID, s.subjectID)
		exec(`INSERT INTO playback_v1_prepared(token,queue_id,device_id,create_key,digest,session_id,session_generation,session_state,queue_revision,next_position,entry_id,item_id,media_session_id,request,presentation,expires_ms,state,created_ms,mp_media_generation) VALUES('mp-next','mp-queue','mp-device','mp-next-create','digest','logical',1,'playing',3,1,'entry',?,'mp-media','{}','{}',9999999999999,'prepared',100,2)`, s.subjectID)
	}
	c := &mpWorkerDisclosure{writer: w, lifetime: t.Context(), scope: s, queue: "mp-queue", device: "mp-device", incarnation: inc}
	c.self = c
	q := mpQueueRepeatRequest{"mp-queue", s.accountID, s.profileID, "mp-device", 3, 7, "all", "mp-stop"}
	command, err := newMPRepeatCommand(CommandIdentity{inc, 1, [16]byte{72}}, q)
	if err != nil {
		t.Fatal(err)
	}
	// Complete Capture of the canonical adopted schema and the existing union.
	all := []stmt.Definition{}
	for _, entry := range w.effective.registry.Manifest() {
		all = append(all, entry.Definition)
	}
	for _, d := range mpWorkerDefinitions() {
		found := false
		for _, old := range all {
			if old.ID == d.ID {
				found = true
				break
			}
		}
		if !found {
			all = append(all, d)
		}
	}
	conn, err := e.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	capturePolicy := e.config
	capturePolicy.Synchronous = sqliteconn.Normal
	golden := mpWorkerCaptureFixture(t, conn, capturePolicy, all, "mp_worker_dispatch_test.go", "mp_worker_dispatch.go", "mp_worker_receipt.go", "mp_worker_receipt_statements.go", "mp_worker_result.go", "mp_queue_repeat.go")
	if err = conn.Close(); err != nil {
		t.Fatal(err)
	}
	p := effectiveFixturePlan()
	p.registration = golden.Receipt()
	p.steps = nil
	p.mpWorkerMemory = commandMemoryBudget{64 << 20, 64 << 20}
	p.mpWorkerLimits = mpWorkerReceiptLimits{1024, 1 << 20}
	for _, d := range mpWorkerDefinitions() {
		p.steps = append(p.steps, statementStep{d.ID, Work{32, 8000, 32, 65536, 100 * time.Microsecond}})
	}
	if err = composeMPRepeat(t.Context(), w.effective, p); err != nil {
		t.Fatal(err)
	}
	return mpRepeatFixture{w, e, c, command, a}
}
func (f mpRepeatFixture) run(ctx context.Context, c *mpRepeatCommand) (mpWorkerResult, error) {
	return f.w.runMPRepeat(ctx, f.cap, f.command.input.Queue, f.command.reference, c)
}
func mpWorkerAssertReturned(t *testing.T, f mpRepeatFixture) {
	t.Helper()
	if f.authority.Usage() != ([4]governor.Amounts{}) || f.e.db.Stats().InUse != 0 {
		t.Fatal("physical writer custody retained", f.authority.Usage(), f.e.db.Stats())
	}
}

func TestMPWorkerRepeatOriginalReplayAndPhysicalStop(t *testing.T) {
	f := mpWorkerFixture(t, true)
	out, err := f.run(t.Context(), f.command)
	if err != nil {
		t.Fatal(err)
	}
	if out.Outcome.State != Applied || out.Outcome.Replayed || !out.Value.Changed || !out.Value.PhysicalPending || out.Value.CanceledToken != "mp-next" || out.Value.StopRequest != "mp-stop" || out.Value.Generation != 2 || out.Value.Revision != 4 {
		t.Fatal(out)
	}
	if out.Outcome.Evidence == nil || out.Outcome.Evidence.Purpose != MPQueueRepeat || out.Outcome.Evidence.Commit != CommitSucceeded {
		t.Fatal("missing sole writer terminal", out)
	}
	var retired, exited, grantRetired, physicalRevision, obligations, events, rows, bytes, actual int64
	var physicalState string
	if err = f.e.db.QueryRowContext(t.Context(), `SELECT mp_retired_generation,mp_exited_generation,state,mp_grant_retired,mp_revision FROM playback_sessions WHERE id='mp-media'`).Scan(&retired, &exited, &physicalState, &grantRetired, &physicalRevision); err != nil || retired != 2 || exited != 0 || physicalState != "stopped" || grantRetired != 1 || physicalRevision != 2 {
		t.Fatal("incomplete physical retirement or invented exit", retired, exited, physicalState, grantRetired, physicalRevision, err)
	}
	if err = f.e.db.QueryRowContext(t.Context(), `SELECT count(*) FROM mp_next_stop_obligations`).Scan(&obligations); err != nil || obligations != 1 {
		t.Fatal(obligations, err)
	}
	if err = f.e.db.QueryRowContext(t.Context(), `SELECT count(*) FROM api_events`).Scan(&events); err != nil || events != 2 {
		t.Fatal(events, err)
	}
	if err = f.e.db.QueryRowContext(t.Context(), `SELECT mp_receipt_rows,mp_receipt_variable_bytes FROM queues_v1 WHERE id='mp-queue'`).Scan(&rows, &bytes); err != nil {
		t.Fatal(err)
	}
	if err = f.e.db.QueryRowContext(t.Context(), `SELECT variable_bytes FROM mp_queue_command_receipts`).Scan(&actual); err != nil || rows != 1 || bytes != actual {
		t.Fatal("actual receipt debt", rows, bytes, actual, err)
	}
	// Independent sum checks the actual generated byte expression, not itself.
	payload, _, _ := mpRepeatPayload(f.command.reference.Identity, f.command.reference.RequestDigest, out.Value)
	if actual != int64(len("mp-queue")+16+16+32+32+len(payload)) {
		t.Fatal("wrong byte grain", actual)
	}
	if _, err = f.e.db.ExecContext(t.Context(), `UPDATE queues_v1 SET revision=99,repeat='one'`); err != nil {
		t.Fatal(err)
	}
	replay, err := f.run(t.Context(), nil)
	if err != nil || replay.Outcome.State != Applied || !replay.Outcome.Replayed || replay.Value != out.Value || replay.Digest != out.Digest {
		t.Fatal("reconstructed current state", replay, err)
	}
	var after int64
	_ = f.e.db.QueryRowContext(t.Context(), `SELECT count(*) FROM api_events`).Scan(&after)
	if after != events {
		t.Fatal("replay event", after)
	}

	if _, err = f.e.db.ExecContext(t.Context(), `UPDATE mp_queue_command_receipts SET result_digest=zeroblob(32)`); err != nil {
		t.Fatal(err)
	}
	if invalid, err := f.run(t.Context(), nil); !errors.Is(err, ErrResolveRequired) || !reflect.DeepEqual(invalid, mpWorkerResult{}) {
		t.Fatal("saved digest mismatch disclosed result", invalid, err)
	}
	mpWorkerAssertReturned(t, f)
}

func TestMPWorkerRepeatNoopConflictAndCurrentDenial(t *testing.T) {
	for _, name := range []string{"noop", "stale-cas", "revoked", "device", "copied", "digest", "two-prepared", "other-account", "null-generation"} {
		t.Run(name, func(t *testing.T) {
			f := mpWorkerFixture(t, name == "noop" || name == "two-prepared" || name == "other-account" || name == "null-generation")
			c := f.command
			switch name {
			case "two-prepared":
				if _, err := f.e.db.ExecContext(t.Context(), `INSERT INTO playback_v1_prepared(token,queue_id,device_id,create_key,digest,session_id,session_generation,session_state,queue_revision,next_position,entry_id,item_id,media_session_id,request,presentation,expires_ms,state,created_ms,mp_media_generation) SELECT 'second-next',queue_id,device_id,'second-create',digest,session_id,session_generation,session_state,queue_revision,next_position,entry_id,item_id,media_session_id,request,presentation,expires_ms,state,created_ms,mp_media_generation FROM playback_v1_prepared WHERE token='mp-next'`); err != nil {
					t.Fatal(err)
				}
			case "other-account":
				if _, err := f.e.db.ExecContext(t.Context(), `UPDATE playback_sessions SET account_id='other-account' WHERE id='mp-media'`); err != nil {
					t.Fatal(err)
				}
			case "null-generation":
				if _, err := f.e.db.ExecContext(t.Context(), `UPDATE playback_v1_prepared SET mp_media_generation=NULL WHERE token='mp-next'`); err != nil {
					t.Fatal(err)
				}
			case "noop":
				q := c.input
				q.Repeat = "off"
				c, _ = newMPRepeatCommand(c.reference.Identity, q)
			case "stale-cas":
				_, _ = f.e.db.ExecContext(t.Context(), `UPDATE queues_v1 SET revision=4`)
			case "revoked":
				_, _ = f.e.db.ExecContext(t.Context(), `UPDATE authorization_session_families SET revoked=1`)
			case "device":
				_, _ = f.e.db.ExecContext(t.Context(), `UPDATE authorization_session_families SET device_id=NULL`)
			case "copied":
				f.cap = &mpWorkerDisclosure{self: f.cap, writer: f.cap.writer, lifetime: f.cap.lifetime, scope: f.cap.scope, queue: f.cap.queue, device: f.cap.device, incarnation: f.cap.incarnation}
			case "digest":
				copy := *c
				copy.reference.RequestDigest[0] ^= 1
				c = &copy
			}
			out, err := f.w.runMPRepeat(t.Context(), f.cap, c.input.Queue, c.reference, c)
			if name == "noop" {
				if err != nil || out.Outcome.State != Applied || out.Value.Changed || out.Value.Revision != 3 {
					t.Fatal(out, err)
				}
				var n int
				_ = f.e.db.QueryRowContext(t.Context(), `SELECT count(*) FROM api_events`).Scan(&n)
				if n != 0 {
					t.Fatal("noop event")
				}
				var state string
				var retired, obligations int64
				if err := f.e.db.QueryRowContext(t.Context(), `SELECT state FROM playback_v1_prepared WHERE token='mp-next'`).Scan(&state); err != nil {
					t.Fatal(err)
				}
				_ = f.e.db.QueryRowContext(t.Context(), `SELECT mp_retired_generation FROM playback_sessions WHERE id='mp-media'`).Scan(&retired)
				_ = f.e.db.QueryRowContext(t.Context(), `SELECT count(*) FROM mp_next_stop_obligations`).Scan(&obligations)
				if state != "prepared" || retired != 0 || obligations != 0 {
					t.Fatal("noop canceled pending next", state, retired, obligations)
				}
			} else if err == nil || !reflect.DeepEqual(out, mpWorkerResult{}) {
				t.Fatal("failure exposed result", out, err)
			}
			if name == "null-generation" && !errors.Is(err, ErrResolveRequired) {
				t.Fatal("NULL generation must be a typed refusal", err)
			}
			mpWorkerAssertReturned(t, f)
		})
	}
}

func TestMPWorkerRepeatSavedDenialAndInputConflict(t *testing.T) {
	f := mpWorkerFixture(t, false)
	if _, err := f.run(t.Context(), f.command); err != nil {
		t.Fatal(err)
	}
	q := f.command.input
	q.Repeat = "one"
	different, _ := newMPRepeatCommand(f.command.reference.Identity, q)
	if out, err := f.w.runMPRepeat(t.Context(), f.cap, q.Queue, different.reference, different); !errors.Is(err, ErrConflict) || !reflect.DeepEqual(out, mpWorkerResult{}) {
		t.Fatal(out, err)
	}
	if _, err := f.e.db.ExecContext(t.Context(), `UPDATE direct_memberships SET disabled=1`); err != nil {
		t.Fatal(err)
	}
	if out, err := f.run(t.Context(), nil); !errors.Is(err, ErrDisclosureDenied) || !reflect.DeepEqual(out, mpWorkerResult{}) {
		t.Fatal("revoked replay disclosed", out, err)
	}
	mpWorkerAssertReturned(t, f)
}

func TestMPWorkerRepeatClosedPlansAndResultBounds(t *testing.T) {
	f := mpWorkerFixture(t, false)
	r := f.w.effective
	old := r.mpRepeatPlan
	r.mpRepeatPlan = nil
	if out, err := f.run(t.Context(), f.command); err == nil || !reflect.DeepEqual(out, mpWorkerResult{}) {
		t.Fatal("uninstalled purpose ran", out, err)
	}
	bad := *old
	bad.registration = nil
	if err := composeMPRepeat(t.Context(), r, bad); err == nil || r.mpRepeatPlan != nil {
		t.Fatal("unqualified installed", err)
	}
	r.mpRepeatPlan = old
	q := f.command.input
	q.Queue = strings.Repeat("x", 129)
	if _, err := newMPRepeatCommand(f.command.reference.Identity, q); err == nil {
		t.Fatal("unbounded queued input")
	}
	v := mpQueueRepeatValue{Queue: "q", Repeat: "all", Revision: 1, AppliedMS: 1}
	payload, d, err := mpRepeatPayload(f.command.reference.Identity, f.command.reference.RequestDigest, v)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range [][]byte{append(append([]byte{}, payload...), 0), payload[:len(payload)-1], make([]byte, mpRepeatPayloadCap+1)} {
		if _, err := decodeMPRepeatPayload(f.command.reference.Identity, f.command.reference.RequestDigest, p, d); err == nil {
			t.Fatal("malformed original payload")
		}
	}
	if sha256.Sum256(payload) == [32]byte{} {
		t.Fatal("empty codec")
	}
	mpWorkerAssertReturned(t, f)
}

// Faults use the existing native driver boundary, never a fake accepting turn.
func mpWorkerFaultPool(t *testing.T, f mpRepeatFixture, fault *faultConnector) {
	t.Helper()
	if err := f.e.db.Close(); err != nil {
		t.Fatal(err)
	}
	connector, err := sqliteconn.Connector(f.w.effective.path, f.e.config, sqliteconn.ExistingFile)
	if err != nil {
		t.Fatal(err)
	}
	fault.Connector = connector
	installFaultPool(f.e, fault)
	primeEffectiveFixturePool(t, f.e)
}

func TestMPWorkerRepeatUnknownCommitNeverApplied(t *testing.T) {
	for _, after := range []bool{false, true} {
		t.Run(map[bool]string{false: "before", true: "after"}[after], func(t *testing.T) {
			f := mpWorkerFixture(t, true)
			mpWorkerFaultPool(t, f, &faultConnector{replyError: true, afterCommit: after})
			out, err := f.run(t.Context(), f.command)
			if !errors.Is(err, ErrResolveRequired) || out.Outcome.State == Applied || !reflect.DeepEqual(out, mpWorkerResult{}) {
				t.Fatal("uncertain commit exposed success", out, err)
			}

			if !f.e.ambiguous || !f.e.fenced || !f.w.effective.fenced {
				t.Fatal("uncertain writer was not fenced")
			}
			// Quarantine deliberately closes the writer pool. Observe durable state
			// through a new read-only native connection, never reopen the writer.
			reader, _, err := sqliteconn.Open(t.Context(), f.w.effective.path, sqliteconn.Config{Role: sqliteconn.Reader, Connections: 1, CacheKiB: 1024, Synchronous: sqliteconn.Full}, sqliteconn.ExistingFile)
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			var revision, receipts, obligations int64
			if err = reader.QueryRowContext(t.Context(), `SELECT revision FROM queues_v1 WHERE id='mp-queue'`).Scan(&revision); err != nil {
				t.Fatal(err)
			}
			if err = reader.QueryRowContext(t.Context(), `SELECT count(*) FROM mp_queue_command_receipts`).Scan(&receipts); err != nil {
				t.Fatal(err)
			}
			if err = reader.QueryRowContext(t.Context(), `SELECT count(*) FROM mp_next_stop_obligations`).Scan(&obligations); err != nil {
				t.Fatal(err)
			}

			want := int64(0)
			if after {
				want = 1
			}
			if revision != 3+want || receipts != want || obligations != want {
				t.Fatal("partial unknown commit", revision, receipts, obligations)
			}
			mpWorkerAssertReturned(t, f)
		})
	}
}

type mpMutationFaultConn struct {
	writerDriverConn
	target string
}

var errMPMutation = errors.New("injected MP statement failure")

func (c *mpMutationFaultConn) QueryContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	if q == c.target {
		return nil, errMPMutation
	}
	return c.writerDriverConn.QueryContext(ctx, q, args)
}
func TestMPWorkerRepeatEveryMutationRollsBack(t *testing.T) {
	for _, id := range []stmt.ID{mpRepeatStop, mpRepeatObligation, mpRepeatCancel, mpRepeatUpdate, wcAPIEventInsert, mpWorkerInsert, mpWorkerDebt} {
		t.Run(string(id), func(t *testing.T) {
			f := mpWorkerFixture(t, true)
			var target string
			for _, d := range mpWorkerDefinitions() {
				if d.ID == id {
					target = d.SQL
				}
			}
			if target == "" {
				t.Fatal(id)
			}
			if err := f.e.db.Close(); err != nil {
				t.Fatal(err)
			}
			connector, err := sqliteconn.Connector(f.w.effective.path, f.e.config, sqliteconn.ExistingFile)
			if err != nil {
				t.Fatal(err)
			}
			f.e.openPool(connector, func(c driver.Conn) driver.Conn { return &mpMutationFaultConn{c.(writerDriverConn), target} })
			primeEffectiveFixturePool(t, f.e)
			out, err := f.run(t.Context(), f.command)
			if !errors.Is(err, errMPMutation) || !reflect.DeepEqual(out, mpWorkerResult{}) {
				t.Fatal(out, err)
			}
			var revision, retired, receipts, events int64
			var state string
			_ = f.e.db.QueryRowContext(t.Context(), `SELECT revision FROM queues_v1 WHERE id='mp-queue'`).Scan(&revision)
			_ = f.e.db.QueryRowContext(t.Context(), `SELECT mp_retired_generation FROM playback_sessions WHERE id='mp-media'`).Scan(&retired)
			_ = f.e.db.QueryRowContext(t.Context(), `SELECT count(*) FROM mp_queue_command_receipts`).Scan(&receipts)
			_ = f.e.db.QueryRowContext(t.Context(), `SELECT count(*) FROM api_events`).Scan(&events)
			_ = f.e.db.QueryRowContext(t.Context(), `SELECT state FROM playback_v1_prepared WHERE token='mp-next'`).Scan(&state)
			if revision != 3 || retired != 0 || receipts != 0 || events != 0 || state != "prepared" {
				t.Fatal("partial failed command", revision, retired, receipts, events, state)
			}
			mpWorkerAssertReturned(t, f)
		})
	}
}

// mpPrefixFaultConn fails one exact occurrence of one statement, so the turn is
// observed after its earlier writes already ran inside the same transaction.
type mpPrefixFaultConn struct {
	writerDriverConn
	target     string
	occurrence int
	seen       int
}

func (c *mpPrefixFaultConn) QueryContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	if q == c.target {
		c.seen++
		if c.seen == c.occurrence {
			return nil, errMPMutation
		}
	}
	return c.writerDriverConn.QueryContext(ctx, q, args)
}

// mpWorkerReopenFaultPool swaps the writer pool for one decorated driver without
// changing the schema or connection policy.
func mpWorkerReopenFaultPool(t *testing.T, f mpRepeatFixture, decorate func(driver.Conn) driver.Conn) {
	t.Helper()
	if err := f.e.db.Close(); err != nil {
		t.Fatal(err)
	}
	connector, err := sqliteconn.Connector(f.w.effective.path, f.e.config, sqliteconn.ExistingFile)
	if err != nil {
		t.Fatal(err)
	}
	f.e.openPool(connector, decorate)
	primeEffectiveFixturePool(t, f.e)
}

// mpWorkerParentFacts is the whole parent/child footprint of one command754 turn:
// the queue owner sequence/ACK/debt row and queue edit, the media stop request,
// the prepared next, and every receipt/obligation/event row count. A failed prefix
// must leave all of it exactly as it was.
type mpWorkerParentFacts struct {
	revision, updatedMS, custody                                      int64
	repeat                                                            string
	last, ack, ownerRevision, receiptRows, receiptBytes, controlBytes int64
	sessionState, sessionRequest                                      string
	grantRetired, retired, exited, physicalRevision, cleanup          int64
	preparedState, preparedCancel                                     string
	receipts, obligations, events                                     int64
}

func mpWorkerReadParentFacts(t *testing.T, f mpRepeatFixture) mpWorkerParentFacts {
	t.Helper()
	var o mpWorkerParentFacts
	if err := f.e.db.QueryRowContext(t.Context(), `SELECT revision,updated_ms,repeat,mp_command_last_sequence,mp_command_acknowledged_sequence,mp_owner_revision,mp_receipt_rows,mp_receipt_variable_bytes,mp_control_variable_bytes,custody_generation FROM queues_v1 WHERE id='mp-queue'`).Scan(&o.revision, &o.updatedMS, &o.repeat, &o.last, &o.ack, &o.ownerRevision, &o.receiptRows, &o.receiptBytes, &o.controlBytes, &o.custody); err != nil {
		t.Fatal(err)
	}
	if err := f.e.db.QueryRowContext(t.Context(), `SELECT state,mp_retire_request_id,mp_grant_retired,mp_retired_generation,mp_exited_generation,mp_revision,mp_cleanup_complete FROM playback_sessions WHERE id='mp-media'`).Scan(&o.sessionState, &o.sessionRequest, &o.grantRetired, &o.retired, &o.exited, &o.physicalRevision, &o.cleanup); err != nil {
		t.Fatal(err)
	}
	if err := f.e.db.QueryRowContext(t.Context(), `SELECT state,ifnull(cancel_reason,'') FROM playback_v1_prepared WHERE token='mp-next'`).Scan(&o.preparedState, &o.preparedCancel); err != nil {
		t.Fatal(err)
	}
	for _, q := range []struct {
		query string
		dest  *int64
	}{
		{`SELECT count(*) FROM mp_queue_command_receipts`, &o.receipts},
		{`SELECT count(*) FROM mp_next_stop_obligations`, &o.obligations},
		{`SELECT count(*) FROM api_events`, &o.events},
	} {
		if err := f.e.db.QueryRowContext(t.Context(), q.query).Scan(q.dest); err != nil {
			t.Fatal(err)
		}
	}
	return o
}

// Every command754 write runs after its predecessors. Failing each position in
// order must roll back the whole parent turn: sequence, debt, queue edit, next
// cancellation, stop request, obligation and both events. A per-statement failure
// that left earlier prefixes behind would be a partial accepted state.
func TestMPWorkerRepeatCommandPrefixRollback(t *testing.T) {
	positions := []struct {
		name       string
		id         stmt.ID
		occurrence int
	}{
		{"stop", mpRepeatStop, 1},
		{"obligation", mpRepeatObligation, 1},
		{"cancel", mpRepeatCancel, 1},
		{"update", mpRepeatUpdate, 1},
		{"event-first", wcAPIEventInsert, 1},
		{"event-second", wcAPIEventInsert, 2},
		{"receipt", mpWorkerInsert, 1},
		{"debt", mpWorkerDebt, 1},
	}
	for _, p := range positions {
		t.Run(p.name, func(t *testing.T) {
			f := mpWorkerFixture(t, true)
			before := mpWorkerReadParentFacts(t, f)
			var target string
			for _, d := range mpWorkerDefinitions() {
				if d.ID == p.id {
					target = d.SQL
				}
			}
			if target == "" {
				t.Fatal(p.id)
			}
			mpWorkerReopenFaultPool(t, f, func(c driver.Conn) driver.Conn {
				return &mpPrefixFaultConn{writerDriverConn: c.(writerDriverConn), target: target, occurrence: p.occurrence}
			})
			out, err := f.run(t.Context(), f.command)
			if !errors.Is(err, errMPMutation) || !reflect.DeepEqual(out, mpWorkerResult{}) {
				t.Fatal(out, err)
			}
			if after := mpWorkerReadParentFacts(t, f); after != before {
				t.Fatalf("prefix left partial effects: before %+v after %+v", before, after)
			}
			mpWorkerAssertReturned(t, f)
		})
	}
}

// The adopted owner row allows exactly one unacknowledged receipt. Sequence 2 is
// refused before any effect until sequence 1 is acknowledged; ACK retires serving
// of the original but never refunds its rows/bytes, and the next sequence then
// becomes fresh. This is protocol witness only: command 780 is still MP-29's gate.
func TestMPWorkerReceiptSequenceAckWindow(t *testing.T) {
	f := mpWorkerFixture(t, false)
	if _, err := f.run(t.Context(), f.command); err != nil {
		t.Fatal(err)
	}
	var last, ack, rows, bytes int64
	if err := f.e.db.QueryRowContext(t.Context(), `SELECT mp_command_last_sequence,mp_command_acknowledged_sequence,mp_receipt_rows,mp_receipt_variable_bytes FROM queues_v1 WHERE id='mp-queue'`).Scan(&last, &ack, &rows, &bytes); err != nil {
		t.Fatal(err)
	}
	if last != 1 || ack != 0 || rows != 1 || bytes <= 0 {
		t.Fatal("first receipt did not establish one unacknowledged slot", last, ack, rows, bytes)
	}

	q := f.command.input
	q.ExpectedRevision = 4
	q.Repeat = "one"
	id := f.command.reference.Identity
	id.Sequence = 2
	id.OperationID = [16]byte{90}
	second, err := newMPRepeatCommand(id, q)
	if err != nil {
		t.Fatal(err)
	}
	out, err := f.w.runMPRepeat(t.Context(), f.cap, q.Queue, second.reference, second)
	if !errors.Is(err, ErrResolveRequired) || !reflect.DeepEqual(out, mpWorkerResult{}) {
		t.Fatal("second sequence bypassed the acknowledgement window", out, err)
	}
	var afterRefusalLast, afterRefusalAck, afterRefusalRows, afterRefusalBytes, receipts int64
	if err = f.e.db.QueryRowContext(t.Context(), `SELECT mp_command_last_sequence,mp_command_acknowledged_sequence,mp_receipt_rows,mp_receipt_variable_bytes FROM queues_v1 WHERE id='mp-queue'`).Scan(&afterRefusalLast, &afterRefusalAck, &afterRefusalRows, &afterRefusalBytes); err != nil {
		t.Fatal(err)
	}
	if err = f.e.db.QueryRowContext(t.Context(), `SELECT count(*) FROM mp_queue_command_receipts`).Scan(&receipts); err != nil {
		t.Fatal(err)
	}
	if afterRefusalLast != last || afterRefusalAck != ack || afterRefusalRows != rows || afterRefusalBytes != bytes || receipts != 1 {
		t.Fatal("refused sequence left partial debt", afterRefusalLast, afterRefusalAck, afterRefusalRows, afterRefusalBytes, receipts)
	}

	// The parent ACK effect of command780 (MP-29, unimplemented) advances only the
	// floor. Storage stays charged until an actual reclaim turn deletes rows.
	result, err := f.e.db.ExecContext(t.Context(), `UPDATE queues_v1 SET mp_command_acknowledged_sequence=1 WHERE id='mp-queue' AND mp_command_last_sequence=1 AND mp_command_acknowledged_sequence=0`)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		t.Fatal("ACK floor did not advance", n, err)
	}
	var ackRows, ackBytes int64
	if err = f.e.db.QueryRowContext(t.Context(), `SELECT mp_receipt_rows,mp_receipt_variable_bytes FROM queues_v1 WHERE id='mp-queue'`).Scan(&ackRows, &ackBytes); err != nil || ackRows != rows || ackBytes != bytes {
		t.Fatal("ACK refunded storage", ackRows, ackBytes, err)
	}

	retired, err := f.run(t.Context(), nil)
	if err != nil || retired.Outcome.State != ResultRetired || retired.Value != (mpQueueRepeatValue{}) || retired.Digest != (ResultDigest{}) {
		t.Fatal("acknowledged original was served as Applied", retired, err)
	}

	applied, err := f.w.runMPRepeat(t.Context(), f.cap, q.Queue, second.reference, second)
	if err != nil || applied.Outcome.State != Applied || !applied.Value.Changed || applied.Value.Revision != 5 {
		t.Fatal("post-ACK sequence did not apply", applied, err)
	}
	var grownRows, grownBytes, grownLast, grownAck, sum int64
	if err = f.e.db.QueryRowContext(t.Context(), `SELECT mp_command_last_sequence,mp_command_acknowledged_sequence,mp_receipt_rows,mp_receipt_variable_bytes FROM queues_v1 WHERE id='mp-queue'`).Scan(&grownLast, &grownAck, &grownRows, &grownBytes); err != nil {
		t.Fatal(err)
	}
	if err = f.e.db.QueryRowContext(t.Context(), `SELECT sum(variable_bytes) FROM mp_queue_command_receipts`).Scan(&sum); err != nil {
		t.Fatal(err)
	}
	if grownLast != 2 || grownAck != 1 || grownRows != 2 || grownBytes != sum || grownBytes <= bytes {
		t.Fatal("debt did not track both receipts", grownLast, grownAck, grownRows, grownBytes, sum)
	}
	replay, err := f.w.runMPRepeat(t.Context(), f.cap, q.Queue, second.reference, nil)
	if err != nil || !replay.Outcome.Replayed || replay.Value != applied.Value || replay.Digest != applied.Digest {
		t.Fatal("saved original after ACK", replay, err)
	}
	mpWorkerAssertReturned(t, f)
}

// mpOwnerCASFaultConn mutates the owner between its read and the debt CAS, exactly
// as a racing ACK/owner write would. The debt statement must lose the CAS and the
// whole turn must roll back rather than absorb the newer owner facts.
type mpOwnerCASFaultConn struct {
	writerDriverConn
	target string
	fired  bool
	err    error
}

func (c *mpOwnerCASFaultConn) QueryContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	if q == c.target && !c.fired {
		c.fired = true
		if _, err := c.writerDriverConn.ExecContext(ctx, `UPDATE queues_v1 SET mp_owner_revision=mp_owner_revision+1 WHERE id='mp-queue'`, nil); err != nil {
			c.err = err
			return nil, err
		}
	}
	return c.writerDriverConn.QueryContext(ctx, q, args)
}

func TestMPWorkerReceiptOwnerCASRollsBack(t *testing.T) {
	f := mpWorkerFixture(t, true)
	before := mpWorkerReadParentFacts(t, f)
	var target string
	for _, d := range mpWorkerDefinitions() {
		if d.ID == mpWorkerDebt {
			target = d.SQL
		}
	}
	if target == "" {
		t.Fatal(mpWorkerDebt)
	}
	fault := &mpOwnerCASFaultConn{target: target}
	mpWorkerReopenFaultPool(t, f, func(c driver.Conn) driver.Conn {
		fault.writerDriverConn = c.(writerDriverConn)
		return fault
	})
	out, err := f.run(t.Context(), f.command)
	if !fault.fired || fault.err != nil {
		t.Fatal("owner CAS witness did not run", fault.fired, fault.err)
	}
	if err == nil || !reflect.DeepEqual(out, mpWorkerResult{}) {
		t.Fatal("stale owner facts were absorbed", out, err)
	}
	if after := mpWorkerReadParentFacts(t, f); after != before {
		t.Fatalf("CAS failure left partial effects: before %+v after %+v", before, after)
	}
	mpWorkerAssertReturned(t, f)
}

func TestMPWorkerRepeatCancelledAfterCommitKeepsOriginal(t *testing.T) {
	f := mpWorkerFixture(t, false)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	mpWorkerFaultPool(t, f, &faultConnector{afterNativeCommit: cancel})
	if out, err := f.run(ctx, f.command); !errors.Is(err, context.Canceled) || !reflect.DeepEqual(out, mpWorkerResult{}) {
		t.Fatal("late result was disclosed", out, err)
	}
	// Stop the one-shot hook, then resolve using the same complete identity.
	// A lost reply does not become another change/event/receipt.
	// The hook's cancel is idempotent and affects only the ended request context.
	replay, err := f.run(t.Context(), nil)
	if err != nil || !replay.Outcome.Replayed || replay.Value.Revision != 4 {
		t.Fatal("lost original result", replay, err)
	}
	mpWorkerAssertReturned(t, f)
}

func TestMPWorkerClosedDefinitionSyntax(t *testing.T) {
	for _, d := range mpWorkerDefinitions() {
		if strings.Contains(d.SQL, "octet_length(") {
			continue
		} // actual Capture proves exact registered points
		if issues := plan.LintSQL(d.SQL); len(issues) > 0 {
			t.Fatal(d.ID, issues)
		}
	}
	for _, id := range []CommandID{753, 755, 780} {
		if commandPlanPurpose(id) {
			t.Fatal("unrelated purpose opened", id)
		}
	}
}

// mpWorkerFamilyGate names one still-unimplemented MP worker family, the command
// metadata it owns and the prerequisite that holds it. MP-04 supplies exactly one
// accepting command, 754 (mp_queue_repeat); a descriptor or a reusable composed
// frame is never acceptance for the rest. MP-24 therefore reuses this 754 frame
// and leaves 753/755/760 closed. MP-05's trusted physical observation boundary is
// a separate prerequisite consumed by the stop/exit families, not a command, and
// is not reimplemented here. The seven MP receipt roots (start, physical, session,
// job, version, queue, next) share one sequence/ACK/debt/fixed-result protocol;
// only the queue root is exercised by this frame, and MP-29 owns ACK/reclaim for
// all seven and stays gated.
type mpWorkerFamilyGate struct {
	family   string
	commands []CommandID
	gate     string
}

var mpWorkerFamilyGates = []mpWorkerFamilyGate{
	{"MP-08", []CommandID{703, 704, 705}, "preparation accept/retire/timeout needs MP-07 and the MP-05 physical seam"},
	{"MP-09", []CommandID{710, 711}, "logical/physical session birth and ready need MP-04, MP-07 and UO birth"},
	{"MP-20", []CommandID{740, 741, 742, 743, 747, 748}, "prepared job birth/claim/renew/cancel/deadline/retry needs MP-04, MP-05, MP-10 and UO birth"},
	{"MP-21", []CommandID{744, 746}, "prepared version publication and encode cleanup need MP-20"},
	{"MP-22", []CommandID{745, 749}, "version retirement and separate cleanup execution need MP-21 and the MP-05 physical seam"},
	{"MP-23", []CommandID{750, 751, 752, 759}, "queue birth/snapshot/stage/seal needs MP-04 accepting composition and UO birth"},
	{"MP-24", []CommandID{753, 755, 760}, "queue edit/shuffle/advance reuse the landed 754 frame and need MP-23 custody"},
	{"MP-25", []CommandID{761, 762, 763, 764}, "next prepare/commit/cancel/physical-exit need MP-09, MP-23, MP-24 and the MP-05 seam"},
	{"MP-29", []CommandID{780, 781, 782, 783, 784, 785, 786, 787}, "ACK and bounded worker receipt reclaim across the seven roots need the preceding families merged"},
}

func TestMPWorkerUnimplementedFamiliesStayClosed(t *testing.T) {
	if !commandPlanPurpose(MPQueueRepeat) {
		t.Fatal("the one implemented MP worker command is closed")
	}
	claimed := map[CommandID]string{}
	for _, gate := range mpWorkerFamilyGates {
		if gate.family == "" || gate.gate == "" || len(gate.commands) == 0 {
			t.Fatal("incomplete family gate", gate)
		}
		for _, id := range gate.commands {
			if id == MPQueueRepeat {
				t.Fatal("implemented command listed as unimplemented", gate.family)
			}
			if other, ok := claimed[id]; ok {
				t.Fatal("command claimed by two families", id, other, gate.family)
			}
			claimed[id] = gate.family
			// Metadata is not permission: the purpose exists and stays closed.
			if _, err := Describe(id); err != nil || commandPlanPurpose(id) {
				t.Fatal("unimplemented family opened or unregistered", gate.family, id, err)
			}
		}
	}
}

// The adopted complete index policy already carries mp_queue_receipt_due, so the
// Capture reads it unchanged. No production schema guard or plan rule is changed,
// and this Capture never claims embedded layout parity or performance.
func mpWorkerCaptureFixture(t *testing.T, c *sql.Conn, cfg sqliteconn.Config, defs []stmt.Definition, sources ...string) plan.Golden {
	t.Helper()
	data, err := schema.ReadContract("INT-INDEX-POLICY.json")
	if err != nil {
		t.Fatal(err)
	}
	indexPolicy, err := plan.ReadIndexPolicy(data)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.New()
	for _, name := range sources {
		b, e := os.ReadFile(name)
		if e != nil {
			t.Fatal(e)
		}
		hash.Write([]byte(name))
		hash.Write(b)
	}
	catalogue, err := stmt.Describe(defs)
	if err != nil {
		t.Fatal(err)
	}
	golden, err := cachedCapture(t.Context(), c, catalogue, plan.Fixture{ConnectionPolicy: cfg, SHA256: hex.EncodeToString(hash.Sum(nil)), Statistics: plan.Unanalyzed, Implementations: "MP04 accepting command754 witness on canonical adopted F (484242fb) through sqliteconn/bootstrap, FK1/DQS0/0; complete adopted policy; synthetic resource forecasts; not performance qualification"}, indexPolicy)
	if err != nil {
		t.Fatal(err)
	}
	if golden.Receipt() == nil || len(golden.Manifest) != len(defs) {
		t.Fatal("incomplete canonical F Capture")
	}
	return golden
}

func TestMPWorkerRepeatKeepsExistingStopIdentity(t *testing.T) {
	f := mpWorkerFixture(t, true)
	if _, err := f.e.db.ExecContext(t.Context(), `UPDATE playback_sessions SET state='stopped',mp_grant_retired=1,mp_revision=mp_revision+1,mp_retired_generation=2,mp_retire_request_id='older-stop' WHERE id='mp-media'`); err != nil {
		t.Fatal(err)
	}
	out, err := f.run(t.Context(), f.command)
	if err != nil || out.Value.StopRequest != "older-stop" || !out.Value.PhysicalPending {
		t.Fatal("retirement request overwritten", out, err)
	}
	mpWorkerAssertReturned(t, f)
}

func TestMPWorkerRepeatQuotaFailureRollsBack(t *testing.T) {
	f := mpWorkerFixture(t, true)
	old := *f.w.effective.mpRepeatPlan
	small := old
	small.mpWorkerLimits.bytes = 1
	f.w.effective.mpRepeatPlan = &small
	out, err := f.run(t.Context(), f.command)
	if err == nil || !reflect.DeepEqual(out, mpWorkerResult{}) {
		t.Fatal("quota failure disclosed output", out, err)
	}
	var revision, retired, receipts, events int64
	if err = f.e.db.QueryRowContext(t.Context(), `SELECT revision FROM queues_v1 WHERE id='mp-queue'`).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	_ = f.e.db.QueryRowContext(t.Context(), `SELECT mp_retired_generation FROM playback_sessions WHERE id='mp-media'`).Scan(&retired)
	_ = f.e.db.QueryRowContext(t.Context(), `SELECT count(*) FROM mp_queue_command_receipts`).Scan(&receipts)
	_ = f.e.db.QueryRowContext(t.Context(), `SELECT count(*) FROM api_events`).Scan(&events)
	if revision != 3 || retired != 0 || receipts != 0 || events != 0 {
		t.Fatal("quota left partial effects", revision, retired, receipts, events)
	}
	mpWorkerAssertReturned(t, f)
}

func TestMPWorkerRepeatRowQuotaRefusesSecondReceipt(t *testing.T) {
	f := mpWorkerFixture(t, false)
	if _, err := f.run(t.Context(), f.command); err != nil {
		t.Fatal(err)
	}
	// Acknowledging advances only the floor: first receipt and bytes stay live.
	if _, err := f.e.db.ExecContext(t.Context(), `UPDATE queues_v1 SET mp_command_acknowledged_sequence=1 WHERE id='mp-queue'`); err != nil {
		t.Fatal(err)
	}
	p := *f.w.effective.mpRepeatPlan
	p.mpWorkerLimits.rows = 1
	f.w.effective.mpRepeatPlan = &p
	q := f.command.input
	q.ExpectedRevision = 4
	q.Repeat = "one"
	id := f.command.reference.Identity
	id.Sequence = 2
	id.OperationID = [16]byte{90}
	command, err := newMPRepeatCommand(id, q)
	if err != nil {
		t.Fatal(err)
	}
	out, err := f.w.runMPRepeat(t.Context(), f.cap, q.Queue, command.reference, command)
	if err == nil || !reflect.DeepEqual(out, mpWorkerResult{}) {
		t.Fatal("row quota bypass", out, err)
	}
	var rows, revision int64
	if err = f.e.db.QueryRowContext(t.Context(), `SELECT mp_receipt_rows,revision FROM queues_v1 WHERE id='mp-queue'`).Scan(&rows, &revision); err != nil || rows != 1 || revision != 4 {
		t.Fatal(rows, revision, err)
	}
	mpWorkerAssertReturned(t, f)
}

func TestMPWorkerRepeatPreservesPriorGenerationStop(t *testing.T) {
	for _, exited := range []bool{false, true} {
		t.Run(map[bool]string{false: "unexited", true: "exited"}[exited], func(t *testing.T) {
			f := mpWorkerFixture(t, true)
			if _, err := f.e.db.ExecContext(t.Context(), `UPDATE playback_sessions SET state='stopped',mp_grant_retired=1,mp_revision=2,mp_retired_generation=1,mp_retire_request_id='older-generation-stop' WHERE id='mp-media'`); err != nil {
				t.Fatal(err)
			}
			if exited {
				if _, err := f.e.db.ExecContext(t.Context(), `UPDATE playback_sessions SET mp_exited_generation=1,mp_exit_request_id='older-generation-stop',mp_exit_supervisor_incarnation=zeroblob(16),mp_exited_ms=100 WHERE id='mp-media'`); err != nil {
					t.Fatal(err)
				}
			}
			out, err := f.run(t.Context(), f.command)
			if !errors.Is(err, ErrConflict) || !reflect.DeepEqual(out, mpWorkerResult{}) {
				t.Fatal("older generation must refuse without overwriting proof", out, err)
			}
			var request string
			var retired, exit, revision, receipts int64
			if err = f.e.db.QueryRowContext(t.Context(), `SELECT mp_retire_request_id,mp_retired_generation,mp_exited_generation,mp_revision FROM playback_sessions WHERE id='mp-media'`).Scan(&request, &retired, &exit, &revision); err != nil {
				t.Fatal(err)
			}
			if err = f.e.db.QueryRowContext(t.Context(), `SELECT count(*) FROM mp_queue_command_receipts`).Scan(&receipts); err != nil {
				t.Fatal(err)
			}
			wantExit := int64(0)
			if exited {
				wantExit = 1
			}
			if request != "older-generation-stop" || retired != 1 || exit != wantExit || revision != 2 || receipts != 0 {
				t.Fatal(request, retired, exit, revision, receipts)
			}
			mpWorkerAssertReturned(t, f)
		})
	}
}

func TestMPWorkerRepeatRejectsHalfRetiredCurrentGeneration(t *testing.T) {
	f := mpWorkerFixture(t, true)
	if _, err := f.e.db.ExecContext(t.Context(), `UPDATE playback_sessions SET mp_retired_generation=2,mp_retire_request_id='partial-stop' WHERE id='mp-media'`); err != nil {
		t.Fatal(err)
	}
	out, err := f.run(t.Context(), f.command)
	if !errors.Is(err, ErrConflict) || !reflect.DeepEqual(out, mpWorkerResult{}) {
		t.Fatal("half-retired source accepted", out, err)
	}
	mpWorkerAssertReturned(t, f)
}
