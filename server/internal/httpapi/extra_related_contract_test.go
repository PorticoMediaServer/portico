package httpapi

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

func TestExtraDetailOmitsUnsupportedRelatedContract(t *testing.T) {
	f := newTL6V1Fixture(t, 0)
	c := catalogtest.New(t, f.db)
	path := filepath.Join(f.root, "extra.mp4")
	extra := c.Entity(compactcatalog.Entity{Library: c.Handle(f.library), Kind: compactcatalog.Extra, Key: compactcatalog.ExtraKey(f.root, path), Title: "Extra", Added: "2026-01-01T00:00:00.000Z"}, nil)
	c.File(extra.ID, path, 60)
	c.Drain()
	w := f.raw("GET", "/v1/items/"+extra.Public+"/detail?related=all", f.owner.AccessToken, nil, nil)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	tl6AssertSpecResponse(t, "GET", "/v1/items/{id}/detail", w)
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out["related"] != nil {
		t.Fatal("extra advertised unsupported related section", out["related"])
	}
}
