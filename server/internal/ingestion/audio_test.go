package ingestion

import (
	"context"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/decodertest"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/localmetadata"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestMain(m *testing.M) {
	if len(os.Args) == 2 && os.Args[1] == "--portico-storage-helper" {
		if err := storage.Helper(os.Stdin, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}
func TestRealLocalAudioScanChaptersArtAndBookResume(t *testing.T) {
	ffmpeg := decodertest.QualifiedFFmpeg(t)
	ffprobe := decodertest.QualifiedFFprobe(t)
	root := t.TempDir()
	music := filepath.Join(root, "music", "Compilation")
	books := filepath.Join(root, "books", "Book")
	for _, path := range []string{music, books} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command(ffmpeg, append([]string{"-nostdin", "-y", "-v", "error"}, args...)...)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("synthetic fixture: %v %s", err, output)
		}
	}
	cover := filepath.Join(root, "cover.png")
	file, err := os.Create(cover)
	if err != nil {
		t.Fatal(err)
	}
	picture := image.NewRGBA(image.Rect(0, 0, 32, 32))
	for y := 0; y < 32; y++ {
		for x := 0; x < 32; x++ {
			picture.Set(x, y, color.RGBA{20, 90, 150, 255})
		}
	}
	if err = png.Encode(file, picture); err != nil {
		t.Fatal(err)
	}
	file.Close()
	run("-f", "lavfi", "-i", "sine=frequency=440:duration=8", "-i", cover, "-map", "0:a", "-map", "1:v", "-c:a", "aac", "-c:v", "copy", "-disposition:v:0", "attached_pic", "-metadata", "title=First Tone", "-metadata", "album=Legal Compilation", "-metadata", "artist=First Artist", "-metadata", "album_artist=Various Artists", "-metadata", "track=1/2", "-metadata", "disc=2/2", filepath.Join(music, "first.m4a"))
	run("-f", "lavfi", "-i", "sine=frequency=660:duration=2", "-c:a", "flac", "-metadata", "title=Second Tone", "-metadata", "album=Legal Compilation", "-metadata", "artist=Second Artist", "-metadata", "album_artist=Various Artists", "-metadata", "track=2/2", "-metadata", "disc=2/2", "-metadata", "MUSICBRAINZ_TRACKID=11111111-1111-1111-1111-111111111111", "-metadata", "MUSICBRAINZ_ALBUMID=22222222-2222-2222-2222-222222222222", "-metadata", "MUSICBRAINZ_RELEASETRACKID=44444444-4444-4444-4444-444444444444", filepath.Join(music, "second.flac"))
	chapterData := ";FFMETADATA1\ntitle=Part One\nalbum=Tagged Book\nartist=Tagged Author\ntrack=1/2\n[CHAPTER]\nTIMEBASE=1/1000\nSTART=0\nEND=2000\ntitle=Opening\n[CHAPTER]\nTIMEBASE=1/1000\nSTART=2000\nEND=4000\ntitle=Closing\n"
	chapterPath := filepath.Join(root, "chapters.txt")
	if err = os.WriteFile(chapterPath, []byte(chapterData), 0600); err != nil {
		t.Fatal(err)
	}
	run("-f", "lavfi", "-i", "sine=frequency=220:duration=4", "-f", "ffmetadata", "-i", chapterPath, "-map", "0:a", "-map_metadata", "1", "-map_chapters", "1", "-c:a", "aac", filepath.Join(books, "part1.m4b"))
	run("-f", "lavfi", "-i", "sine=frequency=330:duration=4", "-c:a", "libmp3lame", "-metadata", "title=Part Two", "-metadata", "album=Tagged Book", "-metadata", "artist=Tagged Author", "-metadata", "track=2/2", filepath.Join(books, "part2.mp3"))
	if err = os.WriteFile(filepath.Join(books, "metadata.opf"), []byte(`<package xmlns:dc="http://purl.org/dc/elements/1.1/"><metadata><dc:title>Synthetic Book</dc:title><dc:creator role="aut">Local Author</dc:creator><dc:creator role="nrt">Local Narrator</dc:creator></metadata></package>`), 0600); err != nil {
		t.Fatal(err)
	}
	rawCover, _ := os.ReadFile(cover)
	if err = os.WriteFile(filepath.Join(books, "cover.png"), rawCover, 0600); err != nil {
		t.Fatal(err)
	}
	db, err := persistence.Open(filepath.Join(root, "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	helper, _ := os.Executable()
	store := storage.New(helper)
	cat := catalog.New(db)
	cat.SetStorage(store)
	meta, err := localmetadata.New(filepath.Join(root, "art"), ffmpeg, store)
	if err != nil {
		t.Fatal(err)
	}
	scanner := New(db, cat, assets.Probe{Binary: ffprobe, Supervisor: store.Supervisor})
	scanner.SetStorage(store)
	scanner.LocalMetadata = meta
	scan := func(name, kind, path string) catalog.Library {
		t.Helper()
		lib, err := cat.Create(name, kind, path)
		if err != nil {
			t.Fatal(err)
		}
		job, err := scanner.Queue(lib.ID)
		if err != nil {
			t.Fatal(err)
		}
		for n := 0; n < 20; n++ {
			scanner.process(context.Background(), job.ID, lib.ID)
			// The derived worker runs beside ingestion in production; analysis
			// waits for its pending catalogue information changes to publish.
			drainDerivedCatalogue(t, db)
			state, err := scanner.Get(job.ID)
			if err != nil {
				t.Fatal(err)
			}
			if state.Status == "complete" || state.Status == "complete_with_warnings" {
				// Every non-file-list Music scan adds loudness and Sonic
				// (musicLoudnessPolicy). This scanner has no deep-analysis
				// worker, so each music file must warn exactly that and
				// nothing else; any other library must finish clean.
				rows, err := db.Query(`SELECT relative_path,reason FROM inventory_scan_issues WHERE job_id=? ORDER BY relative_path`, job.ID)
				if err != nil {
					t.Fatal(err)
				}
				var issues []string
				for rows.Next() {
					var path, reason string
					if err = rows.Scan(&path, &reason); err != nil {
						t.Fatal(err)
					}
					issues = append(issues, path+": "+reason)
				}
				if err = rows.Close(); err != nil {
					t.Fatal(err)
				}
				var want []string
				if kind == "music" {
					want = []string{"Compilation/first.m4a: analysis_worker_unavailable", "Compilation/second.flac: analysis_worker_unavailable"}
				}
				if fmt.Sprint(issues) != fmt.Sprint(want) || (len(want) == 0) != (state.Status == "complete") {
					t.Fatal(name, state.Status, issues)
				}
				catalogtest.New(t, db).Drain()
				return lib
			}
			if state.Status != "running" && state.Status != "queued" {
				t.Fatal(state)
			}
		}
		t.Fatal("audio scan did not finish")
		return lib
	}
	musicLib := scan("Music", "music", filepath.Join(root, "music"))
	bookLib := scan("Books", "audiobook", filepath.Join(root, "books"))
	artists, _, err := cat.Artists(catalog.Viewer{Libraries: []string{musicLib.ID}}, musicLib.ID, "", 20)
	if err != nil || len(artists) != 3 {
		t.Fatal(artists, err)
	}
	var compilation string
	for _, artist := range artists {
		if artist.Name == "Various Artists" {
			compilation = artist.ID
		}
	}
	albums, _, err := cat.Albums(catalog.Viewer{Libraries: []string{musicLib.ID}}, compilation, "", 10)
	if err != nil || len(albums) != 1 || albums[0].PosterURL == "" {
		t.Fatal(albums, err)
	}
	songs, _, err := cat.AudioItems(catalog.Viewer{Profile: "profile", Libraries: []string{musicLib.ID}}, albums[0].ID, "album", "", 10)
	if err != nil || len(songs) != 2 {
		t.Fatal(songs, err)
	}
	if songs[0].Song == nil || songs[0].Song.TrackNumber == nil || songs[0].Song.DiscNumber == nil || *songs[0].Song.TrackNumber != 1 || *songs[0].Song.DiscNumber != 2 || songs[0].Song.Artist != "First Artist" || len(songs[0].Sources) != 0 {
		t.Fatal(songs[0])
	}
	// AudioItems publishes a constant-size summary, without source-detail
	// arrays. Check the actual ingested asset facts: an attached cover must
	// not turn this song into a video source.
	var sources, videoSources int
	if err = db.QueryRow(`SELECT count(*),coalesce(sum(a.video_codec<>''),0)
 FROM catalog_entities e JOIN catalog_asset_links l ON l.entity_id=e.id
 JOIN catalog_assets a ON a.id=l.asset_id WHERE e.public_id=pid_blob(?)`, songs[0].ID).Scan(&sources, &videoSources); err != nil {
		t.Fatal(err)
	}
	if sources != 1 || videoSources != 0 {
		t.Fatal("attached artwork changed the audio asset facts", sources, videoSources)
	}
	var key string
	if err = db.QueryRow(`SELECT d.poster_url FROM catalog_entities e JOIN catalog_item_details d ON d.entity_id=e.id WHERE e.public_id=pid_blob(?)`, songs[0].ID).Scan(&key); err != nil {
		t.Fatal(err)
	}
	art, mime, err := meta.Open(strings.TrimPrefix(key, "local:"))
	if err != nil || (mime != "image/jpeg" && mime != "image/png") {
		t.Fatal("embedded art missing", mime, err)
	}
	art.Close()
	bookRows, _, err := cat.Books(catalog.Viewer{Profile: "profile", Libraries: []string{bookLib.ID}}, bookLib.ID, "", 10)
	if err != nil || len(bookRows) != 1 || bookRows[0].Title != "Synthetic Book" || bookRows[0].Narrator != "Local Narrator" {
		t.Fatal(bookRows, err)
	}
	files, _, err := cat.AudioItems(catalog.Viewer{Profile: "profile", Libraries: []string{bookLib.ID}}, bookRows[0].ID, "book", "", 10)
	if err != nil || len(files) != 2 || files[0].Kind != "audiobook_file" {
		t.Fatal(files, err)
	}
	chapters, _, err := cat.BookChapters(catalog.Viewer{Libraries: []string{bookLib.ID}}, bookRows[0].ID, "", 10)
	if err != nil || len(chapters) != 2 || chapters[0].Title != "Opening" || chapters[1].StartSeconds != 2 || chapters[1].EndSeconds != 4 {
		t.Fatal(chapters, err)
	}
	// Author and series groups are published by the background worker
	// (cmd/server wires RefreshListeningGroups), never by a Content request.
	for more := true; more; {
		if more, err = cat.RefreshListeningGroups(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	catalogtest.New(t, db).Drain()
	projection, err := cat.Content(catalog.ContentRequest{Viewer: catalog.Viewer{Profile: "profile", Fence: "f", Libraries: []string{bookLib.ID}}, ServerID: "fixture", Library: bookLib.ID, Profile: "profile", ViewerFence: "f", View: "book", EntityID: bookRows[0].ID, Limit: 1})
	if err != nil || projection.Heading.Fallback != "Synthetic Book" || len(projection.Sections) != 3 || projection.Sections[0].TotalCount != 2 || projection.Sections[1].TotalCount != 2 {
		t.Fatal("book semantic projection", projection, err)
	}
	if projection.Sections[2].ID != "context" || projection.Sections[2].TotalCount != 1 {
		t.Fatal("missing author context", projection.Sections[2])
	}
	for _, section := range projection.Sections[:2] {
		next, err := cat.Content(catalog.ContentRequest{Viewer: catalog.Viewer{Profile: "profile", Fence: "f", Libraries: []string{bookLib.ID}}, ServerID: "fixture", Library: bookLib.ID, Profile: "profile", ViewerFence: "f", View: "book", EntityID: bookRows[0].ID, Limit: 1, Cursor: section.NextCursor})
		if err != nil || len(next.Sections) != 1 || next.Sections[0].ID != section.ID || len(next.Sections[0].Entries) != 1 {
			t.Fatal("section continuation", next, err)
		}
		if section.ID == "chapters" && *next.Sections[0].Entries[0].Playback.StartSeconds != 2 {
			t.Fatal("chapter offset lost", next)
		}
	}
	albumProjection, err := cat.Content(catalog.ContentRequest{Viewer: catalog.Viewer{Profile: "profile", Fence: "f", Libraries: []string{musicLib.ID}}, Library: musicLib.ID, Profile: "profile", ViewerFence: "f", View: "artist", EntityID: compilation, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	foundRelease := false
	for _, section := range albumProjection.Sections {
		if section.ID == "releases" {
			foundRelease = true
			if len(section.Entries) != 1 || section.Entries[0].PosterURL == "" {
				t.Fatal("album art projection", section)
			}
		}
	}
	if !foundRelease {
		t.Fatal("artist releases missing", albumProjection)
	}

	// Playback admission reads the person's stream limits from their account.
	if _, err = db.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('owner','owner',x'00','profile',1)`); err != nil {
		t.Fatal(err)
	}
	player := playback.New(db)
	p := identity.Principal{Hash: "local-session", Viewer: identity.Viewer{AccountID: "owner", ProfileID: "profile", Role: "owner", Authority: "local"}}
	first, err := player.Create(p, files[0].ID, "automatic", "first")
	if err != nil || first.Mode != "direct" {
		t.Fatal(first, err)
	}
	if err = player.Progress(p, first.ID, first.Generation, 1, 3.9, "paused"); err != nil {
		t.Fatal(err)
	}
	resumed, err := player.Create(p, files[0].ID, "automatic", "resume")
	if err != nil || resumed.ResumeSeconds != 3.9 {
		t.Fatal("near-end bookmark reset", resumed, err)
	}
	nearHome, err := cat.HomeRows(catalog.HomeRequest{Viewer: catalog.Viewer{Profile: identity.PersonalKey(p.Viewer), Fence: "near", Libraries: []string{bookLib.ID}}, Profile: identity.PersonalKey(p.Viewer), ViewerFence: "near", Libraries: []string{bookLib.ID}})
	if err != nil {
		t.Fatal(err)
	}
	// Home now displays a book, with its real playable part in the resume
	// target. The retired file-level continue_listening row is not the contract.
	checkBookResume := func(home catalog.HomeDocument, item string, partPosition, bookPosition float64) {
		t.Helper()
		for _, row := range home.Rows {
			if row.ID != "continue_books" {
				continue
			}
			if row.Total != 1 || len(row.Entries) != 1 || len(row.Resumes) != 1 {
				t.Fatalf("book continuation: %+v", row)
			}
			book, resume := row.Entries[0], row.Resumes[0]
			if book.Kind != "book" || book.ID != bookRows[0].ID || book.Playback == nil || book.Playback.ItemID != item || book.Playback.StartSeconds == nil || *book.Playback.StartSeconds != partPosition || book.ProgressSeconds == nil || math.Abs(*book.ProgressSeconds-bookPosition) > 0.000001 || book.Duration == nil || math.Abs(*book.Duration-(files[0].Duration+files[1].Duration)) > 0.000001 {
				t.Fatalf("whole-book bookmark: %+v", book)
			}
			if resume.EntryID != book.ID || resume.Target.ID != item || resume.Target.ProgressSeconds == nil || *resume.Target.ProgressSeconds != partPosition {
				t.Fatalf("real part bookmark: %+v", resume)
			}
			return
		}
		t.Fatal("book bookmark missing from Home")
	}
	checkBookResume(nearHome, files[0].ID, 3.9, 3.9)
	second, err := player.Create(p, files[1].ID, "automatic", "second")
	if err != nil {
		t.Fatal(err)
	}
	if err = player.Progress(p, second.ID, second.Generation, 1, 1, "paused"); err != nil {
		t.Fatal(err)
	}
	if err = player.Progress(p, resumed.ID, resumed.Generation, 100, 4, "ended"); err != nil {
		t.Fatal(err)
	}
	bookRows, _, err = cat.Books(catalog.Viewer{Profile: identity.PersonalKey(p.Viewer), Libraries: []string{bookLib.ID}}, bookLib.ID, "", 10)
	if err != nil || bookRows[0].Resume.ItemID != files[1].ID || bookRows[0].Resume.PositionSeconds != 1 {
		t.Fatal("stale file moved book bookmark", bookRows, err)
	}
	other, _, err := cat.Books(catalog.Viewer{Profile: "other-profile", Libraries: []string{bookLib.ID}}, bookLib.ID, "", 10)
	if err != nil || other[0].Resume != nil {
		t.Fatal("book bookmark leaked profiles", other, err)
	}
	songSession, err := player.Create(p, songs[0].ID, "automatic", "home-song")
	if err != nil {
		t.Fatal(err)
	}
	if err = player.Progress(p, songSession.ID, songSession.Generation, 1, 0.5, "paused"); err != nil {
		t.Fatal(err)
	}
	home, err := cat.HomeRows(catalog.HomeRequest{Viewer: catalog.Viewer{Profile: identity.PersonalKey(p.Viewer), Fence: "f", Libraries: []string{musicLib.ID, bookLib.ID}}, ServerID: "fixture", Profile: identity.PersonalKey(p.Viewer), ViewerFence: "f", Libraries: []string{musicLib.ID, bookLib.ID}})
	if err != nil {
		t.Fatal("audio Home", home, err)
	}
	checkBookResume(home, files[1].ID, 1, files[0].Duration+1)
	// Music remains resumable as its album independently of the book row.
	var musicResumes []catalog.HomeResume
	for _, row := range home.Rows {
		if row.ID == "listen_next" {
			for _, resume := range row.Resumes {
				if resume.EntryID == albums[0].ID {
					musicResumes = append(musicResumes, resume)
				}
			}
		}
	}
	if len(musicResumes) != 1 || musicResumes[0].Target.ID != songs[0].ID || musicResumes[0].Target.ProgressSeconds == nil || *musicResumes[0].Target.ProgressSeconds != 0.5 {
		t.Fatalf("music bookmark missing from Home: %+v", musicResumes)
	}
	otherHome, err := cat.HomeRows(catalog.HomeRequest{Viewer: catalog.Viewer{Profile: "other-profile", Fence: "other", Libraries: []string{musicLib.ID, bookLib.ID}}, Profile: "other-profile", ViewerFence: "other", Libraries: []string{musicLib.ID, bookLib.ID}})
	if err != nil {
		t.Fatal(err)
	}
	for _, section := range otherHome.Rows {
		if section.ID != "listen_next" && section.ID != "continue_books" {
			continue
		}
		if len(section.Resumes) > 0 || section.ID == "continue_books" && len(section.Entries) > 0 {
			t.Fatal("Home progress leaked", section)
		}
		// Listen next also includes recent additions for a new profile. Those
		// titles must not inherit another profile's position or resume target.
		for _, entry := range section.Entries {
			if entry.ProgressSeconds != nil && *entry.ProgressSeconds != 0 || entry.Playback != nil && entry.Playback.StartSeconds != nil && *entry.Playback.StartSeconds != 0 {
				t.Fatal("recent title inherited another profile's bookmark", entry)
			}
		}
	}
	var mbTags int
	if err = db.QueryRow(`SELECT count(*) FROM audio_tag_evidence WHERE field IN('musicbrainz_trackid','musicbrainz_albumid','musicbrainz_releasetrackid') AND source='embedded'`).Scan(&mbTags); err != nil || mbTags != 3 {
		t.Fatal("real FLAC MBID tags not persisted", mbTags, err)
	}
	var evidence int
	if err = db.QueryRow(`SELECT count(*) FROM audio_tag_evidence WHERE source='opf' AND field='author' AND value='Local Author'`).Scan(&evidence); err != nil || evidence != 2 {
		t.Fatal("local tag provenance missing", evidence, err)
	}
	var providerJobs int
	_ = db.QueryRow(`SELECT count(*) FROM metadata_jobs`).Scan(&providerJobs)
	if providerJobs != 0 {
		t.Fatal("audio queued to movie provider")
	}
}
