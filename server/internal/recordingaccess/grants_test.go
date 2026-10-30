package recordingaccess

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func grantsFixture(t *testing.T) (*sql.DB, *sql.Tx) {
	t.Helper()
	db, err := persistence.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	// The first account is the owner; the second is an ordinary member.
	if _, err = db.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('owner','owner',x'00','primary',1);
 INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('member','member',x'00','member-primary',1);
 INSERT INTO direct_profiles(id,account_id,name,is_primary) VALUES('child','owner','Child',0);
 INSERT INTO live_source_identities VALUES('source');`); err != nil {
		t.Fatal(err)
	}
	var role string
	if err = db.QueryRow(`SELECT role FROM direct_memberships WHERE account_id='owner'`).Scan(&role); err != nil || role != "owner" {
		t.Fatalf("fixture owner membership: %q %v", role, err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tx.Rollback() })
	return db, tx
}

// Who can record is each profile's own Recordings switch. Only the owner's main
// profile records by default; nothing is inherited from the account; a change
// is fenced by the restriction revision; a retired profile never records.
func TestRecordingPermissionIsTheProfileRecordingsSwitch(t *testing.T) {
	_, tx := grantsFixture(t)
	ctx := context.Background()
	owner := livechannels.Owner{Authority: "local", AccountID: "owner", ProfileID: "primary"}
	child := livechannels.Owner{Authority: "local", AccountID: "owner", ProfileID: "child"}
	member := livechannels.Owner{Authority: "local", AccountID: "member", ProfileID: "member-primary"}
	if err := GrantedTx(ctx, tx, owner); err != nil {
		t.Fatal("the owner's main profile records by default", err)
	}
	for name, o := range map[string]livechannels.Owner{"owner's other profile": child, "a member": member, "a hosted owner": {Authority: "hosted", AccountID: "owner", ProfileID: "primary"}} {
		if err := GrantedTx(ctx, tx, o); !errors.Is(err, livechannels.ErrDenied) {
			t.Fatalf("%s records without the owner's choice: %v", name, err)
		}
	}
	listed, err := ReadGrantsTx(ctx, tx)
	if err != nil || len(listed) != 1 || listed[0].Owner != owner || listed[0].Revision != 1 {
		t.Fatalf("enabled list: %+v %v", listed, err)
	}

	saved, err := SaveGrantTx(ctx, tx, Grant{Owner: member, Enabled: true, Revision: 1})
	if err != nil || !saved.Enabled || saved.Revision != 2 {
		t.Fatalf("owner allows a member: %+v %v", saved, err)
	}
	if err = GrantedTx(ctx, tx, member); err != nil {
		t.Fatal(err)
	}
	if r, err := identity.RestrictionsTx(ctx, tx, "member-primary"); err != nil || !r.AllowDVR || !r.AllowDownloads || !r.AllowLiveTV {
		t.Fatalf("Who can record is the profile's own switch and keeps its other restrictions: %+v %v", r, err)
	}
	if _, err = SaveGrantTx(ctx, tx, Grant{Owner: member, Enabled: false, Revision: 1}); !errors.Is(err, livechannels.ErrConflict) {
		t.Fatal("a stale revision must be refused", err)
	}
	// Allowing the owner's account never lets its other profiles record.
	if err = GrantedTx(ctx, tx, child); !errors.Is(err, livechannels.ErrDenied) {
		t.Fatal("a profile inherited recording from its account", err)
	}
	// Switching the owner's own main profile off is honoured.
	if _, err = SaveGrantTx(ctx, tx, Grant{Owner: owner, Enabled: false, Revision: 1}); err != nil {
		t.Fatal(err)
	}
	if err = GrantedTx(ctx, tx, owner); !errors.Is(err, livechannels.ErrDenied) {
		t.Fatal("a switched-off owner profile still records", err)
	}
	if _, err = tx.Exec(`UPDATE direct_profiles SET deleted=1 WHERE id='member-primary'`); err != nil {
		t.Fatal(err)
	}
	if err = GrantedTx(ctx, tx, member); !errors.Is(err, livechannels.ErrDenied) {
		t.Fatal("a retired profile kept its permission", err)
	}
	if _, err = SaveGrantTx(ctx, tx, Grant{Owner: member, Enabled: true, Revision: 2}); !errors.Is(err, livechannels.ErrInvalid) {
		t.Fatal("a retired profile accepted a change", err)
	}
}
