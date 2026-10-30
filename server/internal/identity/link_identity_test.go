package identity

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func TestLocalLinkIdentityUsesCurrentPublicationAndOmitsPortico(t *testing.T) {
	s, db, _ := familyFixture(t)
	ctx := context.Background()
	read := func() (string, error) {
		tx, e := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
		if e != nil {
			return "", e
		}
		defer tx.Rollback()
		return LocalLinkServerIDTx(ctx, tx)
	}
	id, e := read()
	if e != nil || id != s.ID() || id != "srv_Wl4E3ID18LNZFgw_Gjc3R0liIOV67Gd2vD54Ubw6OAY" {
		t.Fatal("link did not carry current published identity", e)
	}
	// An unrelated configuration copy never becomes the link's expected ID.
	if _, e = db.Exec(`UPDATE configuration SET value='untrusted-copy' WHERE key='id'`); e != nil {
		t.Fatal(e)
	}
	again, e := read()
	if e != nil || again != id {
		t.Fatal("configuration replaced current link identity", e)
	}
	if _, e = db.Exec(`UPDATE accounts SET password_hash=x'';UPDATE identity_state SET sign_in_kind='portico' WHERE singleton=1`); e != nil {
		t.Fatal(e)
	}
	if id, e = read(); e != nil || id != "" {
		t.Fatal("Portico link acquired Local identity metadata", e)
	}
}

func TestLocalLinkNeverManufacturesIdentityFromAnAddressOrConfiguration(t *testing.T) {
	db, e := persistence.Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	if _, e = db.Exec(`INSERT INTO identity_state(singleton,sign_in_kind) VALUES(1,'local');INSERT INTO configuration(key,value) VALUES('id','srv_Wl4E3ID18LNZFgw_Gjc3R0liIOV67Gd2vD54Ubw6OAY')`); e != nil {
		t.Fatal(e)
	}
	tx, e := db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	if id, e := LocalLinkServerIDTx(context.Background(), tx); e == nil || id != "" {
		t.Fatal("unpublished Local identity accepted")
	}
}
