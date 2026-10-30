package mediaanalysis

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

func TestAnalysisSessionFamilyAndPreparedClockBoundary(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	c := catalogtest.Open(t)
	library := c.Library("library", "Movies", "movie", "/fixture")
	item := c.Movie(library, "/fixture/movie.mp4", "Movie", 2024)
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.SetAssetTx(ctx, tx, item.Asset, map[string]any{"duration": float64(60)})
	})
	c.Drain()
	db := c.DB
	update := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	update(`UPDATE library_sources SET incarnation='incarnation',generation=4 WHERE id='library'`)
	update(`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns,state) VALUES('object','library',?,'incarnation','movie.mp4','revision','{}',1000,1,'available')`, item.Token)
	update(`INSERT INTO authorization_session_families(id,server_id,account_id,profile_id,authority,role,epoch,authorization_horizon,current_generation,revoked) VALUES('family','server','account','profile','local','owner',1,'2100-01-01T00:00:00Z',2,0),('retired-family','server','account','profile','local','owner',1,'2100-01-01T00:00:00Z',1,0),('other','server','other-account','other-profile','local','owner',1,'2100-01-01T00:00:00Z',1,0)`)
	update(`INSERT INTO authorization_family_tokens(token_hash,family_id,generation,expires_at,retired) VALUES('original','family',1,'2100-01-01T00:00:00Z',1),('current','family',2,'2100-01-01T00:00:00Z',0),('retired','retired-family',1,'2100-01-01T00:00:00Z',1),('other-current','other',1,'2100-01-01T00:00:00Z',0)`)
	update(`INSERT INTO playback_sessions(id,session_hash,account_id,profile_id,item_id,asset_id,generation,state,grant_hash,grant_token,expires_at,request_id,duration) VALUES('session','original','account','profile',?,?,3,'playing','grant-hash','grant','2100-01-01T00:00:00Z','request',60)`, item.ID, item.Token)
	update(`INSERT INTO playback_source_pins(session_id,asset_id,size,modified_ns) VALUES('session',?,1000,1)`, item.Token)
	update(`INSERT INTO playback_physical_source_pins(session_id,source_id,incarnation,configuration_generation) VALUES('session','library','incarnation',4)`)
	service := &Service{db: db}
	access := Access{LibraryID: "library", ItemID: item.Public, AccountID: "account", ProfileID: "profile", SessionHash: "current"}
	target := Target{SourceID: item.Token, SessionID: "session", Generation: 3}
	resolve := func(a Access, target Target) (Source, error) {
		t.Helper()
		tx, err := db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		return service.resolve(context.Background(), tx, a, target)
	}
	if source, err := resolve(access, target); err != nil || source.DurationUS != "60000000" || source.ID != item.Token {
		t.Fatal("current successor lost its original session", source, err)
	}

	// An active session's artifact target must not follow a new inventory
	// object with unchanged catalogue size and timestamp.
	update(`UPDATE library_sources SET root_identity='root-evidence',identity_confirmed=1 WHERE id='library'`)
	update(`UPDATE inventory_objects SET object_identity='ino:accepted' WHERE id='object'`)
	update(`UPDATE playback_source_pins SET binding='inventory',source_id='library',inventory_object_id='object',root='/fixture',root_identity='root-evidence',relative_path='movie.mp4',object_identity='ino:accepted' WHERE session_id='session'`)
	if _, err := resolve(access, target); err != nil {
		t.Fatal("accepted artifact target rejected", err)
	}
	update(`UPDATE inventory_objects SET object_identity='ino:replacement' WHERE id='object'`)
	if _, err := resolve(access, target); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("session artifact target followed replacement", err)
	}
	update(`UPDATE inventory_objects SET object_identity='ino:accepted' WHERE id='object'`)
	// The former catalog_source_dirty miniature-schema probe was not consumed by
	// resolveSource and does not exist in the canonical schema; test its actual
	// source and authority fences below instead.
	for _, hash := range []string{"retired", "other-current", "unknown"} {
		a := access
		a.SessionHash = hash
		if _, err := resolve(a, target); !errors.Is(err, sql.ErrNoRows) {
			t.Fatal("unrelated/retired token admitted", hash, err)
		}
	}
	a := access
	a.ProfileID = "other-profile"
	if _, err := resolve(a, target); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("profile boundary lost", err)
	}
	update(`UPDATE authorization_session_families SET revoked=1 WHERE id='family'`)
	if _, err := resolve(access, target); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("revoked family admitted", err)
	}
	update(`UPDATE authorization_session_families SET revoked=0,current_generation=3 WHERE id='family'`)
	if _, err := resolve(access, target); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("stale token generation admitted", err)
	}
	update(`UPDATE authorization_session_families SET current_generation=2 WHERE id='family'`)
	// Revocation stops live sessions in the real schema; restore this isolated
	// session after resetting the authority fixture for the remaining fences.
	update(`UPDATE playback_sessions SET state='playing' WHERE id='session'`)
	if _, err := resolve(access, target); err != nil {
		t.Fatal("current family did not recover", err)
	}
	badTarget := target
	badTarget.Generation++
	if _, err := resolve(access, badTarget); !errors.Is(err, ErrConflict) {
		t.Fatal("stale playback generation admitted", err)
	}
	update(`INSERT INTO prepared_media_jobs(id,item_id,asset_id,library_id,profile_id,target_id,selection_json,source_revision,principal_json,state,phase,created_ms,updated_ms) VALUES('job',?,?,'library','profile','target','{}','revision','{}','succeeded','complete',1,1)`, item.ID, item.Token)
	update(`INSERT INTO prepared_media_versions(id,item_id,asset_id,library_id,profile_id,target_id,source_revision,selection_json,part_index,input_evidence,transformation_digest,digest,size,facts_json,state,revision,created_ms,job_id) VALUES('version',?,?,'library','profile','target','revision','{}',0,'{}','transform',?,1,'{}','published',1,1,'job')`, item.ID, item.Token, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	update(`INSERT INTO prepared_media_session_pins(session_id,version_id,digest,size) VALUES('session','version','aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',1)`)
	var hasPin bool
	if err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM prepared_media_session_pins WHERE session_id='session')`).Scan(&hasPin); err != nil || !hasPin {
		t.Fatal("prepared fixture pin missing", hasPin, err)
	}
	if _, err := resolve(access, target); !errors.Is(err, ErrClock) {
		t.Fatal("matching duration invented a derivative clock map", err)
	}
	update(`DELETE FROM prepared_media_session_pins WHERE session_id='session'`)
	update(`UPDATE playback_physical_source_pins SET incarnation='replaced' WHERE session_id='session'`)
	if _, err := resolve(access, target); !errors.Is(err, ErrConflict) {
		t.Fatal("replaced physical root admitted", err)
	}
}
