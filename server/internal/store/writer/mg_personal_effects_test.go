package writer

import (
	"context"
	"database/sql/driver"
	"errors"
	"math"
	"strings"
	"testing"
	"time"
)

func TestMGPersonalNativePreferenceAndReminder(t *testing.T) {
	w, _, v := mgPersonalFixture(t)
	turn := mgPersonalTurn(t, w, MGSetChannelPreference)
	p, err := setMGPersonalPreference(t.Context(), turn, mgPersonalPreferenceRequest{owner: v.owner, target: v.live, favorite: true})
	if err != nil || p != (mgPersonalPreferenceFacts{1, true, false}) {
		t.Fatal(p, err)
	}
	live, err := setMGPersonalReminder(t.Context(), turn, v.reminder(false))
	if err != nil || live.revision != 1 || live.externalGeneration != v.live.generation || live.libraryGeneration != "" || live.start != v.start.Format(time.RFC3339) {
		t.Fatal(live, err)
	}
	library, err := setMGPersonalReminder(t.Context(), turn, v.reminder(true))
	if err != nil || library.revision != 1 || library.libraryGeneration != v.library.generation || library.externalGeneration != "" || library.start != v.start.Format(time.RFC3339) {
		t.Fatal(library, err)
	}
	p, err = setMGPersonalPreference(t.Context(), turn, mgPersonalPreferenceRequest{owner: v.owner, target: v.library, hidden: true})
	if err != nil || p.revision != 1 || !p.hidden {
		t.Fatal(p, err)
	}
	mgPersonalCommit(t, turn)
	var source string
	if err := w.db.QueryRowContext(t.Context(), `SELECT source_id FROM live_channel_preferences WHERE channel_id='library-channel'`).Scan(&source); err != nil || source != "library:library-channel" {
		t.Fatal(source, err)
	}
	turn = mgPersonalTurn(t, w, MGSetChannelPreference)
	p, err = setMGPersonalPreference(t.Context(), turn, mgPersonalPreferenceRequest{owner: v.owner, target: v.live, expected: 1})
	if err != nil || p != (mgPersonalPreferenceFacts{2, false, false}) {
		t.Fatal(p, err)
	}
	r := v.reminder(false)
	r.expected = 1
	updated, err := setMGPersonalReminder(t.Context(), turn, r)
	if err != nil || updated.revision != 2 || updated.id != live.id {
		t.Fatal(updated, err)
	}
	mgPersonalCommit(t, turn)
	turn = mgPersonalTurn(t, w, MGSetChannelPreference)
	if got, err := setMGPersonalReminder(t.Context(), turn, r); !errors.Is(err, ErrConflict) || got != (mgPersonalReminderFacts{}) {
		t.Fatal("stale reminder CAS", got, err)
	}
	if _, err := setMGPersonalPreference(t.Context(), turn, mgPersonalPreferenceRequest{owner: v.owner, target: v.live, expected: 1}); !errors.Is(err, ErrConflict) {
		t.Fatal("stale preference CAS", err)
	}
	if _, err := setMGPersonalReminder(t.Context(), turn, v.reminder(false)); !errors.Is(err, ErrConflict) {
		t.Fatal("fresh birth cannot replace public receipt replay", err)
	}
}

func TestMGPersonalCurrentTargetFences(t *testing.T) {
	for name, q := range map[string]string{
		"source-revision":    `UPDATE live_sources SET revision=8 WHERE id='personal-live'`,
		"same-revision-head": `UPDATE live_sources SET active_generation='replacement-generation' WHERE id='personal-live'`,
		"source-disabled":    `UPDATE live_sources SET state='disabled' WHERE id='personal-live'`,
		"source-removal":     `UPDATE live_source_identities SET has_live_source_removals=1,removal_request_id='retirement',removal_revision=8,removal_removed_at='2026-11-07T00:00:00Z' WHERE id='personal-live'`,
	} {
		t.Run(name, func(t *testing.T) {
			w, _, v := mgPersonalFixture(t)
			if _, err := w.db.ExecContext(t.Context(), q); err != nil {
				t.Fatal(err)
			}
			turn := mgPersonalTurn(t, w, MGSetChannelPreference)
			if got, err := setMGPersonalPreference(t.Context(), turn, mgPersonalPreferenceRequest{owner: v.owner, target: v.live}); !errors.Is(err, ErrConflict) || got != (mgPersonalPreferenceFacts{}) {
				t.Fatal(got, err)
			}
			if got, err := setMGPersonalReminder(t.Context(), turn, v.reminder(false)); !errors.Is(err, ErrConflict) || got != (mgPersonalReminderFacts{}) {
				t.Fatal(got, err)
			}
		})
	}
	for name, q := range map[string]string{
		"library-revision":       `UPDATE lc_channels SET revision=6 WHERE id='library-channel'`,
		"library-head":           `UPDATE lc_channels SET active_generation='' WHERE id='library-channel'`,
		"library-disabled":       `UPDATE lc_channels SET enabled=0 WHERE id='library-channel'`,
		"library-removed":        `UPDATE lc_channels SET removed=1 WHERE id='library-channel'`,
		"unpublished-generation": `UPDATE lc_generations SET status='pending' WHERE id='lc-generation'`,
	} {
		t.Run(name, func(t *testing.T) {
			w, _, v := mgPersonalFixture(t)
			if _, err := w.db.ExecContext(t.Context(), q); err != nil {
				t.Fatal(err)
			}
			turn := mgPersonalTurn(t, w, MGSetChannelPreference)
			if _, err := setMGPersonalReminder(t.Context(), turn, v.reminder(true)); !errors.Is(err, ErrConflict) {
				t.Fatal(err)
			}
		})
	}
}

func TestMGPersonalProgrammeOccurrenceAndTimeEvidence(t *testing.T) {
	w, _, v := mgPersonalFixture(t)
	turn := mgPersonalTurn(t, w, MGSetChannelPreference)
	r := v.reminder(false)
	r.programme = "other-programme"
	if _, err := setMGPersonalReminder(t.Context(), turn, r); !errors.Is(err, ErrConflict) {
		t.Fatal("programme from another channel", err)
	}
	r = v.reminder(true)
	r.libraryItem.item++
	if _, err := setMGPersonalReminder(t.Context(), turn, r); !errors.Is(err, ErrConflict) {
		t.Fatal("RB/RS item mismatch", err)
	}
	r = v.reminder(true)
	r.libraryItem.source = "replaced-source"
	if _, err := setMGPersonalReminder(t.Context(), turn, r); !errors.Is(err, ErrConflict) {
		t.Fatal("source binding mismatch", err)
	}
	// A wrong generation is a conflict, never a substitute channel/programme.
	r = v.reminder(true)
	r.target.generation = "other-generation"
	if _, err := setMGPersonalReminder(t.Context(), turn, r); !errors.Is(err, ErrConflict) {
		t.Fatal("wrong generation", err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	if _, err := w.db.ExecContext(t.Context(), `UPDATE live_programmes SET start_utc=? WHERE id='live-programme'`, w.now().UTC().Add(-time.Minute).Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	turn = mgPersonalTurn(t, w, MGSetChannelPreference)
	if _, err := setMGPersonalReminder(t.Context(), turn, v.reminder(false)); !errors.Is(err, ErrConflict) {
		t.Fatal("past reminder accepted", err)
	}
}

func TestMGPersonalDeletionPreservesOtherCustody(t *testing.T) {
	w, _, v := mgPersonalFixture(t)
	turn := mgPersonalTurn(t, w, MGSetChannelPreference)
	saved, err := setMGPersonalReminder(t.Context(), turn, v.reminder(true))
	if err != nil {
		t.Fatal(err)
	}
	mgPersonalCommit(t, turn)
	for _, q := range []string{
		`INSERT INTO lc_playback_refs VALUES('held-player','library-channel','lc-generation','lc-occurrence',1,1)`,
		`INSERT INTO live_allocations(id,source_id,channel_id,resource_id,kind,authority,account_id,profile_id,token,generation,state,lease_until_ms,created_ms) VALUES('held-allocation','personal-live','live-channel','held-resource','live','local','acct','profile','fixture-token',1,'active',1,1)`,
		`UPDATE lc_channels SET active_generation='',enabled=0 WHERE id='library-channel'`,
	} {
		if _, err := w.db.ExecContext(t.Context(), q); err != nil {
			t.Fatal(err)
		}
	}
	turn = mgPersonalTurn(t, w, MGDeleteReminder)
	other := v.owner
	other.profile = "another-profile"
	if _, err := deleteMGPersonalReminder(t.Context(), turn, mgPersonalDeleteRequest{other, saved.id, 1}); !errors.Is(err, ErrConflict) {
		t.Fatal("foreign owner deleted intent", err)
	}
	if _, err := deleteMGPersonalReminder(t.Context(), turn, mgPersonalDeleteRequest{v.owner, saved.id, 2}); !errors.Is(err, ErrConflict) {
		t.Fatal("stale revision deleted intent", err)
	}
	deleted, err := deleteMGPersonalReminder(t.Context(), turn, mgPersonalDeleteRequest{v.owner, saved.id, 1})
	if err != nil || deleted != saved {
		t.Fatal(deleted, err)
	}
	mgPersonalCommit(t, turn)
	var reminders, refs, allocations int
	if err := w.db.QueryRowContext(t.Context(), `SELECT (SELECT count(*) FROM live_reminders),(SELECT count(*) FROM lc_playback_refs WHERE playback_id='held-player'),(SELECT count(*) FROM live_allocations WHERE id='held-allocation' AND state='active' AND lease_until_ms=1)`).Scan(&reminders, &refs, &allocations); err != nil || reminders != 0 || refs != 1 || allocations != 1 {
		t.Fatal(reminders, refs, allocations, err)
	}
}

func TestMGPersonalReminderIsNativeGenerationPin(t *testing.T) {
	w, _, v := mgPersonalFixture(t)
	turn := mgPersonalTurn(t, w, MGSetChannelPreference)
	saved, err := setMGPersonalReminder(t.Context(), turn, v.reminder(true))
	if err != nil {
		t.Fatal(err)
	}
	mgPersonalCommit(t, turn)
	for _, q := range []string{`UPDATE lc_channels SET active_generation='' WHERE id='library-channel'`, `DELETE FROM lc_entries WHERE generation_id='lc-generation'`} {
		if _, err := w.db.ExecContext(t.Context(), q); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := w.db.ExecContext(t.Context(), `DELETE FROM lc_generations WHERE id='lc-generation'`); err == nil {
		t.Fatal("reminder failed to pin generation")
	}
	turn = mgPersonalTurn(t, w, MGSetChannelPreference)
	if _, err := deleteMGPersonalReminder(t.Context(), turn, mgPersonalDeleteRequest{v.owner, saved.id, 1}); err != nil {
		t.Fatal(err)
	}
	mgPersonalCommit(t, turn)
	if _, err := w.db.ExecContext(t.Context(), `DELETE FROM lc_generations WHERE id='lc-generation'`); err != nil {
		t.Fatal("intent ref not removed", err)
	}
}

var mgPersonalInjectedReadback = errors.New("injected personal readback failure")

type mgPersonalFaultConnection struct {
	writerDriverConn
	inserted bool
}

func (c *mgPersonalFaultConnection) QueryContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	if c.inserted && strings.HasPrefix(q, "SELECT "+mgPersonalReminderColumns+" FROM live_reminders") {
		return nil, mgPersonalInjectedReadback
	}
	rows, err := c.writerDriverConn.QueryContext(ctx, q, args)
	if err == nil && strings.HasPrefix(q, "INSERT INTO live_reminders(") {
		c.inserted = true
	}
	return rows, err
}

func TestMGPersonalReadbackFailureRollsBack(t *testing.T) {
	w, _, v := mgPersonalFixtureWith(t, func(c driver.Conn) driver.Conn {
		return &mgPersonalFaultConnection{writerDriverConn: c.(writerDriverConn)}
	})
	turn := mgPersonalTurn(t, w, MGSetChannelPreference)
	if got, err := setMGPersonalReminder(t.Context(), turn, v.reminder(true)); !errors.Is(err, mgPersonalInjectedReadback) || got != (mgPersonalReminderFacts{}) {
		t.Fatal(got, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM live_reminders`).Scan(&count); err != nil || count != 0 {
		t.Fatal("failed intent/pin survived", count, err)
	}
}

func TestMGPersonalNamespaceBoundsAndCanceledRelease(t *testing.T) {
	if mgPersonalID("local", "acct", "profile") != "2e7cf568816edf0389c8b6cf8cc0ad792123b1afe81cba044f7b3d742e8dbab7" {
		t.Fatal("owner identity encoding changed")
	}
	ds := mgPersonalDescriptors()
	if len(ds) != 3 || ds[0].ID != 811 || ds[1].ID != 812 || ds[2].ID != 813 {
		t.Fatal(ds)
	}
	if _, err := buildCommandDescriptors(commandRegistration{owner: "MG", descriptors: ds}); err != nil {
		t.Fatal(err)
	}
	w, _, v := mgPersonalFixture(t)
	turn := mgPersonalTurn(t, w, MGSetChannelPreference)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if got, err := setMGPersonalPreference(ctx, turn, mgPersonalPreferenceRequest{owner: v.owner, target: v.live}); !errors.Is(err, context.Canceled) || got != (mgPersonalPreferenceFacts{}) {
		t.Fatal(got, err)
	}
	if _, err := setMGPersonalPreference(t.Context(), turn, mgPersonalPreferenceRequest{owner: v.owner, target: v.live, expected: math.MaxInt64}); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	bad := v.reminder(false)
	bad.libraryItem = v.item
	if _, err := setMGPersonalReminder(t.Context(), turn, bad); !errors.Is(err, ErrInvalid) {
		t.Fatal("live reminder with a library item accepted")
	}
}
