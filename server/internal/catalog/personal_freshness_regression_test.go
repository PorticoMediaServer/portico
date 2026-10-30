package catalog

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

func TestContinueDismissalPublishesSemanticChange(t *testing.T) {
	s, db, v := savedFixture(t)
	item := phase34SavedFixtureItem(t, db)
	profile := identity.PersonalKey(v)
	homeExec(t, db, `INSERT INTO progress(profile_id,item_id,position,unit,playback_id) VALUES(?,?,0,0,'play')`, profile, item.ID)
	accept(t, s, db, v, item.Public, 1, 40, 100, "playing")
	state, err := s.Personal(profile, item.Public)
	if err != nil {
		t.Fatal(err)
	}
	for i, dismissed := range []bool{true, false} {
		var before int64
		if err = db.QueryRow(`SELECT revision FROM viewer_revisions WHERE profile_id=? AND library_id='library'`, profile).Scan(&before); err != nil {
			t.Fatal(err)
		}
		homeExec(t, db, `DELETE FROM api_change_pending`)
		state, err = s.SetPersonal("local:account", profile, item.Public, PersonalMutation{OperationID: []string{"dismiss", "restore"}[i], ExpectedRevision: state.Revision, ContinueDismissed: &dismissed}, nil)
		if err != nil {
			t.Fatal(err)
		}
		var after, hints int64
		if err = db.QueryRow(`SELECT revision FROM viewer_revisions WHERE profile_id=? AND library_id='library'`, profile).Scan(&after); err != nil {
			t.Fatal(err)
		}
		if err = db.QueryRow(`SELECT count(*) FROM api_change_pending WHERE audience=? AND type='personal.changed' AND resource_id='library'`, "personal:"+profile).Scan(&hints); err != nil {
			t.Fatal(err)
		}
		if after <= before || hints != 1 {
			t.Fatalf("dismiss=%v: semantic revision %d -> %d, hints=%d", dismissed, before, after, hints)
		}
	}
}

func TestExplicitResumeAdvancesCommandRevision(t *testing.T) {
	s, db, v := savedFixture(t)
	item := phase34SavedFixtureItem(t, db)
	profile := identity.PersonalKey(v)
	no := false
	pos := 20.
	first, err := s.SetPersonal("local:account", profile, item.Public, PersonalMutation{OperationID: "resume-one", Watched: &no, ProgressSeconds: &pos}, nil)
	if err != nil {
		t.Fatal(err)
	}
	pos = 40
	second, err := s.SetPersonal("local:account", profile, item.Public, PersonalMutation{OperationID: "resume-two", ExpectedRevision: first.Revision, Watched: &no, ProgressSeconds: &pos}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if second.Revision <= first.Revision {
		t.Fatalf("manual resume kept command revision %d -> %d", first.Revision, second.Revision)
	}
	pos = 60
	_, err = s.SetPersonal("local:account", profile, item.Public, PersonalMutation{OperationID: "stale-resume", ExpectedRevision: first.Revision, Watched: &no, ProgressSeconds: &pos}, nil)
	if !errors.Is(err, ErrPersonalConflict) {
		t.Fatalf("stale resume: %v", err)
	}
	state, err := s.Personal(profile, item.Public)
	if err != nil || state.ProgressSeconds != 40 {
		t.Fatalf("stale edit changed position: %+v %v", state, err)
	}
}

func TestHistoryOnlyChangesPublishPrivateHints(t *testing.T) {
	for _, clear := range []bool{false, true} {
		name := "noncanonical"
		if clear {
			name = "clear"
		}
		t.Run(name, func(t *testing.T) {
			s, db, v := savedFixture(t)
			item := phase34SavedFixtureItem(t, db)
			profile := identity.PersonalKey(v)
			if clear {
				homeExec(t, db, `INSERT INTO progress(profile_id,item_id,position,unit,playback_id) VALUES(?,?,0,0,'other-device')`, profile, item.ID)
			}
			homeExec(t, db, `DELETE FROM api_change_pending`)
			tx, err := db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			if err = s.AcceptPlaybackProgress(tx, v, "other-device", item.Public, 1, 40, 100, "playing", clear); err != nil {
				tx.Rollback()
				t.Fatal(err)
			}
			if err = tx.Commit(); err != nil {
				t.Fatal(err)
			}
			kind := "progress.changed"
			if clear {
				var revision int64
				if err = db.QueryRow(`SELECT revision FROM personal_activity_revisions WHERE profile_id=?`, profile).Scan(&revision); err != nil {
					t.Fatal(err)
				}
				homeExec(t, db, `DELETE FROM api_change_pending`)
				if _, err = s.MutateActivity(profile, ActivityMutation{OperationID: "clear-history-freshness", ExpectedRevision: revision, Action: "clear-history"}, func(*sql.Tx) error { return nil }); err != nil {
					t.Fatal(err)
				}
				kind = "personal.changed"
			}
			var hints int64
			if err = db.QueryRow(`SELECT count(*) FROM api_change_pending WHERE audience=? AND type=? AND resource_kind='personal' AND resource_id='history'`, "personal:"+profile, kind).Scan(&hints); err != nil {
				t.Fatal(err)
			}
			if hints != 1 {
				t.Fatalf("%s History published %d %s hints", name, hints, kind)
			}
		})
	}
}

func TestReplayingDismissedTitlePublishesMembershipChange(t *testing.T) {
	s, db, v := savedFixture(t)
	item := phase34SavedFixtureItem(t, db)
	profile := identity.PersonalKey(v)
	homeExec(t, db, `INSERT INTO progress(profile_id,item_id,position,unit,playback_id) VALUES(?,?,0,0,'play')`, profile, item.ID)
	accept(t, s, db, v, item.Public, 1, 40, 100, "playing")
	state, err := s.Personal(profile, item.Public)
	if err != nil {
		t.Fatal(err)
	}
	yes := true
	if _, err = s.SetPersonal("local:account", profile, item.Public, PersonalMutation{OperationID: "dismiss-before-replay", ExpectedRevision: state.Revision, ContinueDismissed: &yes}, nil); err != nil {
		t.Fatal(err)
	}
	var before, after, hints int64
	if err = db.QueryRow(`SELECT revision FROM viewer_revisions WHERE profile_id=? AND library_id='library'`, profile).Scan(&before); err != nil {
		t.Fatal(err)
	}
	homeExec(t, db, `DELETE FROM api_change_pending`)
	homeExec(t, db, `UPDATE progress SET playback_id='replay' WHERE profile_id=? AND item_id=?`, profile, item.ID)
	if err = db.QueryRow(`SELECT revision FROM viewer_revisions WHERE profile_id=? AND library_id='library'`, profile).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`SELECT count(*) FROM api_change_pending WHERE audience=? AND type='personal.changed' AND resource_id='library'`, "personal:"+profile).Scan(&hints); err != nil {
		t.Fatal(err)
	}
	if after <= before || hints != 1 {
		t.Fatalf("replay membership revision %d -> %d, hints=%d", before, after, hints)
	}
}

// A personal hint names the title it was about, so a title page elsewhere can
// ignore changes to other titles; two unrelated titles leave it unnamed.
func TestPersonalHintNamesTheTitle(t *testing.T) {
	s, db, v := savedFixture(t)
	item := phase34SavedFixtureItem(t, db)
	profile := identity.PersonalKey(v)
	homeExec(t, db, `DELETE FROM api_change_pending`)
	yes := true
	if _, err := s.SetPersonal("local:account", profile, item.Public, PersonalMutation{OperationID: "fav-one", Favorite: &yes}, nil); err != nil {
		t.Fatal(err)
	}
	var titles sql.NullString
	if err := db.QueryRow(`SELECT json_extract(data,'$.titles') FROM api_change_pending WHERE audience=? AND type='personal.changed'`, "personal:"+profile).Scan(&titles); err != nil {
		t.Fatal(err)
	}
	var hexID string
	if err := db.QueryRow(`SELECT lower(hex(public_id)) FROM catalog_entities WHERE id=?`, item.ID).Scan(&hexID); err != nil {
		t.Fatal(err)
	}
	if !titles.Valid || titles.String == "" || titles.String[:len(hexID)] != hexID {
		t.Fatalf("the hint does not name the title: %+v (want %s first)", titles, hexID)
	}
	// Another title in the same library before the hint is published: unnamed.
	c := catalogtest.New(t, db)
	var library int64
	if err := db.QueryRow(`SELECT library_id FROM catalog_entities WHERE id=?`, item.ID).Scan(&library); err != nil {
		t.Fatal(err)
	}
	other := c.Movie(library, "/fixture/other.mkv", "Another Film", 2001).ID
	homeExec(t, db, `INSERT INTO personal_items(profile_id,item_id,revision,favorite) VALUES(?,?,1,1)`, profile, other)
	if err := db.QueryRow(`SELECT json_extract(data,'$.titles') FROM api_change_pending WHERE audience=? AND type='personal.changed'`, "personal:"+profile).Scan(&titles); err != nil {
		t.Fatal(err)
	}
	if titles.Valid {
		t.Fatalf("two titles still name one: %q", titles.String)
	}
}
