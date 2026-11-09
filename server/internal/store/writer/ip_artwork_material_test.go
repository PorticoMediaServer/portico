package writer

import (
	"context"
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

// Group E is not embedded, so material custody is qualified only against the
// exact draft fixture: recreated subject-job owner, the draft artwork_jobs
// subject column, material intents/slots and the closed artwork receipt table.
// No Capture/golden claim is made here. The commands do no file or provider IO;
// verified physical facts arrive in the request after the caller installed them.
func ipArtworkMaterialFixture(t *testing.T) (*executor, *ipPolicyRegistry, *usOwnerAuthorityProof) {
	t.Helper()
	w, keeper, p := ipPolicySecurityFixture(t)
	ddl, err := os.ReadFile("testdata/ip-artwork-material-draft.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = w.db.ExecContext(t.Context(), string(ddl)); err != nil {
		t.Fatal(err)
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
	r, err := stmttest.New(t, append(usAuthorityDefinitions(), ipArtworkMaterialDefinitions()...))
	if err != nil {
		t.Fatal(err)
	}
	return w, &ipPolicyRegistry{r, keeper}, p
}

func ipArtworkMaterialHex(n int) string { return strings.Repeat(fmt.Sprintf("%x", n%16), 64) }

func ipArtworkMaterialOwnerFor() ipArtworkMaterialOwner {
	return ipArtworkMaterialOwner{id: 901, incarnation: [16]byte{1}, entityID: 778,
		sourceIncarnation: [16]byte{4}, sourceRevision: 1, revision: 1,
		leaseOwner: 7, leaseGeneration: 1, leaseUntilMS: 1 << 50}
}

func ipArtworkMaterialPersonOwnerFor() ipArtworkMaterialOwner {
	return ipArtworkMaterialOwner{id: 902, incarnation: [16]byte{6}, personID: 778,
		sourceIncarnation: [16]byte{5}, sourceRevision: 2, revision: 1,
		leaseOwner: 7, leaseGeneration: 1, leaseUntilMS: 1 << 50}
}

func ipArtworkMaterialJobFor() ipArtworkMaterialJob {
	return ipArtworkMaterialJob{id: "job-entity-poster", role: "poster", choice: "matched",
		provider: "tmdb", imageID: "image-001", selectionRevision: 1,
		revision: 1, leaseGeneration: 0, dueMS: 1}
}

func ipArtworkMaterialPersonJobFor() ipArtworkMaterialJob {
	return ipArtworkMaterialJob{id: "job-person-portrait", role: "portrait", choice: "locked",
		provider: "upload", imageID: "upload-001", selectionRevision: 0,
		revision: 1, leaseGeneration: 0, dueMS: 1}
}

func ipArtworkMaterialSpecFor(job ipArtworkMaterialJob, expected int64) ipArtworkMaterialSpec {
	return ipArtworkMaterialSpec{provider: job.provider, language: "en", policyRevision: 1,
		intentID: job.id, sourceDigest: ipArtworkMaterialHex(1), acceptedMIME: "image/jpeg",
		acceptedWidth: 3000, acceptedHeight: 2000, largeEdge: 1920, encoderRevision: 3,
		expectedSlots: expected}
}

func ipArtworkMaterialSlotsFor(spec ipArtworkMaterialSpec) []ipArtworkMaterialSlot {
	slots := []ipArtworkMaterialSlot{
		{slot: 0, digest: ipArtworkMaterialHex(2), mime: "image/jpeg", width: 500, height: 750, bytes: 40 << 10},
		{slot: 1, digest: ipArtworkMaterialHex(3), mime: "image/jpeg", width: 1280, height: 720, bytes: 120 << 10},
		{slot: 2, digest: ipArtworkMaterialHex(4), mime: "image/png", width: 780, height: 1170, bytes: 90 << 10},
	}
	if spec.expectedSlots == 4 {
		slots = append(slots, ipArtworkMaterialSlot{slot: 3, digest: spec.sourceDigest, mime: spec.acceptedMIME,
			width: spec.acceptedWidth, height: spec.acceptedHeight, bytes: 2 << 20})
	}
	return slots
}

func ipArtworkMaterialIntentCommand(t *testing.T, sequence int64, operation byte,
	owner ipArtworkMaterialOwner, job ipArtworkMaterialJob, spec ipArtworkMaterialSpec) *ipArtworkMaterialCommand {
	t.Helper()
	identity := CommandIdentity{Incarnation: owner.incarnation, Sequence: sequence, OperationID: [16]byte{operation}}
	c, err := newIPArtworkMaterialCommand(IPArtworkMaterialIntent, identity, ipArtworkMaterialRequest{
		owner: owner, job: job, spec: spec})
	if err != nil {
		t.Fatal("intent", err)
	}
	return c
}

func ipArtworkMaterialInstalledCommand(t *testing.T, sequence int64, operation byte,
	owner ipArtworkMaterialOwner, spec ipArtworkMaterialSpec, generation, jobRevision, jobGeneration int64,
	slots []ipArtworkMaterialSlot) *ipArtworkMaterialCommand {
	t.Helper()
	identity := CommandIdentity{Incarnation: owner.incarnation, Sequence: sequence, OperationID: [16]byte{operation}}
	c, err := newIPArtworkMaterialCommand(IPArtworkMaterialInstalled, identity, ipArtworkMaterialRequest{
		owner: owner, spec: spec, intentGeneration: generation, intentRevision: 1,
		jobRevision: jobRevision, jobGeneration: jobGeneration, slots: slots})
	if err != nil {
		t.Fatal("installed", err)
	}
	return c
}

func ipArtworkMaterialApply(t *testing.T, w *executor, r *ipPolicyRegistry, p *usOwnerAuthorityProof,
	kind CommandID, c *ipArtworkMaterialCommand) (ipArtworkMaterialCommandResult, *commandTurn, *transaction, ipPolicyBudget) {
	t.Helper()
	turn, tx, b := ipPolicyTurn(t, w, r, kind)
	out, err := applyIPArtworkMaterial(t.Context(), turn, p, t.Context(), b, kind, c)
	if err != nil {
		t.Fatal("material", kind, out, err)
	}
	return out, turn, tx, b
}

func ipArtworkMaterialAck(t *testing.T, w *executor, sequence int64) {
	t.Helper()
	// Acknowledging a sequence also publishes it as the latest observed command;
	// the job CHECK requires receipt_reclaim_after_sequence <= acknowledged <=
	// last_command_sequence.
	ipArtworkDiscoveryAck(t, w, sequence)
}

// Custody is durable with no source, blob, selection or history fanout: those
// belong to IP-20/IP-21 and to the E016 closure review.
func ipArtworkMaterialNoFanout(t *testing.T, w *executor) {
	t.Helper()
	for _, query := range []string{
		`SELECT count(*) FROM artwork_sources`,
		`SELECT count(*) FROM artwork_blobs`,
		`SELECT count(*) FROM artwork_selections`,
		`SELECT count(*) FROM metadata_owner_history`,
		`SELECT count(*) FROM metadata_provider_operations`,
		`SELECT count(*) FROM metadata_acquisition_jobs`,
		`SELECT count(*) FROM metadata_evidence`,
	} {
		if got := ipPolicyQueryInt(t, w, query); got != 0 {
			t.Fatal("material handoff fanned out", query, got)
		}
	}
}

// ipArtworkMaterialDeclared creates and ACKs one intent and reports its
// generation. The caller owns the installed handoff.
func ipArtworkMaterialDeclared(t *testing.T, w *executor, r *ipPolicyRegistry, p *usOwnerAuthorityProof,
	owner ipArtworkMaterialOwner, job ipArtworkMaterialJob, spec ipArtworkMaterialSpec) int64 {
	t.Helper()
	cmd := ipArtworkMaterialIntentCommand(t, 1, 2, owner, job, spec)
	out, turn, tx, _ := ipArtworkMaterialApply(t, w, r, p, IPArtworkMaterialIntent, cmd)
	ipPolicyCommitTurn(t, turn, tx)
	ipArtworkMaterialAck(t, w, 1)
	return out.Value.JobGeneration
}

func TestIPArtworkMaterialIntentInstallReplayRetired(t *testing.T) {
	w, r, p := ipArtworkMaterialFixture(t)
	owner, job := ipArtworkMaterialOwnerFor(), ipArtworkMaterialJobFor()
	spec := ipArtworkMaterialSpecFor(job, 4)
	intent := ipArtworkMaterialIntentCommand(t, 1, 2, owner, job, spec)
	out, turn, tx, _ := ipArtworkMaterialApply(t, w, r, p, IPArtworkMaterialIntent, intent)
	if out.Outcome.State != Applied || out.Outcome.Replayed ||
		out.Value != (ipArtworkMaterialResult{IntentID: job.id, Revision: 1, Slots: 4, JobGeneration: 1}) ||
		out.Digest == (ResultDigest{}) {
		t.Fatal(out)
	}
	ipPolicyCommitTurn(t, turn, tx)
	originalIntent := out
	for query, want := range map[string]int64{
		`SELECT count(*) FROM artwork_material_intents WHERE id='job-entity-poster' AND state='prepared' AND staged_slots=0 AND revision=1 AND generation=1 AND expected_slots=4`: 1,
		`SELECT count(*) FROM artwork_material_slots`:                                                0,
		`SELECT status='running' FROM artwork_jobs WHERE id='job-entity-poster'`:                     1,
		`SELECT job_revision FROM artwork_jobs WHERE id='job-entity-poster'`:                         2,
		`SELECT job_lease_generation FROM artwork_jobs WHERE id='job-entity-poster'`:                 1,
		`SELECT job_lease_owner FROM artwork_jobs WHERE id='job-entity-poster'`:                      7,
		`SELECT job_attempts FROM artwork_jobs WHERE id='job-entity-poster'`:                         1,
		`SELECT count(*) FROM artwork_command_receipts WHERE command_kind='job_claim'`:               1,
		`SELECT count(*) FROM artwork_command_receipts WHERE material_intent_id='job-entity-poster'`: 1,
		`SELECT accepted_revision FROM artwork_command_receipts`:                                     1,
		`SELECT applied_ms FROM artwork_command_receipts`:                                            w.now().UnixMilli(),
		`SELECT last_command_sequence FROM artwork_subject_jobs WHERE id=901`:                        1,
	} {
		if got := ipPolicyQueryInt(t, w, query); got != want {
			t.Fatal(query, got, want)
		}
	}
	// The original intent replays exactly after source, lease, language and
	// policy move.
	ipPolicyExec(t, w, `UPDATE catalog_entities SET artwork_revision=9 WHERE id=778`)
	ipPolicyExec(t, w, `UPDATE metadata_runtime_settings SET artwork_language='textless',artwork_policy_revision=9`)
	ipPolicyExec(t, w, `UPDATE artwork_subject_jobs SET job_revision=99,job_lease_owner=9 WHERE id=901`)
	ipPolicyExec(t, w, `UPDATE artwork_jobs SET job_revision=9,job_lease_owner=9 WHERE id='job-entity-poster'`)
	turn, tx, b := ipPolicyTurn(t, w, r, IPArtworkMaterialIntent)
	replay, err := applyIPArtworkMaterial(t.Context(), turn, p, t.Context(), b, IPArtworkMaterialIntent, intent)
	if err != nil || !replay.Outcome.Replayed || replay.Digest != originalIntent.Digest ||
		replay.Value != originalIntent.Value {
		t.Fatal("source change altered the original intent", replay, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
	ipPolicyExec(t, w, `UPDATE catalog_entities SET artwork_revision=1 WHERE id=778`)
	ipPolicyExec(t, w, `UPDATE metadata_runtime_settings SET artwork_language='same',artwork_policy_revision=1`)
	ipPolicyExec(t, w, `UPDATE artwork_subject_jobs SET job_revision=2,job_lease_owner=7 WHERE id=901`)
	ipPolicyExec(t, w, `UPDATE artwork_jobs SET job_revision=2,job_lease_owner=7 WHERE id='job-entity-poster'`)
	ipArtworkMaterialAck(t, w, 1)

	// At or below ACK is Retired while the intent and job facts stay physical.
	turn, tx, b = ipPolicyTurn(t, w, r, IPArtworkMaterialIntent)
	retired, err := applyIPArtworkMaterial(t.Context(), turn, p, t.Context(), b, IPArtworkMaterialIntent, intent)
	if err != nil || retired.Outcome.State != ResultRetired || retired.Value != (ipArtworkMaterialResult{}) ||
		retired.Digest != (ResultDigest{}) {
		t.Fatal("retired intent served its result", retired, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)

	// The installed handoff records the complete four-slot manifest, including
	// the retained original upload.
	owner.revision = 2
	slots := ipArtworkMaterialSlotsFor(spec)
	var total int64
	for _, slot := range slots {
		total += slot.bytes
	}
	installed := ipArtworkMaterialInstalledCommand(t, 2, 3, owner, spec, 1, 2, 1, slots)
	out, turn, tx, _ = ipArtworkMaterialApply(t, w, r, p, IPArtworkMaterialInstalled, installed)
	if out.Outcome.State != Applied || out.Value != (ipArtworkMaterialResult{
		IntentID: job.id, Revision: 2, Slots: 4, Bytes: total, JobGeneration: 1}) || out.Digest == (ResultDigest{}) {
		t.Fatal(out)
	}
	ipPolicyCommitTurn(t, turn, tx)
	originalInstalled := out
	for query, want := range map[string]int64{
		`SELECT count(*) FROM artwork_material_intents WHERE state='installed' AND staged_slots=4 AND revision=2`: 1,
		`SELECT count(*) FROM artwork_material_slots WHERE intent_id='job-entity-poster'`:                         4,
		`SELECT count(*) FROM artwork_material_slots WHERE slot=3 AND digest=? AND bytes=?`:                       1,
		`SELECT status='complete' FROM artwork_jobs WHERE id='job-entity-poster'`:                                 1,
		`SELECT job_revision FROM artwork_jobs WHERE id='job-entity-poster'`:                                      3,
		`SELECT job_lease_expires_ms FROM artwork_jobs WHERE id='job-entity-poster'`:                              0,
		`SELECT count(*) FROM artwork_command_receipts WHERE command_kind='material_publish'`:                     1,
		`SELECT accepted_revision FROM artwork_command_receipts WHERE command_kind='material_publish'`:            2,
		`SELECT result_count FROM artwork_command_receipts WHERE command_kind='material_publish'`:                 4,
		`SELECT result_bytes FROM artwork_command_receipts WHERE command_kind='material_publish'`:                 total,
		`SELECT last_command_sequence FROM artwork_subject_jobs WHERE id=901`:                                     2,
	} {
		var got int64
		if strings.Contains(query, "?") {
			got = ipPolicyQueryInt(t, w, query, spec.sourceDigest, 2<<20)
		} else {
			got = ipPolicyQueryInt(t, w, query)
		}
		if got != want {
			t.Fatal(query, got, want)
		}
	}
	ipArtworkMaterialNoFanout(t, w)
	// Replay after the job completes and the intent is no longer prepared.
	ipPolicyExec(t, w, `UPDATE artwork_material_intents SET revision=9 WHERE id='job-entity-poster'`)
	turn, tx, b = ipPolicyTurn(t, w, r, IPArtworkMaterialInstalled)
	replay, err = applyIPArtworkMaterial(t.Context(), turn, p, t.Context(), b, IPArtworkMaterialInstalled, installed)
	if err != nil || !replay.Outcome.Replayed || replay.Digest != originalInstalled.Digest ||
		replay.Value != originalInstalled.Value {
		t.Fatal("installed replay", replay, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
	ipArtworkMaterialAck(t, w, 2)
	turn, tx, b = ipPolicyTurn(t, w, r, IPArtworkMaterialInstalled)
	retired, err = applyIPArtworkMaterial(t.Context(), turn, p, t.Context(), b, IPArtworkMaterialInstalled, installed)
	if err != nil || retired.Outcome.State != ResultRetired || retired.Value != (ipArtworkMaterialResult{}) {
		t.Fatal("retired installed", retired, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
}

func TestIPArtworkMaterialThreeSlotsPersonNoOriginal(t *testing.T) {
	w, r, p := ipArtworkMaterialFixture(t)
	owner, job := ipArtworkMaterialPersonOwnerFor(), ipArtworkMaterialPersonJobFor()
	spec := ipArtworkMaterialSpecFor(job, 3)
	spec.provider = "upload"
	generation := ipArtworkMaterialDeclared(t, w, r, p, owner, job, spec)
	owner.revision = 2
	slots := ipArtworkMaterialSlotsFor(spec)
	installed := ipArtworkMaterialInstalledCommand(t, 2, 3, owner, spec, generation, 2, 1, slots)
	out, turn, tx, _ := ipArtworkMaterialApply(t, w, r, p, IPArtworkMaterialInstalled, installed)
	if out.Outcome.State != Applied || out.Value.Slots != 3 || out.Value.Revision != 2 {
		t.Fatal(out)
	}
	ipPolicyCommitTurn(t, turn, tx)
	for query, want := range map[string]int64{
		`SELECT count(*) FROM artwork_material_intents WHERE owner_id=902 AND expected_slots=3 AND state='installed'`: 1,
		`SELECT count(*) FROM artwork_material_slots WHERE slot=3`:                                                    0,
		`SELECT count(*) FROM artwork_material_slots`:                                                                 3,
		`SELECT status='complete' FROM artwork_jobs WHERE id='job-person-portrait'`:                                   1,
		`SELECT count(*) FROM artwork_material_intents WHERE owner_id=901`:                                            0,
	} {
		if got := ipPolicyQueryInt(t, w, query); got != want {
			t.Fatal(query, got, want)
		}
	}
	ipArtworkMaterialNoFanout(t, w)
}

// E016: artwork_material_slots and artwork_material_intents.source_digest stay
// outside the selected/history closure. IP-19 records and retains the ORIGINAL
// uploaded material in slot 3 but never publishes, selects, retires or deletes
// it; installed, unpublished and retiring originals therefore keep their exact
// digest and bytes, and no selection/history row can stand in for them.
func TestIPArtworkMaterialOriginalCustodyE016(t *testing.T) {
	w, r, p := ipArtworkMaterialFixture(t)
	owner, job := ipArtworkMaterialOwnerFor(), ipArtworkMaterialJobFor()
	spec := ipArtworkMaterialSpecFor(job, 4)
	generation := ipArtworkMaterialDeclared(t, w, r, p, owner, job, spec)
	owner.revision = 2
	slots := ipArtworkMaterialSlotsFor(spec)
	installed := ipArtworkMaterialInstalledCommand(t, 2, 3, owner, spec, generation, 2, 1, slots)
	if _, turn, tx, _ := ipArtworkMaterialApply(t, w, r, p, IPArtworkMaterialInstalled, installed); true {
		ipPolicyCommitTurn(t, turn, tx)
	}

	// Installed but unpublished: the original is charged only in the material
	// tables, and nothing in the selection/history closure names it.
	assertOriginal := func(state string) {
		t.Helper()
		if got := ipPolicyString(t, w, `SELECT source_digest FROM artwork_material_intents WHERE id=?`, job.id); got != spec.sourceDigest {
			t.Fatal(state, "source digest lost", got)
		}
		if got := ipPolicyString(t, w, `SELECT digest FROM artwork_material_slots WHERE intent_id=? AND slot=3`, job.id); got != spec.sourceDigest {
			t.Fatal(state, "original slot lost", got)
		}
		if got := ipPolicyQueryInt(t, w, `SELECT bytes FROM artwork_material_slots WHERE intent_id=? AND slot=3`, job.id); got != 2<<20 {
			t.Fatal(state, "original bytes lost", got)
		}
		if got := ipPolicyQueryInt(t, w, `SELECT count(*) FROM artwork_material_slots WHERE intent_id=?`, job.id); got != 4 {
			t.Fatal(state, "manifest lost", got)
		}
		for _, query := range []string{
			`SELECT count(*) FROM artwork_selections`,
			`SELECT count(*) FROM metadata_owner_history`,
			`SELECT count(*) FROM artwork_sources WHERE digest=?`,
			`SELECT count(*) FROM artwork_blobs WHERE digest=?`,
		} {
			var got int64
			if strings.Contains(query, "?") {
				got = ipPolicyQueryInt(t, w, query, spec.sourceDigest)
			} else {
				got = ipPolicyQueryInt(t, w, query)
			}
			if got != 0 {
				t.Fatal(state, "original leaked into closure", query, got)
			}
		}
	}
	assertOriginal("installed/unpublished")
	// The handoff never advances the intent beyond installed; publish is IP-20.
	if got := ipPolicyString(t, w, `SELECT state FROM artwork_material_intents WHERE id=?`, job.id); got != "installed" {
		t.Fatal("unpublished material published early", got)
	}

	// Retiring original: IP-23 owns retirement, so IP-19 must leave the charged
	// manifest untouched and refuse to declare/install over it.
	ipPolicyExec(t, w, `UPDATE artwork_material_intents SET state='retiring',retirement_token=X'0f000000000000000000000000000000',revision=revision+1,reclaim_due_ms=1 WHERE id=?`, job.id)
	assertOriginal("retiring")
	// The original receipt still resolves before any fresh state check, and the
	// result is the exact saved one even while the intent is retiring.
	turn, tx, b := ipPolicyTurn(t, w, r, IPArtworkMaterialInstalled)
	replay, err := applyIPArtworkMaterial(t.Context(), turn, p, t.Context(), b, IPArtworkMaterialInstalled, installed)
	if err != nil || !replay.Outcome.Replayed || replay.Value.Bytes != ipArtworkMaterialSlotBytes(slots) {
		t.Fatal("retiring intent replay", replay, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
	// A fresh install cannot reopen a retiring manifest.
	ipArtworkMaterialAck(t, w, 2)
	owner.revision = 3
	fresh := ipArtworkMaterialInstalledCommand(t, 3, 4, owner, spec, generation, 3, 1, slots)
	turn, tx, b = ipPolicyTurn(t, w, r, IPArtworkMaterialInstalled)
	if out, err := applyIPArtworkMaterial(t.Context(), turn, p, t.Context(), b, IPArtworkMaterialInstalled, fresh); out != (ipArtworkMaterialCommandResult{}) || !errors.Is(err, ErrConflict) {
		t.Fatal("retiring manifest reopened", out, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
	assertOriginal("retiring after refusal")
	// A second declaration for the same native job is refused and adds nothing.
	redeclare := ipArtworkMaterialIntentCommand(t, 4, 5, owner, job, spec)
	turn, tx, b = ipPolicyTurn(t, w, r, IPArtworkMaterialIntent)
	if out, err := applyIPArtworkMaterial(t.Context(), turn, p, t.Context(), b, IPArtworkMaterialIntent, redeclare); out != (ipArtworkMaterialCommandResult{}) || err == nil {
		t.Fatal("retiring job redeclared", out, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
	if got := ipPolicyQueryInt(t, w, `SELECT count(*) FROM artwork_material_intents`); got != 1 {
		t.Fatal("second intent written", got)
	}
	assertOriginal("retiring after redeclare")
}

func TestIPArtworkMaterialInstalledFences(t *testing.T) {
	cases := []struct {
		name   string
		sql    []string
		mutate func(*ipArtworkMaterialRequest)
		want   error
	}{
		{"stale_source_revision", []string{`UPDATE catalog_entities SET artwork_revision=2 WHERE id=778`}, nil, ErrConflict},
		{"retired_entity", []string{`UPDATE catalog_entities SET retired=1 WHERE id=778`}, nil, ErrConflict},
		{"retired_library", []string{`UPDATE catalog_libraries SET retired=1 WHERE id=777`}, nil, ErrConflict},
		{"stale_language", []string{`UPDATE metadata_runtime_settings SET artwork_language='textless'`}, nil, ErrConflict},
		{"stale_policy", []string{`UPDATE metadata_runtime_settings SET artwork_policy_revision=2`}, nil, ErrConflict},
		{"stale_owner_lease", []string{`UPDATE artwork_subject_jobs SET job_lease_owner=8 WHERE id=901`}, nil, ErrConflict},
		{"expired_owner_lease", []string{`UPDATE artwork_subject_jobs SET job_lease_expires_ms=1 WHERE id=901`}, nil, ErrConflict},
		{"wrong_phase", []string{`UPDATE artwork_subject_jobs SET phase='providers' WHERE id=901`}, nil, ErrConflict},
		{"job_not_live", []string{`UPDATE artwork_jobs SET status='pending' WHERE id='job-entity-poster'`}, nil, ErrConflict},
		{"job_revision_moved", []string{`UPDATE artwork_jobs SET job_revision=9 WHERE id='job-entity-poster'`}, nil, ErrConflict},
		{"job_generation_moved", []string{`UPDATE artwork_jobs SET job_lease_generation=9 WHERE id='job-entity-poster'`}, nil, ErrConflict},
		{"job_lease_owner_moved", []string{`UPDATE artwork_jobs SET job_lease_owner=9 WHERE id='job-entity-poster'`}, nil, ErrConflict},
		{"intent_generation", nil, func(q *ipArtworkMaterialRequest) { q.intentGeneration = 2 }, ErrConflict},
		{"intent_not_prepared", []string{`UPDATE artwork_material_intents SET state='installed',staged_slots=4 WHERE id='job-entity-poster'`}, nil, ErrConflict},
		{"stored_source_changed", []string{`UPDATE artwork_material_intents SET source_digest=? WHERE id='job-entity-poster'`}, nil, ErrConflict},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w, r, p := ipArtworkMaterialFixture(t)
			owner, job := ipArtworkMaterialOwnerFor(), ipArtworkMaterialJobFor()
			spec := ipArtworkMaterialSpecFor(job, 4)
			generation := ipArtworkMaterialDeclared(t, w, r, p, owner, job, spec)
			for _, query := range tc.sql {
				if strings.Contains(query, "?") {
					ipPolicyExec(t, w, query, ipArtworkMaterialHex(8))
				} else {
					ipPolicyExec(t, w, query)
				}
			}
			owner.revision = 2
			slots := ipArtworkMaterialSlotsFor(spec)
			cmd := ipArtworkMaterialInstalledCommand(t, 2, 3, owner, spec, generation, 2, 1, slots)
			if tc.mutate != nil {
				tc.mutate(&cmd.request)
				digest, err := canonicalRequestDigest(IPArtworkMaterialInstalled, 1, cmd.identity, cmd.request)
				if err != nil {
					t.Fatal(err)
				}
				cmd.digest = digest
			}
			turn, tx, b := ipPolicyTurn(t, w, r, IPArtworkMaterialInstalled)
			out, err := applyIPArtworkMaterial(t.Context(), turn, p, t.Context(), b, IPArtworkMaterialInstalled, cmd)
			if out != (ipArtworkMaterialCommandResult{}) || !errors.Is(err, tc.want) {
				t.Fatal("stale installed handoff admitted", out, err)
			}
			ipPolicyRollbackTurn(t, turn, tx)
			// intent_not_prepared seeds an already-installed intent row, so the
			// refusal must leave that row intact rather than remove it.
			wantInstalled := int64(0)
			if tc.name == "intent_not_prepared" {
				wantInstalled = 1
			}
			for query, want := range map[string]int64{
				`SELECT count(*) FROM artwork_material_slots`:                                         0,
				`SELECT count(*) FROM artwork_command_receipts WHERE command_kind='material_publish'`: 0,
				`SELECT count(*) FROM artwork_material_intents WHERE state='installed'`:               wantInstalled,
				`SELECT last_command_sequence FROM artwork_subject_jobs WHERE id=901`:                 1,
			} {
				if got := ipPolicyQueryInt(t, w, query); got != want {
					t.Fatal(query, got, want)
				}
			}
		})
	}
}

func TestIPArtworkMaterialStaleIntentAndJob(t *testing.T) {
	cases := []struct {
		name   string
		sql    []string
		mutate func(*ipArtworkMaterialRequest)
		want   error
	}{
		{"stale_job_revision", nil, func(q *ipArtworkMaterialRequest) { q.job.revision = 2 }, ErrConflict},
		{"stale_job_due", nil, func(q *ipArtworkMaterialRequest) { q.job.dueMS = 2 }, ErrConflict},
		{"stale_job_image", nil, func(q *ipArtworkMaterialRequest) { q.job.imageID = "image-002" }, ErrConflict},
		{"stale_job_provider", nil, func(q *ipArtworkMaterialRequest) { q.job.provider = "tvdb" }, ErrConflict},
		{"not_due", []string{`UPDATE artwork_jobs SET job_next_attempt_ms=4000000000000 WHERE id='job-entity-poster'`}, nil, ErrConflict},
		{"completed_job", []string{`UPDATE artwork_jobs SET status='complete' WHERE id='job-entity-poster'`}, nil, ErrConflict},
		{"stale_source_revision", []string{`UPDATE catalog_entities SET artwork_revision=2 WHERE id=778`}, nil, ErrConflict},
		{"retired_entity", []string{`UPDATE catalog_entities SET retired=1 WHERE id=778`}, nil, ErrConflict},
		{"stale_language", []string{`UPDATE metadata_runtime_settings SET artwork_language='textless'`}, nil, ErrConflict},
		{"stale_policy", []string{`UPDATE metadata_runtime_settings SET artwork_policy_revision=2`}, nil, ErrConflict},
		{"stale_owner_lease", []string{`UPDATE artwork_subject_jobs SET job_lease_owner=8 WHERE id=901`}, nil, ErrConflict},
		{"wrong_phase", []string{`UPDATE artwork_subject_jobs SET phase='providers' WHERE id=901`}, nil, ErrConflict},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w, r, p := ipArtworkMaterialFixture(t)
			for _, query := range tc.sql {
				ipPolicyExec(t, w, query)
			}
			owner, job := ipArtworkMaterialOwnerFor(), ipArtworkMaterialJobFor()
			spec := ipArtworkMaterialSpecFor(job, 4)
			cmd := ipArtworkMaterialIntentCommand(t, 1, 2, owner, job, spec)
			if tc.mutate != nil {
				tc.mutate(&cmd.request)
				digest, err := canonicalRequestDigest(IPArtworkMaterialIntent, 1, cmd.identity, cmd.request)
				if err != nil {
					t.Fatal(err)
				}
				cmd.digest = digest
			}
			turn, tx, b := ipPolicyTurn(t, w, r, IPArtworkMaterialIntent)
			out, err := applyIPArtworkMaterial(t.Context(), turn, p, t.Context(), b, IPArtworkMaterialIntent, cmd)
			if out != (ipArtworkMaterialCommandResult{}) || !errors.Is(err, tc.want) {
				t.Fatal("stale intent admitted", out, err)
			}
			ipPolicyRollbackTurn(t, turn, tx)
			// The setup mutation runs outside the refused turn, so only a
			// completed_job setup leaves the job non-pending.
			wantPending := int64(1)
			if tc.name == "completed_job" {
				wantPending = 0
			}
			for query, want := range map[string]int64{
				`SELECT count(*) FROM artwork_material_intents`:                          0,
				`SELECT count(*) FROM artwork_command_receipts`:                          0,
				`SELECT status='pending' FROM artwork_jobs WHERE id='job-entity-poster'`: wantPending,
				`SELECT last_command_sequence FROM artwork_subject_jobs WHERE id=901`:    0,
			} {
				if got := ipPolicyQueryInt(t, w, query); got != want {
					t.Fatal(query, got, want)
				}
			}
		})
	}
}

func TestIPArtworkMaterialCurrentAuthorityAndLifetime(t *testing.T) {
	setup := []struct {
		name  string
		kind  CommandID
		build func(t *testing.T, w *executor, r *ipPolicyRegistry, p *usOwnerAuthorityProof) *ipArtworkMaterialCommand
	}{
		{"intent", IPArtworkMaterialIntent, func(t *testing.T, w *executor, r *ipPolicyRegistry, p *usOwnerAuthorityProof) *ipArtworkMaterialCommand {
			owner, job := ipArtworkMaterialOwnerFor(), ipArtworkMaterialJobFor()
			return ipArtworkMaterialIntentCommand(t, 1, 2, owner, job, ipArtworkMaterialSpecFor(job, 4))
		}},
		{"installed", IPArtworkMaterialInstalled, func(t *testing.T, w *executor, r *ipPolicyRegistry, p *usOwnerAuthorityProof) *ipArtworkMaterialCommand {
			owner, job := ipArtworkMaterialOwnerFor(), ipArtworkMaterialJobFor()
			spec := ipArtworkMaterialSpecFor(job, 4)
			generation := ipArtworkMaterialDeclared(t, w, r, p, owner, job, spec)
			owner.revision = 2
			return ipArtworkMaterialInstalledCommand(t, 2, 3, owner, spec, generation, 2, 1,
				ipArtworkMaterialSlotsFor(spec))
		}},
	}
	for _, mode := range []string{"revoked_family", "forged_proof", "revoked_membership", "cancelled_lifetime"} {
		for _, tc := range setup {
			t.Run(mode+"/"+tc.name, func(t *testing.T) {
				w, r, p := ipArtworkMaterialFixture(t)
				cmd := tc.build(t, w, r, p)
				lifetime := context.Background()
				if mode == "cancelled_lifetime" {
					cancelled, cancel := context.WithCancel(t.Context())
					cancel()
					lifetime = cancelled
				} else {
					p = ipPolicyRevoke(t, w, p, mode)
				}
				turn, tx, b := ipPolicyTurn(t, w, r, tc.kind)
				out, err := applyIPArtworkMaterial(t.Context(), turn, p, lifetime, b, tc.kind, cmd)
				if out != (ipArtworkMaterialCommandResult{}) || err == nil {
					t.Fatal("stale authority/cancelled lifetime admitted", out, err)
				}
				if mode != "cancelled_lifetime" && !errors.Is(err, ErrDisclosureDenied) {
					t.Fatal(err)
				}
				ipPolicyRollbackTurn(t, turn, tx)
			})
		}
	}
}

func TestIPArtworkMaterialUnknownCommitFreshRead(t *testing.T) {
	for _, kind := range []CommandID{IPArtworkMaterialIntent, IPArtworkMaterialInstalled} {
		t.Run(ipArtworkMaterialKindName(kind), func(t *testing.T) {
			w, r, p := ipArtworkMaterialFixture(t)
			owner, job := ipArtworkMaterialOwnerFor(), ipArtworkMaterialJobFor()
			spec := ipArtworkMaterialSpecFor(job, 4)
			var cmd *ipArtworkMaterialCommand
			if kind == IPArtworkMaterialIntent {
				cmd = ipArtworkMaterialIntentCommand(t, 1, 2, owner, job, spec)
			} else {
				generation := ipArtworkMaterialDeclared(t, w, r, p, owner, job, spec)
				owner.revision = 2
				cmd = ipArtworkMaterialInstalledCommand(t, 2, 3, owner, spec, generation, 2, 1,
					ipArtworkMaterialSlotsFor(spec))
			}
			path := ipPolicyDatabasePath(t, w)
			if err := w.db.Close(); err != nil {
				t.Fatal(err)
			}
			connector, err := sqliteconn.Connector(path, w.config)
			if err != nil {
				t.Fatal(err)
			}
			installFaultPool(w, &faultConnector{Connector: connector, afterCommit: true, replyError: true})
			turn, tx, b := ipPolicyTurn(t, w, r, kind)
			out, err := applyIPArtworkMaterial(t.Context(), turn, p, t.Context(), b, kind, cmd)
			if err != nil || out.Outcome.State != Applied {
				t.Fatal(out, err)
			}
			result, err := tx.commit()
			if result.state != commitUnknown || !errors.Is(err, ErrResolveRequired) {
				t.Fatal(result, err)
			}
			if again, e := applyIPArtworkMaterial(t.Context(), turn, p, t.Context(), b, kind, cmd); again != (ipArtworkMaterialCommandResult{}) || e == nil {
				t.Fatal("uncertain frame served the saved result", again, e)
			}
			// Durable truth is read through a FRESH read-only connection, never
			// the quarantined writer pool.
			assertions := map[string]int64{
				`SELECT count(*) FROM artwork_material_intents`:                                                                             1,
				`SELECT CASE state WHEN 'prepared' THEN 1 WHEN 'installed' THEN 2 ELSE 0 END FROM artwork_material_intents`:                 ipArtworkMaterialRevision(kind),
				`SELECT staged_slots FROM artwork_material_intents`:                                                                         ipArtworkMaterialStaged(kind),
				`SELECT count(*) FROM artwork_material_slots`:                                                                               ipArtworkMaterialSlotCount(kind),
				`SELECT count(*) FROM artwork_command_receipts`:                                                                             int64(kind - 222),
				`SELECT CASE status WHEN 'running' THEN 1 WHEN 'complete' THEN 2 ELSE 0 END FROM artwork_jobs WHERE id='job-entity-poster'`: ipArtworkMaterialJobState(kind),
				`SELECT job_revision FROM artwork_jobs WHERE id='job-entity-poster'`:                                                        ipArtworkMaterialJobRevision(kind),
				`SELECT last_command_sequence FROM artwork_subject_jobs WHERE id=901`:                                                       int64(kind - 222),
			}
			for query, want := range assertions {
				if got := ipPolicyFreshInt(t, path, query); got != want {
					t.Fatal(query, got, want)
				}
			}
		})
	}
}

func ipArtworkMaterialStaged(kind CommandID) int64 {
	if kind == IPArtworkMaterialInstalled {
		return 4
	}
	return 0
}

func ipArtworkMaterialSlotCount(kind CommandID) int64 {
	if kind == IPArtworkMaterialInstalled {
		return 4
	}
	return 0
}

func ipArtworkMaterialJobState(kind CommandID) int64 {
	if kind == IPArtworkMaterialInstalled {
		return 2
	}
	return 1
}

func ipArtworkMaterialJobRevision(kind CommandID) int64 {
	if kind == IPArtworkMaterialInstalled {
		return 3
	}
	return 2
}

func TestIPArtworkMaterialMissingOwnerIdentityConflict(t *testing.T) {
	w, r, p := ipArtworkMaterialFixture(t)
	owner, job := ipArtworkMaterialOwnerFor(), ipArtworkMaterialJobFor()
	spec := ipArtworkMaterialSpecFor(job, 4)

	missing := owner
	missing.id = 999
	cmd := ipArtworkMaterialIntentCommand(t, 1, 2, missing, job, spec)
	turn, tx, b := ipPolicyTurn(t, w, r, IPArtworkMaterialIntent)
	if out, err := applyIPArtworkMaterial(t.Context(), turn, p, t.Context(), b, IPArtworkMaterialIntent, cmd); out != (ipArtworkMaterialCommandResult{}) || !errors.Is(err, ErrResolveRequired) {
		t.Fatal("missing owner admitted", out, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)

	foreign := owner
	foreign.incarnation = [16]byte{9}
	cmd = ipArtworkMaterialIntentCommand(t, 1, 2, foreign, job, spec)
	turn, tx, b = ipPolicyTurn(t, w, r, IPArtworkMaterialIntent)
	if out, err := applyIPArtworkMaterial(t.Context(), turn, p, t.Context(), b, IPArtworkMaterialIntent, cmd); out != (ipArtworkMaterialCommandResult{}) || !errors.Is(err, ErrResolveRequired) {
		t.Fatal("foreign incarnation admitted", out, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)

	first := ipArtworkMaterialIntentCommand(t, 1, 2, owner, job, spec)
	if _, turn, tx, _ = ipArtworkMaterialApply(t, w, r, p, IPArtworkMaterialIntent, first); true {
		ipPolicyCommitTurn(t, turn, tx)
	}
	// The same retained sequence with a different original digest is a conflict,
	// never a second intent or job claim.
	conflicting := ipArtworkMaterialIntentCommand(t, 1, 2, owner, job, spec)
	conflicting.request.spec.encoderRevision = 4
	conflicting.digest = first.digest
	turn, tx, b = ipPolicyTurn(t, w, r, IPArtworkMaterialIntent)
	if out, err := applyIPArtworkMaterial(t.Context(), turn, p, t.Context(), b, IPArtworkMaterialIntent, conflicting); out != (ipArtworkMaterialCommandResult{}) || !errors.Is(err, ErrConflict) {
		t.Fatal("tampered request admitted", out, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
	// A sequence gap is a conflict, not an implicit allocation.
	gap := ipArtworkMaterialIntentCommand(t, 3, 4, owner, job, spec)
	turn, tx, b = ipPolicyTurn(t, w, r, IPArtworkMaterialIntent)
	if out, err := applyIPArtworkMaterial(t.Context(), turn, p, t.Context(), b, IPArtworkMaterialIntent, gap); out != (ipArtworkMaterialCommandResult{}) || !errors.Is(err, ErrConflict) {
		t.Fatal("sequence gap admitted", out, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
	if got := ipPolicyQueryInt(t, w, `SELECT count(*) FROM artwork_material_intents`); got != 1 {
		t.Fatal("rejected commands wrote an intent", got)
	}
}

func TestIPArtworkMaterialManifestReadbackRefusesTamper(t *testing.T) {
	w, r, p := ipArtworkMaterialFixture(t)
	owner, job := ipArtworkMaterialOwnerFor(), ipArtworkMaterialJobFor()
	spec := ipArtworkMaterialSpecFor(job, 4)
	generation := ipArtworkMaterialDeclared(t, w, r, p, owner, job, spec)
	owner.revision = 2
	slots := ipArtworkMaterialSlotsFor(spec)
	installed := ipArtworkMaterialInstalledCommand(t, 2, 3, owner, spec, generation, 2, 1, slots)
	if _, turn, tx, _ := ipArtworkMaterialApply(t, w, r, p, IPArtworkMaterialInstalled, installed); true {
		ipPolicyCommitTurn(t, turn, tx)
	}
	turn, tx, _ := ipPolicyTurn(t, w, r, IPArtworkMaterialInstalled)
	total, err := ipArtworkMaterialManifest(t.Context(), turn, job.id, slots)
	if err != nil || total != ipArtworkMaterialSlotBytes(slots) {
		t.Fatal("complete manifest refused", total, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
	// A changed installed digest and a missing RAW slot both refuse the readback.
	ipPolicyExec(t, w, `UPDATE artwork_material_slots SET digest=? WHERE intent_id=? AND slot=1`, ipArtworkMaterialHex(8), job.id)
	turn, tx, _ = ipPolicyTurn(t, w, r, IPArtworkMaterialInstalled)
	if _, err = ipArtworkMaterialManifest(t.Context(), turn, job.id, slots); !errors.Is(err, ErrConflict) {
		t.Fatal("tampered manifest read back", err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
	ipPolicyExec(t, w, `UPDATE artwork_material_slots SET digest=? WHERE intent_id=? AND slot=1`, slots[1].digest, job.id)
	ipPolicyExec(t, w, `DELETE FROM artwork_material_slots WHERE intent_id=? AND slot=2`, job.id)
	turn, tx, _ = ipPolicyTurn(t, w, r, IPArtworkMaterialInstalled)
	if _, err = ipArtworkMaterialManifest(t.Context(), turn, job.id, slots); !errors.Is(err, ErrConflict) {
		t.Fatal("gapped manifest read back", err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
}

func TestIPArtworkMaterialBoundsAndDescriptors(t *testing.T) {
	owner, job := ipArtworkMaterialOwnerFor(), ipArtworkMaterialJobFor()
	spec := ipArtworkMaterialSpecFor(job, 4)
	identity := CommandIdentity{Incarnation: owner.incarnation, Sequence: 1, OperationID: [16]byte{2}}
	for name, mutate := range map[string]func(*ipArtworkMaterialRequest){
		"both_namespaces":  func(q *ipArtworkMaterialRequest) { q.owner.personID = 778 },
		"no_namespace":     func(q *ipArtworkMaterialRequest) { q.owner.entityID = 0 },
		"zero_lease_owner": func(q *ipArtworkMaterialRequest) { q.owner.leaseOwner = 0 },
		"zero_lease_until": func(q *ipArtworkMaterialRequest) { q.owner.leaseUntilMS = 0 },
		"zero_source":      func(q *ipArtworkMaterialRequest) { q.owner.sourceIncarnation = [16]byte{} },
		"zero_revision":    func(q *ipArtworkMaterialRequest) { q.owner.revision = 0 },
		"foreign_incarn":   func(q *ipArtworkMaterialRequest) { q.owner.incarnation = [16]byte{9} },
		"musicbrainz":      func(q *ipArtworkMaterialRequest) { q.spec.provider = "musicbrainz"; q.job.provider = "musicbrainz" },
		"unknown_provider": func(q *ipArtworkMaterialRequest) { q.spec.provider = "plex" },
		"thumbnail_role":   func(q *ipArtworkMaterialRequest) { q.job.role = "thumbnail" },
		"unknown_choice":   func(q *ipArtworkMaterialRequest) { q.job.choice = "auto" },
		"empty_image":      func(q *ipArtworkMaterialRequest) { q.job.imageID = "" },
		"job_id_mismatch":  func(q *ipArtworkMaterialRequest) { q.job.id = "other" },
		"zero_policy":      func(q *ipArtworkMaterialRequest) { q.spec.policyRevision = 0 },
		"zero_encoder":     func(q *ipArtworkMaterialRequest) { q.spec.encoderRevision = 0 },
		"bad_edge":         func(q *ipArtworkMaterialRequest) { q.spec.largeEdge = 1024 },
		"two_slots":        func(q *ipArtworkMaterialRequest) { q.spec.expectedSlots = 2 },
		"five_slots":       func(q *ipArtworkMaterialRequest) { q.spec.expectedSlots = 5 },
		"short_digest":     func(q *ipArtworkMaterialRequest) { q.spec.sourceDigest = "abc" },
		"upper_digest":     func(q *ipArtworkMaterialRequest) { q.spec.sourceDigest = strings.Repeat("A", 64) },
		"bad_mime":         func(q *ipArtworkMaterialRequest) { q.spec.acceptedMIME = "image/gif" },
		"zero_width":       func(q *ipArtworkMaterialRequest) { q.spec.acceptedWidth = 0 },
		"long_language":    func(q *ipArtworkMaterialRequest) { q.spec.language = strings.Repeat("a", 65) },
		"long_intent": func(q *ipArtworkMaterialRequest) {
			q.spec.intentID = strings.Repeat("i", 129)
			q.job.id = q.spec.intentID
		},
		"intent_with_slots": func(q *ipArtworkMaterialRequest) { q.slots = ipArtworkMaterialSlotsFor(spec) },
		"intent_with_gen":   func(q *ipArtworkMaterialRequest) { q.intentGeneration = 1 },
		"intent_with_claim": func(q *ipArtworkMaterialRequest) { q.jobRevision = 2 },
	} {
		t.Run(name, func(t *testing.T) {
			q := ipArtworkMaterialRequest{owner: owner, job: job, spec: spec}
			mutate(&q)
			if c, err := newIPArtworkMaterialCommand(IPArtworkMaterialIntent, identity, q); c != nil || !errors.Is(err, ErrInvalid) {
				t.Fatal("malformed intent admitted", c, err)
			}
		})
	}
	installedSpec := spec
	for name, mutate := range map[string]func(*ipArtworkMaterialRequest){
		"job_witness":       func(q *ipArtworkMaterialRequest) { q.job = job },
		"zero_generation":   func(q *ipArtworkMaterialRequest) { q.intentGeneration = 0 },
		"wrong_revision":    func(q *ipArtworkMaterialRequest) { q.intentRevision = 2 },
		"preclaim_revision": func(q *ipArtworkMaterialRequest) { q.jobRevision = 1 },
		"zero_job_gen":      func(q *ipArtworkMaterialRequest) { q.jobGeneration = 0 },
		"no_slots":          func(q *ipArtworkMaterialRequest) { q.slots = nil },
		"short_manifest":    func(q *ipArtworkMaterialRequest) { q.slots = ipArtworkMaterialSlotsFor(installedSpec)[:3] },
		"gap":               func(q *ipArtworkMaterialRequest) { q.slots[1].slot = 5 },
		"duplicate_digest":  func(q *ipArtworkMaterialRequest) { q.slots[1].digest = q.slots[0].digest },
		"encoded_webp":      func(q *ipArtworkMaterialRequest) { q.slots[1].mime = "image/webp" },
		"foreign_original":  func(q *ipArtworkMaterialRequest) { q.slots[3].digest = ipArtworkMaterialHex(9) },
		"zero_bytes":        func(q *ipArtworkMaterialRequest) { q.slots[0].bytes = 0 },
		"oversize_slot":     func(q *ipArtworkMaterialRequest) { q.slots[0].bytes = ipArtworkMaterialMaxSlot + 1 },
	} {
		t.Run(name, func(t *testing.T) {
			q := ipArtworkMaterialRequest{owner: owner, spec: installedSpec, intentGeneration: 1, intentRevision: 1,
				jobRevision: 2, jobGeneration: 1, slots: ipArtworkMaterialSlotsFor(installedSpec)}
			mutate(&q)
			if c, err := newIPArtworkMaterialCommand(IPArtworkMaterialInstalled, identity, q); c != nil || !errors.Is(err, ErrInvalid) {
				t.Fatal("malformed installed handoff admitted", c, err)
			}
		})
	}
	// Bounded maximum: a 128-byte intent id and the largest admissible manifest.
	long := strings.Repeat("i", 128)
	maxJob := job
	maxJob.id = long
	maxSpec := ipArtworkMaterialSpecFor(maxJob, 3)
	maxSpec.expectedSlots = 3
	maxSlots := ipArtworkMaterialSlotsFor(maxSpec)
	for n := range maxSlots {
		maxSlots[n].bytes = 40 << 20
	}
	if _, err := newIPArtworkMaterialCommand(IPArtworkMaterialIntent, identity,
		ipArtworkMaterialRequest{owner: owner, job: maxJob, spec: maxSpec}); err != nil {
		t.Fatal("bounded maximum intent refused", err)
	}
	maxInstalled := ipArtworkMaterialRequest{owner: owner, spec: maxSpec, intentGeneration: 1,
		intentRevision: 1, jobRevision: 2, jobGeneration: 1, slots: maxSlots}
	if _, err := newIPArtworkMaterialCommand(IPArtworkMaterialInstalled, identity, maxInstalled); err != nil {
		t.Fatal("bounded maximum manifest refused", err)
	}
	maxSlots[0].bytes = ipArtworkMaterialMaxSlot
	if _, err := newIPArtworkMaterialCommand(IPArtworkMaterialInstalled, identity, maxInstalled); !errors.Is(err, ErrInvalid) {
		t.Fatal("oversize manifest admitted", err)
	}
	for _, expected := range []Descriptor{
		{IPArtworkMaterialIntent, "ip_artwork_material_intent", Normal},
		{IPArtworkMaterialInstalled, "ip_artwork_material_installed", Normal},
	} {
		actual, err := Describe(expected.ID)
		if err != nil || actual != expected || actual.ID < 223 || actual.ID > 224 {
			t.Fatal(actual, err)
		}
	}
}

// IP-19 pins the intent id in the receipt, so a stored text longer than the
// request bound is refused before any copy or decode (removing the octet guard
// would decode and compare it instead).
func TestIPArtworkMaterialMalformedReceiptRefused(t *testing.T) {
	w, r, _ := ipArtworkMaterialFixture(t)
	long := strings.Repeat("i", ipArtworkMaterialMaxIntent+1)
	ipPolicyExec(t, w, `INSERT INTO artwork_material_intents(id,owner_id,owner_incarnation,generation,revision,source_digest,accepted_mime,accepted_width,accepted_height,large_edge,encoder_revision,state,expected_slots,staged_slots) VALUES(?,901,X'01000000000000000000000000000000',1,1,?,?,3000,2000,1920,3,'prepared',4,0)`,
		long, ipArtworkMaterialHex(1), "image/jpeg")
	ipPolicyExec(t, w, `INSERT INTO artwork_command_receipts(owner_id,command_incarnation,command_sequence,operation_id,request_digest,applied_ms,result_digest,command_kind,accepted_revision,result_count,result_bytes,lease_generation,lease_until_ms,material_intent_id) VALUES(901,X'01000000000000000000000000000000',1,X'02000000000000000000000000000000',X'0300000000000000000000000000000000000000000000000000000000000000',1,X'0400000000000000000000000000000000000000000000000000000000000000','job_claim',1,4,0,1,?,?)`,
		int64(1)<<50, long)
	ipPolicyExec(t, w, `UPDATE artwork_subject_jobs SET last_command_sequence=1,acknowledged_command_sequence=0 WHERE id=901`)
	owner, job := ipArtworkMaterialOwnerFor(), ipArtworkMaterialJobFor()
	cmd := ipArtworkMaterialIntentCommand(t, 1, 2, owner, job, ipArtworkMaterialSpecFor(job, 4))
	turn, tx, _ := ipPolicyTurn(t, w, r, IPArtworkMaterialIntent)
	if _, err := ipArtworkMaterialLookup(t.Context(), turn, IPArtworkMaterialIntent, cmd.request, cmd.identity, cmd.digest); !errors.Is(err, ErrResolveRequired) {
		t.Fatal("over-long pinned intent id decoded", err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
}

// An intent is durable BEFORE any file is renamed, and an install that rolls
// back (process crash before COMMIT) leaves the prepared intent and the claimed
// job intact so the caller can verify the renamed files and retry the same
// handoff. No file is read here; the caller owns physical custody.
func TestIPArtworkMaterialIntentSurvivesRestartBeforeInstall(t *testing.T) {
	w, r, p := ipArtworkMaterialFixture(t)
	owner, job := ipArtworkMaterialOwnerFor(), ipArtworkMaterialJobFor()
	spec := ipArtworkMaterialSpecFor(job, 4)
	generation := ipArtworkMaterialDeclared(t, w, r, p, owner, job, spec)
	path := ipPolicyDatabasePath(t, w)
	for query, want := range map[string]int64{
		`SELECT count(*) FROM artwork_material_intents WHERE state='prepared' AND staged_slots=0`: 1,
		`SELECT count(*) FROM artwork_material_slots`:                                             0,
		`SELECT status='running' FROM artwork_jobs WHERE id='job-entity-poster'`:                  1,
		`SELECT count(*) FROM artwork_command_receipts`:                                           1,
	} {
		if got := ipPolicyFreshInt(t, path, query); got != want {
			t.Fatal("restart before install", query, got, want)
		}
	}
	owner.revision = 2
	slots := ipArtworkMaterialSlotsFor(spec)
	installed := ipArtworkMaterialInstalledCommand(t, 2, 3, owner, spec, generation, 2, 1, slots)
	// A crashed attempt rolls back whole; the rename is not acknowledged.
	_, turn, tx, _ := ipArtworkMaterialApply(t, w, r, p, IPArtworkMaterialInstalled, installed)
	ipPolicyRollbackTurn(t, turn, tx)
	for query, want := range map[string]int64{
		`SELECT count(*) FROM artwork_material_slots`:                            0,
		`SELECT count(*) FROM artwork_command_receipts`:                          1,
		`SELECT count(*) FROM artwork_material_intents WHERE state='prepared'`:   1,
		`SELECT status='running' FROM artwork_jobs WHERE id='job-entity-poster'`: 1,
	} {
		if got := ipPolicyQueryInt(t, w, query); got != want {
			t.Fatal("rolled-back install leaked", query, got, want)
		}
	}
	// The restart re-verifies the same files and commits the same handoff.
	out, turn, tx, _ := ipArtworkMaterialApply(t, w, r, p, IPArtworkMaterialInstalled, installed)
	if out.Outcome.State != Applied || out.Value.Revision != 2 {
		t.Fatal(out)
	}
	ipPolicyCommitTurn(t, turn, tx)
	if got := ipPolicyQueryInt(t, w, `SELECT count(*) FROM artwork_material_slots`); got != 4 {
		t.Fatal("retried install lost its manifest", got)
	}
}

// No filesystem, provider or HTTP access can happen inside the SQLite turn: the
// writer files must not import an IO or network package. Physical verification
// and rename stay with the caller before the installed handoff.
func TestIPArtworkMaterialIOStaysOutsideHold(t *testing.T) {
	for _, file := range []string{
		"ip_artwork_material_types.go",
		"ip_artwork_material_handoff.go",
		"ip_artwork_material_result.go",
		"ip_artwork_job.go",
		"ip_artwork_material_statements.go",
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
