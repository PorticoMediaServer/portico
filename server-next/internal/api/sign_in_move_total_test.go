package api

import (
	"fmt"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

func TestMoveTotalStaysWhenAnAccountMovesOrIsRemoved(t *testing.T) {
	// Two accounts were asked to move; Sam has already moved, Alex still waits.
	s, owner, _ := porticoServer(t)
	var state SignInState
	call(t, s, "GET", "/v1/admin/sign-in", owner, nil, &state)
	if state.Move == nil || state.Move.Total == nil || *state.Move.Total != 2 || len(state.Waiting) != 1 {
		t.Fatalf("after Sam moves: %+v, waiting %+v", state.Move, state.Waiting)
	}
	if code := status(t, s, "DELETE", "/v1/admin/members/"+state.Waiting[0].ID, owner, nil); code != 204 {
		t.Fatalf("remove Alex: %d", code)
	}
	call(t, s, "GET", "/v1/admin/sign-in", owner, nil, &state)
	if state.Move == nil || state.Move.Total == nil || *state.Move.Total != 2 || len(state.Waiting) != 0 {
		t.Fatalf("after Alex is removed: %+v, waiting %+v", state.Move, state.Waiting)
	}

	// A move begun by an older server has no denominator, rather than a guessed one.
	raw := fmt.Sprintf(`{"to":"portico","at":%d}`, s.now().UnixMilli())
	if err := s.db.SaveSetting(t.Context(), db.MoveKey, raw, s.now()); err != nil {
		t.Fatal(err)
	}
	w := call(t, s, "GET", "/v1/admin/sign-in", owner, nil, nil)
	if w.Code != 200 || strings.Contains(w.Body.String(), `"total"`) {
		t.Fatalf("older move: %d %s", w.Code, w.Body)
	}
}
