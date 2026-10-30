package catalog

import (
	"context"
	"database/sql"

	"errors"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

func TestContainerMarkUndoRestoresProgressAndCompletion(t *testing.T) {
	for _, kind := range []string{"show", "season"} {
		t.Run(kind, func(t *testing.T) {
			s, scheduler, p, v, access, _, names := bulkFixture(t, 3)
			ctx := context.Background()
			yes := true
			no := false
			first, partial := names["ep000000"], names["ep000001"]
			if _, e := s.SetPersonal("a", v.Profile, first.Public, PersonalMutation{OperationID: "old-completed", Watched: &yes}, nil); e != nil {
				t.Fatal(e)
			}
			resume := 45.
			if _, e := s.SetPersonal("a", v.Profile, partial.Public, PersonalMutation{OperationID: "old-partial", Watched: &no, ProgressSeconds: &resume}, nil); e != nil {
				t.Fatal(e)
			}
			id := names["show"].Public
			if kind == "season" {
				id = names["season-001"].Public
			}
			before, e := s.Personal(v.Profile, partial.Public)
			if e != nil {
				t.Fatal(e)
			}
			revision := int64(0)
			for _, watched := range []bool{true, false} {
				state, e := s.SetContainerPersonal(ctx, p, kind, id, ContainerPersonalMutation{ExpectedRevision: revision, Watched: &watched}, scheduler, access)
				if e != nil {
					t.Fatal(e)
				}
				got, e := s.Personal(v.Profile, partial.Public)
				if e != nil || got.Watched != watched || got.ProgressSeconds != 0 || state.UndoToken == "" {
					t.Fatal(state, got, e)
				}
				restored, e := s.UndoContainerPersonal(ctx, p, kind, id, ContainerUndoMutation{ExpectedRevision: state.Revision, UndoToken: state.UndoToken}, access)
				if e != nil || restored.Revision != state.Revision+1 {
					t.Fatal(restored, e)
				}
				got, e = s.Personal(v.Profile, partial.Public)
				if e != nil || got.Watched != before.Watched || got.ProgressSeconds != before.ProgressSeconds {
					t.Fatal(got, before, e)
				}
				got, e = s.Personal(v.Profile, first.Public)
				if e != nil || !got.Watched {
					t.Fatal(got, e)
				}
				revision = restored.Revision
			}
		})
	}
}

func TestContainerUndoRejectsLaterStateAndOtherProfiles(t *testing.T) {
	s, scheduler, p, v, access, _, names := bulkFixture(t, 2)
	yes := true
	state, e := s.SetContainerPersonal(context.Background(), p, "show", names["show"].Public, ContainerPersonalMutation{Watched: &yes}, scheduler, access)
	if e != nil {
		t.Fatal(e)
	}
	in := ContainerUndoMutation{ExpectedRevision: state.Revision, UndoToken: state.UndoToken}
	other := p
	other.ProfileID = "other"
	if _, e = s.UndoContainerPersonal(context.Background(), other, "show", names["show"].Public, in, access); !errors.Is(e, ErrPersonalConflict) {
		t.Fatal(e)
	}
	item := names["ep000000"]
	personal, e := s.Personal(v.Profile, item.Public)
	if e != nil {
		t.Fatal(e)
	}
	no := false
	if _, e = s.SetPersonal("a", v.Profile, item.Public, PersonalMutation{OperationID: "later-item", ExpectedRevision: personal.Revision, Watched: &no}, nil); e != nil {
		t.Fatal(e)
	}
	if _, e = s.UndoContainerPersonal(context.Background(), p, "show", names["show"].Public, in, access); !errors.Is(e, ErrPersonalConflict) {
		t.Fatal(e)
	}
}

func TestContainerUndoExpiresAndSurvivesServiceRestart(t *testing.T) {
	s, scheduler, p, v, access, _, names := bulkFixture(t, 2)
	yes := true
	state, e := s.SetContainerPersonal(context.Background(), p, "show", names["show"].Public, ContainerPersonalMutation{Watched: &yes}, scheduler, access)
	if e != nil {
		t.Fatal(e)
	}
	s = New(s.db)
	if _, e = s.db.Exec(`UPDATE container_personal_undo SET expires_ms=0 WHERE profile_id=?`, v.Profile); e != nil {
		t.Fatal(e)
	}
	if _, e = s.UndoContainerPersonal(context.Background(), p, "show", names["show"].Public, ContainerUndoMutation{ExpectedRevision: state.Revision, UndoToken: state.UndoToken}, access); !errors.Is(e, ErrOperationExpired) {
		t.Fatal(e)
	}
}

func TestPersonalHistoryFiltersRestrictionsBeforePaging(t *testing.T) {
	db := homeFixture(t)
	s := New(db)
	hidden := homeItem(t, db, "m1")
	visible := homeItem(t, db, "m2")
	homeExec(t, db, `INSERT INTO personal_history(id,profile_id,item_id,started_at,updated_at,sequence,position,unit,completed) VALUES('hidden','p',?,'2026-10-01T00:00:00.000Z','2026-10-01T00:00:00.000Z',1,50000,0,0),('visible','p',?,'2026-09-01T00:00:00.000Z','2026-09-01T00:00:00.000Z',1,50000,0,0)`, hidden.ID, visible.ID)
	catalogtest.New(t, db).Attributes(hidden.ID, "label", "Adult")
	viewer := Viewer{Profile: "p", Libraries: []string{"movies"}, Restrictions: identity.ContentRestrictions{BlockedLabels: []string{"adult"}}}
	page, e := s.History("s", "f", "p", "", "all", "", viewer.Libraries, 1, viewer)
	if e != nil || len(page.Entries) != 1 || page.Entries[0].ID != "visible" || page.NextCursor != "" {
		t.Fatal(page, e)
	}
}

func TestPausedPlaybackDoesNotMovePersonalActivity(t *testing.T) {
	s, db, v := savedFixture(t)
	item := phase34SavedFixtureItem(t, db)
	key := identity.PersonalKey(v)
	homeExec(t, db, `INSERT INTO progress(profile_id,item_id,position,unit,playback_id) VALUES(?,?,0,0,'play')`, key, item.ID)
	accept(t, s, db, v, item.Public, 1, 40, 100, "playing")
	before, e := s.Personal(key, item.Public)
	if e != nil {
		t.Fatal(e)
	}
	var at string
	if e = db.QueryRow(`SELECT updated_at FROM progress_activity WHERE profile_id=? AND item_id=?`, key, item.ID).Scan(&at); e != nil {
		t.Fatal(e)
	}
	accept(t, s, db, v, item.Public, 2, 40, 100, "paused")
	after, e := s.Personal(key, item.Public)
	if e != nil || after.Revision != before.Revision || after.LastPlayedAt != before.LastPlayedAt {
		t.Fatal(after, before, e)
	}
	var later string
	if e = db.QueryRow(`SELECT updated_at FROM progress_activity WHERE profile_id=? AND item_id=?`, key, item.ID).Scan(&later); e != nil || later != at {
		t.Fatal(later, at, e)
	}
}

func TestNoncanonicalCompletionKeepsCountsAndWatchedCoherent(t *testing.T) {
	s, db, v := savedFixture(t)
	item := phase34SavedFixtureItem(t, db)
	key := identity.PersonalKey(v)
	homeExec(t, db, `INSERT INTO progress(profile_id,item_id,position,unit,playback_id) VALUES(?,?,40000,0,'newer')`, key, item.ID)
	tx, e := db.Begin()
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	if e = s.AcceptPlaybackProgress(tx, v, "older", item.Public, 1, 100, 100, "ended", false); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	got, e := s.Personal(key, item.Public)
	if e != nil || !got.Watched || got.ProgressSeconds != 0 {
		t.Fatal(got, e)
	}
	var plays int
	if e = db.QueryRow(`SELECT plays FROM personal_play_counts WHERE profile_id=? AND item_id=?`, key, item.ID).Scan(&plays); e != nil || plays != 1 {
		t.Fatal(plays, e)
	}
}

func TestHistoricalPlaybackUsesOccurrenceTime(t *testing.T) {
	s, db, v := savedFixture(t)
	item := phase34SavedFixtureItem(t, db)
	key := identity.PersonalKey(v)
	homeExec(t, db, `INSERT INTO progress(profile_id,item_id,position,unit,playback_id) VALUES(?,?,0,0,'offline')`, key, item.ID)
	at := time.Now().Add(-3 * 24 * time.Hour).UTC().Truncate(time.Millisecond)
	tx, e := db.Begin()
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	if e = s.AcceptPlaybackProgressAt(tx, v, "offline", item.Public, 1, 100, 100, "ended", true, at); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	var started string
	var ms int64
	if e = db.QueryRow(`SELECT started_at FROM personal_history WHERE id='offline'`).Scan(&started); e != nil || started != at.Format("2006-01-02T15:04:05.000Z") {
		t.Fatal(started, e)
	}
	if e = db.QueryRow(`SELECT started_ms FROM play_history WHERE playback_id='offline'`).Scan(&ms); e != nil || ms != at.UnixMilli() {
		t.Fatal(ms, e)
	}
}

func TestShowDetailKeepsDismissedExpiredAndUnmarkedAnchor(t *testing.T) {
	db := homeFixture(t)
	s := New(db)
	ep := homeItem(t, db, "e101")
	show := homeItem(t, db, "show")
	homeWatched(t, db, "p", "2026-01-01T00:00:00.000Z", "e101")
	homeExec(t, db, `INSERT INTO progress(profile_id,item_id,position,unit,playback_id) VALUES('p',?,0,0,'old')`, ep.ID)
	homeExec(t, db, `INSERT INTO progress_activity VALUES('p','tv',?,'2026-01-01T00:00:00.000Z','ended')`, ep.ID)
	homeExec(t, db, `INSERT INTO continue_show_dismissals(profile_id,show_id,item_id,playback_id,last_activity) VALUES('p',?,?,'old','2026-01-01T00:00:00.000Z')`, show.ID, ep.ID)
	viewer := Viewer{Profile: "p", Libraries: []string{"tv"}}
	request := ShowWorkspaceRequest{Viewer: viewer, Profile: "p", Library: "tv"}
	next, _, e := s.workspaceNextUp(request, show.Public)
	if e != nil || next == nil || next.ID != homeItem(t, db, "e102").Public {
		t.Fatal(next, e)
	}
	no := false
	personal, e := s.Personal("p", ep.Public)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.SetPersonal("a", "p", ep.Public, PersonalMutation{OperationID: "unmark-anchor", ExpectedRevision: personal.Revision, Watched: &no}, nil); e != nil {
		t.Fatal(e)
	}
	next, _, e = s.workspaceNextUp(request, show.Public)
	if e != nil || next == nil || next.ID != ep.Public {
		t.Fatal(next, e)
	}
}

func TestHomePublishesExplicitDestinations(t *testing.T) {
	db := homeFixture(t)
	s := New(db)
	row, e := s.HomeSingleRow(homeRequestFixture("movies"), "new_on_server", HomeRowPage{})
	if e != nil || row.SeeAll == nil || row.SeeAll.Type != "row" || row.SeeAll.RowID != "new_on_server" {
		t.Fatal(row.SeeAll, e)
	}
	row, e = s.HomeSingleRow(homeRequestFixture("movies"), "continue", HomeRowPage{})
	if e != nil || row.SeeAll == nil || row.SeeAll.Type != "row" || row.SeeAll.RowID != "continue" {
		t.Fatal(row.SeeAll, e)
	}
}

func TestUnfinishedFilmsStayAvailableWithoutDurationOrAge(t *testing.T) {
	db := homeFixture(t)
	s := New(db)
	item := homeItem(t, db, "m1")
	homeExec(t, db, `UPDATE catalog_assets SET duration=0 WHERE id IN(SELECT asset_id FROM catalog_asset_links WHERE entity_id=?)`, item.ID)
	homeExec(t, db, `INSERT INTO progress(profile_id,item_id,position,unit,playback_id) VALUES('p',?,60000,0,'old')`, item.ID)
	homeExec(t, db, `INSERT INTO progress_activity VALUES('p','movies',?,'2020-01-01T00:00:00.000Z','paused')`, item.ID)
	row, e := s.HomeSingleRow(homeRequestFixture("movies"), "continue", HomeRowPage{})
	if e != nil || len(row.Entries) != 1 || row.Entries[0].ID != item.Public {
		t.Fatal(row, e)
	}
	homeExec(t, db, `UPDATE catalog_assets SET available=0 WHERE id IN(SELECT asset_id FROM catalog_asset_links WHERE entity_id=?)`, item.ID)
	settleCompact(t, db)
	row, e = s.HomeSingleRow(homeRequestFixture("movies"), "continue", HomeRowPage{})
	if e != nil || len(row.Entries) != 0 {
		t.Fatal("missing film offered", row, e)
	}
}

func TestContinueUsesAnEpisodesOwnRange(t *testing.T) {
	db := homeFixture(t)
	s := New(db)
	item := homeItem(t, db, "e101")
	homeExec(t, db, `UPDATE catalog_asset_links SET start_seconds=100,end_seconds=200 WHERE entity_id=?`, item.ID)
	homeExec(t, db, `INSERT INTO progress(profile_id,item_id,position,unit,playback_id) VALUES('p',?,200000,0,'old')`, item.ID)
	homeExec(t, db, `INSERT INTO progress_activity VALUES('p','tv',?,'2026-09-01T00:00:00.000Z','paused')`, item.ID)
	row, e := s.HomeSingleRow(homeRequestFixture("tv"), "continue", HomeRowPage{})
	if e != nil || len(row.Entries) != 1 || row.Entries[0].ID != homeItem(t, db, "e102").Public {
		t.Fatal(row, e)
	}
}

func TestRewatchCountsOncePerOccurrenceAndReentersContinue(t *testing.T) {
	s, db, v := savedFixture(t)
	item := phase34SavedFixtureItem(t, db)
	key := identity.PersonalKey(v)
	homeExec(t, db, `INSERT INTO progress(profile_id,item_id,position,unit,playback_id) VALUES(?,?,0,0,'first')`, key, item.ID)
	report := func(id string, seq int64, pos float64, state string) {
		t.Helper()
		tx, e := db.Begin()
		if e != nil {
			t.Fatal(e)
		}
		defer tx.Rollback()
		if e = s.AcceptPlaybackProgress(tx, v, id, item.Public, seq, pos, 100, state, true); e != nil {
			t.Fatal(e)
		}
		if e = tx.Commit(); e != nil {
			t.Fatal(e)
		}
	}
	report("first", 1, 100, "ended")
	homeExec(t, db, `UPDATE progress SET playback_id='rewatch' WHERE profile_id=? AND item_id=?`, key, item.ID)
	report("rewatch", 1, 20, "playing")
	personal, e := s.Personal(key, item.Public)
	if e != nil || personal.Watched || personal.ProgressSeconds != 20 {
		t.Fatal(personal, e)
	}
	r := HomeRequest{Viewer: Viewer{Profile: key, Fence: "f", Libraries: []string{"library"}}, Profile: key, Libraries: []string{"library"}, ServerID: "s", ViewerFence: "f"}
	row, e := s.HomeSingleRow(r, "continue", HomeRowPage{})
	if e != nil || len(row.Entries) != 1 || row.Entries[0].ID != item.Public {
		t.Fatal(row, e)
	}
	report("rewatch", 2, 95, "playing")
	report("rewatch", 3, 100, "ended")
	var plays int
	if e = db.QueryRow(`SELECT plays FROM personal_play_counts WHERE profile_id=? AND item_id=?`, key, item.ID).Scan(&plays); e != nil || plays != 2 {
		t.Fatal(plays, e)
	}
	personal, e = s.Personal(key, item.Public)
	if e != nil || !personal.Watched || personal.ProgressSeconds != 0 {
		t.Fatal(personal, e)
	}
}

func TestOptOutKeepsOwnerHistoryOutOfSharedCounts(t *testing.T) {
	s, db, v := savedFixture(t)
	item := phase34SavedFixtureItem(t, db)
	key := identity.PersonalKey(v)
	storePreference(t, db, v, map[string]any{"privacy.showActivityToMembers": false})
	homeExec(t, db, `INSERT INTO progress(profile_id,item_id,position,unit,playback_id) VALUES(?,?,0,0,'play')`, key, item.ID)
	accept(t, s, db, v, item.Public, 1, 100, 100, "ended")
	var count int
	if e := db.QueryRow(`SELECT count(*) FROM play_history`).Scan(&count); e != nil || count != 1 {
		t.Fatal(count, e)
	}
	if e := db.QueryRow(`SELECT count(*) FROM play_counts`).Scan(&count); e != nil || count != 0 {
		t.Fatal(count, e)
	}
	if e := db.QueryRow(`SELECT plays FROM personal_play_counts WHERE profile_id=? AND item_id=?`, key, item.ID).Scan(&count); e != nil || count != 1 {
		t.Fatal(count, e)
	}
}

func TestOlderOfflineCompletionCannotReplaceNewerExplicitState(t *testing.T) {
	s, db, v := savedFixture(t)
	item := phase34SavedFixtureItem(t, db)
	key := identity.PersonalKey(v)
	no := false
	if _, e := s.SetPersonal("local:account", key, item.Public, PersonalMutation{OperationID: "newer-explicit", Watched: &no}, nil); e != nil {
		t.Fatal(e)
	}
	at := time.Now().Add(-24 * time.Hour)
	tx, e := db.Begin()
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	if e = s.AcceptPlaybackProgressAt(tx, v, "older-offline", item.Public, 1, 100, 100, "ended", true, at); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	personal, e := s.Personal(key, item.Public)
	if e != nil || personal.Watched || personal.ProgressSeconds != 0 {
		t.Fatal(personal, e)
	}
	var rows int
	if e = db.QueryRow(`SELECT count(*) FROM play_history WHERE playback_id='older-offline'`).Scan(&rows); e != nil || rows != 1 {
		t.Fatal(rows, e)
	}
}

func TestHistoryPauseAndClearDoNotLoseOrDuplicateCompletionCount(t *testing.T) {
	s, db, v := savedFixture(t)
	item := phase34SavedFixtureItem(t, db)
	key := identity.PersonalKey(v)
	homeExec(t, db, `INSERT INTO progress(profile_id,item_id,position,unit,playback_id) VALUES(?,?,0,0,'play')`, key, item.ID)
	storePreference(t, db, v, map[string]any{"privacy.pauseWatchHistory": true})
	accept(t, s, db, v, item.Public, 1, 95, 100, "playing")
	storePreference(t, db, v, map[string]any{"privacy.pauseWatchHistory": false})
	accept(t, s, db, v, item.Public, 2, 100, 100, "ended")
	count := func() {
		t.Helper()
		var plays int
		if e := db.QueryRow(`SELECT plays FROM personal_play_counts WHERE profile_id=? AND item_id=?`, key, item.ID).Scan(&plays); e != nil || plays != 1 {
			t.Fatal(plays, e)
		}
	}
	count()
	page, e := s.History("s", "f", key, "", "all", "", []string{"library"}, 40, Viewer{Profile: key, Libraries: []string{"library"}})
	if e != nil {
		t.Fatal(e)
	}
	if _, e := s.MutateActivity(key, ActivityMutation{OperationID: "clear-mid-occurrence", ExpectedRevision: page.Revision, Action: "clear-history"}, func(*sql.Tx) error { return nil }); e != nil {
		t.Fatal(e)
	}
	accept(t, s, db, v, item.Public, 3, 100, 100, "ended")
	count()
}

func TestSeasonMarksAndUndoTrainEffectiveRecommendationSignals(t *testing.T) {
	s, scheduler, p, v, access, _, names := bulkFixture(t, 2)
	yes := true
	state, e := s.SetContainerPersonal(context.Background(), p, "season", names["season-001"].Public, ContainerPersonalMutation{Watched: &yes}, scheduler, access)
	if e != nil {
		t.Fatal(e)
	}
	signal, e := compactcatalog.RecSignalFor(context.Background(), s.db, v.Profile, names["show"].ID, time.Now())
	if e != nil || !signal.Finished || !signal.Engaged || signal.Weight <= 0 {
		t.Fatal(signal, e)
	}
	if _, e = s.UndoContainerPersonal(context.Background(), p, "season", names["season-001"].Public, ContainerUndoMutation{ExpectedRevision: state.Revision, UndoToken: state.UndoToken}, access); e != nil {
		t.Fatal(e)
	}
	signal, e = compactcatalog.RecSignalFor(context.Background(), s.db, v.Profile, names["show"].ID, time.Now())
	if e != nil || signal.Finished || signal.Engaged {
		t.Fatal(signal, e)
	}
}

func TestExplicitResumeStartsContinueWithoutCreatingHistory(t *testing.T) {
	db := homeFixture(t)
	s := New(db)
	item := homeItem(t, db, "m1")
	no := false
	position := 60.
	if _, e := s.SetPersonal("a", "p", item.Public, PersonalMutation{OperationID: "explicit-resume", Watched: &no, ProgressSeconds: &position}, nil); e != nil {
		t.Fatal(e)
	}
	row, e := s.HomeSingleRow(homeRequestFixture("movies"), "continue", HomeRowPage{})
	if e != nil || len(row.Entries) != 1 || row.Entries[0].ID != item.Public {
		t.Fatal(row, e)
	}
	var count int
	if e = db.QueryRow(`SELECT count(*) FROM personal_history WHERE profile_id='p'`).Scan(&count); e != nil || count != 0 {
		t.Fatal(count, e)
	}
}

func TestUnknownDurationEvidenceKeepsTimelineBounds(t *testing.T) {
	s, db, v := savedFixture(t)
	item := phase34SavedFixtureItem(t, db)
	tx, e := db.Begin()
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	if e = s.AcceptPlaybackProgress(tx, v, "invalid", item.Public, 1, float64(int64(1)<<40), 0, "playing", true); !errors.Is(e, ErrJobRequest) {
		t.Fatal(e)
	}
	var rows int
	if e = tx.QueryRow(`SELECT count(*) FROM play_history`).Scan(&rows); e != nil || rows != 0 {
		t.Fatal(rows, e)
	}
}

func TestContainerMarksUnknownAddedMembersAndUndoPreservesIntent(t *testing.T) {
	s, scheduler, p, v, access, _, names := bulkFixture(t, 2)
	yes := true
	no := false
	item := names["ep000000"]
	if _, e := s.SetPersonal("a", v.Profile, item.Public, PersonalMutation{OperationID: "old-intent", Watched: &no}, nil); e != nil {
		t.Fatal(e)
	}
	var prior string
	if e := s.db.QueryRow(`SELECT authored_at FROM personal_watched_intents WHERE profile_id=? AND item_id=?`, v.Profile, item.ID).Scan(&prior); e != nil {
		t.Fatal(e)
	}
	homeExec(t, s.db, `UPDATE catalog_entities SET added_ms=NULL WHERE id=?`, item.ID)
	mark, e := s.SetContainerPersonal(context.Background(), p, "show", names["show"].Public, ContainerPersonalMutation{Watched: &yes}, scheduler, access)
	if e != nil {
		t.Fatal(e)
	}
	got, e := s.Personal(v.Profile, item.Public)
	if e != nil || !got.Watched {
		t.Fatal(got, e)
	}
	if _, e = s.UndoContainerPersonal(context.Background(), p, "show", names["show"].Public, ContainerUndoMutation{ExpectedRevision: mark.Revision, UndoToken: mark.UndoToken}, access); e != nil {
		t.Fatal(e)
	}
	var restored string
	if e = s.db.QueryRow(`SELECT authored_at FROM personal_watched_intents WHERE profile_id=? AND item_id=?`, v.Profile, item.ID).Scan(&restored); e != nil || restored != prior {
		t.Fatal(restored, prior, e)
	}
	got, e = s.Personal(v.Profile, item.Public)
	if e != nil || got.Watched {
		t.Fatal(got, e)
	}
}

func TestContainerUndoDeclinesChangedSeasonAssignment(t *testing.T) {
	s, scheduler, p, _, access, c, names := bulkFixture(t, 2)
	yes := true
	mark, e := s.SetContainerPersonal(context.Background(), p, "show", names["show"].Public, ContainerPersonalMutation{Watched: &yes}, scheduler, access)
	if e != nil {
		t.Fatal(e)
	}
	season := c.Season(names["show"], 2)
	homeExec(t, s.db, `UPDATE catalog_episodes SET season_id=? WHERE entity_id=?`, season.ID, names["ep000000"].ID)
	if _, e = s.UndoContainerPersonal(context.Background(), p, "show", names["show"].Public, ContainerUndoMutation{ExpectedRevision: mark.Revision, UndoToken: mark.UndoToken}, access); !errors.Is(e, ErrPersonalConflict) {
		t.Fatal(e)
	}
}
