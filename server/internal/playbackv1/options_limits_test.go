package playbackv1

import (
	"context"
	"fmt"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

func TestOptionsIncludesEveryVersionStreamAndChapter(t *testing.T) {
	c := catalogtest.Open(t)
	library := c.Library("library", "Movies", "movie", "/fixture")
	item := c.Movie(library, "/fixture/first.mp4", "Many versions", 2026)
	for i := 1; i < 34; i++ {
		c.File(item.ID, fmt.Sprintf("/fixture/version-%d.mp4", i), 60)
	}
	c.Drain()
	if _, err := c.DB.Exec(`INSERT INTO asset_stream_facts(asset_id,revision,size,modified_ns,fingerprint) VALUES(?,1,1000,1,'test')`, item.Token); err != nil {
		t.Fatal(err)
	}
	if _, err := c.DB.Exec(`INSERT INTO asset_chapter_facts(asset_id,revision,size,modified_ns,status,fingerprint) VALUES(?,1,1000,1,'known','test')`, item.Token); err != nil {
		t.Fatal(err)
	}
	tx, err := c.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 257; i++ {
		if _, err = tx.Exec(`INSERT INTO asset_streams(asset_id,stream_index,type,codec,language,title,channels,channel_layout,is_default,is_forced) VALUES(?,?,'audio','aac','eng','',2,'stereo',0,0)`, item.Token, i); err != nil {
			break
		}
	}
	for i := 0; err == nil && i < 501; i++ {
		_, err = tx.Exec(`INSERT INTO asset_chapters(asset_id,chapter_index,title,start_seconds,end_seconds) VALUES(?,?,?, ?, ?)`, item.Token, i, fmt.Sprintf("Chapter %d", i), float64(i), float64(i+1))
	}
	if err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	got, err := New(c.DB, nil, nil).Options(context.Background(), identity.Principal{}, item.Public, Preview{VersionID: item.Token}, nil, Reach{})
	if err != nil {
		t.Fatal(err)
	}
	var audio int
	for _, v := range got.Versions {
		if v.ID == item.Token {
			audio = len(v.Audio)
		}
	}
	if len(got.Versions) != 34 || audio != 257 || len(got.Chapters) != 501 {
		t.Fatalf("versions=%d audio=%d chapters=%d", len(got.Versions), audio, len(got.Chapters))
	}
}
