package catalog

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"testing"
	"time"
)

func episodicDrain(t *testing.T, db *sql.DB) {
	t.Helper()
	worker := compactcatalog.NewWorker(db)
	for {
		n, err := worker.Step(context.Background(), 500)
		if err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			return
		}
	}
}

func TestEpisodeNamingAcceptance(t *testing.T) {
	cases := []struct {
		path, kind, title, numbering string
		season                       int
		numbers                      []int
		ambiguous                    bool
	}{
		{"Show/Season 2019/Show.S2019E1100.mkv", "tv", "Show", "seasonal", 2019, []int{1100}, false},
		{"News/Season 2024/News.2024.02.29.mkv", "tv", "News", "date", 2024, []int{60}, false},
		{"News.2023.02.29.mkv", "tv", "", "", 0, nil, true},
		{"Show/Season 1/Show.S01E02.1080p.mkv", "tv", "Show", "seasonal", 1, []int{2}, false},
		{"Show/Season 1/Show.1x02.mkv", "tv", "Show", "seasonal", 1, []int{2}, false},
		{"Show/Season 1/Show S01E01E02.mkv", "tv", "Show", "seasonal", 1, []int{1, 2}, false},
		{"Show/Season 1/Show S01E01-E03.mkv", "tv", "Show", "seasonal", 1, []int{1, 2, 3}, false},
		{"Show/Specials/Show S00E01.mkv", "tv", "Show", "seasonal", 0, []int{1}, false},
		{"Anime/[Group] Anime - 001 [1080p][ABCDEF12].mkv", "anime", "Anime", "absolute", -1, []int{1}, false},
		{"Show/Season 2/Show S01E01.mkv", "tv", "Show", "seasonal", 1, []int{1}, false},
		{"Show S01E01 S02E02.mkv", "tv", "", "", 0, nil, true},
		{"S01E01.mkv", "tv", "", "", 0, nil, true},
		{"Show (2024) 1080p.mkv", "tv", "", "", 0, nil, true},
		{"Show S01E01 Part 1.mkv", "tv", "Show", "seasonal", 1, []int{1}, false},
		{"News 2024-01-02.mkv", "tv", "News", "date", 2024, []int{2}, false},
		{"Show S01E01-E99.mkv", "tv", "", "", 0, nil, true},
		{"Show S01E02E01.mkv", "tv", "", "", 0, nil, true},
	}
	for _, c := range cases {
		t.Run(c.path, func(t *testing.T) {
			got := ParseEpisode(c.path, c.kind)
			if (got.Issue != "") != c.ambiguous {
				t.Fatalf("unexpected ambiguity %+v", got)
			}
			if c.ambiguous {
				return
			}
			if got.ShowTitle != c.title || got.Numbering != c.numbering || got.Season != c.season || len(got.Numbers) != len(c.numbers) {
				t.Fatalf("unexpected parse %+v", got)
			}
			for i, n := range c.numbers {
				if got.Numbers[i] != n {
					t.Fatalf("numbers %+v", got)
				}
			}
		})
	}
}

func TestEpisodeStackSuffixAcceptance(t *testing.T) {
	cases := []struct {
		path    string
		kind    string
		season  int
		numbers []int
		issue   string
	}{
		{"Cowboy Bebop/Season 01/Cowboy Bebop - S01E12 - Jupiter Jazz (Part 1).mkv", "tv", 1, []int{12}, ""},
		{"Show/Season 01/Show - S01E05 - The Part 2 Problem.mkv", "tv", 1, []int{5}, ""},
		{"Show/Season 01/Show - S01E05 - pt1.mkv", "tv", 1, []int{5}, ""},
		{"Show/Season 01/Show - S01E05 - pt2.mkv", "tv", 1, []int{5}, ""},
		{"Show/Season 01/Show.S01E05.part2.mkv", "tv", 1, []int{5}, ""},
		{"Show/Season 01/Show S01E05 cd1.mkv", "tv", 1, []int{5}, ""},
		{"Show/Season 01/Show - S01E05 - Title - Disc 2.mkv", "tv", 1, []int{5}, ""},
	}
	for _, c := range cases {
		t.Run(c.path, func(t *testing.T) {
			got := ParseEpisode(c.path, c.kind)
			if got.Issue != c.issue {
				t.Fatalf("issue = %q, want %q (parse %+v)", got.Issue, c.issue, got)
			}
			if c.issue != "" {
				return
			}
			if got.Season != c.season || len(got.Numbers) != len(c.numbers) {
				t.Fatalf("unexpected parse %+v", got)
			}
			for i, n := range c.numbers {
				if got.Numbers[i] != n {
					t.Fatalf("numbers %+v", got)
				}
			}
		})
	}
}

func TestEpisodeHierarchySharedAssetsAndManualCorrections(t *testing.T) {
	root := t.TempDir()
	db, err := persistence.Open(filepath.Join(root, "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := New(db)
	sourceRoot := filepath.Join(root, "tv")
	if err = os.Mkdir(sourceRoot, 0700); err != nil {
		t.Fatal(err)
	}
	lib, err := s.Create("TV", "tv", sourceRoot)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO jobs(id,library_id,status,created_at) VALUES('tvjob',?,'running','now')`, lib.ID); err != nil {
		t.Fatal(err)
	}
	commit := func(relative string) {
		t.Helper()
		path := filepath.Join(sourceRoot, relative)
		if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(path, []byte("shared-fixture"), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec(`INSERT OR REPLACE INTO scan_queue(job_id,path,kind) VALUES('tvjob',?,'file')`, path); err != nil {
			t.Fatal(err)
		}
		if err = s.CommitMedia(context.Background(), "tvjob", lib.ID, path, assets.Facts{Container: "mp4", VideoCodec: "h264", AudioCodec: "aac", Duration: 120}); err != nil {
			t.Fatal(err)
		}
	}
	commit("Show/Season 1/Show S01E01E02.mp4")
	episodicDrain(t, db)
	shows, _, err := s.Shows(Viewer{Libraries: []string{lib.ID}}, lib.ID, "", 10)
	if err != nil || len(shows) != 1 || shows[0].LibraryKind != "tv" || shows[0].ProviderMatchStatus != "unmatched" {
		t.Fatal(shows, err)
	}
	seasons, err := s.Seasons(Viewer{Libraries: []string{lib.ID}}, shows[0].ID)
	if err != nil || len(seasons) != 1 || seasons[0].Number != 1 {
		t.Fatal(seasons, err)
	}
	episodes, _, err := s.Episodes(Viewer{Libraries: []string{lib.ID}}, "", seasons[0].ID, "", 10)
	if err != nil || len(episodes) != 2 {
		t.Fatal(episodes, err)
	}
	projection, err := s.Content(ContentRequest{Viewer: Viewer{Fence: "f", Libraries: []string{lib.ID}}, Library: lib.ID, ViewerFence: "f", View: "show", EntityID: shows[0].ID, Limit: 1})
	if err != nil || projection.Heading.Fallback != "Show" || len(projection.Sections) != 1 || projection.Sections[0].Entries[0].Navigation.EntityID != seasons[0].ID {
		t.Fatal("show content projection", projection, err)
	}
	page, err := s.Content(ContentRequest{Viewer: Viewer{Fence: "f", Libraries: []string{lib.ID}}, Library: lib.ID, ViewerFence: "f", View: "season", EntityID: seasons[0].ID, Limit: 1})
	if err != nil || page.Sections[0].TotalCount != 2 {
		t.Fatal(page, err)
	}
	next, err := s.Content(ContentRequest{Viewer: Viewer{Fence: "f", Libraries: []string{lib.ID}}, Library: lib.ID, ViewerFence: "f", View: "season", EntityID: seasons[0].ID, Limit: 1, Cursor: page.Sections[0].NextCursor})
	if err != nil || len(next.Sections[0].Entries) != 1 || next.Sections[0].Entries[0].ID == page.Sections[0].Entries[0].ID {
		t.Fatal("episode continuation", next, err)
	}
	first, second := episodes[0], episodes[1]
	if first.Kind != "episode" || first.Sources[0].ID != second.Sources[0].ID || first.Episode.SourceBoundary != "unknown_multi_episode" || first.Episode.LocalIdentityStatus != "parsed" {
		t.Fatal(episodes)
	}
	commit("Show/Season 1/Show S01E01E02.mp4")
	episodicDrain(t, db)
	again, _, err := s.Episodes(Viewer{Libraries: []string{lib.ID}}, "", seasons[0].ID, "", 10)
	if err != nil || len(again) != 2 || again[0].ID != first.ID {
		t.Fatal("rescan changed identity", again, err)
	}
	commit("Show/Season 1/Show S01E01 alternate.mp4")
	episodicDrain(t, db)
	first, err = s.Get("", first.ID)
	if err != nil || len(first.Sources) != 0 || len(fileInfoSourcesForTest(t, s, "", first.ID)) != 2 {
		t.Fatal("alternate source not linked", first, err)
	}
	if err = s.RemoveTitle(context.Background(), second.ID); err != nil {
		t.Fatal(err)
	}
	episodicDrain(t, db)
	if _, err = os.Stat(filepath.Join(sourceRoot, "Show/Season 1/Show S01E01E02.mp4")); err != nil {
		t.Fatal("shared bytes deleted", err)
	}
	first, err = s.Get("", first.ID)
	if err != nil || len(first.Sources) != 0 || len(fileInfoSourcesForTest(t, s, "", first.ID)) != 2 || first.Episode.SourceBoundary != "unknown_multi_episode" {
		t.Fatal(first, err)
	}
	commit("Show/Season 2/Show S01E03 S02E04.mp4")
	episodicDrain(t, db)
	issues, _, err := s.EpisodeIssues(lib.ID, "", 10)
	if err != nil || len(issues) != 1 || issues[0].Code != "conflicting_episode_markers" {
		t.Fatal(issues, err)
	}
	season := 1
	if err = s.AssignEpisodes(context.Background(), lib.ID, EpisodeAssignment{AssetID: issues[0].AssetID, ShowTitle: "Show", Numbering: "seasonal", SeasonNumber: &season, EpisodeNumbers: []int{3}}); err != nil {
		t.Fatal(err)
	}
	commit("Show/Season 2/Show S01E03 S02E04.mp4")
	episodicDrain(t, db)
	issues, _, err = s.EpisodeIssues(lib.ID, "", 10)
	if err != nil || len(issues) != 0 {
		t.Fatal("manual assignment did not survive rescan", issues, err)
	}
	episodes, _, err = s.Episodes(Viewer{Libraries: []string{lib.ID}}, "", seasons[0].ID, "", 10)
	if err != nil || len(episodes) != 2 || episodes[1].Episode.Number != 3 || episodes[1].Episode.LocalIdentityStatus != "manual" {
		t.Fatal(episodes, err)
	}
	var jobs int
	if err = db.QueryRow(`SELECT count(*) FROM metadata_jobs`).Scan(&jobs); err != nil || jobs != 0 {
		t.Fatal("episode queued for movie provider", jobs, err)
	}
}
func TestAbsoluteAnimeRemainsUnassignedUntilOwnerCorrection(t *testing.T) {
	root := t.TempDir()
	db, err := persistence.Open(filepath.Join(root, "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := New(db)
	lib, err := s.Create("Anime", "anime", root)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "[Group] Anime - 001 [1080p].mp4")
	if err = os.WriteFile(path, []byte("media"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO jobs VALUES('animejob',?,'running',0,'','now');INSERT INTO scan_queue(job_id,path,kind) VALUES('animejob',?,'file')`, lib.ID, path); err != nil {
		t.Fatal(err)
	}
	facts := assets.Facts{Container: "mp4", VideoCodec: "h264", Duration: 60}
	if err = s.CommitMedia(context.Background(), "animejob", lib.ID, path, facts); err != nil {
		t.Fatal(err)
	}
	episodicDrain(t, db)
	shows, _, _ := s.Shows(Viewer{Libraries: []string{lib.ID}}, lib.ID, "", 10)
	seasons, err := s.Seasons(Viewer{Libraries: []string{lib.ID}}, shows[0].ID)
	if err != nil || len(seasons) != 0 {
		t.Fatal("guessed a season", seasons, err)
	}
	episodes, _, err := s.Episodes(Viewer{Libraries: []string{lib.ID}}, shows[0].ID, "", "", 10)
	if err != nil || len(episodes) != 1 || episodes[0].Episode.SeasonNumber != nil || episodes[0].Episode.Numbering != "absolute" {
		t.Fatal(episodes, err)
	}
	originalID, assetToken := episodes[0].ID, episodes[0].Sources[0].ID
	if _, err = db.Exec(`INSERT INTO personal_items(profile_id,item_id,favorite,revision) SELECT 'p',id,1,1 FROM catalog_entities WHERE public_id=pid_blob(?)`, originalID); err != nil {
		t.Fatal(err)
	}
	season := 2
	if err = s.AssignEpisodes(context.Background(), lib.ID, EpisodeAssignment{AssetID: assetToken, ShowTitle: "Anime", Numbering: "seasonal", SeasonNumber: &season, EpisodeNumbers: []int{4}}); err != nil {
		t.Fatal(err)
	}
	if err = s.CommitMedia(context.Background(), "animejob", lib.ID, path, facts); err != nil {
		t.Fatal(err)
	}
	episodicDrain(t, db)
	unassigned, _, err := s.Episodes(Viewer{Libraries: []string{lib.ID}}, shows[0].ID, "", "", 10)
	if err != nil || len(unassigned) != 0 {
		t.Fatal("old absolute interpretation still browsable", unassigned, err)
	}
	seasons, err = s.Seasons(Viewer{Libraries: []string{lib.ID}}, shows[0].ID)
	if err != nil || len(seasons) != 1 || seasons[0].Number != 2 {
		t.Fatal(seasons, err)
	}
	mapped, _, err := s.Episodes(Viewer{Libraries: []string{lib.ID}}, "", seasons[0].ID, "", 10)
	if err != nil || len(mapped) != 1 || mapped[0].Episode.Number != 4 || mapped[0].Episode.ProviderMatchStatus != "unmatched" {
		t.Fatal(mapped, err)
	}
	var retired, favorite bool
	if err = db.QueryRow(`SELECT e.retired,p.favorite FROM catalog_entities e JOIN personal_items p ON p.item_id=e.id AND p.profile_id='p' WHERE e.public_id=pid_blob(?)`, originalID).Scan(&retired, &favorite); err != nil || !retired || !favorite {
		t.Fatal("superseded identity or personal intent was lost", retired, favorite, err)
	}
	// Returning to that interpretation revives the same logical identity and
	// file. Reassignment must not manufacture another episode or lose history.
	if err = s.AssignEpisodes(context.Background(), lib.ID, EpisodeAssignment{AssetID: assetToken, ShowTitle: "Anime", Numbering: "absolute", EpisodeNumbers: []int{1}}); err != nil {
		t.Fatal(err)
	}
	episodicDrain(t, db)
	unassigned, _, err = s.Episodes(Viewer{Profile: "p", Libraries: []string{lib.ID}}, shows[0].ID, "", "", 10)
	if err != nil || len(unassigned) != 1 || unassigned[0].ID != originalID || len(unassigned[0].Sources) != 1 || unassigned[0].Sources[0].ID != assetToken {
		t.Fatal("earlier interpretation did not retain its identity and source", unassigned, err)
	}
	if err = db.QueryRow(`SELECT p.favorite FROM personal_items p JOIN catalog_entities e ON e.id=p.item_id WHERE e.public_id=pid_blob(?) AND p.profile_id='p'`, originalID).Scan(&favorite); err != nil || !favorite {
		t.Fatal("returning to an interpretation lost personal intent", favorite, err)
	}
}

func TestEpisodeReassignmentKeepsAnInterpretationWithAnotherSource(t *testing.T) {
	c := catalogtest.Open(t)
	lib := c.Library("anime", "Anime", "anime", "/anime")
	show := c.Show(lib, "Anime", 0)
	var first, second string
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		for _, path := range []string{"/anime/one.mkv", "/anime/two.mkv"} {
			_, token, err := compactcatalog.UpsertAssetTx(ctx, tx, compactcatalog.Asset{Path: path, Container: "mkv", VideoCodec: "h264", Duration: 60})
			if err != nil {
				return err
			}
			if first == "" {
				first = token
			} else {
				second = token
			}
			if err = New(c.DB).commitEpisodes(tx, "anime", token, path, EpisodeNaming{ShowTitle: "Anime", Season: -1, Numbering: "absolute", Numbers: []int{1}}, false); err != nil {
				return err
			}
		}
		return nil
	})
	c.Drain()
	s := New(c.DB)
	season := 2
	if err := s.AssignEpisodes(context.Background(), "anime", EpisodeAssignment{AssetID: first, ShowTitle: "Anime", Numbering: "seasonal", SeasonNumber: &season, EpisodeNumbers: []int{4}}); err != nil {
		t.Fatal(err)
	}
	c.Drain()
	items, _, err := s.Episodes(Viewer{Libraries: []string{"anime"}}, show.Public, "", "", 10)
	if err != nil || len(items) != 1 || len(items[0].Sources) != 1 || items[0].Sources[0].ID != second || items[0].Episode.Numbering != "absolute" {
		t.Fatal("another source's interpretation was retired", items, err)
	}
}

func commitEpisodeFile(t *testing.T, s *Service, db *sql.DB, job, root, relative string, libraryID string) {
	t.Helper()
	path := filepath.Join(root, relative)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("daily episode fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT OR REPLACE INTO scan_queue(job_id,path,kind) VALUES(?,?,'file')`, job, path); err != nil {
		t.Fatal(err)
	}
	if err := s.CommitMedia(context.Background(), job, libraryID, path, assets.Facts{Container: "mkv", VideoCodec: "h264", AudioCodec: "aac", Duration: 60}); err != nil {
		t.Fatal(err)
	}
}

func episodicScanFixture(t *testing.T, name string) (*Service, *sql.DB, string, string, string) {
	t.Helper()
	root := t.TempDir()
	db, err := persistence.Open(filepath.Join(root, "catalog.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	s := New(db)
	sourceRoot := filepath.Join(root, "media")
	if err := os.Mkdir(sourceRoot, 0700); err != nil {
		t.Fatal(err)
	}
	lib, err := s.Create(name, "tv", sourceRoot)
	if err != nil {
		t.Fatal(err)
	}
	job := "episodic-test-job"
	if _, err := db.Exec(`INSERT INTO jobs(id,library_id,status,created_at) VALUES(?,?,'running','now')`, job, lib.ID); err != nil {
		t.Fatal(err)
	}
	return s, db, sourceRoot, lib.ID, job
}

func TestYearSeasonDailyAirDatesAndSameDateVersions(t *testing.T) {
	s, db, root, library, job := episodicScanFixture(t, "Daily")
	for _, relative := range []string{
		"Show/Season 2019/Show - S2019E05.mkv",
		"Daily Show/Daily Show 2023.05.14.mkv",
		"Daily Show/Daily Show 2023-05-15.mkv",
		"Daily Show/Daily Show 2023.05.14 Part A.mkv",
		"Daily Show/Daily Show 2023.05.14 Guest Name.mkv",
	} {
		commitEpisodeFile(t, s, db, job, root, relative, library)
	}
	episodicDrain(t, db)
	rows, err := db.Query(`SELECT strftime('%Y-%m-%d',ep.air_date*86400,'unixepoch') FROM catalog_episodes ep JOIN catalog_entities show ON show.id=ep.show_id WHERE show.title='Daily Show' AND ep.numbering='date' ORDER BY ep.air_date`)
	if err != nil {
		t.Fatal(err)
	}
	dates := []string{}
	for rows.Next() {
		var date string
		if err = rows.Scan(&date); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		dates = append(dates, date)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		t.Fatal(err)
	}
	rows.Close()
	if len(dates) != 2 || dates[0] != "2023-05-14" || dates[1] != "2023-05-15" {
		t.Fatalf("daily episodes %+v", dates)
	}
	day, err := time.Parse("2006-01-02", "2023-05-14")
	if err != nil {
		t.Fatal(err)
	}
	var itemID int64
	var public string
	if err = db.QueryRow(`SELECT e.id,pid(e.public_id) FROM catalog_episodes ep JOIN catalog_entities show ON show.id=ep.show_id JOIN catalog_entities e ON e.id=ep.entity_id WHERE show.title='Daily Show' AND ep.numbering='date' AND ep.air_date=?`, day.Unix()/86400).Scan(&itemID, &public); err != nil {
		t.Fatal(err)
	}
	var versions int
	if err = db.QueryRow(`SELECT count(*) FROM catalog_asset_links WHERE entity_id=?`, itemID).Scan(&versions); err != nil || versions != 3 {
		t.Fatalf("same-date versions=%d err=%v", versions, err)
	}
	var seasonNumber int
	if err = db.QueryRow(`SELECT number FROM catalog_episodes WHERE numbering='seasonal' AND number=5`).Scan(&seasonNumber); err != nil || seasonNumber != 5 {
		t.Fatalf("year season episode=%d err=%v", seasonNumber, err)
	}
	info, err := s.episodeInfo(public)
	if err != nil || info.AirDate != "2023-05-14" || info.OrderingBasis != "date" || info.SeasonNumber == nil || *info.SeasonNumber != 2023 {
		t.Fatal(info, err)
	}
}

func TestDailyAndYearSeasonEpisodesPersistAndRescan(t *testing.T) {
	s, db, root, library, job := episodicScanFixture(t, "Daily")
	paths := []string{"News/Season 2024/News.2024.02.29.mkv", "Drama/Season 2019/Drama.S2019E1100.mkv"}
	for _, relative := range paths {
		commitEpisodeFile(t, s, db, job, root, relative, library)
	}
	commitEpisodeFile(t, s, db, job, root, paths[0], library)
	episodicDrain(t, db)
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM catalog_episodes`).Scan(&count); err != nil || count != 2 {
		t.Fatal(count, err)
	}
	var public string
	if err := db.QueryRow(`SELECT pid(e.public_id) FROM catalog_episodes ep JOIN catalog_entities e ON e.id=ep.entity_id WHERE ep.numbering='date'`).Scan(&public); err != nil {
		t.Fatal(err)
	}
	info, err := s.episodeInfo(public)
	if err != nil || info.AirDate != "2024-02-29" || info.SeasonNumber == nil || *info.SeasonNumber != 2024 || info.OrderingBasis != "date" {
		t.Fatal(info, err)
	}
	if err := db.QueryRow(`SELECT number FROM catalog_episodes WHERE numbering='seasonal'`).Scan(&count); err != nil || count != 1100 {
		t.Fatal(count, err)
	}
}

func TestFrameSizeAndCodecTagsAreNotEpisodeMarkers(t *testing.T) {
	cases := []struct {
		path, kind, title, numbering string
		season                       int
		numbers                      []int
	}{
		{"[Group] Show - 05 [1920x1080].mkv", "anime", "Show", "absolute", -1, []int{5}},
		{"Show - S01E01 - 1920x1080.mkv", "tv", "Show", "seasonal", 1, []int{1}},
		{"Show.S01E02.720x480.mkv", "tv", "Show", "seasonal", 1, []int{2}},
		{"Show.S01E01.1080p.DD5.1x264-GRP.mkv", "tv", "Show", "seasonal", 1, []int{1}},
		{"Show.S01E01.1080p.DDP5.1x265-GRP.mkv", "tv", "Show", "seasonal", 1, []int{1}},
		// A real season-by-episode marker still reads, including long-running numbers.
		{"Show.1x264.mkv", "tv", "Show", "seasonal", 1, []int{264}},
		{"Show.12x103.mkv", "tv", "Show", "seasonal", 12, []int{103}},
	}
	for _, c := range cases {
		t.Run(c.path, func(t *testing.T) {
			got := ParseEpisode(c.path, c.kind)
			if got.Issue != "" || got.ShowTitle != c.title || got.Numbering != c.numbering || got.Season != c.season || len(got.Numbers) != len(c.numbers) || got.Numbers[0] != c.numbers[0] {
				t.Fatalf("unexpected parse %+v", got)
			}
		})
	}
	if got := ParseEpisode("Show [1920x1080].mkv", "tv"); got.Issue != "unrecognized_episode_name" {
		t.Fatalf("a frame size alone was filed as an episode: %+v", got)
	}
}

func TestFrameSizeTagsInAnyCaseAndPositionLeaveTheEpisodeReadable(t *testing.T) {
	cases := []struct {
		path, kind, title string
		year, season      int
		numbers           []int
	}{
		// The tag's letter case does not matter.
		{"Show.S01E02.1920X1080.mkv", "tv", "Show", 0, 1, []int{2}},
		{"Show.S01E02.DD5.1X264.mkv", "tv", "Show", 0, 1, []int{2}},
		// A frame size before the marker is not part of the series name or its year.
		{"Show - 1920x1080 - S01E02.mkv", "tv", "Show", 0, 1, []int{2}},
		{"Show/Season 1/Show - 1920x1080 - S01E02.mkv", "tv", "Show", 0, 1, []int{2}},
		{"Show (2010) 1280x720 - S01E02.mkv", "tv", "Show", 2010, 1, []int{2}},
		{"Show.2048x1080.2024.02.29.mkv", "tv", "Show", 0, 2024, []int{60}},
		// Cropped and portrait frames are frames too.
		{"Show.S01E02.1920x804.mkv", "tv", "Show", 0, 1, []int{2}},
		{"Show.S01E02.1080x1920.mkv", "tv", "Show", 0, 1, []int{2}},
		// A real marker beside a frame size.
		{"Show.1x02.720p.mkv", "tv", "Show", 0, 1, []int{2}},
		{"Show 4x12 1280x720.mkv", "tv", "Show", 0, 4, []int{12}},
		// Long season and episode numbers written with an x are still a marker:
		// only a pair that can be a picture size is read as one.
		{"Show.101x102.mkv", "tv", "Show", 0, 101, []int{102}},
		{"Show.2019x100.mkv", "tv", "Show", 0, 2019, []int{100}},
	}
	for _, c := range cases {
		t.Run(c.path, func(t *testing.T) {
			got := ParseEpisode(c.path, c.kind)
			if got.Issue != "" || got.ShowTitle != c.title || got.Year != c.year || got.Season != c.season || len(got.Numbers) != len(c.numbers) || got.Numbers[0] != c.numbers[0] {
				t.Fatalf("unexpected parse %+v", got)
			}
		})
	}
	// A common picture width followed by a long number is read as a picture
	// size, never as a season in the hundreds: "720x101" is not season 720.
	// A season that large is written with S and E.
	if got := ParseEpisode("Show.720x101.mkv", "tv"); got.Season == 720 || got.Issue == "" {
		t.Fatalf("a picture width was read as a season: %+v", got)
	}
	if got := ParseEpisode("Show.S720E101.mkv", "tv"); got.Issue != "" || got.Season != 720 || len(got.Numbers) != 1 || got.Numbers[0] != 101 {
		t.Fatalf("an explicit long season was not read: %+v", got)
	}
}
