package persistence

import (
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/apievents"
)

func TestMembershipAuthorityEventsCommitOnlyRealChanges(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id) VALUES('alice','alice',x'00','alice-main'),('bob','bob',x'00','bob-main')`); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE direct_memberships SET role='admin',revision=revision+1 WHERE account_id='alice'`); err != nil {
		t.Fatal(err)
	}
	var audience, kind, resourceKind, resource, revision, data string
	if err := tx.QueryRow(`SELECT audience,type,resource_kind,resource_id,revision,data FROM api_events WHERE type='authority.changed'`).Scan(&audience, &kind, &resourceKind, &resource, &revision, &data); err != nil {
		t.Fatalf("authority event must exist in the membership transaction: %v", err)
	}
	if audience != "authority:local:alice" || kind != "authority.changed" || resourceKind != "account" || resource != "self" || revision != "2" || data != "" {
		t.Fatalf("authority hint contains more than its own revision: %q %q %q %q %q %q", audience, kind, resourceKind, resource, revision, data)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	count := func(want int) {
		t.Helper()
		var got int
		if err := db.QueryRow(`SELECT count(*) FROM api_events WHERE type='authority.changed'`).Scan(&got); err != nil || got != want {
			t.Fatalf("authority events = %d, want %d: %v", got, want, err)
		}
	}
	count(0)
	for _, query := range []string{
		`UPDATE direct_memberships SET role=role,revision=revision+1 WHERE account_id='alice'`,
		`UPDATE direct_memberships SET notes='A private note',revision=revision+1 WHERE account_id='alice'`,
	} {
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
		count(0)
	}
	if _, err := db.Exec(`UPDATE direct_memberships SET allowed_libraries='["one","two"]',revision=revision+1 WHERE account_id='alice'`); err != nil {
		t.Fatal(err)
	}
	count(1)
	if _, err := db.Exec(`UPDATE direct_memberships SET allowed_libraries='["two", "one"]',revision=revision+1 WHERE account_id='alice'`); err != nil {
		t.Fatal(err)
	}
	count(1) // Ordering and whitespace do not change library access.
	if _, err := db.Exec(`UPDATE direct_memberships SET role='admin',revision=revision+1 WHERE account_id='alice'`); err != nil {
		t.Fatal(err)
	}
	count(2)
	if _, err := db.Exec(`UPDATE direct_memberships SET disabled=1,revision=revision+1 WHERE account_id='alice'`); err != nil {
		t.Fatal(err)
	}
	count(3)
	if _, err := db.Exec(`UPDATE direct_memberships SET disabled=1,revision=revision+1 WHERE account_id='alice'`); err != nil {
		t.Fatal(err)
	}
	count(3)
	if _, err := db.Exec(`UPDATE direct_memberships SET role='admin',revision=revision+1 WHERE account_id='bob'`); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT revision FROM api_events WHERE audience='authority:local:bob'`).Scan(&revision); err != nil || revision != "2" {
		t.Fatalf("another account's changes affected Bob's revision: %q %v", revision, err)
	}
}

func TestMembershipAuthorityEventsKeepTheSharedRingBound(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id) VALUES('alice','alice',x'00','alice-main')`); err != nil {
		t.Fatal(err)
	}
	next := (apievents.RingSize/64 + 1) * 64
	cutoff := next - apievents.RingSize
	if _, err := db.Exec(`INSERT INTO api_events(id,audience,type,at_ms) VALUES(?,'server','test',0),(?,'server','test',0),(?,'server','test',0)`, cutoff, cutoff+1, next-1); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE direct_memberships SET role='admin',revision=revision+1 WHERE account_id='alice'`); err != nil {
		t.Fatal(err)
	}
	var expired, kept int
	if err := db.QueryRow(`SELECT count(*) FROM api_events WHERE id=?`, cutoff).Scan(&expired); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM api_events WHERE id=?`, cutoff+1).Scan(&kept); err != nil {
		t.Fatal(err)
	}
	if expired != 0 || kept != 1 {
		t.Fatalf("shared ring boundary: expired=%d kept=%d", expired, kept)
	}
}
