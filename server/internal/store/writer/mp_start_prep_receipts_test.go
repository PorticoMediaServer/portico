package writer

import (
	"bytes"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"strings"
	"testing"
)

func mpDraftUserBirth() mpStartJobUserBirth {
	return mpStartJobUserBirth{root: strings.Repeat("r", 22), member: strings.Repeat("a", 32), profileLifetime: strings.Repeat("b", 32)}
}
func TestMPPrepDraftUserBirthTuple(t *testing.T) {
	v := mpDraftUserBirth()
	if !v.valid() {
		t.Fatal(v)
	}
	for _, change := range []func(*mpStartJobUserBirth){func(v *mpStartJobUserBirth) { v.root = "" }, func(v *mpStartJobUserBirth) { v.member = "" }, func(v *mpStartJobUserBirth) { v.profileLifetime = "" }} {
		q := v
		change(&q)
		if q.valid() {
			t.Fatal(q)
		}
	}
}
func TestMPPrepDraftExactFMapping(t *testing.T) {
	for _, d := range mpPrepReceiptDefinitions() {
		for _, bad := range []string{"command_history", "birth_results", "birth_owners", "response_json"} {
			if strings.Contains(d.SQL, bad) {
				t.Fatal(d.ID, bad)
			}
		}
		if strings.HasSuffix(string(d.ID), "worker.insert") && !strings.Contains(d.SQL, "RETURNING variable_bytes") {
			t.Fatal(d.ID)
		}
		if strings.HasSuffix(string(d.ID), "worker.original") && (strings.Contains(d.SQL, " JOIN ") || strings.Contains(d.SQL, "prepared_media_jobs")) {
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

func TestMPPrepDraftWorkerPayloadCatalog(t *testing.T) {
	for _, defs := range [][]stmt.Definition{mpStartWorkerPayloadDefinitions(), mpJobWorkerPayloadDefinitions()} {
		if _, e := stmt.Describe(defs); e != nil {
			t.Fatal(e)
		}
		for _, d := range defs {
			if strings.Count(d.SQL, "?") != len(d.Params) {
				t.Fatal(d.ID)
			}
			if strings.Contains(string(d.ID), "advance") {
				for _, s := range []string{"mp_owner_revision=?", "mp_command_acknowledged_sequence=?", "mp_receipt_variable_bytes=?", "mp_receipt_reclaim_due_ms", "mp_owner_revision=mp_owner_revision+1"} {
					if !strings.Contains(d.SQL, s) {
						t.Fatal(d.ID, s)
					}
				}
			}
		}
	}
	for _, kind := range []CommandID{700, 706, 740, 745, 749} {
		v := mpWMediaPayload{header: mpPrepReceiptHeader{kind: kind}}
		if _, e := storeMPStartWorkerPayload(t.Context(), nil, "x", mpMediaWorkerBefore{}, mpMediaWorkerAdmission{}, v); e == nil {
			t.Fatal(kind)
		}
		if _, e := storeMPJobWorkerPayload(t.Context(), nil, "x", mpMediaWorkerBefore{}, mpMediaWorkerAdmission{}, v); e == nil {
			t.Fatal(kind)
		}
	}
}

// Runner-only storage contract: opaque bytes stand in for W's already encoded original.
// Digest/decode qualification remains W-owned.
func TestMPStartJobNativeOriginalPayload(t *testing.T) {
	for _, job := range []bool{false, true} {
		t.Run(map[bool]string{false: "start", true: "job"}[job], func(t *testing.T) {
			n := mpNativeFixture(t)
			owner, table, kind := "prep", "playback_start_preparations", MPStartPrepClaim
			var inc [16]byte
			if job {
				r, e := applyMPTranscodeCreate(t.Context(), n.purpose(MPTranscodeCreate), mpNativeJob())
				if e != nil {
					t.Fatal(e)
				}
				inc = r.rootIncarnation
				claimed, e := applyMPTranscodeClaim(t.Context(), n.purpose(MPTranscodeClaim), mpNativeJobFence(r), 8, 5000, "encode")
				if e != nil {
					t.Fatal(e)
				}
				prepared, e := prepareMPTranscodeReceipt(t.Context(), n.purpose(MPTranscodeClaim), mpPrepReceiptHeader{identity: CommandIdentity{Incarnation: inc, Sequence: 1, OperationID: [16]byte{2}}, kind: MPTranscodeClaim, request: RequestDigest{3}, appliedMS: 200}, claimed)
				if e != nil || prepared.result.phase != "probing source" || prepared.result.sourceRevision != mpDraftRecipe().sourceRevision {
					t.Fatal(prepared, e)
				}
				owner, table, kind = "job", "prepared_media_jobs", MPTranscodeClaim
			} else {
				r, e := applyMPStartPrepBegin(t.Context(), n.purpose(MPStartPrepBegin), mpNativeStart())
				if e != nil {
					t.Fatal(e)
				}
				inc = r.rootIncarnation
				claimed, e := applyMPStartPrepClaim(t.Context(), n.purpose(MPStartPrepClaim), mpNativeStartFence(r), "execution")
				if e != nil {
					t.Fatal(e)
				}
				prepared, e := prepareMPStartPrepReceipt(t.Context(), n.purpose(MPStartPrepClaim), mpPrepReceiptHeader{identity: CommandIdentity{Incarnation: inc, Sequence: 1, OperationID: [16]byte{2}}, kind: MPStartPrepClaim, request: RequestDigest{3}, appliedMS: 200}, claimed)
				if e != nil || !prepared.result.sealed {
					t.Fatal(prepared, e)
				}
			}
			var last, ack, rows, debt, revision int64
			var root, member, profile string
			e := n.conn.QueryRowContext(t.Context(), "SELECT mp_command_last_sequence,mp_command_acknowledged_sequence,mp_receipt_rows,mp_receipt_variable_bytes,mp_owner_revision,mp_user_root_id,mp_owner_member_incarnation,mp_owner_profile_lifetime FROM "+table+" WHERE id=?", owner).Scan(&last, &ack, &rows, &debt, &revision, &root, &member, &profile)
			if e != nil || last != 0 || ack != 0 || rows != 0 || debt != 0 || revision != 1 || (mpStartJobUserBirth{root, member, profile}) != mpDraftUserBirth() {
				t.Fatal("independent root/lifetime", e)
			}
			h := mpPrepReceiptHeader{identity: CommandIdentity{Incarnation: inc, Sequence: 1, OperationID: [16]byte{2}}, kind: kind, request: RequestDigest{3}, resultDigest: ResultDigest{4}, appliedMS: 200}
			v := mpWMediaPayload{header: h, version: 1, count: 1, payload: []byte("original W bytes")}
			o := mpMediaWorkerBefore{incarnation: inc, revision: 1}
			a := mpMediaWorkerAdmission{maxRows: 1, maxBytes: 1000, reclaimDueMS: 300}
			store, load, preflight := storeMPStartWorkerPayload, loadMPStartWorkerPayload, mpStartWorkerPayloadBytes
			if job {
				store, load, preflight = storeMPJobWorkerPayload, loadMPJobWorkerPayload, mpJobWorkerPayloadBytes
			}
			debt, e = store(t.Context(), n.purpose(kind), owner, o, a, v)
			if e != nil {
				t.Fatal(e)
			}
			n.exec("UPDATE " + table + " SET mp_owner_revision=mp_owner_revision+1")
			actual, e := preflight(t.Context(), n.purpose(kind), owner, h)
			if e != nil || actual != debt {
				t.Fatal(actual, debt, e)
			}
			got, e := load(t.Context(), n.purpose(kind), owner, h, actual)
			if e != nil || got.header != h || !bytes.Equal(got.payload, v.payload) {
				t.Fatal("original lost", e)
			}
			got, e = load(t.Context(), n.purpose(kind), owner, h, actual-1)
			if !errors.Is(e, ErrResolveRequired) || got.payload != nil {
				t.Fatal("unadmitted copy", e)
			}
			h.request[0]++
			got, e = load(t.Context(), n.purpose(kind), owner, h, actual)
			if !errors.Is(e, ErrResolveRequired) || got.payload != nil {
				t.Fatal("wrong identity", e)
			}
		})
	}
}

func TestMPStartJobNativeFSQL(t *testing.T) {
	w := mpNativeFExecutor(t)
	for _, d := range append(mpPrepReceiptDefinitions(), mpTranscodeDefinitions()...) {
		rows, e := w.db.QueryContext(t.Context(), "EXPLAIN "+d.SQL, make([]any, len(d.Params))...)
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
func TestMPPrepDraftWorkerAdmission(t *testing.T) {
	h := mpPrepReceiptHeader{identity: CommandIdentity{Incarnation: [16]byte{1}, Sequence: 1, OperationID: [16]byte{2}}, kind: 701, request: RequestDigest{3}, resultDigest: ResultDigest{4}, appliedMS: 100}
	v := mpWMediaPayload{header: h, version: 1, count: 1, payload: []byte("W bytes")}
	o := mpMediaWorkerBefore{incarnation: h.identity.Incarnation, revision: 1}
	a := mpMediaWorkerAdmission{maxRows: 1, maxBytes: 102, reclaimDueMS: 100}
	if n, e := o.admits("x", v, a); e == nil || n != 0 {
		t.Fatal("quota", n, e)
	}
	a.maxBytes = 1000
	for _, change := range []func(*mpMediaWorkerBefore){func(o *mpMediaWorkerBefore) { o.revision = 0 }, func(o *mpMediaWorkerBefore) { o.incarnation[0]++ }, func(o *mpMediaWorkerBefore) { o.last = 1 }, func(o *mpMediaWorkerBefore) { o.ack = 1 }, func(o *mpMediaWorkerBefore) { o.rows = 1 }, func(o *mpMediaWorkerBefore) { o.variableBytes = 1 }} {
		q := o
		change(&q)
		if _, e := q.admits("x", v, a); e == nil {
			t.Fatal(q)
		}
	}
	if _, e := o.admits("x", v, a); e != nil {
		t.Fatal(e)
	}
	a.reclaimDueMS = 99
	if _, e := o.admits("x", v, a); e == nil {
		t.Fatal("past due")
	}
}
