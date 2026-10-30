package social

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"strings"
	"testing"
)

func TestJoinedParticipantsRemainVisibleInsideTheirRoom(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db, err := persistence.Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := New(db)
	p := identity.Principal{Viewer: identity.Viewer{Authority: "local", AccountID: "account", ProfileID: "profile"}}
	if _, err = db.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id) VALUES('account','identity_username',x'00','profile')`); err != nil {
		t.Fatal(err)
	}
	group := seedGroup(t, s, p)
	readMembers := func() []Member {
		t.Helper()
		tx, err := db.BeginTx(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		g, err := loadGroup(context.Background(), tx, group)
		if err != nil {
			t.Fatal(err)
		}
		members, _, err := s.members(context.Background(), tx, g)
		if err != nil {
			t.Fatal(err)
		}
		return members
	}
	if members := readMembers(); len(members) != 1 || members[0].Username != "identity_username" {
		t.Fatalf("public snapshot: %+v", members)
	}
	ctx := context.Background()
	publish := func() {
		t.Helper()
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		if err = s.publish(ctx, tx, group, EventReadiness, map[string]any{"memberId": "mem_seeded", "positionUs": "500"}); err != nil {
			t.Fatal(err)
		}
		if err = tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	publish()
	if _, err = operations.New(db).ApplyPreferences(ctx, p, func(context.Context, *sql.Tx, string) error { return nil }, operations.PreferenceChange{Scope: operations.ScopeProfileServer, DeviceClass: "web", ExpectedRevision: 1, IdempotencyKey: "private", Values: operations.PreferencePatch{"privacy.includeInWatchTogether": false}}); err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	g, err := loadGroup(ctx, tx, group)
	if err != nil {
		t.Fatal(err)
	}
	members, summary, err := s.members(ctx, tx, g)
	tx.Rollback()
	if err != nil || len(members) != 1 || summary.MemberCount != 1 {
		t.Fatal(members, summary, err)
	}
	if !members[0].Private || members[0].DisplayName != "Viewer" || members[0].Username != "" {
		t.Fatal("private member was not anonymized", members)
	}
	raw, err := json.Marshal(members)
	if err != nil || strings.Contains(string(raw), "identity_username") || strings.Contains(string(raw), "username") {
		t.Fatalf("private snapshot: %s %v", raw, err)
	}
	publish()
	events, _, err := s.EventsSince(ctx, p, group, 0)
	if err != nil || len(events) != 2 {
		t.Fatal(events, err)
	}
	for _, event := range events {
		if strings.Contains(string(event.Payload), "identity_username") || strings.Contains(string(event.Payload), "username") {
			t.Fatal("private event leaked username", string(event.Payload))
		}
	}
	if strings.Contains(string(events[0].Payload), "500") || !strings.Contains(string(events[0].Payload), "mem_seeded") {
		t.Fatal("privacy did not hide personal activity while preserving room occupancy", string(events[0].Payload))
	}
	if _, err = operations.New(db).ApplyPreferences(ctx, p, func(context.Context, *sql.Tx, string) error { return nil }, operations.PreferenceChange{Scope: operations.ScopeProfileServer, DeviceClass: "web", ExpectedRevision: 2, IdempotencyKey: "public", Values: operations.PreferencePatch{"privacy.includeInWatchTogether": true}}); err != nil {
		t.Fatal(err)
	}
	publish()
	events, _, err = s.EventsSince(ctx, p, group, 0)
	if err != nil || len(events) != 3 {
		t.Fatal(events, err)
	}
	for _, event := range events {
		if !strings.Contains(string(event.Payload), `"username":"identity_username"`) {
			t.Fatal("public event missing username", string(event.Payload))
		}
	}
	if members := readMembers(); members[0].Username != "identity_username" {
		t.Fatal("public snapshot missing username", members)
	}
}
