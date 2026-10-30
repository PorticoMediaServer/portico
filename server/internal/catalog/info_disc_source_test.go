package catalog

import (
	"context"
	"reflect"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

func TestInfoDiscSourcePublishesNormalizedStructureAndKeepsSurvivingTokens(t *testing.T) {
	s, db, _, job, object, policy, probe := discPublicationFixture(t)
	var revision string
	if err := db.QueryRow(`SELECT revision FROM inventory_objects WHERE id=?`, object).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	c := catalogtest.New(t, db)
	publish := func() {
		t.Helper()
		receipt, err := acceptDiscProbeForTest(t, s, context.Background(), job, object, revision, policy, probe)
		if err != nil || receipt.State != compactcatalog.InfoInputAcceptedPending {
			t.Fatal("disc source receipt", receipt, err)
		}
		c.Drain()
		got, err := ReadInventoryDiscProbe(context.Background(), db, object, revision)
		if err != nil || !reflect.DeepEqual(got, probe) {
			t.Fatal("normalized disc structure differs", got, probe, err)
		}
	}
	publish()
	identities := map[string]string{}
	rows, err := db.Query(`SELECT title_key,selection_token FROM inventory_disc_titles WHERE object_id=?`, object)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var key, token string
		if err := rows.Scan(&key, &token); err != nil {
			t.Fatal(err)
		}
		identities[key] = token
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	// Reordering display positions changes the complete observed fingerprint,
	// but each surviving title's selection token keeps its exact identity.
	probe.Titles[0].DurationMs += 600000
	publish()
	rows, err = db.Query(`SELECT title_key,selection_token FROM inventory_disc_titles WHERE object_id=?`, object)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var key, token string
		if err := rows.Scan(&key, &token); err != nil {
			t.Fatal(err)
		}
		if token != identities[key] {
			t.Fatal("surviving title token replaced", key, token, identities[key])
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	receipt, err := acceptDiscProbeForTest(t, s, context.Background(), job, object, revision, policy, probe)
	if err != nil || receipt.State != compactcatalog.InfoInputApplied || receipt.ChangeID != 0 {
		t.Fatal("same structure wasn't an applied no-op", receipt, err)
	}
}
