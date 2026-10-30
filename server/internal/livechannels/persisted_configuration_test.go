package livechannels

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

func TestPersistedConfigurationRejectsChangedPolicyInputs(t *testing.T) {
	for _, mutation := range []string{"approvals", "credentials", "removed"} {
		t.Run(mutation, func(t *testing.T) {
			s := testStore(t)
			ctx := context.Background()
			input := testInput()
			if _, err := s.Save(ctx, testAuthority("owner", true, true), input); err != nil {
				t.Fatal(err)
			}
			raw := `{"draft":{"kind":"xtream","guideUrl":"","username":"old-user","refreshMs":3600000},"approvals":[]}`
			if _, err := s.db.Exec(`INSERT INTO live_remote_configs(source_id,sealed,next_due_ms) VALUES(?,?,0)`, input.ID, []byte(raw)); err != nil {
				t.Fatal(err)
			}
			var revision int64
			if err := s.db.QueryRow(`SELECT revision FROM live_sources WHERE id=?`, input.ID).Scan(&revision); err != nil {
				t.Fatal(err)
			}
			snapshot, err := dbwork.BeginSnapshot(ctx, s.db)
			if err != nil {
				t.Fatal(err)
			}
			defer snapshot.Rollback()
			pending, err := ReadPersistedConfigurationSnapshotTx(ctx, snapshot.Tx(), input.ID)
			if err != nil {
				t.Fatal(err)
			}
			if err = snapshot.Commit(); err != nil {
				t.Fatal(err)
			}
			access, enabled := "server-members", false
			value := LiveSourceSettings{ViewerAccess: &access, Enabled: &enabled, Remote: &RemoteConfiguration{
				Kind: "xtream", GuideURL: "http://192.168.10.23/guide.xml", RefreshMs: 3600000,
				ConfirmedLANRoots: []string{"http://192.168.10.23/"},
			}}
			prepared, err := pending.Prepare(ctx, &value)
			if err != nil {
				t.Fatal(err)
			}
			// A refresh or another owner can change private state without changing
			// the admin document. Even unchanged source revisions cannot hide it.
			changed := `{"draft":{"kind":"xtream","guideUrl":"","username":"new-user","refreshMs":3600000},"approvals":[]}`
			if mutation == "approvals" {
				changed = `{"draft":{"kind":"xtream","guideUrl":"","username":"old-user","refreshMs":3600000},"approvals":[{"root":"http://192.168.10.24/","addresses":["192.168.10.24"]}]}`
			}
			if mutation == "removed" {
				_, err = s.db.Exec(`DELETE FROM live_remote_configs WHERE source_id=?`, input.ID)
			} else {
				_, err = s.db.Exec(`UPDATE live_remote_configs SET sealed=? WHERE source_id=?`, []byte(changed), input.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			err = dbwork.WithWriteTx(ctx, s.db, dbwork.ClassInteractive, func(tx *sql.Tx) error {
				return SavePersistedConfigurationTx(ctx, tx, input.ID, &value, prepared)
			})
			if !errors.Is(err, ErrConflict) {
				t.Fatal("changed policy inputs accepted", err)
			}
			var current int64
			var state string
			if err = s.db.QueryRow(`SELECT revision,state FROM live_sources WHERE id=?`, input.ID).Scan(&current, &state); err != nil || current != revision || state != "active" {
				t.Fatal("rejected save changed the source", err)
			}
			var sealed string
			err = s.db.QueryRow(`SELECT sealed FROM live_remote_configs WHERE source_id=?`, input.ID).Scan(&sealed)
			if mutation == "removed" {
				if !errors.Is(err, sql.ErrNoRows) {
					t.Fatal("rejected save recreated private configuration", err)
				}
			} else if err != nil || sealed != changed {
				t.Fatal("rejected save overwrote concurrent private state", err)
			}
		})
	}
}
