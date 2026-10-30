package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/apispec"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
	library "github.com/PorticoMediaServer/portico/server/internal/livechannels/library"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

// FEAT-04 (Channels spec §8.1): a Library Channel programme carries its
// library item's facts (year, rating, genres as categories, poster as image);
// once the title is restricted for the profile, the slot shows the slate and
// none of those facts.
func TestLibraryChannelGuideCarriesItemFacts(t *testing.T) {
	root := t.TempDir()
	db, err := persistence.Open(filepath.Join(root, "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ident, err := identity.New(db, root)
	if err != nil {
		t.Fatal(err)
	}
	cat := catalogtest.New(t, db)
	lib := cat.Library("library", "Test", "movie", root)
	item := cat.Movie(lib, filepath.Join(root, "night-film.mkv"), "Night Film", 1999)
	cat.Fields(item.ID, map[string]any{"poster_url": "local:poster.jpg"})
	cat.Genres(item.ID, "tmdb", "Drama", "Thriller")
	cat.Attributes(item.ID, "contentRating", "R")
	if _, err = db.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('account','owner',X'00','profile',1)`); err != nil {
		t.Fatal(err)
	}
	auth, err := ident.Issue("account", "profile", "local", "owner", 1)
	if err != nil {
		t.Fatal(err)
	}
	cat.Drain()

	var ri, ai, ii, assi string
	var rr, ar, ir, assr int64
	for _, query := range []struct {
		statement string
		args      []any
	}{
		{`INSERT OR IGNORE INTO playback_origin_roots(id) VALUES(?)`, []any{"library"}},
		{`INSERT OR IGNORE INTO playback_origin_assets(id) VALUES(?)`, []any{item.Token}},
		{`INSERT OR IGNORE INTO playback_origin_items(id) VALUES(?)`, []any{item.ID}},
		{`INSERT OR IGNORE INTO playback_origin_associations(item_id,asset_id) VALUES(?,?)`, []any{item.ID, item.Token}},
	} {
		if _, err := db.Exec(query.statement, query.args...); err != nil {
			t.Fatal(query.statement, err)
		}
	}
	if err := db.QueryRow(`SELECT r.incarnation,r.revision,a.incarnation,a.revision,i.incarnation,i.revision,l.incarnation,l.revision FROM playback_origin_roots r,playback_origin_assets a,playback_origin_items i,playback_origin_associations l WHERE r.id='library' AND a.id=? AND i.id=? AND l.item_id=? AND l.asset_id=?`, item.Token, item.ID, item.ID, item.Token).Scan(&ri, &rr, &ai, &ar, &ii, &ir, &assi, &assr); err != nil {
		t.Fatal(err)
	}
	parts := []string{"catalog-observation-v1", item.Public, "library", item.Token, ri, fmt.Sprint(rr), ai, fmt.Sprint(ar), ii, fmt.Sprint(ir), assi, fmt.Sprint(assr)}
	hash := sha256.New()
	for _, part := range parts {
		fmt.Fprintf(hash, "%d:%s", len(part), part)
	}
	fence := hex.EncodeToString(hash.Sum(nil))
	config, _ := json.Marshal(library.Config{Version: "1", ID: "channel", Name: "Members channel", Enabled: true, ViewerAccess: "server-members"})
	now := time.Now().UTC().UnixMilli()
	if _, err = db.Exec(`INSERT INTO lc_channels(id,revision,config_json,enabled,position,name,active_generation,state) VALUES('channel',1,?,1,0,'Members channel','generation','ready')`, string(config)); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO lc_generations(id,channel_id,config_revision,config_json,catalog_fence,seed,status,phase,base_generation,start_ms,end_ms,boundary_ms,cursor_ms,created_ms) VALUES('generation','channel',1,?,'','','published','complete','',?,?,?,0,?)`, string(config), now-60000, now+60000, now+60000, now); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO lc_entries(generation_id,occurrence_id,channel_id,start_ms,end_ms,item_id,asset_id,library_id,source_fence,title,rule_id,block_id,source_offset_ms,slate_reason) VALUES('generation','entry','channel',?,?,?,?,'library',?,'Night Film','','',0,'')`, now-60000, now+60000, item.ID, item.Token, fence); err != nil {
		t.Fatal(err)
	}

	scheduled, err := library.New(db)
	if err != nil {
		t.Fatal(err)
	}
	live, err := livechannels.New(db)
	if err != nil {
		t.Fatal(err)
	}
	d := Dependencies{DB: db, Identity: ident, Catalog: catalog.New(db), LibraryChannels: scheduled}
	mux := http.NewServeMux()
	d.liveChannelRoutes(mux, live)
	guide := func() livechannels.Programme {
		query := url.Values{"kind": {"library-channel"}, "start": {time.UnixMilli(now - 30000).UTC().Format(time.RFC3339)}, "end": {time.UnixMilli(now + 30000).UTC().Format(time.RFC3339)}, "timezone": {"UTC"}, "limit": {"30"}}
		r := httptest.NewRequest("GET", "/v1/guide?"+query.Encode(), nil)
		r.Header.Set("Authorization", "Bearer "+auth.AccessToken)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		var body struct {
			Guide livechannels.Guide `json:"guide"`
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &body) != nil || len(body.Guide.Channels) != 1 || len(body.Guide.Channels[0].Programmes) != 1 {
			t.Fatalf("guide: %d %s", w.Code, w.Body)
		}
		tl11AssertChannelGuideSpecResponse(t, "GET", "/v1/guide", w)
		if !strings.Contains(w.Body.String(), `"flags":`) || !strings.Contains(w.Body.String(), `"categories":`) {
			t.Fatalf("guide programme lacks flags or categories: %s", w.Body)
		}
		return body.Guide.Channels[0].Programmes[0]
	}
	p := guide()
	if p.Title != "Night Film" || p.Year != 1999 || p.Rating == nil || p.Rating.Value != "R" || strings.Join(p.Categories, ",") != "Drama,Thriller" || p.Image != "/v1/items/"+item.Public+"/art/poster" {
		t.Fatalf("library programme facts: %+v", p)
	}
	if _, err = db.Exec(`INSERT INTO profile_restrictions(profile_id,maximum_age,allow_unrated) VALUES('profile',13,0)`); err != nil {
		t.Fatal(err)
	}
	p = guide()
	if p.Title == "Night Film" || p.Year != 0 || p.Rating != nil || len(p.Categories) != 0 || p.Image != "" {
		t.Fatalf("restricted slot leaked facts: %+v", p)
	}
}

func tl11AssertChannelGuideSpecResponse(t *testing.T, method, path string, w *httptest.ResponseRecorder) {
	t.Helper()
	if err := apispec.ValidateErrorEnvelope(w.Code, w.Body.Bytes()); method != "HEAD" && err != nil {
		t.Fatalf("%s %s %d: %v", method, path, w.Code, err)
	}
	doc, schema, err := apispec.Response(method, path, w.Code)
	if err != nil {
		t.Fatal(err)
	}
	if problems := doc.ValidateJSON(schema, w.Body.Bytes()); len(problems) > 0 {
		t.Fatalf("%s %s %d does not match %s: %v\n%s", method, path, w.Code, doc.File, problems, w.Body.String())
	}
}
