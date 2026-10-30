package remotesources

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/remotemedia"
)

func TestDAVConnectionTestDoesNotGrantFromMissingOwnerOrBlockedEndpoint(t *testing.T) {
	f, _, identities, owner, family, _ := nativeSaveFixture(t)
	ctx := context.Background()
	in := DAVConnectionInput{Name: "Archive", Root: "https://127.0.0.1/media/", Username: "fixture", Password: "fixture-only"}
	for _, authorize := range []func(*sql.Tx) error{nil, func(tx *sql.Tx) error { return currentLocalOwnerTx(ctx, tx, identities, owner) }} {
		out, err := f.s.TestDAVConnection(ctx, in, authorize)
		if err == nil || out.RangeSupport != remotemedia.SetupRangeUnknown || out.Origin != "" {
			t.Fatal("missing authority or blocked endpoint produced a connection fact", out, err)
		}
	}
	denied := errors.New("current owner refused")
	if _, err := f.s.TestDAVConnection(ctx, in, func(*sql.Tx) error { return denied }); !errors.Is(err, denied) {
		t.Fatal("test hid current authority refusal", err)
	}
	if _, err := f.s.db.Exec(`UPDATE authorization_session_families SET revoked=1 WHERE id=?`, family); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.TestDAVConnection(ctx, in, func(tx *sql.Tx) error { return currentLocalOwnerTx(ctx, tx, identities, owner) }); err == nil {
		t.Fatal("revoked owner started a connection test")
	}
	var operations int
	if err := f.s.db.QueryRow(`SELECT count(*) FROM remote_source_operations`).Scan(&operations); err != nil || operations != 0 {
		t.Fatal("unsaved connection test created a command receipt", operations, err)
	}
}
