package operations

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/usermutation"
)

func settingsMutationContext(s *Store, p identity.Principal, n int, operation, digest string) (context.Context, string) {
	key := fmt.Sprintf("p251_%013d_%032x", s.now(), n)
	b := usermutation.Binding{Authority: p.Authority, AccountID: p.AccountID, ProfileID: p.ProfileID, FamilyID: "family"}
	return usermutation.WithRequest(dbwork.WithDurableUser(context.Background()), b, usermutation.Envelope{Key: key, Operation: operation, Digest: strings.Repeat(digest, 64)}), key
}

func TestSettingsMutationReplayReadsCurrentAndKeepsTinyReceipt(t *testing.T) {
	s, p, auth := consoleFixture(t)
	before, err := s.Settings(context.Background(), auth)
	if err != nil {
		t.Fatal(err)
	}
	ctx, key := settingsMutationContext(s, p, 1, "change_console_settings", "a")
	values := before.Requested
	values.Name = "first"
	command := SettingsChange{ExpectedRevision: before.Revision, IdempotencyKey: key, Values: values}
	first, err := s.ApplySettings(ctx, p, auth, command)
	if err != nil {
		t.Fatal(err)
	}
	values.Name = "current"
	current, err := s.ApplySettings(context.Background(), p, auth, SettingsChange{ExpectedRevision: first.Revision, IdempotencyKey: "legacy-settings", Values: values})
	if err != nil {
		t.Fatal(err)
	}
	replay, err := s.ApplySettings(ctx, p, auth, command)
	if err != nil || replay.Revision != current.Revision || replay.Requested.Name != "current" {
		t.Fatalf("replay current state: %+v %v", replay, err)
	}
	if command.ExpectedRevision != before.Revision {
		t.Fatal("original expected revision rewritten")
	}
	var legacy, receipts int
	if err = s.DB.QueryRow(`SELECT count(*) FROM console_receipts WHERE key=?`, key).Scan(&legacy); err != nil || legacy != 0 {
		t.Fatalf("full response receipt: %d %v", legacy, err)
	}
	if err = s.DB.QueryRow(`SELECT count(*) FROM user_mutation_receipts WHERE key=? AND status='applied' AND revision=?`, key, first.Revision).Scan(&receipts); err != nil || receipts != 1 {
		t.Fatalf("tiny receipt: %d %v", receipts, err)
	}
	changed, _ := settingsMutationContext(s, p, 1, "change_console_settings", "b")
	command.Values.Name = "must not apply"
	if _, err = s.ApplySettings(changed, p, auth, command); !errors.Is(err, usermutation.ErrConflict) {
		t.Fatalf("changed request accepted: %v", err)
	}
	deny := func(context.Context, *sql.Tx, string) error { return identity.ErrForbidden }
	if _, err = s.ApplySettings(ctx, p, deny, command); !errors.Is(err, identity.ErrForbidden) {
		t.Fatalf("replay skipped current authority: %v", err)
	}
}

func TestSettingsMutationReceiptFailureRollsBackDomainAndReservation(t *testing.T) {
	s, p, auth := consoleFixture(t)
	before, err := s.Settings(context.Background(), auth)
	if err != nil {
		t.Fatal(err)
	}
	ctx, key := settingsMutationContext(s, p, 2, "change_console_settings", "a")
	if _, err = s.DB.Exec(`CREATE TRIGGER reject_settings_receipt BEFORE UPDATE OF status ON user_mutation_receipts WHEN NEW.status='applied' BEGIN SELECT RAISE(ABORT,'receipt write failed'); END`); err != nil {
		t.Fatal(err)
	}
	values := before.Requested
	values.Name = "rollback"
	_, err = s.ApplySettings(ctx, p, auth, SettingsChange{ExpectedRevision: before.Revision, IdempotencyKey: key, Values: values})
	if err == nil {
		t.Fatal("receipt failure reported success")
	}
	after, err := s.Settings(context.Background(), auth)
	if err != nil || after.Revision != before.Revision || after.Requested.Name != before.Requested.Name {
		t.Fatalf("domain survived receipt failure: %+v %v", after, err)
	}
	var count int
	if err = s.DB.QueryRow(`SELECT count(*) FROM user_mutation_receipts WHERE key=?`, key).Scan(&count); err != nil || count != 0 {
		t.Fatalf("reservation survived rollback: %d %v", count, err)
	}
}

func TestPreferenceMutationReplayReadsCurrentWithoutRevisionRewrite(t *testing.T) {
	s, p, auth := consoleFixture(t)
	ctx, key := settingsMutationContext(s, p, 3, "patch_preferences", "a")
	c := PreferenceChange{Scope: ScopeProfileServer, DeviceClass: "web", ExpectedRevision: 1, IdempotencyKey: key, Values: PreferencePatch{"music.audioNormalization": "album"}}
	first, err := s.ApplyPreferences(ctx, p, auth, c)
	if err != nil {
		t.Fatal(err)
	}
	var revision int64
	for _, d := range first.Documents {
		if d.Scope == ScopeProfileServer {
			revision = d.Revision
		}
	}
	_, err = s.ApplyPreferences(context.Background(), p, auth, PreferenceChange{Scope: c.Scope, DeviceClass: c.DeviceClass, ExpectedRevision: revision, IdempotencyKey: "new-preference", Values: PreferencePatch{"music.audioNormalization": "track"}})
	if err != nil {
		t.Fatal(err)
	}
	replay, err := s.ApplyPreferences(ctx, p, auth, c)
	if err != nil || replay.Effective.Text("music.audioNormalization") != "track" || c.ExpectedRevision != 1 {
		t.Fatalf("replay: %+v %v", replay, err)
	}
	var count int
	if err = s.DB.QueryRow(`SELECT count(*) FROM console_receipts WHERE key=?`, key).Scan(&count); err != nil || count != 0 {
		t.Fatalf("full response preference receipt: %d %v", count, err)
	}
}

func TestSettingsMutationCapacityRefusesBeforeEffect(t *testing.T) {
	s, p, auth := consoleFixture(t)
	before, err := s.Settings(context.Background(), auth)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec(`UPDATE user_mutation_global SET ordinary_count=65536`); err != nil {
		t.Fatal(err)
	}
	ctx, key := settingsMutationContext(s, p, 4, "change_console_settings", "a")
	values := before.Requested
	values.Name = "must not apply"
	_, err = s.ApplySettings(ctx, p, auth, SettingsChange{ExpectedRevision: before.Revision, IdempotencyKey: key, Values: values})
	if !errors.Is(err, usermutation.ErrCapacity) {
		t.Fatalf("capacity refusal: %v", err)
	}
	after, err := s.Settings(context.Background(), auth)
	if err != nil || after.Revision != before.Revision || after.Requested.Name != before.Requested.Name {
		t.Fatalf("capacity refusal applied settings: %+v %v", after, err)
	}
	var n int
	if err = s.DB.QueryRow(`SELECT count(*) FROM user_mutation_receipts WHERE key=?`, key).Scan(&n); err != nil || n != 0 {
		t.Fatalf("refused receipt remained %d %v", n, err)
	}
}
