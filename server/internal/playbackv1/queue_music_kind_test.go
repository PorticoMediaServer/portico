package playbackv1

import (
	"context"
	"database/sql"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
)

func TestQueueMusicClassificationIncludesEveryChunkAndRefusesOtherMedia(t *testing.T) {
	c := catalogtest.Open(t)
	music := c.Library("music", "Music", "music", "/music")
	song := c.Song(c.Album(c.Artist(music, "Artist"), "Album", 2026), 1, "/music/song.flac", "Song")
	movies := c.Library("movies", "Movies", "movie", "/movies")
	movie := c.Movie(movies, "/movies/movie.mkv", "Movie", 2026)
	books := c.Library("books", "Books", "audiobook", "/books")
	book := c.BookFile(c.Book(books, "Book", "Author"), 1, "/books/book.m4b")
	c.Exec(`INSERT INTO queues_v1(id,owner_kind,owner_id,authority,account_id,profile_id,revision,created_ms,updated_ms) VALUES('q_kind','device','device','local','account','profile',1,0,0)`)
	for _, tc := range []struct {
		name string
		ids  []string
		want bool
	}{{"music", []string{song.Public, song.Public}, true}, {"mixed", []string{song.Public, movie.Public}, false}, {"book", []string{book.Public}, false}} {
		t.Run(tc.name, func(t *testing.T) {
			c.Exec(`INSERT INTO queue_v1_snapshots(id,queue_id,kind,selector,label,count,width,music_only) VALUES(?,'q_kind','items','{}','',0,0,1)`, tc.name)
			c.Write(func(ctx context.Context, tx *sql.Tx) error {
				if err := writeQueueChunkTx(ctx, tx, tc.name, 0, encodeChunk(tc.ids), false); err != nil {
					return err
				}
				// A later music-only chunk must never erase an earlier nonmusic fact.
				if err := writeQueueChunkTx(ctx, tx, tc.name, 1, encodeChunk([]string{song.Public}), false); err != nil {
					return err
				}
				var got bool
				if err := tx.QueryRowContext(ctx, `SELECT music_only FROM queue_v1_snapshots WHERE id=?`, tc.name).Scan(&got); err != nil {
					return err
				}
				if got != tc.want {
					t.Fatal("snapshot classification ignored actual file kinds", got, tc.want)
				}
				return nil
			})
		})
	}
}
