package writer

import (
	"context"
	"errors"
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

// Group E is not embedded, so artwork GC is qualified only against the exact
// draft fixtures: IP-19 material custody, IP-20 source/blob custody, IP-21
// selection/history pins and IP-23's receipt-digest reverse indexes. No
// Capture/golden claim is made here. The commands do no file, provider or
// network IO; the caller verifies every unlinked file before it repeats the
// exact byte facts.
func ipArtworkGCFixture(t *testing.T) (*executor, *ipPolicyRegistry, *usOwnerAuthorityProof) {
	t.Helper()
	w, keeper, p := ipPolicySecurityFixture(t)
	for _, name := range []string{
		"testdata/ip-artwork-material-draft.sql",
		"testdata/ip-artwork-material-publish-draft.sql",
		"testdata/ip-artwork-selection-draft.sql",
		"testdata/ip-artwork-gc-draft.sql",
	} {
		ddl, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = w.db.ExecContext(t.Context(), string(ddl)); err != nil {
			t.Fatal(name, err)
		}
	}
	for _, query := range []string{
		`INSERT INTO catalog_kinds(id,name,playable,container,searchable,browsable,listening) VALUES(1,'movie',1,0,1,1,0)`,
		`INSERT INTO catalog_libraries(id,library_id,root) VALUES(777,'ip-library','/media')`,
		`INSERT INTO metadata_providers(code,adapter,enabled) VALUES('tmdb','tmdb',1),('tvdb','tvdb',1),('musicbrainz','musicbrainz',1)`,
		`INSERT INTO screen_metadata_consent(singleton,revision,confirmed) VALUES(1,1,1) ON CONFLICT(singleton) DO UPDATE SET revision=1,confirmed=1`,
		`INSERT INTO metadata_runtime_settings(singleton,artwork_language,artwork_policy_revision,default_language,online_lookups,lookup_epoch) VALUES(1,'same',1,'en',1,3) ON CONFLICT(singleton) DO UPDATE SET artwork_language='same',artwork_policy_revision=1,default_language='en',online_lookups=1,lookup_epoch=3`,
	} {
		ipPolicyExec(t, w, query)
	}
	public := make([]byte, 16)
	public[8], public[9] = byte(778>>8), byte(778&0xff)
	ipPolicyExec(t, w, `INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(778,?,777,1)`, public)
	ipPolicyExec(t, w, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation,source_revision,identity_revision,metadata_revision,artwork_revision) VALUES(778,?,777,1,'Title','title','t','title','t',0,X'03000000000000000000000000000000',X'04000000000000000000000000000000',1,1,1,1)`, public)
	// One retained subject job at the entity's current artwork revision, under
	// a live lease in its publish phase.
	ipPolicyExec(t, w, `INSERT INTO artwork_subject_jobs(id,entity_id,source_incarnation,source_revision,phase,job_revision,job_lease_owner,job_lease_generation,job_lease_expires_ms,command_incarnation) VALUES(901,778,X'04000000000000000000000000000000',1,'publish',1,7,1,?,X'01000000000000000000000000000000')`, int64(1)<<50)
	defs := append(usAuthorityDefinitions(), ipArtworkGCDefinitions()...)
	r, err := stmttest.New(t, defs)
	if err != nil {
		t.Fatal(err)
	}
	return w, &ipPolicyRegistry{r, keeper}, p
}

func ipArtworkGCOwner() ipArtworkMaterialOwner {
	return ipArtworkMaterialOwner{id: 901, incarnation: [16]byte{1}, entityID: 778,
		sourceIncarnation: [16]byte{4}, sourceRevision: 1, revision: 1,
		leaseOwner: 7, leaseGeneration: 1, leaseUntilMS: 1 << 50}
}

func ipArtworkGCNextOwner(o ipArtworkMaterialOwner) ipArtworkMaterialOwner {
	o.revision++
	return o
}

func ipArtworkGCCommandFor(t *testing.T, kind CommandID, owner ipArtworkMaterialOwner, sequence int64,
	operation byte, q ipArtworkGCRequest) *ipArtworkGCCommand {
	t.Helper()
	q.owner = owner
	c, err := newIPArtworkGCCommand(kind, CommandIdentity{
		Incarnation: owner.incarnation, Sequence: sequence, OperationID: [16]byte{operation},
	}, q)
	if err != nil {
		t.Fatal("gc", kind, err)
	}
	return c
}

func ipArtworkGCApply(t *testing.T, w *executor, r *ipPolicyRegistry, p *usOwnerAuthorityProof,
	kind CommandID, c *ipArtworkGCCommand) (ipArtworkGCCommandResult, *commandTurn, *transaction) {
	t.Helper()
	turn, tx, b := ipPolicyTurn(t, w, r, kind)
	out, err := applyIPArtworkGC(t.Context(), turn, p, t.Context(), b, c)
	if err != nil {
		t.Fatal("gc", kind, out, err)
	}
	return out, turn, tx
}

// A refused command leaves no receipt, no owner advance and no retiring row.
func ipArtworkGCRefuse(t *testing.T, w *executor, digest string) {
	t.Helper()
	ipArtworkGCExpect(t, w, 0, `SELECT count(*) FROM artwork_command_receipts WHERE command_kind IN('gc_mark','gc_finish')`)
	ipArtworkGCExpect(t, w, 0, `SELECT last_command_sequence FROM artwork_subject_jobs WHERE id=901`)
	ipArtworkGCExpect(t, w, 1, `SELECT revision FROM artwork_sources WHERE digest=?`, digest)
	ipArtworkGCExpect(t, w, 0, `SELECT retiring FROM artwork_sources WHERE digest=?`, digest)
	ipArtworkGCExpect(t, w, 1, `SELECT job_revision FROM artwork_subject_jobs WHERE id=901`)
}

func ipArtworkGCExpect(t *testing.T, w *executor, want int64, query string, args ...any) {
	t.Helper()
	if got := ipPolicyQueryInt(t, w, query, args...); got != want {
		t.Fatal(query, got, want)
	}
}

func ipArtworkGCAck(t *testing.T, w *executor, sequence int64) {
	t.Helper()
	ipPolicyExec(t, w, `UPDATE artwork_subject_jobs SET acknowledged_command_sequence=? WHERE id=901`, sequence)
}

// One ready accepted source with three encoded blobs and, optionally, the
// retained original upload bytes. Task-specific names so they can never collide
// with another IP task's production or test helper.
func seedIPArtworkGCSource(t *testing.T, w *executor, seed int, original bool) (string, [3]string) {
	t.Helper()
	digest := ipArtworkMaterialHex(seed)
	var blobs [3]string
	for n := 0; n < 3; n++ {
		blobs[n] = ipArtworkMaterialHex(seed*10 + n + 1)
		mime := "image/jpeg"
		if n == 2 {
			mime = "image/png"
		}
		ipPolicyExec(t, w, `INSERT INTO artwork_blobs(digest,mime,width,height,bytes,created_at) VALUES(?,?,?,?,?,?)`,
			blobs[n], mime, int64(500+n), int64(750), int64(40000+n), "2026-01-01T00:00:00Z")
	}
	var originalBytes int64
	if original {
		originalBytes = 2 << 20
	}
	ipPolicyExec(t, w, `INSERT INTO artwork_sources(digest,accepted_mime,accepted_width,accepted_height,small_blob,large_blob,ambient_blob,large_edge,small_outlier,large_outlier,encoder_revision,created_at,revision,original_bytes,retiring,retirement_token) VALUES(?,'image/jpeg',3000,2000,?,?,?,1920,0,0,3,'2026-01-01T00:00:00Z',1,?,0,NULL)`,
		digest, blobs[0], blobs[1], blobs[2], originalBytes)
	return digest, blobs
}

func ipArtworkGCSourceMark(t *testing.T, w *executor, r *ipPolicyRegistry, p *usOwnerAuthorityProof,
	owner ipArtworkMaterialOwner, sequence int64, operation byte, digest string, bytes int64) (ipArtworkGCCommandResult, ipArtworkMaterialOwner) {
	t.Helper()
	cmd := ipArtworkGCCommandFor(t, IPArtworkGCMark, owner, sequence, operation,
		ipArtworkGCRequest{target: ipArtworkGCSource, digest: digest, revision: 1, bytes: bytes})
	out, turn, tx := ipArtworkGCApply(t, w, r, p, IPArtworkGCMark, cmd)
	ipPolicyCommitTurn(t, turn, tx)
	return out, ipArtworkGCNextOwner(owner)
}

// A fresh mark writes the exact retiring revision/token and replays the original
// result after every witness moves; at or below ACK it is Retired with zero
// output.
func TestIPArtworkGCMarkSourceAndReplay(t *testing.T) {
	w, r, p := ipArtworkGCFixture(t)
	digest, _ := seedIPArtworkGCSource(t, w, 1, true)
	cmd := ipArtworkGCCommandFor(t, IPArtworkGCMark, ipArtworkGCOwner(), 1, 2,
		ipArtworkGCRequest{target: ipArtworkGCSource, digest: digest, revision: 1, bytes: 2 << 20})
	out, turn, tx := ipArtworkGCApply(t, w, r, p, IPArtworkGCMark, cmd)
	want := ipArtworkGCResult{Target: ipArtworkGCSource, Digest: digest,
		Token: ipArtworkGCMarkToken(cmd.identity, cmd.digest), Revision: 1, StorageRevision: 2,
		Bytes: 2 << 20, AppliedMS: w.now().UnixMilli()}
	if out.Outcome.State != Applied || out.Outcome.Replayed || out.Value != want ||
		out.Digest == (ResultDigest{}) {
		t.Fatal(out)
	}
	ipPolicyCommitTurn(t, turn, tx)
	original := out
	ipArtworkGCExpect(t, w, 1, `SELECT count(*) FROM artwork_sources WHERE digest=? AND retiring=1 AND revision=2 AND original_bytes=? AND octet_length(retirement_token)=16 AND retirement_token=?`,
		digest, int64(2<<20), want.Token[:])
	ipArtworkGCExpect(t, w, 1, `SELECT count(*) FROM artwork_command_receipts WHERE command_kind='gc_mark' AND accepted_revision=1 AND storage_revision=2 AND blob_digest=? AND after_ordinal=1 AND result_count=1 AND result_bytes=? AND result_complete=0 AND lease_generation=1 AND retirement_token=? AND selected_digest IS NULL AND previous_digest IS NULL AND material_intent_id IS NULL`,
		digest, int64(2<<20), want.Token[:])
	ipArtworkGCExpect(t, w, 1, `SELECT last_command_sequence FROM artwork_subject_jobs WHERE id=901`)
	ipArtworkGCExpect(t, w, 2, `SELECT job_revision FROM artwork_subject_jobs WHERE id=901`)

	// The source is retiring now, but the original result still replays exactly.
	ipPolicyExec(t, w, `UPDATE artwork_subject_jobs SET job_revision=99,job_lease_owner=9 WHERE id=901`)
	ipPolicyExec(t, w, `UPDATE artwork_sources SET revision=7 WHERE digest=?`, digest)
	turn, tx, b := ipPolicyTurn(t, w, r, IPArtworkGCMark)
	replay, err := applyIPArtworkGC(t.Context(), turn, p, t.Context(), b, cmd)
	if err != nil || !replay.Outcome.Replayed || replay.Digest != original.Digest || replay.Value != original.Value {
		t.Fatal("source change altered the original mark", replay, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)

	ipArtworkGCAck(t, w, 1)
	turn, tx, b = ipPolicyTurn(t, w, r, IPArtworkGCMark)
	retired, err := applyIPArtworkGC(t.Context(), turn, p, t.Context(), b, cmd)
	if err != nil || retired.Outcome.State != ResultRetired || retired.Value != (ipArtworkGCResult{}) ||
		retired.Digest != (ResultDigest{}) {
		t.Fatal("retired mark served its result", retired, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
	ipArtworkGCExpect(t, w, 1, `SELECT count(*) FROM artwork_sources WHERE digest=?`, digest)
}

// Every retained reference and every stale witness refuses the mark whole.
func TestIPArtworkGCMarkFences(t *testing.T) {
	cases := []struct {
		name   string
		sql    []string
		mutate func(*ipArtworkGCRequest, *ipArtworkMaterialOwner)
	}{
		{"selection", []string{`INSERT INTO artwork_selections(kind,entity_id,role,choice,digest,provider,image_id,origin,attribution,source_fence,actor,observed_at) VALUES('entity',778,'poster','locked',?,'upload','id','upload','','fence','owner','fixture')`}, nil},
		{"history", []string{`INSERT INTO metadata_owner_history(kind,entity_id,base_revision,revision,trigger,actor,observed_at,before_json,after_json) VALUES('entity',778,'1','2','fixture','owner','fixture','{}','{}')`, `INSERT INTO metadata_owner_history_artwork(history_id,role,choice,digest) VALUES(1,'poster','locked',?)`}, nil},
		{"selected_receipt", []string{`INSERT INTO artwork_command_receipts(owner_id,command_incarnation,command_sequence,operation_id,request_digest,applied_ms,result_digest,command_kind,accepted_revision,result_count,result_bytes,result_complete,lease_generation,lease_until_ms,selected_digest,storage_revision) VALUES(901,X'01000000000000000000000000000000',9,X'02000000000000000000000000000000',X'0300000000000000000000000000000000000000000000000000000000000000',1,X'0400000000000000000000000000000000000000000000000000000000000000','material_publish',1,1,1,1,1,1,?,1)`}, nil},
		{"previous_receipt", []string{
			`INSERT INTO artwork_sources(digest,accepted_mime,accepted_width,accepted_height,small_blob,large_blob,ambient_blob,large_edge,small_outlier,large_outlier,encoder_revision,created_at,revision,original_bytes,retiring,retirement_token) SELECT 'ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff',accepted_mime,accepted_width,accepted_height,small_blob,large_blob,ambient_blob,large_edge,small_outlier,large_outlier,encoder_revision,'2026-01-01T00:00:00Z',1,0,0,NULL FROM artwork_sources`,
			`INSERT INTO artwork_command_receipts(owner_id,command_incarnation,command_sequence,operation_id,request_digest,applied_ms,result_digest,command_kind,accepted_revision,result_count,result_bytes,result_complete,lease_generation,lease_until_ms,selected_digest,previous_digest,storage_revision) VALUES(901,X'01000000000000000000000000000000',9,X'02000000000000000000000000000000',X'0300000000000000000000000000000000000000000000000000000000000000',1,X'0400000000000000000000000000000000000000000000000000000000000000','material_publish',1,1,1,1,1,1,'ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff',?,1)`}, nil},
		{"material_intent", []string{`INSERT INTO artwork_material_intents(id,owner_id,owner_incarnation,generation,revision,source_digest,accepted_mime,accepted_width,accepted_height,large_edge,encoder_revision,state,expected_slots,staged_slots) VALUES('job-gc',901,X'01000000000000000000000000000000',1,2,?,'image/jpeg',3000,2000,1920,3,'installed',3,3)`}, nil},
		{"backup_hold", []string{`INSERT INTO backup_blob_holds VALUES(1,'backup','epoch','scratch',0,'copying')`}, nil},
		{"blob_deleting", []string{`UPDATE artwork_blobs SET status='deleting',storage_revision=2,deletion_token=X'0f000000000000000000000000000000' WHERE digest=(SELECT small_blob FROM artwork_sources WHERE digest=?)`}, nil},
		{"stale_revision", []string{`UPDATE artwork_sources SET revision=2 WHERE digest=?`}, nil},
		{"stale_original", nil, func(q *ipArtworkGCRequest, _ *ipArtworkMaterialOwner) { q.bytes = 1 << 20 }},
		{"missing_source", nil, func(q *ipArtworkGCRequest, _ *ipArtworkMaterialOwner) { q.digest = ipArtworkMaterialHex(9) }},
		{"wrong_phase", []string{`UPDATE artwork_subject_jobs SET phase='providers' WHERE id=901`}, nil},
		{"stale_lease_owner", []string{`UPDATE artwork_subject_jobs SET job_lease_owner=8 WHERE id=901`}, nil},
		{"expired_lease", []string{`UPDATE artwork_subject_jobs SET job_lease_expires_ms=1 WHERE id=901`}, nil},
		{"stale_owner_revision", nil, func(_ *ipArtworkGCRequest, o *ipArtworkMaterialOwner) { o.revision = 2 }},
		{"stale_source_incarnation", nil, func(_ *ipArtworkGCRequest, o *ipArtworkMaterialOwner) { o.sourceIncarnation = [16]byte{5} }},
		{"wrong_entity", nil, func(_ *ipArtworkGCRequest, o *ipArtworkMaterialOwner) { o.entityID = 779 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w, r, p := ipArtworkGCFixture(t)
			digest, _ := seedIPArtworkGCSource(t, w, 1, true)
			for _, query := range tc.sql {
				if strings.Contains(query, "?") {
					ipPolicyExec(t, w, query, digest)
				} else {
					ipPolicyExec(t, w, query)
				}
			}
			owner := ipArtworkGCOwner()
			q := ipArtworkGCRequest{target: ipArtworkGCSource, digest: digest, revision: 1, bytes: 2 << 20}
			if tc.mutate != nil {
				tc.mutate(&q, &owner)
			}
			cmd := ipArtworkGCCommandFor(t, IPArtworkGCMark, owner, 1, 2, q)
			turn, tx, b := ipPolicyTurn(t, w, r, IPArtworkGCMark)
			out, err := applyIPArtworkGC(t.Context(), turn, p, t.Context(), b, cmd)
			if out != (ipArtworkGCCommandResult{}) || !errors.Is(err, ErrConflict) {
				t.Fatal("stale mark admitted", out, err)
			}
			ipPolicyRollbackTurn(t, turn, tx)
			switch tc.name {
			case "stale_revision":
				ipArtworkGCExpect(t, w, 2, `SELECT revision FROM artwork_sources WHERE digest=?`, digest)
				ipArtworkGCExpect(t, w, 0, `SELECT retiring FROM artwork_sources WHERE digest=?`, digest)
				ipArtworkGCExpect(t, w, 0, `SELECT count(*) FROM artwork_command_receipts`)
				ipArtworkGCExpect(t, w, 0, `SELECT last_command_sequence FROM artwork_subject_jobs WHERE id=901`)
			case "missing_source":
				ipArtworkGCExpect(t, w, 0, `SELECT count(*) FROM artwork_command_receipts`)
				ipArtworkGCExpect(t, w, 0, `SELECT last_command_sequence FROM artwork_subject_jobs WHERE id=901`)
				ipArtworkGCExpect(t, w, 1, `SELECT count(*) FROM artwork_sources WHERE digest=? AND retiring=0 AND revision=1`, digest)
			default:
				ipArtworkGCRefuse(t, w, digest)
			}
		})
	}
}

// A blob cannot be marked while any source or material slot still names it, and
// both mark and finalize remove only their own exact row.
func TestIPArtworkGCBlobMarkFinalizeAndReverseFences(t *testing.T) {
	w, r, p := ipArtworkGCFixture(t)
	digest, blobs := seedIPArtworkGCSource(t, w, 1, true)
	owner := ipArtworkGCOwner()
	blob := blobs[0]

	// The live source still pins the blob on its small slot.
	blobMark := ipArtworkGCRequest{target: ipArtworkGCBlob, digest: blob, revision: 1, bytes: 40000}
	cmd := ipArtworkGCCommandFor(t, IPArtworkGCMark, owner, 1, 2, blobMark)
	turn, tx, b := ipPolicyTurn(t, w, r, IPArtworkGCMark)
	out, err := applyIPArtworkGC(t.Context(), turn, p, t.Context(), b, cmd)
	if out != (ipArtworkGCCommandResult{}) || !errors.Is(err, ErrConflict) {
		t.Fatal("source-referenced blob marked", out, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
	ipArtworkGCExpect(t, w, 1, `SELECT count(*) FROM artwork_sources WHERE digest=?`, digest)

	// Free it by retiring the source row, then prove a NEW source that reuses the
	// exact blob keeps it charged.
	ipPolicyExec(t, w, `DELETE FROM artwork_sources WHERE digest=?`, digest)
	ipPolicyExec(t, w, `INSERT INTO artwork_sources(digest,accepted_mime,accepted_width,accepted_height,small_blob,large_blob,ambient_blob,large_edge,small_outlier,large_outlier,encoder_revision,created_at,revision,original_bytes,retiring,retirement_token) VALUES(?,'image/jpeg',3000,2000,?,?,?,1920,0,0,3,'2026-01-01T00:00:00Z',1,0,0,NULL)`,
		ipArtworkMaterialHex(9), blob, blob, blob)
	turn, tx, b = ipPolicyTurn(t, w, r, IPArtworkGCMark)
	out, err = applyIPArtworkGC(t.Context(), turn, p, t.Context(), b, cmd)
	if out != (ipArtworkGCCommandResult{}) || !errors.Is(err, ErrConflict) {
		t.Fatal("replacement source blob marked", out, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
	ipPolicyExec(t, w, `DELETE FROM artwork_sources WHERE digest=?`, ipArtworkMaterialHex(9))

	// An installed/abandoned material slot also keeps the exact digest charged.
	ipPolicyExec(t, w, `INSERT INTO artwork_material_intents(id,owner_id,owner_incarnation,generation,revision,source_digest,accepted_mime,accepted_width,accepted_height,large_edge,encoder_revision,state,expected_slots,staged_slots) VALUES('job-gc-blob',901,X'01000000000000000000000000000000',1,2,?,'image/jpeg',3000,2000,1920,3,'installed',3,3)`,
		ipArtworkMaterialHex(8))
	ipPolicyExec(t, w, `INSERT INTO artwork_material_slots(intent_id,slot,digest,mime,width,height,bytes,outlier) VALUES('job-gc-blob',1,?,'image/jpeg',500,750,40000,0)`, blob)
	turn, tx, b = ipPolicyTurn(t, w, r, IPArtworkGCMark)
	out, err = applyIPArtworkGC(t.Context(), turn, p, t.Context(), b, cmd)
	if out != (ipArtworkGCCommandResult{}) || !errors.Is(err, ErrConflict) {
		t.Fatal("material-slot blob marked", out, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
	ipPolicyExec(t, w, `DELETE FROM artwork_material_slots WHERE intent_id='job-gc-blob'`)
	ipPolicyExec(t, w, `DELETE FROM artwork_material_intents WHERE id='job-gc-blob'`)

	// Now the mark succeeds and mints its own token.
	obj, turn, tx := ipArtworkGCApply(t, w, r, p, IPArtworkGCMark, cmd)
	if obj.Outcome.State != Applied || obj.Value.Target != ipArtworkGCBlob ||
		obj.Value.StorageRevision != 2 || obj.Value.Bytes != 40000 || obj.Value.Token == ([16]byte{}) {
		t.Fatal(obj)
	}
	ipPolicyCommitTurn(t, turn, tx)
	ipArtworkGCExpect(t, w, 1, `SELECT count(*) FROM artwork_blobs WHERE digest=? AND status='deleting' AND storage_revision=2 AND deletion_token=?`, blob, obj.Value.Token[:])
	next := ipArtworkGCNextOwner(owner)
	ipArtworkGCAck(t, w, 1)

	// No row-ready acknowledgment without the exact file byte fact or token.
	for name, bad := range map[string]ipArtworkGCRequest{
		"wrong_token":    {target: ipArtworkGCBlob, digest: blob, revision: 2, bytes: 40000, token: [16]byte{9}},
		"wrong_bytes":    {target: ipArtworkGCBlob, digest: blob, revision: 2, bytes: 40001, token: obj.Value.Token},
		"wrong_revision": {target: ipArtworkGCBlob, digest: blob, revision: 1, bytes: 40000, token: obj.Value.Token},
	} {
		t.Run(name, func(t *testing.T) {
			cmd := ipArtworkGCCommandFor(t, IPArtworkGCFinish, next, 2, 3, bad)
			turn, tx, b := ipPolicyTurn(t, w, r, IPArtworkGCFinish)
			out, err := applyIPArtworkGC(t.Context(), turn, p, t.Context(), b, cmd)
			if out != (ipArtworkGCCommandResult{}) || !errors.Is(err, ErrConflict) {
				t.Fatal("inexact blob finalize admitted", out, err)
			}
			ipPolicyRollbackTurn(t, turn, tx)
		})
	}
	ipArtworkGCExpect(t, w, 1, `SELECT count(*) FROM artwork_blobs WHERE digest=?`, blob)

	// A reference that appears after mark still refuses the row-ready
	// acknowledgment: the same fence runs again at finalize.
	ipPolicyExec(t, w, `INSERT INTO artwork_sources(digest,accepted_mime,accepted_width,accepted_height,small_blob,large_blob,ambient_blob,large_edge,small_outlier,large_outlier,encoder_revision,created_at,revision,original_bytes,retiring,retirement_token) VALUES(?,'image/jpeg',3000,2000,?,?,?,1920,0,0,3,'2026-01-01T00:00:00Z',1,0,0,NULL)`,
		ipArtworkMaterialHex(9), blob, blob, blob)
	late := ipArtworkGCCommandFor(t, IPArtworkGCFinish, next, 2, 3,
		ipArtworkGCRequest{target: ipArtworkGCBlob, digest: blob, revision: 2, bytes: 40000, token: obj.Value.Token})
	turn, tx, b = ipPolicyTurn(t, w, r, IPArtworkGCFinish)
	out, err = applyIPArtworkGC(t.Context(), turn, p, t.Context(), b, late)
	if out != (ipArtworkGCCommandResult{}) || !errors.Is(err, ErrConflict) {
		t.Fatal("late blob reference acknowledged away", out, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
	ipPolicyExec(t, w, `DELETE FROM artwork_sources WHERE digest=?`, ipArtworkMaterialHex(9))

	finish := ipArtworkGCCommandFor(t, IPArtworkGCFinish, next, 2, 3,
		ipArtworkGCRequest{target: ipArtworkGCBlob, digest: blob, revision: 2, bytes: 40000, token: obj.Value.Token})
	done, turn, tx := ipArtworkGCApply(t, w, r, p, IPArtworkGCFinish, finish)
	if done.Outcome.State != Applied || !done.Value.Complete || done.Value.StorageRevision != 2 {
		t.Fatal(done)
	}
	ipPolicyCommitTurn(t, turn, tx)
	ipArtworkGCExpect(t, w, 0, `SELECT count(*) FROM artwork_blobs WHERE digest=?`, blob)
	ipArtworkGCExpect(t, w, 1, `SELECT count(*) FROM artwork_command_receipts WHERE command_kind='gc_finish' AND result_complete=1 AND blob_digest=? AND retirement_token=?`, blob, obj.Value.Token[:])
	// Its retained receipt is a scalar digest, not a dangling FK.
	rows, e := w.db.QueryContext(t.Context(), `PRAGMA foreign_key_check`)
	if e != nil {
		t.Fatal(e)
	}
	if rows.Next() {
		t.Fatal("FK pin to a deleted blob")
	}
	if e = rows.Err(); e != nil {
		t.Fatal(e)
	}
	if e = rows.Close(); e != nil {
		t.Fatal(e)
	}
}

// A source finalize requires the exact retiring revision/token/file and leaves
// every encoded blob and its retained receipt untouched. A stale token after a
// replacement is refused.
func TestIPArtworkGCFinishStaleTokenAndInterruptedRetry(t *testing.T) {
	w, r, p := ipArtworkGCFixture(t)
	digest, blobs := seedIPArtworkGCSource(t, w, 1, true)
	marked, owner := ipArtworkGCSourceMark(t, w, r, p, ipArtworkGCOwner(), 1, 2, digest, 2<<20)
	if marked.Outcome.State != Applied || marked.Value.Token == ([16]byte{}) {
		t.Fatal(marked)
	}
	ipArtworkGCAck(t, w, 1)

	// Interrupted unlink retry: a stale token or a missing/changed file is
	// refused and the mark stays durable for the same retry.
	for name, bad := range map[string]ipArtworkGCRequest{
		"stale_token":  {target: ipArtworkGCSource, digest: digest, revision: 2, bytes: 2 << 20, token: [16]byte{9}},
		"missing_file": {target: ipArtworkGCSource, digest: digest, revision: 2, bytes: 0, token: marked.Value.Token},
	} {
		t.Run(name, func(t *testing.T) {
			cmd := ipArtworkGCCommandFor(t, IPArtworkGCFinish, owner, 2, 3, bad)
			turn, tx, b := ipPolicyTurn(t, w, r, IPArtworkGCFinish)
			out, err := applyIPArtworkGC(t.Context(), turn, p, t.Context(), b, cmd)
			if out != (ipArtworkGCCommandResult{}) || !errors.Is(err, ErrConflict) {
				t.Fatal("inexact source finalize admitted", out, err)
			}
			ipPolicyRollbackTurn(t, turn, tx)
		})
	}
	ipArtworkGCExpect(t, w, 1, `SELECT count(*) FROM artwork_sources WHERE digest=? AND retiring=1 AND revision=2`, digest)
	ipArtworkGCExpect(t, w, 1, `SELECT count(*) FROM artwork_command_receipts WHERE command_kind='gc_mark'`)

	finish := ipArtworkGCCommandFor(t, IPArtworkGCFinish, owner, 2, 3,
		ipArtworkGCRequest{target: ipArtworkGCSource, digest: digest, revision: 2, bytes: 2 << 20,
			token: marked.Value.Token})
	done, turn, tx := ipArtworkGCApply(t, w, r, p, IPArtworkGCFinish, finish)
	if done.Outcome.State != Applied || !done.Value.Complete || done.Value.StorageRevision != 2 {
		t.Fatal(done)
	}
	ipPolicyCommitTurn(t, turn, tx)
	ipArtworkGCExpect(t, w, 0, `SELECT count(*) FROM artwork_sources WHERE digest=?`, digest)
	ipArtworkGCExpect(t, w, 3, `SELECT count(*) FROM artwork_blobs`)

	// Replay after a witness change, then Retired at ACK.
	ipPolicyExec(t, w, `UPDATE artwork_subject_jobs SET job_revision=40 WHERE id=901`)
	turn, tx, b := ipPolicyTurn(t, w, r, IPArtworkGCFinish)
	replay, err := applyIPArtworkGC(t.Context(), turn, p, t.Context(), b, finish)
	if err != nil || !replay.Outcome.Replayed || replay.Value != done.Value || replay.Digest != done.Digest {
		t.Fatal("source finalize replay changed", replay, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
	ipArtworkGCAck(t, w, 2)
	turn, tx, b = ipPolicyTurn(t, w, r, IPArtworkGCFinish)
	retired, err := applyIPArtworkGC(t.Context(), turn, p, t.Context(), b, finish)
	if err != nil || retired.Outcome.State != ResultRetired || retired.Value != (ipArtworkGCResult{}) {
		t.Fatal("retired source finalize served its result", retired, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)

	// A replacement source re-registers under the same digest. A NEW mark gets a
	// new token, and the previous finalize token can never acknowledge it. The
	// encoded blobs were never deleted, so only the source row is re-created.
	ipPolicyExec(t, w, `UPDATE artwork_subject_jobs SET job_revision=3 WHERE id=901`)
	owner = ipArtworkGCNextOwner(owner) // revision 3 after the finish
	ipPolicyExec(t, w, `INSERT INTO artwork_sources(digest,accepted_mime,accepted_width,accepted_height,small_blob,large_blob,ambient_blob,large_edge,small_outlier,large_outlier,encoder_revision,created_at,revision,original_bytes,retiring,retirement_token) VALUES(?,'image/jpeg',3000,2000,?,?,?,1920,0,0,3,'2026-01-01T00:00:00Z',1,?,0,NULL)`,
		digest, blobs[0], blobs[1], blobs[2], int64(2<<20))
	again, owner := ipArtworkGCSourceMark(t, w, r, p, owner, 3, 4, digest, 2<<20)
	if again.Outcome.State != Applied || again.Value.Token == marked.Value.Token {
		t.Fatal("replacement mark reused the old token", again)
	}
	ipArtworkGCAck(t, w, 3)
	stale := ipArtworkGCCommandFor(t, IPArtworkGCFinish, owner, 4, 5,
		ipArtworkGCRequest{target: ipArtworkGCSource, digest: digest, revision: 2, bytes: 2 << 20,
			token: marked.Value.Token})
	turn, tx, b = ipPolicyTurn(t, w, r, IPArtworkGCFinish)
	out, err := applyIPArtworkGC(t.Context(), turn, p, t.Context(), b, stale)
	if out != (ipArtworkGCCommandResult{}) || !errors.Is(err, ErrConflict) {
		t.Fatal("stale token acknowledged a replacement", out, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
	ipArtworkGCExpect(t, w, 1, `SELECT count(*) FROM artwork_sources WHERE digest=? AND retiring=1`, digest)
}

// A reference or backup that appears between mark and finalize refuses the
// row-ready acknowledgment.
func TestIPArtworkGCFinishLateReferenceRefused(t *testing.T) {
	w, r, p := ipArtworkGCFixture(t)
	digest, _ := seedIPArtworkGCSource(t, w, 1, true)
	marked, owner := ipArtworkGCSourceMark(t, w, r, p, ipArtworkGCOwner(), 1, 2, digest, 2<<20)
	ipArtworkGCAck(t, w, 1)
	ipPolicyExec(t, w, `INSERT INTO metadata_owner_history(kind,entity_id,base_revision,revision,trigger,actor,observed_at,before_json,after_json) VALUES('entity',778,'1','2','late','owner','fixture','{}','{}')`)
	ipPolicyExec(t, w, `INSERT INTO metadata_owner_history_artwork(history_id,role,choice,digest) VALUES(1,'poster','locked',?)`, digest)
	finish := ipArtworkGCCommandFor(t, IPArtworkGCFinish, owner, 2, 3,
		ipArtworkGCRequest{target: ipArtworkGCSource, digest: digest, revision: 2, bytes: 2 << 20,
			token: marked.Value.Token})
	turn, tx, b := ipPolicyTurn(t, w, r, IPArtworkGCFinish)
	out, err := applyIPArtworkGC(t.Context(), turn, p, t.Context(), b, finish)
	if out != (ipArtworkGCCommandResult{}) || !errors.Is(err, ErrConflict) {
		t.Fatal("late history pin acknowledged away", out, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
	ipPolicyExec(t, w, `DELETE FROM metadata_owner_history_artwork`)
	ipPolicyExec(t, w, `DELETE FROM metadata_owner_history`)

	// A backup starting after mark is also refused.
	ipPolicyExec(t, w, `INSERT INTO backup_blob_holds VALUES(1,'backup','epoch','scratch',0,'files')`)
	turn, tx, b = ipPolicyTurn(t, w, r, IPArtworkGCFinish)
	out, err = applyIPArtworkGC(t.Context(), turn, p, t.Context(), b, finish)
	if out != (ipArtworkGCCommandResult{}) || !errors.Is(err, ErrConflict) {
		t.Fatal("backup-racing finalize admitted", out, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
	ipPolicyExec(t, w, `DELETE FROM backup_blob_holds WHERE singleton=1`)
	if done, turn, tx := ipArtworkGCApply(t, w, r, p, IPArtworkGCFinish, finish); done.Outcome.State != Applied {
		t.Fatal(done)
	} else {
		ipPolicyCommitTurn(t, turn, tx)
	}
	ipArtworkGCExpect(t, w, 0, `SELECT count(*) FROM artwork_sources WHERE digest=?`, digest)
}

// An abandoned/installed material generation stays charged until its own exact
// slot/source custody completes; only then can the source and its blobs retire.
func TestIPArtworkGCAbandonedMaterialCompletion(t *testing.T) {
	w, r, p := ipArtworkGCFixture(t)
	digest, blobs := seedIPArtworkGCSource(t, w, 1, true)
	owner := ipArtworkGCOwner()
	ipPolicyExec(t, w, `INSERT INTO artwork_material_intents(id,owner_id,owner_incarnation,generation,revision,source_digest,accepted_mime,accepted_width,accepted_height,large_edge,encoder_revision,state,expected_slots,staged_slots) VALUES('job-gc-abandoned',901,X'01000000000000000000000000000000',1,2,?,'image/jpeg',3000,2000,1920,3,'installed',3,3)`, digest)
	for n := 0; n < 3; n++ {
		ipPolicyExec(t, w, `INSERT INTO artwork_material_slots(intent_id,slot,digest,mime,width,height,bytes,outlier) VALUES('job-gc-abandoned',?,?,'image/jpeg',500,750,40000,0)`, n, blobs[n])
	}
	mark := ipArtworkGCCommandFor(t, IPArtworkGCMark, owner, 1, 2,
		ipArtworkGCRequest{target: ipArtworkGCSource, digest: digest, revision: 1, bytes: 2 << 20})
	turn, tx, b := ipPolicyTurn(t, w, r, IPArtworkGCMark)
	out, err := applyIPArtworkGC(t.Context(), turn, p, t.Context(), b, mark)
	if out != (ipArtworkGCCommandResult{}) || !errors.Is(err, ErrConflict) {
		t.Fatal("abandoned material source marked", out, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
	blobMark := ipArtworkGCCommandFor(t, IPArtworkGCMark, owner, 1, 2,
		ipArtworkGCRequest{target: ipArtworkGCBlob, digest: blobs[0], revision: 1, bytes: 40000})
	turn, tx, b = ipPolicyTurn(t, w, r, IPArtworkGCMark)
	out, err = applyIPArtworkGC(t.Context(), turn, p, t.Context(), b, blobMark)
	if out != (ipArtworkGCCommandResult{}) || !errors.Is(err, ErrConflict) {
		t.Fatal("abandoned material slot marked", out, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)

	// The material generation completes its own exact slot/source reclamation.
	ipPolicyExec(t, w, `DELETE FROM artwork_material_slots WHERE intent_id='job-gc-abandoned'`)
	ipPolicyExec(t, w, `UPDATE artwork_material_intents SET state='done',staged_slots=0 WHERE id='job-gc-abandoned'`)
	marked, owner := ipArtworkGCSourceMark(t, w, r, p, owner, 1, 2, digest, 2<<20)
	if marked.Outcome.State != Applied {
		t.Fatal(marked)
	}
	ipArtworkGCAck(t, w, 1)
	finish := ipArtworkGCCommandFor(t, IPArtworkGCFinish, owner, 2, 3,
		ipArtworkGCRequest{target: ipArtworkGCSource, digest: digest, revision: 2, bytes: 2 << 20,
			token: marked.Value.Token})
	done, turn, tx := ipArtworkGCApply(t, w, r, p, IPArtworkGCFinish, finish)
	if done.Outcome.State != Applied {
		t.Fatal(done)
	}
	ipPolicyCommitTurn(t, turn, tx)
	ipArtworkGCExpect(t, w, 0, `SELECT count(*) FROM artwork_sources WHERE digest=?`, digest)
	ipArtworkGCExpect(t, w, 3, `SELECT count(*) FROM artwork_blobs`)
}

// Current authority, a real turn lifetime and a cancelled caller lifetime gate
// both commands before any custody byte.
func TestIPArtworkGCCurrentAuthorityAndLifetime(t *testing.T) {
	for _, mode := range []string{"revoked_family", "forged_proof", "revoked_membership", "cancelled_lifetime"} {
		t.Run(mode, func(t *testing.T) {
			w, r, p := ipArtworkGCFixture(t)
			digest, blobs := seedIPArtworkGCSource(t, w, 1, true)
			marked, owner := ipArtworkGCSourceMark(t, w, r, p, ipArtworkGCOwner(), 1, 2, digest, 2<<20)
			ipArtworkGCAck(t, w, 1)
			finish := ipArtworkGCCommandFor(t, IPArtworkGCFinish, owner, 2, 3,
				ipArtworkGCRequest{target: ipArtworkGCSource, digest: digest, revision: 2,
					bytes: 2 << 20, token: marked.Value.Token})
			blobMark := ipArtworkGCCommandFor(t, IPArtworkGCMark, owner, 2, 4,
				ipArtworkGCRequest{target: ipArtworkGCBlob, digest: blobs[1], revision: 1, bytes: 40001})
			lifetime := context.Background()
			if mode == "cancelled_lifetime" {
				cancelled, cancel := context.WithCancel(t.Context())
				cancel()
				lifetime = cancelled
			} else {
				p = ipPolicyRevoke(t, w, p, mode)
			}
			for _, tc := range []struct {
				kind CommandID
				cmd  *ipArtworkGCCommand
			}{
				{IPArtworkGCFinish, finish},
				{IPArtworkGCMark, blobMark},
			} {
				turn, tx, b := ipPolicyTurn(t, w, r, tc.kind)
				out, err := applyIPArtworkGC(t.Context(), turn, p, lifetime, b, tc.cmd)
				if out != (ipArtworkGCCommandResult{}) || err == nil {
					t.Fatal("stale authority/cancelled lifetime admitted", out, err)
				}
				if mode != "cancelled_lifetime" && !errors.Is(err, ErrDisclosureDenied) {
					t.Fatal(err)
				}
				ipPolicyRollbackTurn(t, turn, tx)
			}
			ipArtworkGCExpect(t, w, 1, `SELECT count(*) FROM artwork_sources WHERE digest=? AND retiring=1`, digest)
			ipArtworkGCExpect(t, w, 1, `SELECT count(*) FROM artwork_command_receipts`)
			ipArtworkGCExpect(t, w, 1, `SELECT count(*) FROM artwork_blobs WHERE digest=? AND status='ready'`, blobs[1])
		})
	}
}

// An uncertain COMMIT is durable truth only through a FRESH read-only
// connection; the quarantined pool serves no saved GC result afterwards.
func TestIPArtworkGCUnknownCommitFreshRead(t *testing.T) {
	t.Run("mark", func(t *testing.T) {
		w, r, p := ipArtworkGCFixture(t)
		digest, _ := seedIPArtworkGCSource(t, w, 1, true)
		cmd := ipArtworkGCCommandFor(t, IPArtworkGCMark, ipArtworkGCOwner(), 1, 2,
			ipArtworkGCRequest{target: ipArtworkGCSource, digest: digest, revision: 1, bytes: 2 << 20})
		path := ipPolicyDatabasePath(t, w)
		if err := w.db.Close(); err != nil {
			t.Fatal(err)
		}
		connector, err := sqliteconn.Connector(path, w.config)
		if err != nil {
			t.Fatal(err)
		}
		installFaultPool(w, &faultConnector{Connector: connector, afterCommit: true, replyError: true})
		turn, tx, b := ipPolicyTurn(t, w, r, IPArtworkGCMark)
		out, err := applyIPArtworkGC(t.Context(), turn, p, t.Context(), b, cmd)
		if err != nil || out.Outcome.State != Applied {
			t.Fatal(out, err)
		}
		result, err := tx.commit()
		if result.state != commitUnknown || !errors.Is(err, ErrResolveRequired) {
			t.Fatal(result, err)
		}
		if again, e := applyIPArtworkGC(t.Context(), turn, p, t.Context(), b, cmd); again != (ipArtworkGCCommandResult{}) || e == nil {
			t.Fatal("uncertain frame served the saved mark", again, e)
		}
		for _, check := range []struct {
			query string
			want  int64
		}{
			{`SELECT count(*) FROM artwork_command_receipts WHERE command_kind='gc_mark'`, 1},
			{`SELECT revision FROM artwork_sources WHERE digest='` + digest + `'`, 2},
			{`SELECT retiring FROM artwork_sources WHERE digest='` + digest + `'`, 1},
			{`SELECT last_command_sequence FROM artwork_subject_jobs WHERE id=901`, 1},
			{`SELECT job_revision FROM artwork_subject_jobs WHERE id=901`, 2},
		} {
			if got := ipPolicyFreshInt(t, path, check.query); got != check.want {
				t.Fatal("fresh read", check.query, got, check.want)
			}
		}
	})
	t.Run("finish", func(t *testing.T) {
		w, r, p := ipArtworkGCFixture(t)
		digest, _ := seedIPArtworkGCSource(t, w, 1, true)
		marked, owner := ipArtworkGCSourceMark(t, w, r, p, ipArtworkGCOwner(), 1, 2, digest, 2<<20)
		ipArtworkGCAck(t, w, 1)
		cmd := ipArtworkGCCommandFor(t, IPArtworkGCFinish, owner, 2, 3,
			ipArtworkGCRequest{target: ipArtworkGCSource, digest: digest, revision: 2,
				bytes: 2 << 20, token: marked.Value.Token})
		path := ipPolicyDatabasePath(t, w)
		if err := w.db.Close(); err != nil {
			t.Fatal(err)
		}
		connector, err := sqliteconn.Connector(path, w.config)
		if err != nil {
			t.Fatal(err)
		}
		installFaultPool(w, &faultConnector{Connector: connector, afterCommit: true, replyError: true})
		turn, tx, b := ipPolicyTurn(t, w, r, IPArtworkGCFinish)
		out, err := applyIPArtworkGC(t.Context(), turn, p, t.Context(), b, cmd)
		if err != nil || out.Outcome.State != Applied {
			t.Fatal(out, err)
		}
		result, err := tx.commit()
		if result.state != commitUnknown || !errors.Is(err, ErrResolveRequired) {
			t.Fatal(result, err)
		}
		if again, e := applyIPArtworkGC(t.Context(), turn, p, t.Context(), b, cmd); again != (ipArtworkGCCommandResult{}) || e == nil {
			t.Fatal("uncertain frame served the saved finalize", again, e)
		}
		for _, check := range []struct {
			query string
			want  int64
		}{
			{`SELECT count(*) FROM artwork_command_receipts WHERE command_kind='gc_finish'`, 1},
			{`SELECT count(*) FROM artwork_sources WHERE digest='` + digest + `'`, 0},
			{`SELECT count(*) FROM artwork_blobs`, 3},
			{`SELECT last_command_sequence FROM artwork_subject_jobs WHERE id=901`, 2},
		} {
			if got := ipPolicyFreshInt(t, path, check.query); got != check.want {
				t.Fatal("fresh read", check.query, got, check.want)
			}
		}
	})
}

// Bounds, descriptors and the closed saved-result codec.
func TestIPArtworkGCBoundsAndDescriptors(t *testing.T) {
	owner := ipArtworkGCOwner()
	identity := CommandIdentity{Incarnation: owner.incarnation, Sequence: 1, OperationID: [16]byte{2}}
	digest := ipArtworkMaterialHex(1)
	good := ipArtworkGCRequest{owner: owner, target: ipArtworkGCSource, digest: digest, revision: 1, bytes: 0}
	for name, mutate := range map[string]func(*ipArtworkGCRequest){
		"zero_target":      func(q *ipArtworkGCRequest) { q.target = 0 },
		"unknown_target":   func(q *ipArtworkGCRequest) { q.target = 3 },
		"empty_digest":     func(q *ipArtworkGCRequest) { q.digest = "" },
		"short_digest":     func(q *ipArtworkGCRequest) { q.digest = "abc" },
		"uppercase_digest": func(q *ipArtworkGCRequest) { q.digest = strings.Repeat("A", 64) },
		"zero_revision":    func(q *ipArtworkGCRequest) { q.revision = 0 },
		"negative_bytes":   func(q *ipArtworkGCRequest) { q.bytes = -1 },
		"oversized_bytes":  func(q *ipArtworkGCRequest) { q.bytes = ipArtworkMaterialMaxSlot + 1 },
		"caller_token":     func(q *ipArtworkGCRequest) { q.token = [16]byte{1} },
		"both_namespaces":  func(q *ipArtworkGCRequest) { q.owner.personID = 778 },
		"no_namespace":     func(q *ipArtworkGCRequest) { q.owner.entityID = 0 },
		"zero_lease":       func(q *ipArtworkGCRequest) { q.owner.leaseUntilMS = 0 },
		"zero_source":      func(q *ipArtworkGCRequest) { q.owner.sourceIncarnation = [16]byte{} },
		"foreign_incarn":   func(q *ipArtworkGCRequest) { q.owner.incarnation = [16]byte{9} },
		"no_owner":         func(q *ipArtworkGCRequest) { q.owner.id = 0 },
		"blob_zero_bytes":  func(q *ipArtworkGCRequest) { q.target = ipArtworkGCBlob },
	} {
		t.Run(name, func(t *testing.T) {
			q := good
			mutate(&q)
			if c, err := newIPArtworkGCCommand(IPArtworkGCMark, identity, q); c != nil || !errors.Is(err, ErrInvalid) {
				t.Fatal("malformed mark admitted", c, err)
			}
		})
	}
	// A bounded source mark and the largest blob finalize are admissible.
	if _, err := newIPArtworkGCCommand(IPArtworkGCMark, identity, good); err != nil {
		t.Fatal("bounded source mark refused", err)
	}
	blob := ipArtworkGCRequest{owner: owner, target: ipArtworkGCBlob, digest: digest, revision: 1,
		bytes: ipArtworkMaterialMaxSlot, token: [16]byte{1}}
	if _, err := newIPArtworkGCCommand(IPArtworkGCFinish, identity, blob); err != nil {
		t.Fatal("bounded blob finalize refused", err)
	}
	// A finish request without a token is never a mark alias.
	tokenless := blob
	tokenless.token = [16]byte{}
	if c, err := newIPArtworkGCCommand(IPArtworkGCFinish, identity, tokenless); c != nil || !errors.Is(err, ErrInvalid) {
		t.Fatal("tokenless finalize admitted", c, err)
	}
	for _, want := range []Descriptor{
		{IPArtworkGCMark, "ip_artwork_gc_mark", Full},
		{IPArtworkGCFinish, "ip_artwork_gc_finish", Full},
	} {
		if got, err := Describe(want.ID); err != nil || got != want {
			t.Fatal(got, err)
		}
	}
	markGood := ipArtworkGCSavedResult{IPArtworkGCMark, ipArtworkGCResult{Target: ipArtworkGCSource,
		Digest: digest, Token: [16]byte{1}, Revision: 1, StorageRevision: 2, Bytes: 0, AppliedMS: 1}}
	if err := preflightIPArtworkGCResult(markGood); err != nil {
		t.Fatal(err)
	}
	finishGood := ipArtworkGCSavedResult{IPArtworkGCFinish, ipArtworkGCResult{Target: ipArtworkGCBlob,
		Digest: digest, Token: [16]byte{1}, Revision: 2, StorageRevision: 2, Bytes: 1, Complete: true, AppliedMS: 1}}
	if err := preflightIPArtworkGCResult(finishGood); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*ipArtworkGCSavedResult){
		"wrong_kind":       func(v *ipArtworkGCSavedResult) { v.Kind = IPArtworkMaterialPublish },
		"zero_target":      func(v *ipArtworkGCSavedResult) { v.Value.Target = 0 },
		"empty_digest":     func(v *ipArtworkGCSavedResult) { v.Value.Digest = "" },
		"zero_token":       func(v *ipArtworkGCSavedResult) { v.Value.Token = [16]byte{} },
		"zero_revision":    func(v *ipArtworkGCSavedResult) { v.Value.Revision = 0 },
		"mark_complete":    func(v *ipArtworkGCSavedResult) { v.Value.Complete = true },
		"mark_storage":     func(v *ipArtworkGCSavedResult) { v.Value.StorageRevision = 3 },
		"negative_bytes":   func(v *ipArtworkGCSavedResult) { v.Value.Bytes = -1 },
		"negative_applied": func(v *ipArtworkGCSavedResult) { v.Value.AppliedMS = -1 },
		"blob_no_bytes": func(v *ipArtworkGCSavedResult) {
			v.Kind = IPArtworkGCFinish
			v.Value = ipArtworkGCResult{Target: ipArtworkGCBlob, Digest: digest, Token: [16]byte{1},
				Revision: 2, StorageRevision: 2, Complete: true, AppliedMS: 1}
		},
	} {
		v := markGood
		mutate(&v)
		if err := preflightIPArtworkGCResult(v); !errors.Is(err, ErrInvalid) {
			t.Fatal(name, err)
		}
	}
	markDigest, err := ipArtworkGCResultDigest(IPArtworkGCMark, identity, RequestDigest{1}, markGood.Value)
	if err != nil {
		t.Fatal(err)
	}
	finishValue := finishGood.Value
	finishValue.Revision, finishValue.StorageRevision = 1, 1
	finishDigest, err := ipArtworkGCResultDigest(IPArtworkGCFinish, identity, RequestDigest{1}, finishValue)
	if err != nil || markDigest == finishDigest {
		t.Fatal("gc variants share a result digest", err)
	}
}

// The receipt is read by its OWN statement: a foreign label, a missing token, a
// selection/previous FK, a foreign target or a mark row at the finish key can
// never replay.
func TestIPArtworkGCReceiptOwnStatement(t *testing.T) {
	const insert = `INSERT INTO artwork_command_receipts(owner_id,command_incarnation,command_sequence,operation_id,request_digest,applied_ms,result_digest,command_kind,accepted_revision,result_count,result_bytes,result_complete,lease_generation,lease_until_ms,blob_digest,after_ordinal,retirement_token,storage_revision) VALUES(901,X'01000000000000000000000000000000',1,X'02000000000000000000000000000000',X'0300000000000000000000000000000000000000000000000000000000000000',1,X'0400000000000000000000000000000000000000000000000000000000000000','gc_mark',1,1,0,0,1,?,?,1,X'0f000000000000000000000000000000',2)`
	cases := []struct {
		name string
		sql  []string
		kind CommandID
	}{
		{"foreign_kind", []string{`UPDATE artwork_command_receipts SET command_kind='material_publish',result_complete=1`}, IPArtworkGCMark},
		// A mark receipt whose token is absent is an impossible durable row: no
		// writing command can produce it, so the fixture disables CHECK
		// enforcement only for this corruption to prove the lookup refuses it.
		{"missing_token", []string{
			`PRAGMA ignore_check_constraints=ON`,
			`UPDATE artwork_command_receipts SET retirement_token=NULL`,
			`PRAGMA ignore_check_constraints=OFF`,
		}, IPArtworkGCMark},
		{"foreign_target", []string{`UPDATE artwork_command_receipts SET after_ordinal=0`}, IPArtworkGCMark},
		{"mark_at_finish", nil, IPArtworkGCFinish},
		{"selected_fk", []string{`UPDATE artwork_command_receipts SET selected_digest=?`}, IPArtworkGCMark},
		{"previous_fk", []string{`UPDATE artwork_command_receipts SET previous_digest=?`}, IPArtworkGCMark},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w, r, _ := ipArtworkGCFixture(t)
			digest, _ := seedIPArtworkGCSource(t, w, 1, true)
			ipPolicyExec(t, w, insert, int64(1<<50), digest)
			for _, query := range tc.sql {
				if strings.Contains(query, "?") {
					ipPolicyExec(t, w, query, digest)
				} else {
					ipPolicyExec(t, w, query)
				}
			}
			ipPolicyExec(t, w, `UPDATE artwork_subject_jobs SET last_command_sequence=1 WHERE id=901`)
			// A mark carries no caller token; only finalize repeats the minted one.
			var token [16]byte
			if tc.kind == IPArtworkGCFinish {
				token = [16]byte{1}
			}
			cmd := ipArtworkGCCommandFor(t, tc.kind, ipArtworkGCOwner(), 1, 2,
				ipArtworkGCRequest{target: ipArtworkGCSource, digest: digest, revision: 1, bytes: 2 << 20,
					token: token})
			turn, tx, _ := ipPolicyTurn(t, w, r, tc.kind)
			if _, err := ipArtworkGCLookup(t.Context(), turn, tc.kind, cmd.request, cmd.identity, cmd.digest); !errors.Is(err, ErrResolveRequired) {
				t.Fatal("malformed gc receipt decoded", err)
			}
			ipPolicyRollbackTurn(t, turn, tx)
		})
	}
}

// No filesystem, provider or HTTP access can happen inside the SQLite turn.
func TestIPArtworkGCIOStaysOutsideHold(t *testing.T) {
	for _, file := range []string{
		"ip_artwork_gc_native.go",
		"ip_artwork_gc_statements.go",
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
