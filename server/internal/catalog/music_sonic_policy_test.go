package catalog

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
)

func TestMusicSonicPolicyIsLibraryLocalCASAndUsesExistingPendingWork(t *testing.T) {
	c := catalogtest.Open(t)
	c.Library("music", "Music", "music", "/music")
	c.Library("video", "Films", "movie", "/video")
	s := New(c.DB)
	ctx := context.Background()
	policy, err := s.ScanPolicy(ctx, "music")
	if err != nil || !policy.Allows(SonicOperation) || !policy.Allows("loudness") {
		t.Fatal(policy, err)
	}
	video, err := s.ScanPolicy(ctx, "video")
	if err != nil || video.Allows(SonicOperation) {
		t.Fatal(video, err)
	}
	denied := errors.New("not authorized")
	if _, err = s.UpdateSonicSettings(ctx, "music", false, 1, func(*sql.Tx) error { return denied }); !errors.Is(err, denied) {
		t.Fatal(err)
	}
	if _, err = s.UpdateSonicSettings(ctx, "music", false, 1, nil); !errors.Is(err, ErrAdminQuery) {
		t.Fatal("nil authorization admitted", err)
	}
	settings, err := s.UpdateSonicSettings(ctx, "music", false, 1, func(*sql.Tx) error { return nil })
	if err != nil || settings.Enabled || settings.Revision != 2 {
		t.Fatal(settings, err)
	}
	policy, err = s.ScanPolicy(ctx, "music")
	if err != nil || policy.Allows(SonicOperation) || !policy.Allows("loudness") {
		t.Fatal(policy, err)
	}
	var pending, scan int64
	if err = c.DB.QueryRow(`SELECT q.revision,p.revision FROM inventory_policy_pending q JOIN library_scan_policies p ON p.library_id=q.library_id WHERE q.library_id='music'`).Scan(&pending, &scan); err != nil || pending != scan {
		t.Fatal("no durable existing-worker request", pending, scan, err)
	}
	if _, err = s.UpdateSonicSettings(ctx, "music", true, 1, func(*sql.Tx) error { return nil }); !errors.Is(err, ErrLibraryConfigurationConflict) {
		t.Fatal("stale writer replaced newer choice", err)
	}
	unchanged, err := s.UpdateSonicSettings(ctx, "music", false, 2, func(*sql.Tx) error { return nil })
	if err != nil || unchanged != settings {
		t.Fatal("same choice changed provenance", unchanged, err)
	}
	if _, err = s.UpdateSonicSettings(ctx, "video", true, 1, func(*sql.Tx) error { return nil }); !errors.Is(err, ErrAdminQuery) {
		t.Fatal("nonmusic setting accepted", err)
	}
}

func TestMusicSonicPolicyNeverReadsFileListOnly(t *testing.T) {
	c := catalogtest.Open(t)
	c.Library("music", "Music", "music", "/music")
	s := New(c.DB)
	for _, tier := range []string{"basic", "complete", "custom", "file_list_only"} {
		c.Exec(`UPDATE library_scan_policies SET tier=?,operations_json='["probe"]' WHERE library_id='music'`, tier)
		policy, err := s.ScanPolicy(context.Background(), "music")
		if err != nil || policy.Allows(SonicOperation) != (tier != "file_list_only") {
			t.Fatal(tier, policy, err)
		}
	}
}

func TestMusicSonicFactoryDefaultStaysIndependentOfSavedChoice(t *testing.T) {
	c := catalogtest.Open(t)
	c.Library("music", "Music", "music", "/music")
	if !SonicDefaultEnabled() {
		t.Fatal("approved factory choice changed")
	}
	tx, err := c.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	missing, err := SonicSettingsTx(context.Background(), tx, "music")
	tx.Rollback()
	if err != nil || missing.Enabled != SonicDefaultEnabled() || missing.Revision != 1 {
		t.Fatal(missing, err)
	}
	saved, err := New(c.DB).UpdateSonicSettings(context.Background(), "music", false, missing.Revision, func(*sql.Tx) error { return nil })
	if err != nil || saved.Enabled || saved.Revision != 2 || !SonicDefaultEnabled() {
		t.Fatal("saved Off replaced the factory fact", saved, err)
	}
}
