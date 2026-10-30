package playback

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/decodertest"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"strings"
	"testing"
)

func TestRealCurrentSourceChaptersAudioAndVideo(t *testing.T) {
	ffmpeg := decodertest.QualifiedFFmpeg(t)
	probe := decodertest.QualifiedFFprobe(t)
	for _, kind := range []string{"movie", "audiobook"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			metadata := filepath.Join(root, "chapters.txt")
			os.WriteFile(metadata, []byte(";FFMETADATA1\nalbum=Test Book\nartist=Test Author\n[CHAPTER]\nTIMEBASE=1/1000\nSTART=0\nEND=1000\ntitle=Opening\n[CHAPTER]\nTIMEBASE=1/1000\nSTART=1000\nEND=2000\n"), 0600)
			ext := ".m4b"
			args := []string{"-nostdin", "-v", "error", "-f", "lavfi", "-i", "sine=frequency=220:duration=2"}
			if kind == "movie" {
				ext = ".mp4"
				args = []string{"-nostdin", "-v", "error", "-f", "lavfi", "-i", "testsrc2=size=160x90:duration=2"}
			}
			path := filepath.Join(root, "Chapters (2026)"+ext)
			args = append(args, "-f", "ffmetadata", "-i", metadata, "-map", "0", "-map_metadata", "1", "-map_chapters", "1")
			if kind == "movie" {
				args = append(args, "-c:v", "libx264")
			} else {
				args = append(args, "-c:a", "aac")
			}
			args = append(args, path)
			if raw, e := exec.Command(ffmpeg, args...).CombinedOutput(); e != nil {
				t.Fatal(e, string(raw))
			}
			facts, e := (assets.Probe{Binary: probe}).Inspect(context.Background(), path)
			if e != nil || facts.ChapterStatus != "known" || len(facts.Chapters) != 2 {
				t.Fatal(facts, e)
			}
			db, e := persistence.Open(filepath.Join(root, "db"))
			if e != nil {
				t.Fatal(e)
			}
			defer db.Close()
			cat := catalogtest.New(t, db)
			library := cat.Library("library", "Library", kind, root)
			var entity catalogtest.Item
			if kind == "movie" {
				entity = cat.Entity(compactcatalog.Entity{Library: library, Kind: compactcatalog.Movie, Key: compactcatalog.ItemKey(root, path, 0), Title: "Chapters (2026)"}, nil)
			} else {
				book := cat.Book(library, "Test Book", "Test Author")
				entity = cat.Entity(compactcatalog.Entity{Library: library, Kind: compactcatalog.Part, Parent: book.ID, Key: compactcatalog.ItemKey(root, path, 0), Title: "Part 1"}, map[string]any{"book_id": book.ID, "part_number": 1})
			}
			info, e := os.Stat(path)
			if e != nil {
				t.Fatal(e)
			}
			var token string
			cat.Write(func(ctx context.Context, tx *sql.Tx) error {
				assetID, assetToken, err := compactcatalog.UpsertAssetTx(ctx, tx, compactcatalog.Asset{Path: path, Size: info.Size(), ModifiedNS: info.ModTime().UnixNano(), Container: facts.Container, VideoCodec: facts.VideoCodec, AudioCodec: facts.AudioCodec, Width: facts.Width, Height: facts.Height, Duration: facts.Duration})
				if err != nil {
					return err
				}
				token = assetToken
				if err = compactcatalog.LinkAssetTx(ctx, tx, entity.ID, assetID, compactcatalog.Link{}); err != nil {
					return err
				}
				return assets.PersistChapters(tx, token, info.Size(), info.ModTime().UnixNano(), facts)
			})
			cat.Drain()
			item := entity.Public
			_, e = db.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('owner','owner',x'00','profile',1)`)
			if e != nil {
				t.Fatal(e)
			}
			insertFixtureSession(t, db, "session", "owner", "profile", "owner", "2999-01-01T00:00:00Z")
			p := identity.Principal{Hash: "session", Viewer: identity.Viewer{AccountID: "owner", ProfileID: "profile", Authority: "local", Role: "owner"}}
			s := New(db)
			session, e := s.Create(p, item, "automatic", "one")
			if e != nil {
				t.Fatal(e)
			}
			scope := OffersScope{"server", "library", item, "viewer"}
			first, e := s.Chapters(context.Background(), p, scope, session.ID, "", "", 1)
			if e != nil || first.Status != "available" || first.TotalCount != 2 || first.Chapters[0].Title != "Opening" || first.Chapters[0].Action.PositionSeconds != 0 || first.NextCursor == "" {
				t.Fatal(first, e)
			}
			second, e := s.Chapters(context.Background(), p, scope, session.ID, first.Revision, first.NextCursor, 1)
			if e != nil || second.Chapters[0].Title != "Chapter 2" || second.Chapters[0].StartSeconds != 1 || second.Chapters[0].EndSeconds != 2 {
				t.Fatal(second, e)
			}
			s.Progress(p, session.ID, session.Generation, 1, 0.5, "playing")
			if _, e = s.Chapters(context.Background(), p, scope, session.ID, first.Revision, "", 1); e != nil {
				t.Fatal("progress invalidated chapters", e)
			}
			other := p
			other.ProfileID = "other"
			if _, e = s.Chapters(context.Background(), other, scope, session.ID, "", "", 100); e == nil {
				t.Fatal("foreign profile chapter leak")
			}
			// The exact source stays valid when another logical catalog item links it.
			siblingKind := compactcatalog.Movie
			if kind == "audiobook" {
				siblingKind = compactcatalog.Part
			}
			linkExistingCatalogAsset(t, db, "library", siblingKind, "sibling", "Sibling", first.Scope.SourceID)
			if _, e = s.Chapters(context.Background(), p, scope, session.ID, first.Revision, "", 100); e != nil {
				t.Fatal("shared link changed source", e)
			}
			f, e := assets.OpenPlayback(db, session.ID, "")
			if e != nil {
				t.Fatal(e)
			}
			f.Close()
			updateCatalogAsset(t, db, first.Scope.SourceID, func(a *compactcatalog.Asset) { a.ModifiedNS++ })
			if _, e = s.Chapters(context.Background(), p, scope, session.ID, "", "", 100); !errors.Is(e, ErrStaleChapter) {
				t.Fatal("changed source chapters", e)
			}
			grant := strings.TrimPrefix(session.StreamURL, "/v1/media/")
			if _, _, _, e = s.ResolveGrant(grant); !errors.Is(e, ErrStaleChapter) {
				t.Fatal("changed source grant", e)
			}
			if _, e = assets.OpenPlayback(db, session.ID, ""); e == nil {
				t.Fatal("changed path opened under old session")
			}
		})
	}
}

func TestChapterPagingUnavailableAndCurrentGeneration(t *testing.T) {
	db, e := persistence.Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	id, item, tokenA := catalogFixture(t, db, "l", "/fixture", compactcatalog.Movie, "i", "I", compactcatalog.Asset{Path: "/fixture/a", Size: 1, ModifiedNS: 1, Container: "mp4", VideoCodec: "h264", AudioCodec: "aac", Width: 1, Height: 1, Duration: 1000})
	cat := catalogtest.New(t, db)
	var tokenB string
	cat.Write(func(ctx context.Context, tx *sql.Tx) error {
		assetID, token, err := compactcatalog.UpsertAssetTx(ctx, tx, compactcatalog.Asset{Path: "/fixture/b", Size: 1, ModifiedNS: 1, Container: "mp4", VideoCodec: "h264", AudioCodec: "aac", Width: 1, Height: 1, Duration: 1000})
		if err != nil {
			return err
		}
		tokenB = token
		return compactcatalog.LinkAssetTx(ctx, tx, id, assetID, compactcatalog.Link{Part: 1})
	})
	cat.Drain()
	facts := assets.Facts{Duration: 1000, ChapterStatus: "known"}
	for i := 0; i < 105; i++ {
		facts.Chapters = append(facts.Chapters, assets.Chapter{Title: "", Start: float64(i), End: float64(i + 1)})
	}
	tx, _ := db.Begin()
	if e = assets.PersistChapters(tx, tokenA, 1, 1, facts); e != nil {
		t.Fatal(e)
	}
	if e = assets.PersistChapters(tx, tokenB, 1, 1, assets.Facts{Duration: 1000, ChapterStatus: "known", Chapters: []assets.Chapter{{Title: "Other file", Start: 0, End: 20}}}); e != nil {
		t.Fatal(e)
	}
	tx.Commit()
	p := identity.Principal{Hash: "session", Viewer: identity.Viewer{AccountID: "account", ProfileID: "profile"}}
	insertFixtureAccount(t, db, p.AccountID, p.ProfileID)
	s := New(db)
	session, e := s.Create(p, item, "automatic", "one")
	if e != nil {
		t.Fatal(e)
	}
	scope := OffersScope{"server", "l", item, "fence"}
	first, e := s.Chapters(context.Background(), p, scope, session.ID, "", "", 100)
	if e != nil || len(first.Chapters) != 100 || first.TotalCount != 105 || first.Scope.SourceID != tokenA {
		t.Fatal(first, e)
	}
	second, e := s.Chapters(context.Background(), p, scope, session.ID, first.Revision, first.NextCursor, 100)
	if e != nil || len(second.Chapters) != 5 || second.Chapters[0].Index != 101 || second.NextCursor != "" {
		t.Fatal(second, e)
	}
	if _, e = s.Chapters(context.Background(), p, scope, session.ID, "", first.NextCursor+"tampered", 100); e == nil {
		t.Fatal("cursor tamper admitted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e = s.Chapters(ctx, p, scope, session.ID, "", "", 100); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	tx, _ = db.Begin()
	facts.Chapters = nil
	assets.PersistChapters(tx, tokenA, 1, 1, facts)
	tx.Commit()
	none, e := s.Chapters(context.Background(), p, scope, session.ID, "", "", 100)
	if e != nil || none.Status != "none" || len(none.Chapters) != 0 {
		t.Fatal(none, e)
	}
	if _, e = s.Chapters(context.Background(), p, scope, session.ID, first.Revision, first.NextCursor, 100); !errors.Is(e, ErrStaleChapter) {
		t.Fatal("changed chapter cursor", e)
	}
	tx, _ = db.Begin()
	facts.ChapterStatus = "invalid"
	assets.PersistChapters(tx, tokenA, 1, 1, facts)
	tx.Commit()
	invalid, e := s.Chapters(context.Background(), p, scope, session.ID, "", "", 100)
	if e != nil || invalid.Reason == nil || *invalid.Reason != "invalid_chapter_timeline" {
		t.Fatal(invalid, e)
	}
	db.Exec(`DELETE FROM playback_source_pins WHERE session_id=?`, session.ID)
	legacy, e := s.Chapters(context.Background(), p, scope, session.ID, "", "", 100)
	if e != nil || legacy.Reason == nil || *legacy.Reason != "session_source_unverified" {
		t.Fatal(legacy, e)
	}
	next, e := s.Create(p, item, "automatic", "two")
	if e != nil || next.Generation <= session.Generation {
		t.Fatal(e)
	}
	// Independent sessions coexist; only stopping this exact session removes its chapters.
	if e = s.Stop(p, session.ID); e != nil {
		t.Fatal(e)
	}
	old, e := s.Chapters(context.Background(), p, scope, session.ID, "", "", 100)
	if e != nil || old.Reason == nil || *old.Reason != "session_inactive" || len(old.Chapters) != 0 {
		t.Fatal(old, e)
	}
}
