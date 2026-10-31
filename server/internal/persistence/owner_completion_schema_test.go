package persistence

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

func TestOwnerCompletionStoredTupleConstraintsAndFamilyCascade(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db, err := OpenFresh(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO accounts(id,username,password_hash,profile_id) VALUES('owner','owner',X'','profile')`)
	// The account's canonical creation trigger already owns its primary profile.
	var primary string
	if err := db.QueryRow(`SELECT id FROM direct_profiles WHERE account_id='owner' AND is_primary=1`).Scan(&primary); err != nil || primary != "profile" {
		t.Fatalf("account creation did not supply its real primary profile: %s: %v", primary, err)
	}
	exec(`INSERT INTO authorization_session_families(id,server_id,account_id,profile_id,authority,role,epoch,authorization_horizon,current_generation) VALUES('family','server','owner','profile','local','owner',1,'2099-01-01T00:00:00Z',1)`)
	server := "srv_" + strings.Repeat("A", 43)
	exec(`INSERT INTO networking_server_identities(server_id,public_key,key_incarnation,created_at) VALUES(?,zeroblob(32),'incarnation','2026-10-23T00:00:00Z')`, server)
	exec(`INSERT INTO networking_claim_intents(operation_id,server_id,account_id,public_key,local_generation,revision,stage,owner_id,owner_profile_id,owner_epoch,created_at,updated_at) VALUES('operation',?,'hosted-owner',zeroblob(32),1,1,'prepared','owner','profile',1,'2026-10-23T00:00:00Z','2026-10-23T00:00:00Z')`, server)
	exec(`INSERT INTO identity_moves(id,owner_id,owner_revision,from_kind,to_kind,start_over,stage,revision,people,devices,profiles,server_name,created_ms) VALUES('move','owner',1,'local','portico',0,'preview',1,1,1,1,'Home',1000)`)
	exec(`INSERT INTO identity_owner_completions(operation_id,move_id,family_id,token_generation,owner_id,owner_profile_id,owner_epoch,phase,claim_hash,claim_payload,claim_signature,start_payload,start_signature,state_hash,code_challenge,installation_id,server_challenge_hash,created_ms,expires_ms,move_revision) VALUES('operation','move','family',1,'owner','profile',1,'prepared',?,X'01',zeroblob(64),X'02',zeroblob(64),'state',?,'installation','challenge',1000,2000,1)`, strings.Repeat("A", 43), strings.Repeat("B", 43))
	exec(`INSERT INTO networking_claim_intents(operation_id,server_id,account_id,public_key,local_generation,revision,stage,owner_id,owner_profile_id,owner_epoch,created_at,updated_at) VALUES('other-operation',?,'hosted-owner',zeroblob(32),1,1,'cancelled','owner','profile',1,'2026-10-23T00:00:00Z','2026-10-23T00:00:00Z')`, server)
	if _, err := db.Exec(`INSERT INTO identity_owner_completions(operation_id,move_id,family_id,token_generation,owner_id,owner_profile_id,owner_epoch,phase,claim_hash,claim_payload,claim_signature,start_payload,start_signature,state_hash,code_challenge,installation_id,server_challenge_hash,created_ms,expires_ms,move_revision)
 SELECT 'other-operation',move_id,family_id,token_generation,owner_id,owner_profile_id,owner_epoch,phase,claim_hash,claim_payload,claim_signature,start_payload,start_signature,state_hash,code_challenge,installation_id,server_challenge_hash,created_ms,expires_ms,move_revision FROM identity_owner_completions WHERE operation_id='operation'`); err == nil || !strings.Contains(err.Error(), "identity_owner_completions.family_id") {
		t.Fatalf("one family acquired a second completion operation: %v", err)
	}
	var phase, attempt, transaction, registration, origin string
	var installed, consumed, completed, generation int64
	if err := db.QueryRow(`SELECT phase,attempt_id,transaction_id,registration_id,registration_origin,installed_ms,consumed_ms,completed_ms,route_generation FROM identity_owner_completions WHERE operation_id='operation'`).Scan(&phase, &attempt, &transaction, &registration, &origin, &installed, &consumed, &completed, &generation); err != nil || phase != "prepared" || attempt != "" || transaction != "" || registration != "" || origin != "" || installed != 0 || consumed != 0 || completed != 0 || generation != 0 {
		t.Fatalf("uncaptured completion invented runtime facts: %s/%s/%s/%s/%s/%d/%d/%d/%d: %v", phase, attempt, transaction, registration, origin, installed, consumed, completed, generation, err)
	}
	for _, change := range []string{"phase='active'", "phase='unknown'", "token_generation=0", "owner_epoch=0", "move_revision=0", "route_generation=-1", "expires_ms=created_ms", "claim_hash='short'", "code_challenge='short'", "claim_signature=zeroblob(63)", "start_signature=zeroblob(65)", "family_id='missing'", "owner_id='missing'", "owner_profile_id='missing'", "move_id='missing'", "operation_id='missing'"} {
		if _, err := db.Exec(`UPDATE identity_owner_completions SET ` + change + ` WHERE operation_id='operation'`); err == nil {
			t.Errorf("accepted invalid completion tuple: %s", change)
		}
	}
	for _, phase := range []string{"prepared", "installed", "consumed", "completed", "cancelled", "expired", "superseded"} {
		exec(`UPDATE identity_owner_completions SET phase=? WHERE operation_id='operation'`, phase)
	}
	exec(`UPDATE identity_owner_completions SET phase='consumed',consumed_ms=1500,attempt_id='one-attempt',transaction_id='transaction',registration_id='actual-registration',registration_origin='https://server.example',route_generation=7 WHERE operation_id='operation'`)
	if err := db.QueryRow(`SELECT consumed_ms,completed_ms FROM identity_owner_completions WHERE operation_id='operation'`).Scan(&consumed, &completed); err != nil || consumed != 1500 || completed != 0 {
		t.Fatalf("consumption became completion: %d/%d: %v", consumed, completed, err)
	}
	for _, grant := range []string{"cast_target", "owner_claim_completion"} {
		exec(`INSERT INTO authorization_family_grants(family_id,grant_kind) VALUES('family',?) ON CONFLICT(family_id) DO UPDATE SET grant_kind=excluded.grant_kind`, grant)
	}
	if _, err := db.Exec(`UPDATE authorization_family_grants SET grant_kind='future_unknown_grant' WHERE family_id='family'`); err == nil {
		t.Fatal("unknown narrowed grant entered durable storage")
	}
	var count sql.NullInt64
	var countPayload, countSignature []byte
	if err := db.QueryRow(`SELECT m.other_people_count,c.switch_count_payload,c.switch_count_signature FROM identity_moves m JOIN identity_owner_completions c ON c.move_id=m.id WHERE c.operation_id='operation'`).Scan(&count, &countPayload, &countSignature); err != nil || count.Valid || countPayload != nil || countSignature != nil {
		t.Fatalf("uncaptured count invented a scalar or proof: %v/%x/%x: %v", count, countPayload, countSignature, err)
	}
	for _, value := range []any{int64(0), int64(10000), nil} {
		exec(`UPDATE identity_moves SET other_people_count=? WHERE id='move'`, value)
	}
	for _, value := range []any{int64(-1), int64(10001), 0.5} {
		if _, err := db.Exec(`UPDATE identity_moves SET other_people_count=? WHERE id='move'`, value); err == nil {
			t.Fatalf("invalid captured count entered storage: %v", value)
		}
	}
	for _, invalid := range []struct{ payload, signature []byte }{
		{[]byte{1}, nil}, {nil, make([]byte, 64)}, {[]byte{}, make([]byte, 64)},
		{make([]byte, 8193), make([]byte, 64)}, {[]byte{1}, make([]byte, 63)}, {[]byte{1}, make([]byte, 65)},
	} {
		if _, err := db.Exec(`UPDATE identity_owner_completions SET switch_count_payload=?,switch_count_signature=? WHERE operation_id='operation'`, invalid.payload, invalid.signature); err == nil {
			t.Fatal("partial or out-of-bounds optional count proof entered storage")
		}
	}
	exec(`UPDATE identity_owner_completions SET switch_count_payload=?,switch_count_signature=? WHERE operation_id='operation'`, []byte{1}, make([]byte, 64))
	exec(`UPDATE identity_owner_completions SET switch_count_payload=NULL,switch_count_signature=NULL WHERE operation_id='operation'`)
	exec(`DELETE FROM authorization_session_families WHERE id='family'`)
	for _, table := range []string{"identity_owner_completions", "authorization_family_grants"} {
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&n); err != nil || n != 0 {
			t.Fatalf("family retirement left %s: %d: %v", table, n, err)
		}
	}
}
