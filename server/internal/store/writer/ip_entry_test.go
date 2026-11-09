package writer

import (
	"context"
	"errors"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
	"strings"
	"testing"
	"time"
)

type ipEntryRegistry struct {
	*stmt.Registry
	keeper *checkpointComponent
}

func ipEntrySecurityFixture(t *testing.T) (*executor, *checkpointComponent, *usOwnerAuthorityProof) {
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

func ipEntryTurn(t *testing.T, w *executor, r *ipEntryRegistry, q ipEntryRequest) (*commandTurn, ipEntryBudget) {
	t.Helper()
	var seq int
	var name, path string
	if err := w.db.QueryRowContext(t.Context(), `PRAGMA database_list`).Scan(&seq, &name, &path); err != nil {
		t.Fatal(err)
	}
	recipe := testRecipe()
	recipe.limit = Work{10000, 1_000_000, 10000, 4 << 20, 20 * time.Millisecond}
	g, a, wal := r.keeper.g, r.keeper.a, r.keeper.wal
	request := governor.Request{Key: governor.JobKey{ID: "ip/owner-edit"}, Owner: 1, Writer: true, Class: governor.Background, MaxHold: recipe.limit.Hold, Deadline: r.keeper.now().Add(time.Second), Budget: governor.UnitBudget{RawRows: uint32(recipe.limit.Rows), VMOps: recipe.limit.VMOps, Pages: uint32(recipe.limit.Pages)}}
	request.Need[governor.MemoryBytes] = 4 << 20
	request.Need[governor.WALBytes] = int64(recipe.limit.WALBytes)

	request.Key.Domain = uint16(q.purpose)
	request.Key.ID = "ip/owner-edit"
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
	return turn, ipEntryBudget{4 << 20}
}

func ipEntryFixture(t *testing.T, person bool) (*executor, *ipEntryRegistry, *usOwnerAuthorityProof, ipEntryRequest) {
	t.Helper()
	w, keeper, p := ipEntrySecurityFixture(t)
	for _, sql := range []string{
		`INSERT INTO catalog_libraries(id,library_id,root) VALUES(777,'ip-library','/media')`,
		`INSERT OR IGNORE INTO catalog_kinds(id,name,playable,container,searchable,browsable,listening) VALUES(1,'movie',1,0,1,1,0)`,
	} {
		wc008NativeExec(t, w, sql)
	}
	token, inc := [16]byte{8}, [16]byte{4}
	wc008NativeExec(t, w, `INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(778,?,777,1)`, token[:])
	wc008NativeExec(t, w, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation,source_revision,identity_revision,metadata_revision) VALUES(778,?,777,1,'Title','title','t','title','t',0,?,?,1,1,1)`, token[:], inc[:], inc[:])
	wc008NativeExec(t, w, `INSERT INTO catalog_people(id,token,identity_key,name,sort_name,artwork_incarnation) VALUES(778,'person-token','person:key','Person','person',?)`, inc[:])
	q := ipEntryRequest{purpose: IPFieldEdit, action: ipEntryApply, targetID: 778, targetKind: 1, libraryID: 777, libraryKey: "ip-library", reference: ReceiptReference{Identity: CommandIdentity{Incarnation: [16]byte{1}, Sequence: 1, OperationID: [16]byte{2}}, RequestDigest: RequestDigest{3}}}
	if person {
		q.targetKind, q.libraryID, q.libraryKey = 0, 0, ""
	}
	r, err := stmttest.New(t, ipEntryDefinitions())
	if err != nil {
		t.Fatal(err)
	}
	return w, &ipEntryRegistry{r, keeper}, p, q
}
func seedIPEntryHistory(t *testing.T, w *executor, q ipEntryRequest, ack bool) {
	t.Helper()
	var entity, person any
	entity = q.targetID
	if q.targetKind == 0 {
		entity, person = nil, q.targetID
	}
	wc008NativeExec(t, w, `INSERT INTO metadata_edit_owners(id,entity_id,person_id,kind,command_incarnation,last_command_sequence,acknowledged_command_sequence) VALUES(900,?,?,?,?,1,?)`, entity, person, ipEntryOwnerKind(q.targetKind), q.reference.Identity.Incarnation[:], map[bool]int{false: 0, true: 1}[ack])
	kind := map[CommandID]string{IPFieldEdit: "field_edit", IPFieldLock: "field_lock", IPFieldRestore: "field_restore"}[q.purpose]
	wc008NativeExec(t, w, `INSERT INTO metadata_edit_receipts(owner_id,command_incarnation,command_sequence,operation_id,request_digest,applied_ms,result_digest,command_kind,field,accepted_revision,subject_revision,value,value_present,automatic_value,automatic_present,locked,changed,effective_changed) VALUES(900,?,1,?,?,1,?,?,'title',1,1,'',1,'',0,0,0,0)`, q.reference.Identity.Incarnation[:], q.reference.Identity.OperationID[:], q.reference.RequestDigest[:], make([]byte, 32), kind)
}
func TestIPEntryFreshResolveHistory(t *testing.T) {
	for _, person := range []bool{false, true} {
		for _, mode := range []string{"fresh", "resolve_absent", "replay", "retired", "conflict", "missing_header", "owner_retired"} {
			t.Run(fmt.Sprintf("person_%v/%s", person, mode), func(t *testing.T) {
				w, r, p, q := ipEntryFixture(t, person)
				if mode != "fresh" && mode != "resolve_absent" {
					seedIPEntryHistory(t, w, q, mode == "retired")
				}
				if mode == "resolve_absent" {
					q.action = ipEntryResolve
				}
				if mode == "conflict" {
					q.purpose = IPFieldLock
				}
				if mode == "missing_header" {
					wc008NativeExec(t, w, `DELETE FROM metadata_edit_receipts`)
				}
				if mode == "owner_retired" {
					wc008NativeExec(t, w, `UPDATE metadata_edit_owners SET retired_ms=1`)
				}
				if mode == "replay" || mode == "retired" {
					wc008NativeExec(t, w, `UPDATE catalog_entities SET retired=1; UPDATE catalog_libraries SET retired=1`)
					q.action = ipEntryResolve
				}
				turn, b := ipEntryTurn(t, w, r, q)
				entry, err := enterIPCommand(t.Context(), turn, q, p, t.Context(), b)
				if mode == "resolve_absent" || mode == "missing_header" {
					if entry != nil || !errors.Is(err, ErrResolveRequired) {
						t.Fatal(entry, err)
					}
					return
				}
				if mode == "conflict" {
					if entry != nil || !errors.Is(err, ErrConflict) {
						t.Fatal(entry, err)
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
				if err != nil || entry == nil || entry.decision != want {
					t.Fatal(entry, err)
				}
				if mode != "fresh" && entry.ownerID != 900 {
					t.Fatal("entity/person owner collision")
				}
				if want != receiptFresh && !errors.Is(entry.checkFreshProvider(t.Context(), turn, "tmdb", [16]byte{9}, 1, 1), ErrInvalid) {
					t.Fatal("historical result reached fresh policy gate")
				}
			})
		}
	}
}
func TestIPEntryCurrentAuthorityBeforeHistory(t *testing.T) {
	for _, mutation := range []string{`UPDATE direct_memberships SET disabled=1`, `UPDATE authorization_family_tokens SET retired=1`, `DELETE FROM authorization_family_security_fences`, `INSERT INTO reset_file_sessions(family_id,kind) VALUES('issuer-family','owner')`} {
		t.Run(mutation, func(t *testing.T) {
			w, r, p, q := ipEntryFixture(t, false)
			seedIPEntryHistory(t, w, q, true)
			wc008NativeExec(t, w, mutation)
			registry, err := stmttest.New(t, usAuthorityDefinitions())
			if err != nil {
				t.Fatal(err)
			}
			r.Registry = registry
			turn, b := ipEntryTurn(t, w, r, q)
			entry, err := enterIPCommand(t.Context(), turn, q, p, t.Context(), b)
			if entry != nil || !errors.Is(err, ErrDisclosureDenied) {
				t.Fatal(entry, err)
			}
		})
	}
}
func TestIPEntryNativePlans(t *testing.T) {
	w, _, _, _ := ipEntryFixture(t, false)
	for _, d := range ipEntryDefinitions() {
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
func TestIPEntryAdmissionAndRecheck(t *testing.T) {
	w, r, p, q := ipEntryFixture(t, false)
	turn, b := ipEntryTurn(t, w, r, q)
	low := b
	low.memory = 1
	if entry, err := enterIPCommand(t.Context(), turn, q, p, t.Context(), low); entry != nil || err == nil {
		t.Fatal(entry, err)
	}
	forged := *p
	forged.witness.issuer.family = "forged"
	if entry, err := enterIPCommand(t.Context(), turn, q, &forged, t.Context(), b); entry != nil || !errors.Is(err, ErrDisclosureDenied) {
		t.Fatal(entry, err)
	}
	old := turn.transaction.custody
	turn.transaction.custody = &testCustody{}
	if entry, err := enterIPCommand(t.Context(), turn, q, p, t.Context(), b); entry != nil || err == nil {
		t.Fatal(entry, err)
	}
	turn.transaction.custody = old
	entry, err := enterIPCommand(t.Context(), turn, q, p, t.Context(), b)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []any{entry, entry.authority} {
		for _, format := range []string{"%v", "%+v", "%#v"} {
			s := fmt.Sprintf(format, v)
			if !strings.Contains(s, "redacted") || strings.Contains(s, p.witness.issuer.tokenDigest) || strings.Contains(s, p.witness.installationID) {
				t.Fatal("entry revealed witness")
			}
		}
	}
	if _, err = turn.transaction.conn.ExecContext(t.Context(), `UPDATE identity_security_fences SET denied=1`); err != nil {
		t.Fatal(err)
	}
	if err = entry.authority.recheck(t.Context(), turn); !errors.Is(err, ErrDisclosureDenied) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if out, err := enterIPCommand(ctx, turn, q, p, t.Context(), b); out != nil || err == nil {
		t.Fatal(out, err)
	}
}
func TestIPEntryFreshSubjectAndProviderConsent(t *testing.T) {
	w, r, p, q := ipEntryFixture(t, false)
	for _, sql := range []string{
		`INSERT INTO metadata_providers(code,adapter,enabled) VALUES('tmdb','tmdb',1),('musicbrainz','musicbrainz',1)`,
		`INSERT INTO metadata_provider_policy(library_id,provider,incarnation,revision,enabled,priority,region,algorithm,refresh_mode) VALUES(777,'tmdb',X'09000000000000000000000000000000',1,1,1,'US','v1','fill_missing'),(777,'musicbrainz',X'09000000000000000000000000000000',1,1,1,'US','v1','fill_missing')`,
		`INSERT INTO screen_metadata_consent(singleton,revision,confirmed) VALUES(1,1,0) ON CONFLICT(singleton) DO UPDATE SET revision=1,confirmed=0`,
		`INSERT INTO metadata_runtime_settings(singleton,online_lookups,lookup_epoch) VALUES(1,1,3) ON CONFLICT(singleton) DO UPDATE SET online_lookups=1,lookup_epoch=3`,
	} {
		wc008NativeExec(t, w, sql)
	}
	turn, b := ipEntryTurn(t, w, r, q)
	entry, err := enterIPCommand(t.Context(), turn, q, p, t.Context(), b)
	if err != nil {
		t.Fatal(err)
	}
	if err = entry.checkFreshSubject(t.Context(), turn, [16]byte{4}, 1, 1, 1, ""); err != nil {
		t.Fatal(err)
	}
	if err = entry.checkFreshSubject(t.Context(), turn, [16]byte{4}, 2, 1, 1, ""); !errors.Is(err, ErrDisclosureDenied) {
		t.Fatal(err)
	}
	if err = entry.checkFreshProvider(t.Context(), turn, "tmdb", [16]byte{9}, 1, 1); !errors.Is(err, ErrDisclosureDenied) {
		t.Fatal(err)
	}
	if err = entry.checkFreshProvider(t.Context(), turn, "musicbrainz", [16]byte{9}, 1, 3); err != nil {
		t.Fatal("MusicBrainz must not borrow screen consent", err)
	}
	if err = entry.checkFreshProvider(t.Context(), turn, "musicbrainz", [16]byte{9}, 2, 3); !errors.Is(err, ErrDisclosureDenied) {
		t.Fatal(err)
	}
}
func TestIPEntryUnknownCommitInvalidatesStamp(t *testing.T) {
	w, r, p, q := ipEntryFixture(t, false)
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
	turn, b := ipEntryTurn(t, w, r, q)
	entry, err := enterIPCommand(t.Context(), turn, q, p, t.Context(), b)
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
	if out, err := enterIPCommand(t.Context(), turn, q, p, t.Context(), b); out != nil || err == nil {
		t.Fatal(out, err)
	}
}
