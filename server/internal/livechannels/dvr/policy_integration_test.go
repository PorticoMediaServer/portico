package dvr

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func TestRecordingIntentUsesGrantAndSnapshotsDefaults(t *testing.T) {
	db, err := persistence.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	recordingProfiles(t, db, "account", "profile")
	src, gen, ch, prog := strings.Repeat("a", 48), strings.Repeat("b", 48), strings.Repeat("c", 64), strings.Repeat("d", 64)
	now := time.Now().UTC()
	start, end := now.Add(time.Hour).Format(time.RFC3339), now.Add(2*time.Hour).Format(time.RFC3339)
	queries := []struct {
		q    string
		args []any
	}{
		{`INSERT INTO live_source_identities VALUES(?)`, []any{src}},
		{`INSERT INTO live_generations VALUES(?,?,'Guide',2,1,1,?)`, []any{gen, src, now.Format(time.RFC3339)}},
		{`INSERT INTO live_sources VALUES(?,'Aerial',1,'active',2,?,?)`, []any{src, gen, now.Format(time.RFC3339)}},
		{`INSERT INTO live_channel_versions(generation_id,channel_id,provider_key,name,number,source_number,group_name,position,locator_sealed,available_start,available_end) VALUES(?,?,'channel','Channel','1',NULL,'',1,x'00','','')`, []any{gen, ch}},
		{`INSERT INTO live_programmes VALUES(?,?,?,'programme','Episode',?,?,'lineage')`, []any{gen, prog, ch, start, end}},
		{`INSERT INTO live_programme_metadata(generation_id,id,series_id,episode_id,new_evidence,description) VALUES(?,?,'series','episode','new','')`, []any{gen, prog}},
	}
	for _, q := range queries {
		if _, err = db.Exec(q.q, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	granted := false
	s := &Store{db: db, live: &livechannels.Store{}, now: time.Now, captureAvailable: true, durable: func(context.Context, *sql.Tx, livechannels.Owner, string, string) error {
		if !granted {
			return ErrDenied
		}
		return nil
	}}
	owner := livechannels.Owner{Authority: "local", AccountID: "account", ProfileID: "profile"}
	auth := func(context.Context, *sql.Tx, bool) (string, func(string, string) bool, error) {
		return "owner", func(string, string) bool { return true }, nil
	}
	in := ScheduleInput{RequestID: strings.Repeat("e", 48), Occurrence: Occurrence{src, ch, gen, prog}, UseDefaults: true}
	if _, err = s.Schedule(ctx, auth, owner, in); !errors.Is(err, ErrDenied) {
		t.Fatal("viewing/owner authority bypassed recording grant", err)
	}
	granted = true
	r, err := s.Schedule(ctx, auth, owner, in)
	if err != nil {
		t.Fatal(err)
	}
	if r.Options.BeforeMs != 60000 || r.Options.AfterMs != 180000 {
		t.Fatal(r.Options)
	}
	if _, err = db.Exec(`UPDATE dvr_defaults SET before_seconds=600,after_seconds=900`); err != nil {
		t.Fatal(err)
	}
	replayed, err := s.Schedule(ctx, auth, owner, in)
	if err != nil || replayed.Options != r.Options {
		t.Fatal("defaults changed existing receipt", replayed, err)
	}
	rule := RuleInput{Mutation: Mutation{RequestID: strings.Repeat("f", 48)}, ID: strings.Repeat("1", 64), Anchor: in.Occurrence, UseDefaults: true, Config: RuleConfig{Name: "Series", SourceID: src, SeriesID: "series", Enabled: true, Episodes: "all", AllowedChannels: []string{}, BlockedChannels: []string{}, Keywords: []string{}}}
	saved, err := s.SaveRule(ctx, auth, owner, rule)
	if err != nil || saved.Config.Options.BeforeMs != 600000 {
		t.Fatal(saved, err)
	}
	var work int
	if err = db.QueryRow(`SELECT count(*) FROM dvr_rule_work WHERE rule_id=?`, saved.ID).Scan(&work); err != nil || work != 1 {
		t.Fatal("no runnable rule work", work, err)
	}
	// Read-only guard proves writes use a gated write, reads remain snapshots.
	if err = dbwork.WithWriteTx(ctx, db, dbwork.ClassInteractive, func(tx *sql.Tx) error {
		_, e := s.SaveRule(dbwork.WithWrite(ctx, tx, dbwork.ClassInteractive), auth, owner, rule)
		return e
	}); err != nil {
		t.Fatal("nested owner adapter failed", err)
	}
	granted = false
	if _, err = s.Schedule(ctx, auth, owner, in); !errors.Is(err, ErrDenied) {
		t.Fatal("revocation bypassed by receipt", err)
	}
	if _, err = s.SaveRule(ctx, auth, owner, rule); !errors.Is(err, ErrDenied) {
		t.Fatal("rule revocation bypassed", err)
	}
}
