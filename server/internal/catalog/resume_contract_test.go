package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/apispec"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

func TestAudiobookResumeCannotExceedDuration(t *testing.T) {
	c := catalogtest.Open(t)
	library := c.Library("books", "Books", "audiobook", "/books")
	book := c.Book(library, "Book", "Author")
	part := c.BookFile(book, 1, "/books/part.m4b")
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.SetAssetTx(ctx, tx, part.Asset, map[string]any{"duration": 60.0})
	})
	if _, err := c.DB.Exec(`INSERT INTO progress(profile_id,item_id,position,unit,completed,playback_id) VALUES('p',?,60000,0,0,'')`, part.ID); err != nil {
		t.Fatal(err)
	}
	c.Drain()
	s := New(c.DB)
	detail, err := s.Detail(Viewer{Profile: "p", Fence: "fence", Libraries: []string{"books"}}, "server", part.Public, false)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, action := range detail.Actions {
		if action.ID == "resume" {
			found = true
			if action.Playback == nil || action.Playback.StartSeconds == nil || *action.Playback.StartSeconds != 60 {
				t.Fatalf("%+v", action)
			}
		}
	}
	if !found {
		t.Fatal("missing bounded resume")
	}
	doc, schema, err := apispec.Schema("DetailAction")
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range detail.Actions {
		raw, _ := json.Marshal(action)
		if issues := doc.ValidateJSON(schema, raw); len(issues) > 0 {
			t.Fatal(issues)
		}
	}
}
