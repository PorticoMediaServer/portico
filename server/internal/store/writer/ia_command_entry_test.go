package writer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

type iaEntryRegistry struct {
	*stmt.Registry
	keeper *checkpointComponent
}

func iaEntrySecurityFixture(t *testing.T) (*executor, *checkpointComponent, *usOwnerAuthorityProof) {
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
	if _, err := w.db.ExecContext(t.Context(), usAuthorityDraftDDL); err != nil {
		t.Fatal(err)
	}
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

func iaEntryFixture(t *testing.T) (*executor, *iaEntryRegistry, *usOwnerAuthorityProof, iaEntryRequest) {
	t.Helper()
	w, keeper, p := iaEntrySecurityFixture(t)
	ddl, err := os.ReadFile("testdata/ia-entry-draft.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = w.db.ExecContext(t.Context(), string(ddl)); err != nil {
		t.Fatal(err)
	}
	wc008NativeExec(t, w, `INSERT INTO catalog_libraries(id,library_id,root) VALUES(777,'ia-library','/media')`)
	q := iaEntryRequest{purpose: IAConfigureLibrary, action: iaEntryApply, libraryID: 777, libraryKey: "ia-library", reference: ReceiptReference{Identity: CommandIdentity{Incarnation: [16]byte{1}, Sequence: 1, OperationID: [16]byte{2}}, RequestDigest: RequestDigest{3}}}
	r, err := stmttest.New(t, iaEntryDefinitions())
	if err != nil {
		t.Fatal(err)
	}
	return w, &iaEntryRegistry{r, keeper}, p, q
}
func iaEntryTurn(t *testing.T, w *executor, r *iaEntryRegistry, q iaEntryRequest) (*commandTurn, iaEntryBudget) {
	t.Helper()
	var seq int
	var name, path string
	if err := w.db.QueryRowContext(t.Context(), `PRAGMA database_list`).Scan(&seq, &name, &path); err != nil {
		t.Fatal(err)
	}
	recipe := testRecipe()
	recipe.limit = Work{10000, 1_000_000, 10000, 4 << 20, 20 * time.Millisecond}
	g, a, wal := r.keeper.g, r.keeper.a, r.keeper.wal
	request := governor.Request{Key: governor.JobKey{ID: "ia/configuration"}, Owner: 1, Writer: true, Class: governor.Background, MaxHold: recipe.limit.Hold, Deadline: r.keeper.now().Add(time.Second), Budget: governor.UnitBudget{RawRows: uint32(recipe.limit.Rows), VMOps: recipe.limit.VMOps, Pages: uint32(recipe.limit.Pages)}}
	request.Need[governor.MemoryBytes] = 4 << 20
	request.Need[governor.WALBytes] = int64(recipe.limit.WALBytes)

	request.Key.Domain = uint16(q.purpose)
	request.Key.ID = "ia/configuration"
	request.Class = governor.Background
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
	c, err := bindGovernedTurn(q.purpose, recipe.limit, g, a, wal, grant.Grant, request)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := w.begin(t.Context(), q.purpose, recipe, c)
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
	return turn, iaEntryBudget{4 << 20}
}
func seedIAEntryHistory(t *testing.T, w *executor, q iaEntryRequest, ack bool) {
	t.Helper()
	d, _ := Describe(q.purpose)
	var source any
	if q.sourceID != "" {
		source = q.sourceID
	}
	wc008NativeExec(t, w, `INSERT INTO catalog_configuration_operations(original_operation_id,original_request_digest,command_incarnation,command_kind,original_library_id,original_library_key,original_source_id,last_command_sequence,acknowledged_command_sequence) VALUES(?,?,?,?,?,?,?,1,?)`, q.reference.Identity.OperationID[:], q.reference.RequestDigest[:], q.reference.Identity.Incarnation[:], d.Name, q.libraryID, q.libraryKey, source, map[bool]int{false: 0, true: 1}[ack])
	var libraryName, kind any
	charge := int64(160 + len(d.Name))
	if q.purpose == IAConfigureLibrary {
		libraryName, kind = "Saved name", "movies"
		charge += 16
	}
	wc008NativeExec(t, w, `INSERT INTO catalog_configuration_receipts(original_operation_id,command_incarnation,command_sequence,operation_id,request_digest,command_kind,result_version,accepted_revision,applied_ms,result_library_name,result_library_kind,result_count,result_digest,retained_bytes) VALUES(?,?,1,?,?,?,1,1,1,?,?,1,?,?)`, q.reference.Identity.OperationID[:], q.reference.Identity.Incarnation[:], q.reference.Identity.OperationID[:], q.reference.RequestDigest[:], d.Name, libraryName, kind, make([]byte, 32), charge)
}
func TestIAEntryFreshResolveHistory(t *testing.T) {
	for _, mode := range []string{"fresh", "resolve_absent", "replay", "retired", "retired_conflict", "missing_header", "changed_source"} {
		t.Run(mode, func(t *testing.T) {
			w, r, p, q := iaEntryFixture(t)
			if mode == "changed_source" {
				q.purpose = IAConfigureSource
				q.sourceID = "now-removed"
			}
			if mode != "fresh" && mode != "resolve_absent" {
				seedIAEntryHistory(t, w, q, strings.HasPrefix(mode, "retired"))
			}
			if mode == "resolve_absent" {
				q.action = iaEntryResolve
			}
			if mode == "missing_header" {
				wc008NativeExec(t, w, `DELETE FROM catalog_configuration_receipts`)
			}
			if mode == "retired_conflict" {
				q.reference.RequestDigest[0]++
			}
			if mode == "replay" || mode == "retired" || mode == "changed_source" {
				wc008NativeExec(t, w, `UPDATE catalog_libraries SET retired=1 WHERE id=777`)
				q.action = iaEntryResolve
			}
			turn, b := iaEntryTurn(t, w, r, q)
			out, err := enterIACommand(t.Context(), turn, q, p, t.Context(), b)
			if mode == "resolve_absent" || mode == "missing_header" {
				if out != nil || !errors.Is(err, ErrResolveRequired) {
					t.Fatal(out, err)
				}
				return
			}
			if mode == "retired_conflict" {
				if out != nil || !errors.Is(err, ErrConflict) {
					t.Fatal(out, err)
				}
				return
			}
			want := receiptReplay
			if mode == "fresh" {
				want = receiptFresh
			}
			if mode == "retired" {
				want = receiptRetired
			}
			if err != nil || out == nil || out.decision != want {
				t.Fatal(out, err)
			}
			if want != receiptFresh && !errors.Is(out.checkFreshSource(t.Context(), turn, 1, "old", "/old", "old"), ErrInvalid) {
				t.Fatal("history reached fresh source gate")
			}
		})
	}
}
func TestIAEntryCurrentAuthorityBeforeHistory(t *testing.T) {
	for name, mutation := range map[string]string{"owner": "UPDATE direct_memberships SET role='member'", "lifetime": "UPDATE direct_profiles SET lifetime='dddddddddddddddddddddddddddddddd'", "token": "UPDATE authorization_family_tokens SET retired=1", "reset": "INSERT INTO reset_file_sessions(family_id,kind) VALUES('issuer-family','owner')", "fence": "DELETE FROM authorization_family_security_fences", "narrow": "INSERT INTO authorization_family_grants(family_id,grant_kind) VALUES('issuer-family','cast_target')"} {
		t.Run(name, func(t *testing.T) {
			w, r, p, q := iaEntryFixture(t)
			seedIAEntryHistory(t, w, q, true)
			wc008NativeExec(t, w, mutation)
			// No history statements: current denial must precede ALL retained lookup.
			registry, err := stmttest.New(t, usAuthorityDefinitions())
			if err != nil {
				t.Fatal(err)
			}
			r.Registry = registry
			turn, b := iaEntryTurn(t, w, r, q)
			out, err := enterIACommand(t.Context(), turn, q, p, t.Context(), b)
			if out != nil || !errors.Is(err, ErrDisclosureDenied) {
				t.Fatal(out, err)
			}
		})
	}
}
func TestIAEntryAdmissionAndFinalRecheck(t *testing.T) {
	w, r, p, q := iaEntryFixture(t)
	turn, b := iaEntryTurn(t, w, r, q)
	low := b
	low.memory = 1
	if out, err := enterIACommand(t.Context(), turn, q, p, t.Context(), low); out != nil || err == nil {
		t.Fatal(out, err)
	}
	fake := *p
	fake.witness.issuer.family = "forged"
	if out, err := enterIACommand(t.Context(), turn, q, &fake, t.Context(), b); out != nil || !errors.Is(err, ErrDisclosureDenied) {
		t.Fatal(out, err)
	}
	custody := turn.transaction.custody
	turn.transaction.custody = &testCustody{}
	if out, err := enterIACommand(t.Context(), turn, q, p, t.Context(), b); out != nil || err == nil {
		t.Fatal(out, err)
	}
	turn.transaction.custody = custody
	out, err := enterIACommand(t.Context(), turn, q, p, t.Context(), b)
	if err != nil {
		t.Fatal(err)
	}
	if err = out.authority.recheck(t.Context(), &commandTurn{transaction: turn.transaction}); !errors.Is(err, ErrDisclosureDenied) {
		t.Fatal(err)
	}
	if _, err = turn.transaction.conn.ExecContext(t.Context(), `UPDATE identity_security_fences SET denied=1`); err != nil {
		t.Fatal(err)
	}
	if err = out.authority.recheck(t.Context(), turn); !errors.Is(err, ErrDisclosureDenied) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if got, err := enterIACommand(ctx, turn, q, p, t.Context(), b); got != nil || err == nil {
		t.Fatal(got, err)
	}
}
func TestIAEntryDescriptorAndPurposeIdentity(t *testing.T) {
	for id, want := range map[CommandID]string{100: "configure_library", 101: "configure_source", 102: "retire_library", 103: "retire_source", 104: "retire_step", 110: "episode_inventory_accept", 111: "audio_inventory_accept", 112: "reading_inventory_accept", 113: "extra_inventory_accept", 114: "membership_accept"} {
		d, err := Describe(id)
		if err != nil || d.Name != want {
			t.Fatal(d, err)
		}
	}
	//110 and114 may have identical link images, but ORIGINAL purpose is exclusive.
	ref := receiptIdentity{command: CommandIdentity{Incarnation: [16]byte{1}, Sequence: 1, OperationID: [16]byte{2}}, digest: RequestDigest{3}}
	owner := receiptOwnerFacts{exists: true, incarnation: ref.command.Incarnation, lastSequence: 1}
	found := receiptFacts{exists: true, identity: ref, kind: IAAcceptEpisodeInventory, resultCount: 1}
	if _, err := classifyReceipt(ref, IAAcceptMembership, owner, found); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
}
func TestIAEntryNativePlans(t *testing.T) {
	w, _, _, _ := iaEntryFixture(t)
	for _, d := range iaEntryDefinitions() {
		t.Run(string(d.ID), func(t *testing.T) {
			rows, err := w.db.QueryContext(t.Context(), "EXPLAIN QUERY PLAN "+d.SQL, d.Cohorts[0].Bindings...)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			for rows.Next() {
				var id, parent, unused int
				var detail string
				if err = rows.Scan(&id, &parent, &unused, &detail); err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(detail, "SEARCH ") || strings.Contains(detail, "TEMP B-TREE") {
					t.Fatal(detail)
				}
			}
			if err = rows.Err(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestIAEntryUnknownCommitInvalidatesStamp(t *testing.T) {
	w, r, p, q := iaEntryFixture(t)
	var seq int
	var name, path string
	if err := w.db.QueryRowContext(t.Context(), `PRAGMA database_list`).Scan(&seq, &name, &path); err != nil {
		t.Fatal(err)
	}
	if err := w.db.Close(); err != nil {
		t.Fatal(err)
	}
	connector, err := sqliteconn.Connector(path, w.config)
	if err != nil {
		t.Fatal(err)
	}
	fault := &faultConnector{Connector: connector, afterCommit: true, replyError: true}
	installFaultPool(w, fault)
	turn, b := iaEntryTurn(t, w, r, q)
	entry, err := enterIACommand(t.Context(), turn, q, p, t.Context(), b)
	if err != nil {
		t.Fatal(err)
	}
	result, err := turn.transaction.commit()
	if result.state != commitUnknown || !errors.Is(err, ErrResolveRequired) {
		t.Fatal(result, err)
	}
	if err = entry.authority.recheck(t.Context(), turn); !errors.Is(err, ErrDisclosureDenied) {
		t.Fatal(err)
	}
	if out, err := enterIACommand(t.Context(), turn, q, p, t.Context(), b); out != nil || err == nil {
		t.Fatal(out, err)
	}
}

func TestIAEntryFreshSourceAndRetiredLibrary(t *testing.T) {
	for _, retired := range []bool{false, true} {
		t.Run(map[bool]string{false: "source_cas", true: "retired"}[retired], func(t *testing.T) {
			w, r, p, q := iaEntryFixture(t)
			q.purpose = IAConfigureSource
			q.sourceID = "source"
			wc008NativeExec(t, w, `INSERT INTO libraries(id,name,kind,root) VALUES('ia-library','Library','movie','/media')`)
			wc008NativeExec(t, w, `INSERT INTO library_sources(id,library_id,configured_root,root,incarnation,root_identity) VALUES('source','ia-library','/media','/media','incarnation','native')`)
			if retired {
				wc008NativeExec(t, w, `UPDATE catalog_libraries SET retired=1 WHERE id=777`)
			}
			turn, b := iaEntryTurn(t, w, r, q)
			out, err := enterIACommand(t.Context(), turn, q, p, t.Context(), b)
			if retired {
				if out != nil || !errors.Is(err, ErrConflict) {
					t.Fatal(out, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if err = out.checkFreshSource(t.Context(), turn, 1, "incarnation", "/media", "native"); err != nil {
				t.Fatal(err)
			}
			if err = out.checkFreshSource(t.Context(), turn, 2, "incarnation", "/media", "native"); !errors.Is(err, ErrConflict) {
				t.Fatal(err)
			}
		})
	}
}

func TestIAEntryRedaction(t *testing.T) {
	w, r, p, q := iaEntryFixture(t)
	turn, b := iaEntryTurn(t, w, r, q)
	entry, err := enterIACommand(t.Context(), turn, q, p, t.Context(), b)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []any{entry, entry.authority} {
		for _, format := range []string{"%v", "%+v", "%#v"} {
			text := fmt.Sprintf(format, value)
			if !strings.Contains(text, "redacted") || strings.Contains(text, p.witness.issuer.tokenDigest) || strings.Contains(text, p.witness.installationID) {
				t.Fatal("entry revealed authority witness")
			}
		}
	}
}
