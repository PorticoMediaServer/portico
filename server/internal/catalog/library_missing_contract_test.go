package catalog

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
)

func TestLibraryMissingRemovalKeepsDiskAndSeparateProfileTombstones(t *testing.T) {
	c := catalogtest.Open(t)
	root := t.TempDir()
	path := filepath.Join(root, "Film (2000).mkv")
	const media = "source bytes must survive catalogue removal"
	if err := os.WriteFile(path, []byte(media), 0600); err != nil {
		t.Fatal(err)
	}
	c.Library("library", "Films", "movie", root)
	asset, token := identityFile(t, c, "library", path, 1080)
	item := attachMovie(t, c, "library", asset, path, "Film", 2000)
	c.Exec(`INSERT INTO personal_items(profile_id,item_id,watchlisted,favorite,rating,revision,watched,last_played_at,not_interested) VALUES('p',?,1,0,8,1,0,'',0)`, item)
	c.Exec(`INSERT INTO progress(profile_id,item_id,position,unit,completed,playback_id) VALUES('q',?,10000,0,0,'play')`, item)
	markMissing(t, c, "library", token, time.Now().Add(-24*time.Hour))
	s := New(c.DB)
	page, err := s.MissingTitles(context.Background(), "library", "", 40)
	if err != nil || page.Total != 1 || len(page.Items) != 1 {
		t.Fatal("current missing projection", page, err)
	}
	denied := errors.New("authority revoked")
	if _, err = s.EmptyMissing(context.Background(), "library", time.Now(), func(*sql.Tx) error { return denied }); !errors.Is(err, denied) {
		t.Fatal(err)
	}
	if rows(t, c, `SELECT count(*) FROM catalog_entities WHERE id=?`, item) != 1 || rows(t, c, `SELECT count(*) FROM retained_title_state`) != 0 {
		t.Fatal("denied removal wrote catalogue or tombstones")
	}
	out, err := s.EmptyMissing(context.Background(), "library", time.Now(), func(*sql.Tx) error { return nil })
	if err != nil || out.Titles != 1 || out.Files != 1 || rows(t, c, `SELECT count(*) FROM retained_title_state`) != 2 {
		t.Fatal("missing removal lost profile state", out, err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != media {
		t.Fatal("catalogue removal changed media", err)
	}
	page, err = s.MissingTitles(context.Background(), "library", "", 40)
	if err != nil || page.Total != 0 || len(page.Items) != 0 {
		t.Fatal("removed title remained in missing projection", page, err)
	}
}
