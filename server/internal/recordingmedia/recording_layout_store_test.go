//go:build linux || darwin

package recordingmedia

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/artifactlease"
	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
	"github.com/PorticoMediaServer/portico/server/internal/livechannels/dvr"
	"github.com/PorticoMediaServer/portico/server/internal/mediaartifact"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

// This capture fixture supplies closed packets/coverage at the decoder seam.
// Namespace publication, SQL claim/receipt/catalog, filesystem IO, leases and
// deletion below are the production implementations. It does not qualify FFmpeg.
type layoutStoreCapture struct{ *Driver }

func (d *layoutStoreCapture) Capture(ctx context.Context, in dvr.CaptureRequest, checkpoint func(dvr.CaptureCheckpoint) error) (dvr.CaptureResult, error) {
	var out dvr.CaptureResult
	s, root, err := d.store(in.Owner, in.ArtifactKey)
	if err != nil {
		return out, err
	}
	w, err := s.BeginRetained(in.ArtifactKey, 188*3)
	if err != nil {
		return out, err
	}
	defer w.Retain()
	payload := make([]byte, 188*3)
	for i := 0; i < len(payload); i += 188 {
		payload[i] = 0x47
		payload[i+3] = 0x10
	}
	if _, err = w.Write(payload); err != nil {
		return out, err
	}
	object, err := w.Seal(ctx)
	if err != nil {
		return out, err
	}
	start, _ := time.Parse(time.RFC3339Nano, in.Recording.Start)
	cp := dvr.CaptureCheckpoint{Object: object, First: start, Last: start.Add(time.Second), WriterRetired: true}
	if in.OnRetired != nil {
		in.OnRetired()
	}
	if err = checkpoint(cp); err != nil {
		return out, err
	}
	path, _ := s.ObjectPath(object)
	info, err := os.Stat(path)
	if err != nil {
		return out, err
	}
	return dvr.CaptureResult{CaptureCheckpoint: cp, Path: path, Root: root, Container: "mpegts", VideoCodec: "mpeg2video", Width: 160, Height: 90, Duration: 1, ModifiedNS: info.ModTime().UnixNano()}, nil
}
func (d *layoutStoreCapture) Recover(ctx context.Context, in dvr.CaptureRequest, _ dvr.CaptureCheckpoint) (dvr.CaptureResult, error) {
	return d.Capture(ctx, in, in.Checkpoint)
}

func TestRecordingLayoutActualStoreAndIO(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	db, err := persistence.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	live, err := livechannels.New(db)
	if err != nil {
		t.Fatal(err)
	}
	auth := func(context.Context, *sql.Tx, bool) (string, func(string, string) bool, error) {
		return "owner", func(string, string) bool { return true }, nil
	}
	owner := livechannels.Owner{Authority: "local", AccountID: "account", ProfileID: "profile"}
	// Scheduling uses the production profile permission; this recording fixture
	// explicitly grants it rather than relying on a missing profile's default.
	for _, statement := range []string{
		`INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('account','account',x'00','profile',1)`,
		`INSERT OR IGNORE INTO direct_profiles(id,account_id,name) VALUES('profile','account','Profile')`,
		`INSERT INTO profile_restrictions(profile_id,allow_dvr) VALUES('profile',1) ON CONFLICT(profile_id) DO UPDATE SET allow_dvr=1`,
	} {
		if _, err = db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	store, err := dvr.New(db, live, func(_ context.Context, _ *sql.Tx, o livechannels.Owner, _, _ string) error {
		if o != owner {
			return dvr.ErrDenied
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// This uses only the real canonical schema. A missing schema/reader is an
	// integration failure, never replaced by test DDL or an empty/default value.
	if _, err = db.Exec(`UPDATE dvr_defaults SET show_folder_template='{title}',movie_folder_template='{title}',revision=revision+1 WHERE singleton=1`); err != nil {
		t.Fatalf("actual MAIN layout foundation unavailable: %v", err)
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	driver, err := New(root, "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer driver.Close()
	driver.available = true
	capture := &layoutStoreCapture{driver}
	locks, err := livechannels.NewPhysicalLocks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = store.ConfigureCapture(capture, locks); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	start, end := now.Add(-time.Minute), now.Add(5*time.Minute)
	playlist := "#EXTM3U\n#EXTINF:-1 tvg-id=\"one\",One\nhttps://provider.invalid/one\n#EXTINF:-1 tvg-id=\"two\",Two\nhttps://provider.invalid/two\n"
	guide := fmt.Sprintf(`<tv><programme channel="one" start="%s" stop="%s"><title>Example</title></programme><programme channel="two" start="%s" stop="%s"><title>Example</title></programme></tv>`, start.Format("20060102150405 -0700"), end.Format("20060102150405 -0700"), start.Format("20060102150405 -0700"), end.Format("20060102150405 -0700"))
	source, err := live.Save(ctx, auth, livechannels.SourceInput{ID: strings.Repeat("a", 48), RequestID: strings.Repeat("b", 48), Name: "Antenna", TunerCount: 2, Playlist: playlist, Guide: guide})
	if err != nil {
		t.Fatal(err)
	}
	listing, err := live.Guide(ctx, auth, livechannels.GuideQuery{Kind: livechannels.LiveSource, Start: start, End: end, Timezone: "UTC", Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for i, ch := range listing.Channels {
		p := ch.Programmes[0]
		r, e := store.Schedule(ctx, auth, owner, dvr.ScheduleInput{RequestID: strings.Repeat(fmt.Sprint(i+1), 48), Occurrence: dvr.Occurrence{SourceID: source.ID, ChannelID: ch.ID, Generation: source.Generation, ProgrammeID: p.ID}})
		if e != nil {
			t.Fatal(e)
		}
		ids = append(ids, r.ID)
	}
	if len(ids) != 2 {
		t.Fatal("fixture must have both real channel occurrences")
	}
	foreign := filepath.Join(root, "Example.ts")
	if err = os.WriteFile(foreign, []byte("unowned"), 0400); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { defer close(done); store.Run(ctx) }()
	defer func() { cancel(); <-done }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var count int
		if err = db.QueryRow(`SELECT count(*) FROM dvr_recordings WHERE state='incomplete-playable'`).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("production recording worker did not publish both occurrences")
		}
		time.Sleep(10 * time.Millisecond)
	}
	rows, err := db.Query(`SELECT recording_id,target_relative_path,source_relative_path,source_identity,target_identity FROM dvr_recording_layouts WHERE phase='linked' ORDER BY collision_ordinal`)
	if err != nil {
		t.Fatal(err)
	}
	type receipt struct{ id, target, source, sourceID, targetID string }
	receipts := []receipt{}
	for rows.Next() {
		var r receipt
		if err = rows.Scan(&r.id, &r.target, &r.source, &r.sourceID, &r.targetID); err != nil {
			t.Fatal(err)
		}
		receipts = append(receipts, r)
	}
	rows.Close()
	if len(receipts) != 2 || receipts[0].target != "Example (2).ts" || receipts[1].target != "Example (3).ts" {
		t.Fatalf("wrong real collision publications: %+v", receipts)
	}
	for _, r := range receipts {
		a, e := os.Stat(filepath.Join(root, r.source))
		if e != nil {
			t.Fatal(e)
		}
		b, e := os.Stat(filepath.Join(root, r.target))
		if e != nil || !os.SameFile(a, b) || r.sourceID != r.targetID {
			t.Fatal("source/target custody differs", e)
		}
		managed, e := store.IsRecordingLayoutPath(ctx, filepath.Join(root, r.target))
		if e != nil || !managed {
			t.Fatal("actual linked/current provenance not classified", e)
		}
	}
	if managed, e := store.IsRecordingLayoutPath(ctx, foreign); e != nil || managed {
		t.Fatal("unowned path classified", e)
	}
	r := receipts[0]
	var revision int64
	if err = db.QueryRow(`SELECT revision FROM dvr_recordings WHERE id=?`, r.id).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Delete(ctx, auth, owner, r.id, dvr.Mutation{ExpectedRevision: revision, RequestID: strings.Repeat("e", 48)}); err != nil {
		t.Fatal(err)
	}
	// Advance the isolated fixture's elapsed grace; never change permissions,
	// leases or recording ownership to force deletion through an active reader.
	if _, err = db.Exec(`UPDATE dvr_delete_work SET not_before_ms=0 WHERE recording_id=?`, r.id); err != nil {
		t.Fatal(err)
	}
	reader, err := os.Open(filepath.Join(root, r.target))
	if err != nil {
		t.Fatal(err)
	}
	if err = artifactlease.Shared(reader); err != nil {
		reader.Close()
		t.Fatal(err)
	}
	if err = store.DeleteOne(ctx); !errors.Is(err, mediaartifact.ErrLeased) {
		reader.Close()
		t.Fatal("physical reader deletion was not refused", err)
	}
	reader.Close()
	if _, err = db.Exec(`UPDATE dvr_delete_work SET not_before_ms=0 WHERE recording_id=?`, r.id); err != nil {
		t.Fatal(err)
	}
	if err = store.DeleteOne(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(root, r.target)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("alias retained after delete", err)
	}
	if _, err = os.Stat(filepath.Join(root, r.source)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("source retained after delete", err)
	}
	if data, e := os.ReadFile(foreign); e != nil || string(data) != "unowned" {
		t.Fatal("foreign collision changed", e)
	}
	if _, err = os.Stat(filepath.Join(root, receipts[1].target)); err != nil {
		t.Fatal("other occurrence removed", err)
	}
}
