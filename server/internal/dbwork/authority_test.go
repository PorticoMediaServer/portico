package dbwork

import (
	"path/filepath"
	"testing"
)

// The authority generation is what fences every cached authorisation answer in
// the server: the principal cache, the library-grant cache, the restriction
// cache, and the verdict a parked byte stream holds. Two properties have to hold
// at once, and they pull in opposite directions.
//
// It must move for every write that could change who may see or play what — a
// missed bump is a revoked session still being served. And it must not move for
// writes that cannot — a false bump costs nothing by itself, but a *frequent*
// false bump costs the caches entirely, which is eight statements and a pooled
// connection per request for an answer that had not changed.
//
// This pins both halves against the statements the server actually issues.
func TestAuthorityWritesAreRecognisedByTheTableTheyName(t *testing.T) {
	for _, statement := range []string{
		`INSERT INTO authorization_family_tokens(token_hash,family_id,generation,expires_at) VALUES(?,?,1,?)`,
		`DELETE FROM authorization_family_tokens WHERE token_hash=?`,
		`UPDATE accounts SET epoch=epoch+1 WHERE id=?`,
		`INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES(?,?,x'00',?,1)`,
		`UPDATE direct_profiles SET allowed_libraries=? WHERE id=?`,
		`INSERT INTO direct_memberships(account_id,role,allowed_libraries,revision,disabled) VALUES(?,'member',?,1,0)`,
		`INSERT INTO profile_restrictions(profile_id,maximum_age,blocked_labels,allow_unrated,revision) VALUES(?,?,?,0,1)`,
		`INSERT INTO authorization_session_families(id,server_id,account_id,profile_id) VALUES(?,?,?,?)`,
		`UPDATE authorization_family_tokens SET retired=1 WHERE token_hash=?`,
		`INSERT INTO dvr_private_libraries(library_id) VALUES(?)`,
		`REPLACE INTO onboarding_state_v1(singleton,auth_mode) VALUES(1,?)`,
		`DELETE FROM api_credentials WHERE id=?`,
		`DELETE FROM libraries WHERE id=?`,
		`DELETE FROM library_sources WHERE id=?`,
		`UPDATE library_sources SET enabled=0 WHERE id=?`,
		`update policy set revision=revision+1 where server_id=?`,
		`UPDATE download_grants SET revoked=1 WHERE preparation_id=?`,
		`DELETE FROM download_grants WHERE preparation_id=?`,
	} {
		before := AuthorityGeneration()
		noticeAuthorityWrite(statement)
		if AuthorityGeneration() == before {
			t.Errorf("an authority write did not move the generation: %s", statement)
		}
	}
}

func TestOrdinaryWritesLeaveTheAuthorityGenerationAlone(t *testing.T) {
	for _, statement := range []string{
		// These two were the whole of the smoke tier's authority churn: a
		// substring rule found `revoked` in a lease's status and `policy` in a
		// delivery plan's column list, and emptied every authority cache in the
		// server a hundred and thirty-eight times a second.
		`UPDATE playback_viewer_leases SET status='revoked',counted=0 WHERE playback_id=?`,
		`INSERT INTO playback_delivery_plans(session_id,profile,source_id,policy,revision) VALUES(?,?,?,?,?)`,
		`INSERT INTO playback_sessions(id,item_id,profile_id,generation) VALUES(?,?,?,?)`,
		`UPDATE playback_sessions SET position=? WHERE id=?`,
		`INSERT INTO configuration(key,value) VALUES(?,?)`,
		`UPDATE configuration SET value=? WHERE key=?`,
		`INSERT INTO jobs(id,library_id,status,created_at) VALUES(?,?,?,?)`,
		`UPDATE jobs SET status=? WHERE id=?`,
		`UPDATE download_preparations SET bytes_done=?,hash_state=?,updated_ms=? WHERE id=?`,
		`INSERT INTO api_events(audience,type,at_ms) VALUES(?,?,?)`,
		`SELECT count(*) FROM authorization_access WHERE hash=?`,
	} {
		before := AuthorityGeneration()
		noticeAuthorityWrite(statement)
		if AuthorityGeneration() != before {
			t.Errorf("an ordinary write moved the authority generation: %s", statement)
		}
	}
}

// A shape the head reader cannot parse falls back to the older, over-inclusive
// rule rather than to silence. A false bump costs a cache miss; a missed bump
// costs a revoked session.
func TestUnreadableWriteShapesStayConservative(t *testing.T) {
	before := AuthorityGeneration()
	noticeAuthorityWrite(`insert or rollback intoo authorization_family_tokens values(1)`)
	if AuthorityGeneration() == before {
		t.Fatal("a write shape that could not be parsed was treated as harmless")
	}
}

func TestWrittenTableReadsTheHeadOfAWrite(t *testing.T) {
	for _, row := range []struct{ statement, table string }{
		{`insert into accounts(id) values(?)`, "accounts"},
		{`insert or ignore into browse_entity_membership(entity_id,item_id,source) select 1,2,3`, "browse_entity_membership"},
		{`replace into onboarding_state_v1 values(1)`, "onboarding_state_v1"},
		{`update or replace authorization_session_families set revoked=1`, "authorization_session_families"},
		{`delete from content_rating_pending where value_key=?`, "content_rating_pending"},
		{`update main.accounts set epoch=1`, "accounts"},
		{`update "authorization_session_families" set revoked=1`, "authorization_session_families"},
	} {
		got, ok := writtenTable(row.statement)
		if !ok || got != row.table {
			t.Errorf("%s named %q (%v), expected %q", row.statement, got, ok, row.table)
		}
	}
}

// Ending a session is written as UPDATE … RETURNING, which the database driver
// is handed as a query, not as an exec. It moves the generation all the same,
// on whatever lane it runs: a session revoked from an ordinary request must not
// stay in the principal cache until its entry expires. A plain read through
// the same path moves nothing.
func TestAWriteThatReturnsRowsMovesTheAuthorityGeneration(t *testing.T) {
	db, err := OpenHandle(filepath.Join(t.TempDir(), "authority.db"), DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`CREATE TABLE authorization_session_families(id TEXT PRIMARY KEY, revoked INTEGER NOT NULL DEFAULT 0)`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO authorization_session_families(id) VALUES('family')`); err != nil {
		t.Fatal(err)
	}
	before := AuthorityGeneration()
	var id string
	if err = db.QueryRow(`SELECT id FROM authorization_session_families WHERE revoked=0`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if AuthorityGeneration() != before {
		t.Fatal("reading sessions moved the authority generation")
	}
	if err = db.QueryRow(`UPDATE authorization_session_families SET revoked=1 WHERE id=? AND revoked=0 RETURNING id`, "family").Scan(&id); err != nil || id != "family" {
		t.Fatalf("%q %v", id, err)
	}
	if AuthorityGeneration() == before {
		t.Fatal("a session was ended by a statement that returns rows and the authority generation did not move")
	}
}
