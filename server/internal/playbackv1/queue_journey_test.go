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

func TestJourneySelectorIsExclusiveAndBounded(t *testing.T) {
	valid := JourneySelector{From: "from", To: "to", Length: 2}
	if err := (Selector{Journey: &valid}).validate("source"); err != nil {
		t.Fatal(err)
	}
	for _, other := range []Selector{{Items: &ItemList{IDs: []string{"item"}}}, {Container: &ContainerRef{Kind: "album", ID: "album"}}, {Saved: &SavedRef{ID: "saved"}}} {
		other.Journey = &valid
		if err := other.validate("source"); err == nil {
			t.Fatal("Journey accepted another selector", other)
		}
	}
	for _, invalid := range []JourneySelector{{From: "from", To: "to", Length: 1}, {From: "from", To: "to", Length: 129}, {From: "same", To: "same", Length: 2}, {To: "to", Length: 2}} {
		if err := (Selector{Journey: &invalid}).validate("source"); err == nil {
			t.Fatal("invalid Journey accepted", invalid)
		}
	}
}

func TestJourneyCaptureIsFiniteImmutableAndNeverDropsRefusedSongs(t *testing.T) {
	c := catalogtest.Open(t)
	library := c.Library("music", "Music", "music", "/music")
	album := c.Album(c.Artist(library, "Artist"), "Album", 2026)
	ids := []string{}
	var forbidden int64
	for n := 0; n < 5; n++ {
		item := c.Song(album, n+1, fmt.Sprintf("/music/journey-%d.flac", n), fmt.Sprintf("Song %d", n))
		ids = append(ids, item.Public)
		if n == 2 {
			forbidden = item.ID
		}
	}
	c.Drain()
	s := New(c.DB, nil, nil)
	t.Cleanup(func() { releaseCaptures(s) })
	actor := identity.Principal{Viewer: identity.Viewer{Authority: "local", AccountID: "account", ProfileID: "profile"}}
	journey := JourneySelector{From: ids[0], To: ids[4], Length: 5}
	s.JourneyKeys = func(ctx context.Context, tx *sql.Tx, p identity.Principal, request JourneySelector) ([]string, error) {
		if tx == nil || p.Viewer != actor.Viewer || request != journey {
			t.Fatal("Journey lost current actor or capture transaction", p, tx, request)
		}
		return append([]string(nil), ids...), nil
	}
	in := SegmentInput{Source: Selector{Journey: &journey}}
	snap, err := s.buildSnapshot(context.Background(), actor, "q_journey", in, "source", MaxQueueKeys, nil, scanBudget{keys: 1})
	if err != nil || snap.partial || snap.count != 5 || snap.kind != "selector" || len(snap.pending) != 1 {
		t.Fatal(snap, err)
	}
	s.JourneyKeys = nil // Changing analysis after capture cannot change its chunks.
	got := []string{}
	for n := range ids {
		key, ok := chunkKey(snap.pending[0].keys, n)
		if !ok {
			t.Fatal("captured occurrence missing", n)
		}
		got = append(got, key)
	}
	if !reflect.DeepEqual(got, ids) {
		t.Fatal(got, ids)
	}
	for _, bad := range [][]string{ids[:4], {ids[0], ids[1], ids[1], ids[3], ids[4]}, {ids[1], ids[0], ids[2], ids[3], ids[4]}} {
		s.JourneyKeys = func(context.Context, *sql.Tx, identity.Principal, JourneySelector) ([]string, error) { return bad, nil }
		if _, err = s.buildSnapshot(context.Background(), actor, "q_bad", in, "source", MaxQueueKeys, nil, scanBudget{}); !errors.Is(err, ErrUnsupportedSelector) {
			t.Fatal("invalid path fell back or was shortened", bad, err)
		}
	}
	s.JourneyKeys = func(context.Context, *sql.Tx, identity.Principal, JourneySelector) ([]string, error) { return ids, nil }
	s.Visibility = func(context.Context, *sql.Tx, identity.Principal, string) (string, []any, error) {
		return "i.id<>?", []any{forbidden}, nil
	}
	staged := false
	_, err = s.buildSnapshot(context.Background(), actor, "q_forbidden", in, "source", MaxQueueKeys, func(context.Context, *sql.Tx) error { staged = true; return nil }, scanBudget{})
	if !errors.Is(err, ErrUnsupportedSelector) || staged {
		t.Fatal("refused Journey staged a shortened queue", err, staged)
	}
}
