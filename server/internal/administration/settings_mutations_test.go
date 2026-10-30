package administration

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/usermutation"
)

func administrationMutation(t *testing.T, s *Service, n int, op string) (context.Context, string) {
	t.Helper()
	if _, err := s.db.Exec(`INSERT OR IGNORE INTO accounts(id,username,password_hash,profile_id) VALUES('owner','owner',X'00','primary')`); err != nil {
		t.Fatal(err)
	}
	key := fmt.Sprintf("p251_%013d_%032x", s.milliseconds(), n)
	return usermutation.WithRequest(dbwork.WithDurableUser(context.Background()), usermutation.Binding{Authority: "local", AccountID: "owner", ProfileID: "primary", FamilyID: "family"}, usermutation.Envelope{Key: key, Operation: op, Digest: strings.Repeat("a", 64)}), key
}

func TestLiveSettingsMutationPreparationAndReplay(t *testing.T) {
	s, db, _ := newService(t)
	source := channelLookupSource(t, db)
	before, err := s.LiveSourceConfiguration(context.Background(), allow, source)
	if err != nil {
		t.Fatal(err)
	}
	ctx, key := administrationMutation(t, s, 1, "set_live_source_configuration")
	c := Change[LiveSourceSettings]{ExpectedRevision: before.Revision, OperationID: key, Settings: before.Settings}
	accepts := 0
	accept := func(context.Context, *sql.Tx, string, LiveSourceSettings) error { accepts++; return nil }
	first, err := s.SaveLiveSourceConfiguration(ctx, allow, source, c, accept)
	if err != nil {
		t.Fatal(err)
	}
	if accepts != 1 {
		t.Fatalf("accept calls %d", accepts)
	}
	replay, err := s.SaveLiveSourceConfiguration(ctx, allow, source, c, accept)
	if err != nil || replay.Revision != first.Revision || accepts != 1 {
		t.Fatalf("replay revision %d, accepts %d: %v", replay.Revision, accepts, err)
	}
	var n int
	if err = db.QueryRow(`SELECT count(*) FROM user_mutation_receipts WHERE key=? AND status='applied'`, key).Scan(&n); err != nil || n != 1 {
		t.Fatalf("preflight reservation not finalized: %d %v", n, err)
	}
	if err = db.QueryRow(`SELECT count(*) FROM admin_receipts WHERE operation_id=?`, key).Scan(&n); err != nil || n != 0 {
		t.Fatalf("full response receipt persisted: %d %v", n, err)
	}
}

func TestDVRSettingsMutationReplayReadsCurrent(t *testing.T) {
	s, db, _ := newService(t)
	before, err := s.DVRSettings(context.Background(), allow)
	if err != nil {
		t.Fatal(err)
	}
	ctx, key := administrationMutation(t, s, 2, "put_admin_dvr_settings")
	c := Change[DVRDefaults]{ExpectedRevision: before.Revision, OperationID: key, Settings: before.Settings}
	first, err := s.SaveDVRSettings(ctx, allow, c)
	if err != nil {
		t.Fatal(err)
	}
	next := first.Settings
	next.PrePaddingMs += 1000
	current, err := s.SaveDVRSettings(context.Background(), allow, Change[DVRDefaults]{ExpectedRevision: first.Revision, OperationID: "legacy-dvr-settings", Settings: next})
	if err != nil {
		t.Fatal(err)
	}
	replay, err := s.SaveDVRSettings(ctx, allow, c)
	if err != nil || replay.Revision != current.Revision || replay.Settings.PrePaddingMs != current.Settings.PrePaddingMs {
		t.Fatalf("replay %+v: %v", replay, err)
	}
	var n int
	if err = db.QueryRow(`SELECT count(*) FROM admin_receipts WHERE operation_id=?`, key).Scan(&n); err != nil || n != 0 {
		t.Fatalf("full response receipt persisted: %d %v", n, err)
	}
}
