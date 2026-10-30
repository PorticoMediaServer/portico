package playbackv1

import (
	"context"
	"database/sql"
	"reflect"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/sonic"
)

func sonicQueueFixture(t *testing.T) (*catalogtest.Catalog, *Service, Caller, queueRow) {
	t.Helper()
	c := catalogtest.Open(t)
	library := c.Library("music", "Music", "music", "/music")
	album := c.Album(c.Artist(library, "Artist"), "Album", 2026)
	ids := []string{}
	for n := 0; n < 5; n++ {
		item := c.Song(album, n+1, "/music/"+string(rune('a'+n))+".flac", "Song")
		ids = append(ids, item.Public)
	}
	c.Drain()
	caller := Caller{PlaybackProtocol: PlaybackProtocol, Principal: identity.Principal{Viewer: identity.Viewer{Authority: "local", AccountID: "account", ProfileID: "profile"}}}
	q := queueRow{id: "q_capture", shuffle: true, seed: 29, lap: 1, domain: 5}
	c.Exec(`INSERT INTO queues_v1(id,owner_kind,owner_id,authority,account_id,profile_id,revision,created_ms,updated_ms,shuffle_on,shuffle_seed,shuffle_lap,shuffle_domain) VALUES('q_capture','device','device','local','account','profile',1,0,0,1,29,1,5)`)
	s := New(c.DB, nil, nil)
	t.Cleanup(func() { releaseCaptures(s) })
	snap, err := s.buildSnapshot(context.Background(), caller.Principal, q.id, SegmentInput{Source: Selector{Items: &ItemList{IDs: ids}}}, "source", MaxQueueKeys, nil, scanBudget{})
	if err != nil {
		t.Fatal(err)
	}
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		if err := storeSnapshot(ctx, tx, q.id, &snap); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO queue_v1_segments(id,queue_id,sort_key,snapshot_id,first_ordinal,count) VALUES('g_music',?,'a',?,0,5)`, q.id, snap.id)
		return err
	})
	return c, s, caller, q
}

func TestSonicCaptureUsesAdmittedProfileAndReplaysWithoutReanalysis(t *testing.T) {
	c, s, caller, q := sonicQueueFixture(t)
	calls := 0
	s.SmoothOrder = func(ctx context.Context, tx *sql.Tx, actor identity.Principal, ids []string) (catalog.MusicSmoothOrder, error) {
		calls++
		if tx == nil || actor.Viewer != caller.Principal.Viewer || len(ids) != 5 {
			t.Fatal("capture lost actor or bounded source", actor, tx, ids)
		}
		return catalog.MusicSmoothOrder{Positions: []int64{0, 4, 3, 2, 1}, BySound: true, Model: &sonic.Identity{ID: sonic.ModelID, Version: sonic.ModelVersion, SHA256: sonic.ModelSHA256}}, nil
	}
	var first []int64
	for replay := 0; replay < 2; replay++ {
		c.Write(func(ctx context.Context, tx *sql.Tx) error {
			l, err := s.readLayout(ctx, tx, q)
			if err != nil {
				return err
			}
			if err = s.captureMusicWindowTx(ctx, tx, caller, l, 0); err != nil {
				return err
			}
			got := []int64{}
			for position := int64(0); position < 5; position++ {
				got = append(got, l.orderToSource(position))
				if l.sourceToOrder(got[position]) != position {
					t.Fatal("captured inverse changed", got, position)
				}
			}
			if replay == 0 {
				first = got
			} else if !reflect.DeepEqual(first, got) {
				t.Fatal("restart changed captured order", first, got)
			}
			return nil
		})
	}
	if calls != 1 {
		t.Fatal("replay read changed model/analysis", calls)
	}
}

func TestSonicCaptureNeverActivatesForMixedQueueWithMusicOnlyCurrentWindow(t *testing.T) {
	c, s, caller, q := sonicQueueFixture(t)
	movies := c.Library("movies", "Movies", "movie", "/movies")
	movie := c.Movie(movies, "/movies/movie.mkv", "Movie", 2026)
	c.Drain()
	snap, err := s.buildSnapshot(context.Background(), caller.Principal, q.id, SegmentInput{Source: Selector{Items: &ItemList{IDs: []string{movie.Public}}}}, "source", MaxQueueKeys, nil, scanBudget{})
	if err != nil {
		t.Fatal(err)
	}
	s.SmoothOrder = func(context.Context, *sql.Tx, identity.Principal, []string) (catalog.MusicSmoothOrder, error) {
		t.Fatal("mixed queue consulted Sonic")
		return catalog.MusicSmoothOrder{}, nil
	}
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		if err := storeSnapshot(ctx, tx, q.id, &snap); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO queue_v1_segments(id,queue_id,sort_key,snapshot_id,first_ordinal,count) VALUES('g_movie',?,'z',?,0,1)`, q.id, snap.id); err != nil {
			return err
		}
		l, err := s.readLayout(ctx, tx, q)
		if err != nil {
			return err
		}
		if err = s.captureMusicWindowTx(ctx, tx, caller, l, 0); err != nil {
			return err
		}
		var count int
		if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM queue_v1_sonic_windows WHERE queue_id=?`, q.id).Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			t.Fatal("mixed queue captured a music permutation", count)
		}
		return nil
	})
}

func TestSonicCaptureFreezesHeardOrdinaryWindowBeforeBackgroundKeysExist(t *testing.T) {
	c, s, caller, q := sonicQueueFixture(t)
	var snapshot string
	var raw []byte
	if err := c.DB.QueryRow(`SELECT snapshot_id FROM queue_v1_segments WHERE id='g_music'`).Scan(&snapshot); err != nil {
		t.Fatal(err)
	}
	if err := c.DB.QueryRow(`SELECT keys FROM queue_v1_chunks WHERE snapshot_id=? AND chunk_no=0`, snapshot).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	c.Exec(`UPDATE queue_v1_snapshots SET state='building',built=0 WHERE id=?`, snapshot)
	c.Exec(`DELETE FROM queue_v1_chunks WHERE snapshot_id=?`, snapshot)
	s.SmoothOrder = func(context.Context, *sql.Tx, identity.Principal, []string) (catalog.MusicSmoothOrder, error) {
		t.Fatal("unknown or already captured window consulted later analysis")
		return catalog.MusicSmoothOrder{}, nil
	}
	var first []int64
	for phase := 0; phase < 2; phase++ {
		c.Write(func(ctx context.Context, tx *sql.Tx) error {
			if phase == 1 {
				if err := writeQueueChunkTx(ctx, tx, snapshot, 0, raw, false); err != nil {
					return err
				}
				if _, err := tx.ExecContext(ctx, `UPDATE queue_v1_snapshots SET state='ready',built=5 WHERE id=?`, snapshot); err != nil {
					return err
				}
			}
			l, err := s.readLayout(ctx, tx, q)
			if err != nil {
				return err
			}
			if err = s.captureMusicWindowTx(ctx, tx, caller, l, 0); err != nil {
				return err
			}
			if captured, ok := l.sonicWindows[0]; !ok || captured.model != nil {
				t.Fatal("unknown background coverage claimed smoothing", captured, ok)
			}
			got := []int64{}
			for position := int64(0); position < 5; position++ {
				got = append(got, l.orderToSource(position))
			}
			if phase == 0 {
				first = got
			} else if !reflect.DeepEqual(first, got) {
				t.Fatal("background completion changed heard order", first, got)
			}
			return nil
		})
	}
}
