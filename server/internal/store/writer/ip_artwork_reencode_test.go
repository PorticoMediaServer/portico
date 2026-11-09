package writer

import (
	"context"
	"errors"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
)

// ipArtworkReencodeSpec is the new three-variant generation. The accepted
// source identity (digest, mime, dimensions) is unchanged; only the encoded
// variants, large edge and encoder generation advance.
func ipArtworkReencodeSpec() ipArtworkMaterialPublishSpec {
	spec := ipArtworkUploadSpec()
	spec.intentID = "job-reencode-poster"
	spec.expectedSlots = 3
	spec.largeEdge = 1280
	spec.encoderRevision = 4
	spec.intentGeneration = 1
	spec.intentRevision = 2
	return spec
}

func ipArtworkReencodeSlots() []ipArtworkMaterialSlot {
	return []ipArtworkMaterialSlot{
		{slot: 0, digest: ipArtworkMaterialHex(5), mime: "image/jpeg", width: 480, height: 720, bytes: 38 << 10},
		{slot: 1, digest: ipArtworkMaterialHex(6), mime: "image/jpeg", width: 1280, height: 720, bytes: 118 << 10},
		{slot: 2, digest: ipArtworkMaterialHex(7), mime: "image/png", width: 800, height: 1200, bytes: 92 << 10},
	}
}

func ipArtworkReencodePreviousFor() ipArtworkReencodePrevious {
	return ipArtworkReencodePrevious{
		small: ipArtworkMaterialHex(2), large: ipArtworkMaterialHex(3), ambient: ipArtworkMaterialHex(4),
		mime: "image/jpeg", width: 3000, height: 2000, largeEdge: 1920,
		encoderRevision: 3, originalBytes: 2 << 20, revision: 1,
	}
}

func ipArtworkReencodeOwner() ipArtworkMaterialOwner {
	owner := ipArtworkUploadOwner()
	owner.revision = 2
	owner.sourceRevision = 2
	return owner
}

// ipArtworkReencodePrepared runs one real owner upload, then advances the SAME
// subject job to the entity's new artwork revision and seeds its installed
// three-variant intent. Reencode consumes that exact intent.
func ipArtworkReencodePrepared(t *testing.T, w *executor, r *ipPolicyRegistry, p *usOwnerAuthorityProof) (ipArtworkMaterialOwner, ipArtworkMaterialPublishSpec, ipArtworkReencodePrevious, []ipArtworkMaterialSlot) {
	t.Helper()
	upload := ipArtworkUploadSpec()
	upSlots := ipArtworkUploadSlots()
	cmd := ipArtworkUploadCommandFor(t, 1, 2, ipArtworkUploadOwner(), upload, upSlots,
		ipArtworkSelectionBefore{}, ipArtworkSelectionUploadFacts(upload.sourceDigest))
	if out, turn, tx, _ := ipArtworkUploadApply(t, w, r, p, cmd); true {
		if out.Outcome.State != Applied {
			t.Fatal(out)
		}
		ipPolicyCommitTurn(t, turn, tx)
	}
	owner := ipArtworkSelectionAdvance(t, w, ipArtworkUploadOwner(), 1)
	spec := ipArtworkReencodeSpec()
	ipPolicyExec(t, w, `INSERT INTO artwork_material_intents(id,owner_id,owner_incarnation,generation,revision,source_digest,accepted_mime,accepted_width,accepted_height,large_edge,encoder_revision,state,expected_slots,staged_slots) VALUES(?,901,X'01000000000000000000000000000000',1,2,?,'image/jpeg',3000,2000,1280,4,'installed',3,3)`,
		spec.intentID, spec.sourceDigest)
	for _, slot := range ipArtworkReencodeSlots() {
		ipPolicyExec(t, w, `INSERT INTO artwork_material_slots(intent_id,slot,digest,mime,width,height,bytes,outlier) VALUES(?,?,?,?,?,?,?,0)`,
			spec.intentID, slot.slot, slot.digest, slot.mime, slot.width, slot.height, slot.bytes)
	}
	return owner, spec, ipArtworkReencodePreviousFor(), ipArtworkReencodeSlots()
}

func ipArtworkReencodeCommandFor(t *testing.T, sequence int64, operation byte, owner ipArtworkMaterialOwner,
	spec ipArtworkMaterialPublishSpec, previous ipArtworkReencodePrevious,
	slots []ipArtworkMaterialSlot) *ipArtworkReencodeCommand {
	t.Helper()
	c, err := newIPArtworkReencodeCommand(CommandIdentity{
		Incarnation: owner.incarnation, Sequence: sequence, OperationID: [16]byte{operation},
	}, ipArtworkReencodeRequest{owner: owner, spec: spec, previous: previous, slots: slots})
	if err != nil {
		t.Fatal("reencode", err)
	}
	return c
}

func ipArtworkReencodeApply(t *testing.T, w *executor, r *ipPolicyRegistry, p *usOwnerAuthorityProof,
	c *ipArtworkReencodeCommand) (ipArtworkReencodeCommandResult, *commandTurn, *transaction, ipPolicyBudget) {
	t.Helper()
	turn, tx, b := ipPolicyTurn(t, w, r, IPArtworkReencode)
	out, err := applyIPArtworkReencode(t.Context(), turn, p, t.Context(), b, c)
	if err != nil {
		t.Fatal("reencode", out, err)
	}
	return out, turn, tx, b
}

func ipArtworkReencodeExpect(t *testing.T, w *executor, want int64, query string, args ...any) {
	t.Helper()
	if got := ipPolicyQueryInt(t, w, query, args...); got != want {
		t.Fatal(query, got, want)
	}
}

// The source's encoded variants and encoder generation advance in one CAS; the
// retained original, the source digest, the owner selection and every history
// pin stay exactly as they were.
func TestIPArtworkReencodeReplacesVariantsWithoutReferenceFanout(t *testing.T) {
	w, r, p := ipArtworkUploadFixture(t)
	owner, spec, previous, slots := ipArtworkReencodePrepared(t, w, r, p)
	total := ipArtworkMaterialSlotBytes(slots)
	cmd := ipArtworkReencodeCommandFor(t, 2, 3, owner, spec, previous, slots)
	out, turn, tx, _ := ipArtworkReencodeApply(t, w, r, p, cmd)
	want := ipArtworkReencodeResult{
		IntentID: spec.intentID, SourceDigest: spec.sourceDigest, SourceRevision: 2,
		Slots: 3, Bytes: total, OriginalBytes: 2 << 20, EncoderRevision: 4,
		PreviousRevision: 1, AppliedMS: w.now().UnixMilli(),
	}
	if out.Outcome.State != Applied || out.Outcome.Replayed || out.Value != want ||
		out.Digest == (ResultDigest{}) {
		t.Fatal(out)
	}
	ipPolicyCommitTurn(t, turn, tx)
	originalReencode := out

	ipArtworkReencodeExpect(t, w, 1,
		`SELECT count(*) FROM artwork_sources WHERE digest=? AND small_blob=? AND large_blob=? AND ambient_blob=? AND large_edge=1280 AND encoder_revision=4 AND revision=2 AND original_bytes=? AND retiring=0`,
		spec.sourceDigest, slots[0].digest, slots[1].digest, slots[2].digest, int64(2<<20))
	// Old encoded variants remain charged and ready; nothing is deleted.
	for n := 0; n < 3; n++ {
		ipArtworkReencodeExpect(t, w, 1,
			`SELECT count(*) FROM artwork_blobs WHERE digest=? AND status='ready' AND deletion_token IS NULL AND storage_revision=1`,
			ipArtworkMaterialHex(n+2))
	}
	ipArtworkReencodeExpect(t, w, 6, `SELECT count(*) FROM artwork_blobs`)
	// No reference fanout: the owner selection and its history pin are unchanged.
	ipArtworkReencodeExpect(t, w, 1,
		`SELECT count(*) FROM artwork_selections WHERE choice='locked' AND digest=? AND revision=1`, spec.sourceDigest)
	ipArtworkReencodeExpect(t, w, 1, `SELECT count(*) FROM metadata_owner_history`)
	ipArtworkReencodeExpect(t, w, 1, `SELECT count(*) FROM metadata_owner_history_artwork WHERE digest=?`, spec.sourceDigest)
	ipArtworkReencodeExpect(t, w, 2, `SELECT artwork_revision FROM catalog_entities WHERE id=778`)
	ipArtworkReencodeExpect(t, w, 2, `SELECT last_command_sequence FROM artwork_subject_jobs WHERE id=901`)
	ipArtworkReencodeExpect(t, w, 3, `SELECT job_revision FROM artwork_subject_jobs WHERE id=901`)
	ipArtworkReencodeExpect(t, w, 1,
		`SELECT count(*) FROM artwork_command_receipts WHERE command_kind='reencode' AND accepted_revision=2 AND storage_revision=2 AND result_count=3 AND result_bytes=? AND selected_digest=? AND material_intent_id=? AND result_complete=1`,
		total, spec.sourceDigest, spec.intentID)
	ipArtworkReencodeExpect(t, w, 3, `SELECT revision FROM artwork_material_intents WHERE id=?`, spec.intentID)
	ipArtworkReencodeExpect(t, w, 1, `SELECT CASE state WHEN 'published' THEN 1 ELSE 0 END FROM artwork_material_intents WHERE id=?`, spec.intentID)

	// The original result replays exactly after every fresh witness moves.
	ipPolicyExec(t, w, `UPDATE artwork_sources SET revision=9 WHERE digest=?`, spec.sourceDigest)
	ipPolicyExec(t, w, `UPDATE metadata_runtime_settings SET artwork_language='textless',artwork_policy_revision=9`)
	ipPolicyExec(t, w, `UPDATE artwork_subject_jobs SET job_revision=99,job_lease_owner=9 WHERE id=901`)
	ipPolicyExec(t, w, `UPDATE catalog_entities SET artwork_revision=9 WHERE id=778`)
	turn, tx, b := ipPolicyTurn(t, w, r, IPArtworkReencode)
	replay, err := applyIPArtworkReencode(t.Context(), turn, p, t.Context(), b, cmd)
	if err != nil || !replay.Outcome.Replayed || replay.Digest != originalReencode.Digest ||
		replay.Value != originalReencode.Value {
		t.Fatal("source change altered the original reencode", replay, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)

	// At or below ACK is Retired while every source/blob row stays physical.
	ipPolicyExec(t, w, `UPDATE artwork_subject_jobs SET acknowledged_command_sequence=2 WHERE id=901`)
	turn, tx, b = ipPolicyTurn(t, w, r, IPArtworkReencode)
	retired, err := applyIPArtworkReencode(t.Context(), turn, p, t.Context(), b, cmd)
	if err != nil || retired.Outcome.State != ResultRetired ||
		retired.Value != (ipArtworkReencodeResult{}) || retired.Digest != (ResultDigest{}) {
		t.Fatal("retired reencode served its result", retired, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
	ipArtworkReencodeExpect(t, w, 1, `SELECT count(*) FROM artwork_sources WHERE digest=?`, spec.sourceDigest)
}

// A stale encoder generation, source revision, original-byte custody, intent or
// manifest is refused before any blob or source byte.
func TestIPArtworkReencodeFences(t *testing.T) {
	cases := []struct {
		name   string
		sql    []string
		mutate func(*ipArtworkReencodeRequest)
	}{
		{"stale_entity_revision", []string{`UPDATE catalog_entities SET artwork_revision=3 WHERE id=778`}, nil},
		{"retired_entity", []string{`UPDATE catalog_entities SET retired=1 WHERE id=778`}, nil},
		{"stale_language", []string{`UPDATE metadata_runtime_settings SET artwork_language='textless'`}, nil},
		{"stale_policy", []string{`UPDATE metadata_runtime_settings SET artwork_policy_revision=2`}, nil},
		{"stale_owner_lease", []string{`UPDATE artwork_subject_jobs SET job_lease_owner=8 WHERE id=901`}, nil},
		{"expired_owner_lease", []string{`UPDATE artwork_subject_jobs SET job_lease_expires_ms=1 WHERE id=901`}, nil},
		{"stale_owner_revision", nil, func(q *ipArtworkReencodeRequest) { q.owner.revision = 3 }},
		{"intent_prepared", []string{`UPDATE artwork_material_intents SET state='prepared',staged_slots=0,revision=1 WHERE id='job-reencode-poster'`}, nil},
		{"intent_published", []string{`UPDATE artwork_material_intents SET state='published' WHERE id='job-reencode-poster'`}, nil},
		{"manifest_slot_changed", []string{`UPDATE artwork_material_slots SET digest=? WHERE intent_id='job-reencode-poster' AND slot=1`}, nil},
		{"manifest_slot_missing", []string{`DELETE FROM artwork_material_slots WHERE intent_id='job-reencode-poster' AND slot=2`}, nil},
		{"source_retiring", []string{`UPDATE artwork_sources SET retiring=1,retirement_token=X'0f000000000000000000000000000000' WHERE digest=?`}, nil},
		{"source_revision_changed", []string{`UPDATE artwork_sources SET revision=2 WHERE digest=?`}, nil},
		{"source_encoder_changed", []string{`UPDATE artwork_sources SET encoder_revision=4 WHERE digest=?`}, nil},
		{"source_original_changed", []string{`UPDATE artwork_sources SET original_bytes=0 WHERE digest=?`}, nil},
		{"stale_generation", nil, func(q *ipArtworkReencodeRequest) { q.previous.encoderRevision = 2 }},
		{"stale_revision", nil, func(q *ipArtworkReencodeRequest) { q.previous.revision = 9 }},
		{"wrong_original", nil, func(q *ipArtworkReencodeRequest) { q.previous.originalBytes = 0 }},
		{"wrong_old_blob", nil, func(q *ipArtworkReencodeRequest) { q.previous.large = ipArtworkMaterialHex(9) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w, r, p := ipArtworkUploadFixture(t)
			owner, spec, previous, slots := ipArtworkReencodePrepared(t, w, r, p)
			for _, query := range tc.sql {
				if strings.Contains(query, "?") {
					ipPolicyExec(t, w, query, spec.sourceDigest)
				} else {
					ipPolicyExec(t, w, query)
				}
			}
			cmd := ipArtworkReencodeCommandFor(t, 2, 3, owner, spec, previous, slots)
			if tc.mutate != nil {
				tc.mutate(&cmd.request)
				digest, err := canonicalRequestDigest(IPArtworkReencode, 1, cmd.identity, cmd.request)
				if err != nil {
					t.Fatal(err)
				}
				cmd.digest = digest
			}
			turn, tx, b := ipPolicyTurn(t, w, r, IPArtworkReencode)
			out, err := applyIPArtworkReencode(t.Context(), turn, p, t.Context(), b, cmd)
			if out != (ipArtworkReencodeCommandResult{}) || !errors.Is(err, ErrConflict) {
				t.Fatal("stale reencode admitted", out, err)
			}
			ipPolicyRollbackTurn(t, turn, tx)
			ipArtworkReencodeExpect(t, w, 1, `SELECT count(*) FROM artwork_sources WHERE digest=? AND small_blob=? AND revision=1`, spec.sourceDigest, previous.small)
			ipArtworkReencodeExpect(t, w, 3, `SELECT count(*) FROM artwork_blobs`)
			ipArtworkReencodeExpect(t, w, 1, `SELECT count(*) FROM artwork_command_receipts WHERE command_kind='upload_accept'`)
			ipArtworkReencodeExpect(t, w, 1, `SELECT last_command_sequence FROM artwork_subject_jobs WHERE id=901`)
		})
	}
}

// Current authority, a real turn lifetime and a cancelled caller lifetime gate
// every reencode before any blob or source byte.
func TestIPArtworkReencodeCurrentAuthorityAndLifetime(t *testing.T) {
	for _, mode := range []string{"revoked_family", "forged_proof", "revoked_membership", "cancelled_lifetime"} {
		t.Run(mode, func(t *testing.T) {
			w, r, p := ipArtworkUploadFixture(t)
			owner, spec, previous, slots := ipArtworkReencodePrepared(t, w, r, p)
			cmd := ipArtworkReencodeCommandFor(t, 2, 3, owner, spec, previous, slots)
			lifetime := context.Background()
			if mode == "cancelled_lifetime" {
				cancelled, cancel := context.WithCancel(t.Context())
				cancel()
				lifetime = cancelled
			} else {
				p = ipPolicyRevoke(t, w, p, mode)
			}
			turn, tx, b := ipPolicyTurn(t, w, r, IPArtworkReencode)
			out, err := applyIPArtworkReencode(t.Context(), turn, p, lifetime, b, cmd)
			if out != (ipArtworkReencodeCommandResult{}) || err == nil {
				t.Fatal("stale authority/cancelled lifetime admitted", out, err)
			}
			if mode != "cancelled_lifetime" && !errors.Is(err, ErrDisclosureDenied) {
				t.Fatal(err)
			}
			ipPolicyRollbackTurn(t, turn, tx)
			ipArtworkReencodeExpect(t, w, 1, `SELECT count(*) FROM artwork_sources WHERE digest=? AND revision=1`, spec.sourceDigest)
			ipArtworkReencodeExpect(t, w, 3, `SELECT count(*) FROM artwork_blobs`)
			ipArtworkReencodeExpect(t, w, 0, `SELECT count(*) FROM artwork_command_receipts WHERE command_kind='reencode'`)
		})
	}
}

// An uncertain COMMIT is durable truth only through a FRESH read-only
// connection; the quarantined pool serves no saved reencode afterwards.
func TestIPArtworkReencodeUnknownCommitFreshRead(t *testing.T) {
	w, r, p := ipArtworkUploadFixture(t)
	owner, spec, previous, slots := ipArtworkReencodePrepared(t, w, r, p)
	total := ipArtworkMaterialSlotBytes(slots)
	cmd := ipArtworkReencodeCommandFor(t, 2, 3, owner, spec, previous, slots)
	path := ipPolicyDatabasePath(t, w)
	if err := w.db.Close(); err != nil {
		t.Fatal(err)
	}
	connector, err := sqliteconn.Connector(path, w.config)
	if err != nil {
		t.Fatal(err)
	}
	installFaultPool(w, &faultConnector{Connector: connector, afterCommit: true, replyError: true})
	turn, tx, b := ipPolicyTurn(t, w, r, IPArtworkReencode)
	out, err := applyIPArtworkReencode(t.Context(), turn, p, t.Context(), b, cmd)
	if err != nil || out.Outcome.State != Applied {
		t.Fatal(out, err)
	}
	result, err := tx.commit()
	if result.state != commitUnknown || !errors.Is(err, ErrResolveRequired) {
		t.Fatal(result, err)
	}
	if again, e := applyIPArtworkReencode(t.Context(), turn, p, t.Context(), b, cmd); again != (ipArtworkReencodeCommandResult{}) || e == nil {
		t.Fatal("uncertain frame served the saved reencode", again, e)
	}
	for _, check := range []struct {
		query string
		want  int64
	}{
		{`SELECT revision FROM artwork_sources WHERE digest='` + spec.sourceDigest + `'`, 2},
		{`SELECT count(*) FROM artwork_sources WHERE digest='` + spec.sourceDigest + `' AND small_blob='` + ipArtworkMaterialHex(5) + `' AND large_blob='` + ipArtworkMaterialHex(6) + `'`, 1},
		{`SELECT count(*) FROM artwork_blobs`, 6},
		{`SELECT count(*) FROM artwork_blobs WHERE status='ready'`, 6},
		{`SELECT count(*) FROM artwork_command_receipts WHERE command_kind='reencode'`, 1},
		{`SELECT result_bytes FROM artwork_command_receipts WHERE command_kind='reencode'`, total},
		{`SELECT last_command_sequence FROM artwork_subject_jobs WHERE id=901`, 2},
		{`SELECT revision FROM artwork_material_intents WHERE id='job-reencode-poster'`, 3},
		{`SELECT count(*) FROM artwork_selections WHERE digest='` + spec.sourceDigest + `'`, 1},
	} {
		if got := ipPolicyFreshInt(t, path, check.query); got != check.want {
			t.Fatal("fresh read", check.query, got, check.want)
		}
	}
}

// A reencode treats an identical desired variant set as a no-op: no source
// revision bump, but the installed intent and receipt are still saved exactly.
func TestIPArtworkReencodeIdenticalVariantsNoRevisionBump(t *testing.T) {
	w, r, p := ipArtworkUploadFixture(t)
	owner, spec, previous, _ := ipArtworkReencodePrepared(t, w, r, p)
	// Declare an installed intent whose slots are the CURRENT source variants.
	identical := []ipArtworkMaterialSlot{
		{slot: 0, digest: previous.small, mime: "image/jpeg", width: 500, height: 750, bytes: 40 << 10},
		{slot: 1, digest: previous.large, mime: "image/jpeg", width: 1280, height: 720, bytes: 120 << 10},
		{slot: 2, digest: previous.ambient, mime: "image/png", width: 780, height: 1170, bytes: 90 << 10},
	}
	spec.largeEdge = previous.largeEdge
	spec.encoderRevision = previous.encoderRevision
	ipPolicyExec(t, w, `UPDATE artwork_material_intents SET large_edge=1920,encoder_revision=3 WHERE id=?`, spec.intentID)
	for _, slot := range identical {
		ipPolicyExec(t, w, `UPDATE artwork_material_slots SET digest=?,mime=?,width=?,height=?,bytes=? WHERE intent_id=? AND slot=?`,
			slot.digest, slot.mime, slot.width, slot.height, slot.bytes, spec.intentID, slot.slot)
	}
	cmd := ipArtworkReencodeCommandFor(t, 2, 3, owner, spec, previous, identical)
	out, turn, tx, _ := ipArtworkReencodeApply(t, w, r, p, cmd)
	if out.Outcome.State != Applied || out.Value.SourceRevision != 1 ||
		out.Value.PreviousRevision != 1 || out.Value.Bytes != ipArtworkMaterialSlotBytes(identical) {
		t.Fatal(out)
	}
	ipPolicyCommitTurn(t, turn, tx)
	ipArtworkReencodeExpect(t, w, 1, `SELECT revision FROM artwork_sources WHERE digest=?`, spec.sourceDigest)
	ipArtworkReencodeExpect(t, w, 1, `SELECT CASE state WHEN 'published' THEN 1 ELSE 0 END FROM artwork_material_intents WHERE id=?`, spec.intentID)
	ipArtworkReencodeExpect(t, w, 1, `SELECT count(*) FROM artwork_command_receipts WHERE command_kind='reencode' AND accepted_revision=1`)
}

// The reencode receipt is read by its OWN statement: a material/upload row at
// the same key, a retirement token, a wrong count or a mismatched accepted
// revision can never replay.
func TestIPArtworkReencodeMalformedReceiptRefused(t *testing.T) {
	const insert = `INSERT INTO artwork_command_receipts(owner_id,command_incarnation,command_sequence,operation_id,request_digest,applied_ms,result_digest,command_kind,accepted_revision,result_count,result_bytes,result_complete,lease_generation,lease_until_ms,material_intent_id,selected_digest,storage_revision,retirement_token) VALUES(901,X'01000000000000000000000000000000',2,X'02000000000000000000000000000000',X'0300000000000000000000000000000000000000000000000000000000000000',1,X'0400000000000000000000000000000000000000000000000000000000000000','reencode',?,?,?,1,1,?,?,?,?,?)`
	for _, tc := range []struct {
		name   string
		mutate func(t *testing.T, w *executor, spec ipArtworkMaterialPublishSpec, slots []ipArtworkMaterialSlot)
	}{
		{"foreign_kind", func(t *testing.T, w *executor, spec ipArtworkMaterialPublishSpec, slots []ipArtworkMaterialSlot) {
			ipPolicyExec(t, w, insert, int64(2), int64(3), ipArtworkMaterialSlotBytes(slots),
				int64(1<<50), spec.intentID, spec.sourceDigest, int64(2), nil)
			ipPolicyExec(t, w, `UPDATE artwork_command_receipts SET command_kind='material_publish' WHERE command_sequence=2`)
		}},
		{"retirement_token", func(t *testing.T, w *executor, spec ipArtworkMaterialPublishSpec, slots []ipArtworkMaterialSlot) {
			ipPolicyExec(t, w, insert, int64(2), int64(3), ipArtworkMaterialSlotBytes(slots),
				int64(1<<50), spec.intentID, spec.sourceDigest, int64(2),
				[]byte{15, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0})
		}},
		{"wrong_count", func(t *testing.T, w *executor, spec ipArtworkMaterialPublishSpec, slots []ipArtworkMaterialSlot) {
			ipPolicyExec(t, w, insert, int64(2), int64(4), ipArtworkMaterialSlotBytes(slots),
				int64(1<<50), spec.intentID, spec.sourceDigest, int64(2), nil)
		}},
		{"accepted_mismatch", func(t *testing.T, w *executor, spec ipArtworkMaterialPublishSpec, slots []ipArtworkMaterialSlot) {
			ipPolicyExec(t, w, insert, int64(1), int64(3), ipArtworkMaterialSlotBytes(slots),
				int64(1<<50), spec.intentID, spec.sourceDigest, int64(2), nil)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, r, p := ipArtworkUploadFixture(t)
			owner, spec, previous, slots := ipArtworkReencodePrepared(t, w, r, p)
			tc.mutate(t, w, spec, slots)
			ipPolicyExec(t, w, `UPDATE artwork_subject_jobs SET last_command_sequence=2 WHERE id=901`)
			cmd := ipArtworkReencodeCommandFor(t, 2, 3, owner, spec, previous, slots)
			turn, tx, _ := ipPolicyTurn(t, w, r, IPArtworkReencode)
			if _, err := ipArtworkReencodeLookup(t.Context(), turn, cmd.request, cmd.identity, cmd.digest); !errors.Is(err, ErrResolveRequired) {
				t.Fatal("malformed reencode receipt decoded", err)
			}
			ipPolicyRollbackTurn(t, turn, tx)
		})
	}
}

func TestIPArtworkReencodeBoundsAndDescriptors(t *testing.T) {
	owner, spec, previous := ipArtworkReencodeOwner(), ipArtworkReencodeSpec(), ipArtworkReencodePreviousFor()
	slots := ipArtworkReencodeSlots()
	identity := CommandIdentity{Incarnation: owner.incarnation, Sequence: 2, OperationID: [16]byte{3}}
	good := ipArtworkReencodeRequest{owner: owner, spec: spec, previous: previous, slots: slots}
	for name, mutate := range map[string]func(*ipArtworkReencodeRequest){
		"both_namespaces":  func(q *ipArtworkReencodeRequest) { q.owner.personID = 778 },
		"no_namespace":     func(q *ipArtworkReencodeRequest) { q.owner.entityID = 0 },
		"zero_lease":       func(q *ipArtworkReencodeRequest) { q.owner.leaseUntilMS = 0 },
		"foreign_incarn":   func(q *ipArtworkReencodeRequest) { q.owner.incarnation = [16]byte{9} },
		"zero_policy":      func(q *ipArtworkReencodeRequest) { q.spec.policyRevision = 0 },
		"zero_encoder":     func(q *ipArtworkReencodeRequest) { q.spec.encoderRevision = 0 },
		"bad_edge":         func(q *ipArtworkReencodeRequest) { q.spec.largeEdge = 1024 },
		"four_slots":       func(q *ipArtworkReencodeRequest) { q.spec.expectedSlots = 4 },
		"short_digest":     func(q *ipArtworkReencodeRequest) { q.spec.sourceDigest = "abc" },
		"bad_mime":         func(q *ipArtworkReencodeRequest) { q.spec.acceptedMIME = "image/gif" },
		"zero_width":       func(q *ipArtworkReencodeRequest) { q.spec.acceptedWidth = 0 },
		"empty_language":   func(q *ipArtworkReencodeRequest) { q.spec.language = "" },
		"wrong_intent_rev": func(q *ipArtworkReencodeRequest) { q.spec.intentRevision = 1 },
		"two_slots":        func(q *ipArtworkReencodeRequest) { q.slots = q.slots[:2] },
		"encoded_webp":     func(q *ipArtworkReencodeRequest) { q.slots[1].mime = "image/webp" },
		"duplicate_digest": func(q *ipArtworkReencodeRequest) { q.slots[1].digest = q.slots[0].digest },
		"zero_prev_rev":    func(q *ipArtworkReencodeRequest) { q.previous.revision = 0 },
		"zero_prev_encoder": func(q *ipArtworkReencodeRequest) {
			q.previous.encoderRevision = 0
		},
		"negative_original": func(q *ipArtworkReencodeRequest) { q.previous.originalBytes = -1 },
		"short_prev_digest": func(q *ipArtworkReencodeRequest) { q.previous.small = "abc" },
		"empty_prev_digest": func(q *ipArtworkReencodeRequest) { q.previous.ambient = "" },
		"prev_mime_mismatch": func(q *ipArtworkReencodeRequest) {
			q.previous.mime = "image/png"
		},
		"prev_width_mismatch": func(q *ipArtworkReencodeRequest) {
			q.previous.width = 3001
		},
		"prev_bad_edge": func(q *ipArtworkReencodeRequest) { q.previous.largeEdge = 1000 },
	} {
		t.Run(name, func(t *testing.T) {
			q := good
			q.slots = append([]ipArtworkMaterialSlot(nil), slots...)
			mutate(&q)
			if c, err := newIPArtworkReencodeCommand(identity, q); c != nil || !errors.Is(err, ErrInvalid) {
				t.Fatal("malformed reencode admitted", c, err)
			}
		})
	}
	// Bounded maximum: a 128-byte intent id and the largest admissible manifest.
	maxSpec := spec
	maxSpec.intentID = strings.Repeat("i", 128)
	maxSlots := append([]ipArtworkMaterialSlot(nil), slots...)
	for n := range maxSlots {
		maxSlots[n].bytes = 40 << 20
	}
	if _, err := newIPArtworkReencodeCommand(identity, ipArtworkReencodeRequest{
		owner: owner, spec: maxSpec, previous: previous, slots: maxSlots}); err != nil {
		t.Fatal("bounded maximum reencode refused", err)
	}
	expected, err := Describe(IPArtworkReencode)
	if err != nil || expected != (Descriptor{IPArtworkReencode, "ip_artwork_reencode", Normal}) ||
		expected.ID != 231 {
		t.Fatal(expected, err)
	}
	reencodeGood := ipArtworkReencodeSavedResult{IPArtworkReencode, ipArtworkReencodeResult{
		IntentID: "job-reencode-poster", SourceDigest: spec.sourceDigest, SourceRevision: 2,
		Slots: 3, Bytes: 1 << 20, OriginalBytes: 2 << 20, EncoderRevision: 4,
		PreviousRevision: 1, AppliedMS: 1,
	}}
	if err := preflightIPArtworkReencodeResult(reencodeGood); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*ipArtworkReencodeSavedResult){
		"wrong_kind":      func(v *ipArtworkReencodeSavedResult) { v.Kind = IPArtworkUploadAccept },
		"zero_revision":   func(v *ipArtworkReencodeSavedResult) { v.Value.SourceRevision = 0 },
		"jumped_revision": func(v *ipArtworkReencodeSavedResult) { v.Value.SourceRevision = 3 },
		"zero_bytes":      func(v *ipArtworkReencodeSavedResult) { v.Value.Bytes = 0 },
		"negative_original": func(v *ipArtworkReencodeSavedResult) {
			v.Value.OriginalBytes = -1
		},
		"zero_encoder":  func(v *ipArtworkReencodeSavedResult) { v.Value.EncoderRevision = 0 },
		"zero_previous": func(v *ipArtworkReencodeSavedResult) { v.Value.PreviousRevision = 0 },
		"empty_intent":  func(v *ipArtworkReencodeSavedResult) { v.Value.IntentID = "" },
		"empty_digest":  func(v *ipArtworkReencodeSavedResult) { v.Value.SourceDigest = "" },
		"wrong_slots":   func(v *ipArtworkReencodeSavedResult) { v.Value.Slots = 4 },
	} {
		v := reencodeGood
		mutate(&v)
		if err := preflightIPArtworkReencodeResult(v); !errors.Is(err, ErrInvalid) {
			t.Fatal(name, err)
		}
	}
}

// No filesystem, provider or HTTP access can happen inside the SQLite turn.
func TestIPArtworkReencodeIOStaysOutsideHold(t *testing.T) {
	for _, file := range []string{
		"ip_artwork_reencode.go",
		"ip_artwork_reencode_statements.go",
	} {
		parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(file, err)
		}
		for _, spec := range parsed.Imports {
			path, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				t.Fatal(err)
			}
			for _, forbidden := range []string{"os", "io", "io/fs", "path/filepath", "net", "net/http", "mime/multipart"} {
				if path == forbidden || strings.HasPrefix(path, forbidden+"/") {
					t.Fatal(file, "performs IO inside the hold:", path)
				}
			}
		}
	}
}
