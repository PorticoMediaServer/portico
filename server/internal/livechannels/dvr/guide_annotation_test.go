package dvr

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func TestGuideAnnotationPreservesOwnerChannelAndWindowBoundaries(t *testing.T) {
	db, err := persistence.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	owner := livechannels.Owner{Authority: "local", AccountID: "account", ProfileID: "profile"}
	other := livechannels.Owner{Authority: "local", AccountID: "account", ProfileID: "other-profile"}
	start := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	for _, source := range []string{"source-one", "source-two", "denied-source"} {
		if _, err = db.Exec(`INSERT INTO live_source_identities(id) VALUES(?)`, source); err != nil {
			t.Fatal(err)
		}
	}
	insert := func(id string, o livechannels.Owner, source, channel, programme, state string, from, to time.Time) {
		t.Helper()
		_, err := db.Exec(`INSERT INTO dvr_recordings(id,owner_key,authority,account_id,profile_id,source_id,channel_id,programme_id,guide_generation,programme_json,options_json,start_ms,end_ms,priority,revision,state,created_ms,updated_ms) VALUES(?,?,?,?,?,?,?,?,?,'{}','{}',?,?,0,1,?,0,0)`, id, o.Key(), o.Authority, o.AccountID, o.ProfileID, source, channel, programme, "older-generation", from.UnixMilli(), to.UnixMilli(), state)
		if err != nil {
			t.Fatal(err)
		}
	}
	insert("mine-one", owner, "source-one", "one", "shared-programme", "scheduled", start, end)
	insert("mine-two", owner, "source-two", "one", "shared-programme", "recording", start, end)
	insert("private", other, "source-one", "one", "shared-programme", "completed", start, end)
	insert("denied", owner, "denied-source", "one", "shared-programme", "scheduled", start, end)
	insert("cancelled", owner, "source-one", "one", "cancelled-programme", "cancelled", start, end)
	insert("deleted", owner, "source-one", "one", "deleted-programme", "deleted", start, end)
	insert("past", owner, "source-one", "one", "past-programme", "completed", start.Add(-time.Hour), start)
	insert("future", owner, "source-one", "one", "future-programme", "scheduled", end, end.Add(time.Hour))
	insert("completed", owner, "source-one", "two", "finished-programme", "completed", start, end)
	channel := func(source, id string, programmes ...string) livechannels.Channel {
		ch := livechannels.Channel{SourceID: source, ID: id, Provenance: livechannels.LiveSource, Generation: "current-generation"}
		for _, id := range programmes {
			ch.Programmes = append(ch.Programmes, livechannels.Programme{ID: id})
		}
		return ch
	}
	g := livechannels.Guide{Start: start.Format(time.RFC3339Nano), End: end.Format(time.RFC3339Nano), Channels: []livechannels.Channel{
		channel("source-one", "one", "shared-programme", "cancelled-programme", "deleted-programme", "past-programme", "future-programme", "not-recorded"),
		channel("source-two", "one", "shared-programme"),
		channel("source-one", "two", "finished-programme"),
		channel("denied-source", "one", "shared-programme"),
		channel("source-one", "wrong-channel", "shared-programme"),
		channel("source-one", "one", "shared-programme"), // Duplicate rows retain their annotations.
	}}
	g.Channels = append(g.Channels, channel("source-one", "one", "shared-programme"))
	g.Channels[6].Provenance = livechannels.Provenance("library")
	authority := func(context.Context, *sql.Tx, bool) (string, func(string, string) bool, error) {
		return "fence", func(source, channel string) bool { return source != "denied-source" }, nil
	}
	if err = (&Store{db: db}).AnnotateGuide(context.Background(), authority, owner, &g); err != nil {
		t.Fatal(err)
	}
	want := map[int]struct{ id, state string }{0: {"mine-one", "scheduled"}, 1: {"mine-two", "recording"}, 2: {"completed", "completed"}, 5: {"mine-one", "scheduled"}}
	for i, ch := range g.Channels {
		for j, p := range ch.Programmes {
			w := want[i]
			if j != 0 {
				w.id, w.state = "", ""
			}
			if p.RecordingID != w.id || p.RecordingState != w.state {
				t.Fatalf("channel %d programme %d: got %q/%q, want %q/%q", i, j, p.RecordingID, p.RecordingState, w.id, w.state)
			}
		}
	}
}

func TestRowsOnlyGuideAnnotationDoesNotReadRecordingsButStillChecksAuthority(t *testing.T) {
	db, err := persistence.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// A rows-only response must not depend on the recording table at all.
	if _, err = db.Exec(`DROP TABLE dvr_recordings`); err != nil {
		t.Fatal(err)
	}
	g := livechannels.Guide{Channels: []livechannels.Channel{{SourceID: "source", ID: "channel", Provenance: livechannels.LiveSource}}}
	owner := livechannels.Owner{Authority: "local", AccountID: "account", ProfileID: "profile"}
	checked := false
	authority := func(context.Context, *sql.Tx, bool) (string, func(string, string) bool, error) {
		checked = true
		return "fence", func(string, string) bool { return true }, nil
	}
	store := &Store{db: db}
	if err = store.AnnotateGuide(context.Background(), authority, owner, &g); err != nil || !checked {
		t.Fatalf("rows-only annotation: error=%v authority checked=%v", err, checked)
	}
	if err = store.AnnotateGuide(context.Background(), nil, owner, &g); !errors.Is(err, ErrDenied) {
		t.Fatalf("missing authority: %v", err)
	}
}
