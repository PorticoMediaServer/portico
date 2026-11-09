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

// Group E is not embedded, so owner upload acceptance is qualified only against
// the exact draft fixtures: IP-19's material custody, IP-20's source/blob
// custody and IP-21's selection replacement plus normalized history-artwork
// pins. No Capture/golden claim is made here. The command does no file or
// provider IO; the caller verifies every installed file before it repeats the
// manifest.
func ipArtworkUploadFixture(t *testing.T) (*executor, *ipPolicyRegistry, *usOwnerAuthorityProof) {
	t.Helper()
	w, keeper, p := ipPolicySecurityFixture(t)
	for _, name := range []string{
		"testdata/ip-artwork-material-draft.sql",
		"testdata/ip-artwork-material-publish-draft.sql",
		"testdata/ip-artwork-selection-draft.sql",
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
	// One retained subject job at the entity's current artwork revision.
	ipPolicyExec(t, w, `INSERT INTO artwork_subject_jobs(id,entity_id,source_incarnation,source_revision,phase,job_revision,job_lease_owner,job_lease_generation,job_lease_expires_ms,command_incarnation) VALUES(901,778,X'04000000000000000000000000000000',1,'publish',1,7,1,?,X'01000000000000000000000000000000')`, int64(1)<<50)
	// A completed native owner-upload job whose ID is the installed intent ID.
	ipPolicyExec(t, w, `INSERT INTO artwork_jobs(id,kind,entity_id,role,choice,image_id,attribution,provider,origin,source_fence,actor,selection_revision,status,created_at,job_next_attempt_ms,job_revision,job_lease_generation,job_lease_owner,job_lease_expires_ms,subject_job_id) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		"job-upload-poster", "entity", 778, "poster", "locked", "upload-001", "Upload", "upload",
		"upload", "upload-fence", "owner", 0, "complete", "", nil, 1, 0, 0, 0, 901)
	spec := ipArtworkUploadSpec()
	ipPolicyExec(t, w, `INSERT INTO artwork_material_intents(id,owner_id,owner_incarnation,generation,revision,source_digest,accepted_mime,accepted_width,accepted_height,large_edge,encoder_revision,state,expected_slots,staged_slots) VALUES(?,901,X'01000000000000000000000000000000',1,2,?,'image/jpeg',3000,2000,1920,3,'installed',4,4)`,
		spec.intentID, spec.sourceDigest)
	for _, slot := range ipArtworkUploadSlots() {
		ipPolicyExec(t, w, `INSERT INTO artwork_material_slots(intent_id,slot,digest,mime,width,height,bytes,outlier) VALUES(?,?,?,?,?,?,?,0)`,
			spec.intentID, slot.slot, slot.digest, slot.mime, slot.width, slot.height, slot.bytes)
	}
	defs := append(usAuthorityDefinitions(), ipArtworkMaterialDefinitions()...)
	defs = append(defs, ipArtworkMaterialPublishDefinitions()...)
	defs = append(defs, ipArtworkSelectionDefinitions()...)
	defs = append(defs, ipArtworkUploadDefinitions()...)
	defs = append(defs, ipArtworkReencodeDefinitions()...)
	r, err := stmttest.New(t, defs)
	if err != nil {
		t.Fatal(err)
	}
	return w, &ipPolicyRegistry{r, keeper}, p
}

func ipArtworkUploadSpec() ipArtworkMaterialPublishSpec {
	return ipArtworkMaterialPublishSpec{
		intentID: "job-upload-poster", sourceDigest: ipArtworkMaterialHex(1),
		acceptedMIME: "image/jpeg", language: "en", policyRevision: 1,
		acceptedWidth: 3000, acceptedHeight: 2000, largeEdge: 1920, encoderRevision: 3,
		expectedSlots: 4, intentGeneration: 1, intentRevision: 2,
	}
}

func ipArtworkUploadSlots() []ipArtworkMaterialSlot {
	spec := ipArtworkUploadSpec()
	return []ipArtworkMaterialSlot{
		{slot: 0, digest: ipArtworkMaterialHex(2), mime: "image/jpeg", width: 500, height: 750, bytes: 40 << 10},
		{slot: 1, digest: ipArtworkMaterialHex(3), mime: "image/jpeg", width: 1280, height: 720, bytes: 120 << 10},
		{slot: 2, digest: ipArtworkMaterialHex(4), mime: "image/png", width: 780, height: 1170, bytes: 90 << 10},
		{slot: 3, digest: spec.sourceDigest, mime: spec.acceptedMIME, width: spec.acceptedWidth, height: spec.acceptedHeight, bytes: 2 << 20},
	}
}

func ipArtworkUploadOwner() ipArtworkMaterialOwner {
	return ipArtworkMaterialOwner{id: 901, incarnation: [16]byte{1}, entityID: 778,
		sourceIncarnation: [16]byte{4}, sourceRevision: 1, revision: 1,
		leaseOwner: 7, leaseGeneration: 1, leaseUntilMS: 1 << 50}
}

func ipArtworkUploadCommandFor(t *testing.T, sequence int64, operation byte, owner ipArtworkMaterialOwner,
	spec ipArtworkMaterialPublishSpec, slots []ipArtworkMaterialSlot,
	before ipArtworkSelectionBefore, after ipArtworkSelectionFacts) *ipArtworkUploadCommand {
	t.Helper()
	c, err := newIPArtworkUploadCommand(CommandIdentity{
		Incarnation: owner.incarnation, Sequence: sequence, OperationID: [16]byte{operation},
	}, ipArtworkUploadRequest{
		owner: owner, spec: spec, slots: slots, provider: "upload",
		selection: ipArtworkUploadSelection{
			role: "poster", actor: "owner", before: before, after: after,
		},
	})
	if err != nil {
		t.Fatal("upload", err)
	}
	return c
}

func ipArtworkUploadApply(t *testing.T, w *executor, r *ipPolicyRegistry, p *usOwnerAuthorityProof,
	c *ipArtworkUploadCommand) (ipArtworkUploadCommandResult, *commandTurn, *transaction, ipPolicyBudget) {
	t.Helper()
	turn, tx, b := ipPolicyTurn(t, w, r, IPArtworkUploadAccept)
	out, err := applyIPArtworkUpload(t.Context(), turn, p, t.Context(), b, c)
	if err != nil {
		t.Fatal("upload", out, err)
	}
	return out, turn, tx, b
}

func ipArtworkUploadExpect(t *testing.T, w *executor, want int64, query string, args ...any) {
	t.Helper()
	if got := ipPolicyQueryInt(t, w, query, args...); got != want {
		t.Fatal(query, got, want)
	}
}

// A refused upload retains nothing: no source, blob, selection, history or
// receipt, and the owner/entity counters are unchanged.
func ipArtworkUploadClean(t *testing.T, w *executor) {
	t.Helper()
	for _, check := range []struct {
		query string
		want  int64
	}{
		{`SELECT count(*) FROM artwork_sources`, 0},
		{`SELECT count(*) FROM artwork_blobs`, 0},
		{`SELECT count(*) FROM artwork_selections`, 0},
		{`SELECT count(*) FROM metadata_owner_history`, 0},
		{`SELECT count(*) FROM metadata_owner_history_artwork`, 0},
		{`SELECT count(*) FROM artwork_command_receipts`, 0},
		{`SELECT last_command_sequence FROM artwork_subject_jobs WHERE id=901`, 0},
		{`SELECT job_revision FROM artwork_subject_jobs WHERE id=901`, 1},
		{`SELECT artwork_revision FROM catalog_entities WHERE id=778`, 1},
		{`SELECT revision FROM artwork_material_intents WHERE id='job-upload-poster'`, 2},
		{`SELECT count(*) FROM artwork_material_slots WHERE intent_id='job-upload-poster'`, 4},
	} {
		ipArtworkUploadExpect(t, w, check.want, check.query)
	}
}

func ipArtworkUploadAck(t *testing.T, w *executor, sequence int64) {
	t.Helper()
	ipPolicyExec(t, w, `UPDATE artwork_subject_jobs SET acknowledged_command_sequence=? WHERE id=901`, sequence)
}

// The accepted source, its three encoded blobs, the retained original and the
// locked owner choice are written together; the original saved result replays
// after source, policy, lease and ACK changes.
func TestIPArtworkUploadAcceptSourceSelectionAndReplay(t *testing.T) {
	w, r, p := ipArtworkUploadFixture(t)
	spec, slots := ipArtworkUploadSpec(), ipArtworkUploadSlots()
	owner := ipArtworkUploadOwner()
	total := ipArtworkMaterialSlotBytes(slots)
	cmd := ipArtworkUploadCommandFor(t, 1, 2, owner, spec, slots, ipArtworkSelectionBefore{},
		ipArtworkSelectionUploadFacts(spec.sourceDigest))
	out, turn, tx, _ := ipArtworkUploadApply(t, w, r, p, cmd)
	want := ipArtworkUploadResult{
		IntentID: spec.intentID, SourceDigest: spec.sourceDigest, Revision: 3,
		Slots: 4, Bytes: total, OriginalBytes: 2 << 20, Role: "poster",
		SelectionRevision: 1, Selected: spec.sourceDigest,
		History: EvidenceInteger{Value: 1, Present: true}, AppliedMS: w.now().UnixMilli(),
	}
	if out.Outcome.State != Applied || out.Outcome.Replayed || out.Value != want ||
		out.Digest == (ResultDigest{}) {
		t.Fatal(out)
	}
	ipPolicyCommitTurn(t, turn, tx)
	originalUpload := out

	ipArtworkUploadExpect(t, w, 1, `SELECT count(*) FROM artwork_sources WHERE digest=? AND accepted_mime='image/jpeg' AND accepted_width=3000 AND accepted_height=2000 AND small_blob=? AND large_blob=? AND ambient_blob=? AND large_edge=1920 AND encoder_revision=3 AND revision=1 AND original_bytes=? AND retiring=0 AND retirement_token IS NULL`,
		spec.sourceDigest, slots[0].digest, slots[1].digest, slots[2].digest, int64(2<<20))
	ipArtworkUploadExpect(t, w, 3, `SELECT count(*) FROM artwork_blobs`)
	ipArtworkUploadExpect(t, w, 1,
		`SELECT count(*) FROM artwork_blobs WHERE digest=? AND mime='image/jpeg' AND width=1280 AND height=720 AND bytes=122880 AND status='ready' AND storage_revision=1 AND deletion_token IS NULL`,
		slots[1].digest)
	ipArtworkUploadExpect(t, w, 1,
		`SELECT CASE state WHEN 'published' THEN 1 ELSE 0 END FROM artwork_material_intents WHERE id=?`, spec.intentID)
	ipArtworkUploadExpect(t, w, 3, `SELECT revision FROM artwork_material_intents WHERE id=?`, spec.intentID)
	ipArtworkUploadExpect(t, w, 1,
		`SELECT count(*) FROM artwork_selections WHERE kind='entity' AND entity_id=778 AND role='poster' AND choice='locked' AND digest=? AND provider='upload' AND revision=1`,
		spec.sourceDigest)
	ipArtworkUploadExpect(t, w, 1, `SELECT count(*) FROM metadata_owner_history WHERE trigger='artwork_upload' AND actor='owner' AND base_revision='1' AND revision='2'`)
	ipArtworkUploadExpect(t, w, 1, `SELECT count(*) FROM metadata_owner_history_artwork WHERE role='poster' AND choice='locked' AND digest=?`, spec.sourceDigest)
	ipArtworkUploadExpect(t, w, 1,
		`SELECT count(*) FROM artwork_command_receipts WHERE command_kind='upload_accept' AND role='poster' AND choice='locked' AND selection_revision=1 AND selected_digest=? AND previous_digest IS NULL AND storage_revision=1 AND result_count=4 AND result_bytes=? AND result_complete=1 AND material_intent_id=? AND history_id=1`,
		spec.sourceDigest, total, spec.intentID)
	ipArtworkUploadExpect(t, w, 1, `SELECT last_command_sequence FROM artwork_subject_jobs WHERE id=901`)
	ipArtworkUploadExpect(t, w, 2, `SELECT job_revision FROM artwork_subject_jobs WHERE id=901`)
	ipArtworkUploadExpect(t, w, 2, `SELECT artwork_revision FROM catalog_entities WHERE id=778`)

	// The original upload replays exactly after every fresh witness moves.
	ipPolicyExec(t, w, `UPDATE catalog_entities SET artwork_revision=9 WHERE id=778`)
	ipPolicyExec(t, w, `UPDATE metadata_runtime_settings SET artwork_language='textless',artwork_policy_revision=9`)
	ipPolicyExec(t, w, `UPDATE artwork_subject_jobs SET job_revision=99,job_lease_owner=9 WHERE id=901`)
	ipPolicyExec(t, w, `UPDATE artwork_sources SET revision=7 WHERE digest=?`, spec.sourceDigest)
	ipPolicyExec(t, w, `UPDATE artwork_jobs SET status='retry',provider='tmdb' WHERE id='job-upload-poster'`)
	turn, tx, b := ipPolicyTurn(t, w, r, IPArtworkUploadAccept)
	replay, err := applyIPArtworkUpload(t.Context(), turn, p, t.Context(), b, cmd)
	if err != nil || !replay.Outcome.Replayed || replay.Digest != originalUpload.Digest ||
		replay.Value != originalUpload.Value {
		t.Fatal("source change altered the original upload", replay, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)

	// At or below ACK is Retired, including the physically retained source.
	ipArtworkUploadAck(t, w, 1)
	turn, tx, b = ipPolicyTurn(t, w, r, IPArtworkUploadAccept)
	retired, err := applyIPArtworkUpload(t.Context(), turn, p, t.Context(), b, cmd)
	if err != nil || retired.Outcome.State != ResultRetired ||
		retired.Value != (ipArtworkUploadResult{}) || retired.Digest != (ResultDigest{}) {
		t.Fatal("retired upload served its result", retired, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
	ipArtworkUploadExpect(t, w, 1, `SELECT count(*) FROM artwork_sources WHERE digest=?`, spec.sourceDigest)
	ipArtworkUploadExpect(t, w, 1, `SELECT count(*) FROM artwork_selections WHERE digest=?`, spec.sourceDigest)
}

// A cancelled owner upload with already installed files retains the complete
// installed intent and every slot; it publishes no source and moves no counter.
func TestIPArtworkUploadCanceledWithInstalledFiles(t *testing.T) {
	w, r, p := ipArtworkUploadFixture(t)
	spec, slots := ipArtworkUploadSpec(), ipArtworkUploadSlots()
	cmd := ipArtworkUploadCommandFor(t, 1, 2, ipArtworkUploadOwner(), spec, slots,
		ipArtworkSelectionBefore{}, ipArtworkSelectionUploadFacts(spec.sourceDigest))
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	turn, tx, b := ipPolicyTurn(t, w, r, IPArtworkUploadAccept)
	out, err := applyIPArtworkUpload(t.Context(), turn, p, cancelled, b, cmd)
	if out != (ipArtworkUploadCommandResult{}) || err == nil {
		t.Fatal("cancelled upload admitted", out, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
	ipArtworkUploadClean(t, w)
	// The installed files stay charged for a later retry or reencode.
	ipArtworkUploadExpect(t, w, 4, `SELECT count(*) FROM artwork_material_slots WHERE intent_id=?`, spec.intentID)
	ipArtworkUploadExpect(t, w, 1,
		`SELECT count(*) FROM artwork_material_slots WHERE intent_id=? AND slot=3 AND digest=? AND bytes=?`,
		spec.intentID, spec.sourceDigest, int64(2<<20))
}

// A previous locked choice is pinned before it is replaced, so the old source,
// its blobs and the retained original stay charged for undo.
func TestIPArtworkUploadPreviousChoiceUndoStaysCharged(t *testing.T) {
	w, r, p := ipArtworkUploadFixture(t)
	previousDigest, previousBlobs := ipArtworkUploadSeedSource(t, w, 9, true)
	owner := ipArtworkUploadOwner()
	ipArtworkUploadSeedPrevious(t, w, owner, previousDigest)
	before := ipArtworkSelectionBeforeFor(t, w, owner, "poster")
	if !before.present || before.facts.digest != previousDigest {
		t.Fatal("previous lock not observed", before)
	}
	spec, slots := ipArtworkUploadSpec(), ipArtworkUploadSlots()
	cmd := ipArtworkUploadCommandFor(t, 1, 2, owner, spec, slots, before,
		ipArtworkSelectionUploadFacts(spec.sourceDigest))
	out, turn, tx, _ := ipArtworkUploadApply(t, w, r, p, cmd)
	if out.Outcome.State != Applied || out.Value.Previous != previousDigest ||
		out.Value.Selected != spec.sourceDigest || out.Value.SelectionRevision != 2 ||
		out.Value.History.Value != 1 {
		t.Fatal(out)
	}
	ipPolicyCommitTurn(t, turn, tx)
	ipArtworkUploadExpect(t, w, 1, `SELECT count(*) FROM artwork_sources WHERE digest=? AND original_bytes=? AND retiring=0`, previousDigest, int64(2<<20))
	for _, blob := range previousBlobs {
		ipArtworkUploadExpect(t, w, 1, `SELECT count(*) FROM artwork_blobs WHERE digest=? AND status='ready'`, blob)
	}
	ipArtworkUploadExpect(t, w, 1, `SELECT count(*) FROM metadata_owner_history_artwork WHERE digest=? AND role='poster'`, previousDigest)
	ipArtworkUploadExpect(t, w, 1, `SELECT count(*) FROM metadata_owner_history_artwork WHERE digest=? AND role='poster'`, spec.sourceDigest)
	ipArtworkUploadExpect(t, w, 1,
		`SELECT count(*) FROM artwork_command_receipts WHERE previous_digest=? AND selected_digest=? AND selection_revision=2`,
		previousDigest, spec.sourceDigest)
	ipArtworkUploadExpect(t, w, 1,
		`SELECT count(*) FROM artwork_selections WHERE choice='locked' AND digest=? AND revision=2`, spec.sourceDigest)
}

// Seeds the exact current locked row the caller last observed.
func ipArtworkUploadSeedPrevious(t *testing.T, w *executor, owner ipArtworkMaterialOwner, digest string) {
	t.Helper()
	kind, subject := ipArtworkSelectionNamespace(owner)
	ipPolicyExec(t, w, `INSERT INTO artwork_selections(kind,entity_id,role,choice,revision,digest,provider,image_id,origin,attribution,locale,effective_language,source_fence,actor,source_object_id,source_object_revision,observed_at) VALUES(?,?,'poster','locked',1,?,'upload','upload-old','upload','Upload','','en','upload-fence','owner',NULL,NULL,'2026-01-01T00:00:00Z')`,
		kind, subject, digest)
}

// Seeds one ready accepted source with three encoded blobs and (optionally) the
// retained original bytes. Unique to this task; it never collides with the
// IP-21 production readiness helper of a similar name.
func ipArtworkUploadSeedSource(t *testing.T, w *executor, seed int, original bool) (string, [3]string) {
	t.Helper()
	digest := ipArtworkMaterialHex(seed)
	var blobs [3]string
	for n := 0; n < 3; n++ {
		blobs[n] = ipArtworkMaterialHex(seed*10 + n + 1)
		ipPolicyExec(t, w, `INSERT INTO artwork_blobs(digest,mime,width,height,bytes,created_at) VALUES(?,?,?,?,?,?)`,
			blobs[n], "image/jpeg", int64(500+n), int64(750), int64(40000+n), "2026-01-01T00:00:00Z")
	}
	var originalBytes int64
	if original {
		originalBytes = 2 << 20
	}
	ipPolicyExec(t, w, `INSERT INTO artwork_sources(digest,accepted_mime,accepted_width,accepted_height,small_blob,large_blob,ambient_blob,large_edge,small_outlier,large_outlier,encoder_revision,created_at,revision,original_bytes,retiring,retirement_token) VALUES(?,'image/jpeg',3000,2000,?,?,?,1920,0,0,3,'2026-01-01T00:00:00Z',1,?,0,NULL)`,
		digest, blobs[0], blobs[1], blobs[2], originalBytes)
	return digest, blobs
}

// Every fresh witness is required before any source or selection byte.
func TestIPArtworkUploadFences(t *testing.T) {
	cases := []struct {
		name   string
		sql    []string
		mutate func(*ipArtworkUploadRequest)
	}{
		{"stale_entity_revision", []string{`UPDATE catalog_entities SET artwork_revision=2 WHERE id=778`}, nil},
		{"retired_entity", []string{`UPDATE catalog_entities SET retired=1 WHERE id=778`}, nil},
		{"retired_library", []string{`UPDATE catalog_libraries SET retired=1 WHERE id=777`}, nil},
		{"stale_language", []string{`UPDATE metadata_runtime_settings SET artwork_language='textless'`}, nil},
		{"stale_policy", []string{`UPDATE metadata_runtime_settings SET artwork_policy_revision=2`}, nil},
		{"stale_owner_lease", []string{`UPDATE artwork_subject_jobs SET job_lease_owner=8 WHERE id=901`}, nil},
		{"expired_owner_lease", []string{`UPDATE artwork_subject_jobs SET job_lease_expires_ms=1 WHERE id=901`}, nil},
		{"wrong_phase", []string{`UPDATE artwork_subject_jobs SET phase='providers' WHERE id=901`}, nil},
		{"stale_owner_revision", nil, func(q *ipArtworkUploadRequest) { q.owner.revision = 2 }},
		{"intent_prepared", []string{`UPDATE artwork_material_intents SET state='prepared',staged_slots=0,revision=1 WHERE id='job-upload-poster'`}, nil},
		{"intent_retiring", []string{`UPDATE artwork_material_intents SET state='retiring',retirement_token=X'0f000000000000000000000000000000' WHERE id='job-upload-poster'`}, nil},
		{"stored_source_changed", []string{`UPDATE artwork_material_intents SET source_digest=? WHERE id='job-upload-poster'`}, nil},
		{"stored_mime_changed", []string{`UPDATE artwork_material_intents SET accepted_mime='image/png' WHERE id='job-upload-poster'`}, nil},
		{"stored_encoder_changed", []string{`UPDATE artwork_material_intents SET encoder_revision=4 WHERE id='job-upload-poster'`}, nil},
		{"manifest_slot_changed", []string{`UPDATE artwork_material_slots SET digest=? WHERE intent_id='job-upload-poster' AND slot=1`}, nil},
		{"manifest_slot_missing", []string{`DELETE FROM artwork_material_slots WHERE intent_id='job-upload-poster' AND slot=2`}, nil},
		{"job_not_complete", []string{`UPDATE artwork_jobs SET status='pending' WHERE id='job-upload-poster'`}, nil},
		{"job_wrong_provider", []string{`UPDATE artwork_jobs SET provider='tmdb' WHERE id='job-upload-poster'`}, nil},
		{"job_wrong_subject", []string{`UPDATE artwork_jobs SET subject_job_id=NULL WHERE id='job-upload-poster'`}, nil},
		{"selection_before_changed", nil, func(q *ipArtworkUploadRequest) {
			q.selection.before = ipArtworkSelectionBefore{present: true, revision: 1, actor: "owner",
				facts: ipArtworkSelectionUploadFacts(ipArtworkMaterialHex(9))}
		}},
		{"selection_role_changed", nil, func(q *ipArtworkUploadRequest) { q.selection.role = "backdrop" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w, r, p := ipArtworkUploadFixture(t)
			spec, slots := ipArtworkUploadSpec(), ipArtworkUploadSlots()
			for _, query := range tc.sql {
				if strings.Contains(query, "?") {
					ipPolicyExec(t, w, query, ipArtworkMaterialHex(8))
				} else {
					ipPolicyExec(t, w, query)
				}
			}
			cmd := ipArtworkUploadCommandFor(t, 1, 2, ipArtworkUploadOwner(), spec, slots,
				ipArtworkSelectionBefore{}, ipArtworkSelectionUploadFacts(spec.sourceDigest))
			if tc.mutate != nil {
				tc.mutate(&cmd.request)
				digest, err := canonicalRequestDigest(IPArtworkUploadAccept, 1, cmd.identity, cmd.request)
				if err != nil {
					t.Fatal(err)
				}
				cmd.digest = digest
			}
			turn, tx, b := ipPolicyTurn(t, w, r, IPArtworkUploadAccept)
			out, err := applyIPArtworkUpload(t.Context(), turn, p, t.Context(), b, cmd)
			if out != (ipArtworkUploadCommandResult{}) || !errors.Is(err, ErrConflict) {
				t.Fatal("stale upload admitted", out, err)
			}
			ipPolicyRollbackTurn(t, turn, tx)
			ipArtworkUploadExpect(t, w, 0, `SELECT count(*) FROM artwork_sources`)
			ipArtworkUploadExpect(t, w, 0, `SELECT count(*) FROM artwork_blobs`)
			ipArtworkUploadExpect(t, w, 0, `SELECT count(*) FROM artwork_selections`)
			ipArtworkUploadExpect(t, w, 0, `SELECT count(*) FROM artwork_command_receipts`)
			ipArtworkUploadExpect(t, w, 0, `SELECT last_command_sequence FROM artwork_subject_jobs WHERE id=901`)
		})
	}
}

// Current authority, a real turn lifetime and a cancelled caller lifetime gate
// every upload before any custody byte.
func TestIPArtworkUploadCurrentAuthorityAndLifetime(t *testing.T) {
	for _, mode := range []string{"revoked_family", "forged_proof", "revoked_membership", "cancelled_lifetime"} {
		t.Run(mode, func(t *testing.T) {
			w, r, p := ipArtworkUploadFixture(t)
			spec, slots := ipArtworkUploadSpec(), ipArtworkUploadSlots()
			cmd := ipArtworkUploadCommandFor(t, 1, 2, ipArtworkUploadOwner(), spec, slots,
				ipArtworkSelectionBefore{}, ipArtworkSelectionUploadFacts(spec.sourceDigest))
			lifetime := context.Background()
			if mode == "cancelled_lifetime" {
				cancelled, cancel := context.WithCancel(t.Context())
				cancel()
				lifetime = cancelled
			} else {
				p = ipPolicyRevoke(t, w, p, mode)
			}
			turn, tx, b := ipPolicyTurn(t, w, r, IPArtworkUploadAccept)
			out, err := applyIPArtworkUpload(t.Context(), turn, p, lifetime, b, cmd)
			if out != (ipArtworkUploadCommandResult{}) || err == nil {
				t.Fatal("stale authority/cancelled lifetime admitted", out, err)
			}
			if mode != "cancelled_lifetime" && !errors.Is(err, ErrDisclosureDenied) {
				t.Fatal(err)
			}
			ipPolicyRollbackTurn(t, turn, tx)
			ipArtworkUploadClean(t, w)
		})
	}
}

// An uncertain COMMIT is durable truth only through a FRESH read-only
// connection; the quarantined pool serves no saved upload afterwards.
func TestIPArtworkUploadUnknownCommitFreshRead(t *testing.T) {
	w, r, p := ipArtworkUploadFixture(t)
	spec, slots := ipArtworkUploadSpec(), ipArtworkUploadSlots()
	total := ipArtworkMaterialSlotBytes(slots)
	cmd := ipArtworkUploadCommandFor(t, 1, 2, ipArtworkUploadOwner(), spec, slots,
		ipArtworkSelectionBefore{}, ipArtworkSelectionUploadFacts(spec.sourceDigest))
	path := ipPolicyDatabasePath(t, w)
	if err := w.db.Close(); err != nil {
		t.Fatal(err)
	}
	connector, err := sqliteconn.Connector(path, w.config)
	if err != nil {
		t.Fatal(err)
	}
	installFaultPool(w, &faultConnector{Connector: connector, afterCommit: true, replyError: true})
	turn, tx, b := ipPolicyTurn(t, w, r, IPArtworkUploadAccept)
	out, err := applyIPArtworkUpload(t.Context(), turn, p, t.Context(), b, cmd)
	if err != nil || out.Outcome.State != Applied {
		t.Fatal(out, err)
	}
	result, err := tx.commit()
	if result.state != commitUnknown || !errors.Is(err, ErrResolveRequired) {
		t.Fatal(result, err)
	}
	if again, e := applyIPArtworkUpload(t.Context(), turn, p, t.Context(), b, cmd); again != (ipArtworkUploadCommandResult{}) || e == nil {
		t.Fatal("uncertain frame served the saved upload", again, e)
	}
	for _, check := range []struct {
		query string
		want  int64
	}{
		{`SELECT count(*) FROM artwork_sources WHERE digest='` + spec.sourceDigest + `'`, 1},
		{`SELECT original_bytes FROM artwork_sources WHERE digest='` + spec.sourceDigest + `'`, 2 << 20},
		{`SELECT count(*) FROM artwork_blobs`, 3},
		{`SELECT CASE state WHEN 'published' THEN 1 ELSE 0 END FROM artwork_material_intents`, 1},
		{`SELECT revision FROM artwork_material_intents`, 3},
		{`SELECT count(*) FROM artwork_selections WHERE choice='locked'`, 1},
		{`SELECT count(*) FROM metadata_owner_history`, 1},
		{`SELECT count(*) FROM metadata_owner_history_artwork`, 1},
		{`SELECT count(*) FROM artwork_command_receipts`, 1},
		{`SELECT result_bytes FROM artwork_command_receipts`, total},
		{`SELECT last_command_sequence FROM artwork_subject_jobs WHERE id=901`, 1},
		{`SELECT artwork_revision FROM catalog_entities WHERE id=778`, 2},
	} {
		if got := ipPolicyFreshInt(t, path, check.query); got != check.want {
			t.Fatal("fresh read", check.query, got, check.want)
		}
	}
}

// The upload receipt is read by its OWN statement: a select/clear/material row
// at the same key, an over-long intent or a retirement token can never replay.
func TestIPArtworkUploadMalformedReceiptRefused(t *testing.T) {
	const receiptInsert = `INSERT INTO artwork_command_receipts(owner_id,command_incarnation,command_sequence,operation_id,request_digest,applied_ms,result_digest,command_kind,accepted_revision,result_count,result_bytes,result_complete,lease_generation,lease_until_ms,material_intent_id,role,choice,selection_revision,selected_digest,previous_digest,storage_revision,history_id,retirement_token) VALUES(901,X'01000000000000000000000000000000',1,X'02000000000000000000000000000000',X'0300000000000000000000000000000000000000000000000000000000000000',1,X'0400000000000000000000000000000000000000000000000000000000000000','upload_accept',3,4,?,1,1,?,?,'poster','locked',1,?,NULL,1,1,?)`
	for _, tc := range []struct {
		name   string
		mutate func(t *testing.T, w *executor, spec ipArtworkMaterialPublishSpec, slots []ipArtworkMaterialSlot)
	}{
		{"over_long_intent", func(t *testing.T, w *executor, spec ipArtworkMaterialPublishSpec, slots []ipArtworkMaterialSlot) {
			long := strings.Repeat("i", ipArtworkMaterialMaxIntent+1)
			ipPolicyExec(t, w, `INSERT INTO artwork_material_intents(id,owner_id,owner_incarnation,generation,revision,source_digest,accepted_mime,accepted_width,accepted_height,large_edge,encoder_revision,state,expected_slots,staged_slots) VALUES(?,901,X'01000000000000000000000000000000',1,1,?,'image/jpeg',3000,2000,1920,3,'prepared',4,0)`,
				long, spec.sourceDigest)
			ipPolicyExec(t, w, `INSERT INTO artwork_sources(digest,accepted_mime,accepted_width,accepted_height,small_blob,large_blob,ambient_blob,large_edge,small_outlier,large_outlier,encoder_revision,created_at,revision,original_bytes,retiring,retirement_token) VALUES(?,'image/jpeg',3000,2000,?,?,?,1920,0,0,3,'2026-01-01T00:00:00Z',1,? ,0,NULL)`,
				spec.sourceDigest, slots[0].digest, slots[1].digest, slots[2].digest, int64(2<<20))
			ipPolicyExec(t, w, receiptInsert, int64(1<<20), int64(1<<50), long, spec.sourceDigest, nil)
		}},
		{"retirement_token", func(t *testing.T, w *executor, spec ipArtworkMaterialPublishSpec, slots []ipArtworkMaterialSlot) {
			ipPolicyExec(t, w, `INSERT INTO artwork_sources(digest,accepted_mime,accepted_width,accepted_height,small_blob,large_blob,ambient_blob,large_edge,small_outlier,large_outlier,encoder_revision,created_at,revision,original_bytes,retiring,retirement_token) VALUES(?,'image/jpeg',3000,2000,?,?,?,1920,0,0,3,'2026-01-01T00:00:00Z',1,?,0,NULL)`,
				spec.sourceDigest, slots[0].digest, slots[1].digest, slots[2].digest, int64(2<<20))
			ipPolicyExec(t, w, receiptInsert, int64(1<<20), int64(1<<50), spec.intentID, spec.sourceDigest,
				[]byte{15, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0})
		}},
		{"foreign_kind", func(t *testing.T, w *executor, spec ipArtworkMaterialPublishSpec, slots []ipArtworkMaterialSlot) {
			ipPolicyExec(t, w, `INSERT INTO artwork_sources(digest,accepted_mime,accepted_width,accepted_height,small_blob,large_blob,ambient_blob,large_edge,small_outlier,large_outlier,encoder_revision,created_at,revision,original_bytes,retiring,retirement_token) VALUES(?,'image/jpeg',3000,2000,?,?,?,1920,0,0,3,'2026-01-01T00:00:00Z',1,?,0,NULL)`,
				spec.sourceDigest, slots[0].digest, slots[1].digest, slots[2].digest, int64(2<<20))
			ipPolicyExec(t, w, receiptInsert, int64(1<<20), int64(1<<50), spec.intentID, spec.sourceDigest, nil)
			ipPolicyExec(t, w, `UPDATE artwork_command_receipts SET command_kind='select' WHERE command_sequence=1`)
		}},
		{"wrong_selected_source", func(t *testing.T, w *executor, spec ipArtworkMaterialPublishSpec, slots []ipArtworkMaterialSlot) {
			ipPolicyExec(t, w, `INSERT INTO artwork_sources(digest,accepted_mime,accepted_width,accepted_height,small_blob,large_blob,ambient_blob,large_edge,small_outlier,large_outlier,encoder_revision,created_at,revision,original_bytes,retiring,retirement_token) VALUES(?,'image/jpeg',3000,2000,?,?,?,1920,0,0,3,'2026-01-01T00:00:00Z',1,?,0,NULL)`,
				spec.sourceDigest, slots[0].digest, slots[1].digest, slots[2].digest, int64(2<<20))
			other := ipArtworkMaterialHex(9)
			ipPolicyExec(t, w, `INSERT INTO artwork_sources(digest,accepted_mime,accepted_width,accepted_height,small_blob,large_blob,ambient_blob,large_edge,small_outlier,large_outlier,encoder_revision,created_at,revision,original_bytes,retiring,retirement_token) VALUES(?,'image/jpeg',3000,2000,?,?,?,1920,0,0,3,'2026-01-01T00:00:00Z',1,?,0,NULL)`,
				other, slots[0].digest, slots[1].digest, slots[2].digest, int64(2<<20))
			ipPolicyExec(t, w, receiptInsert, int64(1<<20), int64(1<<50), spec.intentID, other, nil)
		}},
		{"not_complete", func(t *testing.T, w *executor, spec ipArtworkMaterialPublishSpec, slots []ipArtworkMaterialSlot) {
			ipPolicyExec(t, w, `INSERT INTO artwork_sources(digest,accepted_mime,accepted_width,accepted_height,small_blob,large_blob,ambient_blob,large_edge,small_outlier,large_outlier,encoder_revision,created_at,revision,original_bytes,retiring,retirement_token) VALUES(?,'image/jpeg',3000,2000,?,?,?,1920,0,0,3,'2026-01-01T00:00:00Z',1,?,0,NULL)`,
				spec.sourceDigest, slots[0].digest, slots[1].digest, slots[2].digest, int64(2<<20))
			ipPolicyExec(t, w, receiptInsert, int64(1<<20), int64(1<<50), spec.intentID, spec.sourceDigest, nil)
			ipPolicyExec(t, w, `UPDATE artwork_command_receipts SET result_complete=0 WHERE command_sequence=1`)
		}},
		{"zero_lease", func(t *testing.T, w *executor, spec ipArtworkMaterialPublishSpec, slots []ipArtworkMaterialSlot) {
			ipPolicyExec(t, w, `INSERT INTO artwork_sources(digest,accepted_mime,accepted_width,accepted_height,small_blob,large_blob,ambient_blob,large_edge,small_outlier,large_outlier,encoder_revision,created_at,revision,original_bytes,retiring,retirement_token) VALUES(?,'image/jpeg',3000,2000,?,?,?,1920,0,0,3,'2026-01-01T00:00:00Z',1,?,0,NULL)`,
				spec.sourceDigest, slots[0].digest, slots[1].digest, slots[2].digest, int64(2<<20))
			ipPolicyExec(t, w, receiptInsert, int64(1<<20), int64(1<<50), spec.intentID, spec.sourceDigest, nil)
			ipPolicyExec(t, w, `UPDATE artwork_command_receipts SET lease_generation=0 WHERE command_sequence=1`)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, r, _ := ipArtworkUploadFixture(t)
			spec, slots := ipArtworkUploadSpec(), ipArtworkUploadSlots()
			// artwork_sources' three encoded blobs are FK parents. Insert them so
			// each malformed receipt is refused for its own reason, not a dangling
			// blob reference.
			for _, slot := range slots[:3] {
				ipPolicyExec(t, w, `INSERT INTO artwork_blobs(digest,mime,width,height,bytes,created_at) VALUES(?,?,?,?,?,?)`,
					slot.digest, slot.mime, int64(slot.width), int64(slot.height), int64(slot.bytes), "2026-01-01T00:00:00Z")
			}
			// The receipt's history_id=1 is a FK parent row too.
			ipPolicyExec(t, w, `INSERT INTO metadata_owner_history(id,kind,entity_id,base_revision,revision,trigger,actor,observed_at,before_json,after_json) VALUES(1,'entity',778,'1','2','fixture','owner','2026-01-01T00:00:00Z','{}','{}')`)
			tc.mutate(t, w, spec, slots)
			ipPolicyExec(t, w, `UPDATE artwork_subject_jobs SET last_command_sequence=1 WHERE id=901`)
			cmd := ipArtworkUploadCommandFor(t, 1, 2, ipArtworkUploadOwner(), spec, slots,
				ipArtworkSelectionBefore{}, ipArtworkSelectionUploadFacts(spec.sourceDigest))
			turn, tx, _ := ipPolicyTurn(t, w, r, IPArtworkUploadAccept)
			if _, err := ipArtworkUploadLookup(t.Context(), turn, cmd.request, cmd.identity, cmd.digest); !errors.Is(err, ErrResolveRequired) {
				t.Fatal("malformed upload receipt decoded", err)
			}
			ipPolicyRollbackTurn(t, turn, tx)
		})
	}
}

func TestIPArtworkUploadBoundsAndDescriptors(t *testing.T) {
	spec, slots := ipArtworkUploadSpec(), ipArtworkUploadSlots()
	owner := ipArtworkUploadOwner()
	identity := CommandIdentity{Incarnation: owner.incarnation, Sequence: 1, OperationID: [16]byte{2}}
	good := ipArtworkUploadRequest{owner: owner, spec: spec, slots: slots, provider: "upload",
		selection: ipArtworkUploadSelection{role: "poster", actor: "owner",
			after: ipArtworkSelectionUploadFacts(spec.sourceDigest)}}
	for name, mutate := range map[string]func(*ipArtworkUploadRequest){
		"both_namespaces": func(q *ipArtworkUploadRequest) { q.owner.personID = 778 },
		"no_namespace":    func(q *ipArtworkUploadRequest) { q.owner.entityID = 0 },
		"zero_lease":      func(q *ipArtworkUploadRequest) { q.owner.leaseUntilMS = 0 },
		"zero_source":     func(q *ipArtworkUploadRequest) { q.owner.sourceIncarnation = [16]byte{} },
		"foreign_incarn":  func(q *ipArtworkUploadRequest) { q.owner.incarnation = [16]byte{9} },
		"wrong_provider":  func(q *ipArtworkUploadRequest) { q.provider = "tmdb" },
		"zero_policy":     func(q *ipArtworkUploadRequest) { q.spec.policyRevision = 0 },
		"zero_encoder":    func(q *ipArtworkUploadRequest) { q.spec.encoderRevision = 0 },
		"bad_edge":        func(q *ipArtworkUploadRequest) { q.spec.largeEdge = 1024 },
		"two_slots":       func(q *ipArtworkUploadRequest) { q.spec.expectedSlots = 2 },
		"wrong_intent":    func(q *ipArtworkUploadRequest) { q.spec.intentRevision = 1 },
		"short_digest":    func(q *ipArtworkUploadRequest) { q.spec.sourceDigest = "abc" },
		"bad_mime":        func(q *ipArtworkUploadRequest) { q.spec.acceptedMIME = "image/gif" },
		"zero_width":      func(q *ipArtworkUploadRequest) { q.spec.acceptedWidth = 0 },
		"long_language":   func(q *ipArtworkUploadRequest) { q.spec.language = strings.Repeat("a", 65) },
		"empty_language":  func(q *ipArtworkUploadRequest) { q.spec.language = "" },
		"no_slots":        func(q *ipArtworkUploadRequest) { q.slots = nil },
		"gap":             func(q *ipArtworkUploadRequest) { q.slots[1].slot = 5 },
		"duplicate_slot":  func(q *ipArtworkUploadRequest) { q.slots[1].digest = q.slots[0].digest },
		"encoded_webp":    func(q *ipArtworkUploadRequest) { q.slots[1].mime = "image/webp" },
		"foreign_original": func(q *ipArtworkUploadRequest) {
			q.slots[3].digest = ipArtworkMaterialHex(9)
		},
		"bad_role":    func(q *ipArtworkUploadRequest) { q.selection.role = "thumb" },
		"empty_actor": func(q *ipArtworkUploadRequest) { q.selection.actor = "" },
		"long_actor":  func(q *ipArtworkUploadRequest) { q.selection.actor = strings.Repeat("a", ipTextActorMax+1) },
		"zero_before_rev": func(q *ipArtworkUploadRequest) {
			q.selection.before = ipArtworkSelectionBefore{present: true, actor: "owner",
				facts: ipArtworkSelectionUploadFacts(spec.sourceDigest)}
		},
		"after_other_source": func(q *ipArtworkUploadRequest) {
			q.selection.after = ipArtworkSelectionUploadFacts(ipArtworkMaterialHex(9))
		},
		"after_not_upload": func(q *ipArtworkUploadRequest) {
			facts := ipArtworkSelectionUploadFacts(spec.sourceDigest)
			facts.provider = "tmdb"
			q.selection.after = facts
		},
		"after_wrong_language": func(q *ipArtworkUploadRequest) {
			facts := ipArtworkSelectionUploadFacts(spec.sourceDigest)
			facts.language = "fr"
			q.selection.after = facts
		},
	} {
		t.Run(name, func(t *testing.T) {
			q := good
			q.slots = append([]ipArtworkMaterialSlot(nil), slots...)
			mutate(&q)
			if c, err := newIPArtworkUploadCommand(identity, q); c != nil || !errors.Is(err, ErrInvalid) {
				t.Fatal("malformed upload admitted", c, err)
			}
		})
	}
	// Bounded maximum: a 128-byte intent id and the largest admissible manifest.
	maxSpec := spec
	maxSpec.intentID = strings.Repeat("i", 128)
	maxSpec.expectedSlots = 3
	maxSlots := append([]ipArtworkMaterialSlot(nil), slots[:3]...)
	for n := range maxSlots {
		maxSlots[n].bytes = 40 << 20
	}
	maxReq := ipArtworkUploadRequest{owner: owner, spec: maxSpec, slots: maxSlots, provider: "upload",
		selection: ipArtworkUploadSelection{role: "poster", actor: "owner",
			after: ipArtworkSelectionUploadFacts(spec.sourceDigest)}}
	if _, err := newIPArtworkUploadCommand(identity, maxReq); err != nil {
		t.Fatal("bounded maximum upload refused", err)
	}
	expected, err := Describe(IPArtworkUploadAccept)
	if err != nil || expected != (Descriptor{IPArtworkUploadAccept, "ip_artwork_upload_accept", Normal}) ||
		expected.ID != 228 {
		t.Fatal(expected, err)
	}
	uploadGood := ipArtworkUploadSavedResult{IPArtworkUploadAccept, ipArtworkUploadResult{
		IntentID: "job-upload-poster", SourceDigest: spec.sourceDigest, Revision: 3, Slots: 4,
		Bytes: 1 << 20, OriginalBytes: 2 << 20, Role: "poster", SelectionRevision: 1,
		Selected: spec.sourceDigest, History: EvidenceInteger{Value: 1, Present: true}, AppliedMS: 1,
	}}
	if err := preflightIPArtworkUploadResult(uploadGood); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*ipArtworkUploadSavedResult){
		"wrong_kind":     func(v *ipArtworkUploadSavedResult) { v.Kind = IPArtworkReencode },
		"wrong_revision": func(v *ipArtworkUploadSavedResult) { v.Value.Revision = 2 },
		"zero_bytes":     func(v *ipArtworkUploadSavedResult) { v.Value.Bytes = 0 },
		"no_original":    func(v *ipArtworkUploadSavedResult) { v.Value.OriginalBytes = 0 },
		"empty_intent":   func(v *ipArtworkUploadSavedResult) { v.Value.IntentID = "" },
		"bad_role":       func(v *ipArtworkUploadSavedResult) { v.Value.Role = "" },
		"no_history":     func(v *ipArtworkUploadSavedResult) { v.Value.History = EvidenceInteger{} },
		"selected_moved": func(v *ipArtworkUploadSavedResult) { v.Value.Selected = ipArtworkMaterialHex(9) },
		"zero_selection": func(v *ipArtworkUploadSavedResult) { v.Value.SelectionRevision = 0 },
	} {
		v := uploadGood
		mutate(&v)
		if err := preflightIPArtworkUploadResult(v); !errors.Is(err, ErrInvalid) {
			t.Fatal(name, err)
		}
	}
}

// No filesystem, provider or HTTP access can happen inside the SQLite turn.
func TestIPArtworkUploadIOStaysOutsideHold(t *testing.T) {
	for _, file := range []string{
		"ip_artwork_upload.go",
		"ip_artwork_upload_statements.go",
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
