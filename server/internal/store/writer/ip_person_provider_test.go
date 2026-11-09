package writer

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

// Group D is not embedded, so person custody and the evidence exact-date
// carriers are qualified only against the exact draft subset. The catalogue
// person, library, provider, subject, policy and generic evidence body are the
// real embedded owners.
func ipPersonFixture(t *testing.T) (*executor, *ipPolicyRegistry, *usOwnerAuthorityProof, time.Time) {
	t.Helper()
	w, keeper, p := ipPolicySecurityFixture(t)
	base := w.now()
	ddl, err := os.ReadFile("testdata/ip-person-draft.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = w.db.ExecContext(t.Context(), string(ddl)); err != nil {
		t.Fatal(err)
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
	registry, err := stmttest.New(t, defs)
	if err != nil {
		t.Fatal(err)
	}
	return w, &ipPolicyRegistry{registry, keeper}, p, base
}

func ipPersonAdvance(t *testing.T, w *executor, base time.Time, d time.Duration) {
	t.Helper()
	at := base.Add(d)
	w.now = func() time.Time { return at }
}

func ipPersonIdentity(sequence int64, operation byte) CommandIdentity {
	return CommandIdentity{Incarnation: [16]byte{2}, Sequence: sequence, OperationID: [16]byte{operation}}
}

func ipPersonClaimFor() ipPersonClaimRequest {
	return ipPersonClaimRequest{
		personID: 42, personToken: "person-token-42", personRevision: 1,
		libraryID: 777, libraryKey: "ip-library",
		provider: "tmdb", providerID: "42", language: "en-US", region: "US",
		requestShape:       "tmdb.person.detail.header-exact-dates.v1",
		subjectID:          501,
		subjectIncarnation: [16]byte{0x0b},
		subjectRevision:    1,
		policyIncarnation:  [16]byte{9},
		policyRevision:     1,
		consentRevision:    1,
	}
}

func ipPersonClaimCommand(t *testing.T, identity CommandIdentity, mutate func(*ipPersonClaimRequest)) *ipPersonCommand {
	t.Helper()
	q := ipPersonClaimFor()
	if mutate != nil {
		mutate(&q)
	}
	c, err := newIPPersonClaimCommand(identity, q)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func ipPersonClaimed(t *testing.T, w *executor, r *ipPolicyRegistry, p *usOwnerAuthorityProof) (ipPersonResult, CommandIdentity) {
	t.Helper()
	identity := ipPersonIdentity(1, 0x31)
	turn, tx, b := ipPolicyTurn(t, w, r, IPPersonClaim)
	out, err := applyIPPersonClaim(t.Context(), turn, p, t.Context(), b, ipPersonClaimCommand(t, identity, nil))
	if err != nil || out.Outcome.State != Applied {
		t.Fatal(out, err)
	}
	ipPolicyCommitTurn(t, turn, tx)
	ipPersonAck(t, w, out.Value.OperationID, out.Value.Revision)
	return out.Value, identity
}

func ipPersonAck(t *testing.T, w *executor, owner string, sequence int64) {
	t.Helper()
	ipPolicyExec(t, w, `UPDATE metadata_person_operations SET acknowledged_command_sequence=? WHERE id=?`, sequence, owner)
	if got := ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_person_operations WHERE id=? AND acknowledged_command_sequence=?`, owner, sequence); got != 1 {
		t.Fatal("ack failed", got)
	}
}

func ipPersonOwnerFreshFor(v ipPersonResult) ipPersonRenewRequest {
	return ipPersonRenewRequest{operationID: v.OperationID, provider: v.Provider, providerID: v.ProviderID,
		personToken: "", personID: v.PersonID, lease: v.Lease, generation: v.Generation,
		revision: v.Revision, leaseGeneration: v.LeaseGeneration, leaseUntilMS: v.LeaseUntilMS}
}

func ipPersonRenewFor(v ipPersonResult) ipPersonRenewRequest {
	q := ipPersonOwnerFreshFor(v)
	q.personToken = "person-token-42"
	return q
}

func ipPersonFinishFor(v ipPersonResult, outcome string) ipPersonFinishRequest {
	return ipPersonFinishRequest{operationID: v.OperationID, provider: v.Provider, providerID: v.ProviderID,
		personToken: "person-token-42", personID: v.PersonID, lease: v.Lease, generation: v.Generation,
		revision: v.Revision, leaseGeneration: v.LeaseGeneration, leaseUntilMS: v.LeaseUntilMS,
		outcome: outcome}
}

func mustIPPersonRenew(t *testing.T, identity CommandIdentity, q ipPersonRenewRequest) *ipPersonCommand {
	t.Helper()
	c, err := newIPPersonRenewCommand(identity, q)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func mustIPPersonFinish(t *testing.T, identity CommandIdentity, q ipPersonFinishRequest) *ipPersonCommand {
	t.Helper()
	c, err := newIPPersonFinishCommand(identity, q)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func ipPersonHeaderInput() EvidenceHeaderInput {
	return EvidenceHeaderInput{
		ID: "evidence-person-1", ParserVersion: "tmdb.person.header-exact-dates.v1",
		Provider: "tmdb", SubjectKind: "person", ProviderID: "42", SubjectID: 501,
		Language: "en-US", Region: "US",
		RequestShape:      "tmdb.person.detail.header-exact-dates.v1",
		ObservationDigest: [32]byte{7}, ObservedAt: "2026-01-01T00:00:00Z",
		SourceURL: "https://api.themoviedb.org/3/person/42",
		Title:     "Ada Lovelace", Biography: "Mathematician.",
		BiographySource: "tmdb", BiographySourceURL: "https://api.themoviedb.org/3/person/42",
	}
}

func ipPersonBeginFor(t *testing.T, first ipPersonResult) ipPersonEvidenceBeginRequest {
	t.Helper()
	q := ipPersonEvidenceBeginRequest{
		operationID: first.OperationID, provider: first.Provider, providerID: first.ProviderID,
		language: "en-US", region: "US",
		requestShape: "tmdb.person.detail.header-exact-dates.v1", lease: first.Lease,
		personID: first.PersonID, subjectID: 501, generation: first.Generation, revision: first.Revision,
		leaseGeneration: first.LeaseGeneration, leaseUntilMS: first.LeaseUntilMS,
		buildID: "build-person-1", buildGeneration: 1,
		header:     ipPersonHeaderInput(),
		birthDate:  EvidenceText{Present: true, Value: "1815-12-10"},
		deathDate:  EvidenceText{Present: true, Value: "1852-11-27"},
		birthPlace: EvidenceText{Present: true, Value: "London"},
		aliases:    []ipPersonEvidenceAlias{{RequestedID: "Ada", CanonicalID: "42", Alias: "Ada Lovelace", ObservedAt: "2026-01-01T00:00:00Z"}},
		external:   []ipPersonEvidenceExternal{{Provider: "imdb", SubjectKind: "person", ProviderID: "nm0000001"}},
	}
	header, err := NewEvidenceHeader(q.header)
	if err != nil {
		t.Fatal(err)
	}
	commitment, total, _, err := ipPersonEvidenceManifest(q, header)
	if err != nil {
		t.Fatal(err)
	}
	q.expectedCommitment = commitment
	q.expectedBytes = total
	q.expectedRows = 1 + int64(q.children())
	return q
}

func mustIPPersonBegin(t *testing.T, identity CommandIdentity, q ipPersonEvidenceBeginRequest) *ipPersonCommand {
	t.Helper()
	c, err := newIPPersonEvidenceBeginCommand(identity, q)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestIPPersonClaimFreshReplayAndRetired(t *testing.T) {
	w, r, p, _ := ipPersonFixture(t)
	identity := ipPersonIdentity(1, 0x31)
	out, turn, tx, _ := func() (ipPersonCommandResult, *commandTurn, *transaction, ipPolicyBudget) {
		turn, tx, b := ipPolicyTurn(t, w, r, IPPersonClaim)
		out, err := applyIPPersonClaim(t.Context(), turn, p, t.Context(), b, ipPersonClaimCommand(t, identity, nil))
		if err != nil {
			t.Fatal(err)
		}
		return out, turn, tx, b
	}()
	if out.Outcome.State != Applied || out.Outcome.Replayed || out.Value.Kind != "claim" ||
		out.Value.OperationID != ipPersonHex(identity.OperationID) ||
		out.Value.Lease != ipPersonHex(identity.Incarnation) || out.Value.Generation != 1 ||
		out.Value.Revision != 1 || out.Value.LeaseGeneration != 1 ||
		out.Value.LeaseUntilMS != w.now().UnixMilli()+ipPersonLeaseMS ||
		out.Value.AppliedMS != w.now().UnixMilli() || out.Digest == (ResultDigest{}) {
		t.Fatal(out)
	}
	ipPolicyCommitTurn(t, turn, tx)
	for query, want := range map[string]int64{
		`SELECT count(*) FROM metadata_person_operations WHERE id='` + out.Value.OperationID + `' AND provider='tmdb' AND provider_id='42' AND person_id=42 AND phase='claimed' AND generation=1 AND revision=1 AND lease_generation=1 AND last_command_sequence=1 AND acknowledged_command_sequence=0`: 1,
		`SELECT count(*) FROM metadata_person_operations WHERE captured_person_token='person-token-42' AND captured_person_revision=1 AND subject_id=501 AND policy_revision=1 AND consent_revision=1`:                                                                                                  1,
		`SELECT count(*) FROM metadata_person_receipts WHERE operation_id=X'` + ipPersonHex(identity.OperationID) + `' AND command_kind='begin' AND evidence_id IS NULL AND accepted_revision=1`:                                                                                                        1,
	} {
		if got := ipPolicyQueryInt(t, w, query); got != want {
			t.Fatal(query, got)
		}
	}
	// The original receipt resolves before any fresh person/subject/policy probe.
	original := out
	ipPolicyExec(t, w, `UPDATE catalog_people SET revision=5 WHERE id=42`)
	ipPolicyExec(t, w, `UPDATE metadata_provider_subjects SET revision=5 WHERE id=501`)
	ipPolicyExec(t, w, `UPDATE metadata_provider_policy SET revision=5 WHERE library_id=777 AND provider='tmdb'`)
	ipPolicyExec(t, w, `UPDATE metadata_providers SET enabled=0 WHERE code='tmdb'`)
	turn, tx, b := ipPolicyTurn(t, w, r, IPPersonClaim)
	replay, err := applyIPPersonClaim(t.Context(), turn, p, t.Context(), b, ipPersonClaimCommand(t, identity, nil))
	if err != nil || !replay.Outcome.Replayed || replay.Value != original.Value || replay.Digest != original.Digest {
		t.Fatal("claim replay after source change", replay, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
	if got := ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_person_receipts`); got != 1 {
		t.Fatal("replay wrote receipts", got)
	}
	// At or below ACK is Retired even while the receipt remains physical.
	ipPolicyExec(t, w, `UPDATE metadata_person_operations SET acknowledged_command_sequence=1 WHERE id='`+original.Value.OperationID+`'`)
	turn, tx, b = ipPolicyTurn(t, w, r, IPPersonClaim)
	retired, err := applyIPPersonClaim(t.Context(), turn, p, t.Context(), b, ipPersonClaimCommand(t, identity, nil))
	if err != nil || retired.Outcome.State != ResultRetired || retired.Value != (ipPersonResult{}) || retired.Digest != (ResultDigest{}) {
		t.Fatal("retired claim served", retired, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
}

func TestIPPersonClaimExactPersonSubjectPolicyAndConsent(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*ipPersonClaimRequest)
		sql    []string
		want   error
	}{
		{"wrong_token", func(q *ipPersonClaimRequest) { q.personToken = "other-token" }, nil, ErrDisclosureDenied},
		{"stale_person_revision", func(q *ipPersonClaimRequest) { q.personRevision = 2 }, nil, ErrConflict},
		{"wrong_subject", func(q *ipPersonClaimRequest) { q.subjectID = 599 }, nil, ErrDisclosureDenied},
		{"stale_subject_revision", func(q *ipPersonClaimRequest) { q.subjectRevision = 2 }, nil, ErrConflict},
		{"wrong_subject_incarnation", func(q *ipPersonClaimRequest) { q.subjectIncarnation = [16]byte{0x0c} }, nil, ErrConflict},
		{"wrong_namespace", func(q *ipPersonClaimRequest) { q.language = "fr-FR" }, nil, ErrDisclosureDenied},
		{"policy_region", func(q *ipPersonClaimRequest) { q.region = "FR"; q.subjectID = 503 }, nil, ErrConflict},
		{"stale_policy_revision", func(q *ipPersonClaimRequest) { q.policyRevision = 2 }, nil, ErrConflict},
		{"wrong_policy_incarnation", func(q *ipPersonClaimRequest) { q.policyIncarnation = [16]byte{0x0a} }, nil, ErrConflict},
		{"wrong_consent", func(q *ipPersonClaimRequest) { q.consentRevision = 2 }, nil, ErrDisclosureDenied},
		{"wrong_library_key", func(q *ipPersonClaimRequest) { q.libraryKey = "other-library" }, nil, ErrDisclosureDenied},
		{"disabled_provider", nil, []string{`UPDATE metadata_providers SET enabled=0 WHERE code='tmdb'`}, ErrDisclosureDenied},
		{"retired_library", nil, []string{`UPDATE catalog_libraries SET retired=1 WHERE id=777`}, ErrDisclosureDenied},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w, r, p, _ := ipPersonFixture(t)
			for _, query := range tc.sql {
				ipPolicyExec(t, w, query)
			}
			turn, tx, b := ipPolicyTurn(t, w, r, IPPersonClaim)
			out, err := applyIPPersonClaim(t.Context(), turn, p, t.Context(), b,
				ipPersonClaimCommand(t, ipPersonIdentity(1, 0x31), tc.mutate))
			if out != (ipPersonCommandResult{}) || !errors.Is(err, tc.want) {
				t.Fatal(out, err)
			}
			ipPolicyRollbackTurn(t, turn, tx)
			if got := ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_person_operations`); got != 0 {
				t.Fatal("refused claim wrote an owner", got)
			}
		})
	}
}

func TestIPPersonClaimConsentDomain(t *testing.T) {
	for _, tc := range []struct {
		name, provider                        string
		subject, consent                      int64
		subjectIncarnation, policyIncarnation [16]byte
		ok                                    bool
	}{
		{"tmdb_screen", "tmdb", 501, 1, [16]byte{0x0b}, [16]byte{9}, true},
		{"musicbrainz_epoch", "musicbrainz", 502, 3, [16]byte{0x0c}, [16]byte{0x0a}, true},
		{"musicbrainz_screen_revision", "musicbrainz", 502, 1, [16]byte{0x0c}, [16]byte{0x0a}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, r, p, _ := ipPersonFixture(t)
			turn, tx, b := ipPolicyTurn(t, w, r, IPPersonClaim)
			q := func() *ipPersonCommand {
				c := ipPersonClaimCommand(t, ipPersonIdentity(1, 0x31), func(q *ipPersonClaimRequest) {
					q.provider, q.subjectID, q.consentRevision = tc.provider, tc.subject, tc.consent
					// Subject 502 and the musicbrainz policy carry their own
					// incarnations; the tmdb defaults would refuse before the
					// epoch rule this case proves.
					q.subjectIncarnation, q.policyIncarnation = tc.subjectIncarnation, tc.policyIncarnation
				})
				return c
			}()
			out, err := applyIPPersonClaim(t.Context(), turn, p, t.Context(), b, q)
			if tc.ok {
				if err != nil || out.Outcome.State != Applied {
					t.Fatal(out, err)
				}
				ipPolicyCommitTurn(t, turn, tx)
				return
			}
			if out != (ipPersonCommandResult{}) || !errors.Is(err, ErrDisclosureDenied) {
				t.Fatal(out, err)
			}
			ipPolicyRollbackTurn(t, turn, tx)
		})
	}
}

func TestIPPersonRenewFreshReplayAndRetired(t *testing.T) {
	w, r, p, base := ipPersonFixture(t)
	first, _ := ipPersonClaimed(t, w, r, p)
	ipPersonAdvance(t, w, base, time.Second)
	q := ipPersonRenewFor(first)
	identity := ipPersonIdentity(2, 0x41)
	turn, tx, b := ipPolicyTurn(t, w, r, IPPersonRenew)
	out, err := applyIPPersonRenew(t.Context(), turn, p, t.Context(), b, mustIPPersonRenew(t, identity, q))
	if err != nil || out.Outcome.State != Applied || out.Value.Revision != 2 ||
		out.Value.LeaseGeneration != 2 || out.Value.Kind != "renew" ||
		out.Value.LeaseUntilMS != w.now().UnixMilli()+ipPersonLeaseMS {
		t.Fatal(out, err)
	}
	ipPolicyCommitTurn(t, turn, tx)
	if got := ipPolicyQueryInt(t, w, `SELECT revision FROM metadata_person_operations WHERE id=?`, first.OperationID); got != 2 {
		t.Fatal("renew revision", got)
	}
	// The original renew result replays before any fresh lease or source probe.
	original := out
	ipPolicyExec(t, w, `UPDATE catalog_people SET revision=9 WHERE id=42`)
	ipPolicyExec(t, w, `UPDATE metadata_person_operations SET lease_until_ms=1 WHERE id='`+first.OperationID+`'`)
	turn, tx, b = ipPolicyTurn(t, w, r, IPPersonRenew)
	replay, err := applyIPPersonRenew(t.Context(), turn, p, t.Context(), b, mustIPPersonRenew(t, identity, q))
	if err != nil || !replay.Outcome.Replayed || replay.Value != original.Value || replay.Digest != original.Digest {
		t.Fatal("renew replay", replay, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
	ipPersonAck(t, w, first.OperationID, 2)
	turn, tx, b = ipPolicyTurn(t, w, r, IPPersonRenew)
	retired, err := applyIPPersonRenew(t.Context(), turn, p, t.Context(), b, mustIPPersonRenew(t, identity, q))
	if err != nil || retired.Outcome.State != ResultRetired || retired.Value != (ipPersonResult{}) {
		t.Fatal("retired renew served", retired, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
}

func TestIPPersonLifecycleStaleLeaseAndAuthority(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*ipPersonRenewRequest)
		sql    []string
		want   error
	}{
		{"wrong_lease", func(q *ipPersonRenewRequest) { q.lease = strings.Repeat("0", 32) }, nil, ErrConflict},
		{"wrong_lease_generation", func(q *ipPersonRenewRequest) { q.leaseGeneration = 4 }, nil, ErrConflict},
		{"wrong_revision", func(q *ipPersonRenewRequest) { q.revision = 7 }, nil, ErrConflict},
		{"wrong_person_token", func(q *ipPersonRenewRequest) { q.personToken = "other-token" }, nil, ErrConflict},
		{"stale_person_revision", nil, []string{`UPDATE catalog_people SET revision=2 WHERE id=42`}, ErrConflict},
		{"stale_subject", nil, []string{`UPDATE metadata_provider_subjects SET revision=2 WHERE id=501`}, ErrConflict},
		{"stale_policy", nil, []string{`UPDATE metadata_provider_policy SET revision=2 WHERE library_id=777 AND provider='tmdb'`}, ErrConflict},
		{"revoked_consent", nil, []string{`UPDATE screen_metadata_consent SET confirmed=0`}, ErrDisclosureDenied},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w, r, p, base := ipPersonFixture(t)
			first, _ := ipPersonClaimed(t, w, r, p)
			ipPersonAdvance(t, w, base, time.Second)
			for _, query := range tc.sql {
				ipPolicyExec(t, w, query)
			}
			q := ipPersonRenewFor(first)
			if tc.mutate != nil {
				tc.mutate(&q)
			}
			turn, tx, b := ipPolicyTurn(t, w, r, IPPersonRenew)
			out, err := applyIPPersonRenew(t.Context(), turn, p, t.Context(), b,
				mustIPPersonRenew(t, ipPersonIdentity(2, 0x41), q))
			if out != (ipPersonCommandResult{}) || !errors.Is(err, tc.want) {
				t.Fatal(out, err)
			}
			ipPolicyRollbackTurn(t, turn, tx)
		})
	}
}

func TestIPPersonFinishDoneAndRetry(t *testing.T) {
	for _, outcome := range []string{"done", "retry"} {
		t.Run(outcome, func(t *testing.T) {
			w, r, p, base := ipPersonFixture(t)
			first, _ := ipPersonClaimed(t, w, r, p)
			ipPersonAdvance(t, w, base, time.Second)
			identity := ipPersonIdentity(2, 0x51)
			q := ipPersonFinishFor(first, outcome)
			turn, tx, b := ipPolicyTurn(t, w, r, IPPersonFinish)
			out, err := applyIPPersonFinish(t.Context(), turn, p, t.Context(), b, mustIPPersonFinish(t, identity, q))
			if err != nil || out.Outcome.State != Applied || out.Value.Kind != "finish" ||
				out.Value.Outcome != outcome || out.Value.Revision != 2 {
				t.Fatal(out, err)
			}
			ipPolicyCommitTurn(t, turn, tx)
			if got := ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_person_operations WHERE id=? AND lease_owner=? AND lease_until_ms=?`,
				first.OperationID, first.Lease, w.now().UnixMilli()); got != 1 {
				t.Fatal("finish lease", got)
			}
			// The saved terminal/retry result replays before any fresh lease probe.
			original := out
			turn, tx, b = ipPolicyTurn(t, w, r, IPPersonFinish)
			replay, err := applyIPPersonFinish(t.Context(), turn, p, t.Context(), b, mustIPPersonFinish(t, identity, q))
			if err != nil || !replay.Outcome.Replayed || replay.Value != original.Value || replay.Digest != original.Digest {
				t.Fatal("finish replay", replay, err)
			}
			ipPolicyRollbackTurn(t, turn, tx)
			if outcome == "done" {
				if got := ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_person_operations WHERE id=? AND phase='done' AND next_attempt_ms IS NULL`, first.OperationID); got != 1 {
					t.Fatal("done phase", got)
				}
				// A terminal owner never begins a second build.
				begin := ipPersonBeginFor(t, first)
				begin.revision = out.Value.Revision
				turn, tx, b = ipPolicyTurn(t, w, r, IPPersonEvidenceBegin)
				refused, err := applyIPPersonEvidenceBegin(t.Context(), turn, p, t.Context(), b,
					mustIPPersonBegin(t, ipPersonIdentity(3, 0x61), begin))
				if refused != (ipPersonCommandResult{}) || err == nil {
					t.Fatal("terminal owner began a build", refused, err)
				}
				ipPolicyRollbackTurn(t, turn, tx)
			} else {
				if got := ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_person_operations WHERE id=? AND phase='claimed' AND next_attempt_ms=?`,
					first.OperationID, w.now().UnixMilli()+ipPersonRetryMS); got != 1 {
					t.Fatal("retry phase", got)
				}
			}
		})
	}
}

func TestIPPersonEvidenceBeginFreshReplayAndRecovery(t *testing.T) {
	w, r, p, base := ipPersonFixture(t)
	first, _ := ipPersonClaimed(t, w, r, p)
	ipPersonAdvance(t, w, base, time.Second)
	q := ipPersonBeginFor(t, first)
	identity := ipPersonIdentity(2, 0x61)
	turn, tx, b := ipPolicyTurn(t, w, r, IPPersonEvidenceBegin)
	out, err := applyIPPersonEvidenceBegin(t.Context(), turn, p, t.Context(), b, mustIPPersonBegin(t, identity, q))
	if err != nil || out.Outcome.State != Applied || out.Value.Kind != "begin" ||
		out.Value.EvidenceID != q.header.ID || out.Value.BuildID != q.buildID ||
		out.Value.SubjectKind != "person" || out.Value.Revision != first.Revision+1 ||
		out.Value.MaterializedRows != 1 || out.Value.ResultCount != 1 {
		t.Fatal(out, err)
	}
	ipPolicyCommitTurn(t, turn, tx)
	for query, want := range map[string]int64{
		`SELECT count(*) FROM metadata_evidence WHERE id='` + q.header.ID + `' AND subject_id=501 AND subject_kind='person' AND title='Ada Lovelace' AND biography='Mathematician.' AND birth_date='1815-12-10' AND death_date='1852-11-27' AND birth_place='London' AND sealed=0`:                                                  1,
		`SELECT count(*) FROM metadata_person_builds WHERE id='` + q.buildID + `' AND state='building' AND cursor=1 AND staged_rows=1 AND expected_rows=` + ipWritingInt(q.expectedRows) + ` AND expected_bytes=` + ipWritingInt(q.expectedBytes) + ` AND expected_commitment=X'` + ipPersonHexBytes(q.expectedCommitment[:]) + `'`: 1,
		`SELECT count(*) FROM metadata_person_receipts WHERE command_kind='begin' AND evidence_id='` + q.header.ID + `' AND build_id='` + q.buildID + `'`:                                                                                                                                                                           1,
	} {
		if got := ipPolicyQueryInt(t, w, query); got != want {
			t.Fatal(query, got)
		}
	}
	// Replay returns the exact saved BEGIN after fresh source changes.
	original := out
	ipPolicyExec(t, w, `UPDATE catalog_people SET revision=9 WHERE id=42`)
	ipPolicyExec(t, w, `UPDATE metadata_person_builds SET state='ready' WHERE id='`+q.buildID+`'`)
	ipPolicyExec(t, w, `UPDATE metadata_evidence SET title='changed' WHERE id='`+q.header.ID+`'`)
	turn, tx, b = ipPolicyTurn(t, w, r, IPPersonEvidenceBegin)
	replay, err := applyIPPersonEvidenceBegin(t.Context(), turn, p, t.Context(), b, mustIPPersonBegin(t, identity, q))
	if err != nil || !replay.Outcome.Replayed || replay.Value != original.Value || replay.Digest != original.Digest {
		t.Fatal("begin replay", replay, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
	// The exact build survives BEGIN ACK through the unique owner key only.
	ipPersonAck(t, w, first.OperationID, 2)
	turn, tx, b = ipPolicyTurn(t, w, r, IPPersonEvidenceBegin)
	retired, err := applyIPPersonEvidenceBegin(t.Context(), turn, p, t.Context(), b, mustIPPersonBegin(t, identity, q))
	if err != nil || retired.Outcome.State != ResultRetired || retired.Value != (ipPersonResult{}) {
		t.Fatal("retired begin served", retired, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
	recovery := func() ipPersonRecoveryResult {
		turn, tx, _ := ipPolicyTurn(t, w, r, IPPersonEvidenceBegin)
		got, err := ipPersonRecover(t.Context(), turn,
			ipPersonRecoveryRequest{ownerID: first.OperationID, incarnation: identity.Incarnation,
				personID: 42, token: "person-token-42"})
		if err != nil {
			t.Fatal(err)
		}
		ipPolicyRollbackTurn(t, turn, tx)
		return got
	}
	got := recovery()
	if !got.exists || got.terminal || got.buildID != q.buildID || got.evidenceID != q.header.ID ||
		got.generation != first.Generation || got.rows != q.expectedRows ||
		got.commitment != q.expectedCommitment {
		t.Fatal(got)
	}
	ipPolicyExec(t, w, `DELETE FROM metadata_person_receipts`)
	got = recovery()
	if !got.exists || got.buildID != q.buildID || got.commitment != q.expectedCommitment {
		t.Fatal("recovery after receipt reclamation", got)
	}
}

func TestIPPersonEvidenceBeginOneBuildAndExactNamespace(t *testing.T) {
	w, r, p, base := ipPersonFixture(t)
	first, _ := ipPersonClaimed(t, w, r, p)
	ipPersonAdvance(t, w, base, time.Second)
	q := ipPersonBeginFor(t, first)
	turn, tx, b := ipPolicyTurn(t, w, r, IPPersonEvidenceBegin)
	if _, err := applyIPPersonEvidenceBegin(t.Context(), turn, p, t.Context(), b,
		mustIPPersonBegin(t, ipPersonIdentity(2, 0x61), q)); err != nil {
		t.Fatal(err)
	}
	ipPolicyCommitTurn(t, turn, tx)
	ipPersonAck(t, w, first.OperationID, 2)
	// A second build under the same owner/incarnation is refused even with a
	// different build and evidence ID. Re-open the claimed phase so the failure
	// is the one-build index guard, not only the phase.
	ipPolicyExec(t, w, `UPDATE metadata_person_operations SET phase='claimed',revision=? WHERE id=?`, first.Revision+1, first.OperationID)
	second := q
	second.buildID = "build-person-2"
	second.header.ID = "evidence-person-2"
	second.header.Title = "Ada Lovelace second"
	header, err := NewEvidenceHeader(second.header)
	if err != nil {
		t.Fatal(err)
	}
	commitment, total, _, err := ipPersonEvidenceManifest(second, header)
	if err != nil {
		t.Fatal(err)
	}
	second.expectedCommitment, second.expectedBytes = commitment, total
	second.expectedRows = 1 + int64(second.children())
	second.revision = first.Revision + 1
	second.leaseGeneration = first.LeaseGeneration
	second.leaseUntilMS = first.LeaseUntilMS
	turn, tx, b = ipPolicyTurn(t, w, r, IPPersonEvidenceBegin)
	refused, err := applyIPPersonEvidenceBegin(t.Context(), turn, p, t.Context(), b,
		mustIPPersonBegin(t, ipPersonIdentity(3, 0x71), second))
	if refused != (ipPersonCommandResult{}) || !errors.Is(err, ErrConflict) {
		t.Fatal("second build admitted", refused, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
	if got := ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_person_builds`); got != 1 {
		t.Fatal("second build wrote", got)
	}
}

func TestIPPersonEvidenceBeginNamespacesAndBounds(t *testing.T) {
	w, r, p, base := ipPersonFixture(t)
	first, _ := ipPersonClaimed(t, w, r, p)
	ipPersonAdvance(t, w, base, time.Second)
	cases := map[string]func(*ipPersonEvidenceBeginRequest){
		"wrong_subject":          func(q *ipPersonEvidenceBeginRequest) { q.header.SubjectID = 99 },
		"wrong_provider":         func(q *ipPersonEvidenceBeginRequest) { q.header.Provider = "tvdb" },
		"wrong_subject_kind":     func(q *ipPersonEvidenceBeginRequest) { q.header.SubjectKind = "series" },
		"wrong_parser_domain":    func(q *ipPersonEvidenceBeginRequest) { q.header.ParserVersion = "en.movie.v1" },
		"parser_equals_shape":    func(q *ipPersonEvidenceBeginRequest) { q.header.ParserVersion = q.header.RequestShape },
		"missing_source_url":     func(q *ipPersonEvidenceBeginRequest) { q.header.SourceURL = "" },
		"zero_digest":            func(q *ipPersonEvidenceBeginRequest) { q.header.ObservationDigest = [32]byte{} },
		"aliased_movie_field":    func(q *ipPersonEvidenceBeginRequest) { q.header.OriginalTitle = "other" },
		"bad_birth_date":         func(q *ipPersonEvidenceBeginRequest) { q.birthDate.Value = "1815-13-10" },
		"unexpected_birth":       func(q *ipPersonEvidenceBeginRequest) { q.birthDate.Present = false },
		"alias_not_canonical":    func(q *ipPersonEvidenceBeginRequest) { q.aliases[0].CanonicalID = "99" },
		"external_kind":          func(q *ipPersonEvidenceBeginRequest) { q.external[0].SubjectKind = "series" },
		"expected_row_mismatch":  func(q *ipPersonEvidenceBeginRequest) { q.expectedRows++ },
		"expected_byte_mismatch": func(q *ipPersonEvidenceBeginRequest) { q.expectedBytes++ },
		"build_generation":       func(q *ipPersonEvidenceBeginRequest) { q.buildGeneration = 2 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			q := ipPersonBeginFor(t, first)
			mutate(&q)
			if _, err := newIPPersonEvidenceBeginCommand(ipPersonIdentity(2, 0x61), q); err == nil {
				t.Fatal("invalid begin admitted")
			}
			if got := ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_person_builds`); got != 0 {
				t.Fatal("constructor wrote", got)
			}
		})
	}
	// A body bound still refuses before any write.
	tooMany := ipPersonBeginFor(t, first)
	tooMany.aliases = make([]ipPersonEvidenceAlias, ipPersonMaxChildren+1)
	for i := range tooMany.aliases {
		tooMany.aliases[i] = ipPersonEvidenceAlias{RequestedID: "id", CanonicalID: "42", ObservedAt: "2026-01-01T00:00:00Z"}
	}
	if _, err := newIPPersonEvidenceBeginCommand(ipPersonIdentity(2, 0x61), tooMany); err == nil {
		t.Fatal("oversized child list admitted")
	}
	// A stale person revision after the claim is a runtime conflict, not a
	// constructor refusal.
	ipPolicyExec(t, w, `UPDATE catalog_people SET revision=2 WHERE id=42`)
	turn, tx, b := ipPolicyTurn(t, w, r, IPPersonEvidenceBegin)
	out, err := applyIPPersonEvidenceBegin(t.Context(), turn, p, t.Context(), b,
		mustIPPersonBegin(t, ipPersonIdentity(2, 0x61), ipPersonBeginFor(t, first)))
	if out != (ipPersonCommandResult{}) || !errors.Is(err, ErrConflict) {
		t.Fatal("stale person began a build", out, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
}

func TestIPPersonProviderAuthorityAndLifetime(t *testing.T) {
	prepare := func(t *testing.T, kind string) preparedIPPerson {
		w, r, p, base := ipPersonFixture(t)
		first, _ := ipPersonClaimed(t, w, r, p)
		ipPersonAdvance(t, w, base, time.Second)
		switch kind {
		case "claim":
			return preparedIPPerson{w, r, p, ipPersonClaimCommand(t, ipPersonIdentity(1, 0x31), nil), IPPersonClaim}
		case "renew":
			return preparedIPPerson{w, r, p, mustIPPersonRenew(t, ipPersonIdentity(2, 0x41), ipPersonRenewFor(first)), IPPersonRenew}
		case "finish":
			return preparedIPPerson{w, r, p, mustIPPersonFinish(t, ipPersonIdentity(2, 0x51), ipPersonFinishFor(first, "done")), IPPersonFinish}
		default:
			return preparedIPPerson{w, r, p, mustIPPersonBegin(t, ipPersonIdentity(2, 0x61), ipPersonBeginFor(t, first)), IPPersonEvidenceBegin}
		}
	}
	apply := func(ctx context.Context, purpose CommandID, turn *commandTurn, proof *usOwnerAuthorityProof,
		b ipPolicyBudget, c *ipPersonCommand) (ipPersonCommandResult, error) {
		switch purpose {
		case IPPersonClaim:
			return applyIPPersonClaim(ctx, turn, proof, ctx, b, c)
		case IPPersonRenew:
			return applyIPPersonRenew(ctx, turn, proof, ctx, b, c)
		case IPPersonFinish:
			return applyIPPersonFinish(ctx, turn, proof, ctx, b, c)
		default:
			return applyIPPersonEvidenceBegin(ctx, turn, proof, ctx, b, c)
		}
	}
	for _, mode := range []string{"revoked_family", "forged_proof", "revoked_membership"} {
		for _, kind := range []string{"claim", "renew", "finish", "begin"} {
			t.Run(kind+"_"+mode, func(t *testing.T) {
				state := prepare(t, kind)
				proof := ipPolicyRevoke(t, state.w, state.p, mode)
				turn, tx, b := ipPolicyTurn(t, state.w, state.r, state.purpose)
				out, err := apply(t.Context(), state.purpose, turn, proof, b, state.command)
				if out != (ipPersonCommandResult{}) || !errors.Is(err, ErrDisclosureDenied) {
					t.Fatal("stale current authority admitted", out, err)
				}
				ipPolicyRollbackTurn(t, turn, tx)
			})
		}
	}
	for _, kind := range []string{"claim", "renew", "finish", "begin"} {
		t.Run(kind+"_cancelled_lifetime", func(t *testing.T) {
			state := prepare(t, kind)
			lifetime, cancel := context.WithCancel(t.Context())
			cancel()
			turn, tx, b := ipPolicyTurn(t, state.w, state.r, state.purpose)
			out, err := apply(lifetime, state.purpose, turn, state.p, b, state.command)
			if out != (ipPersonCommandResult{}) || err == nil {
				t.Fatal("cancelled lifetime admitted", out, err)
			}
			ipPolicyRollbackTurn(t, turn, tx)
		})
	}
}

type preparedIPPerson struct {
	w       *executor
	r       *ipPolicyRegistry
	p       *usOwnerAuthorityProof
	command *ipPersonCommand
	purpose CommandID
}

func TestIPPersonUnknownCommitFreshRead(t *testing.T) {
	for _, kind := range []string{"claim", "renew", "finish", "begin"} {
		t.Run(kind, func(t *testing.T) {
			w, r, p, base := ipPersonFixture(t)
			var command *ipPersonCommand
			var purpose CommandID
			var owner string
			switch kind {
			case "claim":
				command = ipPersonClaimCommand(t, ipPersonIdentity(1, 0x31), nil)
				purpose = IPPersonClaim
				owner = ipPersonHex(ipPersonIdentity(1, 0x31).OperationID)
			case "renew":
				first, _ := ipPersonClaimed(t, w, r, p)
				ipPersonAdvance(t, w, base, time.Second)
				command = mustIPPersonRenew(t, ipPersonIdentity(2, 0x41), ipPersonRenewFor(first))
				purpose = IPPersonRenew
				owner = first.OperationID
			case "finish":
				first, _ := ipPersonClaimed(t, w, r, p)
				ipPersonAdvance(t, w, base, time.Second)
				command = mustIPPersonFinish(t, ipPersonIdentity(2, 0x51), ipPersonFinishFor(first, "done"))
				purpose = IPPersonFinish
				owner = first.OperationID
			default:
				first, _ := ipPersonClaimed(t, w, r, p)
				ipPersonAdvance(t, w, base, time.Second)
				command = mustIPPersonBegin(t, ipPersonIdentity(2, 0x61), ipPersonBeginFor(t, first))
				purpose = IPPersonEvidenceBegin
				owner = first.OperationID
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
			turn, tx, b := ipPolicyTurn(t, w, r, purpose)
			var out ipPersonCommandResult
			switch purpose {
			case IPPersonClaim:
				out, err = applyIPPersonClaim(t.Context(), turn, p, t.Context(), b, command)
			case IPPersonRenew:
				out, err = applyIPPersonRenew(t.Context(), turn, p, t.Context(), b, command)
			case IPPersonFinish:
				out, err = applyIPPersonFinish(t.Context(), turn, p, t.Context(), b, command)
			default:
				out, err = applyIPPersonEvidenceBegin(t.Context(), turn, p, t.Context(), b, command)
			}
			if err != nil || out.Outcome.State != Applied {
				t.Fatal(out, err)
			}
			result, err := tx.commit()
			if result.state != commitUnknown || !errors.Is(err, ErrResolveRequired) {
				t.Fatal(result, err)
			}
			applyAgain := func() (ipPersonCommandResult, error) {
				switch purpose {
				case IPPersonClaim:
					return applyIPPersonClaim(t.Context(), turn, p, t.Context(), b, command)
				case IPPersonRenew:
					return applyIPPersonRenew(t.Context(), turn, p, t.Context(), b, command)
				case IPPersonFinish:
					return applyIPPersonFinish(t.Context(), turn, p, t.Context(), b, command)
				default:
					return applyIPPersonEvidenceBegin(t.Context(), turn, p, t.Context(), b, command)
				}
			}
			if again, e := applyAgain(); again != (ipPersonCommandResult{}) || e == nil {
				t.Fatal("uncertain frame reserved", again, e)
			}
			switch kind {
			case "claim":
				if got := ipPolicyFreshInt(t, path, `SELECT count(*) FROM metadata_person_receipts WHERE owner_id='`+owner+`' AND command_kind='begin' AND evidence_id IS NULL`); got != 1 {
					t.Fatal("durable claim receipt", got)
				}
				if got := ipPolicyFreshInt(t, path, `SELECT count(*) FROM metadata_person_operations WHERE id='`+owner+`' AND phase='claimed' AND last_command_sequence=1`); got != 1 {
					t.Fatal("durable claim owner", got)
				}
			case "renew":
				if got := ipPolicyFreshInt(t, path, `SELECT count(*) FROM metadata_person_receipts WHERE owner_id='`+owner+`' AND command_kind='renew'`); got != 1 {
					t.Fatal("durable renew receipt", got)
				}
				if got := ipPolicyFreshInt(t, path, `SELECT revision FROM metadata_person_operations WHERE id='`+owner+`'`); got != 2 {
					t.Fatal("durable renew revision", got)
				}
			case "finish":
				if got := ipPolicyFreshInt(t, path, `SELECT count(*) FROM metadata_person_receipts WHERE owner_id='`+owner+`' AND command_kind='retry'`); got != 1 {
					t.Fatal("durable finish receipt", got)
				}
				if got := ipPolicyFreshInt(t, path, `SELECT count(*) FROM metadata_person_operations WHERE id='`+owner+`' AND phase='done'`); got != 1 {
					t.Fatal("durable finish phase", got)
				}
			case "begin":
				if got := ipPolicyFreshInt(t, path, `SELECT count(*) FROM metadata_person_receipts WHERE owner_id='`+owner+`' AND command_kind='begin' AND evidence_id IS NOT NULL`); got != 1 {
					t.Fatal("durable begin receipt", got)
				}
				if got := ipPolicyFreshInt(t, path, `SELECT count(*) FROM metadata_person_builds WHERE owner_id='`+owner+`' AND state='building'`); got != 1 {
					t.Fatal("durable begin build", got)
				}
			}
		})
	}
}

func ipWritingInt(v int64) string { return strconv.FormatInt(v, 10) }

func ipPersonHexBytes(v []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, len(v)*2)
	for n, b := range v {
		out[n*2], out[n*2+1] = digits[b>>4], digits[b&0x0f]
	}
	return string(out)
}
