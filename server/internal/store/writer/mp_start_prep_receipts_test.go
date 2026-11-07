package writer

import (
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
	"strings"
	"testing"
)

func mpBirthDraftHeader() mpStartJobBirthHeader {
	return mpStartJobBirthHeader{birthID: [16]byte{9}, identity: mpDraftIdentity(), request: RequestDigest{3}, appliedMS: 100, scope: mpStartJobBirthScope{authority: "local", account: "account", profile: "profile", member: strings.Repeat("a", 32), profileLifetime: strings.Repeat("b", 32), installation: strings.Repeat("c", 32), root: strings.Repeat("r", 22), family: "family", device: "device", registration: 1, issuedMS: 90, keyDigest: [32]byte{4}}}
}
func TestMPPrepDraftBirthScopeDigest(t *testing.T) {
	v := mpStartPrepBirthReceipt{header: mpBirthDraftHeader(), result: mpStartPrepBirthResult{preparation: "prep", incarnation: [16]byte{4}, state: "pending", revision: 1, deadlineMS: 9000, ids: mpDraftIDs()}}
	base, e := mpStartPrepBirthDigest(v)
	if e != nil {
		t.Fatal(e)
	}
	for _, change := range []func(*mpStartPrepBirthReceipt){
		func(v *mpStartPrepBirthReceipt) { v.header.scope.member = strings.Repeat("d", 32) }, func(v *mpStartPrepBirthReceipt) { v.header.scope.profileLifetime = strings.Repeat("d", 32) }, func(v *mpStartPrepBirthReceipt) { v.header.scope.installation = strings.Repeat("d", 32) }, func(v *mpStartPrepBirthReceipt) { v.header.scope.registration++ }, func(v *mpStartPrepBirthReceipt) { v.header.scope.family = "other" }, func(v *mpStartPrepBirthReceipt) { v.header.scope.keyDigest[0]++ }, func(v *mpStartPrepBirthReceipt) { v.header.scope.issuedMS++ }, func(v *mpStartPrepBirthReceipt) { v.header.birthID[0]++ }, func(v *mpStartPrepBirthReceipt) { v.result.ids.session = "other" }, func(v *mpStartPrepBirthReceipt) { v.result.incarnation[0]++ },
	} {
		q := v
		change(&q)
		d, e := mpStartPrepBirthDigest(q)
		if e != nil || d == base {
			t.Fatal("scope/result not bound", e)
		}
	}
	q := v
	q.result.incarnation = q.header.identity.Incarnation
	if _, e = mpStartPrepBirthDigest(q); !errors.Is(e, ErrInvalid) {
		t.Fatal("shared birth/root incarnation", e)
	}
	q = v
	q.header.identity.Sequence = 2
	if _, e = mpStartPrepBirthDigest(q); !errors.Is(e, ErrInvalid) {
		t.Fatal("creation on retirement sequence", e)
	}
	j := mpTranscodeBirthReceipt{header: v.header, result: mpTranscodeBirthResult{job: "job", incarnation: [16]byte{5}, state: "queued", revision: 1, nextAttemptMS: 100, sourceRevision: mpDraftRecipe().sourceRevision}}
	d, e := mpTranscodeBirthDigest(j)
	if e != nil {
		t.Fatal(e)
	}
	j.result.nextAttemptMS++
	d2, e := mpTranscodeBirthDigest(j)
	if e != nil || d == d2 {
		t.Fatal("next attempt not bound", e)
	}
}
func TestMPPrepDraftExactFMapping(t *testing.T) {
	for _, d := range mpPrepReceiptDefinitions() {
		for _, bad := range []string{"command_history", "birth_results", "birth_owners", "response_json"} {
			if strings.Contains(d.SQL, bad) {
				t.Fatal(d.ID, bad)
			}
		}
		if strings.HasSuffix(string(d.ID), "receipt_insert") && !strings.Contains(d.SQL, "RETURNING variable_bytes") {
			t.Fatal(d.ID)
		}
		if strings.HasSuffix(string(d.ID), "receipt_load") && (strings.Contains(d.SQL, " JOIN ") || strings.Contains(d.SQL, "prepared_media_jobs")) {
			t.Fatal("mutable replay", d.ID)
		}
	}
	if mpJobResultPurpose(MPTranscodeRetire) {
		t.Fatal("745 belongs to version codec")
	}
	q := mpTranscodeRetireInput{version: mpTranscodeVersionFence{version: "v", cleanupRequestID: "first"}}
	d, e := mpPrepDraftRequestDigest(MPTranscodeRetire, mpDraftIdentity(), q)
	if e != nil {
		t.Fatal(e)
	}
	q.version.cleanupRequestID = "second"
	d2, e := mpPrepDraftRequestDigest(MPTranscodeRetire, mpDraftIdentity(), q)
	if e != nil || d == d2 {
		t.Fatal("cleanup request not bound", e)
	}
	for _, d := range mpTranscodeDefinitions() {
		if strings.Contains(d.SQL, "UPDATE mp_job_custody SET cleaned=0") || strings.Contains(d.SQL, "SET state='deleted'") {
			t.Fatal("job owns artifact deletion", d.ID)
		}
	}
}
func TestMPStartPrepNativeOriginalReceipt(t *testing.T) {
	n := mpNativeFixture(t)
	r, e := applyMPStartPrepBegin(t.Context(), n.purpose(MPStartPrepBegin), mpNativeStart())
	if e != nil {
		t.Fatal(e)
	}
	b := mpStartPrepBirthReceipt{header: mpBirthDraftHeader(), result: mpStartPrepBirthResult{preparation: r.preparation, incarnation: r.rootIncarnation, state: r.state, revision: r.revision, deadlineMS: r.deadlineMS, ids: r.ids}}
	b.header.digest, e = mpStartPrepBirthDigest(b)
	if e != nil {
		t.Fatal(e)
	}
	debt, e := storeMPStartPrepBirthReceipt(t.Context(), n.purpose(MPStartPrepBegin), b)
	if e != nil || debt.rows != 1 || debt.variableBytes <= 0 || debt.rootControlAfter-debt.rootControlBefore != 16 {
		t.Fatal(debt, e)
	}
	var last int64
	if e = n.conn.QueryRowContext(t.Context(), "SELECT mp_command_last_sequence FROM playback_start_preparations WHERE id='prep'").Scan(&last); e != nil || last != 0 {
		t.Fatal("birth consumed root sequence", last, e)
	}
	r, e = applyMPStartPrepClaim(t.Context(), n.purpose(MPStartPrepClaim), mpNativeStartFence(r), "execution")
	if e != nil {
		t.Fatal(e)
	}
	h := mpPrepReceiptHeader{kind: MPStartPrepClaim, identity: mpDraftIdentity(), request: RequestDigest{4}, appliedMS: 200}
	h.identity.Incarnation = r.rootIncarnation
	v, e := prepareMPStartPrepReceipt(t.Context(), n.purpose(h.kind), h, r)
	if e != nil || !v.result.sealed {
		t.Fatal(v, e)
	}
	bytes, e := storeMPStartPrepReceipt(t.Context(), n.purpose(h.kind), mpPrepOwnerBefore{incarnation: h.identity.Incarnation}, v)
	if e != nil {
		t.Fatal(e)
	}
	var actual int64
	if e = n.conn.QueryRowContext(t.Context(), "SELECT mp_receipt_variable_bytes FROM playback_start_preparations WHERE id='prep'").Scan(&actual); e != nil || actual != bytes {
		t.Fatal(actual, bytes, e)
	}
	n.exec("UPDATE playback_start_preparation_lifecycle SET revision=revision+1,state='retired'; UPDATE inventory_objects SET revision='drifted'")
	got, e := loadMPStartPrepReceipt(t.Context(), n.purpose(h.kind), "prep", v.header)
	if e != nil || got != v {
		t.Fatal("original receipt lost", got, e)
	}
	ref, e := lookupMPStartPrepBirth(t.Context(), n.purpose(MPStartPrepBegin), b.header.scope)
	if e != nil || ref.header != b.header {
		t.Fatal(ref, e)
	}
	original, e := loadMPStartPrepBirthReceipt(t.Context(), n.purpose(MPStartPrepBegin), ref.header)
	if e != nil || original != b {
		t.Fatal("birth followed mutable head", original, e)
	}
	n.exec("UPDATE mp_start_command_receipts SET result_session_id='tampered'")
	got, e = loadMPStartPrepReceipt(t.Context(), n.purpose(h.kind), "prep", v.header)
	if !errors.Is(e, ErrResolveRequired) || got != (mpStartPrepReceipt{}) {
		t.Fatal("tampered original", got, e)
	}
}
func TestMPTranscodeNativeOriginalReceipt(t *testing.T) {
	n := mpNativeFixture(t)
	r, e := applyMPTranscodeCreate(t.Context(), n.purpose(MPTranscodeCreate), mpNativeJob())
	if e != nil {
		t.Fatal(e)
	}
	b := mpTranscodeBirthReceipt{header: mpBirthDraftHeader(), result: mpTranscodeBirthResult{job: r.job, incarnation: r.rootIncarnation, state: r.state, revision: r.revision, nextAttemptMS: r.nextAttemptMS, sourceRevision: r.sourceRevision}}
	b.header.digest, e = mpTranscodeBirthDigest(b)
	if e != nil {
		t.Fatal(e)
	}
	debt, e := storeMPTranscodeBirthReceipt(t.Context(), n.purpose(MPTranscodeCreate), b)
	if e != nil || debt.rows != 1 || debt.variableBytes <= 0 {
		t.Fatal(debt, e)
	}
	r, e = applyMPTranscodeClaim(t.Context(), n.purpose(MPTranscodeClaim), mpNativeJobFence(r), 8, 5000, "encode")
	if e != nil {
		t.Fatal(e)
	}
	h := mpPrepReceiptHeader{kind: MPTranscodeClaim, identity: mpDraftIdentity(), request: RequestDigest{4}, appliedMS: 200}
	h.identity.Incarnation = r.rootIncarnation
	v, e := prepareMPTranscodeReceipt(t.Context(), n.purpose(h.kind), h, r)
	if e != nil || v.result.phase != "probing source" || v.result.sourceRevision != mpDraftRecipe().sourceRevision {
		t.Fatal(v, e)
	}
	bytes, e := storeMPTranscodeReceipt(t.Context(), n.purpose(h.kind), mpPrepOwnerBefore{incarnation: h.identity.Incarnation}, v)
	if e != nil {
		t.Fatal(e)
	}
	var actual int64
	if e = n.conn.QueryRowContext(t.Context(), "SELECT mp_receipt_variable_bytes FROM prepared_media_jobs WHERE id='job'").Scan(&actual); e != nil || actual != bytes {
		t.Fatal(actual, bytes, e)
	}
	n.exec("UPDATE prepared_media_jobs SET generation=generation+1,job_lease_owner=9,job_lease_generation=job_lease_generation+1,revision=revision+1,phase='drifted',error_code='changed'")
	got, e := loadMPTranscodeReceipt(t.Context(), n.purpose(h.kind), "job", v.header)
	if e != nil || got != v {
		t.Fatal(got, e)
	}
	ref, e := lookupMPTranscodeBirth(t.Context(), n.purpose(MPTranscodeCreate), b.header.scope)
	if e != nil {
		t.Fatal(e)
	}
	original, e := loadMPTranscodeBirthReceipt(t.Context(), n.purpose(MPTranscodeCreate), ref.header)
	if e != nil || original != b {
		t.Fatal(original, e)
	}
	wrong := v.header
	wrong.request = RequestDigest{5}
	got, e = loadMPTranscodeReceipt(t.Context(), n.purpose(h.kind), "job", wrong)
	if !errors.Is(e, ErrResolveRequired) || got != (mpTranscodeReceipt{}) {
		t.Fatal(got, e)
	}
}
func TestMPStartJobNativeFSQL(t *testing.T) {
	w := mpNativeFExecutor(t)
	for _, d := range append(mpPrepReceiptDefinitions(), mpTranscodeDefinitions()...) {
		args := make([]any, len(d.Params))
		rows, e := w.db.QueryContext(t.Context(), "EXPLAIN "+d.SQL, args...)
		if e != nil {
			t.Fatal(d.ID, e)
		}
		for rows.Next() {
		}
		e = errors.Join(rows.Err(), rows.Close())
		if e != nil {
			t.Fatal(d.ID, e)
		}
	}
}

// Bound the concrete scalar args against HN's actual parameter contracts,
// including SQL NULL vs integer zero and immutable object size vs job bytes.
func TestMPPrepDraftScalarBindings(t *testing.T) {
	registry, e := stmttest.New(t, mpPrepReceiptDefinitions())
	if e != nil {
		t.Fatal(e)
	}
	bind := func(id stmt.ID, args []any) {
		t.Helper()
		s, e := registry.Lookup(id)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = s.Bind(args...); e != nil {
			t.Fatal(id, e)
		}
	}
	h := mpPrepReceiptHeader{identity: mpDraftIdentity(), kind: MPStartPrepClaim, request: RequestDigest{3}, appliedMS: 100, resultDigest: ResultDigest{4}}
	r := mpStartPrepEffect{preparation: "prep", rootIncarnation: h.identity.Incarnation, state: "preparing", execution: "exec", sealed: true, revision: 2, deadlineMS: 9000, ids: mpDraftIDs(), cleanupPending: true}
	bind("mp.start.receipt_insert", mpStartReceiptArgs(mpStartPrepReceipt{header: h, result: r}))
	h.kind = MPTranscodeClaim
	j := mpTranscodeEffect{job: "job", rootIncarnation: h.identity.Incarnation, state: "running", phase: "probe", sourceRevision: mpDraftRecipe().sourceRevision, revision: 2, nextAttemptMS: -1, cleanupPending: true}
	bind("mp.transcode.receipt_insert", mpJobReceiptArgs(mpTranscodeReceipt{header: h, result: j}))
	args := mpJobReceiptArgs(mpTranscodeReceipt{header: h, result: j})
	if args[18] != nil || args[28] != nil || args[29] != nil {
		t.Fatal("absent SQL values were fabricated", args)
	}
	j.version = "version"
	j.versionIncarnation = [16]byte{7}
	j.versionState = "published"
	j.versionRevision = 1
	j.objectSize = 123
	j.size = 456
	j.digest = strings.Repeat("d", 64)
	j.nextAttemptMS = 0
	args = mpJobReceiptArgs(mpTranscodeReceipt{header: h, result: j})
	bind("mp.transcode.receipt_insert", args)
	if args[18] != int64(0) || args[29] != int64(123) || args[19] != int64(456) {
		t.Fatal("object/job bytes conflated", args)
	}
	b := mpBirthDraftHeader()
	bind("mp.start.birth_insert", append(b.args(MPStartPrepBegin), "session", "media", int64(1), "snapshot", "prep", []byte{4}, "pending", int64(1), int64(9000)))
	bind("mp.transcode.birth_insert", append(b.args(MPTranscodeCreate), int64(100), "job", []byte{5}, int64(1), "queued", mpDraftRecipe().sourceRevision))
}
func TestMPPrepDraftRejectVersionReconcile(t *testing.T) {
	turn := &commandTurn{transaction: &transaction{ctx: t.Context(), descriptor: Descriptor{ID: MPTranscodeReconcile}}}
	q := mpTranscodeCleanup{version: mpTranscodeVersionFence{cleanupRequestID: "exact"}}
	out, e := applyMPTranscodeReconcile(t.Context(), turn, mpTranscodeFence{}, q)
	if !errors.Is(e, ErrInvalid) || out != (mpTranscodeEffect{}) {
		t.Fatal(out, e)
	}
	out745, e := applyMPTranscodeRetire(t.Context(), turn, mpTranscodeFence{}, mpTranscodeVersionFence{})
	if !errors.Is(e, ErrInvalid) || out745 != (mpTranscodeRetirementEffect{}) {
		t.Fatal(out745, e)
	}
}
