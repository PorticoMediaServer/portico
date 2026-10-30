package catalog

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/metadatalanguage"
)

func workspacePublishedLanguage(t *testing.T, db *sql.DB, library, kind string, entity int64, raw string) {
	t.Helper()
	id := fmt.Sprintf("language-%s-%d", kind, entity)
	if _, err := db.Exec(`INSERT OR IGNORE INTO screen_metadata_work(target_kind,target_id,library_id,title_seed) VALUES(?,?,?,'fixture')`, kind, entity, library); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO screen_metadata_publications(id,target_kind,target_id,provider,provider_type,provider_id,payload,confidence,reasons,source_kind,input_digest,query_digest,observed_at,language,region,selection_revision,generation,decision) VALUES(?,?,?,'tmdb','show','42','{}',1,'[]','provider','fixture','fixture','now','en','US',1,1,'accepted')`, id, kind, entity); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE screen_metadata_work SET accepted_publication=? WHERE target_kind=? AND target_id=?`, id, kind, entity); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO screen_metadata_fields(target_kind,target_id,field,value,provider,source_kind,source_url,confidence,observed_at,language,region,selection_revision,publication_id) VALUES(?,?,'language',?,'tmdb','provider','',1,'now','en','US',1,?)`, kind, entity, fmt.Sprintf("%q", raw), id); err != nil {
		t.Fatal(err)
	}
}
func TestEpisodeAndSeasonOriginalLanguageComeFromPublishedShow(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, db, names := workspaceFixture(t)
	workspacePublishedLanguage(t, db, "tv", "show", names["show"].ID, "ja-JP")
	workspacePublishedLanguage(t, db, "tv", "item", names["ep-001"].ID, "en")
	for _, name := range []string{"show", "season1", "ep-001"} {
		got, err := metadatalanguage.Published(context.Background(), db, names[name].Public)
		if err != nil || got != "ja" {
			t.Fatal(name, got, err)
		}
	}
	detail, err := s.Detail(workspaceRequest(names).Viewer, "server", names["ep-001"].Public, false)
	if err != nil || detail.Facts == nil || detail.Facts.OriginalLanguage != "ja" {
		t.Fatal("episode detail", detail.Facts, err)
	}
	page, err := s.ShowWorkspace(workspaceRequest(names))
	if err != nil || page.Show.OriginalLanguage != "ja" {
		t.Fatal("workspace language", page.Show, err)
	}
	if _, err = db.Exec(`UPDATE screen_metadata_publications SET decision='superseded' WHERE target_kind='show' AND target_id=?`, names["show"].ID); err != nil {
		t.Fatal(err)
	}
	if got, err := metadatalanguage.Published(context.Background(), db, names["ep-001"].Public); err != nil || got != "" {
		t.Fatal("superseded language leaked", got, err)
	}
}
