package metadata

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

func TestLibraryAgentSettingsHintCommitsAtomicallyAndSkipsNoOps(t *testing.T) {
	for name, change := range map[string]func(context.Context, *sql.Tx) error{
		"agent selection":    func(ctx context.Context, tx *sql.Tx) error { return ApplyLibraryAgentTx(ctx, tx, "lib", AgentLocal) },
		"provider selection": func(ctx context.Context, tx *sql.Tx) error { return syncLibraryAgentTx(ctx, tx, "lib", false) },
	} {
		t.Run(name, func(t *testing.T) {
			_, db, _ := screenFixture(t)
			ctx := context.Background()
			state := func() (revision int64, hint string) {
				t.Helper()
				if err := db.QueryRow(`SELECT revision FROM console_settings_revision WHERE singleton=1`).Scan(&revision); err != nil {
					t.Fatal(err)
				}
				if err := db.QueryRow(`SELECT COALESCE((SELECT revision FROM api_change_pending WHERE audience='server' AND type='settings.changed' AND resource_kind='settings' AND resource_id='server'),'')`).Scan(&hint); err != nil {
					t.Fatal(err)
				}
				return
			}
			before, oldHint := state()
			rollback := errors.New("roll back the agent change")
			err := dbwork.WithWriteTx(ctx, db, dbwork.ClassInteractive, func(tx *sql.Tx) error {
				if err := change(ctx, tx); err != nil {
					return err
				}
				return rollback
			})
			if !errors.Is(err, rollback) {
				t.Fatal(err)
			}
			if revision, hint := state(); revision != before || hint != oldHint {
				t.Fatalf("rolled-back change emitted a hint: %d %q", revision, hint)
			}
			if err := dbwork.WithWriteTx(ctx, db, dbwork.ClassInteractive, func(tx *sql.Tx) error { return change(ctx, tx) }); err != nil {
				t.Fatal(err)
			}
			if revision, hint := state(); revision != before+1 || hint != fmt.Sprint(revision) {
				t.Fatalf("committed agent change omitted its settings hint: revision=%d before=%d hint=%q", revision, before, hint)
			}
			after, changedHint := state()
			if err := dbwork.WithWriteTx(ctx, db, dbwork.ClassInteractive, func(tx *sql.Tx) error { return change(ctx, tx) }); err != nil {
				t.Fatal(err)
			}
			if revision, hint := state(); revision != after || hint != changedHint {
				t.Fatalf("no-op agent choice emitted another hint: %d %q", revision, hint)
			}
		})
	}
}
