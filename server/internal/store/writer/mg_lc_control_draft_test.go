package writer

import (
	"database/sql"
	"errors"
	"testing"
)

func TestMGLCControlDraftBirthAndEdit(t *testing.T) {
	w, _ := mgLCDraftExecutor(t)
	turn := mgSourceTestTurn(t, w, MGSetSourceEnabled, mgLCControlDefinitions())
	turn.transaction.descriptor.ID = MGSaveLCConfiguration
	out, err := mgSaveLCConfiguration(t.Context(), turn, mgLCControlChannel{id: "new-channel"}, mgLCControlConfig{"{}", "New", 0, 0})
	if err != nil || out.channel.head != (sql.NullString{Valid: true}) || out.channel.revision != 1 {
		t.Fatal("headless birth", out, err)
	}
	var ref sql.NullString
	if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT active_generation_ref FROM lc_channels WHERE id='new-channel'`).Scan(&ref); err != nil || ref.Valid {
		t.Fatal("generated head reference must be null", ref, err)
	}
	out, err = mgSaveLCConfiguration(t.Context(), turn, out.channel, mgLCControlConfig{"{}", "Edited", 0, 1})
	if err != nil || out.channel.head != (sql.NullString{Valid: true}) || out.channel.revision != 2 {
		t.Fatal("headless edit", out, err)
	}
}

func TestMGLCControlDraftGenerationCustody(t *testing.T) {
	for _, mode := range []string{"save", "expired-ref", "retire-unreferenced"} {
		t.Run(mode, func(t *testing.T) {
			w, b := mgLCFixture(t)
			mgLCBeginFixture(t, w, b)
			if mode == "retire-unreferenced" {
				// Fixture starts after an independently completed physical
				// retirement. This is not an expiry-based release effect.
				if _, err := w.db.ExecContext(t.Context(), `DELETE FROM lc_playback_refs WHERE playback_id='active-player'`); err != nil {
					t.Fatal(err)
				}
			}
			const witnessSQL = `SELECT hex(command_incarnation),last_command_sequence,acknowledged_command_sequence,staged_bytes,hex(entry_digest) FROM lc_generations WHERE id=?`
			type witness struct {
				incarnation, digest string
				sequence, ack, size int64
			}
			var before witness
			if err := w.db.QueryRowContext(t.Context(), witnessSQL, b.generation).Scan(&before.incarnation, &before.sequence, &before.ack, &before.size, &before.digest); err != nil {
				t.Fatal(err)
			}
			turn := mgSourceTestTurn(t, w, MGSetSourceEnabled, mgLCControlDefinitions())
			turn.transaction.descriptor.ID = MGSaveLCConfiguration
			channel, err := mgLCControlReadChannel(t.Context(), turn, b.channel)
			if err != nil {
				t.Fatal(err)
			}
			var out mgLCControlResult
			if mode == "save" {
				out, err = mgSaveLCConfiguration(t.Context(), turn, channel, mgLCControlConfig{"{}", "Edited", 0, 1})
			} else {
				turn.transaction.descriptor.ID = MGRetireLCChannel
				out, err = mgRetireLCChannel(t.Context(), turn, channel)
			}
			status := "superseded"
			if mode == "expired-ref" {
				if !errors.Is(err, ErrConflict) || out != (mgLCControlResult{}) {
					t.Fatal("expired physical reference accepted as release", out, err)
				}
				status = "pending"
			} else if err != nil || out.channel.head != channel.head {
				t.Fatal("head custody changed", out, err)
			}
			var after witness
			if err := turn.transaction.conn.QueryRowContext(t.Context(), witnessSQL, b.generation).Scan(&after.incarnation, &after.sequence, &after.ack, &after.size, &after.digest); err != nil || after != before {
				t.Fatal("generation namespace/receipt/data debt changed", before, after, err)
			}
			var actualStatus, lease string
			if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT status,lease_token FROM lc_generations WHERE id=?`, b.generation).Scan(&actualStatus, &lease); err != nil || actualStatus != status || lease != b.lease {
				t.Fatal("pending invalidation changed lease custody", actualStatus, lease, err)
			}
			var old string
			if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT id FROM lc_generations WHERE id=?`, b.base).Scan(&old); err != nil || old != b.base {
				t.Fatal("retained published generation lost", old, err)
			}
		})
	}
}
