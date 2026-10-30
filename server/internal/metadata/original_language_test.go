package metadata

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/metadatalanguage"
)

func TestOriginalLanguagePublicationReachesDetailWithoutOwnerLanguageOverride(t *testing.T) {
	for _, raw := range []string{"jpn", "cn", "xx"} {
		t.Run(raw, func(t *testing.T) {
			s, db, p := screenFixture(t)
			item := publicationFixtureMovie(t, db)
			screenConfirm(t, s)
			p.record.Language = raw
			if err := s.ScreenStep(context.Background()); err != nil {
				t.Fatal(err)
			}
			want := map[string]string{"jpn": "ja", "cn": "yue", "xx": ""}[raw]
			got, err := metadatalanguage.Published(context.Background(), db, item.Public)
			if err != nil || got != want {
				t.Fatal("published language", got, want, err)
			}
			detail, err := catalog.New(db).Detail(catalog.Viewer{Profile: "profile", Fence: "f", Libraries: []string{"lib"}}, "server", item.Public, false)
			if err != nil {
				t.Fatal(err)
			}
			if want != "" && (detail.Facts == nil || detail.Facts.OriginalLanguage != want) {
				t.Fatal("detail language", detail.Facts, want)
			}
			if want == "" && detail.Facts != nil && detail.Facts.OriginalLanguage != "" {
				t.Fatal("unknown inferred", detail.Facts)
			}
			for _, field := range repairSchema("item", "movie") {
				if field.Field == "language" || field.Field == "originalLanguage" {
					t.Fatal("original language is not an owner control", field)
				}
			}
			// The editor's different metadata language cannot supply this fact.
			target := RepairTarget{Kind: "item", ID: item.Public}
			state, editErr := s.RepairState(context.Background(), target, allowAll)
			if editErr != nil {
				t.Fatal(editErr)
			}
			preference := "fr"
			if _, editErr = s.Repair(context.Background(), target, RepairCommand{ExpectedRevision: state.Revision, Action: "edit", Fields: map[string]RepairFieldEdit{"metadataLanguage": {Value: &preference}}}, editorActor(), allowAll); editErr != nil {
				t.Fatal(editErr)
			}
			if got, err = metadatalanguage.Published(context.Background(), db, item.Public); err != nil || got != want {
				t.Fatal("editor preference affected original language", got, err)
			}
			state, editErr = s.RepairState(context.Background(), target, allowAll)
			if editErr != nil {
				t.Fatal(editErr)
			}
			locked := true
			for _, field := range []string{"language", "originalLanguage"} {
				if _, editErr = s.Repair(context.Background(), target, RepairCommand{ExpectedRevision: state.Revision, Action: "edit", Fields: map[string]RepairFieldEdit{field: {Value: &preference, Locked: &locked}}}, editorActor(), allowAll); !errors.Is(editErr, ErrRepairInput) {
					t.Fatal("original language accepted an owner edit/lock", field, editErr)
				}
			}
		})
	}
}

func TestOriginalLanguageUnknownRefreshWithdrawsPriorFactDuringFillMissing(t *testing.T) {
	s, db, p := screenFixture(t)
	item := publicationFixtureMovie(t, db)
	ctx := context.Background()
	screenConfirm(t, s)
	p.record.Language = "ja"
	if err := s.ScreenStep(ctx); err != nil {
		t.Fatal(err)
	}
	if got, err := metadatalanguage.Published(ctx, db, item.Public); err != nil || got != "ja" {
		t.Fatal("initial published language", got, err)
	}
	// Expire the fixture's provider receipt and schedule the same identity.
	// Both publications still pass through the actual ScreenStep producer.
	publicationExec(t, db, `UPDATE screen_metadata_policies SET refresh_mode='fill_missing',revision=revision+1 WHERE library_id='lib';
UPDATE screen_metadata_work SET status='pending',generation=generation+1,revision=revision+1,lease='',lease_until='',next_attempt='',error='' WHERE target_kind='item' AND target_id=?;
DELETE FROM metadata_document_freshness`, item.ID)
	p.record.Language = "xx"
	p.record.Overview = "Replacement description"
	if err := s.ScreenStep(ctx); err != nil {
		t.Fatal(err)
	}
	if got, err := metadatalanguage.Published(ctx, db, item.Public); err != nil || got != "" {
		t.Fatal("unknown refresh retained prior language", got, err)
	}
	if got := publicationScalar(t, db, `SELECT count(*) FROM screen_metadata_fields WHERE target_kind='item' AND target_id=? AND field='language'`, item.ID); got != "0" {
		t.Fatal("obsolete language remains stored", got)
	}
	if got := publicationScalar(t, db, `SELECT json_extract(value,'$') FROM screen_metadata_fields WHERE target_kind='item' AND target_id=? AND field='overview'`, item.ID); got != "Verified description" {
		t.Fatal("fill-missing description changed", got)
	}
	if got := publicationScalar(t, db, `SELECT count(*) FROM screen_metadata_publications WHERE target_kind='item' AND target_id=? AND decision='accepted'`, item.ID); got != "1" {
		t.Fatal("accepted publication authority", got)
	}
}

func TestOriginalLanguageRequiresCurrentAcceptedProviderPublication(t *testing.T) {
	s, db, p := screenFixture(t)
	item := publicationFixtureMovie(t, db)
	screenConfirm(t, s)
	p.record.Language = "ja"
	ctx := context.Background()
	if err := s.ScreenStep(ctx); err != nil {
		t.Fatal(err)
	}
	c := catalogtest.New(t, db)
	var library int64
	if err := db.QueryRow(`SELECT id FROM catalog_libraries WHERE library_id='lib'`).Scan(&library); err != nil {
		t.Fatal(err)
	}
	other := c.Movie(library, "/owned/other.mp4", "Other", 2009)
	c.Drain()
	for _, test := range []struct {
		name, query string
		args        []any
	}{
		{"no-current-publication", `UPDATE screen_metadata_work SET accepted_publication='' WHERE target_kind='item' AND target_id=?`, []any{item.ID}},
		{"superseded-publication", `UPDATE screen_metadata_publications SET decision='superseded' WHERE target_kind='item' AND target_id=?`, []any{item.ID}},
		{"unsupported-field-source", `UPDATE screen_metadata_fields SET provider='filename' WHERE target_kind='item' AND target_id=? AND field='language'`, []any{item.ID}},
		{"provider-mismatch", `UPDATE screen_metadata_publications SET provider='tvdb' WHERE target_kind='item' AND target_id=?`, []any{item.ID}},
		{"publication-for-other-title", `UPDATE screen_metadata_publications SET target_id=? WHERE target_kind='item' AND target_id=?`, []any{other.ID, item.ID}},
		{"non-text-field", `UPDATE screen_metadata_fields SET value='1' WHERE target_kind='item' AND target_id=? AND field='language'`, []any{item.ID}},
	} {
		t.Run(test.name, func(t *testing.T) {
			tx, err := db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			if _, err = tx.ExecContext(ctx, test.query, test.args...); err != nil {
				t.Fatal(err)
			}
			if got, err := metadatalanguage.Published(ctx, tx, item.Public); err != nil || got != "" {
				t.Fatal("unaccepted language escaped the transaction", got, err)
			}
		})
	}
	// Each rejected state was rolled back: the actual accepted fact remains.
	if got, err := metadatalanguage.Published(ctx, db, item.Public); err != nil || got != "ja" {
		t.Fatal("accepted language", got, err)
	}
	if _, err := metadatalanguage.Published(ctx, db, "AAAAAAAAAAAAAAAAAAAAAA"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("absent title must not yield a fabricated fact", err)
	}
}
