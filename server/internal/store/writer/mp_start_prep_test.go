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

	"github.com/PorticoMediaServer/portico/server/internal/entityid"
	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

// The adopted full F schema (484242fb) is the only fixture DDL: preparation
// parents, the MP start receipt root, mp_start_execution and the queue control
// columns. A missing parent is a canonical-bootstrap failure, never a reason to
// recreate schema or an overlay here.
func mpStartPrepRequireCanonicalF(t *testing.T, e *executor) {
	t.Helper()
	for _, table := range []string{"playback_start_preparations", "playback_start_preparation_lifecycle", "playback_start_preparation_actor", "playback_start_preparation_source", "playback_start_preparation_queue", "playback_start_preparation_results", "mp_start_command_receipts", "mp_start_execution"} {
		var n int
		if err := e.db.QueryRowContext(t.Context(), `SELECT count(*) FROM sqlite_schema WHERE type='table' AND name=?`, table).Scan(&n); err != nil || n != 1 {
			t.Fatal("canonical F table missing", table, n, err)
		}
	}
	for _, index := range []string{"mp_start_command_owner", "mp_start_receipt_due"} {
		var n int
		if err := e.db.QueryRowContext(t.Context(), `SELECT count(*) FROM sqlite_schema WHERE type='index' AND name=?`, index).Scan(&n); err != nil || n != 1 {
			t.Fatal("canonical F index missing", index, n, err)
		}
	}
	var fk int
	if err := e.db.QueryRowContext(t.Context(), `PRAGMA foreign_keys`).Scan(&fk); err != nil || fk != 1 {
		t.Fatal("canonical F fixture requires enforced foreign keys", fk, err)
	}
}

type mpStartPrepFixture struct {
	w         *Writer
	e         *executor
	authority *governor.Authority
	cap       *mpStartPrepDisclosure
	actor     mpPrepActor
	source    mpPrepSource
	queue     mpPrepQueue
	ids       mpStartPrepStableIDs
	public    [16]byte
	inc       [16]byte
	nowMS     int64
	deadline  int64
	expires   int64
}

func mpStartPrepFixtureFor(t *testing.T) mpStartPrepFixture {
	t.Helper()
	w, pd, a := wcProgressCompositionFixture(t)
	e := w.effective.executor
	mpStartPrepRequireCanonicalF(t, e)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := e.db.ExecContext(t.Context(), q, args...); err != nil {
			t.Fatal(q, err)
		}
	}
	s := pd.scope
	if err := pd.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO identity_devices(id,account_id,installation_id,name,platform,app,app_version,first_seen,last_seen) VALUES('mp-start-device',?,'mp-start-install','Fixture','test','test','1','2026-11-08T00:00:00Z','2026-11-08T00:00:00Z')`, s.accountID)
	exec(`UPDATE authorization_session_families SET device_id='mp-start-device' WHERE id=?`, s.familyID)
	exec(`INSERT OR IGNORE INTO libraries(id,name,kind,root) VALUES('library-one','Library','movie','/wc-fixture')`)
	exec(`INSERT OR IGNORE INTO catalog_libraries(id,library_id,root) VALUES(?,'library-one','/wc-fixture')`, s.libraryID)
	exec(`INSERT OR IGNORE INTO catalog_kinds(id,name,playable,container,searchable,browsable,listening) VALUES(1,'movie',1,0,1,1,0)`)
	exec(`INSERT INTO library_sources(id,library_id,configured_root,root,generation,incarnation,root_identity,identity_confirmed,health,enabled) VALUES('mp-source','library-one','/wc-fixture','/wc-fixture',1,'mp-inc','mp-root',1,'ready',1)`)
	exec(`INSERT INTO catalog_assets(id,token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration,library_id) VALUES(8001,'mp-asset','mp.mkv',100,1,'matroska','h264','aac',1920,1080,1,?)`, s.libraryID)
	var public [16]byte
	copy(public[:], s.subjectPublicID[:])
	public[0] ^= 0x5a
	entityInc := [16]byte{0x52}
	exec(`INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(8002,?,?,1)`, public[:], s.libraryID)
	exec(`INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation) VALUES(8002,?,?,1,'Fixture','fixture','fixture','fixture','fixture',1,?,?)`, public[:], s.libraryID, entityInc[:], entityInc[:])
	exec(`INSERT INTO catalog_asset_links(entity_id,asset_id,library_id,part_index,available) VALUES(8002,8001,?,0,1)`, s.libraryID)
	exec(`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns,state) VALUES('mp-object','mp-source','mp-asset','mp-inc','mp.mkv','r1','{}',100,1,'available')`)
	exec(`INSERT INTO queues_v1(id,owner_kind,owner_id,authority,account_id,profile_id,revision,custody_generation,current_entry_id,created_ms,updated_ms) VALUES('mp-queue','device','mp-start-device','local',?,?,3,7,'entry',100,100)`, s.accountID, s.profileID)
	hash := strings.Repeat("a", 64)
	// The WC birth already owns this family/generation token; replace its hash
	// with a full 64-hex token instead of inventing a second generation.
	exec(`UPDATE authorization_family_tokens SET token_hash=? WHERE family_id=? AND generation=1`, hash, s.familyID)
	horizon, err := time.Parse(time.RFC3339Nano, s.horizon)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if e.now != nil {
		now = e.now()
	}
	inc := [16]byte{0x51}
	actor := mpPrepActor{server: s.serverID, authority: "local", account: s.accountID, profile: s.profileID,
		device: "mp-start-device", family: s.familyID, grant: "mp-grant", sessionHash: hash,
		epoch: s.epoch, tokenGeneration: 1, horizonMS: horizon.UnixMilli()}
	scope := mpPrepScope{memberBirth: s.memberBirth, profileBirth: s.profileBirth, memberLibraries: s.memberLibraries,
		memberRevision: s.memberRevision, profileRevision: s.profileRevision, membershipRole: s.membershipRole,
		role: s.role, primary: s.primary, primaryProfile: s.primaryProfile, profileLibraries: s.profileLibraries,
		horizon: s.horizon, expires: s.expires}
	source := mpPrepSource{library: "library-one", source: "mp-source", asset: "mp-asset", object: "mp-object",
		revision: "r1", root: "mp-root", incarnation: "mp-inc", remote: "", configuration: "mp-config",
		policy: "mp-policy", authority: "mp-read-authority", libraryGeneration: 1, configurationGeneration: 1,
		profileRevision: 1, size: 100, modifiedNS: 1}
	f := mpStartPrepFixture{w: w, e: e, authority: a, actor: actor, source: source,
		queue:  mpPrepQueue{id: "mp-queue", entry: "entry", ownerKind: "device", ownerID: "mp-start-device", revision: 3, custody: 7},
		ids:    mpStartPrepStableIDs{session: "mp-session", legacySession: "mp-legacy", legacyGeneration: 9, snapshot: "mp-snapshot"},
		public: public, inc: inc, nowMS: now.UnixMilli(), deadline: now.Add(30 * time.Minute).UnixMilli(),
		expires: now.Add(45 * time.Minute).UnixMilli()}
	if f.expires > actor.horizonMS {
		f.expires = actor.horizonMS
	}
	c := &mpStartPrepDisclosure{writer: w, lifetime: t.Context(), actor: actor, library: "library-one", scope: scope, incarnation: inc}
	c.self = c
	f.cap = c
	return f
}

func (f mpStartPrepFixture) beginInput() mpStartPrepBeginInput {
	return mpStartPrepBeginInput{
		birth:     mpStartPrepUOBirth{birth: mpStartJobUserBirth{root: strings.Repeat("r", 22), member: strings.Repeat("b", 32), profileLifetime: strings.Repeat("c", 32)}, incarnation: f.inc},
		id:        "mp-prep",
		item:      entityid.Encode(f.public[:]),
		keyDigest: strings.Repeat("d", 64), bodyDigest: strings.Repeat("e", 64),
		deadlineMS: f.deadline, tokenExpiresMS: f.expires,
		actor: f.actor, source: f.source, queue: f.queue, results: f.ids,
	}
}

func (f mpStartPrepFixture) beginIdentity() CommandIdentity {
	return CommandIdentity{Incarnation: f.inc, Sequence: 1, OperationID: [16]byte{0x61}}
}

func (f mpStartPrepFixture) claimInput(revision int64, execution string) mpStartPrepClaimInput {
	return mpStartPrepClaimInput{fence: mpStartPrepFence{id: "mp-prep", revision: revision, actor: f.actor,
		source: f.source, queue: f.queue}, execution: execution}
}

func (f mpStartPrepFixture) sealInput(revision int64, c mpStartPrepCompletion) mpStartPrepSealInput {
	return mpStartPrepSealInput{fence: mpStartPrepFence{id: "mp-prep", execution: c.execution, revision: revision,
		actor: f.actor, source: f.source, queue: f.queue}, completion: c}
}

func (f mpStartPrepFixture) prepCommand(t *testing.T, kind CommandID, id CommandIdentity, payload any) (*mpStartPrepCommand, error) {
	t.Helper()
	return newMPStartPrepCommand(kind, id, payload)
}

func (f mpStartPrepFixture) run(t *testing.T, kind CommandID, reference ReceiptReference, payload any) (mpStartPrepResult, error) {
	t.Helper()
	return f.w.runMPStartPrep(t.Context(), f.cap, kind, reference, payload)
}

func (f mpStartPrepFixture) begin(t *testing.T) (mpStartPrepResult, error) {
	t.Helper()
	command, err := f.prepCommand(t, MPStartPrepBegin, f.beginIdentity(), f.beginInput())
	if err != nil {
		t.Fatal(err)
	}
	return f.run(t, MPStartPrepBegin, command.reference, f.beginInput())
}

func (f mpStartPrepFixture) claim(t *testing.T, revision int64, execution string, sequence int64, op byte) (mpStartPrepResult, error) {
	t.Helper()
	input := f.claimInput(revision, execution)
	id := CommandIdentity{Incarnation: f.inc, Sequence: sequence, OperationID: [16]byte{op}}
	command, err := f.prepCommand(t, MPStartPrepClaim, id, input)
	if err != nil {
		t.Fatal(err)
	}
	return f.run(t, MPStartPrepClaim, command.reference, input)
}

func (f mpStartPrepFixture) seal(t *testing.T, revision int64, c mpStartPrepCompletion, sequence int64, op byte) (mpStartPrepResult, error) {
	t.Helper()
	input := f.sealInput(revision, c)
	id := CommandIdentity{Incarnation: f.inc, Sequence: sequence, OperationID: [16]byte{op}}
	command, err := f.prepCommand(t, MPStartPrepSeal, id, input)
	if err != nil {
		t.Fatal(err)
	}
	return f.run(t, MPStartPrepSeal, command.reference, input)
}

func (f mpStartPrepFixture) ack(t *testing.T, sequence int64) {
	t.Helper()
	result, err := f.e.db.ExecContext(t.Context(), `UPDATE playback_start_preparations SET mp_command_acknowledged_sequence=? WHERE id='mp-prep' AND mp_command_last_sequence>=? AND mp_command_acknowledged_sequence<=?`, sequence, sequence, sequence)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		t.Fatal("ACK floor did not advance", n, err)
	}
}

// mpStartPrepCaptureFixture is the same complete Capture the landed MP-04 frame
// uses: the canonical adopted schema and the existing registry union, with no
// stored golden and no performance claim.
func mpStartPrepCaptureFixture(t *testing.T, c *sql.Conn, cfg sqliteconn.Config, defs []stmt.Definition, sources ...string) plan.Golden {
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
	golden, err := cachedCapture(t.Context(), c, catalogue, plan.Fixture{ConnectionPolicy: cfg, SHA256: hex.EncodeToString(hash.Sum(nil)), Statistics: plan.Unanalyzed,
		Implementations: "MP07 start preparation birth/claim/seal on canonical adopted F (484242fb) through sqliteconn/bootstrap, FK1/DQS0/0; complete adopted policy; synthetic resource forecasts; not performance qualification"}, indexPolicy)
	if err != nil {
		t.Fatal(err)
	}
	if golden.Receipt() == nil || len(golden.Manifest) != len(defs) {
		t.Fatal("incomplete canonical F Capture")
	}
	return golden
}

func mpStartPrepComposeFixture(t *testing.T, f mpStartPrepFixture) {
	t.Helper()
	all := []stmt.Definition{}
	for _, entry := range f.w.effective.registry.Manifest() {
		all = append(all, entry.Definition)
	}
	for _, d := range mpStartPrepDefinitions() {
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
	conn, err := f.e.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	capturePolicy := f.e.config
	capturePolicy.Synchronous = sqliteconn.Normal
	golden := mpStartPrepCaptureFixture(t, conn, capturePolicy, all, "mp_start_prep_test.go", "mp_start_prep_core.go", "mp_start_prep_receipts.go", "mp_start_prep_births.go", "mp_start_prep_commands.go")
	if err = conn.Close(); err != nil {
		t.Fatal(err)
	}
	p := effectiveFixturePlan()
	p.registration = golden.Receipt()
	p.steps = nil
	p.mpWorkerMemory = commandMemoryBudget{64 << 20, 64 << 20}
	for _, d := range mpStartPrepDefinitions() {
		p.steps = append(p.steps, statementStep{d.ID, Work{32, 8000, 32, 65536, 100 * time.Microsecond}})
	}
	if err = composeMPStartPrep(t.Context(), f.w.effective, p); err != nil {
		t.Fatal(err)
	}
}

func mpStartPrepAssertReturned(t *testing.T, f mpStartPrepFixture) {
	t.Helper()
	if f.authority.Usage() != ([4]governor.Amounts{}) || f.e.db.Stats().InUse != 0 {
		t.Fatal("physical writer custody retained", f.authority.Usage(), f.e.db.Stats())
	}
}

func TestMPStartPrepBirthClaimSealAndSavedResults(t *testing.T) {
	f := mpStartPrepFixtureFor(t)
	mpStartPrepComposeFixture(t, f)
	begin, err := f.begin(t)
	if err != nil || begin.Outcome.State != Applied || begin.Outcome.Replayed ||
		begin.Value.State != "pending" || begin.Value.Revision != 1 || begin.Value.Execution != "" || begin.Value.Sealed {
		t.Fatal(begin, err)
	}
	var state, execution string
	var sealed, revision, rows, receipts int64
	if err = f.e.db.QueryRowContext(t.Context(), `SELECT state,revision,execution_id,sealed FROM playback_start_preparation_lifecycle WHERE operation_id='mp-prep'`).Scan(&state, &revision, &execution, &sealed); err != nil || state != "pending" || revision != 1 || execution != "" || sealed != 0 {
		t.Fatal("birth lifecycle", state, revision, execution, sealed, err)
	}
	// The birth stages exact source/plan rows but starts no execution.
	if err = f.e.db.QueryRowContext(t.Context(), `SELECT count(*) FROM mp_start_execution`).Scan(&rows); err != nil || rows != 0 {
		t.Fatal("premature execution row", rows, err)
	}
	// Identical duplicate replays the original without a second preparation.
	duplicate, err := f.begin(t)
	if err != nil || duplicate.Outcome.State != Applied || !duplicate.Outcome.Replayed ||
		duplicate.Value != begin.Value || duplicate.Digest != begin.Digest {
		t.Fatal("duplicate birth", duplicate, err)
	}
	var preps int64
	if err = f.e.db.QueryRowContext(t.Context(), `SELECT count(*) FROM playback_start_preparations`).Scan(&preps); err != nil || preps != 1 {
		t.Fatal("duplicate staged a second preparation", preps, err)
	}
	claim, err := f.claim(t, 1, "mp-execution", 1, 0x62)
	if err != nil || claim.Outcome.State != Applied || claim.Value.State != "preparing" ||
		claim.Value.Execution != "mp-execution" || !claim.Value.Sealed || claim.Value.Revision != 2 {
		t.Fatal(claim, err)
	}
	// Claim assigns one execution but starts no helper and stages nothing.
	var stagedRows, stagedBytes int64
	var resultDigest string
	if err = f.e.db.QueryRowContext(t.Context(), `SELECT staged_rows,staged_bytes,result_digest,cleaned FROM mp_start_execution WHERE operation_id='mp-prep'`).Scan(&stagedRows, &stagedBytes, &resultDigest, &rows); err != nil || stagedRows != 0 || stagedBytes != 0 || resultDigest != "" || rows != 0 {
		t.Fatal("claim started helper work", stagedRows, stagedBytes, resultDigest, rows, err)
	}
	// A second claim with a stale revision is a conflict, never a takeover.
	if out, e := f.claim(t, 1, "other-execution", 1, 0x63); e == nil || !reflect.DeepEqual(out, mpStartPrepResult{}) {
		t.Fatal("stale claim", out, e)
	}
	// The saved claim original survives head drift while unacknowledged.
	if _, err = f.e.db.ExecContext(t.Context(), `UPDATE playback_start_preparations SET mp_owner_revision=mp_owner_revision+9`); err != nil {
		t.Fatal(err)
	}
	savedClaim, err := f.claim(t, 1, "mp-execution", 1, 0x62)
	if err != nil || savedClaim.Outcome.State != Applied || !savedClaim.Outcome.Replayed || savedClaim.Value != claim.Value || savedClaim.Digest != claim.Digest {
		t.Fatal("claim replay", savedClaim, err)
	}
	// Seal needs the claim receipt's acknowledgement floor (command 780).
	if out, e := f.seal(t, 2, mpStartPrepCompletion{execution: "mp-execution", resultDigest: strings.Repeat("f", 64), stagedRows: 4, stagedBytes: 128}, 2, 0x64); e == nil || !reflect.DeepEqual(out, mpStartPrepResult{}) {
		t.Fatal("seal bypassed the ACK window", out, e)
	}
	f.ack(t, 1)
	seal, err := f.seal(t, 2, mpStartPrepCompletion{execution: "mp-execution", resultDigest: strings.Repeat("f", 64), stagedRows: 4, stagedBytes: 128}, 2, 0x65)
	if err != nil || seal.Outcome.State != Applied || seal.Value.State != "prepared" || !seal.Value.Sealed || seal.Value.Revision != 3 {
		t.Fatal(seal, err)
	}
	var debt, control, last, ack, receiptRows, receiptBytes int64
	if err = f.e.db.QueryRowContext(t.Context(), `SELECT count(*),sum(variable_bytes) FROM mp_start_command_receipts`).Scan(&receipts, &debt); err != nil {
		t.Fatal(err)
	}
	if err = f.e.db.QueryRowContext(t.Context(), `SELECT mp_command_last_sequence,mp_command_acknowledged_sequence,mp_receipt_rows,mp_receipt_variable_bytes,mp_control_variable_bytes FROM playback_start_preparations WHERE id='mp-prep'`).Scan(&last, &ack, &receiptRows, &receiptBytes, &control); err != nil {
		t.Fatal(err)
	}
	if receipts != 2 || debt <= 0 || last != 2 || ack != 1 || receiptRows != 2 || receiptBytes != debt || control <= 0 {
		t.Fatal("receipt debt", receipts, debt, last, ack, receiptRows, receiptBytes, control)
	}
	// The saved seal original survives head drift and returns identical bytes.
	savedSeal, err := f.seal(t, 2, mpStartPrepCompletion{execution: "mp-execution", resultDigest: strings.Repeat("f", 64), stagedRows: 4, stagedBytes: 128}, 2, 0x65)
	if err != nil || savedSeal.Outcome.State != Applied || !savedSeal.Outcome.Replayed || savedSeal.Value != seal.Value || savedSeal.Digest != seal.Digest {
		t.Fatal("seal replay", savedSeal, err)
	}
	mpStartPrepAssertReturned(t, f)
}

func TestMPStartPrepRefusalsKeepOriginalState(t *testing.T) {
	for _, name := range []string{"changed-duplicate", "partial-seal", "interrupted-stage", "deadline-drift", "queue-drift", "revoked", "forged-actor", "copied"} {
		t.Run(name, func(t *testing.T) {
			f := mpStartPrepFixtureFor(t)
			mpStartPrepComposeFixture(t, f)
			if _, err := f.begin(t); err != nil {
				t.Fatal(err)
			}
			if _, err := f.claim(t, 1, "mp-execution", 1, 0x62); err != nil {
				t.Fatal(err)
			}
			f.ack(t, 1)
			var out mpStartPrepResult
			var err error
			switch name {
			case "changed-duplicate":
				q := f.beginInput()
				q.bodyDigest = strings.Repeat("9", 64)
				out, err = f.run(t, MPStartPrepBegin, f.beginReference(t, q), q)
			case "partial-seal":
				if _, e := f.e.db.ExecContext(t.Context(), `UPDATE mp_start_execution SET result_digest=? WHERE operation_id='mp-prep'`, strings.Repeat("f", 64)); e != nil {
					t.Fatal(e)
				}
				out, err = f.seal(t, 2, mpStartPrepCompletion{execution: "mp-execution", resultDigest: strings.Repeat("f", 64), stagedRows: 4, stagedBytes: 128}, 2, 0x65)
			case "interrupted-stage":
				// A stage interrupted before writing anything reports zero
				// totals. That is never a complete seal, so the canonical
				// preflight refuses the request before any frame runs and the
				// preparation must stay untouched.
				q := f.sealInput(2, mpStartPrepCompletion{execution: "mp-execution", resultDigest: strings.Repeat("f", 64)})
				reference := ReceiptReference{Identity: CommandIdentity{Incarnation: f.inc, Sequence: 2, OperationID: [16]byte{0x65}}}
				out, err = f.run(t, MPStartPrepSeal, reference, q)
			case "deadline-drift":
				if _, e := f.e.db.ExecContext(t.Context(), `UPDATE playback_start_preparations SET deadline_ms=created_ms+1 WHERE id='mp-prep'`); e != nil {
					t.Fatal(e)
				}
				out, err = f.seal(t, 2, mpStartPrepCompletion{execution: "mp-execution", resultDigest: strings.Repeat("f", 64), stagedRows: 4, stagedBytes: 128}, 2, 0x65)
			case "queue-drift":
				if _, e := f.e.db.ExecContext(t.Context(), `UPDATE queues_v1 SET custody_generation=8 WHERE id='mp-queue'`); e != nil {
					t.Fatal(e)
				}
				out, err = f.seal(t, 2, mpStartPrepCompletion{execution: "mp-execution", resultDigest: strings.Repeat("f", 64), stagedRows: 4, stagedBytes: 128}, 2, 0x65)
			case "revoked":
				if _, e := f.e.db.ExecContext(t.Context(), `UPDATE authorization_family_tokens SET retired=1 WHERE token_hash=?`, f.actor.sessionHash); e != nil {
					t.Fatal(e)
				}
				out, err = f.seal(t, 2, mpStartPrepCompletion{execution: "mp-execution", resultDigest: strings.Repeat("f", 64), stagedRows: 4, stagedBytes: 128}, 2, 0x65)
			case "forged-actor":
				q := f.sealInput(2, mpStartPrepCompletion{execution: "mp-execution", resultDigest: strings.Repeat("f", 64), stagedRows: 4, stagedBytes: 128})
				q.fence.actor.device = "forged-device"
				out, err = f.run(t, MPStartPrepSeal, f.claimReference(t, MPStartPrepSeal, q, 2, 0x65), q)
			case "copied":
				f.cap = &mpStartPrepDisclosure{self: f.cap, writer: f.cap.writer, lifetime: f.cap.lifetime, actor: f.cap.actor, library: f.cap.library, incarnation: f.cap.incarnation}
				out, err = f.seal(t, 2, mpStartPrepCompletion{execution: "mp-execution", resultDigest: strings.Repeat("f", 64), stagedRows: 4, stagedBytes: 128}, 2, 0x65)
			}
			if err == nil || !reflect.DeepEqual(out, mpStartPrepResult{}) {
				t.Fatal("refusal disclosed a result", out, err)
			}
			var state string
			var revision, receipts int64
			if e := f.e.db.QueryRowContext(t.Context(), `SELECT state,revision FROM playback_start_preparation_lifecycle WHERE operation_id='mp-prep'`).Scan(&state, &revision); e != nil || state != "preparing" || revision != 2 {
				t.Fatal("refusal changed lifecycle", state, revision, e)
			}
			if e := f.e.db.QueryRowContext(t.Context(), `SELECT count(*) FROM mp_start_command_receipts`).Scan(&receipts); e != nil || receipts != 1 {
				t.Fatal("refusal wrote a receipt", receipts, e)
			}
			mpStartPrepAssertReturned(t, f)
		})
	}
}

func (f mpStartPrepFixture) beginReference(t *testing.T, q mpStartPrepBeginInput) ReceiptReference {
	t.Helper()
	command, err := f.prepCommand(t, MPStartPrepBegin, f.beginIdentity(), q)
	if err != nil {
		t.Fatal(err)
	}
	return command.reference
}

func (f mpStartPrepFixture) claimReference(t *testing.T, kind CommandID, q any, sequence int64, op byte) ReceiptReference {
	t.Helper()
	command, err := f.prepCommand(t, kind, CommandIdentity{Incarnation: f.inc, Sequence: sequence, OperationID: [16]byte{op}}, q)
	if err != nil {
		t.Fatal(err)
	}
	return command.reference
}

func TestMPStartPrepReplayAfterAckRetired(t *testing.T) {
	f := mpStartPrepFixtureFor(t)
	mpStartPrepComposeFixture(t, f)
	if _, err := f.begin(t); err != nil {
		t.Fatal(err)
	}
	claim, err := f.claim(t, 1, "mp-execution", 1, 0x62)
	if err != nil {
		t.Fatal(err)
	}
	f.ack(t, 1)
	seal, err := f.seal(t, 2, mpStartPrepCompletion{execution: "mp-execution", resultDigest: strings.Repeat("f", 64), stagedRows: 4, stagedBytes: 128}, 2, 0x65)
	if err != nil {
		t.Fatal(err)
	}
	f.ack(t, 2)
	retiredClaim, err := f.claim(t, 1, "mp-execution", 1, 0x62)
	if err != nil || retiredClaim.Outcome.State != ResultRetired || retiredClaim.Value != (mpStartPrepValue{}) || retiredClaim.Digest != (ResultDigest{}) {
		t.Fatal("ACKed claim served as Applied", retiredClaim, err)
	}
	retiredSeal, err := f.seal(t, 2, mpStartPrepCompletion{execution: "mp-execution", resultDigest: strings.Repeat("f", 64), stagedRows: 4, stagedBytes: 128}, 2, 0x65)
	if err != nil || retiredSeal.Outcome.State != ResultRetired || retiredSeal.Value != (mpStartPrepValue{}) {
		t.Fatal("ACKed seal served as Applied", retiredSeal, err)
	}
	if claim.Value.State != "preparing" || seal.Value.State != "prepared" {
		t.Fatal("originals changed")
	}
	// A cancelled lifetime releases no value and writes no effect.
	ctx, cancel := context.WithCancel(t.Context())
	f.cap.lifetime = ctx
	cancel()
	if out, err := f.claim(t, 1, "mp-execution", 3, 0x66); err == nil || (!errors.Is(err, context.Canceled) && !errors.Is(err, ErrDisclosureDenied)) || !reflect.DeepEqual(out, mpStartPrepResult{}) {
		t.Fatal("cancelled lifetime disclosed", out, err)
	}
	f.cap.lifetime = t.Context()
	mpStartPrepAssertReturned(t, f)
}

// mpStartPrepFreezeClock pins the executor's protocol clock so a test can move
// time past the stored deadline without editing any row. The returned advance
// moves the frozen instant forward; every call stays monotonic.
func mpStartPrepFreezeClock(f mpStartPrepFixture) func(time.Duration) {
	now := f.e.now()
	f.e.now = func() time.Time { return now }
	return func(d time.Duration) { now = now.Add(d) }
}

// tightDeadline puts the stored deadline one millisecond after the frozen
// clock, so a two-millisecond advance models "the deadline passed" without a
// wall jump that the WAL would treat as a restart.
func (f *mpStartPrepFixture) tightDeadline() {
	f.deadline = f.e.now().UnixMilli() + 1
}

// mpStartPrepDrift moves one current dependency after a command committed and
// its response was lost. It never edits the preparation's saved receipt.
func mpStartPrepDrift(t *testing.T, f mpStartPrepFixture, drift string, advance func(time.Duration)) {
	t.Helper()
	switch drift {
	case "queue":
		if _, err := f.e.db.ExecContext(t.Context(), `UPDATE queues_v1 SET revision=revision+1,custody_generation=custody_generation+1 WHERE id='mp-queue'`); err != nil {
			t.Fatal(err)
		}
	case "source":
		if _, err := f.e.db.ExecContext(t.Context(), `UPDATE inventory_objects SET revision='r2' WHERE id='mp-object'`); err != nil {
			t.Fatal(err)
		}
	case "deadline":
		// The frozen clock moves just past the tight stored deadline. A large
		// wall jump would look like a WAL restart, not an ordinary deadline.
		advance(2 * time.Millisecond)
	default:
		t.Fatal("unknown drift", drift)
	}
}

func (f mpStartPrepFixture) sealCompletion() mpStartPrepCompletion {
	return mpStartPrepCompletion{execution: "mp-execution", resultDigest: strings.Repeat("f", 64), stagedRows: 4, stagedBytes: 128}
}

// TestMPStartPrepReplaySurvivesFreshDrift pins the contract order: current
// disclosure/actor authority, THEN the saved original, THEN the fresh
// deadline/item/source/queue gates. An applied-but-unacknowledged begin, claim
// or seal is served with zero new effects after the queue moved, the source
// revision changed or the deadline passed.
func TestMPStartPrepReplaySurvivesFreshDrift(t *testing.T) {
	for _, command := range []string{"begin", "claim", "seal"} {
		for _, drift := range []string{"queue", "source", "deadline"} {
			t.Run(command+"-"+drift, func(t *testing.T) {
				f := mpStartPrepFixtureFor(t)
				mpStartPrepComposeFixture(t, f)
				advance := mpStartPrepFreezeClock(f)
				if drift == "deadline" {
					f.tightDeadline()
				}
				begin, err := f.begin(t)
				if err != nil {
					t.Fatal(err)
				}
				var original mpStartPrepResult
				var replay func() (mpStartPrepResult, error)
				switch command {
				case "begin":
					original = begin
					replay = func() (mpStartPrepResult, error) { return f.begin(t) }
				case "claim":
					if original, err = f.claim(t, 1, "mp-execution", 1, 0x62); err != nil {
						t.Fatal(err)
					}
					replay = func() (mpStartPrepResult, error) { return f.claim(t, 1, "mp-execution", 1, 0x62) }
				case "seal":
					if _, err = f.claim(t, 1, "mp-execution", 1, 0x62); err != nil {
						t.Fatal(err)
					}
					f.ack(t, 1)
					if original, err = f.seal(t, 2, f.sealCompletion(), 2, 0x65); err != nil {
						t.Fatal(err)
					}
					replay = func() (mpStartPrepResult, error) { return f.seal(t, 2, f.sealCompletion(), 2, 0x65) }
				}
				before := mpStartPrepReadFacts(t, f)
				mpStartPrepDrift(t, f, drift, advance)
				saved, err := replay()
				if err != nil || saved.Outcome.State != Applied || !saved.Outcome.Replayed ||
					saved.Value != original.Value || saved.Digest != original.Digest {
					t.Fatal("replay lost the saved original", saved, original, err)
				}
				if after := mpStartPrepReadFacts(t, f); after != before {
					t.Fatalf("replay wrote effects: before %+v after %+v", before, after)
				}
				mpStartPrepAssertReturned(t, f)
			})
		}
	}
	// An acknowledged cohort stays Retired even when the queue moved afterwards.
	t.Run("acked-claim-retired-after-queue-drift", func(t *testing.T) {
		f := mpStartPrepFixtureFor(t)
		mpStartPrepComposeFixture(t, f)
		if _, err := f.begin(t); err != nil {
			t.Fatal(err)
		}
		if _, err := f.claim(t, 1, "mp-execution", 1, 0x62); err != nil {
			t.Fatal(err)
		}
		f.ack(t, 1)
		if _, err := f.e.db.ExecContext(t.Context(), `UPDATE queues_v1 SET revision=revision+1 WHERE id='mp-queue'`); err != nil {
			t.Fatal(err)
		}
		retired, err := f.claim(t, 1, "mp-execution", 1, 0x62)
		if err != nil || retired.Outcome.State != ResultRetired || retired.Value != (mpStartPrepValue{}) {
			t.Fatal("ACKed replay after drift", retired, err)
		}
		mpStartPrepAssertReturned(t, f)
	})
}

// TestMPStartPrepFreshStillRefusesDrift proves the moved gates still run for a
// FRESH command in the same states: the fix is ordering, not a new bypass.
func TestMPStartPrepFreshStillRefusesDrift(t *testing.T) {
	for _, drift := range []string{"queue", "source", "deadline"} {
		for _, command := range []string{"begin", "claim", "seal"} {
			t.Run(command+"-"+drift, func(t *testing.T) {
				f := mpStartPrepFixtureFor(t)
				mpStartPrepComposeFixture(t, f)
				advance := mpStartPrepFreezeClock(f)
				if drift == "deadline" {
					f.tightDeadline()
				}
				if _, err := f.begin(t); err != nil {
					t.Fatal(err)
				}
				if command == "seal" {
					if _, err := f.claim(t, 1, "mp-execution", 1, 0x62); err != nil {
						t.Fatal(err)
					}
					f.ack(t, 1)
				}
				mpStartPrepDrift(t, f, drift, advance)
				var out mpStartPrepResult
				var err error
				switch command {
				case "begin":
					// A fresh birth has its own owner incarnation and start key.
					inc := [16]byte{0x52}
					q := f.beginInput()
					q.id, q.birth.incarnation, q.keyDigest = "mp-prep-2", inc, strings.Repeat("7", 64)
					q.results = mpStartPrepStableIDs{session: "mp-session-2", legacySession: "mp-legacy-2", legacyGeneration: 9, snapshot: "mp-snapshot-2"}
					c := &mpStartPrepDisclosure{writer: f.cap.writer, lifetime: f.cap.lifetime, actor: f.cap.actor,
						library: f.cap.library, scope: f.cap.scope, incarnation: inc}
					c.self = c
					cmd, e := newMPStartPrepCommand(MPStartPrepBegin, CommandIdentity{Incarnation: inc, Sequence: 1, OperationID: [16]byte{0x68}}, q)
					if e != nil {
						t.Fatal(e)
					}
					out, err = f.w.runMPStartPrep(t.Context(), c, MPStartPrepBegin, cmd.reference, q)
				case "claim":
					out, err = f.claim(t, 1, "mp-execution", 1, 0x62)
				case "seal":
					out, err = f.seal(t, 2, f.sealCompletion(), 2, 0x65)
				}
				if !errors.Is(err, ErrConflict) || !reflect.DeepEqual(out, mpStartPrepResult{}) {
					t.Fatal("fresh command bypassed the currency gate", out, err)
				}
				var preps, receipts int64
				if e := f.e.db.QueryRowContext(t.Context(), `SELECT count(*) FROM playback_start_preparations`).Scan(&preps); e != nil || preps != 1 {
					t.Fatal("fresh refusal staged a preparation", preps, e)
				}
				if e := f.e.db.QueryRowContext(t.Context(), `SELECT count(*) FROM mp_start_command_receipts`).Scan(&receipts); e != nil || receipts > 1 {
					t.Fatal("fresh refusal wrote a receipt", receipts, e)
				}
				mpStartPrepAssertReturned(t, f)
			})
		}
	}
}

// TestMPStartPrepScopeAuthority pins the SAME-TURN member/profile authority the
// frame was missing: removing the library, tightening the profile restriction,
// or presenting a capability for another library refuses both fresh commands and
// replays. An unchanged scope still passes.
func TestMPStartPrepScopeAuthority(t *testing.T) {
	denied := func(t *testing.T, out mpStartPrepResult, err error) {
		t.Helper()
		if !errors.Is(err, ErrDisclosureDenied) || !reflect.DeepEqual(out, mpStartPrepResult{}) {
			t.Fatal("scope authority was not enforced", out, err)
		}
	}
	t.Run("library-removed-fresh", func(t *testing.T) {
		f := mpStartPrepFixtureFor(t)
		mpStartPrepComposeFixture(t, f)
		// A current disclosure carries the new list; the library itself is gone.
		f.cap.scope.memberLibraries = `["other-library"]`
		if _, err := f.e.db.ExecContext(t.Context(), `UPDATE direct_memberships SET allowed_libraries=? WHERE account_id=?`, f.cap.scope.memberLibraries, f.cap.actor.account); err != nil {
			t.Fatal(err)
		}
		out, err := f.begin(t)
		denied(t, out, err)
		var preps int64
		if e := f.e.db.QueryRowContext(t.Context(), `SELECT count(*) FROM playback_start_preparations`).Scan(&preps); e != nil || preps != 0 {
			t.Fatal("denied birth staged a preparation", preps, e)
		}
		mpStartPrepAssertReturned(t, f)
	})
	t.Run("library-removed-replay", func(t *testing.T) {
		f := mpStartPrepFixtureFor(t)
		mpStartPrepComposeFixture(t, f)
		if _, err := f.begin(t); err != nil {
			t.Fatal(err)
		}
		claim, err := f.claim(t, 1, "mp-execution", 1, 0x62)
		if err != nil {
			t.Fatal(err)
		}
		f.cap.scope.memberLibraries = `["other-library"]`
		if _, err := f.e.db.ExecContext(t.Context(), `UPDATE direct_memberships SET allowed_libraries=? WHERE account_id=?`, f.cap.scope.memberLibraries, f.cap.actor.account); err != nil {
			t.Fatal(err)
		}
		out, err := f.claim(t, 1, "mp-execution", 1, 0x62)
		denied(t, out, err)
		out, err = f.begin(t)
		denied(t, out, err)
		var receipts int64
		if e := f.e.db.QueryRowContext(t.Context(), `SELECT count(*) FROM mp_start_command_receipts`).Scan(&receipts); e != nil || receipts != 1 {
			t.Fatal("denied replay wrote a receipt", receipts, e)
		}
		if claim.Value.State != "preparing" {
			t.Fatal("denied replay changed the original", claim.Value)
		}
		mpStartPrepAssertReturned(t, f)
	})
	t.Run("profile-restriction-tightened", func(t *testing.T) {
		f := mpStartPrepFixtureFor(t)
		mpStartPrepComposeFixture(t, f)
		f.cap.scope.profileLibraries = sql.NullString{String: `["other-library"]`, Valid: true}
		if _, err := f.e.db.ExecContext(t.Context(), `UPDATE direct_profiles SET allowed_libraries=? WHERE id=?`, f.cap.scope.profileLibraries.String, f.cap.actor.profile); err != nil {
			t.Fatal(err)
		}
		out, err := f.begin(t)
		denied(t, out, err)
		mpStartPrepAssertReturned(t, f)
	})
	t.Run("capability-library-mismatch", func(t *testing.T) {
		f := mpStartPrepFixtureFor(t)
		mpStartPrepComposeFixture(t, f)
		f.cap.library = "library-two"
		out, err := f.begin(t)
		denied(t, out, err)
		mpStartPrepAssertReturned(t, f)
	})
	t.Run("source-library-mismatch", func(t *testing.T) {
		f := mpStartPrepFixtureFor(t)
		mpStartPrepComposeFixture(t, f)
		q := f.beginInput()
		q.source.library = "library-two"
		out, err := f.run(t, MPStartPrepBegin, f.beginReference(t, q), q)
		denied(t, out, err)
		mpStartPrepAssertReturned(t, f)
	})
	t.Run("unchanged-scope-passes", func(t *testing.T) {
		f := mpStartPrepFixtureFor(t)
		mpStartPrepComposeFixture(t, f)
		if _, err := f.begin(t); err != nil {
			t.Fatal(err)
		}
		if _, err := f.claim(t, 1, "mp-execution", 1, 0x62); err != nil {
			t.Fatal(err)
		}
		mpStartPrepAssertReturned(t, f)
	})
}

// mpStartPrepPrefixFault fails one exact statement so the turn is observed after
// its earlier writes already ran inside the same transaction.
type mpStartPrepPrefixFault struct {
	writerDriverConn
	target     string
	occurrence int
	seen       int
}

func (c *mpStartPrepPrefixFault) intercept(q string) bool {
	if q != c.target {
		return false
	}
	c.seen++
	return c.seen == c.occurrence
}

func (c *mpStartPrepPrefixFault) QueryContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	if c.intercept(q) {
		return nil, errMPMutation
	}
	return c.writerDriverConn.QueryContext(ctx, q, args)
}

func (c *mpStartPrepPrefixFault) ExecContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Result, error) {
	if c.intercept(q) {
		return nil, errMPMutation
	}
	return c.writerDriverConn.ExecContext(ctx, q, args)
}

func mpStartPrepReopenFaultPool(t *testing.T, f mpStartPrepFixture, fault *mpStartPrepPrefixFault) {
	t.Helper()
	if err := f.e.db.Close(); err != nil {
		t.Fatal(err)
	}
	connector, err := sqliteconn.Connector(f.w.effective.path, f.e.config, sqliteconn.ExistingFile)
	if err != nil {
		t.Fatal(err)
	}
	fault.writerDriverConn = nil
	f.e.openPool(connector, func(c driver.Conn) driver.Conn {
		fault.writerDriverConn = c.(writerDriverConn)
		return fault
	})
	primeEffectiveFixturePool(t, f.e)
}

func mpStartPrepFaultPool(t *testing.T, f mpStartPrepFixture, fault *faultConnector) {
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

type mpStartPrepFacts struct {
	state, execution           string
	revision, sealed, receipts int64
	last, ack, ownerRevision   int64
	rows, bytes, control       int64
	executionRows              int64
	stagedRows, stagedBytes    sql.NullInt64
	executionResult            sql.NullString
}

func mpStartPrepReadFacts(t *testing.T, f mpStartPrepFixture) mpStartPrepFacts {
	t.Helper()
	var o mpStartPrepFacts
	if err := f.e.db.QueryRowContext(t.Context(), `SELECT state,revision,execution_id,sealed FROM playback_start_preparation_lifecycle WHERE operation_id='mp-prep'`).Scan(&o.state, &o.revision, &o.execution, &o.sealed); err != nil {
		t.Fatal(err)
	}
	if err := f.e.db.QueryRowContext(t.Context(), `SELECT mp_command_last_sequence,mp_command_acknowledged_sequence,mp_owner_revision,mp_receipt_rows,mp_receipt_variable_bytes,mp_control_variable_bytes FROM playback_start_preparations WHERE id='mp-prep'`).Scan(&o.last, &o.ack, &o.ownerRevision, &o.rows, &o.bytes, &o.control); err != nil {
		t.Fatal(err)
	}
	if err := f.e.db.QueryRowContext(t.Context(), `SELECT count(*) FROM mp_start_command_receipts`).Scan(&o.receipts); err != nil {
		t.Fatal(err)
	}
	if err := f.e.db.QueryRowContext(t.Context(), `SELECT count(*),coalesce(max(staged_rows),0),coalesce(max(staged_bytes),0),coalesce(max(result_digest),'') FROM mp_start_execution WHERE operation_id='mp-prep'`).Scan(&o.executionRows, &o.stagedRows, &o.stagedBytes, &o.executionResult); err != nil {
		t.Fatal(err)
	}
	return o
}

func TestMPStartPrepClaimAndSealPrefixRollback(t *testing.T) {
	for _, p := range []struct {
		name       string
		step       CommandID
		id         stmt.ID
		occurrence int
	}{
		{"claim-lifecycle", MPStartPrepClaim, "mp.start.claim", 1},
		{"claim-execution", MPStartPrepClaim, "mp.start.execution_insert", 1},
		{"seal-completion", MPStartPrepSeal, "mp.start.completion", 1},
		{"seal-lifecycle", MPStartPrepSeal, "mp.start.seal", 1},
		{"seal-receipt", MPStartPrepSeal, "mp.start.receipt_insert", 1},
		{"seal-debt", MPStartPrepSeal, "mp.start.debt", 1},
	} {
		t.Run(p.name, func(t *testing.T) {
			f := mpStartPrepFixtureFor(t)
			mpStartPrepComposeFixture(t, f)
			if _, err := f.begin(t); err != nil {
				t.Fatal(err)
			}
			if p.step == MPStartPrepSeal {
				if _, err := f.claim(t, 1, "mp-execution", 1, 0x62); err != nil {
					t.Fatal(err)
				}
				f.ack(t, 1)
			}
			before := mpStartPrepReadFacts(t, f)
			var target string
			for _, d := range mpStartPrepDefinitions() {
				if d.ID == p.id {
					target = d.SQL
				}
			}
			if target == "" {
				t.Fatal(p.id)
			}
			fault := &mpStartPrepPrefixFault{target: target, occurrence: p.occurrence}
			mpStartPrepReopenFaultPool(t, f, fault)
			var out mpStartPrepResult
			var err error
			if p.step == MPStartPrepClaim {
				out, err = f.claim(t, 1, "mp-execution", 1, 0x62)
			} else {
				out, err = f.seal(t, 2, mpStartPrepCompletion{execution: "mp-execution", resultDigest: strings.Repeat("f", 64), stagedRows: 4, stagedBytes: 128}, 2, 0x65)
			}
			if !errors.Is(err, errMPMutation) || !reflect.DeepEqual(out, mpStartPrepResult{}) {
				t.Fatal(out, err)
			}
			if after := mpStartPrepReadFacts(t, f); after != before {
				t.Fatalf("prefix left partial effects: before %+v after %+v", before, after)
			}
			mpStartPrepAssertReturned(t, f)
		})
	}
}

func TestMPStartPrepBirthPrefixRollback(t *testing.T) {
	for _, id := range []stmt.ID{"mp.start.begin", "mp.start.actor_insert", "mp.start.source_insert", "mp.start.queue_insert", "mp.start.results_insert", "mp.start.lifecycle_insert"} {
		t.Run(string(id), func(t *testing.T) {
			f := mpStartPrepFixtureFor(t)
			mpStartPrepComposeFixture(t, f)
			var target string
			for _, d := range mpStartPrepDefinitions() {
				if d.ID == id {
					target = d.SQL
				}
			}
			fault := &mpStartPrepPrefixFault{target: target, occurrence: 1}
			mpStartPrepReopenFaultPool(t, f, fault)
			out, err := f.begin(t)
			if !errors.Is(err, errMPMutation) || !reflect.DeepEqual(out, mpStartPrepResult{}) {
				t.Fatal(out, err)
			}
			var preps, life, receipts int64
			for _, q := range []struct {
				sql  string
				dest *int64
			}{
				{`SELECT count(*) FROM playback_start_preparations`, &preps},
				{`SELECT count(*) FROM playback_start_preparation_lifecycle`, &life},
				{`SELECT count(*) FROM mp_start_command_receipts`, &receipts},
			} {
				if err := f.e.db.QueryRowContext(t.Context(), q.sql).Scan(q.dest); err != nil {
					t.Fatal(err)
				}
			}
			if preps != 0 || life != 0 || receipts != 0 {
				t.Fatal("prefix left partial birth", preps, life, receipts)
			}
			mpStartPrepAssertReturned(t, f)
		})
	}
}

func TestMPStartPrepUnknownCommitNeverApplied(t *testing.T) {
	for _, after := range []bool{false, true} {
		t.Run(map[bool]string{false: "before", true: "after"}[after], func(t *testing.T) {
			f := mpStartPrepFixtureFor(t)
			mpStartPrepComposeFixture(t, f)
			if _, err := f.begin(t); err != nil {
				t.Fatal(err)
			}
			if _, err := f.claim(t, 1, "mp-execution", 1, 0x62); err != nil {
				t.Fatal(err)
			}
			f.ack(t, 1)
			mpStartPrepFaultPool(t, f, &faultConnector{replyError: true, afterCommit: after})
			out, err := f.seal(t, 2, mpStartPrepCompletion{execution: "mp-execution", resultDigest: strings.Repeat("f", 64), stagedRows: 4, stagedBytes: 128}, 2, 0x65)
			if !errors.Is(err, ErrResolveRequired) || out.Outcome.State == Applied || !reflect.DeepEqual(out, mpStartPrepResult{}) {
				t.Fatal("uncertain commit exposed success", out, err)
			}
			// Observe durable state through a fresh read-only native connection,
			// never the quarantined writer pool.
			reader, _, err := sqliteconn.Open(t.Context(), f.w.effective.path, sqliteconn.Config{Role: sqliteconn.Reader, Connections: 1, CacheKiB: 1024, Synchronous: sqliteconn.Full}, sqliteconn.ExistingFile)
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			var state string
			var revision, receipts int64
			if err = reader.QueryRowContext(t.Context(), `SELECT state,revision FROM playback_start_preparation_lifecycle WHERE operation_id='mp-prep'`).Scan(&state, &revision); err != nil {
				t.Fatal(err)
			}
			if err = reader.QueryRowContext(t.Context(), `SELECT count(*) FROM mp_start_command_receipts`).Scan(&receipts); err != nil {
				t.Fatal(err)
			}
			wantState, wantRevision, wantReceipts := "preparing", int64(2), int64(1)
			if after {
				wantState, wantRevision, wantReceipts = "prepared", 3, 2
			}
			if state != wantState || revision != wantRevision || receipts != wantReceipts {
				t.Fatal("partial unknown commit", state, revision, receipts)
			}
			mpStartPrepAssertReturned(t, f)
		})
	}
}

func TestMPStartPrepCompositionStaysClosed(t *testing.T) {
	f := mpStartPrepFixtureFor(t)
	r := f.w.effective
	if out, err := f.begin(t); err == nil || !reflect.DeepEqual(out, mpStartPrepResult{}) {
		t.Fatal("uninstalled purpose ran", out, err)
	}
	mpStartPrepComposeFixture(t, f)
	old := r.mpStartPrepPlan
	r.mpStartPrepPlan = nil
	bad := *old
	bad.registration = nil
	if err := composeMPStartPrep(t.Context(), r, bad); err == nil || r.mpStartPrepPlan != nil {
		t.Fatal("unqualified profile installed", err)
	}
	r.mpStartPrepPlan = old
	for _, d := range mpStartPrepDefinitions() {
		if strings.Contains(d.SQL, "octet_length(") {
			continue
		}
		if issues := plan.LintSQL(d.SQL); len(issues) > 0 {
			t.Fatal(d.ID, issues)
		}
	}
	for _, d := range mpStartPrepDefinitions() {
		if d.Kind == stmt.Write && strings.HasPrefix(strings.TrimSpace(d.SQL), "INSERT") && (len(d.Plan.Indexes) != 0 || len(d.Plan.PrimaryKeys) != 0) {
			t.Fatal("plain INSERT carries a read seek", d.ID)
		}
	}
	// A claim without a UO birth witness never stages a preparation.
	q := f.beginInput()
	q.birth = mpStartPrepUOBirth{}
	if _, err := f.prepCommand(t, MPStartPrepBegin, f.beginIdentity(), q); err == nil {
		t.Fatal("zero UO birth admitted")
	}
	var preps int64
	if err := f.e.db.QueryRowContext(t.Context(), `SELECT count(*) FROM playback_start_preparations`).Scan(&preps); err != nil || preps != 0 {
		t.Fatal("refused birth staged a preparation", preps, err)
	}
	mpStartPrepAssertReturned(t, f)
}
