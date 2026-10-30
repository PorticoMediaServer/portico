package preferenceownership_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/preferenceownership"
	"testing"
)

func TestErasureIsQualifiedAndCascadesOrderedItems(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db, err := persistence.Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	store := operations.New(db)
	auth := func(context.Context, *sql.Tx, string) error { return nil }
	viewers := []identity.Viewer{{Authority: "local", AccountID: "one", ProfileID: "first"}, {Authority: "local", AccountID: "one", ProfileID: "second"}, {Authority: "other", AccountID: "one", ProfileID: "first"}, {Authority: "local", AccountID: "two", ProfileID: "first"}}
	for _, v := range viewers {
		if _, err = store.ApplyPreferences(ctx, identity.Principal{Viewer: v}, auth, operations.PreferenceChange{Scope: operations.ScopeProfileServer, DeviceClass: "web", ExpectedRevision: 1, IdempotencyKey: identity.Token(), Values: operations.PreferencePatch{"playback.preferredAudioLanguages": []string{"fr", "en"}}}); err != nil {
			t.Fatal(err)
		}
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = preferenceownership.EraseProfile(ctx, tx, "local", "one", "first"); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	for i, v := range viewers {
		prefs, _, _, e := operations.EffectivePreferences(db, v, "")
		if e != nil {
			t.Fatal(e)
		}
		first := prefs.List("playback.preferredAudioLanguages")[0]
		if (i == 0 && first != "@appLanguage") || (i != 0 && first != "fr") {
			t.Fatal(i, first)
		}
	}
	tx, err = db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = preferenceownership.EraseAccount(ctx, tx, "local", "one"); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var scopes, values, items int
	if err = db.QueryRow(`SELECT count(*) FROM preference_scopes`).Scan(&scopes); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`SELECT count(*) FROM preference_values`).Scan(&values); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`SELECT count(*) FROM preference_list_items`).Scan(&items); err != nil {
		t.Fatal(err)
	}
	if scopes != 2 || values != 2 || items != 4 {
		t.Fatal(scopes, values, items)
	}
}
