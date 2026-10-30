package httpapi

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/hosted"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/ingestion"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

// CD-05: the folder picker asked GET /v1/admin/filesystem for limit=500 while
// the published contract (and every listing in admin-libraries) allows 1-200.
// The server keeps the contract: a page of at most 200, a named refusal above
// it, and a cursor that walks a large folder completely, including names that
// differ only by case across a page boundary.
func TestFilesystemBrowseContract(t *testing.T) {
	root := t.TempDir()
	db, err := persistence.Open(filepath.Join(root, "server.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ident, err := identity.New(db, root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('owner','owner',x'00','profile',1)`); err != nil {
		t.Fatal(err)
	}
	owner, err := ident.Issue("owner", "profile", "local", "owner", 1)
	if err != nil {
		t.Fatal(err)
	}
	cat := catalog.New(db)
	control, err := hosted.New(db, ident, "http://127.0.0.1:19410", base64.RawURLEncoding.EncodeToString(make([]byte, 32)), zeroHostedRootID)
	if err != nil {
		t.Fatal(err)
	}
	// Outside the server's state directory: the picker never browses Portico's own state.
	media := filepath.Join(t.TempDir(), "media")
	folder := filepath.Join(media, "Films")
	// 205 folders, with "Case" and "case" at positions 200 and 201: the first
	// page ends between two names that differ only by case. On a
	// case-insensitive filesystem only one of the pair can exist, so the
	// expected total is counted from what was actually created.
	names := []string{"Case", "case"}
	for i := 0; i < 199; i++ {
		names = append(names, fmt.Sprintf("a-%03d", i))
	}
	for i := 0; i < 4; i++ {
		names = append(names, fmt.Sprintf("title-%03d", i))
	}
	for _, name := range names {
		if err = os.MkdirAll(filepath.Join(folder, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	created, err := os.ReadDir(folder)
	if err != nil {
		t.Fatal(err)
	}
	pickerRoots := []string{media}
	for i := 0; i < 65; i++ {
		dir := filepath.Join(t.TempDir(), fmt.Sprintf("root-%03d", i))
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		pickerRoots = append(pickerRoots, dir)
	}
	t.Setenv("PORTICO_MEDIA_ROOTS", strings.Join(pickerRoots, string(os.PathListSeparator)))
	handler := New(Dependencies{DB: db, Identity: ident, Catalog: cat, Ingestion: ingestion.New(db, cat, assets.Probe{}), Hosted: control})
	get := func(query string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/v1/admin/filesystem?"+query, bytes.NewReader(nil))
		r.Header.Set("Authorization", "Bearer "+owner.AccessToken)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}

	// The roots answer (configured media roots) matches the contract.
	roots := get("")
	if roots.Code != 200 {
		t.Fatalf("roots: %d %s", roots.Code, roots.Body.String())
	}
	assertSpecResponse(t, "GET", "/v1/admin/filesystem", roots)
	var rootPage struct {
		Result struct {
			Roots     []json.RawMessage `json:"roots"`
			Truncated bool              `json:"truncated"`
		} `json:"result"`
	}
	if err := json.Unmarshal(roots.Body.Bytes(), &rootPage); err != nil {
		t.Fatal(err)
	}
	if len(rootPage.Result.Roots) != 64 || !rootPage.Result.Truncated {
		t.Fatalf("unbounded or silently truncated roots: %s", roots.Body.String())
	}
	last := get("path=" + url.QueryEscape(pickerRoots[len(pickerRoots)-1]))
	if last.Code != 200 {
		t.Fatalf("omitted root: %d %s", last.Code, last.Body.String())
	}
	assertSpecResponse(t, "GET", "/v1/admin/filesystem", last)

	// Over the contract's maximum: refused, naming the field.
	for _, limit := range []string{"500", "201", "0", "many"} {
		w := get("limit=" + limit + "&path=" + url.QueryEscape(folder))
		if w.Code != 400 {
			t.Fatalf("limit=%s: %d %s", limit, w.Code, w.Body.String())
		}
		assertSpecResponse(t, "GET", "/v1/admin/filesystem", w)
		var refusal struct {
			Error struct {
				Fields []string `json:"fields"`
			} `json:"error"`
		}
		if json.Unmarshal(w.Body.Bytes(), &refusal) != nil || len(refusal.Error.Fields) != 1 || refusal.Error.Fields[0] != "limit" {
			t.Fatalf("limit=%s refusal does not name the field: %s", limit, w.Body.String())
		}
	}

	// At the maximum, the cursor walks every folder exactly once.
	seen := map[string]bool{}
	cursor, pages := "", 0
	for {
		query := "limit=200&path=" + url.QueryEscape(folder)
		if cursor != "" {
			query += "&cursor=" + url.QueryEscape(cursor)
		}
		w := get(query)
		if w.Code != 200 {
			t.Fatalf("page %d: %d %s", pages, w.Code, w.Body.String())
		}
		assertSpecResponse(t, "GET", "/v1/admin/filesystem", w)
		var envelope struct {
			Result struct {
				Entries []struct {
					Name string `json:"name"`
				} `json:"entries"`
				NextCursor string `json:"nextCursor"`
			} `json:"result"`
		}
		if err = json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		if len(envelope.Result.Entries) > 200 {
			t.Fatalf("page of %d entries exceeds the limit", len(envelope.Result.Entries))
		}
		for _, entry := range envelope.Result.Entries {
			if seen[entry.Name] {
				t.Fatalf("%q listed twice", entry.Name)
			}
			seen[entry.Name] = true
		}
		pages++
		if cursor = envelope.Result.NextCursor; cursor == "" {
			break
		}
		if pages > 5 {
			t.Fatal("cursor does not terminate")
		}
	}
	if len(seen) != len(created) || pages != 2 {
		t.Fatalf("walked %d of %d folders in %d pages", len(seen), len(created), pages)
	}
}
