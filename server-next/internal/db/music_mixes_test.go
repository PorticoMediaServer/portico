package db

import (
	"context"
	"testing"
)

func TestMixesBeforeTheSoundAnalysisSchema(t *testing.T) {
	ctx := context.Background()
	conn := databaseFrom(t, schemaSQL, schemaVersion())
	d := &DB{read: conn}
	// A new empty analysis table already gives no songs.
	if songs, err := d.RecentPlayedSongs(ctx, Viewer{}, "model", 100); err != nil || len(songs) != 0 {
		t.Fatalf("before analysis: %d songs, %v", len(songs), err)
	}
	// Only this synthetic fixture is changed, to match an earlier pre-launch schema.
	if _, err := conn.Exec(`DROP TABLE track_embeddings`); err != nil {
		t.Fatal(err)
	}
	if songs, err := d.RecentPlayedSongs(ctx, Viewer{}, "model", 100); err != nil || len(songs) != 0 {
		t.Fatalf("before the analysis table: %d songs, %v", len(songs), err)
	}
	// Other database failures must not become an empty successful read.
	conn.Close()
	if _, err := d.RecentPlayedSongs(ctx, Viewer{}, "model", 100); err == nil {
		t.Fatal("closed database was accepted")
	}
}
