package writer

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

// Person and entity evidence are separate purposes with separate owner,
// receipt and proof tables. These cells prove that equal numeric subject IDs,
// owner IDs, incarnations and sequences never let one family's receipt or
// command satisfy the other.
func seedIPPersonCrossFixture(t *testing.T) (*executor, *ipPolicyRegistry, *usOwnerAuthorityProof, time.Time) {
	t.Helper()
	w, keeper, p := ipPolicySecurityFixture(t)
	base := w.now()
	// The person draft carries the person custody/build subset; the entity
	// evidence draft carries only the shared group-D IP receipt header, so the
	// cross-kind mirror can live beside the person receipt.
	for _, name := range []string{"testdata/ip-person-draft.sql", "testdata/ip-entity-evidence-draft.sql"} {
		ddl, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(name, err)
		}
		if _, err = w.db.ExecContext(t.Context(), string(ddl)); err != nil {
			t.Fatal(name, err)
		}
	}
	for _, query := range []string{
		`INSERT INTO catalog_libraries(id,library_id,root) VALUES(777,'ip-library','/media')`,
		`INSERT INTO metadata_providers(code,adapter,enabled) VALUES('tmdb','tmdb',1),('tvdb','tvdb',1),('musicbrainz','musicbrainz',1)`,
		`INSERT INTO metadata_provider_policy(library_id,provider,incarnation,revision,enabled,priority,region,algorithm,refresh_mode) VALUES(777,'tmdb',X'09000000000000000000000000000000',1,1,5,'US','v1','fill_missing'),(777,'tvdb',X'09000000000000000000000000000000',1,1,5,'US','v1','fill_missing'),(777,'musicbrainz',X'0a000000000000000000000000000000',1,1,5,'US','v1','fill_missing')`,
		`INSERT INTO screen_metadata_consent(singleton,revision,confirmed) VALUES(1,1,1) ON CONFLICT(singleton) DO UPDATE SET revision=1,confirmed=1`,
		`INSERT INTO metadata_runtime_settings(singleton,online_lookups,lookup_epoch) VALUES(1,1,3) ON CONFLICT(singleton) DO UPDATE SET online_lookups=1,lookup_epoch=3`,
		`INSERT INTO catalog_people(id,token,identity_key,name,sort_name,artwork_incarnation) VALUES(42,'person-token-42','ik-42','Ada Lovelace','Lovelace, Ada',X'01000000000000000000000000000000')`,
		`INSERT INTO metadata_provider_subjects(id,provider,subject_kind,provider_id,language,region,request_shape,incarnation,revision) VALUES(501,'tmdb','person','42','en-US','US','tmdb.person.detail.header-exact-dates.v1',X'0b000000000000000000000000000000',1),(502,'musicbrainz','person','42','en-US','US','tmdb.person.detail.header-exact-dates.v1',X'0c000000000000000000000000000000',1),(503,'tmdb','person','42','en-US','FR','tmdb.person.detail.header-exact-dates.v1',X'0d000000000000000000000000000000',1)`,
	} {
		ipPolicyExec(t, w, query)
	}
	defs := append(ipPolicyDefinitions(), ipPersonDefinitions()...)
	defs = append(defs, ipPersonEvidenceAppendDefinitions()...)
	defs = append(defs, ipPersonEvidenceSealDefinitions()...)
	defs = append(defs, ipEntityJobDefinitions()...)
	registry, err := stmttest.New(t, defs)
	if err != nil {
		t.Fatal(err)
	}
	return w, &ipPolicyRegistry{registry, keeper}, p, base
}

// seedIPPersonCrossEntityOwner creates an entity acquisition owner whose ID,
// incarnation and floor exactly mirror the person operation, with numeric
// entity ID 42 equal to the person ID 42.
func seedIPPersonCrossEntityOwner(t *testing.T, w *executor, owner string, incarnation [16]byte, sequence int64) {
	t.Helper()
	ipPolicyExec(t, w, `INSERT INTO catalog_kinds(id,name,playable,container,searchable,browsable,listening) VALUES(99,'ip-cross-person',1,0,1,1,0)`)
	ipPolicyExec(t, w, `INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(42,X'02020202020202020202020202020202',777,99)`)
	ipPolicyExec(t, w, `INSERT INTO metadata_provider_operations(id,entity_id,provider,phase,status,generation,lease,lease_until,entity_incarnation,source_revision,identity_revision,metadata_revision,hierarchy_revision,selection_revision,descriptive_revision,policy_incarnation,policy_revision,consent_revision,algorithm,refresh_mode,query_title,query_artist,query_year,query_digest,input_digest,selected_id,manual,review_search,qualification,created_at,source_content_revision,command_incarnation,last_command_sequence,acknowledged_command_sequence) VALUES(?,42,'tmdb','ip-acquisition','claimed',1,'lease','2030-01-01T00:00:00Z',?,1,1,1,0,0,0,X'09000000000000000000000000000000',1,1,'v1','fill_missing','','',0,'','','42',0,0,'ip-acquisition-v1','2026-01-01T00:00:00Z',1,?,?,?)`,
		owner, incarnation[:], incarnation[:], sequence-1, sequence-1)
}

// seedIPPersonCrossEntityReceipt mirrors one person receipt as an entity append
// receipt with the same owner, incarnation, sequence, operation and digests.
func seedIPPersonCrossEntityReceipt(t *testing.T, w *executor, owner string, incarnation [16]byte,
	sequence int64, operation [16]byte, request RequestDigest) {
	t.Helper()
	ipPolicyExec(t, w, `INSERT INTO metadata_ip_receipts(owner_id,command_incarnation,command_sequence,operation_id,request_digest,command_kind,accepted_revision,applied_ms,result_digest,result_phase,after_family,after_ordinal,after_key,result_count,result_bytes,terminal) VALUES(?,?,?,?,?,'append',2,1,?,'building',0,1,'',1,1,0)`,
		owner, incarnation[:], sequence, operation[:], request[:], make([]byte, 32))
}

func TestIPPersonCrossKindPurposeDescriptorsAndTurns(t *testing.T) {
	w, r, p, _ := seedIPPersonCrossFixture(t)
	person := ipPersonResult{Kind: "claim", OperationID: strings.Repeat("a", 32), PersonID: 42,
		Provider: "tmdb", ProviderID: "42", Lease: strings.Repeat("b", 32), Generation: 1,
		Revision: 2, LeaseGeneration: 1, LeaseUntilMS: 300000, BuildGeneration: 1,
		EvidenceID: "evidence-person-1", BuildID: "build-person-1"}
	appendReq := ipPersonAppendFor(person, ipPersonFamilyAlias, 0, 1, 1, 1)
	appendReq.aliases = ipPersonAliasPageRows(0, 1)
	sealReq := ipPersonSealFor(person, ipPersonBeginRequestFor(t, person, 1, 1), 1)
	cells := []struct {
		name    string
		entity  CommandID
		command *ipPersonCommand
	}{
		{"claim", IPEntityClaim, ipPersonClaimCommand(t, ipPersonIdentity(1, 0x31), nil)},
		{"renew", IPEntityRenew, mustIPPersonRenew(t, ipPersonIdentity(2, 0x41), ipPersonRenewFor(person))},
		{"finish", IPEntityFinish, mustIPPersonFinish(t, ipPersonIdentity(2, 0x51), ipPersonFinishFor(person, "done"))},
		{"begin", IPEntityEvidenceBegin, mustIPPersonBegin(t, ipPersonIdentity(2, 0x61), ipPersonBeginRequestFor(t, person, 1, 1))},
		{"append", IPEntityEvidenceAppend, mustIPPersonAppend(t, ipPersonIdentity(3, 0x73), appendReq)},
	}
	for _, cell := range cells {
		t.Run(cell.name, func(t *testing.T) {
			turn, tx, b := ipPolicyTurn(t, w, r, cell.entity)
			var out ipPersonCommandResult
			var err error
			switch cell.name {
			case "claim":
				out, err = applyIPPersonClaim(t.Context(), turn, p, t.Context(), b, cell.command)
			case "renew":
				out, err = applyIPPersonRenew(t.Context(), turn, p, t.Context(), b, cell.command)
			case "finish":
				out, err = applyIPPersonFinish(t.Context(), turn, p, t.Context(), b, cell.command)
			case "begin":
				out, err = applyIPPersonEvidenceBegin(t.Context(), turn, p, t.Context(), b, cell.command)
			default:
				out, err = applyIPPersonEvidenceAppend(t.Context(), turn, p, t.Context(), b, cell.command)
			}
			if out != (ipPersonCommandResult{}) || !errors.Is(err, ErrInvalid) {
				t.Fatal("person command ran under an entity purpose", out, err)
			}
			ipPolicyRollbackTurn(t, turn, tx)
		})
	}
	t.Run("seal", func(t *testing.T) {
		turn, tx, b := ipPolicyTurn(t, w, r, IPEntitySeal)
		out, err := applyIPPersonSeal(t.Context(), turn, p, t.Context(), b,
			mustIPPersonSeal(t, ipPersonIdentity(3, 0x74), sealReq))
		if out != (ipPersonSealCommandResult{}) || !errors.Is(err, ErrInvalid) {
			t.Fatal("person seal ran under an entity purpose", out, err)
		}
		ipPolicyRollbackTurn(t, turn, tx)
	})
	// The reverse direction: entity commands refuse a person purpose even with
	// the same witnesses.
	entityClaim, err := newIPEntityClaimCommand(ipPersonIdentity(1, 0x31), ipEntityJobClaimRequest{
		entityID: 42, libraryID: 777, libraryKey: "ip-library", selectedID: "42",
		provider: "tmdb", purpose: "metadata", role: "primary", jobGeneration: 1, jobRevision: 1,
		entityRevision: 1, sourceRevision: 1, identityRevision: 1, metadataRevision: 1,
		entityIncarnation: [16]byte{3}, policyIncarnation: [16]byte{9}, policyRevision: 1,
		consentRevision: 1})
	if err != nil {
		t.Fatal(err)
	}
	turn, tx, b := ipPolicyTurn(t, w, r, IPPersonClaim)
	out, err := applyIPEntityClaim(t.Context(), turn, p, t.Context(), b, entityClaim)
	if out != (ipEntityJobCommandResult{}) || !errors.Is(err, ErrInvalid) {
		t.Fatal("entity claim ran under a person purpose", out, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
	entityAppend, err := newIPEntityEvidenceAppendCommand(ipPersonIdentity(3, 0x73),
		ipEntityEvidenceAppendRequest{operationID: strings.Repeat("a", 32), provider: "tmdb",
			purpose: "metadata", selectedID: "42", lease: strings.Repeat("b", 32), entityID: 42,
			generation: 1, leaseUntilMS: 300000, evidenceID: "evidence-one", subjectID: 42,
			subjectKind: "series", providerID: "42", parserVersion: "ip.tmdb.series.v1",
			observedAt: "2026-01-01T00:00:00Z", buildID: "build-one", buildGeneration: 1,
			buildRevision: 1, family: ipPersonFamilyAlias, familyIndex: 0, familyStart: 1,
			familyRows: 1, afterOrdinal: 0, materializedBefore: 1,
			aliases: []ipEvidenceAlias{{Ordinal: 0, RequestedID: "requested", CanonicalID: "42", Name: "Alias"}}})
	if err != nil {
		t.Fatal(err)
	}
	turn, tx, b = ipPolicyTurn(t, w, r, IPPersonEvidenceAppend)
	out2, err := applyIPEntityEvidenceAppend(t.Context(), turn, p, t.Context(), b, entityAppend)
	if out2 != (ipEntityEvidenceCommandResult{}) || !errors.Is(err, ErrInvalid) {
		t.Fatal("entity append ran under a person purpose", out2, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
	// The numeric purposes stay separate, NORMAL and unused by the other family.
	for _, cell := range []struct {
		id   CommandID
		name string
	}{{IPPersonEvidenceAppend, "ip_person_evidence_append"}, {IPPersonSeal, "ip_person_seal"}} {
		got, err := Describe(cell.id)
		if err != nil || got.Name != cell.name || got.Durability != Normal {
			t.Fatal(cell, got, err)
		}
	}
	if IPPersonEvidenceAppend == IPEntityEvidenceAppend || IPPersonSeal == IPEntitySeal ||
		IPPersonClaim == IPEntityClaim || IPPersonRenew == IPEntityRenew ||
		IPPersonFinish == IPEntityFinish || IPPersonEvidenceBegin == IPEntityEvidenceBegin {
		t.Fatal("person and entity purposes share numeric IDs")
	}
}

func TestIPPersonCrossKindReceiptMapping(t *testing.T) {
	for _, cell := range []struct {
		kind     string
		evidence string
		want     CommandID
	}{
		{"begin", "", IPPersonClaim},
		{"begin", "evidence-one", IPPersonEvidenceBegin},
		{"renew", "", IPPersonRenew},
		{"retry", "", IPPersonFinish},
		{"append", "evidence-one", IPPersonEvidenceAppend},
		{"evidence_seal", "evidence-one", IPPersonSeal},
	} {
		got, ok := ipPersonReceiptKind(cell.kind, cell.evidence)
		if !ok || got != cell.want {
			t.Fatal(cell, got, ok)
		}
	}
	// Entity-only spellings never decode in the person path, and a person child
	// receipt without its evidence ID is malformed, not a claim.
	for _, kind := range []string{"claim", "finish", "evidence_append", "seal", "set_begin", "set_page", "set_seal"} {
		if got, ok := ipPersonReceiptKind(kind, "evidence-one"); ok {
			t.Fatal("entity receipt kind decoded as person", kind, got)
		}
	}
	for _, kind := range []string{"append", "evidence_seal"} {
		if got, ok := ipPersonReceiptKind(kind, ""); ok {
			t.Fatal("person child receipt without evidence decoded", kind, got)
		}
	}
}

func TestIPPersonCrossKindClassificationRejectsEqualWitnesses(t *testing.T) {
	identity := CommandIdentity{Incarnation: [16]byte{7}, Sequence: 3, OperationID: [16]byte{9}}
	digest := RequestDigest{1}
	owner := receiptOwnerFacts{exists: true, incarnation: identity.Incarnation,
		lastSequence: 3, acknowledgedSequence: 2}
	for _, cell := range []struct {
		name    string
		purpose CommandID
		foreign CommandID
	}{
		{"claim", IPPersonClaim, IPEntityClaim},
		{"renew", IPPersonRenew, IPEntityRenew},
		{"finish", IPPersonFinish, IPEntityFinish},
		{"begin", IPPersonEvidenceBegin, IPEntityEvidenceBegin},
		{"append", IPPersonEvidenceAppend, IPEntityEvidenceAppend},
		{"seal", IPPersonSeal, IPEntitySeal},
	} {
		t.Run(cell.name, func(t *testing.T) {
			found := receiptFacts{exists: true, identity: receiptIdentity{command: identity, digest: digest},
				kind: cell.foreign, resultCount: 1}
			decision, err := classifyReceipt(receiptIdentity{command: identity, digest: digest},
				cell.purpose, owner, found)
			if decision != receiptConflict || !errors.Is(err, ErrConflict) {
				t.Fatal("foreign receipt kind classified", decision, err)
			}
		})
	}
}

func TestIPPersonCrossKindEntityReceiptDoesNotServePerson(t *testing.T) {
	w, r, p, _ := seedIPPersonCrossFixture(t)
	staged := ipPersonEvidenceStage(t, w, r, p, 1, 1)
	q := ipPersonAppendFor(staged.begin, ipPersonFamilyAlias, 0, 1, 1, 1)
	q.aliases = staged.beginReq.aliases
	identity := ipPersonIdentity(3, 0x73)
	command := mustIPPersonAppend(t, identity, q)
	seedIPPersonCrossEntityOwner(t, w, staged.begin.OperationID, identity.Incarnation, 3)
	seedIPPersonCrossEntityReceipt(t, w, staged.begin.OperationID, identity.Incarnation, 3,
		identity.OperationID, command.digest)
	// The mirror exists in metadata_ip_receipts only: the person path is fresh.
	out, turn, tx := ipPersonEvidenceApplyAppend(t, w, r, p, command)
	if out.Outcome.State != Applied || out.Outcome.Replayed {
		t.Fatal("person append served an entity receipt", out)
	}
	ipPolicyCommitTurn(t, turn, tx)
	if got := ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_person_receipts WHERE command_kind='append'`); got != 1 {
		t.Fatal("person append did not save its own receipt", got)
	}
	if got := ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_ip_receipts WHERE command_kind='append'`); got != 1 {
		t.Fatal("entity mirror changed", got)
	}
	// With the person receipt deliberately lost, the surviving entity mirror
	// must not be decoded as the missing person proof.
	ipPolicyExec(t, w, `DELETE FROM metadata_person_receipts WHERE command_kind='append'`)
	turn, tx, b := ipPolicyTurn(t, w, r, IPPersonEvidenceAppend)
	again, err := applyIPPersonEvidenceAppend(t.Context(), turn, p, t.Context(), b, command)
	if again != (ipPersonCommandResult{}) || err == nil {
		t.Fatal("lost person receipt resolved from the entity receipt", again, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
}

func TestIPPersonCrossKindPersonReceiptDoesNotServeEntity(t *testing.T) {
	w, r, p, _ := seedIPPersonCrossFixture(t)
	staged := ipPersonEvidenceStage(t, w, r, p, 1, 1)
	q := ipPersonAppendFor(staged.begin, ipPersonFamilyAlias, 0, 1, 1, 1)
	q.aliases = staged.beginReq.aliases
	identity := ipPersonIdentity(3, 0x73)
	command := mustIPPersonAppend(t, identity, q)
	out, turn, tx := ipPersonEvidenceApplyAppend(t, w, r, p, command)
	if out.Outcome.State != Applied {
		t.Fatal(out)
	}
	ipPolicyCommitTurn(t, turn, tx)
	seedIPPersonCrossEntityOwner(t, w, staged.begin.OperationID, identity.Incarnation, 3)
	turn, tx, _ = ipPolicyTurn(t, w, r, IPEntityEvidenceAppend)
	lookup, err := ipEntityEvidenceLookupReceipt(t.Context(), turn, IPEntityEvidenceAppend,
		ReceiptReference{Identity: identity, RequestDigest: command.digest}, staged.begin.OperationID,
		"tmdb", 42, func(ipEntityJobReceipt) (ipEntityEvidenceResult, error) {
			t.Fatal("entity lookup decoded a person receipt")
			return ipEntityEvidenceResult{}, ErrResolveRequired
		})
	if err != nil {
		t.Fatal(err)
	}
	if lookup.hasReceipt || lookup.decision != receiptFresh {
		t.Fatal("entity lookup saw the person receipt", lookup.decision, lookup.hasReceipt)
	}
	ipPolicyRollbackTurn(t, turn, tx)
}
