package httpapi

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
)

// The matrix probe for POST /v1/playlists/{id}/entries used to send itemIds to an
// action that only accepts itemId, so it answered 400 before reading any item and
// the route's hidden-vs-absent behaviour was never exercised. These tests send one
// real item id at the playlist's own current revision, and check the three
// properties the route owes:
//
//  1. a title this profile may not see is indistinguishable from one that does
//     not exist (same status, same code, no identifier in the answer), and it
//     never becomes a playlist member;
//  2. creation carries its initial items and refuses the whole request for a
//     restricted title exactly as it does for an absent one, atomically;
//  3. the accepting writer reads current authority, not the authority the caller
//     held when the playlist was read: a ceiling that tightens between the read
//     and the write refuses the write.
//
// Session revocation is already covered by TestPlaylistHTTPShareNeverGrantsMedia.
//

type playlistRestrictionFixture struct {
	handler http.Handler
	owner   string
	db      *sql.DB
	// visible is a PG film inside the profile's ceiling; hidden is an R film
	// beyond it. Both live in the same library the profile may see, so only the
	// content restriction (not library access) separates them.
	visible string
	hidden  string
}

// newPlaylistRestrictionFixture builds an owner session whose profile has a
// maximum age of ceiling, one visible and one restricted film in the same
// library, and the rating ages both need.
func newPlaylistRestrictionFixture(t *testing.T, ceiling int) playlistRestrictionFixture {
	t.Helper()
	handler, owner, _, db := editorHTTPFixtureDB(t)
	d := Dependencies{DB: db}
	f := playlistRestrictionFixture{handler: handler, owner: owner, db: db}
	visible := tl6FixtureMovie(t, d, "private-library", "Visible.mkv", "Visible Film", 2020)
	hidden := tl6FixtureMovie(t, d, "private-library", "Restricted.mkv", "Restricted Film", 2021)
	f.visible, f.hidden = visible.Public, hidden.Public
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.Exec(query, args...); err != nil {
			t.Fatalf("%v\n%s", err, query)
		}
	}
	// Classified ratings: an unmapped spelling is withheld until the classifier
	// publishes its age, which is not what this fixture is about.
	exec(`INSERT INTO content_rating_ages(value_key,minimum_age) VALUES('pg',8),('r',17) ON CONFLICT(value_key) DO UPDATE SET minimum_age=excluded.minimum_age`)
	catalogue := catalogtest.New(t, db)
	catalogue.Attributes(visible.ID, "contentRating", "PG")
	catalogue.Attributes(hidden.ID, "contentRating", "R")
	catalogue.Drain()
	f.setCeiling(t, ceiling)
	return f
}

func (f playlistRestrictionFixture) setCeiling(t *testing.T, ceiling int) {
	t.Helper()
	if _, err := f.db.Exec(`INSERT INTO profile_restrictions(profile_id,maximum_age,allow_unrated,revision) VALUES('profile',?,1,1) ON CONFLICT(profile_id) DO UPDATE SET maximum_age=excluded.maximum_age,revision=revision+1`, ceiling); err != nil {
		t.Fatal(err)
	}
}

func (f playlistRestrictionFixture) request(t *testing.T, method, path string, body any) (int, string) {
	t.Helper()
	var raw []byte
	if body != nil {
		var err error
		if raw, err = json.Marshal(body); err != nil {
			t.Fatal(err)
		}
	}
	w := editorRequest(t, f.handler, method, path, f.owner, "application/json", raw)
	return w.Code, w.Body.String()
}

// createPlaylist creates an empty playlist and returns its id and revision.
func (f playlistRestrictionFixture) createPlaylist(t *testing.T, operation string) (string, int64) {
	t.Helper()
	status, body := f.request(t, "POST", "/v1/playlists", map[string]any{"operationId": operation, "name": "Held"})
	if status != 200 {
		t.Fatalf("create playlist: %d %s", status, body)
	}
	var receipt struct {
		PlaylistID string `json:"playlistId"`
		Revision   int64  `json:"revision"`
	}
	if err := json.Unmarshal([]byte(body), &receipt); err != nil || receipt.PlaylistID == "" || receipt.Revision < 1 {
		t.Fatalf("create playlist receipt: %s %v", body, err)
	}
	return receipt.PlaylistID, receipt.Revision
}

func (f playlistRestrictionFixture) revision(t *testing.T, id string) int64 {
	t.Helper()
	var revision int64
	if err := f.db.QueryRow(`SELECT revision FROM catalog_playlists WHERE token=?`, id).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	return revision
}

// membership counts this playlist's occurrences of one item, across every
// profile: nothing was written, not only nothing the caller can see.
func (f playlistRestrictionFixture) membership(t *testing.T, playlist, item string) int {
	t.Helper()
	var n int
	if err := f.db.QueryRow(`SELECT count(*) FROM catalog_playlist_entries e JOIN catalog_playlists p ON p.id=e.playlist_id JOIN catalog_entities i ON i.id=e.item_id WHERE p.token=? AND i.public_id=pid_blob(?)`, playlist, item).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func (f playlistRestrictionFixture) playlists(t *testing.T) int {
	t.Helper()
	var n int
	if err := f.db.QueryRow(`SELECT count(*) FROM catalog_playlists`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// A direct add names one item, so a title beyond this profile's ceiling must
// answer exactly as an id that does not exist, and must not become a member.
func TestPlaylistDirectAddTreatsRestrictedItemAsAbsent(t *testing.T) {
	f := newPlaylistRestrictionFixture(t, 13)
	id, revision := f.createPlaylist(t, "restriction-create")
	hiddenStatus, hiddenBody := f.request(t, "POST", "/v1/playlists/"+id+"/entries", map[string]any{"operationId": "add-restricted", "expectedRevision": revision, "itemId": f.hidden})
	absentStatus, absentBody := f.request(t, "POST", "/v1/playlists/"+id+"/entries", map[string]any{"operationId": "add-absent", "expectedRevision": revision, "itemId": absentTwin(f.hidden)})
	if hiddenStatus != absentStatus || bodyErrorCode(hiddenBody) != bodyErrorCode(absentBody) {
		t.Fatalf("a restricted title is distinguishable from an absent one: restricted %d %q, absent %d %q",
			hiddenStatus, bodyErrorCode(hiddenBody), absentStatus, bodyErrorCode(absentBody))
	}
	if hiddenStatus != http.StatusNotFound || bodyErrorCode(hiddenBody) != "not_found" {
		t.Fatalf("a restricted title answered %d %q, want 404 not_found", hiddenStatus, bodyErrorCode(hiddenBody))
	}
	if strings.Contains(hiddenBody, f.hidden) {
		t.Fatalf("the refusal named the restricted title: %s", hiddenBody)
	}
	if n := f.membership(t, id, f.hidden); n != 0 {
		t.Fatalf("a restricted title reached playlist membership (%d occurrences)", n)
	}
	if got := f.revision(t, id); got != revision {
		t.Fatalf("a refused add moved the playlist revision: %d -> %d", revision, got)
	}
	// The same route still accepts a title this profile may see, at the revision
	// the refused attempts left untouched.
	status, body := f.request(t, "POST", "/v1/playlists/"+id+"/entries", map[string]any{"operationId": "add-visible", "expectedRevision": revision, "itemId": f.visible})
	if status != 200 {
		t.Fatalf("a permitted title was refused: %d %s", status, body)
	}
	if n := f.membership(t, id, f.visible); n != 1 {
		t.Fatalf("the permitted title was not added (%d occurrences)", n)
	}
}

// Creation carries its initial items, so a restricted title in itemIds refuses
// the whole request exactly as an absent id does. The refusal is atomic: no
// playlist and no membership row is written for either request.
func TestPlaylistCreateWithItemsTreatsRestrictedItemAsAbsent(t *testing.T) {
	f := newPlaylistRestrictionFixture(t, 13)
	before := f.playlists(t)
	hiddenStatus, hiddenBody := f.request(t, "POST", "/v1/playlists", map[string]any{"operationId": "create-restricted", "name": "Held", "itemIds": []string{f.hidden}})
	absentStatus, absentBody := f.request(t, "POST", "/v1/playlists", map[string]any{"operationId": "create-absent", "name": "Held", "itemIds": []string{absentTwin(f.hidden)}})
	if hiddenStatus != absentStatus || bodyErrorCode(hiddenBody) != bodyErrorCode(absentBody) {
		t.Fatalf("creation tells a restricted title from an absent one: restricted %d %q, absent %d %q",
			hiddenStatus, bodyErrorCode(hiddenBody), absentStatus, bodyErrorCode(absentBody))
	}
	if hiddenStatus != http.StatusNotFound || bodyErrorCode(hiddenBody) != "not_found" {
		t.Fatalf("creation with a restricted title answered %d %q, want 404 not_found", hiddenStatus, bodyErrorCode(hiddenBody))
	}
	if strings.Contains(hiddenBody, f.hidden) {
		t.Fatalf("the refusal named the restricted title: %s", hiddenBody)
	}
	// The restricted title is in no playlist's membership, under any profile.
	var n int
	if err := f.db.QueryRow(`SELECT count(*) FROM catalog_playlist_entries e JOIN catalog_entities i ON i.id=e.item_id WHERE i.public_id=pid_blob(?)`, f.hidden).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("creation wrote a restricted title into playlist membership (%d occurrences)", n)
	}
	// Atomic refusal leaves no playlist behind for either request.
	if after := f.playlists(t); after != before {
		t.Fatalf("a refused creation left %d playlist(s) behind", after-before)
	}
	// A permitted title in the same body is still created with its item.
	status, body := f.request(t, "POST", "/v1/playlists", map[string]any{"operationId": "create-visible", "name": "Held", "itemIds": []string{f.visible}})
	if status != 200 {
		t.Fatalf("creation with a permitted title was refused: %d %s", status, body)
	}
	var receipt struct {
		PlaylistID string `json:"playlistId"`
	}
	if err := json.Unmarshal([]byte(body), &receipt); err != nil || receipt.PlaylistID == "" {
		t.Fatalf("creation receipt: %s %v", body, err)
	}
	if got := f.membership(t, receipt.PlaylistID, f.visible); got != 1 {
		t.Fatalf("creation did not carry its permitted item (%d occurrences)", got)
	}
}

// The accepting writer reads current authority. The same title is inside the
// ceiling when the playlist is read, and beyond it when the add arrives.
func TestPlaylistDirectAddRechecksCurrentItemAuthority(t *testing.T) {
	f := newPlaylistRestrictionFixture(t, 18)
	id, revision := f.createPlaylist(t, "authority-create")
	status, body := f.request(t, "POST", "/v1/playlists/"+id+"/entries", map[string]any{"operationId": "add-inside-ceiling", "expectedRevision": revision, "itemId": f.hidden})
	if status != 200 {
		t.Fatalf("a title inside the ceiling was refused: %d %s", status, body)
	}
	if n := f.membership(t, id, f.hidden); n != 1 {
		t.Fatalf("the title inside the ceiling was not added (%d occurrences)", n)
	}
	// The profile's ceiling tightens after that read, which is a real authority
	// change (profile_restrictions is an authority table: the caches are fenced
	// on the generation this write moves).
	f.setCeiling(t, 13)
	current := f.revision(t, id)
	status, body = f.request(t, "POST", "/v1/playlists/"+id+"/entries", map[string]any{"operationId": "add-after-ceiling", "expectedRevision": current, "itemId": f.hidden})
	if status == 200 {
		t.Fatalf("the add used the authority the viewer held before the ceiling tightened: %d %s", status, body)
	}
	if code := bodyErrorCode(body); code != "not_found" {
		t.Fatalf("the refusal answered %q, want not_found so it matches an absent id", code)
	}
	if n := f.membership(t, id, f.hidden); n != 1 {
		t.Fatalf("the refused add changed membership (%d occurrences)", n)
	}
	if got := f.revision(t, id); got != current {
		t.Fatalf("the refused add moved the playlist revision: %d -> %d", current, got)
	}
	// A title still inside the tightened ceiling is accepted at the same revision.
	if status, body = f.request(t, "POST", "/v1/playlists/"+id+"/entries", map[string]any{"operationId": "add-visible-after-ceiling", "expectedRevision": current, "itemId": f.visible}); status != 200 {
		t.Fatalf("a permitted title was refused after the ceiling changed: %d %s", status, body)
	}
	// Admission runs before the receipt replay: the exact request that was
	// accepted above, resent under its original operation id, is refused now
	// instead of answered from the stored receipt.
	replay := map[string]any{"operationId": "add-inside-ceiling", "expectedRevision": revision, "itemId": f.hidden}
	status, body = f.request(t, "POST", "/v1/playlists/"+id+"/entries", replay)
	if status == 200 {
		t.Fatalf("a receipt replay restored authority the profile no longer holds: %d %s", status, body)
	}
	if code := bodyErrorCode(body); code != "not_found" {
		t.Fatalf("the replayed refusal answered %q, want not_found from admission ahead of the receipt", code)
	}
	if n := f.membership(t, id, f.hidden); n != 1 {
		t.Fatalf("the replayed refusal changed membership (%d occurrences)", n)
	}
}
