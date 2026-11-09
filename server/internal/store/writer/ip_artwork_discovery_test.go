package writer

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

// Group E is embedded, so discovery is qualified against the canonical
// subject-job owner, discovery stages/candidates and the closed artwork receipt
// table. No Capture/golden claim is made here.
func ipArtworkDiscoveryFixture(t *testing.T) (*executor, *ipPolicyRegistry, *usOwnerAuthorityProof) {
	t.Helper()
	w, keeper, p := ipPolicySecurityFixture(t)
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
	ipPolicyExec(t, w, `INSERT INTO artwork_subject_jobs(id,entity_id,source_incarnation,source_revision,phase,job_revision,job_lease_owner,job_lease_generation,job_lease_expires_ms,command_incarnation) VALUES(901,778,X'04000000000000000000000000000000',1,'providers',1,7,1,?,X'01000000000000000000000000000000')`, leaseUntil)
	ipPolicyExec(t, w, `INSERT INTO artwork_subject_jobs(id,person_id,source_incarnation,source_revision,phase,job_revision,job_lease_owner,job_lease_generation,job_lease_expires_ms,command_incarnation) VALUES(902,778,X'05000000000000000000000000000000',2,'providers',1,7,1,?,X'06000000000000000000000000000000')`, leaseUntil)
	r, err := stmttest.New(t, append(usAuthorityDefinitions(), ipArtworkDiscoveryDefinitions()...))
	if err != nil {
		t.Fatal(err)
	}
	return w, &ipPolicyRegistry{r, keeper}, p
}

func ipArtworkDiscoveryOwnerFor() ipArtworkDiscoveryOwner {
	return ipArtworkDiscoveryOwner{id: 901, incarnation: [16]byte{1}, entityID: 778,
		sourceIncarnation: [16]byte{4}, sourceRevision: 1, revision: 1,
		leaseOwner: 7, leaseGeneration: 1, leaseUntilMS: 1 << 50}
}

func ipArtworkDiscoveryPersonOwnerFor() ipArtworkDiscoveryOwner {
	return ipArtworkDiscoveryOwner{id: 902, incarnation: [16]byte{6}, personID: 778,
		sourceIncarnation: [16]byte{5}, sourceRevision: 2, revision: 1,
		leaseOwner: 7, leaseGeneration: 1, leaseUntilMS: 1 << 50}
}

// Canonical role order with unique per-role source ordinals and stable RAW
// ordinals; the writer never reorders the provider page.
func ipArtworkDiscoveryRows(count int) []ipArtworkDiscoveryCandidate {
	roles := []string{"poster", "backdrop", "still", "logo", "square", "portrait", "banner", "cover"}
	out := make([]ipArtworkDiscoveryCandidate, count)
	for n := range out {
		out[n] = ipArtworkDiscoveryCandidate{
			ordinal: int64(n), sourceOrdinal: int64(n / len(roles)), role: roles[n%len(roles)],
			imageID:     fmt.Sprintf("image-%03d", n),
			origin:      fmt.Sprintf("https://images.example.test/%03d.jpg", n),
			locale:      "en",
			attribution: "Example",
			score:       float64(100 - n),
		}
	}
	return out
}

func ipArtworkDiscoverySpecFor(owner ipArtworkDiscoveryOwner, rows []ipArtworkDiscoveryCandidate) ipArtworkDiscoverySpec {
	spec := ipArtworkDiscoverySpec{provider: "tmdb", language: "en", policyRevision: 1, consentRevision: 1,
		generation: 1, expectedCount: int64(len(rows))}
	for _, candidate := range rows {
		size, err := ipArtworkDiscoveryCandidateBytes(candidate)
		if err != nil {
			panic(err)
		}
		spec.expectedBytes += size
	}
	spec.manifest = ipArtworkDiscoveryManifestDigest(owner, spec, rows)
	return spec
}

func ipArtworkDiscoveryNewCommand(t *testing.T, kind CommandID, sequence int64, operation byte,
	owner ipArtworkDiscoveryOwner, spec ipArtworkDiscoverySpec, stageID, stageRevision, afterOrdinal int64,
	candidates []ipArtworkDiscoveryCandidate) *ipArtworkDiscoveryCommand {
	t.Helper()
	identity := CommandIdentity{Incarnation: owner.incarnation, Sequence: sequence, OperationID: [16]byte{operation}}
	c, err := newIPArtworkDiscoveryCommand(kind, identity, ipArtworkDiscoveryRequest{owner: owner, spec: spec,
		stageID: stageID, stageRevision: stageRevision, afterOrdinal: afterOrdinal, candidates: candidates})
	if err != nil {
		t.Fatal(kind, err)
	}
	return c
}

func ipArtworkDiscoveryApply(t *testing.T, w *executor, r *ipPolicyRegistry, p *usOwnerAuthorityProof,
	kind CommandID, c *ipArtworkDiscoveryCommand) (ipArtworkDiscoveryCommandResult, *commandTurn, *transaction, ipPolicyBudget) {
	t.Helper()
	turn, tx, b := ipPolicyTurn(t, w, r, kind)
	out, err := applyIPArtworkDiscovery(t.Context(), turn, p, t.Context(), b, kind, c)
	if err != nil {
		t.Fatal("discovery", kind, out, err)
	}
	return out, turn, tx, b
}

func ipArtworkDiscoveryAck(t *testing.T, w *executor, sequence int64) {
	t.Helper()
	ipPolicyExec(t, w, `UPDATE artwork_subject_jobs SET last_command_sequence=max(last_command_sequence,?),acknowledged_command_sequence=?,receipt_reclaim_due_ms=1 WHERE id IN(901,902)`, sequence, sequence)
}

// Discovery produces an immutable candidate head only: no artwork source,
// selected image, installed material or job material exists yet.
func ipArtworkDiscoveryNoMaterial(t *testing.T, w *executor) {
	t.Helper()
	for _, query := range []string{
		`SELECT count(*) FROM artwork_sources`,
		`SELECT count(*) FROM artwork_blobs`,
		`SELECT count(*) FROM artwork_jobs`,
		`SELECT count(*) FROM artwork_selections`,
		`SELECT count(*) FROM artwork_material_intents`,
	} {
		if got := ipPolicyQueryInt(t, w, query); got != 0 {
			t.Fatal("discovery claimed file-ready material", query, got)
		}
	}
}

// ipArtworkDiscoveryClaimed commits a claim and ACKs it. The caller owns the
// following page or finish request.
func ipArtworkDiscoveryClaimed(t *testing.T, w *executor, r *ipPolicyRegistry, p *usOwnerAuthorityProof, count int) (ipArtworkDiscoveryOwner, ipArtworkDiscoverySpec, int64) {
	t.Helper()
	owner := ipArtworkDiscoveryOwnerFor()
	spec := ipArtworkDiscoverySpecFor(owner, ipArtworkDiscoveryRows(count))
	claim := ipArtworkDiscoveryNewCommand(t, IPArtworkDiscoveryClaim, 1, 2, owner, spec, 0, 0, 0, nil)
	out, turn, tx, _ := ipArtworkDiscoveryApply(t, w, r, p, IPArtworkDiscoveryClaim, claim)
	ipPolicyCommitTurn(t, turn, tx)
	ipArtworkDiscoveryAck(t, w, 1)
	return owner, spec, out.Value.StageID
}

func TestIPArtworkDiscoveryClaimPagesFinishAndReplayRetired(t *testing.T) {
	w, r, p := ipArtworkDiscoveryFixture(t)
	rows := ipArtworkDiscoveryRows(20)
	owner := ipArtworkDiscoveryOwnerFor()
	spec := ipArtworkDiscoverySpecFor(owner, rows)

	claim := ipArtworkDiscoveryNewCommand(t, IPArtworkDiscoveryClaim, 1, 2, owner, spec, 0, 0, 0, nil)
	out, turn, tx, _ := ipArtworkDiscoveryApply(t, w, r, p, IPArtworkDiscoveryClaim, claim)
	if out.Outcome.State != Applied || out.Outcome.Replayed || out.Value.StageID <= 0 ||
		out.Value.Revision != 1 || out.Value.Count != 0 || out.Value.AfterOrdinal != 0 ||
		out.Digest == (ResultDigest{}) {
		t.Fatal(out)
	}
	stageID := out.Value.StageID
	ipPolicyCommitTurn(t, turn, tx)
	original := out
	stageQuery := fmt.Sprintf(`SELECT %%s FROM artwork_discovery_stages WHERE id=%d`, stageID)
	for query, want := range map[string]int64{
		fmt.Sprintf(stageQuery, "staged_count"):                                              0,
		fmt.Sprintf(stageQuery, "staged_bytes"):                                              0,
		fmt.Sprintf(stageQuery, "expected_bytes"):                                            spec.expectedBytes,
		`SELECT count(*) FROM artwork_discovery_stages WHERE state='building'`:               1,
		`SELECT last_command_sequence FROM artwork_subject_jobs WHERE id=901`:                1,
		`SELECT acknowledged_command_sequence FROM artwork_subject_jobs WHERE id=901`:        0,
		`SELECT job_revision FROM artwork_subject_jobs WHERE id=901`:                         2,
		`SELECT count(*) FROM artwork_command_receipts WHERE command_kind='discovery_claim'`: 1,
		`SELECT stage_id FROM artwork_command_receipts WHERE command_kind='discovery_claim'`: stageID,
		`SELECT count(*) FROM artwork_discovery_candidates`:                                  0,
	} {
		if got := ipPolicyQueryInt(t, w, query); got != want {
			t.Fatal(query, got, want)
		}
	}
	if got := ipPolicyString(t, w, `SELECT hex(manifest_digest) FROM artwork_discovery_stages WHERE id=?`, stageID); got != fmt.Sprintf("%X", spec.manifest) {
		t.Fatal("stage manifest", got)
	}
	// The original result replays after source, language, lease and revision move.
	ipPolicyExec(t, w, `UPDATE catalog_entities SET artwork_revision=9 WHERE id=778`)
	ipPolicyExec(t, w, `UPDATE metadata_runtime_settings SET artwork_language='textless'`)
	ipPolicyExec(t, w, `UPDATE artwork_subject_jobs SET job_revision=99,job_lease_owner=9 WHERE id=901`)
	turn, tx, b := ipPolicyTurn(t, w, r, IPArtworkDiscoveryClaim)
	replay, err := applyIPArtworkDiscovery(t.Context(), turn, p, t.Context(), b, IPArtworkDiscoveryClaim, claim)
	if err != nil || !replay.Outcome.Replayed || replay.Digest != original.Digest || replay.Value != original.Value {
		t.Fatal("source change altered the original claim", replay, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
	if got := ipPolicyQueryInt(t, w, `SELECT count(*) FROM artwork_command_receipts`); got != 1 {
		t.Fatal("replay wrote receipts", got)
	}
	ipPolicyExec(t, w, `UPDATE catalog_entities SET artwork_revision=1 WHERE id=778`)
	ipPolicyExec(t, w, `UPDATE metadata_runtime_settings SET artwork_language='same'`)
	ipPolicyExec(t, w, `UPDATE artwork_subject_jobs SET job_revision=2,job_lease_owner=7 WHERE id=901`)
	ipArtworkDiscoveryAck(t, w, 1)

	// At or below ACK is Retired while the receipt and stage remain physical.
	turn, tx, b = ipPolicyTurn(t, w, r, IPArtworkDiscoveryClaim)
	retired, err := applyIPArtworkDiscovery(t.Context(), turn, p, t.Context(), b, IPArtworkDiscoveryClaim, claim)
	if err != nil || retired.Outcome.State != ResultRetired || retired.Value != (ipArtworkDiscoveryResult{}) ||
		retired.Digest != (ResultDigest{}) {
		t.Fatal("retired claim result served", retired, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)

	// Page one: RAW ordinals 0..15.
	owner.revision = 2
	page1 := ipArtworkDiscoveryNewCommand(t, IPArtworkDiscoveryPage, 2, 3, owner, spec, stageID, 1, 0, rows[:16])
	out, turn, tx, _ = ipArtworkDiscoveryApply(t, w, r, p, IPArtworkDiscoveryPage, page1)
	if out.Value.Count != 16 || out.Value.Revision != 2 || out.Value.AfterOrdinal != 16 ||
		out.Value.StageID != stageID || out.Outcome.State != Applied {
		t.Fatal(out)
	}
	ipPolicyCommitTurn(t, turn, tx)
	pageOne := out
	for query, want := range map[string]int64{
		fmt.Sprintf(stageQuery, "staged_count"):                                             16,
		fmt.Sprintf(stageQuery, "revision"):                                                 2,
		`SELECT count(*) FROM artwork_discovery_candidates WHERE ordinal<16`:                16,
		`SELECT count(*) FROM artwork_subject_jobs WHERE id=901 AND job_revision=3`:         1,
		`SELECT count(*) FROM artwork_command_receipts WHERE command_kind='discovery_page'`: 1,
	} {
		if got := ipPolicyQueryInt(t, w, query); got != want {
			t.Fatal(query, got, want)
		}
	}
	// Page replay after a source and lease change is exact with no effect.
	ipPolicyExec(t, w, `UPDATE catalog_entities SET artwork_revision=9 WHERE id=778`)
	ipPolicyExec(t, w, `UPDATE artwork_subject_jobs SET job_lease_owner=11 WHERE id=901`)
	turn, tx, b = ipPolicyTurn(t, w, r, IPArtworkDiscoveryPage)
	replay, err = applyIPArtworkDiscovery(t.Context(), turn, p, t.Context(), b, IPArtworkDiscoveryPage, page1)
	if err != nil || !replay.Outcome.Replayed || replay.Digest != pageOne.Digest || replay.Value != pageOne.Value {
		t.Fatal("page replay", replay, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
	ipPolicyExec(t, w, `UPDATE catalog_entities SET artwork_revision=1 WHERE id=778`)
	ipPolicyExec(t, w, `UPDATE artwork_subject_jobs SET job_lease_owner=7 WHERE id=901`)
	ipArtworkDiscoveryAck(t, w, 2)

	// Page two: RAW ordinals 16..19.
	owner.revision = 3
	page2 := ipArtworkDiscoveryNewCommand(t, IPArtworkDiscoveryPage, 3, 4, owner, spec, stageID, 2, 16, rows[16:])
	out, turn, tx, _ = ipArtworkDiscoveryApply(t, w, r, p, IPArtworkDiscoveryPage, page2)
	if out.Value.Count != 20 || out.Value.Revision != 3 || out.Value.AfterOrdinal != 20 {
		t.Fatal(out)
	}
	ipPolicyCommitTurn(t, turn, tx)
	ipArtworkDiscoveryAck(t, w, 3)

	// Finish re-reads the complete page in RAW order and seals it.
	owner.revision = 4
	finish := ipArtworkDiscoveryNewCommand(t, IPArtworkDiscoveryFinish, 4, 5, owner, spec, stageID, 3, 20, nil)
	out, turn, tx, _ = ipArtworkDiscoveryApply(t, w, r, p, IPArtworkDiscoveryFinish, finish)
	if out.Value.Count != 20 || out.Value.Revision != 4 || out.Value.AfterOrdinal != 20 ||
		out.Value.StageID != stageID || out.Outcome.State != Applied {
		t.Fatal(out)
	}
	ipPolicyCommitTurn(t, turn, tx)
	finished := out
	ipArtworkDiscoveryNoMaterial(t, w)
	for query, want := range map[string]int64{
		`SELECT count(*) FROM artwork_discovery_stages WHERE state='ready'`:                            1,
		fmt.Sprintf(stageQuery, "staged_count"):                                                        20,
		`SELECT count(*) FROM artwork_discovery_candidates WHERE ordinal BETWEEN 0 AND 19`:             20,
		`SELECT count(*) FROM artwork_command_receipts WHERE command_kind='discovery_finish'`:          1,
		`SELECT accepted_revision FROM artwork_command_receipts WHERE command_kind='discovery_finish'`: 4,
	} {
		if got := ipPolicyQueryInt(t, w, query); got != want {
			t.Fatal(query, got, want)
		}
	}
	// A finished stage is terminal for discovery: the original result replays,
	// then ACK retires it; no page may append beneath the sealed head.
	ipPolicyExec(t, w, `UPDATE catalog_entities SET artwork_revision=7 WHERE id=778`)
	turn, tx, b = ipPolicyTurn(t, w, r, IPArtworkDiscoveryFinish)
	replay, err = applyIPArtworkDiscovery(t.Context(), turn, p, t.Context(), b, IPArtworkDiscoveryFinish, finish)
	if err != nil || !replay.Outcome.Replayed || replay.Digest != finished.Digest || replay.Value != finished.Value {
		t.Fatal("finish replay", replay, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
	ipArtworkDiscoveryAck(t, w, 4)
	turn, tx, b = ipPolicyTurn(t, w, r, IPArtworkDiscoveryFinish)
	retired, err = applyIPArtworkDiscovery(t.Context(), turn, p, t.Context(), b, IPArtworkDiscoveryFinish, finish)
	if err != nil || retired.Outcome.State != ResultRetired || retired.Value != (ipArtworkDiscoveryResult{}) {
		t.Fatal("retired finish", retired, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
	owner.revision = 5
	late := ipArtworkDiscoveryNewCommand(t, IPArtworkDiscoveryPage, 5, 6, owner, spec, stageID, 4, 16, rows[16:])
	turn, tx, b = ipPolicyTurn(t, w, r, IPArtworkDiscoveryPage)
	if refused, err := applyIPArtworkDiscovery(t.Context(), turn, p, t.Context(), b, IPArtworkDiscoveryPage, late); refused != (ipArtworkDiscoveryCommandResult{}) || !errors.Is(err, ErrConflict) {
		t.Fatal("page appended beneath a ready head", refused, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
}

func TestIPArtworkDiscoveryEmptyPageAndMaximumDeclaration(t *testing.T) {
	w, r, p := ipArtworkDiscoveryFixture(t)
	owner := ipArtworkDiscoveryOwnerFor()
	spec := ipArtworkDiscoverySpecFor(owner, nil)
	if spec.expectedCount != 0 || spec.expectedBytes != 0 {
		t.Fatal(spec)
	}
	claim := ipArtworkDiscoveryNewCommand(t, IPArtworkDiscoveryClaim, 1, 2, owner, spec, 0, 0, 0, nil)
	out, turn, tx, _ := ipArtworkDiscoveryApply(t, w, r, p, IPArtworkDiscoveryClaim, claim)
	stageID := out.Value.StageID
	ipPolicyCommitTurn(t, turn, tx)
	ipArtworkDiscoveryAck(t, w, 1)
	owner.revision = 2
	finish := ipArtworkDiscoveryNewCommand(t, IPArtworkDiscoveryFinish, 2, 3, owner, spec, stageID, 1, 0, nil)
	out, turn, tx, _ = ipArtworkDiscoveryApply(t, w, r, p, IPArtworkDiscoveryFinish, finish)
	if out.Outcome.State != Applied || out.Value.Count != 0 || out.Value.Revision != 2 {
		t.Fatal(out)
	}
	ipPolicyCommitTurn(t, turn, tx)
	for query, want := range map[string]int64{
		`SELECT count(*) FROM artwork_discovery_stages WHERE state='ready' AND staged_count=0 AND expected_count=0`: 1,
		`SELECT count(*) FROM artwork_discovery_candidates`:                                                         0,
	} {
		if got := ipPolicyQueryInt(t, w, query); got != want {
			t.Fatal(query, got)
		}
	}
	// The declared maximum page is admissible even though it spans ten pages.
	maxOwner := ipArtworkDiscoveryOwnerFor()
	maxSpec := ipArtworkDiscoverySpec{provider: "tmdb", language: "en", policyRevision: 1, consentRevision: 1,
		generation: 1, expectedCount: ipArtworkDiscoveryMaxCandidates,
		expectedBytes: 64 * ipArtworkDiscoveryMaxCandidates}
	maxSpec.manifest = ipArtworkDiscoveryManifestDigest(maxOwner, maxSpec, nil)
	if _, err := newIPArtworkDiscoveryCommand(IPArtworkDiscoveryClaim,
		CommandIdentity{Incarnation: maxOwner.incarnation, Sequence: 1, OperationID: [16]byte{2}},
		ipArtworkDiscoveryRequest{owner: maxOwner, spec: maxSpec}); err != nil {
		t.Fatal("maximum declaration refused", err)
	}
}

func TestIPArtworkDiscoveryFinishRefusesIncompleteOrTamperedPage(t *testing.T) {
	w, r, p := ipArtworkDiscoveryFixture(t)
	rows := ipArtworkDiscoveryRows(20)
	owner := ipArtworkDiscoveryOwnerFor()
	spec := ipArtworkDiscoverySpecFor(owner, rows)

	claim := ipArtworkDiscoveryNewCommand(t, IPArtworkDiscoveryClaim, 1, 2, owner, spec, 0, 0, 0, nil)
	out, turn, tx, _ := ipArtworkDiscoveryApply(t, w, r, p, IPArtworkDiscoveryClaim, claim)
	stageID := out.Value.StageID
	ipPolicyCommitTurn(t, turn, tx)
	ipArtworkDiscoveryAck(t, w, 1)

	// Finish before a single page exists is a conflict, not an empty seal.
	owner.revision = 2
	early := ipArtworkDiscoveryNewCommand(t, IPArtworkDiscoveryFinish, 2, 3, owner, spec, stageID, 1, 20, nil)
	turn, tx, b := ipPolicyTurn(t, w, r, IPArtworkDiscoveryFinish)
	refused, err := applyIPArtworkDiscovery(t.Context(), turn, p, t.Context(), b, IPArtworkDiscoveryFinish, early)
	if refused != (ipArtworkDiscoveryCommandResult{}) || !errors.Is(err, ErrConflict) {
		t.Fatal("incomplete page sealed", refused, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
	for query, want := range map[string]int64{
		`SELECT count(*) FROM artwork_command_receipts WHERE command_kind='discovery_finish'`: 0,
		`SELECT count(*) FROM artwork_discovery_stages WHERE state='ready'`:                   0,
		`SELECT last_command_sequence FROM artwork_subject_jobs WHERE id=901`:                 1,
	} {
		if got := ipPolicyQueryInt(t, w, query); got != want {
			t.Fatal(query, got, want)
		}
	}

	// A complete first page still cannot finish a declared two-page head.
	owner.revision = 2
	page1 := ipArtworkDiscoveryNewCommand(t, IPArtworkDiscoveryPage, 2, 3, owner, spec, stageID, 1, 0, rows[:16])
	_, turn, tx, _ = ipArtworkDiscoveryApply(t, w, r, p, IPArtworkDiscoveryPage, page1)
	ipPolicyCommitTurn(t, turn, tx)
	ipArtworkDiscoveryAck(t, w, 2)
	owner.revision = 3
	prefix := ipArtworkDiscoveryNewCommand(t, IPArtworkDiscoveryFinish, 3, 4, owner, spec, stageID, 2, 20, nil)
	turn, tx, b = ipPolicyTurn(t, w, r, IPArtworkDiscoveryFinish)
	if refused, err = applyIPArtworkDiscovery(t.Context(), turn, p, t.Context(), b, IPArtworkDiscoveryFinish, prefix); refused != (ipArtworkDiscoveryCommandResult{}) || !errors.Is(err, ErrConflict) {
		t.Fatal("staged prefix sealed", refused, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)

	owner.revision = 3
	page2 := ipArtworkDiscoveryNewCommand(t, IPArtworkDiscoveryPage, 3, 4, owner, spec, stageID, 2, 16, rows[16:])
	_, turn, tx, _ = ipArtworkDiscoveryApply(t, w, r, p, IPArtworkDiscoveryPage, page2)
	ipPolicyCommitTurn(t, turn, tx)
	ipArtworkDiscoveryAck(t, w, 3)

	// A tampered candidate no longer matches the original observation manifest
	// even though the stage counters look complete.
	ipPolicyExec(t, w, `UPDATE artwork_discovery_candidates SET attribution='Tampered' WHERE ordinal=7`)
	owner.revision = 4
	tampered := ipArtworkDiscoveryNewCommand(t, IPArtworkDiscoveryFinish, 4, 5, owner, spec, stageID, 3, 20, nil)
	turn, tx, b = ipPolicyTurn(t, w, r, IPArtworkDiscoveryFinish)
	if refused, err = applyIPArtworkDiscovery(t.Context(), turn, p, t.Context(), b, IPArtworkDiscoveryFinish, tampered); refused != (ipArtworkDiscoveryCommandResult{}) || !errors.Is(err, ErrConflict) {
		t.Fatal("tampered candidate sealed", refused, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
	if got := ipPolicyQueryInt(t, w, `SELECT count(*) FROM artwork_discovery_stages WHERE state='ready'`); got != 0 {
		t.Fatal("tampered page reached ready", got)
	}
	ipPolicyExec(t, w, `UPDATE artwork_discovery_candidates SET attribution='Example' WHERE ordinal=7`)

	// A missing RAW ordinal also refuses before any seal.
	ipPolicyExec(t, w, `DELETE FROM artwork_discovery_candidates WHERE ordinal=9`)
	owner.revision = 4
	missing := ipArtworkDiscoveryNewCommand(t, IPArtworkDiscoveryFinish, 4, 6, owner, spec, stageID, 3, 20, nil)
	turn, tx, b = ipPolicyTurn(t, w, r, IPArtworkDiscoveryFinish)
	if refused, err = applyIPArtworkDiscovery(t.Context(), turn, p, t.Context(), b, IPArtworkDiscoveryFinish, missing); refused != (ipArtworkDiscoveryCommandResult{}) || !errors.Is(err, ErrConflict) {
		t.Fatal("ordinal gap sealed", refused, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
	if got := ipPolicyQueryInt(t, w, `SELECT count(*) FROM artwork_command_receipts`); got != 3 {
		t.Fatal("refused finish wrote a receipt", got)
	}
}

func TestIPArtworkDiscoveryPageRawOrderAndBounds(t *testing.T) {
	for name, mutate := range map[string]func([]ipArtworkDiscoveryCandidate) []ipArtworkDiscoveryCandidate{
		"ordinal_gap": func(rows []ipArtworkDiscoveryCandidate) []ipArtworkDiscoveryCandidate {
			rows[1].ordinal = 5
			return rows
		},
		"duplicate_image": func(rows []ipArtworkDiscoveryCandidate) []ipArtworkDiscoveryCandidate {
			rows[1].role, rows[1].imageID = rows[0].role, rows[0].imageID
			return rows
		},
		"duplicate_source_ordinal": func(rows []ipArtworkDiscoveryCandidate) []ipArtworkDiscoveryCandidate {
			rows[1].role, rows[1].sourceOrdinal = rows[0].role, rows[0].sourceOrdinal
			return rows
		},
		"invalid_role": func(rows []ipArtworkDiscoveryCandidate) []ipArtworkDiscoveryCandidate {
			rows[1].role = "thumbnail"
			return rows
		},
		"invalid_locale": func(rows []ipArtworkDiscoveryCandidate) []ipArtworkDiscoveryCandidate {
			rows[1].locale = "not a locale!!"
			return rows
		},
		"overlong_attribution": func(rows []ipArtworkDiscoveryCandidate) []ipArtworkDiscoveryCandidate {
			rows[1].attribution = strings.Repeat("a", ipArtworkDiscoveryMaxText+1)
			return rows
		},
		"nan_score": func(rows []ipArtworkDiscoveryCandidate) []ipArtworkDiscoveryCandidate {
			rows[1].score = math.NaN()
			return rows
		},
		"missing_image": func(rows []ipArtworkDiscoveryCandidate) []ipArtworkDiscoveryCandidate {
			rows[1].imageID = ""
			return rows
		},
	} {
		t.Run(name, func(t *testing.T) {
			owner := ipArtworkDiscoveryOwnerFor()
			spec := ipArtworkDiscoverySpecFor(owner, ipArtworkDiscoveryRows(2))
			rows := mutate(ipArtworkDiscoveryRows(2))
			identity := CommandIdentity{Incarnation: owner.incarnation, Sequence: 2, OperationID: [16]byte{3}}
			c, err := newIPArtworkDiscoveryCommand(IPArtworkDiscoveryPage, identity, ipArtworkDiscoveryRequest{
				owner: owner, spec: spec, stageID: 1, stageRevision: 1, afterOrdinal: 0, candidates: rows})
			if c != nil || !errors.Is(err, ErrInvalid) {
				t.Fatal("malformed candidate page admitted", c, err)
			}
		})
	}

	// A cursor that no longer matches the stage is a conflict, and an existing
	// RAW key blocks a different page from being appended at that ordinal.
	w, r, p := ipArtworkDiscoveryFixture(t)
	rows := ipArtworkDiscoveryRows(4)
	owner := ipArtworkDiscoveryOwnerFor()
	spec := ipArtworkDiscoverySpecFor(owner, rows)
	claim := ipArtworkDiscoveryNewCommand(t, IPArtworkDiscoveryClaim, 1, 2, owner, spec, 0, 0, 0, nil)
	out, turn, tx, _ := ipArtworkDiscoveryApply(t, w, r, p, IPArtworkDiscoveryClaim, claim)
	stageID := out.Value.StageID
	ipPolicyCommitTurn(t, turn, tx)
	ipArtworkDiscoveryAck(t, w, 1)

	owner.revision = 2
	first := ipArtworkDiscoveryNewCommand(t, IPArtworkDiscoveryPage, 2, 3, owner, spec, stageID, 1, 0, rows[:2])
	_, turn, tx, _ = ipArtworkDiscoveryApply(t, w, r, p, IPArtworkDiscoveryPage, first)
	ipPolicyCommitTurn(t, turn, tx)
	ipArtworkDiscoveryAck(t, w, 2)

	stale := ipArtworkDiscoveryNewCommand(t, IPArtworkDiscoveryPage, 3, 4, owner, spec, stageID, 2, 0, rows[:2])
	turn, tx, b := ipPolicyTurn(t, w, r, IPArtworkDiscoveryPage)
	if refused, err := applyIPArtworkDiscovery(t.Context(), turn, p, t.Context(), b, IPArtworkDiscoveryPage, stale); refused != (ipArtworkDiscoveryCommandResult{}) || !errors.Is(err, ErrConflict) {
		t.Fatal("stale page cursor admitted", refused, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
	if got := ipPolicyQueryInt(t, w, `SELECT count(*) FROM artwork_discovery_candidates`); got != 2 {
		t.Fatal("stale page wrote candidates", got)
	}

	// A raw key already present at the next ordinal refuses the page even when
	// the declared cursor matches; the prefix is never overwritten.
	ipPolicyExec(t, w, `INSERT INTO artwork_discovery_candidates(stage_id,ordinal,role,source_ordinal,image_id,origin,locale,attribution,score) VALUES(?,2,'poster',9,'extra','https://images.example.test/extra.jpg','en','Example',1)`, stageID)
	prefix := ipArtworkDiscoveryNewCommand(t, IPArtworkDiscoveryPage, 3, 5, owner, spec, stageID, 2, 2, rows[2:])
	turn, tx, b = ipPolicyTurn(t, w, r, IPArtworkDiscoveryPage)
	if refused, err := applyIPArtworkDiscovery(t.Context(), turn, p, t.Context(), b, IPArtworkDiscoveryPage, prefix); refused != (ipArtworkDiscoveryCommandResult{}) || !errors.Is(err, ErrConflict) {
		t.Fatal("present raw key overwritten", refused, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
	if got := ipPolicyQueryInt(t, w, `SELECT count(*) FROM artwork_discovery_candidates`); got != 3 {
		t.Fatal("prefix page wrote candidates", got)
	}
}

func TestIPArtworkDiscoveryStaleClaimAndSourceFence(t *testing.T) {
	cases := []struct {
		name   string
		sql    []string
		mutate func(*ipArtworkDiscoveryOwner, *ipArtworkDiscoverySpec)
		want   error
	}{
		{"stale_job_revision", nil, func(o *ipArtworkDiscoveryOwner, _ *ipArtworkDiscoverySpec) { o.revision = 2 }, ErrConflict},
		{"stale_lease_owner", []string{`UPDATE artwork_subject_jobs SET job_lease_owner=8 WHERE id=901`}, nil, ErrConflict},
		{"stale_lease_generation", []string{`UPDATE artwork_subject_jobs SET job_lease_generation=2 WHERE id=901`}, nil, ErrConflict},
		{"expired_lease", []string{`UPDATE artwork_subject_jobs SET job_lease_expires_ms=1 WHERE id=901`}, nil, ErrConflict},
		{"wrong_phase", []string{`UPDATE artwork_subject_jobs SET phase='ready' WHERE id=901`}, nil, ErrConflict},
		{"stale_source_revision", []string{`UPDATE catalog_entities SET artwork_revision=2 WHERE id=778`}, nil, ErrConflict},
		{"retired_entity", []string{`UPDATE catalog_entities SET retired=1 WHERE id=778`}, nil, ErrConflict},
		{"retired_library", []string{`UPDATE catalog_libraries SET retired=1 WHERE id=777`}, nil, ErrConflict},
		{"stale_language", []string{`UPDATE metadata_runtime_settings SET artwork_language='textless'`}, nil, ErrConflict},
		{"policy_revision", nil, func(_ *ipArtworkDiscoveryOwner, s *ipArtworkDiscoverySpec) { s.policyRevision = 2 }, ErrConflict},
		{"provider_disabled", []string{`UPDATE metadata_providers SET enabled=0 WHERE code='tmdb'`}, nil, ErrConflict},
		{"screen_consent_revision", nil, func(_ *ipArtworkDiscoveryOwner, s *ipArtworkDiscoverySpec) { s.consentRevision = 2 }, ErrDisclosureDenied},
		{"screen_consent_withdrawn", []string{`UPDATE screen_metadata_consent SET confirmed=0`}, nil, ErrDisclosureDenied},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w, r, p := ipArtworkDiscoveryFixture(t)
			for _, query := range tc.sql {
				ipPolicyExec(t, w, query)
			}
			owner := ipArtworkDiscoveryOwnerFor()
			spec := ipArtworkDiscoverySpecFor(owner, ipArtworkDiscoveryRows(2))
			if tc.mutate != nil {
				tc.mutate(&owner, &spec)
			}
			claim := ipArtworkDiscoveryNewCommand(t, IPArtworkDiscoveryClaim, 1, 2, owner, spec, 0, 0, 0, nil)
			turn, tx, b := ipPolicyTurn(t, w, r, IPArtworkDiscoveryClaim)
			out, err := applyIPArtworkDiscovery(t.Context(), turn, p, t.Context(), b, IPArtworkDiscoveryClaim, claim)
			if out != (ipArtworkDiscoveryCommandResult{}) || !errors.Is(err, tc.want) {
				t.Fatal("stale claim admitted", out, err)
			}
			ipPolicyRollbackTurn(t, turn, tx)
			for query, want := range map[string]int64{
				`SELECT count(*) FROM artwork_discovery_stages`:                       0,
				`SELECT count(*) FROM artwork_command_receipts`:                       0,
				`SELECT last_command_sequence FROM artwork_subject_jobs WHERE id=901`: 0,
			} {
				if got := ipPolicyQueryInt(t, w, query); got != want {
					t.Fatal(query, got)
				}
			}
		})
	}
}

func TestIPArtworkDiscoveryPersonSource(t *testing.T) {
	w, r, p := ipArtworkDiscoveryFixture(t)
	rows := ipArtworkDiscoveryRows(1)
	owner := ipArtworkDiscoveryPersonOwnerFor()
	spec := ipArtworkDiscoverySpecFor(owner, rows)
	spec.provider = "tvdb"
	spec.manifest = ipArtworkDiscoveryManifestDigest(owner, spec, rows)

	claim := ipArtworkDiscoveryNewCommand(t, IPArtworkDiscoveryClaim, 1, 2, owner, spec, 0, 0, 0, nil)
	out, turn, tx, _ := ipArtworkDiscoveryApply(t, w, r, p, IPArtworkDiscoveryClaim, claim)
	if out.Outcome.State != Applied || out.Value.StageID <= 0 {
		t.Fatal(out)
	}
	stageID := out.Value.StageID
	ipPolicyCommitTurn(t, turn, tx)
	ipArtworkDiscoveryAck(t, w, 1)
	owner.revision = 2
	page := ipArtworkDiscoveryNewCommand(t, IPArtworkDiscoveryPage, 2, 3, owner, spec, stageID, 1, 0, rows)
	_, turn, tx, _ = ipArtworkDiscoveryApply(t, w, r, p, IPArtworkDiscoveryPage, page)
	ipPolicyCommitTurn(t, turn, tx)
	ipArtworkDiscoveryAck(t, w, 2)
	owner.revision = 3
	finish := ipArtworkDiscoveryNewCommand(t, IPArtworkDiscoveryFinish, 3, 4, owner, spec, stageID, 2, 1, nil)
	out, turn, tx, _ = ipArtworkDiscoveryApply(t, w, r, p, IPArtworkDiscoveryFinish, finish)
	if out.Outcome.State != Applied || out.Value.Count != 1 {
		t.Fatal(out)
	}
	ipPolicyCommitTurn(t, turn, tx)
	for query, want := range map[string]int64{
		`SELECT count(*) FROM artwork_discovery_stages WHERE id=? AND owner_id=902 AND state='ready'`: 1,
		`SELECT count(*) FROM artwork_discovery_stages WHERE id=? AND owner_id=901`:                   0,
	} {
		if got := ipPolicyQueryInt(t, w, query, stageID); got != want {
			t.Fatal(query, got)
		}
	}
	ipArtworkDiscoveryNoMaterial(t, w)
}

func TestIPArtworkDiscoveryCurrentAuthorityAndLifetime(t *testing.T) {
	setup := []struct {
		name  string
		kind  CommandID
		build func(t *testing.T, w *executor, r *ipPolicyRegistry, p *usOwnerAuthorityProof) *ipArtworkDiscoveryCommand
	}{
		{"claim", IPArtworkDiscoveryClaim, func(t *testing.T, w *executor, r *ipPolicyRegistry, p *usOwnerAuthorityProof) *ipArtworkDiscoveryCommand {
			owner := ipArtworkDiscoveryOwnerFor()
			spec := ipArtworkDiscoverySpecFor(owner, ipArtworkDiscoveryRows(2))
			return ipArtworkDiscoveryNewCommand(t, IPArtworkDiscoveryClaim, 1, 2, owner, spec, 0, 0, 0, nil)
		}},
		{"page", IPArtworkDiscoveryPage, func(t *testing.T, w *executor, r *ipPolicyRegistry, p *usOwnerAuthorityProof) *ipArtworkDiscoveryCommand {
			owner, spec, stageID := ipArtworkDiscoveryClaimed(t, w, r, p, 2)
			owner.revision = 2
			return ipArtworkDiscoveryNewCommand(t, IPArtworkDiscoveryPage, 2, 3, owner, spec, stageID, 1, 0, ipArtworkDiscoveryRows(2))
		}},
		{"finish", IPArtworkDiscoveryFinish, func(t *testing.T, w *executor, r *ipPolicyRegistry, p *usOwnerAuthorityProof) *ipArtworkDiscoveryCommand {
			owner, spec, stageID := ipArtworkDiscoveryClaimed(t, w, r, p, 2)
			owner.revision = 2
			page := ipArtworkDiscoveryNewCommand(t, IPArtworkDiscoveryPage, 2, 3, owner, spec, stageID, 1, 0, ipArtworkDiscoveryRows(2))
			_, turn, tx, _ := ipArtworkDiscoveryApply(t, w, r, p, IPArtworkDiscoveryPage, page)
			ipPolicyCommitTurn(t, turn, tx)
			ipArtworkDiscoveryAck(t, w, 2)
			owner.revision = 3
			return ipArtworkDiscoveryNewCommand(t, IPArtworkDiscoveryFinish, 3, 4, owner, spec, stageID, 2, 2, nil)
		}},
	}
	for _, mode := range []string{"revoked_family", "forged_proof", "revoked_membership", "cancelled_lifetime"} {
		for _, tc := range setup {
			t.Run(mode+"/"+tc.name, func(t *testing.T) {
				w, r, p := ipArtworkDiscoveryFixture(t)
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
				out, err := applyIPArtworkDiscovery(t.Context(), turn, p, lifetime, b, tc.kind, cmd)
				if out != (ipArtworkDiscoveryCommandResult{}) || err == nil {
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

func TestIPArtworkDiscoveryUnknownCommitFreshRead(t *testing.T) {
	for _, kind := range []CommandID{IPArtworkDiscoveryClaim, IPArtworkDiscoveryPage, IPArtworkDiscoveryFinish} {
		t.Run(ipArtworkDiscoveryKindName(kind), func(t *testing.T) {
			w, r, p := ipArtworkDiscoveryFixture(t)
			rows := ipArtworkDiscoveryRows(2)
			owner := ipArtworkDiscoveryOwnerFor()
			spec := ipArtworkDiscoverySpecFor(owner, rows)
			var cmd *ipArtworkDiscoveryCommand
			switch kind {
			case IPArtworkDiscoveryClaim:
				cmd = ipArtworkDiscoveryNewCommand(t, kind, 1, 2, owner, spec, 0, 0, 0, nil)
			case IPArtworkDiscoveryPage:
				owner, spec, stage := ipArtworkDiscoveryClaimed(t, w, r, p, 2)
				owner.revision = 2
				cmd = ipArtworkDiscoveryNewCommand(t, kind, 2, 3, owner, spec, stage, 1, 0, rows)
			case IPArtworkDiscoveryFinish:
				owner, spec, stage := ipArtworkDiscoveryClaimed(t, w, r, p, 2)
				owner.revision = 2
				page := ipArtworkDiscoveryNewCommand(t, IPArtworkDiscoveryPage, 2, 3, owner, spec, stage, 1, 0, rows)
				_, turn, tx, _ := ipArtworkDiscoveryApply(t, w, r, p, IPArtworkDiscoveryPage, page)
				ipPolicyCommitTurn(t, turn, tx)
				ipArtworkDiscoveryAck(t, w, 2)
				owner.revision = 3
				cmd = ipArtworkDiscoveryNewCommand(t, kind, 3, 4, owner, spec, stage, 2, 2, nil)
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
			out, err := applyIPArtworkDiscovery(t.Context(), turn, p, t.Context(), b, kind, cmd)
			if err != nil || out.Outcome.State != Applied {
				t.Fatal(out, err)
			}
			result, err := tx.commit()
			if result.state != commitUnknown || !errors.Is(err, ErrResolveRequired) {
				t.Fatal(result, err)
			}
			if again, e := applyIPArtworkDiscovery(t.Context(), turn, p, t.Context(), b, kind, cmd); again != (ipArtworkDiscoveryCommandResult{}) || e == nil {
				t.Fatal("uncertain frame served the saved result", again, e)
			}
			// Durable truth is read through a FRESH read-only connection, never
			// the quarantined writer pool.
			for query, want := range map[string]int64{
				`SELECT count(*) FROM artwork_command_receipts`:                                  int64(kind - 219),
				`SELECT count(*) FROM artwork_discovery_stages`:                                  1,
				`SELECT job_revision FROM artwork_subject_jobs WHERE id=901`:                     int64(kind - 218),
				`SELECT last_command_sequence FROM artwork_subject_jobs WHERE id=901`:            int64(kind - 219),
				`SELECT CASE state WHEN 'ready' THEN 1 ELSE 0 END FROM artwork_discovery_stages`: ipArtworkDiscoveryReady(kind),
				`SELECT staged_count FROM artwork_discovery_stages`:                              ipArtworkDiscoveryStaged(kind),
			} {
				if got := ipPolicyFreshInt(t, path, query); got != want {
					t.Fatal(query, got, want)
				}
			}
		})
	}
}

func ipArtworkDiscoveryReady(kind CommandID) int64 {
	if kind == IPArtworkDiscoveryFinish {
		return 1
	}
	return 0
}

func ipArtworkDiscoveryStaged(kind CommandID) int64 {
	if kind == IPArtworkDiscoveryClaim {
		return 0
	}
	return 2
}

func TestIPArtworkDiscoveryMissingOwnerAndIdentity(t *testing.T) {
	w, r, p := ipArtworkDiscoveryFixture(t)
	rows := ipArtworkDiscoveryRows(2)
	owner := ipArtworkDiscoveryOwnerFor()
	spec := ipArtworkDiscoverySpecFor(owner, rows)
	missing := owner
	missing.id = 999
	cmd := ipArtworkDiscoveryNewCommand(t, IPArtworkDiscoveryClaim, 1, 2, missing, spec, 0, 0, 0, nil)
	turn, tx, b := ipPolicyTurn(t, w, r, IPArtworkDiscoveryClaim)
	if out, err := applyIPArtworkDiscovery(t.Context(), turn, p, t.Context(), b, IPArtworkDiscoveryClaim, cmd); out != (ipArtworkDiscoveryCommandResult{}) || !errors.Is(err, ErrResolveRequired) {
		t.Fatal("missing owner admitted", out, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)

	// A retained foreign incarnation cannot resolve this owner.
	foreign := owner
	foreign.incarnation = [16]byte{9}
	cmd = ipArtworkDiscoveryNewCommand(t, IPArtworkDiscoveryClaim, 1, 2, foreign, spec, 0, 0, 0, nil)
	turn, tx, b = ipPolicyTurn(t, w, r, IPArtworkDiscoveryClaim)
	if out, err := applyIPArtworkDiscovery(t.Context(), turn, p, t.Context(), b, IPArtworkDiscoveryClaim, cmd); out != (ipArtworkDiscoveryCommandResult{}) || !errors.Is(err, ErrResolveRequired) {
		t.Fatal("foreign incarnation admitted", out, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)

	// The same retained sequence with a different original digest is a conflict,
	// never a second effect under the first result.
	first := ipArtworkDiscoveryNewCommand(t, IPArtworkDiscoveryClaim, 1, 2, owner, spec, 0, 0, 0, nil)
	_, turn, tx, _ = ipArtworkDiscoveryApply(t, w, r, p, IPArtworkDiscoveryClaim, first)
	ipPolicyCommitTurn(t, turn, tx)
	conflicting := ipArtworkDiscoveryNewCommand(t, IPArtworkDiscoveryClaim, 1, 2, owner, spec, 0, 0, 0, nil)
	conflicting.request.spec.generation = 2
	conflicting.digest = first.digest // retained identity, tampered payload
	turn, tx, b = ipPolicyTurn(t, w, r, IPArtworkDiscoveryClaim)
	if out, err := applyIPArtworkDiscovery(t.Context(), turn, p, t.Context(), b, IPArtworkDiscoveryClaim, conflicting); out != (ipArtworkDiscoveryCommandResult{}) || !errors.Is(err, ErrConflict) {
		t.Fatal("tampered request admitted", out, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)

	// A sequence gap is a conflict, not an implicit allocation.
	gap := ipArtworkDiscoveryNewCommand(t, IPArtworkDiscoveryClaim, 3, 4, owner, spec, 0, 0, 0, nil)
	turn, tx, b = ipPolicyTurn(t, w, r, IPArtworkDiscoveryClaim)
	if out, err := applyIPArtworkDiscovery(t.Context(), turn, p, t.Context(), b, IPArtworkDiscoveryClaim, gap); out != (ipArtworkDiscoveryCommandResult{}) || !errors.Is(err, ErrConflict) {
		t.Fatal("sequence gap admitted", out, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
	if got := ipPolicyQueryInt(t, w, `SELECT count(*) FROM artwork_command_receipts`); got != 1 {
		t.Fatal("rejected claim wrote a receipt", got)
	}
}

func TestIPArtworkDiscoveryBoundsAndDescriptors(t *testing.T) {
	owner := ipArtworkDiscoveryOwnerFor()
	rows := ipArtworkDiscoveryRows(2)
	spec := ipArtworkDiscoverySpecFor(owner, rows)
	identity := CommandIdentity{Incarnation: owner.incarnation, Sequence: 1, OperationID: [16]byte{2}}
	for name, mutate := range map[string]func(*ipArtworkDiscoveryRequest){
		"musicbrainz_provider": func(q *ipArtworkDiscoveryRequest) { q.spec.provider = "musicbrainz" },
		"unknown_provider":     func(q *ipArtworkDiscoveryRequest) { q.spec.provider = "plex" },
		"zero_policy":          func(q *ipArtworkDiscoveryRequest) { q.spec.policyRevision = 0 },
		"zero_consent":         func(q *ipArtworkDiscoveryRequest) { q.spec.consentRevision = 0 },
		"zero_generation":      func(q *ipArtworkDiscoveryRequest) { q.spec.generation = 0 },
		"too_many_candidates":  func(q *ipArtworkDiscoveryRequest) { q.spec.expectedCount = ipArtworkDiscoveryMaxCandidates + 1 },
		"zero_bytes":           func(q *ipArtworkDiscoveryRequest) { q.spec.expectedBytes = 0 },
		"negative_bytes":       func(q *ipArtworkDiscoveryRequest) { q.spec.expectedBytes = -1 },
		"both_namespaces":      func(q *ipArtworkDiscoveryRequest) { q.owner.personID = 778 },
		"no_namespace":         func(q *ipArtworkDiscoveryRequest) { q.owner.entityID = 0 },
		"zero_lease_owner":     func(q *ipArtworkDiscoveryRequest) { q.owner.leaseOwner = 0 },
		"zero_lease_until":     func(q *ipArtworkDiscoveryRequest) { q.owner.leaseUntilMS = 0 },
		"zero_source":          func(q *ipArtworkDiscoveryRequest) { q.owner.sourceIncarnation = [16]byte{} },
		"zero_owner_revision":  func(q *ipArtworkDiscoveryRequest) { q.owner.revision = 0 },
		"long_language":        func(q *ipArtworkDiscoveryRequest) { q.spec.language = strings.Repeat("a", 65) },
		"claim_with_stage":     func(q *ipArtworkDiscoveryRequest) { q.stageID = 1 },
		"claim_with_page":      func(q *ipArtworkDiscoveryRequest) { q.candidates = rows[:1] },
		"foreign_incarnation":  func(q *ipArtworkDiscoveryRequest) { q.owner.incarnation = [16]byte{9} },
	} {
		t.Run(name, func(t *testing.T) {
			q := ipArtworkDiscoveryRequest{owner: owner, spec: spec}
			mutate(&q)
			if c, err := newIPArtworkDiscoveryCommand(IPArtworkDiscoveryClaim, identity, q); c != nil || !errors.Is(err, ErrInvalid) {
				t.Fatal("malformed claim admitted", c, err)
			}
		})
	}
	for name, mutate := range map[string]func(*ipArtworkDiscoveryRequest){
		"empty_page":    func(q *ipArtworkDiscoveryRequest) { q.candidates = nil },
		"page_over_16":  func(q *ipArtworkDiscoveryRequest) { q.candidates = ipArtworkDiscoveryRows(ipArtworkDiscoveryPage + 1) },
		"page_past_end": func(q *ipArtworkDiscoveryRequest) { q.afterOrdinal = 2 },
		"finish_rows":   func(q *ipArtworkDiscoveryRequest) { q.candidates = rows[:1] },
		"finish_prefix": func(q *ipArtworkDiscoveryRequest) { q.afterOrdinal = 1 },
	} {
		t.Run(name, func(t *testing.T) {
			kind := IPArtworkDiscoveryPage
			q := ipArtworkDiscoveryRequest{owner: owner, spec: spec, stageID: 1, stageRevision: 1, afterOrdinal: 0, candidates: rows}
			if name == "finish_rows" || name == "finish_prefix" {
				kind = IPArtworkDiscoveryFinish
				q.afterOrdinal = 2
				q.candidates = nil
			}
			mutate(&q)
			if c, err := newIPArtworkDiscoveryCommand(kind, identity, q); c != nil || !errors.Is(err, ErrInvalid) {
				t.Fatal("malformed page/finish admitted", c, err)
			}
		})
	}
	// The bounded maximum page is admitted; one candidate past it is refused.
	big := ipArtworkDiscoveryRows(ipArtworkDiscoveryPage)
	bigSpec := ipArtworkDiscoverySpecFor(owner, big)
	if _, err := newIPArtworkDiscoveryCommand(IPArtworkDiscoveryPage, identity, ipArtworkDiscoveryRequest{
		owner: owner, spec: bigSpec, stageID: 1, stageRevision: 1, afterOrdinal: 0, candidates: big}); err != nil {
		t.Fatal("bounded maximum page refused", err)
	}
	for _, expected := range []Descriptor{
		{IPArtworkDiscoveryClaim, "ip_artwork_discovery_claim", Normal},
		{IPArtworkDiscoveryPage, "ip_artwork_discovery_page", Normal},
		{IPArtworkDiscoveryFinish, "ip_artwork_discovery_finish", Normal},
	} {
		actual, err := Describe(expected.ID)
		if err != nil || actual != expected || actual.ID < 220 || actual.ID > 222 {
			t.Fatal(actual, err)
		}
	}
}
