package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"github.com/PorticoMediaServer/portico/server/internal/reading"
	"github.com/PorticoMediaServer/portico/server/internal/readingtest"
)

func TestReadingPrivatePauseHistoryAndVersionIsolation(t *testing.T) {
	r := newReadingFixture(t, "book", "pdf")
	ctx := context.Background()
	session, _, err := r.s.Prepare(ctx, r.p, r.item, r.source)
	if err != nil {
		t.Fatal(err)
	}
	page := int64(0)
	location := reading.Location{Format: "pdf", PageIndex: &page, Progression: .1}
	first, _, err := r.s.SaveSessionProgress(ctx, r.p, session.ID, 0, location)
	if err != nil {
		t.Fatal(err)
	}
	// Use the actual preference API, including its account/profile authority and CAS.
	prefs := r.f.raw("GET", "/v1/preferences?deviceClass=web", r.f.owner.AccessToken, nil, nil)
	if prefs.Code != 200 {
		t.Fatal(prefs.Code, prefs.Body.String())
	}
	var doc struct {
		Data operations.PreferenceSnapshot `json:"data"`
	}
	if err = json.Unmarshal(prefs.Body.Bytes(), &doc); err != nil {
		t.Fatal(prefs.Body.String(), err)
	}
	var preferenceRevision int64
	for _, document := range doc.Data.Documents {
		if document.Scope == operations.ScopeProfileServer {
			preferenceRevision = document.Revision
		}
	}
	if preferenceRevision < 1 {
		t.Fatal("profile preference document is missing")
	}
	paused := r.f.raw("PATCH", "/v1/preferences", r.f.owner.AccessToken, nil, map[string]any{
		"expectedRevision": preferenceRevision, "idempotencyKey": "reading-pause", "scope": "profile-server", "deviceClass": "web",
		"values": map[string]any{"privacy.pauseWatchHistory": true},
	})
	if paused.Code != 200 {
		t.Fatal(paused.Code, paused.Body.String())
	}
	location.Progression = .9
	same, saved, err := r.s.SaveSessionProgress(ctx, r.p, session.ID, first.Revision, location)
	if err != nil || saved || same == nil || same.Revision != first.Revision || same.Location.Progression != .1 {
		t.Fatal("Pause history changed Reading state", same, saved, err)
	}
	// Reading-specific persistence is independent of the timed-media progress table.
	var count int
	if err = r.f.db.QueryRow(`SELECT count(*) FROM progress WHERE profile_id=?`, identity.PersonalKey(r.p.Viewer)).Scan(&count); err != nil || count != 0 {
		t.Fatal("reading wrote audio/video positions", count, err)
	}
	read, err := r.s.SessionProgress(ctx, r.p, session.ID)
	if err != nil || read.Revision != first.Revision {
		t.Fatal(read, err)
	}
	// No body-supplied profile identity or source id is accepted by session writes.
	other := r.p
	other.ProfileID = "other"
	if _, _, err = r.s.SaveSessionProgress(ctx, other, session.ID, 1, location); err == nil {
		t.Fatal("profile crossover")
	}
	if _, err = r.s.Open(ctx, r.p, r.item, "another-version", first.SourceRevision); err == nil {
		t.Fatal("version crossover")
	}
}

func TestReadingPrivateMemberAndProfileAccessIsLive(t *testing.T) {
	r := newReadingFixture(t, "book", "pdf")
	ctx := context.Background()
	owner := readingOwnerLogin(t, r)
	member, err := r.f.id.CreateDirectMember(ctx, owner.AccountToken, identity.DirectMemberCreate{
		Username: "reader", Password: "different-test-password", Name: "Reader", AllowedLibraries: []string{r.library},
	})
	if err != nil {
		t.Fatal(err)
	}
	login, err := r.f.id.DirectLogin(ctx, "reader", "different-test-password")
	if err != nil || login.Session == nil {
		t.Fatal(login, err)
	}
	principal, err := r.f.id.Authenticate(login.Session.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	session, _, err := r.s.Prepare(ctx, principal, r.item, r.source)
	if err != nil {
		t.Fatal("ordinary member could not read", err)
	}
	list, err := r.s.List(ctx, principal, r.library, "", 1)
	if err != nil || len(list.Items) != 1 || list.Items[0].ID != r.item {
		t.Fatal(list, err)
	}
	source, err := r.s.OpenSession(ctx, principal, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	if err = r.f.id.UpdateDirectMember(ctx, owner.AccountToken, member.ID, identity.DirectMemberInput{
		Username: member.Username, Name: "Reader", ExpectedRevision: member.Revision, AllowedLibraries: []string{},
	}); err != nil {
		t.Fatal(err)
	}
	if n, err := source.ReadAt(make([]byte, 5), 0); n != 0 || err == nil {
		t.Fatal("removed library grant leaked bytes", n, err)
	}
	list, err = r.s.List(ctx, principal, r.library, "", 1)
	if err == nil && len(list.Items) != 0 {
		t.Fatal("hidden item leaked into bounded list", list)
	}

	profiles, err := r.f.id.CreateDirectProfile(ctx, owner.AccountToken, "Restricted reader", "blue")
	if err != nil {
		t.Fatal(err)
	}
	var child identity.DirectProfile
	for _, p := range profiles.Profiles {
		if !p.Primary && p.Name == "Restricted reader" {
			child = p
		}
	}
	selected, err := r.f.id.SelectDirectProfile(ctx, owner.AccountToken, child.ID, identity.DirectSelection{})
	if err != nil {
		t.Fatal(err)
	}
	principal, err = r.f.id.Authenticate(selected.Session.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	session, _, err = r.s.Prepare(ctx, principal, r.item, r.source)
	if err != nil {
		t.Fatal("non-primary profile could not read", err)
	}
	source, err = r.s.OpenSession(ctx, principal, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	if _, err = r.f.id.SetProfileRestrictions(ctx, owner.AccountToken, child.ID, identity.ProfileRestrictionEdit{
		ExpectedRevision: 1, AllowUnrated: false,
	}); err != nil {
		t.Fatal(err)
	}
	if n, err := source.ReadAt(make([]byte, 5), 0); n != 0 || err == nil {
		t.Fatal("new title restriction leaked bytes", n, err)
	}
	if _, err = r.s.Versions(ctx, principal, r.item); err == nil {
		t.Fatal("restricted versions leaked")
	}
	list, err = r.s.List(ctx, principal, r.library, "", 1)
	if err == nil && len(list.Items) != 0 {
		t.Fatal("restricted title listed", list)
	}
}

func TestReadingPrivateProfileErasurePreservesSiblingAndArchive(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	r := newReadingFixture(t, "book", "pdf")
	ctx := context.Background()
	owner := readingOwnerLogin(t, r)
	snapshot, err := r.f.id.CreateDirectProfile(ctx, owner.AccountToken, "Erase reader", "blue")
	if err != nil {
		t.Fatal(err)
	}
	var child identity.DirectProfile
	for _, p := range snapshot.Profiles {
		if !p.Primary && p.Name == "Erase reader" {
			child = p
		}
	}
	selected, err := r.f.id.SelectDirectProfile(ctx, owner.AccountToken, child.ID, identity.DirectSelection{})
	if err != nil {
		t.Fatal(err)
	}
	principal, err := r.f.id.Authenticate(selected.Session.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	page := int64(0)
	loc := reading.Location{Format: "pdf", PageIndex: &page, Progression: .3}
	childSession, _, err := r.s.Prepare(ctx, principal, r.item, r.source)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = r.s.SaveSessionProgress(ctx, principal, childSession.ID, 0, loc); err != nil {
		t.Fatal(err)
	}
	ownerSession, _, err := r.s.Prepare(ctx, r.p, r.item, r.source)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = r.s.SaveSessionProgress(ctx, r.p, ownerSession.ID, 0, loc); err != nil {
		t.Fatal(err)
	}
	activePath := filepath.Join(filepath.Dir(r.path), "Still present.pdf")
	if err = os.WriteFile(activePath, readingtest.PDF(), 0600); err != nil {
		t.Fatal(err)
	}
	readingScan(t, r.f, r.library)
	var activeItem, activeToken string
	if err = r.f.db.QueryRow(`SELECT pid(e.public_id),a.token FROM reading_publications p JOIN catalog_entities e ON e.id=p.item_id JOIN catalog_assets a ON a.id=p.asset_id WHERE a.path=?`, activePath).Scan(&activeItem, &activeToken); err != nil {
		t.Fatal(err)
	}
	for _, who := range []identity.Principal{principal, r.p} {
		active, _, err := r.s.Prepare(ctx, who, activeItem, activeToken)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err = r.s.SaveSessionProgress(ctx, who, active.ID, 0, loc); err != nil {
			t.Fatal(err)
		}
	}
	childKey := identity.PersonalKey(principal.Viewer)
	ownerKey := identity.PersonalKey(r.p.Viewer)
	// A disconnected authority's archived owner tuple may share the raw IDs.
	// It is persisted data, never an authenticated principal for this service.
	archivedViewer := principal.Viewer
	archivedViewer.Authority = "hosted"
	archivedKey := identity.PersonalKey(archivedViewer)
	err = dbwork.WithWriteTx(ctx, r.f.db, dbwork.ClassInteractive, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO reading_progress(owner_key,item_id,asset_id,source_revision,format,cfi,section,page_index,progression,revision,observed_at) SELECT ?,item_id,asset_id,source_revision,format,cfi,section,page_index,progression,revision,observed_at FROM reading_progress WHERE owner_key=?`, archivedKey, childKey)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	// Catalogue removal retains all three owner tuples without touching files.
	if err = r.f.cat.RemoveTitle(ctx, r.item); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{childKey, ownerKey, archivedKey} {
		var n int
		if err = r.f.db.QueryRow(`SELECT count(*) FROM retained_reading_progress p JOIN retained_title_state s ON s.id=p.state_id WHERE s.profile_id=?`, key).Scan(&n); err != nil || n != 1 {
			t.Fatal("retained owner", key, n, err)
		}
	}
	if err = retireProfileInHTTPTest(t, r.f.id, ctx, owner.AccountToken, child.ID, child.Revision, true); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		key  string
		want int
	}{{childKey, 0}, {ownerKey, 1}, {archivedKey, 1}} {
		var n int
		if err = r.f.db.QueryRow(`SELECT count(*) FROM retained_reading_progress p JOIN retained_title_state s ON s.id=p.state_id WHERE s.profile_id=?`, tc.key).Scan(&n); err != nil || n != tc.want {
			t.Fatal("erasure crossed owner", tc.key, n, err)
		}
	}
	if _, err = r.s.OpenSession(ctx, principal, childSession.ID); err == nil {
		t.Fatal("erased profile session reopened")
	}
	if _, err = os.Stat(r.path); err != nil {
		t.Fatal("erasure removed publication", err)
	}
	var n int
	for _, tc := range []struct {
		key  string
		want int
	}{{childKey, 0}, {ownerKey, 1}, {archivedKey, 1}} {
		if err = r.f.db.QueryRow(`SELECT count(*) FROM reading_progress WHERE owner_key=?`, tc.key).Scan(&n); err != nil || n != tc.want {
			t.Fatal("active erasure crossed owner", tc.key, n, err)
		}
	}
	if _, err = os.Stat(activePath); err != nil {
		t.Fatal("erasure removed sibling publication", err)
	}
}
