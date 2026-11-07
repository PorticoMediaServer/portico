package read

import (
	"errors"
	"strings"
	"testing"
)

// Pure mocked controls: no Reader Capture, database, fixture, or native engine.
func TestNativeAutomaticEqualityTail(t *testing.T) {
	var calls []string
	q := nativeAutoQuery(func(name string, args ...any) ([][]any, error) {
		calls = append(calls, name)
		if strings.HasSuffix(name, "tail0") {
			return [][]any{{int64(9), "relations"}}, nil
		}
		return nil, nil
	})
	after := nativeAutoKey{N: 2}
	after.V[0].I = 8
	after.V[1] = nativeAutoValue{S: "title", Text: true}
	k, ok, e := nativeAutoSeek(q, "origins", []any{"evidence"}, after)
	if e != nil || !ok || k.V[0].I != 9 || len(calls) != 2 || !strings.HasSuffix(calls[0], "tail1") {
		t.Fatalf("tail seek: %+v %v %v %v", k, ok, e, calls)
	}
}
func TestNativeAutomaticRejectedRawAdvances(t *testing.T) {
	q := func(name string, args ...any) ([][]any, error) {
		switch name {
		case "full.native.auto.map":
			return [][]any{{int64(7), int64(0)}}, nil
		case "full.native.auto.owner":
			return [][]any{{int64(7)}}, nil
		case "full.native.auto.over.first":
			return [][]any{{int64(42)}}, nil
		case "full.native.auto.work":
			return nil, nil // retired/wrong library/kind
		default:
			t.Fatalf("unexpected query %s", name)
			return nil, nil
		}
	}
	c := nativeAutomaticCursor{Library: 1, Collection: 7, HaveMapping: true, Phase: 3, Mapping: nativeAutoKey{N: 3}}
	c.Mapping.V[0].I = 1
	c.Mapping.V[1] = nativeAutoValue{S: "tmdb", Text: true}
	c.Mapping.V[2] = nativeAutoValue{S: "123", Text: true}
	p, next, done, e := nativeAutomaticNext(q, 1, 7, &c)
	if e != nil || done || p.Work != 0 || next == nil || next.Override.V[0].I != 42 || c.Override.N != 0 {
		t.Fatalf("rejected raw position lost: %+v %+v %v %v", p, next, done, e)
	}
}
func TestNativeAutomaticWorkspaceRefuses(t *testing.T) {
	calls := 0
	q := nativeAutoQuery(func(string, ...any) ([][]any, error) { calls++; return [][]any{{strings.Repeat("x", 140000)}}, nil })
	_, e := nativeAutoPoint(q, "work", int64(1), int64(1))
	if !errors.Is(e, errFullSearchPending) || calls != 1 {
		t.Fatalf("oversized proof returned success: %v", e)
	}
}
