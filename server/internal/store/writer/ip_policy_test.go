package writer

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

type ipPolicyRegistry struct {
	*stmt.Registry
	keeper *checkpointComponent
}

func ipPolicySecurityFixture(t *testing.T) (*executor, *checkpointComponent, *usOwnerAuthorityProof) {
	t.Helper()
	seed, path, _ := testExecutor(t)
	if err := seed.close(); err != nil {
		t.Fatal(err)
	}
	f := newCheckpointComponentAt(t, path, true, false)
	w := f.w
	// Frozen whole-second current clock, no real-clock assertions.
	now := f.now().UTC().Truncate(time.Second)
	w.now = func() time.Time { return now }
	// Group B (security) is embedded, so the retired overlay DDL is not replayed.
	_, request := usPolicyFixture()
	witness := usOwnerAuthorityWitness{issuer: request.issuer, device: "issuer-device", lineage: "issuer-lineage", installationID: "issuer-installation-synthetic-000001", tokenExpiry: now.Add(10 * time.Minute).Format(time.RFC3339), horizon: now.Add(time.Hour).Format(time.RFC3339)}
	proof, err := prepareUSOwnerAuthority(witness)
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO configuration(key,value) VALUES('id','server') ON CONFLICT(key) DO UPDATE SET value=excluded.value`, nil},
		{`INSERT INTO accounts(id,username,password_hash,profile_id) VALUES('issuer','issuer',X'','issuer-profile')`, nil},
		{`INSERT INTO direct_memberships(account_id,incarnation,role) VALUES('issuer',?,'owner')`, []any{witness.issuer.memberIncarnation}},
		{`INSERT INTO direct_profiles(id,account_id,name,is_primary,lifetime) VALUES('issuer-profile','issuer','Issuer',1,?)`, []any{witness.issuer.profileLifetime}},
		{`INSERT INTO identity_installations(installation_hash,incarnation) VALUES(?,?)`, []any{proof.installationHash, witness.issuer.installation}},
		{`INSERT INTO identity_devices(id,account_id,installation_id,name,platform,app,app_version,first_seen,last_seen) VALUES('issuer-device','issuer',?,'Issuer','test','test','1','','')`, []any{witness.installationID}},
		{`INSERT INTO identity_pin_lineages(id,account_id,origin_device_id,created_at) VALUES('issuer-lineage','issuer','issuer-device','')`, nil},
		{`INSERT INTO identity_state(singleton,sign_in_kind,authority_generation) VALUES(1,'local',1) ON CONFLICT(singleton) DO UPDATE SET sign_in_kind='local',authority_generation=1`, nil},
		{`INSERT INTO authorization_session_families(security_kind,id,server_id,account_id,profile_id,authority,role,epoch,authorization_horizon,current_generation,device_id,pin_lineage_id,command_member_incarnation,command_profile_lifetime,command_incarnation) VALUES(0,'issuer-family','server','issuer','issuer-profile','local','owner',1,?,1,'issuer-device','issuer-lineage',?,?,?)`, []any{witness.horizon, witness.issuer.memberIncarnation, witness.issuer.profileLifetime, []byte{9, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}}},
		{`INSERT INTO authorization_family_tokens(token_hash,family_id,generation,expires_at) VALUES(?,'issuer-family',1,?)`, []any{witness.issuer.tokenDigest, witness.tokenExpiry}},
		{`INSERT INTO identity_security_fences(id,scope_kind,account_id,member_incarnation,device_id,updated_ms) VALUES(1,'device','issuer',?,'issuer-device',0)`, []any{witness.issuer.memberIncarnation}},
		{`INSERT INTO authorization_family_security_fences(family_id,scope_kind,fence_id,confirmed_generation) VALUES('issuer-family','device',1,1)`, nil},
	} {
		if _, err = w.db.ExecContext(t.Context(), query.sql, query.args...); err != nil {
			t.Fatal(query.sql, err)
		}
	}
	return w, f, proof
}

func ipPolicyFixture(t *testing.T) (*executor, *ipPolicyRegistry, *usOwnerAuthorityProof) {
	t.Helper()
	w, keeper, p := ipPolicySecurityFixture(t)
	ddl, err := os.ReadFile("testdata/ip-policy-draft.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = w.db.ExecContext(t.Context(), string(ddl)); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		`INSERT INTO catalog_libraries(id,library_id,root) VALUES(777,'ip-library','/media')`,
		`INSERT INTO metadata_providers(code,adapter,enabled) VALUES('tmdb','tmdb',1),('tvdb','tvdb',1),('musicbrainz','musicbrainz',1)`,
		`INSERT INTO metadata_provider_policy(library_id,provider,incarnation,revision,enabled,priority,region,algorithm,refresh_mode,credit_enabled,credit_after_item,credit_after_provider,command_incarnation) VALUES(777,'tmdb',X'09000000000000000000000000000000',1,1,5,'US','v1','fill_missing',1,NULL,NULL,X'01000000000000000000000000000000'),(777,'musicbrainz',X'0a000000000000000000000000000000',1,1,5,'US','v1','fill_missing',1,NULL,NULL,X'01000000000000000000000000000000')`,
		`INSERT INTO screen_metadata_consent(singleton,revision,confirmed) VALUES(1,1,1) ON CONFLICT(singleton) DO UPDATE SET revision=1,confirmed=1`,
		`INSERT INTO metadata_runtime_settings(singleton,online_lookups,lookup_epoch) VALUES(1,1,3) ON CONFLICT(singleton) DO UPDATE SET online_lookups=1,lookup_epoch=3`,
	} {
		if _, err = w.db.ExecContext(t.Context(), query); err != nil {
			t.Fatal(query, err)
		}
	}
	r, err := stmttest.New(t, ipPolicyDefinitions())
	if err != nil {
		t.Fatal(err)
	}
	return w, &ipPolicyRegistry{r, keeper}, p
}

func ipPolicyTurn(t *testing.T, w *executor, r *ipPolicyRegistry, purpose CommandID) (*commandTurn, *transaction, ipPolicyBudget) {
	t.Helper()
	var seq int
	var name, path string
	if err := w.db.QueryRowContext(t.Context(), `PRAGMA database_list`).Scan(&seq, &name, &path); err != nil {
		t.Fatal(err)
	}
	recipe := testRecipe()
	recipe.limit = Work{10000, 1_000_000, 10000, 4 << 20, 20 * time.Millisecond}
	g, a, wal := r.keeper.g, r.keeper.a, r.keeper.wal
	request := governor.Request{Key: governor.JobKey{ID: "ip/policy"}, Owner: 1, Writer: true, Class: governor.Background, MaxHold: recipe.limit.Hold, Deadline: r.keeper.now().Add(time.Second), Budget: governor.UnitBudget{RawRows: uint32(recipe.limit.Rows), VMOps: recipe.limit.VMOps, Pages: uint32(recipe.limit.Pages)}}
	request.Need[governor.MemoryBytes] = 4 << 20
	request.Need[governor.WALBytes] = int64(recipe.limit.WALBytes)
	request.Key.Domain = uint16(purpose)
	if err := refreshWriterWAL(path, wal, r.keeper.now); err != nil {
		t.Fatal(err)
	}
	var err error
	request.RestartRisk, err = wal.RestartRisk(r.keeper.now())
	if err != nil {
		t.Fatal(err)
	}
	grant := g.Try(request)
	if grant.Grant == nil {
		t.Fatal(grant.Refusal)
	}
	c, err := bindGovernedTurn(purpose, recipe.limit, g, a, wal, grant.Grant, request)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := w.begin(t.Context(), purpose, recipe, c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = tx.rollback(nil) })
	var steps []statementStep
	for _, d := range r.Manifest() {
		steps = append(steps, statementStep{d.ID, Work{8, 500, 8, 4096, 10 * time.Microsecond}})
	}
	turn, err := newCommandTurn(tx, r.Registry, steps)
	if err != nil {
		t.Fatal(err)
	}
	return turn, tx, ipPolicyBudget{4 << 20}
}

func ipPolicyIdentity(sequence int64, operation byte) CommandIdentity {
	return CommandIdentity{Incarnation: [16]byte{1}, Sequence: sequence, OperationID: [16]byte{operation}}
}

func ipPolicyTestRequest() ipPolicyRequest {
	return ipPolicyRequest{libraryID: 777, libraryKey: "ip-library", provider: "tmdb",
		policyIncarnation: [16]byte{9}, expectedRevision: 1, enabled: true, priority: 5,
		region: "US", algorithm: "v1", refreshMode: "fill_missing", creditEnabled: true, consentRevision: 1}
}

func ipPolicyTestCommand(t *testing.T, sequence int64, operation byte, mutate func(*ipPolicyRequest)) *ipPolicyCommand {
	t.Helper()
	q := ipPolicyTestRequest()
	if mutate != nil {
		mutate(&q)
	}
	c, err := newIPPolicyCommand(ipPolicyIdentity(sequence, operation), q)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func ipRefreshTestCommand(t *testing.T, sequence int64, operation byte, mutate func(*ipRefreshRequest)) *ipRefreshCommand {
	t.Helper()
	q := ipRefreshRequest{provider: "tmdb", purpose: "metadata", consentRevision: 1}
	if mutate != nil {
		mutate(&q)
	}
	c, err := newIPRefreshCommand(ipPolicyIdentity(sequence, operation), q)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func ipPolicyQueryInt(t *testing.T, w *executor, query string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := w.db.QueryRowContext(t.Context(), query, args...).Scan(&n); err != nil {
		t.Fatal(query, err)
	}
	return n
}

func ipPolicyExec(t *testing.T, w *executor, query string, args ...any) {
	t.Helper()
	if _, err := w.db.ExecContext(t.Context(), query, args...); err != nil {
		t.Fatal(query, err)
	}
}

// A direct test caller owns the frame terminal path. Close native rows before
// the single-connection pool is handed back or reused by another statement.
func ipPolicyCommitTurn(t *testing.T, turn *commandTurn, tx *transaction) {
	t.Helper()
	if err := turn.closeRows(); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.commit(); err != nil {
		t.Fatal(err)
	}
}

func ipPolicyRollbackTurn(t *testing.T, turn *commandTurn, tx *transaction) {
	t.Helper()
	if err := turn.closeRows(); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.rollback(nil); err != nil {
		t.Fatal(err)
	}
}

func TestIPPolicySetFreshExactReplayAfterSourceChanges(t *testing.T) {
	w, r, p := ipPolicyFixture(t)
	cmd := ipPolicyTestCommand(t, 1, 2, nil)
	turn, tx, b := ipPolicyTurn(t, w, r, IPPolicySet)
	out, err := applyIPPolicySet(t.Context(), turn, p, t.Context(), b, cmd)
	if err != nil {
		t.Fatal(err)
	}
	if out.Outcome.State != Applied || out.Outcome.Replayed || out.Value.AcceptedRevision != 2 ||
		out.Value.ResultCount != 1 || out.Digest == (ResultDigest{}) || out.Value.AppliedMS != w.now().UnixMilli() {
		t.Fatal(out)
	}
	ipPolicyCommitTurn(t, turn, tx)
	original := out

	// Source changes after the accepted result must not rebuild or reapply it.
	ipPolicyExec(t, w, `UPDATE metadata_providers SET enabled=0 WHERE code='tmdb'`)
	ipPolicyExec(t, w, `UPDATE screen_metadata_consent SET confirmed=0`)
	turn, tx, b = ipPolicyTurn(t, w, r, IPPolicySet)
	out, err = applyIPPolicySet(t.Context(), turn, p, t.Context(), b, cmd)
	if err != nil {
		t.Fatal(err)
	}
	if out.Outcome.State != Applied || !out.Outcome.Replayed || out.Digest != original.Digest || !ipPolicyResultEqual(out.Value, original.Value) {
		t.Fatal("source change altered original replay", out)
	}
	ipPolicyRollbackTurn(t, turn, tx)

	// An independent policy advance is not a freshness gate for the original.
	ipPolicyExec(t, w, `UPDATE metadata_provider_policy SET revision=7 WHERE provider='tmdb'`)
	turn, tx, b = ipPolicyTurn(t, w, r, IPPolicySet)
	out, err = applyIPPolicySet(t.Context(), turn, p, t.Context(), b, cmd)
	if err != nil || out.Value.AcceptedRevision != 2 || !out.Outcome.Replayed {
		t.Fatal("policy advance altered original result", out, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
	if got := ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_policy_receipts`); got != 1 {
		t.Fatal("replay wrote receipts", got)
	}

	// At or below ACK is Retired, even while the row remains physical.
	ipPolicyExec(t, w, `UPDATE metadata_provider_policy SET last_command_sequence=1,acknowledged_command_sequence=1 WHERE provider='tmdb'`)
	turn, tx, b = ipPolicyTurn(t, w, r, IPPolicySet)
	out, err = applyIPPolicySet(t.Context(), turn, p, t.Context(), b, cmd)
	if err != nil || out.Outcome.State != ResultRetired || out.Value != (ipPolicyResult{}) || out.Digest != (ResultDigest{}) {
		t.Fatal("retired result served", out, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
}

func TestIPPolicySetFreshCreate(t *testing.T) {
	w, r, p := ipPolicyFixture(t)
	ipPolicyExec(t, w, `DELETE FROM metadata_provider_policy WHERE provider='tmdb'`)
	cmd := ipPolicyTestCommand(t, 1, 2, func(q *ipPolicyRequest) { q.expectedRevision = 0 })
	turn, tx, b := ipPolicyTurn(t, w, r, IPPolicySet)
	out, err := applyIPPolicySet(t.Context(), turn, p, t.Context(), b, cmd)
	if err != nil || out.Value.AcceptedRevision != 1 || out.Outcome.State != Applied {
		t.Fatal(out, err)
	}
	ipPolicyCommitTurn(t, turn, tx)
	if got := ipPolicyQueryInt(t, w, `SELECT revision FROM metadata_provider_policy WHERE library_id=777 AND provider='tmdb'`); got != 1 {
		t.Fatal(got)
	}
	if got := ipPolicyQueryInt(t, w, `SELECT last_command_sequence FROM metadata_provider_policy WHERE library_id=777 AND provider='tmdb'`); got != 1 {
		t.Fatal(got)
	}
	if got := ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_provider_policy WHERE library_id=777 AND provider='tmdb' AND incarnation=X'09000000000000000000000000000000' AND command_incarnation=X'01000000000000000000000000000000'`); got != 1 {
		t.Fatal(got)
	}
	turn, tx, b = ipPolicyTurn(t, w, r, IPPolicySet)
	out, err = applyIPPolicySet(t.Context(), turn, p, t.Context(), b, cmd)
	if err != nil || !out.Outcome.Replayed || out.Value.AcceptedRevision != 1 {
		t.Fatal("create replay", out, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
}

func TestIPPolicySetStaleDisabledAndConsent(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*ipPolicyRequest)
		sql    []string
		want   error
	}{
		{"stale_policy", func(q *ipPolicyRequest) { q.expectedRevision = 2 }, nil, ErrConflict},
		{"disabled_provider", nil, []string{`UPDATE metadata_providers SET enabled=0 WHERE code='tmdb'`}, ErrDisclosureDenied},
		{"screen_consent_revision", func(q *ipPolicyRequest) { q.consentRevision = 2 }, nil, ErrDisclosureDenied},
		{"music_consent_mismatch", func(q *ipPolicyRequest) {
			q.provider = "musicbrainz"
			q.policyIncarnation = [16]byte{10}
			q.consentRevision = 1
		}, nil, ErrDisclosureDenied},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w, r, p := ipPolicyFixture(t)
			for _, query := range tc.sql {
				ipPolicyExec(t, w, query)
			}
			cmd := ipPolicyTestCommand(t, 1, 2, tc.mutate)
			turn, tx, b := ipPolicyTurn(t, w, r, IPPolicySet)
			out, err := applyIPPolicySet(t.Context(), turn, p, t.Context(), b, cmd)
			if out != (ipPolicyCommandResult{}) || !errors.Is(err, tc.want) {
				t.Fatal("stale/disabled/consent admitted", out, err)
			}
			ipPolicyRollbackTurn(t, turn, tx)
			if got := ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_policy_receipts`); got != 0 {
				t.Fatal("denied command wrote receipts", got)
			}
		})
	}
	w, r, p := ipPolicyFixture(t)
	cmd := ipPolicyTestCommand(t, 1, 2, func(q *ipPolicyRequest) {
		q.provider = "musicbrainz"
		q.policyIncarnation = [16]byte{10}
		q.consentRevision = 3
	})
	turn, tx, b := ipPolicyTurn(t, w, r, IPPolicySet)
	out, err := applyIPPolicySet(t.Context(), turn, p, t.Context(), b, cmd)
	if err != nil || out.Outcome.State != Applied {
		t.Fatal("MusicBrainz lookup epoch", out, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
}

func TestIPPolicyCurrentAuthorityBeforeReceipt(t *testing.T) {
	for _, mode := range []string{"revoked_family", "forged_proof", "revoked_membership"} {
		t.Run(mode, func(t *testing.T) {
			w, r, p := ipPolicyFixture(t)
			cmd := ipPolicyTestCommand(t, 1, 2, nil)
			turn, tx, b := ipPolicyTurn(t, w, r, IPPolicySet)
			if out, err := applyIPPolicySet(t.Context(), turn, p, t.Context(), b, cmd); err != nil || out.Outcome.State != Applied {
				t.Fatal(out, err)
			}
			ipPolicyCommitTurn(t, turn, tx)
			proof := ipPolicyRevoke(t, w, p, mode)
			turn, tx, b = ipPolicyTurn(t, w, r, IPPolicySet)
			out, err := applyIPPolicySet(t.Context(), turn, proof, t.Context(), b, cmd)
			if out != (ipPolicyCommandResult{}) || !errors.Is(err, ErrDisclosureDenied) {
				t.Fatal("stale current authority served the saved result", out, err)
			}
			ipPolicyRollbackTurn(t, turn, tx)
		})
	}
}

func TestIPPolicySetNoFanout(t *testing.T) {
	w, r, p := ipPolicyFixture(t)
	before := map[string]int64{
		"policy":     ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_provider_policy`),
		"receipts":   ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_policy_receipts`),
		"subjects":   ipPolicyQueryInt(t, w, `SELECT count(*) FROM catalog_subjects`),
		"entities":   ipPolicyQueryInt(t, w, `SELECT count(*) FROM catalog_entities`),
		"people":     ipPolicyQueryInt(t, w, `SELECT count(*) FROM catalog_people`),
		"operations": ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_provider_operations`),
		"accounts":   ipPolicyQueryInt(t, w, `SELECT count(*) FROM accounts`),
		"sweeps":     ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_refresh_sweeps`),
	}
	cmd := ipPolicyTestCommand(t, 1, 2, nil)
	turn, tx, b := ipPolicyTurn(t, w, r, IPPolicySet)
	if _, err := applyIPPolicySet(t.Context(), turn, p, t.Context(), b, cmd); err != nil {
		t.Fatal(err)
	}
	ipPolicyCommitTurn(t, turn, tx)
	for _, table := range []string{"subjects", "entities", "people", "operations", "accounts", "sweeps"} {
		var query string
		switch table {
		case "subjects":
			query = `SELECT count(*) FROM catalog_subjects`
		case "entities":
			query = `SELECT count(*) FROM catalog_entities`
		case "people":
			query = `SELECT count(*) FROM catalog_people`
		case "operations":
			query = `SELECT count(*) FROM metadata_provider_operations`
		case "accounts":
			query = `SELECT count(*) FROM accounts`
		case "sweeps":
			query = `SELECT count(*) FROM metadata_refresh_sweeps`
		}
		if got := ipPolicyQueryInt(t, w, query); got != before[table] {
			t.Fatal("policy command fanned out to", table, got)
		}
	}
	if got := ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_provider_policy`); got != before["policy"] {
		t.Fatal("policy row count", got)
	}
	if got := ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_policy_receipts`); got != before["receipts"]+1 {
		t.Fatal("receipt count", got)
	}
	if got := ipPolicyQueryInt(t, w, `SELECT revision FROM metadata_provider_policy WHERE provider='tmdb'`); got != 2 {
		t.Fatal("one policy revision expected", got)
	}
	if got := ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_provider_policy WHERE provider='tmdb' AND enabled=1 AND priority=5 AND region='US' AND algorithm='v1' AND refresh_mode='fill_missing'`); got != 1 {
		t.Fatal(got)
	}
}

func TestIPPolicySetBoundsRollbackAndConflicts(t *testing.T) {
	w, r, p := ipPolicyFixture(t)
	longKey := strings.Repeat("k", 128)
	longAlgorithm := strings.Repeat("a", 64)
	longProvider := strings.Repeat("p", 128)
	ipPolicyExec(t, w, `UPDATE catalog_libraries SET library_id=? WHERE id=777`, longKey)
	turn, tx, b := ipPolicyTurn(t, w, r, IPPolicySet)
	cmd := ipPolicyTestCommand(t, 1, 2, func(q *ipPolicyRequest) {
		q.libraryKey = longKey
		q.algorithm = longAlgorithm
		q.creditAfterItem = func() *int64 { v := int64(999_999); return &v }()
		q.creditAfterProvider = longProvider
	})
	if out, err := applyIPPolicySet(t.Context(), turn, p, t.Context(), b, cmd); err != nil || out.Outcome.State != Applied {
		t.Fatal("bounded maximum refused", out, err)
	}
	// The caller-owned frame rolls back the whole cohort on any later failure.
	ipPolicyRollbackTurn(t, turn, tx)
	if got := ipPolicyQueryInt(t, w, `SELECT revision FROM metadata_provider_policy WHERE provider='tmdb'`); got != 1 {
		t.Fatal("rollback kept policy revision", got)
	}
	if got := ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_policy_receipts`); got != 0 {
		t.Fatal("rollback kept receipt", got)
	}

	for name, mutate := range map[string]func(*ipPolicyRequest){
		"empty_library":      func(q *ipPolicyRequest) { q.libraryKey = "" },
		"unknown_provider":   func(q *ipPolicyRequest) { q.provider = "plex" },
		"lower_region":       func(q *ipPolicyRequest) { q.region = "us" },
		"empty_algorithm":    func(q *ipPolicyRequest) { q.algorithm = "" },
		"bad_refresh_mode":   func(q *ipPolicyRequest) { q.refreshMode = "replace" },
		"credit_without_id":  func(q *ipPolicyRequest) { v := int64(1); q.creditAfterItem = &v },
		"priority_too_large": func(q *ipPolicyRequest) { q.priority = 1001 },
		"zero_consent":       func(q *ipPolicyRequest) { q.consentRevision = 0 },
		"empty_incarnation":  func(q *ipPolicyRequest) { q.policyIncarnation = [16]byte{} },
	} {
		t.Run(name, func(t *testing.T) {
			q := ipPolicyTestRequest()
			mutate(&q)
			if c, err := newIPPolicyCommand(ipPolicyIdentity(1, 2), q); c != nil || !errors.Is(err, ErrInvalid) {
				t.Fatal(c, err)
			}
		})
	}

	// A sequence gap is a conflict, never an implicit allocation.
	ipPolicyExec(t, w, `UPDATE metadata_provider_policy SET last_command_sequence=1,acknowledged_command_sequence=0 WHERE provider='tmdb'`)
	cmd = ipPolicyTestCommand(t, 3, 2, nil)
	turn, tx, b = ipPolicyTurn(t, w, r, IPPolicySet)
	if out, err := applyIPPolicySet(t.Context(), turn, p, t.Context(), b, cmd); out != (ipPolicyCommandResult{}) || !errors.Is(err, ErrConflict) {
		t.Fatal("sequence gap admitted", out, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)

	// Same sequence, different original request digest is a conflict.
	w, r, p = ipPolicyFixture(t)
	ipPolicyExec(t, w, `INSERT INTO metadata_policy_receipts(library_id,provider,command_incarnation,command_sequence,operation_id,request_digest,applied_ms,result_digest,policy_incarnation,accepted_revision,enabled,priority,region,algorithm,refresh_mode,credit_enabled,credit_after_item,credit_after_provider) VALUES(777,'tmdb',X'01000000000000000000000000000000',1,X'05000000000000000000000000000000',X'0600000000000000000000000000000000000000000000000000000000000000',1,X'0700000000000000000000000000000000000000000000000000000000000000',X'09000000000000000000000000000000',2,1,5,'US','v1','fill_missing',1,NULL,NULL)`)
	ipPolicyExec(t, w, `UPDATE metadata_provider_policy SET last_command_sequence=1,acknowledged_command_sequence=0 WHERE provider='tmdb'`)
	cmd = ipPolicyTestCommand(t, 1, 2, nil)
	turn, tx, b = ipPolicyTurn(t, w, r, IPPolicySet)
	if out, err := applyIPPolicySet(t.Context(), turn, p, t.Context(), b, cmd); out != (ipPolicyCommandResult{}) || !errors.Is(err, ErrConflict) {
		t.Fatal("conflicting request admitted", out, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
}

func TestIPRefreshBeginFreshReplayAndRetired(t *testing.T) {
	w, r, p := ipPolicyFixture(t)
	cmd := ipRefreshTestCommand(t, 1, 2, nil)
	turn, tx, b := ipPolicyTurn(t, w, r, IPRefreshBegin)
	out, err := applyIPRefreshBegin(t.Context(), turn, p, t.Context(), b, cmd)
	if err != nil {
		t.Fatal(err)
	}
	if out.Outcome.State != Applied || out.Value.Generation != 1 || out.Value.AcceptedRevision != 1 ||
		out.Value.Phase != "entities" || out.Value.AppliedMS != w.now().UnixMilli() || out.Digest == (ResultDigest{}) {
		t.Fatal(out)
	}
	ipPolicyCommitTurn(t, turn, tx)
	original := out
	// The request is discoverable through the due keyset at the applied time.
	for query, want := range map[string]int64{
		`SELECT job_next_attempt_ms FROM metadata_refresh_sweeps WHERE provider='tmdb' AND purpose='metadata'`: w.now().UnixMilli(),
		`SELECT job_due_ms FROM metadata_refresh_sweeps WHERE provider='tmdb' AND purpose='metadata'`:          w.now().UnixMilli(),
	} {
		if got := ipPolicyQueryInt(t, w, query); got != want {
			t.Fatal(query, got)
		}
	}

	ipPolicyExec(t, w, `UPDATE metadata_providers SET enabled=0 WHERE code='tmdb'`)
	ipPolicyExec(t, w, `UPDATE screen_metadata_consent SET confirmed=0`)
	turn, tx, b = ipPolicyTurn(t, w, r, IPRefreshBegin)
	out, err = applyIPRefreshBegin(t.Context(), turn, p, t.Context(), b, cmd)
	if err != nil || !out.Outcome.Replayed || out.Digest != original.Digest || !ipRefreshResultEqual(out.Value, original.Value) {
		t.Fatal("refresh replay after source change", out, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)

	ipPolicyExec(t, w, `UPDATE metadata_refresh_sweeps SET revision=9 WHERE provider='tmdb' AND purpose='metadata'`)
	turn, tx, b = ipPolicyTurn(t, w, r, IPRefreshBegin)
	out, err = applyIPRefreshBegin(t.Context(), turn, p, t.Context(), b, cmd)
	if err != nil || out.Value.AcceptedRevision != 1 || !out.Outcome.Replayed {
		t.Fatal("refresh replay after sweep advance", out, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)

	ipPolicyExec(t, w, `UPDATE metadata_refresh_sweeps SET last_command_sequence=1,acknowledged_command_sequence=1 WHERE provider='tmdb' AND purpose='metadata'`)
	turn, tx, b = ipPolicyTurn(t, w, r, IPRefreshBegin)
	out, err = applyIPRefreshBegin(t.Context(), turn, p, t.Context(), b, cmd)
	if err != nil || out.Outcome.State != ResultRetired || out.Value != (ipRefreshResult{}) || out.Digest != (ResultDigest{}) {
		t.Fatal("refresh retired result served", out, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
}

func TestIPRefreshBeginSecondRevisionResetsCursor(t *testing.T) {
	w, r, p := ipPolicyFixture(t)
	first := ipRefreshTestCommand(t, 1, 2, nil)
	turn, tx, b := ipPolicyTurn(t, w, r, IPRefreshBegin)
	if _, err := applyIPRefreshBegin(t.Context(), turn, p, t.Context(), b, first); err != nil {
		t.Fatal(err)
	}
	ipPolicyCommitTurn(t, turn, tx)
	ipPolicyExec(t, w, `UPDATE metadata_refresh_sweeps SET after_id=40,page_from_id=10,page_until_id=40,page_waiting=1,acknowledged_command_sequence=1,last_command_sequence=1 WHERE provider='tmdb' AND purpose='metadata'`)
	second := ipRefreshTestCommand(t, 2, 3, func(q *ipRefreshRequest) { q.expectedGeneration, q.expectedRevision = 1, 1 })
	turn, tx, b = ipPolicyTurn(t, w, r, IPRefreshBegin)
	out, err := applyIPRefreshBegin(t.Context(), turn, p, t.Context(), b, second)
	if err != nil {
		t.Fatal(err)
	}
	if out.Value.AcceptedRevision != 2 || out.Value.Generation != 1 || out.Value.AfterID != 0 || out.Value.ScheduledCount != 0 || out.Value.PageWaiting {
		t.Fatal(out)
	}
	ipPolicyCommitTurn(t, turn, tx)
	for query, want := range map[string]int64{
		`SELECT after_id FROM metadata_refresh_sweeps WHERE provider='tmdb' AND purpose='metadata'`:            0,
		`SELECT page_from_id FROM metadata_refresh_sweeps WHERE provider='tmdb' AND purpose='metadata'`:        0,
		`SELECT page_until_id FROM metadata_refresh_sweeps WHERE provider='tmdb' AND purpose='metadata'`:       0,
		`SELECT page_waiting FROM metadata_refresh_sweeps WHERE provider='tmdb' AND purpose='metadata'`:        0,
		`SELECT job_lease_owner FROM metadata_refresh_sweeps WHERE provider='tmdb' AND purpose='metadata'`:     0,
		`SELECT job_next_attempt_ms FROM metadata_refresh_sweeps WHERE provider='tmdb' AND purpose='metadata'`: w.now().UnixMilli(),
		`SELECT job_due_ms FROM metadata_refresh_sweeps WHERE provider='tmdb' AND purpose='metadata'`:          w.now().UnixMilli(),
		`SELECT revision FROM metadata_refresh_sweeps WHERE provider='tmdb' AND purpose='metadata'`:            2,
		`SELECT count(*) FROM metadata_refresh_receipts`:                                                       2,
	} {
		if got := ipPolicyQueryInt(t, w, query); got != want {
			t.Fatal(query, got)
		}
	}
}

func TestIPRefreshBeginStaleDisabledAndConsent(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*ipRefreshRequest)
		sql    []string
		want   error
	}{
		{"stale_revision", func(q *ipRefreshRequest) { q.expectedGeneration, q.expectedRevision = 1, 2 }, []string{`INSERT INTO metadata_refresh_sweeps(provider,purpose,generation,revision,epoch_ms,phase,command_incarnation) VALUES('tmdb','metadata',1,1,1,'entities',X'01000000000000000000000000000000')`}, ErrConflict},
		{"disabled_provider", nil, []string{`UPDATE metadata_providers SET enabled=0 WHERE code='tmdb'`}, ErrDisclosureDenied},
		{"music_consent_mismatch", func(q *ipRefreshRequest) { q.provider, q.consentRevision = "musicbrainz", 1 }, nil, ErrDisclosureDenied},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w, r, p := ipPolicyFixture(t)
			for _, query := range tc.sql {
				ipPolicyExec(t, w, query)
			}
			cmd := ipRefreshTestCommand(t, 1, 2, tc.mutate)
			turn, tx, b := ipPolicyTurn(t, w, r, IPRefreshBegin)
			out, err := applyIPRefreshBegin(t.Context(), turn, p, t.Context(), b, cmd)
			if out != (ipRefreshCommandResult{}) || !errors.Is(err, tc.want) {
				t.Fatal("stale/disabled/consent sweep admitted", out, err)
			}
			ipPolicyRollbackTurn(t, turn, tx)
			if got := ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_refresh_receipts`); got != 0 {
				t.Fatal("denied sweep wrote receipts", got)
			}
		})
	}
	w, r, p := ipPolicyFixture(t)
	cmd := ipRefreshTestCommand(t, 1, 2, func(q *ipRefreshRequest) { q.provider, q.consentRevision = "musicbrainz", 3 })
	turn, tx, b := ipPolicyTurn(t, w, r, IPRefreshBegin)
	if out, err := applyIPRefreshBegin(t.Context(), turn, p, t.Context(), b, cmd); err != nil || out.Outcome.State != Applied {
		t.Fatal("MusicBrainz sweep epoch", out, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
	for name, mutate := range map[string]func(*ipRefreshRequest){
		"wrong_purpose":       func(q *ipRefreshRequest) { q.purpose = "everything" },
		"artwork_music":       func(q *ipRefreshRequest) { q.purpose, q.provider = "artwork", "musicbrainz" },
		"generation_only":     func(q *ipRefreshRequest) { q.expectedGeneration = 1 },
		"zero_consent":        func(q *ipRefreshRequest) { q.consentRevision = 0 },
		"unknown_provider":    func(q *ipRefreshRequest) { q.provider = "plex" },
		"negative_generation": func(q *ipRefreshRequest) { q.expectedGeneration, q.expectedRevision = -1, -1 },
	} {
		t.Run(name, func(t *testing.T) {
			q := ipRefreshRequest{provider: "tmdb", purpose: "metadata", consentRevision: 1}
			mutate(&q)
			if c, err := newIPRefreshCommand(ipPolicyIdentity(1, 2), q); c != nil || !errors.Is(err, ErrInvalid) {
				t.Fatal(c, err)
			}
		})
	}
}

func TestIPPolicyDescriptors(t *testing.T) {
	for _, expected := range []Descriptor{
		{ID: IPPolicySet, Name: "ip_policy_set", Durability: Normal},
		{ID: IPRefreshBegin, Name: "ip_refresh_begin", Durability: Normal},
	} {
		actual, err := Describe(expected.ID)
		if err != nil || actual != expected || actual.ID < 200 || actual.ID > 299 {
			t.Fatal(actual, err)
		}
	}
	if _, err := Describe(IPRefreshBegin); err != nil {
		t.Fatal(err)
	}
}
