package writer

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

// Group D is embedded, so the acquisition receipt header is qualified against
// the canonical schema. The provider operation, subject binding and acquisition
// job are the real embedded owners.
func ipEntityJobFixture(t *testing.T) (*executor, *ipPolicyRegistry, *usOwnerAuthorityProof, time.Time) {
	t.Helper()
	w, keeper, p := ipPolicySecurityFixture(t)
	base := w.now()
	for _, query := range []string{
		`INSERT INTO catalog_kinds(id,name,playable,container,searchable,browsable,listening) VALUES(1,'movie',1,0,1,1,0)`,
		`INSERT INTO catalog_libraries(id,library_id,root) VALUES(777,'ip-library','/media')`,
		`INSERT INTO metadata_providers(code,adapter,enabled) VALUES('tmdb','tmdb',1),('tvdb','tvdb',1),('musicbrainz','musicbrainz',1)`,
		`INSERT INTO metadata_provider_policy(library_id,provider,incarnation,revision,enabled,priority,region,algorithm,refresh_mode,credit_enabled,credit_after_item,credit_after_provider,command_incarnation) VALUES(777,'tmdb',X'09000000000000000000000000000000',1,1,5,'US','v1','fill_missing',1,NULL,NULL,X'01000000000000000000000000000000'),(777,'tvdb',X'09000000000000000000000000000000',1,1,5,'US','v1','fill_missing',1,NULL,NULL,X'01000000000000000000000000000000'),(777,'musicbrainz',X'0a000000000000000000000000000000',1,1,5,'US','v1','fill_missing',1,NULL,NULL,X'01000000000000000000000000000000')`,
		`INSERT INTO screen_metadata_consent(singleton,revision,confirmed) VALUES(1,1,1) ON CONFLICT(singleton) DO UPDATE SET revision=1,confirmed=1`,
		`INSERT INTO metadata_runtime_settings(singleton,online_lookups,lookup_epoch) VALUES(1,1,3) ON CONFLICT(singleton) DO UPDATE SET online_lookups=1,lookup_epoch=3`,
	} {
		if _, err := w.db.ExecContext(t.Context(), query); err != nil {
			t.Fatal(query, err)
		}
	}
	defs := append(ipPolicyDefinitions(), ipEntityJobDefinitions()...)
	r, err := stmttest.New(t, defs)
	if err != nil {
		t.Fatal(err)
	}
	return w, &ipPolicyRegistry{r, keeper}, p, base
}

func ipEntityJobAdvance(t *testing.T, w *executor, base time.Time, d time.Duration) {
	t.Helper()
	at := base.Add(d)
	w.now = func() time.Time { return at }
}

func ipEntityJobIncarnation(id int64) [16]byte {
	var inc [16]byte
	inc[0] = 0x0b
	binary.BigEndian.PutUint64(inc[8:], uint64(id))
	return inc
}

func ipEntityJobEntity(t *testing.T, w *executor, id int64) {
	t.Helper()
	public := make([]byte, 16)
	binary.BigEndian.PutUint64(public[8:], uint64(id))
	inc := ipEntityJobIncarnation(id)
	ipPolicyExec(t, w, `INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(?,?,777,1)`, id, public)
	ipPolicyExec(t, w, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation,source_revision,identity_revision,metadata_revision) VALUES(?,?,777,1,?,'s','s','s','s',1,?,?,1,1,1)`, id, public, fmt.Sprintf("Entity %d", id), inc[:], inc[:])
}

type ipEntityJobSeedOptions struct {
	provider, purpose, status, selected, lease string
	generation, revision                       int64
	nextAttempt, leaseUntil, attempts          int64
}

func ipEntityJobSeed(t *testing.T, w *executor, id int64, o ipEntityJobSeedOptions) {
	t.Helper()
	ipEntityJobEntity(t, w, id)
	if o.provider == "" {
		o.provider = "tmdb"
	}
	if o.purpose == "" {
		o.purpose = "metadata"
	}
	if o.status == "" {
		o.status = "pending"
	}
	if o.selected == "" {
		o.selected = fmt.Sprintf("%s-%d", o.provider, id)
	}
	if o.generation == 0 {
		o.generation = 1
	}
	if o.revision == 0 {
		o.revision = 1
	}
	ipPolicyExec(t, w, `INSERT INTO metadata_acquisition_jobs(entity_id,provider,purpose,generation,revision,status,selected_id,manual,review_search,query_override,year_override,force_refresh,episode_order,parent_publication,attempts,next_attempt_ms,lease,lease_until_ms,error,page,after_item,staged_bytes,observed_at) VALUES(?,?,?,?,?,?,?,0,0,'',0,0,'','',?,?,?,?,'',0,'',0,'2026-01-01T00:00:00.000000000Z')`,
		id, o.provider, o.purpose, o.generation, o.revision, o.status, o.selected, o.attempts, o.nextAttempt, o.lease, o.leaseUntil)
}

func ipEntityJobIdentity(seed byte, sequence int64, operation byte) CommandIdentity {
	var inc, op [16]byte
	inc[0] = seed
	inc[1] = 0x5a
	op[0] = operation
	op[1] = 0x77
	return CommandIdentity{Incarnation: inc, Sequence: sequence, OperationID: op}
}

func ipEntityJobClaimFor(id int64, provider string) ipEntityJobClaimRequest {
	policyIncarnation := [16]byte{9}
	if provider == "musicbrainz" {
		policyIncarnation[0] = 0x0a
	}
	return ipEntityJobClaimRequest{entityID: id, libraryID: 777, entityIncarnation: ipEntityJobIncarnation(id),
		libraryKey: "ip-library", provider: provider, purpose: "metadata", role: ipEntityJobRole,
		selectedID: fmt.Sprintf("%s-%d", provider, id), jobGeneration: 1, jobRevision: 1,
		entityRevision: 1, sourceRevision: 1, identityRevision: 1, metadataRevision: 1,
		policyIncarnation: policyIncarnation, policyRevision: 1, consentRevision: 1}
}

func ipEntityJobClaimCommand(t *testing.T, identity CommandIdentity, mutate func(*ipEntityJobClaimRequest)) *ipEntityJobCommand {
	t.Helper()
	q := ipEntityJobClaimFor(1, "tmdb")
	if mutate != nil {
		mutate(&q)
	}
	c, err := newIPEntityClaimCommand(identity, q)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func ipEntityJobApplyClaim(t *testing.T, w *executor, r *ipPolicyRegistry, p *usOwnerAuthorityProof, c *ipEntityJobCommand) (ipEntityJobCommandResult, *commandTurn, *transaction, ipPolicyBudget) {
	t.Helper()
	turn, tx, b := ipPolicyTurn(t, w, r, IPEntityClaim)
	out, err := applyIPEntityClaim(t.Context(), turn, p, t.Context(), b, c)
	if err != nil {
		t.Fatal("claim", out, err)
	}
	return out, turn, tx, b
}

func ipEntityJobRenewFor(v ipEntityJobResult) ipEntityJobRenewRequest {
	return ipEntityJobRenewRequest{operationID: v.OperationID, provider: v.Provider, purpose: v.Purpose,
		selectedID: v.SelectedID, lease: v.Lease, entityID: v.EntityID, generation: 1,
		jobGeneration: v.Generation, jobRevision: v.Revision, leaseUntilMS: v.LeaseUntilMS}
}

func ipEntityJobFinishFor(v ipEntityJobResult) ipEntityJobFinishRequest {
	return ipEntityJobFinishRequest{operationID: v.OperationID, provider: v.Provider, purpose: v.Purpose,
		selectedID: v.SelectedID, lease: v.Lease, entityID: v.EntityID, generation: 1,
		jobGeneration: v.Generation, jobRevision: v.Revision, leaseUntilMS: v.LeaseUntilMS, outcome: "done"}
}

func ipEntityJobAck(t *testing.T, w *executor, v ipEntityJobResult, sequence int64) {
	t.Helper()
	ipPolicyExec(t, w, `UPDATE metadata_provider_operations SET last_command_sequence=?,acknowledged_command_sequence=? WHERE id=? AND provider=?`,
		sequence, sequence, v.OperationID, v.Provider)
}

// ipEntityJobClaimed seeds one due subject, claims it and acknowledges sequence 1.
func ipEntityJobClaimed(t *testing.T, w *executor, r *ipPolicyRegistry, p *usOwnerAuthorityProof, id int64, provider string) (ipEntityJobResult, CommandIdentity) {
	t.Helper()
	ipEntityJobSeed(t, w, id, ipEntityJobSeedOptions{provider: provider})
	identity := ipEntityJobIdentity(0x21, 1, 0x31)
	q := ipEntityJobClaimFor(id, provider)
	if provider == "musicbrainz" {
		q.consentRevision = 3
	}
	c, err := newIPEntityClaimCommand(identity, q)
	if err != nil {
		t.Fatal(err)
	}
	out, turn, tx, _ := ipEntityJobApplyClaim(t, w, r, p, c)
	if out.Outcome.State != Applied {
		t.Fatal(out)
	}
	ipPolicyCommitTurn(t, turn, tx)
	ipEntityJobAck(t, w, out.Value, 1)
	return out.Value, identity
}

func TestIPEntityJobClaimFreshReplayAndRetired(t *testing.T) {
	w, r, p, _ := ipEntityJobFixture(t)
	ipEntityJobSeed(t, w, 1, ipEntityJobSeedOptions{})
	identity := ipEntityJobIdentity(0x21, 1, 0x31)
	out, turn, tx, _ := ipEntityJobApplyClaim(t, w, r, p, ipEntityJobClaimCommand(t, identity, nil))
	if out.Outcome.State != Applied || out.Outcome.Replayed || out.Value.Kind != "claim" ||
		out.Value.OperationID != ipEntityJobHex(identity.OperationID) ||
		out.Value.Lease != ipEntityJobHex(identity.Incarnation) ||
		out.Value.Generation != 2 || out.Value.Revision != 2 ||
		out.Value.LeaseUntilMS != w.now().UnixMilli()+ipEntityJobLeaseMS ||
		out.Value.AppliedMS != w.now().UnixMilli() || out.Digest == (ResultDigest{}) {
		t.Fatal(out)
	}
	ipPolicyCommitTurn(t, turn, tx)
	for query, want := range map[string]int64{
		`SELECT count(*) FROM metadata_provider_operations WHERE id='` + out.Value.OperationID + `' AND provider='tmdb' AND status='claimed' AND generation=1 AND last_command_sequence=1 AND acknowledged_command_sequence=0`: 1,
		`SELECT count(*) FROM metadata_provider_operations WHERE entity_id=1 AND policy_revision=1 AND consent_revision=1 AND selected_id='tmdb-1'`:                                                                            1,
		`SELECT count(*) FROM metadata_operation_subjects WHERE operation_id='` + out.Value.OperationID + `' AND provider='tmdb' AND role='primary' AND entity_id=1 AND revision=1`:                                            1,
		`SELECT count(*) FROM metadata_acquisition_jobs WHERE entity_id=1 AND status='claimed' AND generation=2 AND revision=2 AND attempts=1 AND error='' AND lease='` + out.Value.Lease + `'`:                                1,
		`SELECT count(*) FROM metadata_acquisition_jobs WHERE entity_id=1 AND observed_at='2026-01-01T00:00:00.000000000Z'`:                                                                                                    1,
		`SELECT count(*) FROM metadata_ip_receipts WHERE command_kind='claim' AND accepted_revision=2 AND result_phase='claimed' AND terminal=0`:                                                                               1,
	} {
		if got := ipPolicyQueryInt(t, w, query); got != want {
			t.Fatal(query, got)
		}
	}
	// Replay after unrelated lease/source changes returns the same saved result
	// and writes nothing.
	original := out
	ipPolicyExec(t, w, `UPDATE metadata_acquisition_jobs SET status='pending',generation=9,revision=9,lease='other',lease_until_ms=1000000,observed_at='changed' WHERE entity_id=1`)
	ipPolicyExec(t, w, `UPDATE catalog_entities SET metadata_revision=5,revision=5 WHERE id=1`)
	ipPolicyExec(t, w, `UPDATE metadata_providers SET enabled=0 WHERE code='tmdb'`)
	turn, tx, b := ipPolicyTurn(t, w, r, IPEntityClaim)
	replay, err := applyIPEntityClaim(t.Context(), turn, p, t.Context(), b, ipEntityJobClaimCommand(t, identity, nil))
	if err != nil || !replay.Outcome.Replayed || replay.Value != original.Value || replay.Digest != original.Digest {
		t.Fatal("claim replay after source change", replay, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
	if got := ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_ip_receipts`); got != 1 {
		t.Fatal("replay wrote receipts", got)
	}
	if got := ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_acquisition_jobs WHERE entity_id=1 AND status='pending' AND lease='other'`); got != 1 {
		t.Fatal("replay touched the job", got)
	}
	// At or below ACK is Retired even while the receipt remains physical.
	ipPolicyExec(t, w, `UPDATE metadata_provider_operations SET acknowledged_command_sequence=1 WHERE id='`+original.Value.OperationID+`'`)
	turn, tx, b = ipPolicyTurn(t, w, r, IPEntityClaim)
	retired, err := applyIPEntityClaim(t.Context(), turn, p, t.Context(), b, ipEntityJobClaimCommand(t, identity, nil))
	if err != nil || retired.Outcome.State != ResultRetired || retired.Value != (ipEntityJobResult{}) || retired.Digest != (ResultDigest{}) {
		t.Fatal("retired claim served", retired, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
}

func TestIPEntityJobClaimSelectedRoleAndExactOwner(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*ipEntityJobClaimRequest)
		sql    []string
		want   error
	}{
		{"wrong_role", func(q *ipEntityJobClaimRequest) { q.role = "secondary" }, nil, nil},
		{"wrong_selected", func(q *ipEntityJobClaimRequest) { q.selectedID = "tmdb-9" }, nil, ErrConflict},
		{"stale_entity_revision", func(q *ipEntityJobClaimRequest) { q.metadataRevision = 2 }, nil, ErrConflict},
		{"stale_source_revision", func(q *ipEntityJobClaimRequest) { q.sourceRevision = 2 }, nil, ErrConflict},
		{"stale_policy", func(q *ipEntityJobClaimRequest) { q.policyRevision = 2 }, nil, ErrConflict},
		{"wrong_library", func(q *ipEntityJobClaimRequest) { q.libraryKey = "other-library" }, nil, ErrDisclosureDenied},
		{"wrong_generation", func(q *ipEntityJobClaimRequest) { q.jobGeneration = 2 }, nil, ErrConflict},
		{"wrong_revision", func(q *ipEntityJobClaimRequest) { q.jobRevision = 2 }, nil, ErrConflict},
		{"not_pending", nil, []string{`UPDATE metadata_acquisition_jobs SET status='matched' WHERE entity_id=1`}, ErrConflict},
		{"not_due", nil, []string{`UPDATE metadata_acquisition_jobs SET next_attempt_ms=9999999999999 WHERE entity_id=1`}, ErrConflict},
		{"live_lease", nil, []string{`UPDATE metadata_acquisition_jobs SET lease='other-worker',lease_until_ms=9999999999999 WHERE entity_id=1`}, ErrConflict},
		{"retired_entity", nil, []string{`UPDATE catalog_entities SET retired=1 WHERE id=1`}, ErrDisclosureDenied},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w, r, p, _ := ipEntityJobFixture(t)
			ipEntityJobSeed(t, w, 1, ipEntityJobSeedOptions{})
			for _, query := range tc.sql {
				ipPolicyExec(t, w, query)
			}
			identity := ipEntityJobIdentity(0x21, 1, 0x31)
			q := ipEntityJobClaimFor(1, "tmdb")
			if tc.mutate != nil {
				tc.mutate(&q)
			}
			c, err := newIPEntityClaimCommand(identity, q)
			if tc.want == nil {
				if c != nil || !errors.Is(err, ErrInvalid) {
					t.Fatal(c, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			turn, tx, b := ipPolicyTurn(t, w, r, IPEntityClaim)
			out, err := applyIPEntityClaim(t.Context(), turn, p, t.Context(), b, c)
			if out != (ipEntityJobCommandResult{}) || !errors.Is(err, tc.want) {
				t.Fatal("ineligible claim admitted", out, err)
			}
			ipPolicyRollbackTurn(t, turn, tx)
			for _, query := range []string{
				`SELECT count(*) FROM metadata_provider_operations`,
				`SELECT count(*) FROM metadata_operation_subjects`,
				`SELECT count(*) FROM metadata_ip_receipts`,
			} {
				if got := ipPolicyQueryInt(t, w, query); got != 0 {
					t.Fatal(query, got)
				}
			}
			if got := ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_acquisition_jobs WHERE status='claimed'`); got != 0 {
				t.Fatal("denied claim leased the job", got)
			}
		})
	}
	t.Run("failed_claim_retries_after_expired_lease", func(t *testing.T) {
		w, r, p, base := ipEntityJobFixture(t)
		ipEntityJobSeed(t, w, 1, ipEntityJobSeedOptions{})
		first, turn, tx, _ := ipEntityJobApplyClaim(t, w, r, p, ipEntityJobClaimCommand(t, ipEntityJobIdentity(0x21, 1, 0x31), nil))
		ipPolicyCommitTurn(t, turn, tx)
		ipEntityJobAck(t, w, first.Value, 1)
		ipEntityJobAdvance(t, w, base, 6*time.Minute)
		// The expired lease still consumes exactly one new generation for the
		// replacement worker; the old owner can no longer renew or finish.
		second, turn, tx, _ := ipEntityJobApplyClaim(t, w, r, p, ipEntityJobClaimCommand(t, ipEntityJobIdentity(0x22, 1, 0x32), func(q *ipEntityJobClaimRequest) {
			q.jobGeneration, q.jobRevision = 2, 2
		}))
		if second.Value.Generation != 3 || second.Value.Revision != 3 || second.Value.Lease == first.Value.Lease {
			t.Fatal("replacement claim", second)
		}
		ipPolicyCommitTurn(t, turn, tx)
		if got := ipPolicyQueryInt(t, w, `SELECT attempts FROM metadata_acquisition_jobs WHERE entity_id=1`); got != 2 {
			t.Fatal("lease retry attempts", got)
		}
		old := ipEntityJobRenewFor(first.Value)
		turn, tx, b := ipPolicyTurn(t, w, r, IPEntityRenew)
		out, err := applyIPEntityRenew(t.Context(), turn, p, t.Context(), b, mustRenew(t, ipEntityJobIdentity(0x21, 2, 0x41), old))
		if out != (ipEntityJobCommandResult{}) || !errors.Is(err, ErrConflict) {
			t.Fatal("lost lease renewed", out, err)
		}
		ipPolicyRollbackTurn(t, turn, tx)
	})
}

func mustRenew(t *testing.T, identity CommandIdentity, q ipEntityJobRenewRequest) *ipEntityJobCommand {
	t.Helper()
	c, err := newIPEntityRenewCommand(identity, q)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func mustFinish(t *testing.T, identity CommandIdentity, q ipEntityJobFinishRequest) *ipEntityJobCommand {
	t.Helper()
	c, err := newIPEntityFinishCommand(identity, q)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestIPEntityJobConsentDomain(t *testing.T) {
	cases := []struct {
		name     string
		provider string
		consent  int64
		sql      []string
		want     error
	}{
		{"screen_revision", "tmdb", 2, nil, ErrDisclosureDenied},
		{"screen_unconfirmed", "tmdb", 1, []string{`UPDATE screen_metadata_consent SET confirmed=0`}, ErrDisclosureDenied},
		{"music_uses_online_lookups", "musicbrainz", 3, nil, nil},
		{"music_does_not_borrow_screen_revision", "musicbrainz", 1, nil, ErrDisclosureDenied},
		{"music_offline", "musicbrainz", 3, []string{`UPDATE metadata_runtime_settings SET online_lookups=0`}, ErrDisclosureDenied},
		{"screen_does_not_borrow_lookup_epoch", "tmdb", 3, nil, ErrDisclosureDenied},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w, r, p, _ := ipEntityJobFixture(t)
			ipEntityJobSeed(t, w, 1, ipEntityJobSeedOptions{provider: tc.provider})
			for _, query := range tc.sql {
				ipPolicyExec(t, w, query)
			}
			q := ipEntityJobClaimFor(1, tc.provider)
			q.consentRevision = tc.consent
			c, err := newIPEntityClaimCommand(ipEntityJobIdentity(0x21, 1, 0x31), q)
			if err != nil {
				t.Fatal(err)
			}
			turn, tx, b := ipPolicyTurn(t, w, r, IPEntityClaim)
			out, err := applyIPEntityClaim(t.Context(), turn, p, t.Context(), b, c)
			if tc.want == nil {
				if err != nil || out.Outcome.State != Applied {
					t.Fatal(out, err)
				}
				ipPolicyCommitTurn(t, turn, tx)
				return
			}
			if out != (ipEntityJobCommandResult{}) || !errors.Is(err, tc.want) {
				t.Fatal("consent domain admitted", out, err)
			}
			ipPolicyRollbackTurn(t, turn, tx)
			if got := ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_provider_operations`); got != 0 {
				t.Fatal("denied consent wrote an operation", got)
			}
		})
	}
}

func TestIPEntityJobProviderNamespace(t *testing.T) {
	w, r, p, _ := ipEntityJobFixture(t)
	ipEntityJobSeed(t, w, 1, ipEntityJobSeedOptions{})
	first, turn, tx, _ := ipEntityJobApplyClaim(t, w, r, p, ipEntityJobClaimCommand(t, ipEntityJobIdentity(0x21, 1, 0x31), nil))
	ipPolicyCommitTurn(t, turn, tx)
	ipEntityJobAck(t, w, first.Value, 1)
	// The same operation ID is not visible under another provider: the owner
	// lookup is the provider namespace, and a restore/accept for TVDB cannot
	// borrow the TMDB claim.
	renamed := ipEntityJobRenewFor(first.Value)
	renamed.provider = "tvdb"
	turn, tx, b := ipPolicyTurn(t, w, r, IPEntityRenew)
	out, err := applyIPEntityRenew(t.Context(), turn, p, t.Context(), b, mustRenew(t, ipEntityJobIdentity(0x21, 2, 0x41), renamed))
	if out != (ipEntityJobCommandResult{}) || !errors.Is(err, ErrResolveRequired) {
		t.Fatal("cross-provider owner served", out, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
	// A second entity cannot reuse the same operation ID under the same provider.
	ipEntityJobSeed(t, w, 2, ipEntityJobSeedOptions{})
	q := ipEntityJobClaimFor(2, "tmdb")
	c, err := newIPEntityClaimCommand(ipEntityJobIdentity(0x23, 1, 0x31), q)
	if err != nil {
		t.Fatal(err)
	}
	turn, tx, b = ipPolicyTurn(t, w, r, IPEntityClaim)
	dup, err := applyIPEntityClaim(t.Context(), turn, p, t.Context(), b, c)
	if dup != (ipEntityJobCommandResult{}) || err == nil {
		t.Fatal("duplicate provider operation ID admitted", dup, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
	if got := ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_provider_operations`); got != 1 {
		t.Fatal("duplicate operation", got)
	}
}

func TestIPEntityJobRenewExtendsLeaseAndReplays(t *testing.T) {
	w, r, p, base := ipEntityJobFixture(t)
	first, _ := ipEntityJobClaimed(t, w, r, p, 1, "tmdb")
	ipEntityJobAdvance(t, w, base, time.Minute)
	renew := ipEntityJobRenewFor(first)
	out, turn, tx, _ := ipEntityJobApplyRenew(t, w, r, p, mustRenew(t, ipEntityJobIdentity(0x21, 2, 0x41), renew))
	if out.Outcome.State != Applied || out.Value.Kind != "renew" || out.Value.Revision != 3 ||
		out.Value.Generation != 2 || out.Value.LeaseUntilMS != w.now().UnixMilli()+ipEntityJobLeaseMS ||
		out.Value.Lease != first.Lease || out.Digest == (ResultDigest{}) {
		t.Fatal(out)
	}
	ipPolicyCommitTurn(t, turn, tx)
	for query, want := range map[string]int64{
		`SELECT lease_until_ms FROM metadata_acquisition_jobs WHERE entity_id=1`: out.Value.LeaseUntilMS,
		`SELECT revision FROM metadata_acquisition_jobs WHERE entity_id=1`:       3,
	} {
		if got := ipPolicyQueryInt(t, w, query); got != want {
			t.Fatal(query, got)
		}
	}
	if got := ipPolicyString(t, w, `SELECT lease_until FROM metadata_provider_operations WHERE id='`+first.OperationID+`'`); got != ipEntityJobTimeText(out.Value.LeaseUntilMS) {
		t.Fatal("owner lease", got)
	}
	if got := ipPolicyQueryInt(t, w, `SELECT last_command_sequence FROM metadata_provider_operations WHERE id='`+first.OperationID+`'`); got != 2 {
		t.Fatal("owner sequence", got)
	}
	if got := ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_ip_receipts WHERE command_kind='renew'`); got != 1 {
		t.Fatal("renew receipt", got)
	}
	// The original renew result replays before any fresh lease or source probe.
	ipPolicyExec(t, w, `UPDATE metadata_acquisition_jobs SET revision=9,lease='other',lease_until_ms=1,status='pending' WHERE entity_id=1`)
	ipPolicyExec(t, w, `UPDATE catalog_entities SET metadata_revision=7 WHERE id=1`)
	turn, tx, b := ipPolicyTurn(t, w, r, IPEntityRenew)
	replay, err := applyIPEntityRenew(t.Context(), turn, p, t.Context(), b, mustRenew(t, ipEntityJobIdentity(0x21, 2, 0x41), renew))
	if err != nil || !replay.Outcome.Replayed || replay.Value != out.Value || replay.Digest != out.Digest {
		t.Fatal("renew replay after fresh changes", replay, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
	if got := ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_ip_receipts`); got != 2 {
		t.Fatal("replay wrote receipts", got)
	}
	ipEntityJobAck(t, w, out.Value, 2)
	turn, tx, b = ipPolicyTurn(t, w, r, IPEntityRenew)
	retired, err := applyIPEntityRenew(t.Context(), turn, p, t.Context(), b, mustRenew(t, ipEntityJobIdentity(0x21, 2, 0x41), renew))
	if err != nil || retired.Outcome.State != ResultRetired || retired.Value != (ipEntityJobResult{}) {
		t.Fatal("retired renew served", retired, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
}

func ipEntityJobApplyRenew(t *testing.T, w *executor, r *ipPolicyRegistry, p *usOwnerAuthorityProof, c *ipEntityJobCommand) (ipEntityJobCommandResult, *commandTurn, *transaction, ipPolicyBudget) {
	t.Helper()
	turn, tx, b := ipPolicyTurn(t, w, r, IPEntityRenew)
	out, err := applyIPEntityRenew(t.Context(), turn, p, t.Context(), b, c)
	if err != nil {
		t.Fatal("renew", out, err)
	}
	return out, turn, tx, b
}

func TestIPEntityJobRenewRejectsStaleOwner(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*ipEntityJobRenewRequest)
		sql    []string
		want   error
	}{
		{"wrong_lease", func(q *ipEntityJobRenewRequest) { q.lease = strings.Repeat("f", 32) }, nil, ErrConflict},
		{"wrong_generation", func(q *ipEntityJobRenewRequest) { q.generation = 2 }, nil, ErrConflict},
		{"wrong_job_revision", func(q *ipEntityJobRenewRequest) { q.jobRevision = 3 }, nil, ErrConflict},
		{"wrong_selected", func(q *ipEntityJobRenewRequest) { q.selectedID = "tmdb-9" }, nil, ErrConflict},
		{"new_observation", nil, []string{`UPDATE catalog_entities SET metadata_revision=2,revision=2 WHERE id=1`}, ErrConflict},
		{"new_source_revision", nil, []string{`UPDATE catalog_entities SET source_revision=2 WHERE id=1`}, ErrConflict},
		{"policy_advanced", nil, []string{`UPDATE metadata_provider_policy SET revision=2 WHERE library_id=777 AND provider='tmdb'`}, ErrConflict},
		{"consent_advanced", nil, []string{`UPDATE screen_metadata_consent SET revision=2 WHERE singleton=1`}, ErrDisclosureDenied},
		{"owner_not_claimed", nil, []string{`UPDATE metadata_provider_operations SET status='staged' WHERE provider='tmdb'`}, ErrConflict},
		{"lease_expired", nil, []string{`UPDATE metadata_acquisition_jobs SET lease_until_ms=1 WHERE entity_id=1`}, ErrConflict},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w, r, p, _ := ipEntityJobFixture(t)
			first, _ := ipEntityJobClaimed(t, w, r, p, 1, "tmdb")
			for _, query := range tc.sql {
				ipPolicyExec(t, w, query)
			}
			q := ipEntityJobRenewFor(first)
			if tc.mutate != nil {
				tc.mutate(&q)
			}
			c, err := newIPEntityRenewCommand(ipEntityJobIdentity(0x21, 2, 0x41), q)
			if err != nil {
				t.Fatal(err)
			}
			turn, tx, b := ipPolicyTurn(t, w, r, IPEntityRenew)
			out, err := applyIPEntityRenew(t.Context(), turn, p, t.Context(), b, c)
			if out != (ipEntityJobCommandResult{}) || !errors.Is(err, tc.want) {
				t.Fatal("stale owner renewed", out, err)
			}
			ipPolicyRollbackTurn(t, turn, tx)
			if got := ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_ip_receipts`); got != 1 {
				t.Fatal("refused renew wrote receipts", got)
			}
			if got := ipPolicyQueryInt(t, w, `SELECT revision FROM metadata_acquisition_jobs WHERE entity_id=1`); got != 2 {
				t.Fatal("refused renew advanced the job", got)
			}
		})
	}
	t.Run("expired_at_renew_time", func(t *testing.T) {
		w, r, p, base := ipEntityJobFixture(t)
		first, _ := ipEntityJobClaimed(t, w, r, p, 1, "tmdb")
		ipEntityJobAdvance(t, w, base, 6*time.Minute)
		turn, tx, b := ipPolicyTurn(t, w, r, IPEntityRenew)
		out, err := applyIPEntityRenew(t.Context(), turn, p, t.Context(), b, mustRenew(t, ipEntityJobIdentity(0x21, 2, 0x41), ipEntityJobRenewFor(first)))
		if out != (ipEntityJobCommandResult{}) || !errors.Is(err, ErrConflict) {
			t.Fatal("expired lease renewed", out, err)
		}
		ipPolicyRollbackTurn(t, turn, tx)
	})
	t.Run("rollback_keeps_owner", func(t *testing.T) {
		w, r, p, _ := ipEntityJobFixture(t)
		first, _ := ipEntityJobClaimed(t, w, r, p, 1, "tmdb")
		turn, tx, b := ipPolicyTurn(t, w, r, IPEntityRenew)
		if out, err := applyIPEntityRenew(t.Context(), turn, p, t.Context(), b, mustRenew(t, ipEntityJobIdentity(0x21, 2, 0x41), ipEntityJobRenewFor(first))); err != nil || out.Outcome.State != Applied {
			t.Fatal(out, err)
		}
		ipPolicyRollbackTurn(t, turn, tx)
		for query, want := range map[string]int64{
			`SELECT revision FROM metadata_acquisition_jobs WHERE entity_id=1`:                                    2,
			`SELECT last_command_sequence FROM metadata_provider_operations WHERE id='` + first.OperationID + `'`: 1,
			`SELECT count(*) FROM metadata_ip_receipts`:                                                           1,
		} {
			if got := ipPolicyQueryInt(t, w, query); got != want {
				t.Fatal(query, got)
			}
		}
	})
}

func TestIPEntityJobFinishDoneRetryAndReplay(t *testing.T) {
	t.Run("done", func(t *testing.T) {
		w, r, p, base := ipEntityJobFixture(t)
		first, _ := ipEntityJobClaimed(t, w, r, p, 1, "tmdb")
		ipEntityJobAdvance(t, w, base, time.Minute)
		before := map[string]int64{
			"evidence": ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_evidence`),
			"builds":   ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_evidence_builds`),
			"publics":  ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_publications`),
			"subjects": ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_subject_providers`),
		}
		q := ipEntityJobFinishFor(first)
		out, turn, tx, _ := ipEntityJobApplyFinish(t, w, r, p, mustFinish(t, ipEntityJobIdentity(0x21, 2, 0x51), q))
		if out.Outcome.State != Applied || out.Value.Kind != "finish" || out.Value.Outcome != "done" ||
			out.Value.Revision != 3 || out.Value.Lease != "" || out.Value.LeaseUntilMS != w.now().UnixMilli() ||
			out.Digest == (ResultDigest{}) {
			t.Fatal(out)
		}
		ipPolicyCommitTurn(t, turn, tx)
		for query, want := range map[string]int64{
			`SELECT count(*) FROM metadata_acquisition_jobs WHERE entity_id=1 AND status='done' AND lease='' AND attempts=2 AND next_attempt_ms=` + fmt.Sprint(w.now().UnixMilli()): 1,
			`SELECT count(*) FROM metadata_provider_operations WHERE id='` + first.OperationID + `' AND status='staged' AND lease='' AND reason=''`:                                 1,
			`SELECT count(*) FROM metadata_ip_receipts WHERE command_kind='finish' AND result_phase='done' AND terminal=1`:                                                          1,
		} {
			if got := ipPolicyQueryInt(t, w, query); got != want {
				t.Fatal(query, got)
			}
		}
		// Finish never publishes or accepts metadata: no acquisition success.
		for name, query := range map[string]string{
			"evidence": `SELECT count(*) FROM metadata_evidence`,
			"builds":   `SELECT count(*) FROM metadata_evidence_builds`,
			"publics":  `SELECT count(*) FROM metadata_publications`,
			"subjects": `SELECT count(*) FROM metadata_subject_providers`,
		} {
			if got := ipPolicyQueryInt(t, w, query); got != before[name] {
				t.Fatal(name, got)
			}
		}
		// The terminal receipt replays without fresh lease state.
		ipPolicyExec(t, w, `UPDATE metadata_acquisition_jobs SET status='matched',revision=9,lease='other' WHERE entity_id=1`)
		ipPolicyExec(t, w, `UPDATE catalog_entities SET metadata_revision=7 WHERE id=1`)
		turn, tx, b := ipPolicyTurn(t, w, r, IPEntityFinish)
		replay, err := applyIPEntityFinish(t.Context(), turn, p, t.Context(), b, mustFinish(t, ipEntityJobIdentity(0x21, 2, 0x51), ipEntityJobFinishFor(first)))
		if err != nil || !replay.Outcome.Replayed || replay.Value != out.Value || replay.Digest != out.Digest {
			t.Fatal("finish replay", replay, err)
		}
		ipPolicyRollbackTurn(t, turn, tx)
		ipEntityJobAck(t, w, out.Value, 2)
		turn, tx, b = ipPolicyTurn(t, w, r, IPEntityFinish)
		retired, err := applyIPEntityFinish(t.Context(), turn, p, t.Context(), b, mustFinish(t, ipEntityJobIdentity(0x21, 2, 0x51), ipEntityJobFinishFor(first)))
		if err != nil || retired.Outcome.State != ResultRetired || retired.Value != (ipEntityJobResult{}) {
			t.Fatal("retired finish served", retired, err)
		}
		ipPolicyRollbackTurn(t, turn, tx)
	})
	t.Run("retry_reschedules_then_new_claim", func(t *testing.T) {
		w, r, p, base := ipEntityJobFixture(t)
		first, _ := ipEntityJobClaimed(t, w, r, p, 1, "tmdb")
		ipEntityJobAdvance(t, w, base, time.Minute)
		q := ipEntityJobFinishFor(first)
		q.outcome, q.reason = "retry", "provider timeout"
		out, turn, tx, _ := ipEntityJobApplyFinish(t, w, r, p, mustFinish(t, ipEntityJobIdentity(0x21, 2, 0x51), q))
		if out.Value.Outcome != "retry" || out.Value.LeaseUntilMS != w.now().UnixMilli() {
			t.Fatal(out)
		}
		ipPolicyCommitTurn(t, turn, tx)
		want := w.now().UnixMilli() + ipEntityJobRetryMS
		for query, wantValue := range map[string]int64{
			`SELECT count(*) FROM metadata_acquisition_jobs WHERE entity_id=1 AND status='pending' AND lease='' AND error='provider timeout' AND next_attempt_ms=` + fmt.Sprint(want): 1,
			`SELECT count(*) FROM metadata_provider_operations WHERE status='failed' AND reason='provider timeout'`:                                                                   1,
		} {
			if got := ipPolicyQueryInt(t, w, query); got != wantValue {
				t.Fatal(query, got)
			}
		}
		ipEntityJobAdvance(t, w, base, 6*time.Minute)
		second, turn, tx, _ := ipEntityJobApplyClaim(t, w, r, p, ipEntityJobClaimCommand(t, ipEntityJobIdentity(0x24, 1, 0x34), func(q *ipEntityJobClaimRequest) {
			q.jobGeneration, q.jobRevision = 2, 3
		}))
		if second.Value.Generation != 3 || second.Value.Revision != 4 {
			t.Fatal("retry claim", second)
		}
		ipPolicyCommitTurn(t, turn, tx)
	})
	t.Run("stale_observation_refused", func(t *testing.T) {
		w, r, p, _ := ipEntityJobFixture(t)
		first, _ := ipEntityJobClaimed(t, w, r, p, 1, "tmdb")
		ipPolicyExec(t, w, `UPDATE catalog_entities SET metadata_revision=2,revision=2 WHERE id=1`)
		turn, tx, b := ipPolicyTurn(t, w, r, IPEntityFinish)
		out, err := applyIPEntityFinish(t.Context(), turn, p, t.Context(), b, mustFinish(t, ipEntityJobIdentity(0x21, 2, 0x51), ipEntityJobFinishFor(first)))
		if out != (ipEntityJobCommandResult{}) || !errors.Is(err, ErrConflict) {
			t.Fatal("stale finish admitted", out, err)
		}
		ipPolicyRollbackTurn(t, turn, tx)
		if got := ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_ip_receipts WHERE command_kind='finish'`); got != 0 {
			t.Fatal("stale finish wrote a receipt", got)
		}
	})
}

func ipEntityJobApplyFinish(t *testing.T, w *executor, r *ipPolicyRegistry, p *usOwnerAuthorityProof, c *ipEntityJobCommand) (ipEntityJobCommandResult, *commandTurn, *transaction, ipPolicyBudget) {
	t.Helper()
	turn, tx, b := ipPolicyTurn(t, w, r, IPEntityFinish)
	out, err := applyIPEntityFinish(t.Context(), turn, p, t.Context(), b, c)
	if err != nil {
		t.Fatal("finish", out, err)
	}
	return out, turn, tx, b
}

func TestIPEntityJobCurrentAuthorityAndLifetime(t *testing.T) {
	type prepared struct {
		w       *executor
		r       *ipPolicyRegistry
		p       *usOwnerAuthorityProof
		command *ipEntityJobCommand
		purpose CommandID
	}
	prepare := func(t *testing.T, kind string) prepared {
		t.Helper()
		w, r, p, _ := ipEntityJobFixture(t)
		switch kind {
		case "claim":
			ipEntityJobSeed(t, w, 1, ipEntityJobSeedOptions{})
			return prepared{w, r, p, ipEntityJobClaimCommand(t, ipEntityJobIdentity(0x21, 1, 0x31), nil), IPEntityClaim}
		case "renew":
			first, _ := ipEntityJobClaimed(t, w, r, p, 1, "tmdb")
			return prepared{w, r, p, mustRenew(t, ipEntityJobIdentity(0x21, 2, 0x41), ipEntityJobRenewFor(first)), IPEntityRenew}
		default:
			first, _ := ipEntityJobClaimed(t, w, r, p, 1, "tmdb")
			return prepared{w, r, p, mustFinish(t, ipEntityJobIdentity(0x21, 2, 0x51), ipEntityJobFinishFor(first)), IPEntityFinish}
		}
	}
	apply := func(ctx context.Context, purpose CommandID, turn *commandTurn, p *usOwnerAuthorityProof, b ipPolicyBudget, c *ipEntityJobCommand) (ipEntityJobCommandResult, error) {
		switch purpose {
		case IPEntityClaim:
			return applyIPEntityClaim(ctx, turn, p, ctx, b, c)
		case IPEntityRenew:
			return applyIPEntityRenew(ctx, turn, p, ctx, b, c)
		default:
			return applyIPEntityFinish(ctx, turn, p, ctx, b, c)
		}
	}
	for _, mode := range []string{"revoked_family", "forged_proof", "revoked_membership"} {
		for _, kind := range []string{"claim", "renew", "finish"} {
			t.Run(kind+"_"+mode, func(t *testing.T) {
				state := prepare(t, kind)
				proof := ipPolicyRevoke(t, state.w, state.p, mode)
				turn, tx, b := ipPolicyTurn(t, state.w, state.r, state.purpose)
				out, err := apply(t.Context(), state.purpose, turn, proof, b, state.command)
				if out != (ipEntityJobCommandResult{}) || !errors.Is(err, ErrDisclosureDenied) {
					t.Fatal("stale current authority admitted", out, err)
				}
				ipPolicyRollbackTurn(t, turn, tx)
			})
		}
	}
	for _, kind := range []string{"claim", "renew", "finish"} {
		t.Run(kind+"_cancelled_lifetime", func(t *testing.T) {
			state := prepare(t, kind)
			lifetime, cancel := context.WithCancel(t.Context())
			cancel()
			turn, tx, b := ipPolicyTurn(t, state.w, state.r, state.purpose)
			out, err := apply(lifetime, state.purpose, turn, state.p, b, state.command)
			if out != (ipEntityJobCommandResult{}) || err == nil {
				t.Fatal("cancelled lifetime admitted", out, err)
			}
			ipPolicyRollbackTurn(t, turn, tx)
		})
	}
}

func TestIPEntityJobUnknownCommitFreshRead(t *testing.T) {
	for _, kind := range []string{"claim", "renew", "finish"} {
		t.Run(kind, func(t *testing.T) {
			w, r, p, _ := ipEntityJobFixture(t)
			var command *ipEntityJobCommand
			var purpose CommandID
			var owner string
			switch kind {
			case "claim":
				ipEntityJobSeed(t, w, 1, ipEntityJobSeedOptions{})
				command = ipEntityJobClaimCommand(t, ipEntityJobIdentity(0x21, 1, 0x31), nil)
				purpose = IPEntityClaim
				owner = ipEntityJobHex(ipEntityJobIdentity(0x21, 1, 0x31).OperationID)
			case "renew":
				first, _ := ipEntityJobClaimed(t, w, r, p, 1, "tmdb")
				command = mustRenew(t, ipEntityJobIdentity(0x21, 2, 0x41), ipEntityJobRenewFor(first))
				purpose = IPEntityRenew
				owner = first.OperationID
			default:
				first, _ := ipEntityJobClaimed(t, w, r, p, 1, "tmdb")
				command = mustFinish(t, ipEntityJobIdentity(0x21, 2, 0x51), ipEntityJobFinishFor(first))
				purpose = IPEntityFinish
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
			var out ipEntityJobCommandResult
			switch purpose {
			case IPEntityClaim:
				out, err = applyIPEntityClaim(t.Context(), turn, p, t.Context(), b, command)
			case IPEntityRenew:
				out, err = applyIPEntityRenew(t.Context(), turn, p, t.Context(), b, command)
			default:
				out, err = applyIPEntityFinish(t.Context(), turn, p, t.Context(), b, command)
			}
			if err != nil || out.Outcome.State != Applied {
				t.Fatal(out, err)
			}
			result, err := tx.commit()
			if result.state != commitUnknown || !errors.Is(err, ErrResolveRequired) {
				t.Fatal(result, err)
			}
			// The uncertain frame is closed; it never reserves the saved result.
			switch purpose {
			case IPEntityClaim:
				if again, e := applyIPEntityClaim(t.Context(), turn, p, t.Context(), b, command); again != (ipEntityJobCommandResult{}) || e == nil {
					t.Fatal("uncertain claim frame reserved", again, e)
				}
			case IPEntityRenew:
				if again, e := applyIPEntityRenew(t.Context(), turn, p, t.Context(), b, command); again != (ipEntityJobCommandResult{}) || e == nil {
					t.Fatal("uncertain renew frame reserved", again, e)
				}
			default:
				if again, e := applyIPEntityFinish(t.Context(), turn, p, t.Context(), b, command); again != (ipEntityJobCommandResult{}) || e == nil {
					t.Fatal("uncertain finish frame reserved", again, e)
				}
			}
			// Only a FRESH read-only connection may prove the durable outcome.
			switch kind {
			case "claim":
				if got := ipPolicyFreshInt(t, path, `SELECT count(*) FROM metadata_ip_receipts WHERE owner_id='`+owner+`' AND command_kind='claim'`); got != 1 {
					t.Fatal("durable claim receipt", got)
				}
				if got := ipPolicyFreshInt(t, path, `SELECT count(*) FROM metadata_acquisition_jobs WHERE entity_id=1 AND status='claimed' AND generation=2`); got != 1 {
					t.Fatal("durable claim lease", got)
				}
			case "renew":
				if got := ipPolicyFreshInt(t, path, `SELECT count(*) FROM metadata_ip_receipts WHERE owner_id='`+owner+`' AND command_kind='renew'`); got != 1 {
					t.Fatal("durable renew receipt", got)
				}
				if got := ipPolicyFreshInt(t, path, `SELECT revision FROM metadata_acquisition_jobs WHERE entity_id=1`); got != 3 {
					t.Fatal("durable renew revision", got)
				}
			case "finish":
				if got := ipPolicyFreshInt(t, path, `SELECT count(*) FROM metadata_ip_receipts WHERE owner_id='`+owner+`' AND command_kind='finish' AND terminal=1`); got != 1 {
					t.Fatal("durable finish receipt", got)
				}
				if got := ipPolicyFreshInt(t, path, `SELECT count(*) FROM metadata_acquisition_jobs WHERE entity_id=1 AND status='done'`); got != 1 {
					t.Fatal("durable finish status", got)
				}
			}
		})
	}
}

func TestIPEntityJobBounds(t *testing.T) {
	long := strings.Repeat("s", 128)
	claim := map[string]func(*ipEntityJobClaimRequest){
		"zero_entity":       func(q *ipEntityJobClaimRequest) { q.entityID = 0 },
		"zero_library":      func(q *ipEntityJobClaimRequest) { q.libraryID = 0 },
		"empty_incarnation": func(q *ipEntityJobClaimRequest) { q.entityIncarnation = [16]byte{} },
		"empty_library_key": func(q *ipEntityJobClaimRequest) { q.libraryKey = "" },
		"long_library_key":  func(q *ipEntityJobClaimRequest) { q.libraryKey = long + "x" },
		"empty_selected":    func(q *ipEntityJobClaimRequest) { q.selectedID = "" },
		"long_selected":     func(q *ipEntityJobClaimRequest) { q.selectedID = long + "x" },
		"wrong_role":        func(q *ipEntityJobClaimRequest) { q.role = "primary2" },
		"unknown_provider":  func(q *ipEntityJobClaimRequest) { q.provider = "plex" },
		"artwork_purpose":   func(q *ipEntityJobClaimRequest) { q.purpose = "artwork" },
		"zero_generation":   func(q *ipEntityJobClaimRequest) { q.jobGeneration = 0 },
		"zero_revision":     func(q *ipEntityJobClaimRequest) { q.jobRevision = 0 },
		"zero_entity_rev":   func(q *ipEntityJobClaimRequest) { q.entityRevision = 0 },
		"zero_source_rev":   func(q *ipEntityJobClaimRequest) { q.sourceRevision = 0 },
		"empty_policy":      func(q *ipEntityJobClaimRequest) { q.policyIncarnation = [16]byte{} },
		"zero_policy_rev":   func(q *ipEntityJobClaimRequest) { q.policyRevision = 0 },
		"zero_consent":      func(q *ipEntityJobClaimRequest) { q.consentRevision = 0 },
	}
	for name, mutate := range claim {
		t.Run("claim_"+name, func(t *testing.T) {
			q := ipEntityJobClaimFor(1, "tmdb")
			mutate(&q)
			if c, err := newIPEntityClaimCommand(ipEntityJobIdentity(0x21, 1, 0x31), q); c != nil || !errors.Is(err, ErrInvalid) {
				t.Fatal(c, err)
			}
		})
	}
	q := ipEntityJobClaimFor(1, "tmdb")
	q.selectedID, q.libraryKey = long, long
	if c, err := newIPEntityClaimCommand(ipEntityJobIdentity(0x21, 1, 0x31), q); err != nil || c == nil {
		t.Fatal("maximum claim bounds refused", err)
	}
	base := ipEntityJobRenewRequest{operationID: strings.Repeat("a", 32), provider: "tmdb", purpose: "metadata",
		selectedID: "tmdb-1", lease: strings.Repeat("b", 32), entityID: 1, generation: 1,
		jobGeneration: 1, jobRevision: 1, leaseUntilMS: 1}
	renew := map[string]func(*ipEntityJobRenewRequest){
		"short_operation":   func(q *ipEntityJobRenewRequest) { q.operationID = "short" },
		"empty_lease":       func(q *ipEntityJobRenewRequest) { q.lease = "" },
		"long_lease":        func(q *ipEntityJobRenewRequest) { q.lease = long + "x" },
		"zero_generation":   func(q *ipEntityJobRenewRequest) { q.generation = 0 },
		"zero_job_revision": func(q *ipEntityJobRenewRequest) { q.jobRevision = 0 },
		"zero_lease_until":  func(q *ipEntityJobRenewRequest) { q.leaseUntilMS = 0 },
	}
	for name, mutate := range renew {
		t.Run("renew_"+name, func(t *testing.T) {
			value := base
			mutate(&value)
			if c, err := newIPEntityRenewCommand(ipEntityJobIdentity(0x21, 2, 0x41), value); c != nil || !errors.Is(err, ErrInvalid) {
				t.Fatal(c, err)
			}
		})
	}
	finish := ipEntityJobFinishRequest{operationID: strings.Repeat("a", 32), provider: "tmdb", purpose: "metadata",
		selectedID: "tmdb-1", lease: strings.Repeat("b", 32), entityID: 1, generation: 1,
		jobGeneration: 1, jobRevision: 1, leaseUntilMS: 1}
	finishCases := map[string]func(*ipEntityJobFinishRequest){
		"empty_outcome": func(q *ipEntityJobFinishRequest) { q.outcome = "" },
		"bad_outcome":   func(q *ipEntityJobFinishRequest) { q.outcome = "ok" },
		"done_reason":   func(q *ipEntityJobFinishRequest) { q.outcome, q.reason = "done", "why" },
		"long_reason": func(q *ipEntityJobFinishRequest) {
			q.outcome, q.reason = "retry", strings.Repeat("r", ipEntityJobMaxReason+1)
		},
	}
	for name, mutate := range finishCases {
		t.Run("finish_"+name, func(t *testing.T) {
			value := finish
			mutate(&value)
			if c, err := newIPEntityFinishCommand(ipEntityJobIdentity(0x21, 2, 0x51), value); c != nil || !errors.Is(err, ErrInvalid) {
				t.Fatal(c, err)
			}
		})
	}
	finish.outcome, finish.reason = "retry", strings.Repeat("r", ipEntityJobMaxReason)
	if c, err := newIPEntityFinishCommand(ipEntityJobIdentity(0x21, 2, 0x51), finish); err != nil || c == nil {
		t.Fatal("maximum finish reason refused", err)
	}
}

func TestIPEntityJobDescriptors(t *testing.T) {
	for _, expected := range []Descriptor{
		{ID: IPEntityClaim, Name: "ip_entity_claim", Durability: Normal},
		{ID: IPEntityRenew, Name: "ip_entity_renew", Durability: Normal},
		{ID: IPEntityFinish, Name: "ip_entity_finish", Durability: Normal},
	} {
		actual, err := Describe(expected.ID)
		if err != nil || actual != expected || actual.ID < 200 || actual.ID > 299 {
			t.Fatal(actual, err)
		}
	}
}

func TestIPEntityJobClaimMaximumLengths(t *testing.T) {
	w, r, p, _ := ipEntityJobFixture(t)
	long := strings.Repeat("m", 128)
	ipPolicyExec(t, w, `UPDATE catalog_libraries SET library_id=? WHERE id=777`, long)
	ipEntityJobSeed(t, w, 1, ipEntityJobSeedOptions{selected: long})
	out, turn, tx, _ := ipEntityJobApplyClaim(t, w, r, p, ipEntityJobClaimCommand(t, ipEntityJobIdentity(0x21, 1, 0x31), func(value *ipEntityJobClaimRequest) {
		value.libraryKey, value.selectedID = long, long
	}))
	if out.Outcome.State != Applied || out.Value.SelectedID != long {
		t.Fatal(out)
	}
	ipPolicyCommitTurn(t, turn, tx)
	if got := ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_provider_operations WHERE selected_id=?`, long); got != 1 {
		t.Fatal("maximum selected length", got)
	}
}
