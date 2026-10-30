package administration

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"
)

func TestLibraryPreferencesAreTypedEffectiveAndTransactional(t *testing.T) {
	s, db, root := newService(t)
	library, _, _ := seedLibrary(t, db, root)
	ctx := context.Background()
	before, err := s.LibrarySettingsFor(ctx, allow, library)
	if err != nil {
		t.Fatal(err)
	}
	if !before.Settings.GroupCollections || before.Settings.LocalTags != "prefer" || before.Settings.LyricsSource != "files" {
		t.Fatal(before.Settings)
	}
	next := before.Settings
	next.GroupCollections = false
	next.LocalTags = "off"
	next.TitleSource = "names"
	next.SortIgnoreArticles = false
	next.FindExtras = false
	next.RefreshMode = "fill_missing"
	denied := errors.New("revoked")
	if _, err = s.SaveLibrarySettings(ctx, func(context.Context, *sql.Tx) error { return denied }, library, Change[LibrarySettings]{ExpectedRevision: before.Revision, OperationID: "denied-library-preferences", Settings: next}); !errors.Is(err, denied) {
		t.Fatal(err)
	}
	var n int
	if err = db.QueryRow(`SELECT count(*) FROM library_preferences`).Scan(&n); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	saved, err := s.SaveLibrarySettings(ctx, allow, library, Change[LibrarySettings]{ExpectedRevision: before.Revision, OperationID: "save-library-preferences-1", Settings: next})
	if err != nil {
		t.Fatal(err)
	}
	if saved.Settings.GroupCollections || saved.Settings.LocalTags != "off" || saved.Settings.TitleSource != "names" || saved.Settings.FindExtras || saved.Settings.RefreshMode != "fill_missing" {
		t.Fatal(saved.Settings)
	}
	var mode, title, refresh string
	if err = db.QueryRow(`SELECT p.local_tags,p.title_source,s.refresh_mode FROM library_preferences p JOIN screen_metadata_policies s ON s.library_id=p.library_id WHERE p.library_id=?`, library).Scan(&mode, &title, &refresh); err != nil || mode != "off" || title != "names" || refresh != "fill_missing" {
		t.Fatal(mode, title, refresh, err)
	}
	var duplicated bool
	if err = db.QueryRow(`SELECT json_type(body,'$.localTags') IS NOT NULL OR json_type(body,'$.groupCollections') IS NOT NULL FROM admin_documents WHERE scope=?`, libraryScope(library)).Scan(&duplicated); err != nil || duplicated {
		t.Fatal("duplicate authority", duplicated, err)
	}
	if _, err = s.SaveLibrarySettings(ctx, allow, library, Change[LibrarySettings]{ExpectedRevision: before.Revision, OperationID: "stale-library-preferences-1", Settings: next}); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	next.LocalTags = "supplement"
	if _, err = s.SaveLibrarySettings(ctx, allow, library, Change[LibrarySettings]{ExpectedRevision: saved.Revision, OperationID: "invalid-video-local-mode", Settings: next}); err == nil {
		t.Fatal("video supplement admitted")
	}
	if saved.Audience.Accounts < 0 || len(saved.Actions) != 3 || saved.Sources[0].LibraryID != library || saved.Sources[0].IntervalSeconds < 0 {
		t.Fatal(saved)
	}
	for _, row := range saved.EligibleControls {
		if row == "libraries.episodeThumbs" {
			t.Fatal("unwired thumbnails offered")
		}
	}
}

func TestLibrarySettingsDescribeManagedRestrictionsAndHideRetiringLibrary(t *testing.T) {
	s, db, root := newService(t)
	library, _, _ := seedLibrary(t, db, root)
	if _, err := db.Exec(`INSERT INTO dvr_private_libraries(library_id,authority,account_id,profile_id) VALUES(?,'local','account','profile')`, library); err != nil {
		t.Fatal(err)
	}
	doc, err := s.LibrarySettingsFor(context.Background(), allow, library)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Actions) != 1 || doc.Actions[0] != "rename" || len(doc.EligibleControls) != 3 {
		t.Fatal(doc.Actions, doc.EligibleControls)
	}
	if _, err = db.Exec(`UPDATE catalog_libraries SET retired=1 WHERE library_id=?`, library); err != nil {
		t.Fatal(err)
	}
	if _, err = s.LibrarySettingsFor(context.Background(), allow, library); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if _, err = s.SaveLibrarySettings(context.Background(), allow, library, Change[LibrarySettings]{ExpectedRevision: doc.Revision, OperationID: "retired-library-settings", Settings: doc.Settings}); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}

func TestLibraryProducerDefaultsResetEffectivePolicyThroughCAS(t *testing.T) {
	s, db, root := newService(t)
	library, _, _ := seedLibrary(t, db, root)
	ctx := context.Background()
	doc, err := s.LibrarySettingsFor(ctx, allow, library)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Defaults.ContinueWatching.VideoPlayedThreshold != 95 || doc.Defaults.Providers[0].Language != "en" || doc.Defaults.Providers[0].Region != "US" {
		t.Fatal("factory reset defaults", doc.Defaults)
	}
	changed := doc.Settings
	changed.LocalTags = "off"
	changed.GroupCollections = false
	changed.ContinueWatching.VideoPlayedThreshold = 80
	saved, err := s.SaveLibrarySettings(ctx, allow, library, Change[LibrarySettings]{ExpectedRevision: doc.Revision, OperationID: "defaults-edit", Settings: changed})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(saved.Defaults, doc.Defaults) {
		t.Fatal("current overrides changed factory defaults")
	}
	reset, err := s.SaveLibrarySettings(ctx, allow, library, Change[LibrarySettings]{ExpectedRevision: saved.Revision, OperationID: "defaults-reset", Settings: saved.Defaults})
	if err != nil || !reflect.DeepEqual(reset.Settings, reset.Defaults) {
		t.Fatal(reset.Settings, reset.Defaults, err)
	}
}
