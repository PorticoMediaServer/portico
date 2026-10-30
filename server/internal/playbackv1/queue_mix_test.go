package playbackv1

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

func TestMixCaptureUsesFiniteOrdinaryChunksAndCurrentItemAdmission(t *testing.T) {
	c := catalogtest.Open(t)
	library := c.Library("music", "Music", "music", "/music")
	album := c.Album(c.Artist(library, "Artist"), "Album", 2026)
	keys := []string{}
	var forbidden int64
	for n := 0; n < 6; n++ {
		item := c.Song(album, n+1, fmt.Sprintf("/music/%d.flac", n), fmt.Sprintf("Song %d", n))
		keys = append(keys, item.Public)
		if n == 2 {
			forbidden = item.ID
		}
	}
	c.Drain()
	s := New(c.DB, nil, nil)
	t.Cleanup(func() { releaseCaptures(s) })
	actor := identity.Principal{Viewer: identity.Viewer{Authority: "local", AccountID: "account", ProfileID: "profile"}}
	const mixID = "mix-current"
	s.MixKeys = func(ctx context.Context, tx *sql.Tx, p identity.Principal, id string) ([]string, error) {
		if tx == nil || p.Viewer != actor.Viewer || id != mixID {
			t.Fatal("capture lost admitted transaction or current actor", tx, p, id)
		}
		return append([]string(nil), keys...), nil
	}
	s.Visibility = func(ctx context.Context, tx *sql.Tx, p identity.Principal, column string) (string, []any, error) {
		return column + "<>?", []any{forbidden}, nil
	}
	in := SegmentInput{Source: Selector{Container: &ContainerRef{Kind: "mix", ID: mixID}}}
	snap, err := s.buildSnapshot(context.Background(), actor, "q_mix", in, "source", MaxQueueKeys, nil, scanBudget{keys: 1000})
	if err != nil || snap.partial || snap.kind != "selector" || snap.sourceRevision != mixID || snap.count != 5 || len(snap.pending) != 1 {
		t.Fatal(snap, err)
	}
	actual := []string{}
	for n := 0; n < int(snap.count); n++ {
		key, ok := chunkKey(snap.pending[0].keys, n)
		if !ok {
			t.Fatal("ordinary chunk lost occurrence", n)
		}
		actual = append(actual, key)
	}
	expected := append(append([]string{}, keys[:2]...), keys[3:]...)
	if !reflect.DeepEqual(actual, expected) {
		t.Fatal("newly forbidden key captured or member order changed", actual, expected)
	}
	s.MixKeys = func(context.Context, *sql.Tx, identity.Principal, string) ([]string, error) {
		return nil, sql.ErrNoRows
	}
	if _, err := s.buildSnapshot(context.Background(), actor, "q_stale", in, "source", MaxQueueKeys, nil, scanBudget{keys: 1000}); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("stale mix did not use existing not-found", err)
	}
}
