package writer

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"strings"
	"testing"
	"time"
)

func ipPersonFixture(t *testing.T) (*executor, ipPersonRequest) {
	t.Helper()
	w := ipPolicyFixture(t)
	for _, s := range []string{
		`INSERT INTO metadata_runtime_settings(singleton) VALUES(1)`,
		`INSERT INTO libraries(id,name,kind,root) VALUES('ip-test','IP','movie','/fixture')`,
		`INSERT INTO catalog_people(id,token,identity_key,name,sort_name,revision,artwork_incarnation) VALUES(101,'person-token','person-native','Before','Before',17,X'ffffffffffffffffffffffffffffffff')`,
		`INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(101,zeroblob(16),7,1)`,
		`INSERT INTO metadata_provider_subjects(id,provider,subject_kind,provider_id,language,region,request_shape,incarnation,revision) VALUES(44,'tmdb','person','203','en','US','person-header-alias-id-v1',X'01020300000000000000000000000000',6)`,
		`UPDATE metadata_provider_policy SET refresh_mode='replace_unlocked'`,
	} {
		ipPolicyTestExec(t, w, s)
	}
	r := ipPersonRequest{identity: ipPolicyTestIdentity(1), owner: ipPersonOwner{id: "person-op", personID: 101, token: "person-token", personRevision: 17, libraryID: 7, libraryLanguage: "en-US", libraryMetadataRevision: 1, namespace: ipPersonNamespace{44, "tmdb", "203", "en", "US", ipPersonShape, ipPolicyTestInc, 6}, policyIncarnation: ipPolicyTestInc, policyRevision: 5, consentRevision: 3, generation: 2, revision: 1, phase: "claimed", leaseOwner: "offline-worker", leaseGeneration: 3, leaseUntilMS: 200000, source: ipPersonSource{role: "primary", incarnation: [16]byte{9}}}, nowMS: 100000}
	r.evidence = ipPersonEvidence{id: "person-evidence", buildID: "person-build", header: ipPersonHeader{"Person é", "Biography", "1980-02-29", "", "Halifax", "2026-11-07T12:00:00Z", "https://fixture.invalid/person/203"}, aliases: []ipPersonAlias{{"old203", "203", "Alias", "2026-11-07"}}, externalIDs: []ipPersonExternalID{{"wikidata", "person", "Q203"}}}
	ipPersonCommitment(t, &r)
	now := time.Now().Add(time.Hour)
	r.nowMS = now.UnixMilli()
	r.owner.leaseUntilMS = r.nowMS + 100000
	w.now = func() time.Time { return now }
	// Native owner fixture; owner birth is tested separately through its typed body.
	o, n := r.owner, r.owner.namespace
	ipPolicyTestExec(t, w, `INSERT INTO metadata_person_operations(id,person_id,provider,library_id,subject_id,subject_kind,provider_id,language,region,request_shape,captured_person_token,captured_person_revision,subject_incarnation,subject_revision,policy_incarnation,policy_revision,consent_revision,generation,revision,phase,lease_owner,lease_generation,lease_until_ms,command_incarnation) VALUES(?,?,?,?,?,'person',?,?,?,?,?,?,?,?,?,?,?,?,?,'claimed',?,?,?,?)`, o.id, o.personID, n.provider, o.libraryID, n.subjectID, n.providerID, n.language, n.region, n.shape, o.token, o.personRevision, n.incarnation[:], n.revision, o.policyIncarnation[:], o.policyRevision, o.consentRevision, o.generation, o.revision, o.leaseOwner, o.leaseGeneration, o.leaseUntilMS, r.identity.Incarnation[:])

	return w, r
}
func ipPersonCommitment(t *testing.T, r *ipPersonRequest) {
	t.Helper()
	d, n, e := ipPersonManifestDigest(r.owner.namespace, r.evidence)
	if e != nil {
		t.Fatal(e)
	}
	r.evidence.commitment = d
	r.evidence.bytes = n
}
func ipPersonTurn(t *testing.T, w *executor, k CommandID) *commandTurn {
	return ipPolicyTestTurn(t, w, k, ipPersonDefinitions())
}
func ipPersonBudget() *ipPersonDecode { return &ipPersonDecode{remaining: ipPersonMaxBytes} }
func ipPersonRollback(t *testing.T, turn *commandTurn) {
	t.Helper()
	if _, e := turn.transaction.rollback(nil); e != nil {
		t.Fatal(e)
	}
}

func TestIPPersonNativeStatements(t *testing.T) {
	w, r := ipPersonFixture(t)
	defs := ipPersonDefinitions()
	for _, d := range defs {
		if strings.Count(d.SQL, "?") != len(d.Params) {
			t.Fatalf("%s placeholders=%d params=%d", d.ID, strings.Count(d.SQL, "?"), len(d.Params))
		}
		if d.Kind == stmt.Read {
			rows, e := w.db.QueryContext(t.Context(), "EXPLAIN QUERY PLAN "+d.SQL, d.Cohorts[0].Bindings...)
			if e != nil {
				t.Fatal(d.ID, e)
			}
			for rows.Next() {
				var id, parent, unused int64
				var detail string
				if e = rows.Scan(&id, &parent, &unused, &detail); e != nil {
					t.Fatal(e)
				}
				if strings.Contains(detail, "SCAN ") && !strings.Contains(detail, "SCAN CONSTANT ROW") {
					t.Fatalf("%s unbounded plan: %s", d.ID, detail)
				}
				if strings.Contains(detail, "TEMP B-TREE") {
					t.Fatalf("%s temporary sort: %s", d.ID, detail)
				}
			}
			e = errors.Join(rows.Err(), rows.Close())
			if e != nil {
				t.Fatal(d.ID, e)
			}
		}
		s, e := w.db.PrepareContext(t.Context(), d.SQL)
		if e != nil {
			t.Fatalf("%s: %v", d.ID, e)
		}
		if e = s.Close(); e != nil {
			t.Fatal(e)
		}
	}
	turn := ipPersonTurn(t, w, IPEvidenceBegin)
	if e := ipPersonFresh(t.Context(), turn, r); e != nil {
		t.Fatal(e)
	}
	if _, e := ipPersonBegin(t.Context(), turn, r); e != nil {
		t.Fatal(e)
	}
	ipPersonTestVerify(t, turn, r, 1)
	ipPersonRollback(t, turn)
}
func ipPersonTestVerify(t *testing.T, turn *commandTurn, r ipPersonRequest, rows int64) {
	t.Helper()
	v, e := ipPersonReadEvidence(t.Context(), turn, r, 0, ipPersonBudget())
	if e != nil {
		t.Fatal(e)
	}
	if v.rows() != rows {
		t.Fatal(v.rows(), rows)
	}
	d, n, e := ipPersonManifestDigest(r.owner.namespace, v)
	want, size, _ := ipPersonManifestDigest(r.owner.namespace, ipPersonPrefix(r.evidence, rows))
	if e != nil || d != want || n != size {
		t.Fatal("native independent manifest", d, n, e)
	}
}

func TestIPPersonNativeStageSealAndRollback(t *testing.T) {
	w, r := ipPersonFixture(t)
	turn := ipPersonTurn(t, w, IPEvidenceBegin)
	if _, e := ipPersonBegin(t.Context(), turn, r); e != nil {
		t.Fatal(e)
	}
	r.buildRevision = 1
	r.after = 1
	r.through = 3
	if _, e := ipPersonAppend(t.Context(), turn, r, ipPersonBudget()); e != nil {
		t.Fatal(e)
	}
	r.buildRevision = 2
	if _, e := ipPersonSeal(t.Context(), turn, r, ipPersonBudget()); e != nil {
		t.Fatal(e)
	}
	// A real person seal needs its exact original saved receipt before linking.
	// The lower projection sets only sealed=1, never EN seal_proof_* columns.
	var wrong int64
	if e := ipPersonScan(t.Context(), turn, ipPPNoChildren, []any{"missing", "missing"}, &wrong); e != nil {
		t.Fatal(e)
	}
	if wrong != 1 {
		t.Fatal("no-next")
	}
	// Read full sealed native content, including exact birth date/place.
	v, e := ipPersonReadEvidence(t.Context(), turn, r, 1, ipPersonBudget())
	if e != nil || v.header.birthDate != "1980-02-29" || v.header.birthPlace != "Halifax" {
		t.Fatal(v, e)
	}
	ipPersonRollback(t, turn)
	if ipPolicyTestScalar(t, w, `SELECT count(*) FROM metadata_evidence`) != 0 || ipPolicyTestScalar(t, w, `SELECT count(*) FROM metadata_person_builds`) != 0 {
		t.Fatal("partial stage survived rollback")
	}
}
func TestIPPersonNativeIdentityLocaleAndRawTail(t *testing.T) {
	for _, name := range []string{"entity_collision", "locale", "provider", "source_role", "source_head", "library_locale", "library_retired", "person_revision", "policy", "consent", "extra_alias", "omitted_family", "birth_tamper", "byte_preflight"} {
		t.Run(name, func(t *testing.T) {
			w, r := ipPersonFixture(t)
			switch name {
			case "entity_collision":
				ipPolicyTestExec(t, w, `DELETE FROM metadata_person_operations`)
				ipPolicyTestExec(t, w, `DELETE FROM catalog_people WHERE id=101`)
			case "locale":
				r.owner.namespace.language = "fr"
			case "provider":
				r.owner.namespace.provider = "tvdb"
			case "source_role":
				r.owner.source.role = "additional"
			case "source_head":
				r.owner.source.revision = 1
			case "library_locale":
				ipPolicyTestExec(t, w, `UPDATE libraries SET metadata_language='fr-US',data_metadata_revision=2`)
			case "library_retired":
				ipPolicyTestExec(t, w, `UPDATE catalog_libraries SET retired=1`)
			case "person_revision":
				ipPolicyTestExec(t, w, `UPDATE catalog_people SET revision=18`)
			case "policy":
				ipPolicyTestExec(t, w, `UPDATE metadata_provider_policy SET enabled=0`)
			case "consent":
				ipPolicyTestExec(t, w, `UPDATE screen_metadata_consent SET confirmed=0`)
			}
			turn := ipPersonTurn(t, w, IPEvidenceBegin)
			if name != "extra_alias" && name != "omitted_family" && name != "birth_tamper" && name != "byte_preflight" {
				e := ipPersonFresh(t.Context(), turn, r)
				if e == nil {
					t.Fatal("stale identity/scope admitted")
				}
				_, _ = turn.transaction.rollback(e)
				return
			}
			if _, e := ipPersonBegin(t.Context(), turn, r); e != nil {
				t.Fatal(e)
			}
			if name == "extra_alias" {
				if e := ipPersonOne(t.Context(), turn, ipPPAliasInsert, r.evidence.id, int64(99), "bad", "203", "tail", "now"); e != nil {
					t.Fatal(e)
				}
			}
			if name == "omitted_family" || name == "birth_tamper" {
				// Fixture-only commit, then alter the disposable database while
				// no writer turn exists; production has no raw SQL escape.
				ipPolicyTestCommit(t, turn)
				if name == "omitted_family" {
					ipPolicyTestExec(t, w, `INSERT INTO metadata_evidence_terms VALUES('person-evidence','tag',0,'excluded')`)
				} else {
					ipPolicyTestExec(t, w, `UPDATE metadata_evidence SET birth_date='impossible'`)
				}
				turn = ipPersonTurn(t, w, IPEvidenceSeal)
			}
			budget := ipPersonBudget()
			if name == "byte_preflight" {
				budget.remaining = 0
			}
			_, e := ipPersonReadEvidence(t.Context(), turn, r, 0, budget)
			if e == nil {
				t.Fatal("unbounded/tail evidence admitted")
			}
			_, _ = turn.transaction.rollback(e)
		})
	}
}

func TestIPPersonCanonicalGateRollback(t *testing.T) {
	w, r := ipPersonFixture(t)
	turn := ipPersonTurn(t, w, IPEvidenceBegin)
	out, e := ipPersonProviderBody(t.Context(), turn, IPEvidenceBegin, r, ipPersonBudget())
	if e == nil { // Parent has composed the two closed registry cases.
		ipPolicyTestCommit(t, turn)
		ipPolicyTestExec(t, w, `UPDATE catalog_people SET revision=999`)
		ipPolicyTestExec(t, w, `UPDATE metadata_provider_policy SET enabled=0,revision=999`)
		ipPolicyTestExec(t, w, `UPDATE metadata_person_operations SET lease_until_ms=0,revision=999`)
		replay := ipPersonTurn(t, w, IPEvidenceBegin)
		got, e := ipPersonProviderBody(t.Context(), replay, IPEvidenceBegin, r, ipPersonBudget())
		if e != nil || got.result != out.result || got.change != (ipPersonSearchIntent{}) {
			t.Fatal(got, e)
		}
		ipPolicyTestCommit(t, replay)
		return
	}
	if out != (ipPersonOutcome{}) || !errors.Is(e, ErrInvalid) {
		t.Fatal(out, e)
	}
	_, _ = turn.transaction.rollback(e)
	if ipPolicyTestScalar(t, w, `SELECT count(*) FROM metadata_evidence`) != 0 || ipPolicyTestScalar(t, w, `SELECT last_command_sequence FROM metadata_person_operations`) != 0 {
		t.Fatal("missing codec gate leaked effects")
	}
}

// Independent manifest vector assembly uses only the written wire specification,
// not the implementation encoder, row-byte helper or the expected counters.
func TestIPPersonManifestIndependentVector(t *testing.T) {
	n := ipPersonNamespace{7, "offline", "p", "en", "CA", ipPersonShape, [16]byte{1}, 3}
	v := ipPersonEvidence{header: ipPersonHeader{"A", "", "2000-02-29", "", "H", "now", "fixture"}, aliases: []ipPersonAlias{{"old", "p", "Alias", "then"}}, externalIDs: []ipPersonExternalID{{"wikidata", "person", "Q1"}}}
	var wire []byte
	number := func(n uint64) { var x [8]byte; binary.BigEndian.PutUint64(x[:], n); wire = append(wire, x[:]...) }
	text := func(s string) { number(uint64(len(s))); wire = append(wire, []byte(s)...) }
	text("portico-ip-person-source-sha256-v1")
	text("portico-person-header-alias-id-v1")
	number(7)
	for _, s := range []string{"offline", "person", "p", "en", "CA", "person-header-alias-id-v1"} {
		text(s)
	}
	wire = append(wire, n.incarnation[:]...)
	number(3)
	number(0)
	for _, s := range []string{"A", "", "2000-02-29", "", "H", "now", "fixture"} {
		text(s)
	}
	number(1)
	number(1)
	number(0)
	for _, s := range []string{"old", "p", "Alias", "then"} {
		text(s)
	}
	number(1)
	number(2)
	for _, s := range []string{"wikidata", "person", "Q1"} {
		text(s)
	}
	want := sha256.Sum256(wire)
	got, size, e := ipPersonManifestDigest(n, v)
	if e != nil || got != want || size != int64(len(wire)) || hex.EncodeToString(want[:]) != "5455569b6240af1bc5eb8713fa767f59d3172d9a7adec84fcd204b0535f4d43e" {
		t.Fatal(hex.EncodeToString(got[:]), hex.EncodeToString(want[:]), size, len(wire), e)
	}
	t.Logf("independent person vector sha256=%x bytes=%d", want, len(wire))
	changed := n
	changed.language = "fr"
	other, _, _ := ipPersonManifestDigest(changed, v)
	if other == got {
		t.Fatal("locale omitted")
	}
	r := ipPersonRequest{identity: ipPolicyTestIdentity(1), owner: ipPersonOwner{namespace: n}, evidence: v}
	d, e := r.digest(IPEvidenceBegin)
	if e != nil {
		t.Fatal(e)
	}
	r.owner.source.role = "other"
	d2, _ := r.digest(IPEvidenceBegin)
	if d == d2 {
		t.Fatal("full original root omitted source role")
	}
}

func TestIPPersonLockedEmptyNative(t *testing.T) {
	w, r := ipPersonFixture(t)
	ipPolicyTestExec(t, w, `INSERT INTO metadata_owner_fields(kind,entity_id,field,value,automatic_value,locked,actor,observed_at) VALUES('person',101,'biography','','old',1,'owner','before')`)
	ipPolicyTestExec(t, w, `INSERT INTO metadata_owner_fields(kind,entity_id,field,value,automatic_value,locked,actor,observed_at) VALUES('item',101,'biography','entity','entity-auto',1,'owner','before')`)
	turn := ipPersonTurn(t, w, IPProviderAccept)
	locks, e := ipPersonLocks(t.Context(), turn, r.owner.personID, ipPersonBudget())
	if e != nil || !locks[1].locked || locks[1].value != "" || !locks[1].valuePresent {
		t.Fatal(locks, e)
	}
	if e = ipPersonOne(t.Context(), turn, ipPPFieldUpdate, int64(101), "biography", "new-auto", "new-auto", "tmdb", "now"); e != nil {
		t.Fatal(e)
	}
	locks, e = ipPersonLocks(t.Context(), turn, r.owner.personID, ipPersonBudget())
	if e != nil || locks[1].value != "" || locks[1].automatic != "new-auto" {
		t.Fatal(locks, e)
	}
	ipPersonRollback(t, turn)
	if ipPolicyTestScalar(t, w, `SELECT locked FROM metadata_owner_fields WHERE kind='item' AND entity_id=101`) != 1 {
		t.Fatal("entity collision")
	}
}

func TestIPPersonZeroChildrenAndCancel(t *testing.T) {
	w, r := ipPersonFixture(t)
	r.evidence.aliases = nil
	r.evidence.externalIDs = nil
	ipPersonCommitment(t, &r)
	turn := ipPersonTurn(t, w, IPEvidenceBegin)
	if _, e := ipPersonBegin(t.Context(), turn, r); e != nil {
		t.Fatal(e)
	}
	r.buildRevision = 1
	if _, e := ipPersonSeal(t.Context(), turn, r, ipPersonBudget()); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	v, e := ipPersonReadEvidence(ctx, turn, r, 1, ipPersonBudget())
	if e == nil || v.header.name != "" {
		t.Fatal(v, e)
	}
	_, _ = turn.transaction.rollback(e)
}

func TestIPPersonBirthNative(t *testing.T) {
	w, r := ipPersonFixture(t)
	ipPolicyTestExec(t, w, `DELETE FROM metadata_person_operations`)
	turn := ipPersonTurn(t, w, IPProviderClaim)
	birth := ipPersonBirth{identity: r.identity, owner: r.owner, nowMS: r.nowMS}
	if e := ipPersonCreateOwner(t.Context(), turn, birth); e != nil {
		t.Fatal(e)
	}
	if e := ipPersonCreateOwner(t.Context(), turn, birth); e == nil {
		t.Fatal("duplicate birth accepted")
	}
	ipPersonRollback(t, turn)
	if ipPolicyTestScalar(t, w, `SELECT count(*) FROM metadata_person_operations`) != 0 {
		t.Fatal("birth survived rollback")
	}
}
func TestIPPersonNativeReclaimAbandoned(t *testing.T) {
	w, r := ipPersonFixture(t)
	turn := ipPersonTurn(t, w, IPEvidenceBegin)
	if _, e := ipPersonBegin(t.Context(), turn, r); e != nil {
		t.Fatal(e)
	}
	r.buildRevision = 1
	r.after = 1
	r.through = 3
	if _, e := ipPersonAppend(t.Context(), turn, r, ipPersonBudget()); e != nil {
		t.Fatal(e)
	}
	ipPolicyTestCommit(t, turn) // unreceipted disposable fixture only
	retire := ipPersonRetireRequest{identity: r.identity, ownerID: r.owner.id, personID: r.owner.personID, token: r.owner.token, ownerRevision: 1, evidenceID: r.evidence.id, buildID: r.evidence.buildID, buildRevision: 2, nowMS: r.nowMS}
	turn = ipPersonTurn(t, w, IPProviderReclaim)
	if e := ipPersonRetireBuild(t.Context(), turn, retire); e != nil {
		t.Fatal(e)
	}
	retire.buildRevision = 3
	retire.ownerRevision = 2
	first, e := ipPersonRetireChildren(t.Context(), turn, retire, ipPersonBudget())
	if e != nil || first.deletedRows != 1 || first.afterAlias != 0 || first.childrenDone {
		t.Fatal(first, e)
	}
	retire.buildRevision = 4
	second, e := ipPersonRetireChildren(t.Context(), turn, retire, ipPersonBudget())
	if e != nil || second.deletedRows != 1 || second.afterExternal.id != "Q203" || !second.childrenDone {
		t.Fatal(second, e)
	}
	ipPersonRollback(t, turn)
	if ipPolicyTestScalar(t, w, `SELECT count(*) FROM metadata_evidence_aliases`) != 1 || ipPolicyTestScalar(t, w, `SELECT count(*) FROM metadata_evidence_external_ids`) != 1 {
		t.Fatal("reclaim survived rollback")
	}
}

func TestIPPersonNativeAcceptReplayAndProofPins(t *testing.T) {
	sample := ipPersonSavedResult{kind: IPProviderClaim, value: ipPersonResult{revision: 1, appliedMS: 1}}
	if _, e := canonicalResultDigest(ipPolicyTestIdentity(1), RequestDigest{1}, sample); e != nil {
		t.Skip("parent must add two ipPersonSavedResult cases to result_digest.go before full receipt/accept smoke")
	}
	w, r := ipPersonFixture(t)
	ipPolicyTestExec(t, w, `INSERT INTO metadata_owner_fields(kind,entity_id,field,value,automatic_value,locked,actor,observed_at) VALUES('person',101,'biography','','old',1,'owner','before')`)
	kinds := []CommandID{IPEvidenceBegin, IPEvidenceAppend, IPEvidenceSeal, IPProviderAccept}
	var accepted ipPersonOutcome
	for idx, k := range kinds {
		r.identity = ipPolicyTestIdentity(int64(idx + 1))
		r.owner.revision = int64(idx + 1)
		switch k {
		case IPEvidenceAppend:
			r.owner.phase = "building"
			r.buildRevision = 1
			r.after = 1
			r.through = 3
		case IPEvidenceSeal:
			r.buildRevision = 2
		case IPProviderAccept:
			r.owner.phase = "sealed"
			r.buildRevision = 3
			r.publicationID = "person-publication"
			r.sealSequence = 3
		}
		turn := ipPersonTurn(t, w, k)
		out, e := ipPersonProviderBody(t.Context(), turn, k, r, ipPersonBudget())
		if e != nil {
			t.Fatal(k, e)
		}
		ipPolicyTestCommit(t, turn)
		if k == IPProviderAccept {
			accepted = out
			break
		}
		// Synthetic fixture ACK, not an issuer or a product ACK path.
		ipPolicyTestExec(t, w, `UPDATE metadata_person_operations SET acknowledged_command_sequence=last_command_sequence`)
	}
	var name, bio, date, place string
	if e := w.db.QueryRowContext(t.Context(), `SELECT name,biography,birth_date,birth_place FROM catalog_people WHERE id=101`).Scan(&name, &bio, &date, &place); e != nil || name != "Person é" || bio != "" || date != "1980-02-29" || place != "Halifax" {
		t.Fatal(name, bio, date, place, e)
	}
	if accepted.change.personID != 101 || accepted.change.revision != 18 {
		t.Fatal(accepted)
	}
	// Replay is complete historical result, before fresh revision/lease/policy.
	ipPolicyTestExec(t, w, `UPDATE catalog_people SET revision=999`)
	ipPolicyTestExec(t, w, `UPDATE metadata_provider_policy SET enabled=0,revision=999`)
	ipPolicyTestExec(t, w, `UPDATE metadata_person_operations SET lease_until_ms=0,revision=999`)
	turn := ipPersonTurn(t, w, IPProviderAccept)
	replay, e := ipPersonProviderBody(t.Context(), turn, IPProviderAccept, r, ipPersonBudget())
	if e != nil || replay.result != accepted.result || replay.change != (ipPersonSearchIntent{}) {
		t.Fatal(replay, e)
	}
	ipPolicyTestCommit(t, turn)
	// ACK of the seal never removes publication/build proof custody.
	retire := ipPersonRetireRequest{identity: r.identity, ownerID: r.owner.id, personID: 101, token: r.owner.token, ownerRevision: 999, evidenceID: r.evidence.id, buildID: r.evidence.buildID, buildRevision: 3, nowMS: r.nowMS}
	turn = ipPersonTurn(t, w, IPProviderReclaim)
	if e = ipPersonRetireBuild(t.Context(), turn, retire); e == nil {
		t.Fatal("ACK erased pinned seal")
	}
	_, _ = turn.transaction.rollback(e)
}

func TestIPPersonResultWireVector(t *testing.T) {
	i := CommandIdentity{Incarnation: [16]byte{1}, Sequence: 2, OperationID: [16]byte{3}}
	q := RequestDigest{4}
	v := ipPersonSavedResult{kind: IPEvidenceSeal, value: ipPersonResult{revision: 7, after: 1, count: 1, appliedMS: 9, evidenceID: "e", buildID: "b", sealAlgorithm: ipPersonManifest, sealVersion: 1, sealCommitment: [32]byte{5}, sealRows: 1, sealBytes: 200}}
	if e := preflightIPPersonResult(v); e != nil {
		t.Fatal(e)
	}
	enc := requestEncoder{h: sha256.New()}
	if e := writeIPPersonResult(&enc, i, q, v); e != nil {
		t.Fatal(e)
	}
	// Independently assembled fixed wire, using the documented BE64/LP order.
	var wire []byte
	number := func(n uint64) { var p [8]byte; binary.BigEndian.PutUint64(p[:], n); wire = append(wire, p[:]...) }
	text := func(s string) { number(uint64(len(s))); wire = append(wire, s...) }
	wire = append(wire, []byte("portico-writer-result-sha256-v1\x00")...)
	wire = append(wire, 0, 1)
	text("ip_person_provider")
	wire = append(wire, i.Incarnation[:]...)
	number(2)
	wire = append(wire, i.OperationID[:]...)
	wire = append(wire, q[:]...)
	wire = append(wire, 0, 212)
	for _, n := range []uint64{7, 1, 1, 9} {
		number(n)
	}
	text("e")
	text("b")
	text("")
	wire = append(wire, 1)
	text("portico-ip-person-source-sha256-v1")
	number(1)
	wire = append(wire, v.value.sealCommitment[:]...)
	number(1)
	number(200)
	want := sha256.Sum256(wire)
	if hex.EncodeToString(enc.h.Sum(nil)) != hex.EncodeToString(want[:]) || enc.written != uint64(len(wire)) || hex.EncodeToString(want[:]) != "7fded29efc04e42b5075cda0c2f33ec2fd4f587caa2cda4cd1ce1d1a3d8f0efb" {
		t.Fatal("result wire differs from independent vector")
	}
	t.Logf("person result vector sha256=%x bytes=%d", want, len(wire))
}

func TestIPPersonRequestVariantPreflight(t *testing.T) {
	n := ipPersonNamespace{44, "tmdb", "203", "en", "US", ipPersonShape, ipPolicyTestInc, 6}
	v, e := ipPersonPrepareEvidence(n, "e", "b", ipPersonHeader{"Name", "", "", "", "", "now", "fixture"}, nil, nil)
	if e != nil {
		t.Fatal(e)
	}
	r := ipPersonRequest{identity: ipPolicyTestIdentity(1), owner: ipPersonOwner{id: "o", personID: 1, token: "token", personRevision: 1, libraryID: 1, libraryLanguage: "en-US", libraryMetadataRevision: 1, namespace: n, policyIncarnation: ipPolicyTestInc, policyRevision: 1, consentRevision: 1, generation: 1, revision: 1, phase: "claimed", leaseOwner: "w", leaseGeneration: 1, leaseUntilMS: 20, source: ipPersonSource{role: "primary", incarnation: [16]byte{2}}}, evidence: v, nowMS: 10}
	if !r.valid(IPEvidenceBegin) {
		t.Fatal("valid bounded root refused")
	}
	r.terminal = strings.Repeat("x", 129)
	if r.valid(IPEvidenceBegin) {
		t.Fatal("unused field escaped complete root byte bounds")
	}
	r.terminal = ""
	r.nextLeaseOwner = "w"
	r.nextLeaseGeneration = 1
	r.nextLeaseUntilMS = 30
	if r.valid(IPProviderRenew) {
		t.Fatal("renew carried unrelated source graph")
	}
	r.evidence = ipPersonEvidence{}
	if !r.valid(IPProviderRenew) {
		t.Fatal("valid renewal refused")
	}
	r.evidence = v
	r.publicationID = "new-publication"
	r.sealSequence = 3
	if r.valid(IPProviderDelta) {
		t.Fatal("delta admitted absent accepted source")
	}
	r.owner.source.revision = 1
	r.owner.source.publication = "old-publication"
	r.owner.source.evidence = "old-evidence"
	if !r.valid(IPProviderDelta) {
		t.Fatal("bounded exact-head delta refused")
	}
	delta := ipPersonSavedResult{kind: IPProviderDelta, value: ipPersonResult{revision: 2, after: 1, count: 1, appliedMS: 1, evidenceID: "e", buildID: "b", publicationID: "p"}}
	if e := preflightIPPersonResult(delta); e != nil {
		t.Fatal(e)
	}
}

func TestIPPersonBirthCanonicalGate(t *testing.T) {
	w, r := ipPersonFixture(t)
	ipPolicyTestExec(t, w, `DELETE FROM metadata_person_operations`)
	turn := ipPersonTurn(t, w, IPProviderClaim)
	out, e := ipPersonBirthBody(t.Context(), turn, ipPersonBirth{r.identity, r.owner, r.nowMS}, ipPersonBudget())
	if e == nil {
		ipPolicyTestCommit(t, turn)
		if out.result.revision != 2 {
			t.Fatal(out)
		}
		return
	}
	if !errors.Is(e, ErrInvalid) || out != (ipPersonOutcome{}) {
		t.Fatal(out, e)
	}
	_, _ = turn.transaction.rollback(e)
	if ipPolicyTestScalar(t, w, `SELECT count(*) FROM metadata_person_operations`) != 0 {
		t.Fatal("birth codec gate leaked owner")
	}
}

func TestIPPersonOneBuildRecoveryAndTerminal(t *testing.T) {
	w, r := ipPersonFixture(t)
	turn := ipPersonTurn(t, w, IPEvidenceBegin)
	if _, e := ipPersonBegin(t.Context(), turn, r); e != nil {
		t.Fatal(e)
	}
	ipPolicyTestCommit(t, turn) // construction fixture; no accepting receipt
	// Original BEGIN is ACKed and its receipt physically reclaimed. Its original
	// root must still return Retired; recovery is an independent owner-key read.
	ipPolicyTestExec(t, w, `UPDATE metadata_person_operations SET last_command_sequence=1,acknowledged_command_sequence=1,receipt_reclaim_after_sequence=1,revision=2`)
	turn = ipPersonTurn(t, w, IPEvidenceBegin)
	old, e := ipPersonProviderBody(t.Context(), turn, IPEvidenceBegin, r, ipPersonBudget())
	if e != nil || old.decision != receiptRetired || old.result != (ipPersonResult{}) {
		t.Fatal(old, e)
	}
	recoverKey := ipPersonRecoveryRequest{r.owner.id, r.identity.Incarnation, r.owner.personID, r.owner.token}
	recovered, e := ipPersonRecover(t.Context(), turn, recoverKey, ipPersonBudget())
	if e != nil || !recovered.exists || recovered.buildID != r.evidence.buildID || recovered.evidenceID != r.evidence.id || recovered.generation != r.owner.generation || recovered.ownerGeneration != r.owner.generation || recovered.cursor != 1 {
		t.Fatal(recovered, e)
	}
	ipPolicyTestCommit(t, turn)
	// The native UNIQUE owner key rejects a second build with a DISTINCT evidence
	// ID too; this is not the unrelated UNIQUE(evidence_id) falsifier.
	ipPolicyTestExec(t, w, `INSERT INTO metadata_evidence(id,subject_id,parser_version,provider,subject_kind,provider_id,language,region,request_shape,digest,observed_at,source_url,title) SELECT 'second-evidence',subject_id,parser_version,provider,subject_kind,provider_id,language,region,request_shape,?,observed_at,source_url,title FROM metadata_evidence WHERE id='person-evidence'`, strings.Repeat("a", 64))
	_, e = w.db.ExecContext(t.Context(), `INSERT INTO metadata_person_builds(id,evidence_id,provider,subject_kind,provider_id,owner_id,owner_incarnation,owner_generation,generation,content_revision,state,cursor,expected_rows,expected_bytes,expected_algorithm,expected_commitment,staged_rows,staged_bytes,materialized_charge) SELECT 'second-build','second-evidence',provider,subject_kind,provider_id,owner_id,owner_incarnation,owner_generation,generation,content_revision,state,cursor,expected_rows,expected_bytes,expected_algorithm,expected_commitment,staged_rows,staged_bytes,materialized_charge FROM metadata_person_builds WHERE id='person-build'`)
	if e == nil || !strings.Contains(e.Error(), "metadata_person_builds.owner_id") {
		t.Fatal("missing exact UNIQUE(owner,incarnation)", e)
	}
	// Renewal and retry preserve the existing phase and operation generation.
	turn = ipPersonTurn(t, w, IPProviderRenew)
	if e = ipPersonOne(t.Context(), turn, ipPPLease, r.owner.leaseOwner, r.owner.leaseGeneration, r.owner.leaseUntilMS+1000, r.owner.id, r.identity.Incarnation[:], int64(2), r.owner.leaseOwner, r.owner.leaseGeneration, r.owner.leaseUntilMS); e != nil {
		t.Fatal(e)
	}
	if e = ipPersonOne(t.Context(), turn, ipPPRetry, r.nowMS+500, r.nowMS, r.owner.id, r.identity.Incarnation[:], int64(2), r.owner.leaseOwner, r.owner.leaseGeneration, r.owner.leaseUntilMS+1000); e != nil {
		t.Fatal(e)
	}
	again, e := ipPersonRecover(t.Context(), turn, recoverKey, ipPersonBudget())
	if e != nil || again != recovered {
		t.Fatal("lease/retry changed source generation/build", again, e)
	}
	ipPolicyTestCommit(t, turn)
	// Retirement retains a terminal owner; deleting its build cannot revive BEGIN.
	retire := ipPersonRetireRequest{identity: r.identity, ownerID: r.owner.id, personID: r.owner.personID, token: r.owner.token, ownerRevision: 2, evidenceID: r.evidence.id, buildID: r.evidence.buildID, buildRevision: 1, nowMS: r.nowMS}
	turn = ipPersonTurn(t, w, IPProviderReclaim)
	if e = ipPersonRetireBuild(t.Context(), turn, retire); e != nil {
		t.Fatal(e)
	}
	ipPolicyTestCommit(t, turn)
	ipPolicyTestExec(t, w, `DELETE FROM metadata_person_builds WHERE id='person-build'`) // terminal-body retirement fixture
	turn = ipPersonTurn(t, w, IPEvidenceBegin)
	terminal, e := ipPersonRecover(t.Context(), turn, recoverKey, ipPersonBudget())
	if e != nil || terminal.exists || !terminal.terminal || terminal.ownerGeneration != 2 {
		t.Fatal(terminal, e)
	}
	next := r
	next.owner.revision = 3
	next.evidence.id = "third-evidence"
	next.evidence.buildID = "third-build"
	if _, e = ipPersonBegin(t.Context(), turn, next); !errors.Is(e, ErrConflict) {
		t.Fatal("terminal owner allowed rebirth", e)
	}
	_, _ = turn.transaction.rollback(e)
	if ipPolicyTestScalar(t, w, `SELECT count(*) FROM metadata_person_operations WHERE phase='done' AND generation=2`) != 1 || ipPolicyTestScalar(t, w, `SELECT count(*) FROM metadata_person_builds`) != 0 {
		t.Fatal("terminal owner/generation lost")
	}
}

func TestIPPersonACKKeepsPersonSealFK(t *testing.T) {
	w, r := ipPersonFixture(t)
	r.evidence.aliases = nil
	r.evidence.externalIDs = nil
	ipPersonCommitment(t, &r)
	turn := ipPersonTurn(t, w, IPEvidenceBegin)
	if _, e := ipPersonBegin(t.Context(), turn, r); e != nil {
		t.Fatal(e)
	}
	r.buildRevision = 1
	sealed, e := ipPersonSeal(t.Context(), turn, r, ipPersonBudget())
	if e != nil {
		t.Fatal(e)
	}
	ipPolicyTestCommit(t, turn)
	// Fixture-only FK test. Zero hashes are never accepted or disclosed as proof.
	ipPolicyTestExec(t, w, `INSERT INTO metadata_person_receipts(owner_id,command_incarnation,command_sequence,operation_id,request_digest,applied_ms,result_digest,command_kind,accepted_revision,after_ordinal,result_count,evidence_id,build_id,seal_algorithm,seal_version,seal_commitment,seal_rows,seal_bytes) VALUES(?,?,1,?,zeroblob(32),?,zeroblob(32),'evidence_seal',2,1,1,?,?,?,1,?,1,?)`, r.owner.id, r.identity.Incarnation[:], r.identity.OperationID[:], r.nowMS, r.evidence.id, r.evidence.buildID, ipPersonManifest, sealed.sealCommitment[:], sealed.sealBytes)
	ipPolicyTestExec(t, w, `UPDATE metadata_person_operations SET last_command_sequence=1,acknowledged_command_sequence=1`)
	turn = ipPersonTurn(t, w, IPEvidenceSeal)
	if e = ipPersonOne(t.Context(), turn, ipPPBuildSeal, r.owner.id, r.identity.Incarnation[:], int64(1), r.evidence.buildID); e != nil {
		t.Fatal(e)
	}
	ipPolicyTestCommit(t, turn)
	if _, e = w.db.ExecContext(t.Context(), `DELETE FROM metadata_person_receipts WHERE owner_id='person-op' AND command_sequence=1`); e == nil {
		t.Fatal("ACK removed FK-pinned person seal")
	}
	if ipPolicyTestScalar(t, w, `SELECT count(*) FROM metadata_person_receipts`) != 1 {
		t.Fatal("lost seal receipt")
	}
}
