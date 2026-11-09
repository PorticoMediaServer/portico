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

// Group E is not embedded, so publish custody is qualified only against the
// exact draft fixtures: IP-19's material subset plus the draft source/blob
// revision, original-byte and retirement columns. No Capture/golden claim is
// made here. The command does no file or provider IO; the caller verifies every
// installed file before it repeats the manifest.
func ipArtworkMaterialPublishFixture(t *testing.T) (*executor, *ipPolicyRegistry, *usOwnerAuthorityProof) {
	t.Helper()
	w, keeper, p := ipPolicySecurityFixture(t)
	for _, name := range []string{
		"testdata/ip-artwork-material-draft.sql",
		"testdata/ip-artwork-material-publish-draft.sql",
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
	ipPolicyExec(t, w, `INSERT INTO catalog_people(id,token,identity_key,name,sort_name,artwork_incarnation,artwork_revision) VALUES(778,'person-token','person:key','Person','person',X'05000000000000000000000000000000',2)`)
	leaseUntil := int64(1) << 50
	ipPolicyExec(t, w, `INSERT INTO artwork_subject_jobs(id,entity_id,source_incarnation,source_revision,phase,job_revision,job_lease_owner,job_lease_generation,job_lease_expires_ms,command_incarnation) VALUES(901,778,X'04000000000000000000000000000000',1,'publish',1,7,1,?,X'01000000000000000000000000000000')`, leaseUntil)
	ipPolicyExec(t, w, `INSERT INTO artwork_subject_jobs(id,person_id,source_incarnation,source_revision,phase,job_revision,job_lease_owner,job_lease_generation,job_lease_expires_ms,command_incarnation) VALUES(902,778,X'05000000000000000000000000000000',2,'publish',1,7,1,?,X'06000000000000000000000000000000')`, leaseUntil)
	ipPolicyExec(t, w, `INSERT INTO artwork_jobs(id,kind,entity_id,role,choice,image_id,attribution,provider,selection_revision,status,created_at,job_next_attempt_ms,job_revision,job_lease_generation,job_lease_owner,job_lease_expires_ms,subject_job_id) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		"job-entity-poster", "entity", 778, "poster", "matched", "image-001", "Example", "tmdb", 1, "pending", "", 1, 1, 0, 0, 0, 901)
	ipPolicyExec(t, w, `INSERT INTO artwork_jobs(id,kind,entity_id,role,choice,image_id,attribution,provider,selection_revision,status,created_at,job_next_attempt_ms,job_revision,job_lease_generation,job_lease_owner,job_lease_expires_ms,subject_job_id) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		"job-person-portrait", "person", 778, "portrait", "locked", "upload-001", "Upload", "upload", 0, "pending", "", 1, 1, 0, 0, 0, 902)
	defs := append(usAuthorityDefinitions(), ipArtworkMaterialDefinitions()...)
	defs = append(defs, ipArtworkMaterialPublishDefinitions()...)
	r, err := stmttest.New(t, defs)
	if err != nil {
		t.Fatal(err)
	}
	return w, &ipPolicyRegistry{r, keeper}, p
}

func ipArtworkMaterialPublishSpecFor(spec ipArtworkMaterialSpec, generation int64) ipArtworkMaterialPublishSpec {
	return ipArtworkMaterialPublishSpec{
		intentID: spec.intentID, sourceDigest: spec.sourceDigest, acceptedMIME: spec.acceptedMIME,
		language: spec.language, policyRevision: spec.policyRevision,
		acceptedWidth: spec.acceptedWidth, acceptedHeight: spec.acceptedHeight,
		largeEdge: spec.largeEdge, encoderRevision: spec.encoderRevision,
		expectedSlots: spec.expectedSlots, intentGeneration: generation, intentRevision: 2,
	}
}

func ipArtworkMaterialPublishCommandFor(t *testing.T, sequence int64, operation byte,
	owner ipArtworkMaterialOwner, spec ipArtworkMaterialPublishSpec,
	slots []ipArtworkMaterialSlot) *ipArtworkMaterialPublishCommand {
	t.Helper()
	identity := CommandIdentity{Incarnation: owner.incarnation, Sequence: sequence, OperationID: [16]byte{operation}}
	c, err := newIPArtworkMaterialPublishCommand(identity, ipArtworkMaterialPublishRequest{
		owner: owner, spec: spec, slots: slots})
	if err != nil {
		t.Fatal("publish", err)
	}
	return c
}

// ipArtworkMaterialPublishInstalled declares and installs one material and ACKs
// it, leaving the owner at job revision 3 ready for publish. Only the command's
// own subject job is acknowledged, so the other fixture job keeps its floor. The
// returned installed command is IP-19's, reused to prove its retired path still
// resolves.
func ipArtworkMaterialPublishInstalled(t *testing.T, w *executor, r *ipPolicyRegistry,
	p *usOwnerAuthorityProof, four bool) (ipArtworkMaterialOwner, ipArtworkMaterialPublishSpec,
	[]ipArtworkMaterialSlot, *ipArtworkMaterialCommand) {
	t.Helper()
	owner, job := ipArtworkMaterialOwnerFor(), ipArtworkMaterialJobFor()
	spec := ipArtworkMaterialSpecFor(job, 4)
	if !four {
		owner, job = ipArtworkMaterialPersonOwnerFor(), ipArtworkMaterialPersonJobFor()
		spec = ipArtworkMaterialSpecFor(job, 3)
		spec.provider = "upload"
	}
	intent := ipArtworkMaterialIntentCommand(t, 1, 2, owner, job, spec)
	if out, turn, tx, _ := ipArtworkMaterialApply(t, w, r, p, IPArtworkMaterialIntent, intent); true {
		if out.Outcome.State != Applied || out.Value.Revision != 1 {
			t.Fatal("intent", out)
		}
		ipPolicyCommitTurn(t, turn, tx)
	}
	generation := int64(1)
	ipArtworkMaterialPublishAck(t, w, owner.id, 1)
	owner.revision = 2
	slots := ipArtworkMaterialSlotsFor(spec)
	installed := ipArtworkMaterialInstalledCommand(t, 2, 3, owner, spec, generation, 2, 1, slots)
	out, turn, tx, _ := ipArtworkMaterialApply(t, w, r, p, IPArtworkMaterialInstalled, installed)
	if out.Outcome.State != Applied || out.Value.Revision != 2 {
		t.Fatal("install", out)
	}
	ipPolicyCommitTurn(t, turn, tx)
	ipArtworkMaterialPublishAck(t, w, owner.id, 2)
	owner.revision = 3
	return owner, ipArtworkMaterialPublishSpecFor(spec, generation), slots, installed
}

func ipArtworkMaterialPublishAck(t *testing.T, w *executor, owner, sequence int64) {
	t.Helper()
	ipPolicyExec(t, w, `UPDATE artwork_subject_jobs SET acknowledged_command_sequence=? WHERE id=?`, sequence, owner)
}

func ipArtworkMaterialPublishApply(t *testing.T, w *executor, r *ipPolicyRegistry,
	p *usOwnerAuthorityProof, c *ipArtworkMaterialPublishCommand) (ipArtworkMaterialPublishCommandResult, *commandTurn, *transaction, ipPolicyBudget) {
	t.Helper()
	turn, tx, b := ipPolicyTurn(t, w, r, IPArtworkMaterialPublish)
	out, err := applyIPArtworkMaterialPublish(t.Context(), turn, p, t.Context(), b, c)
	if err != nil {
		t.Fatal("publish", out, err)
	}
	return out, turn, tx, b
}

func ipArtworkMaterialPublishExpect(t *testing.T, w *executor, want int64, query string, args ...any) {
	t.Helper()
	if got := ipPolicyQueryInt(t, w, query, args...); got != want {
		t.Fatal(query, got, want)
	}
}

// Nothing from a refused publish is retained: no blob, no source, no third
// receipt, and the owner floor is unchanged.
func ipArtworkMaterialPublishClean(t *testing.T, w *executor) {
	t.Helper()
	for _, check := range []struct {
		query string
		want  int64
	}{
		{`SELECT count(*) FROM artwork_blobs`, 0},
		{`SELECT count(*) FROM artwork_sources`, 0},
		{`SELECT count(*) FROM artwork_command_receipts`, 2},
		{`SELECT count(*) FROM artwork_command_receipts WHERE accepted_revision=3`, 0},
		{`SELECT last_command_sequence FROM artwork_subject_jobs WHERE id=901`, 2},
		{`SELECT job_revision FROM artwork_subject_jobs WHERE id=901`, 3},
		{`SELECT count(*) FROM artwork_selections`, 0},
		{`SELECT count(*) FROM metadata_owner_history`, 0},
	} {
		ipArtworkMaterialPublishExpect(t, w, check.want, check.query)
	}
}

// The accepted source, its three encoded blobs and the retained original are
// written together, and the original saved result replays after source, policy,
// lease and ACK changes.
func TestIPArtworkMaterialPublishFourSlotSourceAndReplay(t *testing.T) {
	w, r, p := ipArtworkMaterialPublishFixture(t)
	owner, spec, slots, installed := ipArtworkMaterialPublishInstalled(t, w, r, p, true)
	total := ipArtworkMaterialSlotBytes(slots)
	cmd := ipArtworkMaterialPublishCommandFor(t, 3, 4, owner, spec, slots)
	out, turn, tx, _ := ipArtworkMaterialPublishApply(t, w, r, p, cmd)
	want := ipArtworkMaterialPublishResult{IntentID: spec.intentID, Revision: 3, Slots: 4,
		Bytes: total, JobGeneration: spec.intentGeneration}
	if out.Outcome.State != Applied || out.Outcome.Replayed || out.Value != want ||
		out.Digest == (ResultDigest{}) {
		t.Fatal(out)
	}
	ipPolicyCommitTurn(t, turn, tx)
	originalPublish := out

	ipArtworkMaterialPublishExpect(t, w, 1, `SELECT count(*) FROM artwork_sources`)
	ipArtworkMaterialPublishExpect(t, w, 1,
		`SELECT count(*) FROM artwork_sources WHERE digest=? AND accepted_mime='image/jpeg' AND accepted_width=3000 AND accepted_height=2000 AND small_blob=? AND large_blob=? AND ambient_blob=? AND large_edge=1920 AND small_outlier=0 AND large_outlier=0 AND encoder_revision=3 AND revision=1 AND original_bytes=? AND retiring=0 AND retirement_token IS NULL`,
		spec.sourceDigest, slots[0].digest, slots[1].digest, slots[2].digest, int64(2<<20))
	ipArtworkMaterialPublishExpect(t, w, 3, `SELECT count(*) FROM artwork_blobs`)
	ipArtworkMaterialPublishExpect(t, w, 1,
		`SELECT count(*) FROM artwork_blobs WHERE digest=? AND mime='image/jpeg' AND width=500 AND height=750 AND bytes=40960 AND status='ready' AND storage_revision=1 AND deletion_token IS NULL`,
		slots[0].digest)
	ipArtworkMaterialPublishExpect(t, w, 1,
		`SELECT count(*) FROM artwork_blobs WHERE digest=? AND mime='image/jpeg' AND width=1280 AND height=720 AND bytes=122880`,
		slots[1].digest)
	ipArtworkMaterialPublishExpect(t, w, 1,
		`SELECT count(*) FROM artwork_blobs WHERE digest=? AND mime='image/png' AND width=780 AND height=1170 AND bytes=92160`,
		slots[2].digest)
	ipArtworkMaterialPublishExpect(t, w, 1,
		`SELECT CASE state WHEN 'published' THEN 1 ELSE 0 END FROM artwork_material_intents WHERE id=?`,
		spec.intentID)
	ipArtworkMaterialPublishExpect(t, w, 3,
		`SELECT revision FROM artwork_material_intents WHERE id=?`, spec.intentID)
	ipArtworkMaterialPublishExpect(t, w, 4,
		`SELECT count(*) FROM artwork_material_slots WHERE intent_id=?`, spec.intentID)
	ipArtworkMaterialPublishExpect(t, w, 1,
		`SELECT count(*) FROM artwork_material_slots WHERE intent_id=? AND slot=3 AND digest=? AND bytes=?`,
		spec.intentID, spec.sourceDigest, int64(2<<20))
	ipArtworkMaterialPublishExpect(t, w, 1,
		`SELECT count(*) FROM artwork_command_receipts WHERE command_kind='material_publish' AND accepted_revision=3 AND selected_digest=? AND storage_revision=1 AND result_count=4 AND result_bytes=? AND material_intent_id=? AND result_complete=1 AND lease_generation=1`,
		spec.sourceDigest, total, spec.intentID)
	ipArtworkMaterialPublishExpect(t, w, 3,
		`SELECT last_command_sequence FROM artwork_subject_jobs WHERE id=901`)
	ipArtworkMaterialPublishExpect(t, w, 4,
		`SELECT job_revision FROM artwork_subject_jobs WHERE id=901`)
	ipArtworkMaterialPublishExpect(t, w, 0, `SELECT count(*) FROM artwork_selections`)
	ipArtworkMaterialPublishExpect(t, w, 0, `SELECT count(*) FROM metadata_owner_history`)

	// IP-19's installed handoff remains retired at or below its ACK after a
	// publish; publish neither rewrote nor reopened the earlier receipt.
	turn, tx, b := ipPolicyTurn(t, w, r, IPArtworkMaterialInstalled)
	retired, err := applyIPArtworkMaterial(t.Context(), turn, p, t.Context(), b, IPArtworkMaterialInstalled, installed)
	if err != nil || retired.Outcome.State != ResultRetired || retired.Value != (ipArtworkMaterialResult{}) {
		t.Fatal("installed handoff after publish", retired, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)

	// The original publish replays exactly after every fresh witness moves.
	ipPolicyExec(t, w, `UPDATE catalog_entities SET artwork_revision=9 WHERE id=778`)
	ipPolicyExec(t, w, `UPDATE metadata_runtime_settings SET artwork_language='textless',artwork_policy_revision=9`)
	ipPolicyExec(t, w, `UPDATE artwork_subject_jobs SET job_revision=99,job_lease_owner=9 WHERE id=901`)
	ipPolicyExec(t, w, `UPDATE artwork_sources SET revision=7 WHERE digest=?`, spec.sourceDigest)
	turn, tx, b = ipPolicyTurn(t, w, r, IPArtworkMaterialPublish)
	replay, err := applyIPArtworkMaterialPublish(t.Context(), turn, p, t.Context(), b, cmd)
	if err != nil || !replay.Outcome.Replayed || replay.Digest != originalPublish.Digest ||
		replay.Value != originalPublish.Value {
		t.Fatal("source change altered the original publish", replay, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)

	// At or below ACK is Retired, including the physically retained source.
	ipArtworkMaterialPublishAck(t, w, 901, 3)
	turn, tx, b = ipPolicyTurn(t, w, r, IPArtworkMaterialPublish)
	retiredPublish, err := applyIPArtworkMaterialPublish(t.Context(), turn, p, t.Context(), b, cmd)
	if err != nil || retiredPublish.Outcome.State != ResultRetired ||
		retiredPublish.Value != (ipArtworkMaterialPublishResult{}) || retiredPublish.Digest != (ResultDigest{}) {
		t.Fatal("retired publish served its result", retiredPublish, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
	ipArtworkMaterialPublishExpect(t, w, 1, `SELECT count(*) FROM artwork_sources WHERE digest=?`, spec.sourceDigest)
}

// Three complete variants without a retained original: the source records zero
// original bytes and no slot 3 is invented.
func TestIPArtworkMaterialPublishThreeSlotNoOriginal(t *testing.T) {
	w, r, p := ipArtworkMaterialPublishFixture(t)
	owner, spec, slots, _ := ipArtworkMaterialPublishInstalled(t, w, r, p, false)
	total := ipArtworkMaterialSlotBytes(slots)
	cmd := ipArtworkMaterialPublishCommandFor(t, 3, 4, owner, spec, slots)
	out, turn, tx, _ := ipArtworkMaterialPublishApply(t, w, r, p, cmd)
	if out.Outcome.State != Applied ||
		out.Value != (ipArtworkMaterialPublishResult{IntentID: spec.intentID, Revision: 3, Slots: 3, Bytes: total, JobGeneration: spec.intentGeneration}) {
		t.Fatal(out)
	}
	ipPolicyCommitTurn(t, turn, tx)
	ipArtworkMaterialPublishExpect(t, w, 3, `SELECT count(*) FROM artwork_blobs`)
	ipArtworkMaterialPublishExpect(t, w, 1, `SELECT count(*) FROM artwork_sources WHERE digest=?`, spec.sourceDigest)
	ipArtworkMaterialPublishExpect(t, w, 0, `SELECT original_bytes FROM artwork_sources WHERE digest=?`, spec.sourceDigest)
	ipArtworkMaterialPublishExpect(t, w, 1, `SELECT revision FROM artwork_sources WHERE digest=?`, spec.sourceDigest)
	ipArtworkMaterialPublishExpect(t, w, 3, `SELECT count(*) FROM artwork_material_slots`)
	ipArtworkMaterialPublishExpect(t, w, 0, `SELECT count(*) FROM artwork_material_slots WHERE slot=3`)
	ipArtworkMaterialPublishExpect(t, w, 1, `SELECT count(*) FROM artwork_material_intents WHERE owner_id=902`)
	ipArtworkMaterialPublishExpect(t, w, 0, `SELECT count(*) FROM artwork_material_intents WHERE owner_id=901`)
	ipArtworkMaterialPublishExpect(t, w, 1, `SELECT count(*) FROM artwork_command_receipts WHERE accepted_revision=3`)
}

// Every fresh witness is required before any blob or source byte is written.
func TestIPArtworkMaterialPublishFences(t *testing.T) {
	cases := []struct {
		name   string
		sql    []string
		mutate func(*ipArtworkMaterialPublishRequest)
	}{
		{"stale_source_revision", []string{`UPDATE catalog_entities SET artwork_revision=2 WHERE id=778`}, nil},
		{"retired_entity", []string{`UPDATE catalog_entities SET retired=1 WHERE id=778`}, nil},
		{"retired_library", []string{`UPDATE catalog_libraries SET retired=1 WHERE id=777`}, nil},
		{"stale_language", []string{`UPDATE metadata_runtime_settings SET artwork_language='textless'`}, nil},
		{"stale_policy", []string{`UPDATE metadata_runtime_settings SET artwork_policy_revision=2`}, nil},
		{"stale_owner_lease", []string{`UPDATE artwork_subject_jobs SET job_lease_owner=8 WHERE id=901`}, nil},
		{"expired_owner_lease", []string{`UPDATE artwork_subject_jobs SET job_lease_expires_ms=1 WHERE id=901`}, nil},
		{"wrong_phase", []string{`UPDATE artwork_subject_jobs SET phase='providers' WHERE id=901`}, nil},
		{"stale_job_revision", nil, func(q *ipArtworkMaterialPublishRequest) { q.owner.revision = 2 }},
		{"intent_prepared", []string{`UPDATE artwork_material_intents SET state='prepared' WHERE id='job-entity-poster'`}, nil},
		{"intent_published", []string{`UPDATE artwork_material_intents SET state='published' WHERE id='job-entity-poster'`}, nil},
		{"intent_retiring", []string{`UPDATE artwork_material_intents SET state='retiring',retirement_token=X'0f000000000000000000000000000000' WHERE id='job-entity-poster'`}, nil},
		{"stored_source_changed", []string{`UPDATE artwork_material_intents SET source_digest=? WHERE id='job-entity-poster'`}, nil},
		{"stored_mime_changed", []string{`UPDATE artwork_material_intents SET accepted_mime='image/png' WHERE id='job-entity-poster'`}, nil},
		{"stored_width_changed", []string{`UPDATE artwork_material_intents SET accepted_width=3001 WHERE id='job-entity-poster'`}, nil},
		{"stored_edge_changed", []string{`UPDATE artwork_material_intents SET large_edge=1280 WHERE id='job-entity-poster'`}, nil},
		{"stored_encoder_changed", []string{`UPDATE artwork_material_intents SET encoder_revision=4 WHERE id='job-entity-poster'`}, nil},
		{"stored_intent_revision", []string{`UPDATE artwork_material_intents SET revision=1 WHERE id='job-entity-poster'`}, nil},
		{"manifest_slot_changed", []string{`UPDATE artwork_material_slots SET digest=? WHERE intent_id='job-entity-poster' AND slot=1`}, nil},
		{"manifest_slot_missing", []string{`DELETE FROM artwork_material_slots WHERE intent_id='job-entity-poster' AND slot=2`}, nil},
		{"intent_generation", nil, func(q *ipArtworkMaterialPublishRequest) { q.spec.intentGeneration = 2 }},
		{"request_slot_changed", nil, func(q *ipArtworkMaterialPublishRequest) { q.slots[1].digest = ipArtworkMaterialHex(9) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w, r, p := ipArtworkMaterialPublishFixture(t)
			owner, spec, slots, _ := ipArtworkMaterialPublishInstalled(t, w, r, p, true)
			for _, query := range tc.sql {
				if strings.Contains(query, "?") {
					ipPolicyExec(t, w, query, ipArtworkMaterialHex(8))
				} else {
					ipPolicyExec(t, w, query)
				}
			}
			cmd := ipArtworkMaterialPublishCommandFor(t, 3, 4, owner, spec, slots)
			if tc.mutate != nil {
				tc.mutate(&cmd.request)
				digest, err := canonicalRequestDigest(IPArtworkMaterialPublish, 1, cmd.identity, cmd.request)
				if err != nil {
					t.Fatal(err)
				}
				cmd.digest = digest
			}
			turn, tx, b := ipPolicyTurn(t, w, r, IPArtworkMaterialPublish)
			out, err := applyIPArtworkMaterialPublish(t.Context(), turn, p, t.Context(), b, cmd)
			if out != (ipArtworkMaterialPublishCommandResult{}) || !errors.Is(err, ErrConflict) {
				t.Fatal("stale publish admitted", out, err)
			}
			ipPolicyRollbackTurn(t, turn, tx)
			ipArtworkMaterialPublishClean(t, w)
		})
	}
}

// Blob custody never overwrites changed bytes and a source digest is never
// republished, so an installed/unpublished/retiring original stays charged.
func TestIPArtworkMaterialPublishCustodyRefusals(t *testing.T) {
	const blobInsert = `INSERT INTO artwork_blobs(digest,mime,width,height,bytes,created_at) VALUES(?,?,?,?,?,?)`
	const sourceInsert = `INSERT INTO artwork_sources(digest,accepted_mime,accepted_width,accepted_height,small_blob,large_blob,ambient_blob,large_edge,small_outlier,large_outlier,encoder_revision,created_at,original_bytes,retiring,retirement_token) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`
	t.Run("changed_blob_refused", func(t *testing.T) {
		w, r, p := ipArtworkMaterialPublishFixture(t)
		owner, spec, slots, _ := ipArtworkMaterialPublishInstalled(t, w, r, p, true)
		ipPolicyExec(t, w, blobInsert, slots[0].digest, "image/jpeg", int64(501), int64(750),
			int64(40960), "2026-01-01T00:00:00Z")
		cmd := ipArtworkMaterialPublishCommandFor(t, 3, 4, owner, spec, slots)
		turn, tx, b := ipPolicyTurn(t, w, r, IPArtworkMaterialPublish)
		out, err := applyIPArtworkMaterialPublish(t.Context(), turn, p, t.Context(), b, cmd)
		if out != (ipArtworkMaterialPublishCommandResult{}) || !errors.Is(err, ErrConflict) {
			t.Fatal("changed blob admitted", out, err)
		}
		ipPolicyRollbackTurn(t, turn, tx)
		ipArtworkMaterialPublishExpect(t, w, 0, `SELECT count(*) FROM artwork_sources`)
		ipArtworkMaterialPublishExpect(t, w, 2, `SELECT count(*) FROM artwork_command_receipts`)
		ipArtworkMaterialPublishExpect(t, w, 1, `SELECT count(*) FROM artwork_blobs`)
		ipArtworkMaterialPublishExpect(t, w, 501, `SELECT width FROM artwork_blobs WHERE digest=?`, slots[0].digest)
		ipArtworkMaterialPublishExpect(t, w, 2, `SELECT last_command_sequence FROM artwork_subject_jobs WHERE id=901`)
	})
	t.Run("identical_shared_blob_reused", func(t *testing.T) {
		w, r, p := ipArtworkMaterialPublishFixture(t)
		owner, spec, slots, _ := ipArtworkMaterialPublishInstalled(t, w, r, p, true)
		ipPolicyExec(t, w, blobInsert, slots[0].digest, "image/jpeg", int64(500), int64(750),
			int64(40960), "2026-01-01T00:00:00Z")
		cmd := ipArtworkMaterialPublishCommandFor(t, 3, 4, owner, spec, slots)
		out, turn, tx, _ := ipArtworkMaterialPublishApply(t, w, r, p, cmd)
		if out.Outcome.State != Applied {
			t.Fatal(out)
		}
		ipPolicyCommitTurn(t, turn, tx)
		ipArtworkMaterialPublishExpect(t, w, 3, `SELECT count(*) FROM artwork_blobs`)
		ipArtworkMaterialPublishExpect(t, w, 1,
			`SELECT count(*) FROM artwork_blobs WHERE digest=? AND created_at='2026-01-01T00:00:00Z' AND status='ready'`,
			slots[0].digest)
	})
	t.Run("retiring_original_refused", func(t *testing.T) {
		w, r, p := ipArtworkMaterialPublishFixture(t)
		owner, spec, slots, _ := ipArtworkMaterialPublishInstalled(t, w, r, p, true)
		ipPolicyExec(t, w, blobInsert, spec.sourceDigest, "image/jpeg", int64(3000), int64(2000),
			int64(2<<20), "2026-01-01T00:00:00Z")
		ipPolicyExec(t, w, sourceInsert, spec.sourceDigest, "image/jpeg", int64(3000), int64(2000),
			spec.sourceDigest, spec.sourceDigest, spec.sourceDigest, int64(1920), int64(0), int64(0),
			int64(3), "2026-01-01T00:00:00Z", int64(2<<20), int64(1),
			[]byte{15, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0})
		cmd := ipArtworkMaterialPublishCommandFor(t, 3, 4, owner, spec, slots)
		turn, tx, b := ipPolicyTurn(t, w, r, IPArtworkMaterialPublish)
		out, err := applyIPArtworkMaterialPublish(t.Context(), turn, p, t.Context(), b, cmd)
		if out != (ipArtworkMaterialPublishCommandResult{}) || !errors.Is(err, ErrConflict) {
			t.Fatal("retiring original republished", out, err)
		}
		ipPolicyRollbackTurn(t, turn, tx)
		ipArtworkMaterialPublishExpect(t, w, 1,
			`SELECT count(*) FROM artwork_sources WHERE digest=? AND retiring=1 AND retirement_token IS NOT NULL AND original_bytes=?`,
			spec.sourceDigest, int64(2<<20))
		ipArtworkMaterialPublishExpect(t, w, 4, `SELECT count(*) FROM artwork_material_slots`)
		ipArtworkMaterialPublishExpect(t, w, 2, `SELECT count(*) FROM artwork_command_receipts`)
		ipArtworkMaterialPublishExpect(t, w, 2, `SELECT last_command_sequence FROM artwork_subject_jobs WHERE id=901`)
	})
}

// Current authority, a real turn lifetime and a cancelled caller lifetime gate
// every publish before any custody byte.
func TestIPArtworkMaterialPublishCurrentAuthorityAndLifetime(t *testing.T) {
	for _, mode := range []string{"revoked_family", "forged_proof", "revoked_membership", "cancelled_lifetime"} {
		t.Run(mode, func(t *testing.T) {
			w, r, p := ipArtworkMaterialPublishFixture(t)
			owner, spec, slots, _ := ipArtworkMaterialPublishInstalled(t, w, r, p, true)
			cmd := ipArtworkMaterialPublishCommandFor(t, 3, 4, owner, spec, slots)
			lifetime := context.Background()
			if mode == "cancelled_lifetime" {
				cancelled, cancel := context.WithCancel(t.Context())
				cancel()
				lifetime = cancelled
			} else {
				p = ipPolicyRevoke(t, w, p, mode)
			}
			turn, tx, b := ipPolicyTurn(t, w, r, IPArtworkMaterialPublish)
			out, err := applyIPArtworkMaterialPublish(t.Context(), turn, p, lifetime, b, cmd)
			if out != (ipArtworkMaterialPublishCommandResult{}) || err == nil {
				t.Fatal("stale authority/cancelled lifetime admitted", out, err)
			}
			if mode != "cancelled_lifetime" && !errors.Is(err, ErrDisclosureDenied) {
				t.Fatal(err)
			}
			ipPolicyRollbackTurn(t, turn, tx)
			ipArtworkMaterialPublishClean(t, w)
		})
	}
}

// An uncertain COMMIT is durable truth only through a FRESH read-only
// connection; the quarantined pool serves no saved publish afterwards.
func TestIPArtworkMaterialPublishUnknownCommitFreshRead(t *testing.T) {
	w, r, p := ipArtworkMaterialPublishFixture(t)
	owner, spec, slots, _ := ipArtworkMaterialPublishInstalled(t, w, r, p, true)
	total := ipArtworkMaterialSlotBytes(slots)
	cmd := ipArtworkMaterialPublishCommandFor(t, 3, 4, owner, spec, slots)
	path := ipPolicyDatabasePath(t, w)
	if err := w.db.Close(); err != nil {
		t.Fatal(err)
	}
	connector, err := sqliteconn.Connector(path, w.config)
	if err != nil {
		t.Fatal(err)
	}
	installFaultPool(w, &faultConnector{Connector: connector, afterCommit: true, replyError: true})
	turn, tx, b := ipPolicyTurn(t, w, r, IPArtworkMaterialPublish)
	out, err := applyIPArtworkMaterialPublish(t.Context(), turn, p, t.Context(), b, cmd)
	if err != nil || out.Outcome.State != Applied {
		t.Fatal(out, err)
	}
	result, err := tx.commit()
	if result.state != commitUnknown || !errors.Is(err, ErrResolveRequired) {
		t.Fatal(result, err)
	}
	if again, e := applyIPArtworkMaterialPublish(t.Context(), turn, p, t.Context(), b, cmd); again != (ipArtworkMaterialPublishCommandResult{}) || e == nil {
		t.Fatal("uncertain frame served the saved publish", again, e)
	}
	for _, check := range []struct {
		query string
		want  int64
	}{
		{`SELECT count(*) FROM artwork_blobs`, 3},
		{`SELECT count(*) FROM artwork_sources WHERE digest='` + spec.sourceDigest + `'`, 1},
		{`SELECT original_bytes FROM artwork_sources WHERE digest='` + spec.sourceDigest + `'`, 2 << 20},
		{`SELECT CASE state WHEN 'published' THEN 1 ELSE 0 END FROM artwork_material_intents`, 1},
		{`SELECT revision FROM artwork_material_intents`, 3},
		{`SELECT count(*) FROM artwork_material_slots`, 4},
		{`SELECT count(*) FROM artwork_command_receipts`, 3},
		{`SELECT accepted_revision FROM artwork_command_receipts WHERE command_sequence=3`, 3},
		{`SELECT result_bytes FROM artwork_command_receipts WHERE command_sequence=3`, total},
		{`SELECT last_command_sequence FROM artwork_subject_jobs WHERE id=901`, 3},
	} {
		if got := ipPolicyFreshInt(t, path, check.query); got != check.want {
			t.Fatal("fresh read", check.query, got, check.want)
		}
	}
}

// The publish receipt is read by its OWN statement: an over-long pinned intent,
// a retirement token or an IP-19 handoff row at the same key can never replay as
// a publish.
func TestIPArtworkMaterialPublishMalformedReceiptRefused(t *testing.T) {
	const receiptInsert = `INSERT INTO artwork_command_receipts(owner_id,command_incarnation,command_sequence,operation_id,request_digest,applied_ms,result_digest,command_kind,accepted_revision,result_count,result_bytes,result_complete,lease_generation,lease_until_ms,material_intent_id,selected_digest,storage_revision,retirement_token) VALUES(901,X'01000000000000000000000000000000',3,X'0a000000000000000000000000000000',X'0300000000000000000000000000000000000000000000000000000000000000',1,X'0400000000000000000000000000000000000000000000000000000000000000','material_publish',?,?,?,?,1,?,?,?,?,?)`
	const sourceSeed = `INSERT INTO artwork_blobs(digest,mime,width,height,bytes,created_at) VALUES(?,'image/jpeg',3000,2000,2097152,'2026-01-01T00:00:00Z'); INSERT INTO artwork_sources(digest,accepted_mime,accepted_width,accepted_height,small_blob,large_blob,ambient_blob,large_edge,small_outlier,large_outlier,encoder_revision,created_at,original_bytes) VALUES(?,'image/jpeg',3000,2000,?,?,?,1920,0,0,3,'2026-01-01T00:00:00Z',2097152)`
	cases := []struct {
		name string
		seed func(t *testing.T, w *executor, spec ipArtworkMaterialPublishSpec, slots []ipArtworkMaterialSlot)
	}{
		{"over_long_intent", func(t *testing.T, w *executor, spec ipArtworkMaterialPublishSpec, slots []ipArtworkMaterialSlot) {
			long := strings.Repeat("i", ipArtworkMaterialMaxIntent+1)
			ipPolicyExec(t, w, `INSERT INTO artwork_material_intents(id,owner_id,owner_incarnation,generation,revision,source_digest,accepted_mime,accepted_width,accepted_height,large_edge,encoder_revision,state,expected_slots,staged_slots) VALUES(?,901,X'01000000000000000000000000000000',1,1,?,'image/jpeg',3000,2000,1920,3,'prepared',4,0)`,
				long, ipArtworkMaterialHex(1))
			ipPolicyExec(t, w, sourceSeed, spec.sourceDigest, spec.sourceDigest, spec.sourceDigest,
				spec.sourceDigest, spec.sourceDigest)
			ipPolicyExec(t, w, receiptInsert, int64(3), int64(4), int64(1<<20), int64(1),
				int64(1)<<50, long, spec.sourceDigest, int64(1), nil)
		}},
		{"retirement_token", func(t *testing.T, w *executor, spec ipArtworkMaterialPublishSpec, slots []ipArtworkMaterialSlot) {
			ipPolicyExec(t, w, sourceSeed, spec.sourceDigest, spec.sourceDigest, spec.sourceDigest,
				spec.sourceDigest, spec.sourceDigest)
			ipPolicyExec(t, w, receiptInsert, int64(3), int64(4), int64(1<<20), int64(1),
				int64(1)<<50, spec.intentID, spec.sourceDigest, int64(1),
				[]byte{15, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0})
		}},
		{"missing_selected_witness", func(t *testing.T, w *executor, spec ipArtworkMaterialPublishSpec, slots []ipArtworkMaterialSlot) {
			ipPolicyExec(t, w, receiptInsert, int64(2), int64(4), int64(1<<20), int64(1),
				int64(1)<<50, spec.intentID, nil, nil, nil)
		}},
		{"handoff_revision_two", func(t *testing.T, w *executor, spec ipArtworkMaterialPublishSpec, slots []ipArtworkMaterialSlot) {
			ipPolicyExec(t, w, sourceSeed, spec.sourceDigest, spec.sourceDigest, spec.sourceDigest,
				spec.sourceDigest, spec.sourceDigest)
			ipPolicyExec(t, w, receiptInsert, int64(2), int64(4), int64(1<<20), int64(1),
				int64(1)<<50, spec.intentID, spec.sourceDigest, int64(1), nil)
		}},
		{"foreign_kind_with_publish_witness", func(t *testing.T, w *executor, spec ipArtworkMaterialPublishSpec, slots []ipArtworkMaterialSlot) {
			ipPolicyExec(t, w, sourceSeed, spec.sourceDigest, spec.sourceDigest, spec.sourceDigest,
				spec.sourceDigest, spec.sourceDigest)
			ipPolicyExec(t, w, receiptInsert, int64(3), int64(4), int64(1<<20), int64(1),
				int64(1)<<50, spec.intentID, spec.sourceDigest, int64(1), nil)
			ipPolicyExec(t, w, `UPDATE artwork_command_receipts SET command_kind='job_claim' WHERE command_sequence=3`)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w, r, p := ipArtworkMaterialPublishFixture(t)
			owner, spec, slots, _ := ipArtworkMaterialPublishInstalled(t, w, r, p, true)
			tc.seed(t, w, spec, slots)
			ipPolicyExec(t, w, `UPDATE artwork_subject_jobs SET last_command_sequence=3 WHERE id=901`)
			cmd := ipArtworkMaterialPublishCommandFor(t, 3, 4, owner, spec, slots)
			turn, tx, _ := ipPolicyTurn(t, w, r, IPArtworkMaterialPublish)
			if _, err := ipArtworkMaterialPublishLookup(t.Context(), turn, cmd.request, cmd.identity, cmd.digest); !errors.Is(err, ErrResolveRequired) {
				t.Fatal("malformed publish receipt decoded", err)
			}
			ipPolicyRollbackTurn(t, turn, tx)
		})
	}
}

// Sequence, identity and request-digest integrity: no implicit allocation, no
// second publish for the same original result, no foreign owner.
func TestIPArtworkMaterialPublishSequenceAndIdentity(t *testing.T) {
	t.Run("sequence_gap", func(t *testing.T) {
		w, r, p := ipArtworkMaterialPublishFixture(t)
		owner, spec, slots, _ := ipArtworkMaterialPublishInstalled(t, w, r, p, true)
		cmd := ipArtworkMaterialPublishCommandFor(t, 4, 5, owner, spec, slots)
		turn, tx, b := ipPolicyTurn(t, w, r, IPArtworkMaterialPublish)
		out, err := applyIPArtworkMaterialPublish(t.Context(), turn, p, t.Context(), b, cmd)
		if out != (ipArtworkMaterialPublishCommandResult{}) || !errors.Is(err, ErrConflict) {
			t.Fatal("sequence gap admitted", out, err)
		}
		ipPolicyRollbackTurn(t, turn, tx)
		ipArtworkMaterialPublishClean(t, w)
	})
	t.Run("conflicting_request", func(t *testing.T) {
		w, r, p := ipArtworkMaterialPublishFixture(t)
		owner, spec, slots, _ := ipArtworkMaterialPublishInstalled(t, w, r, p, true)
		first := ipArtworkMaterialPublishCommandFor(t, 3, 4, owner, spec, slots)
		if _, turn, tx, _ := ipArtworkMaterialPublishApply(t, w, r, p, first); true {
			ipPolicyCommitTurn(t, turn, tx)
		}
		conflicting := ipArtworkMaterialPublishCommandFor(t, 3, 4, owner, spec, slots)
		conflicting.request.spec.encoderRevision = 4
		conflicting.digest = first.digest
		turn, tx, b := ipPolicyTurn(t, w, r, IPArtworkMaterialPublish)
		out, err := applyIPArtworkMaterialPublish(t.Context(), turn, p, t.Context(), b, conflicting)
		if out != (ipArtworkMaterialPublishCommandResult{}) || err == nil {
			t.Fatal("tampered request admitted", out, err)
		}
		ipPolicyRollbackTurn(t, turn, tx)
		ipArtworkMaterialPublishExpect(t, w, 3, `SELECT count(*) FROM artwork_command_receipts`)
	})
	t.Run("missing_owner", func(t *testing.T) {
		w, r, p := ipArtworkMaterialPublishFixture(t)
		owner, spec, slots, _ := ipArtworkMaterialPublishInstalled(t, w, r, p, true)
		owner.id = 999
		cmd := ipArtworkMaterialPublishCommandFor(t, 3, 4, owner, spec, slots)
		turn, tx, b := ipPolicyTurn(t, w, r, IPArtworkMaterialPublish)
		out, err := applyIPArtworkMaterialPublish(t.Context(), turn, p, t.Context(), b, cmd)
		if out != (ipArtworkMaterialPublishCommandResult{}) || !errors.Is(err, ErrResolveRequired) {
			t.Fatal("missing owner admitted", out, err)
		}
		ipPolicyRollbackTurn(t, turn, tx)
	})
	t.Run("foreign_incarnation", func(t *testing.T) {
		w, r, p := ipArtworkMaterialPublishFixture(t)
		owner, spec, slots, _ := ipArtworkMaterialPublishInstalled(t, w, r, p, true)
		owner.incarnation = [16]byte{9}
		cmd := ipArtworkMaterialPublishCommandFor(t, 3, 4, owner, spec, slots)
		turn, tx, b := ipPolicyTurn(t, w, r, IPArtworkMaterialPublish)
		out, err := applyIPArtworkMaterialPublish(t.Context(), turn, p, t.Context(), b, cmd)
		if out != (ipArtworkMaterialPublishCommandResult{}) || !errors.Is(err, ErrResolveRequired) {
			t.Fatal("foreign incarnation admitted", out, err)
		}
		ipPolicyRollbackTurn(t, turn, tx)
	})
}

// A crashed publish rolls back whole; the restart re-verifies the same files
// and commits the same handoff.
func TestIPArtworkMaterialPublishInterruptedRetry(t *testing.T) {
	w, r, p := ipArtworkMaterialPublishFixture(t)
	owner, spec, slots, _ := ipArtworkMaterialPublishInstalled(t, w, r, p, true)
	cmd := ipArtworkMaterialPublishCommandFor(t, 3, 4, owner, spec, slots)
	if _, turn, tx, _ := ipArtworkMaterialPublishApply(t, w, r, p, cmd); true {
		ipPolicyRollbackTurn(t, turn, tx)
	}
	ipArtworkMaterialPublishClean(t, w)
	out, turn, tx, _ := ipArtworkMaterialPublishApply(t, w, r, p, cmd)
	if out.Outcome.State != Applied || out.Value.Revision != 3 {
		t.Fatal(out)
	}
	ipPolicyCommitTurn(t, turn, tx)
	ipArtworkMaterialPublishExpect(t, w, 3, `SELECT count(*) FROM artwork_blobs`)
	ipArtworkMaterialPublishExpect(t, w, 1, `SELECT count(*) FROM artwork_sources`)
}

func TestIPArtworkMaterialPublishBoundsAndDescriptors(t *testing.T) {
	owner, job := ipArtworkMaterialOwnerFor(), ipArtworkMaterialJobFor()
	spec := ipArtworkMaterialSpecFor(job, 4)
	publish := ipArtworkMaterialPublishSpecFor(spec, 1)
	identity := CommandIdentity{Incarnation: owner.incarnation, Sequence: 3, OperationID: [16]byte{4}}
	for name, mutate := range map[string]func(*ipArtworkMaterialPublishRequest){
		"both_namespaces":  func(q *ipArtworkMaterialPublishRequest) { q.owner.personID = 778 },
		"no_namespace":     func(q *ipArtworkMaterialPublishRequest) { q.owner.entityID = 0 },
		"zero_lease_owner": func(q *ipArtworkMaterialPublishRequest) { q.owner.leaseOwner = 0 },
		"zero_lease_until": func(q *ipArtworkMaterialPublishRequest) { q.owner.leaseUntilMS = 0 },
		"zero_source":      func(q *ipArtworkMaterialPublishRequest) { q.owner.sourceIncarnation = [16]byte{} },
		"zero_revision":    func(q *ipArtworkMaterialPublishRequest) { q.owner.revision = 0 },
		"foreign_incarn":   func(q *ipArtworkMaterialPublishRequest) { q.owner.incarnation = [16]byte{9} },
		"zero_policy":      func(q *ipArtworkMaterialPublishRequest) { q.spec.policyRevision = 0 },
		"zero_encoder":     func(q *ipArtworkMaterialPublishRequest) { q.spec.encoderRevision = 0 },
		"bad_edge":         func(q *ipArtworkMaterialPublishRequest) { q.spec.largeEdge = 1024 },
		"two_slots":        func(q *ipArtworkMaterialPublishRequest) { q.spec.expectedSlots = 2 },
		"five_slots":       func(q *ipArtworkMaterialPublishRequest) { q.spec.expectedSlots = 5 },
		"short_digest":     func(q *ipArtworkMaterialPublishRequest) { q.spec.sourceDigest = "abc" },
		"upper_digest":     func(q *ipArtworkMaterialPublishRequest) { q.spec.sourceDigest = strings.Repeat("A", 64) },
		"bad_mime":         func(q *ipArtworkMaterialPublishRequest) { q.spec.acceptedMIME = "image/gif" },
		"zero_width":       func(q *ipArtworkMaterialPublishRequest) { q.spec.acceptedWidth = 0 },
		"long_language":    func(q *ipArtworkMaterialPublishRequest) { q.spec.language = strings.Repeat("a", 65) },
		"empty_language":   func(q *ipArtworkMaterialPublishRequest) { q.spec.language = "" },
		"long_intent": func(q *ipArtworkMaterialPublishRequest) {
			q.spec.intentID = strings.Repeat("i", 129)
		},
		"zero_generation":  func(q *ipArtworkMaterialPublishRequest) { q.spec.intentGeneration = 0 },
		"wrong_revision":   func(q *ipArtworkMaterialPublishRequest) { q.spec.intentRevision = 1 },
		"no_slots":         func(q *ipArtworkMaterialPublishRequest) { q.slots = nil },
		"short_manifest":   func(q *ipArtworkMaterialPublishRequest) { q.slots = q.slots[:3] },
		"gap":              func(q *ipArtworkMaterialPublishRequest) { q.slots[1].slot = 5 },
		"duplicate_digest": func(q *ipArtworkMaterialPublishRequest) { q.slots[1].digest = q.slots[0].digest },
		"encoded_webp":     func(q *ipArtworkMaterialPublishRequest) { q.slots[1].mime = "image/webp" },
		"foreign_original": func(q *ipArtworkMaterialPublishRequest) { q.slots[3].digest = ipArtworkMaterialHex(9) },
		"zero_bytes":       func(q *ipArtworkMaterialPublishRequest) { q.slots[0].bytes = 0 },
		"oversize_slot":    func(q *ipArtworkMaterialPublishRequest) { q.slots[0].bytes = ipArtworkMaterialMaxSlot + 1 },
	} {
		t.Run(name, func(t *testing.T) {
			q := ipArtworkMaterialPublishRequest{owner: owner, spec: publish,
				slots: ipArtworkMaterialSlotsFor(spec)}
			mutate(&q)
			if c, err := newIPArtworkMaterialPublishCommand(identity, q); c != nil || !errors.Is(err, ErrInvalid) {
				t.Fatal("malformed publish admitted", c, err)
			}
		})
	}
	// Bounded maximum: a 128-byte intent id and the largest admissible manifest.
	long := strings.Repeat("i", 128)
	maxSpec := publish
	maxSpec.intentID = long
	maxSpec.expectedSlots = 3
	maxSlots := ipArtworkMaterialSlotsFor(spec)[:3]
	for n := range maxSlots {
		maxSlots[n].bytes = 40 << 20
	}
	if _, err := newIPArtworkMaterialPublishCommand(identity, ipArtworkMaterialPublishRequest{
		owner: owner, spec: maxSpec, slots: maxSlots}); err != nil {
		t.Fatal("bounded maximum publish refused", err)
	}
	oversize := ipArtworkMaterialPublishRequest{owner: owner, spec: maxSpec,
		slots: append([]ipArtworkMaterialSlot(nil), maxSlots...)}
	oversize.slots[0].bytes = ipArtworkMaterialMaxSlot
	if _, err := newIPArtworkMaterialPublishCommand(identity, oversize); !errors.Is(err, ErrInvalid) {
		t.Fatal("oversize publish admitted", err)
	}
	expected, err := Describe(IPArtworkMaterialPublish)
	if err != nil || expected != (Descriptor{IPArtworkMaterialPublish, "ip_artwork_material_publish", Normal}) ||
		expected.ID != 225 {
		t.Fatal(expected, err)
	}
	// Preflight refuses a differently labelled or incomplete saved result.
	good := ipArtworkMaterialPublishSavedResult{IPArtworkMaterialPublish,
		ipArtworkMaterialPublishResult{IntentID: "job-entity-poster", Revision: 3, Slots: 4, Bytes: 1 << 10, JobGeneration: 1}}
	if err := preflightIPArtworkMaterialPublishResult(good); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*ipArtworkMaterialPublishSavedResult){
		"wrong_kind":      func(v *ipArtworkMaterialPublishSavedResult) { v.Kind = IPArtworkMaterialInstalled },
		"wrong_revision":  func(v *ipArtworkMaterialPublishSavedResult) { v.Value.Revision = 2 },
		"zero_bytes":      func(v *ipArtworkMaterialPublishSavedResult) { v.Value.Bytes = 0 },
		"zero_generation": func(v *ipArtworkMaterialPublishSavedResult) { v.Value.JobGeneration = 0 },
		"empty_intent":    func(v *ipArtworkMaterialPublishSavedResult) { v.Value.IntentID = "" },
		"too_many_slots":  func(v *ipArtworkMaterialPublishSavedResult) { v.Value.Slots = 5 },
	} {
		v := good
		mutate(&v)
		if err := preflightIPArtworkMaterialPublishResult(v); !errors.Is(err, ErrInvalid) {
			t.Fatal(name, err)
		}
	}
}

// No filesystem, provider or HTTP access can happen inside the SQLite turn: the
// publish writer files must not import an IO or network package.
func TestIPArtworkMaterialPublishIOStaysOutsideHold(t *testing.T) {
	for _, file := range []string{
		"ip_artwork_material_publish.go",
		"ip_artwork_material_publish_result.go",
		"ip_artwork_material_publish_statements.go",
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
