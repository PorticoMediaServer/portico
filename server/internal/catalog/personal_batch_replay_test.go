package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

func TestPersonalBatchReplayUsesCurrentVisibility(t *testing.T) {
	db, s, names := phase34ListFixture(t)
	viewer := Viewer{Profile: "p", Libraries: []string{"m"}}
	checks := 0
	authorize := func(tx *sql.Tx, item string) error {
		checks++
		ctx, err := dbwork.BindSnapshot(context.Background(), tx)
		if err != nil {
			return err
		}
		return s.WithContext(ctx).VisibleItem(ctx, viewer, item)
	}
	yes := true
	stale := int64(99)
	request := PersonalBatchMutation{OperationID: "visibility-replay", Items: []PersonalBatchItem{
		{ItemID: names["a"].Public, Favorite: &yes},
		{ItemID: names["b"].Public, ExpectedRevision: &stale, Favorite: &yes},
		{ItemID: names["c"].Public, Watchlisted: &yes},
	}}
	first, err := s.SetPersonalBatch("local:account", "p", request, authorize)
	if err != nil || first.Updated != 2 || first.Failed != 1 || first.Results[0].Personal == nil || first.Results[1].Personal == nil {
		t.Fatalf("initial receipt: %+v %v", first, err)
	}
	var stored string
	if err := db.QueryRow(`SELECT response FROM personal_batch_receipts WHERE profile_id='p' AND operation_id=?`, request.OperationID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	c := catalogtest.New(t, db)
	for _, name := range []string{"a", "b"} {
		c.Attributes(names[name].ID, "label", "Blocked")
	}
	viewer.Restrictions = identity.ContentRestrictions{BlockedLabels: []string{"blocked"}}
	checks = 0
	replay, err := s.SetPersonalBatch("local:account", "p", request, authorize)
	if err != nil || checks != 3 || replay.Updated != 1 || replay.Failed != 2 {
		t.Fatalf("current authorization not applied to every stored result: %+v checks=%d %v", replay, checks, err)
	}
	for _, row := range replay.Results[:2] {
		if row.OK || row.Personal != nil || row.Code != "not_found" || row.Message != sql.ErrNoRows.Error() {
			t.Fatalf("stored personal state disclosed: %+v", row)
		}
	}
	if !replay.Results[2].OK || replay.Results[2].Personal == nil || !replay.Results[2].Personal.Watchlisted {
		t.Fatalf("visible receipt changed: %+v", replay.Results[2])
	}
	var retained string
	if err := db.QueryRow(`SELECT response FROM personal_batch_receipts WHERE profile_id='p' AND operation_id=?`, request.OperationID).Scan(&retained); err != nil || retained != stored {
		t.Fatalf("redaction rewrote immutable receipt: %v", err)
	}
	var revision int64
	if err := db.QueryRow(`SELECT revision FROM personal_items WHERE profile_id='p' AND item_id=?`, names["a"].ID).Scan(&revision); err != nil || revision != first.Results[0].Personal.Revision {
		t.Fatalf("replay changed personal state: %d %v", revision, err)
	}
	viewer.Restrictions = identity.ContentRestrictions{}
	restored, err := s.SetPersonalBatch("local:account", "p", request, authorize)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(restored)
	if err != nil || string(raw) != stored {
		t.Fatalf("re-authorized replay did not return original outcome: %+v %v", restored, err)
	}
}

func TestPersonalBatchReplayFailsOnAuthorizationError(t *testing.T) {
	_, s, names := phase34ListFixture(t)
	yes := true
	request := PersonalBatchMutation{OperationID: "failed-visibility-replay", Items: []PersonalBatchItem{{ItemID: names["a"].Public, Favorite: &yes}}}
	if _, err := s.SetPersonalBatch("local:account", "p", request, nil); err != nil {
		t.Fatal(err)
	}
	broken := errors.New("visibility storage failure")
	result, err := s.SetPersonalBatch("local:account", "p", request, func(*sql.Tx, string) error { return broken })
	if !errors.Is(err, broken) || len(result.Results) != 0 {
		t.Fatalf("authorization failure returned receipt data: %+v %v", result, err)
	}
}
