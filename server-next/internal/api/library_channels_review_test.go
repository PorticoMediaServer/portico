package api

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/librarychannels"
)

// A condition whose value the schedule cannot use is refused when saved, or the channel says
// it needs attention; it never sits in "building" for good.
func TestReviewABadConditionValueNeverLeavesTheChannelBuilding(t *testing.T) {
	s, owner, _ := seededWith(t, 200, 0)
	films := libraryOfKind(t, s, owner, "movies")
	name := "Bad"
	b := librarychannels.Builder{Selection: librarychannels.Selection{Libraries: []string{films}, Kinds: []string{"movie"},
		Conditions: []db.ChannelCondition{{Field: "year", Op: "between", Values: []json.RawMessage{json.RawMessage(`"nineteen"`)}}}}}
	var ch LibraryChannel
	w := call(t, s, "POST", "/v1/admin/library-channels", owner, LibraryChannelSave{Name: &name, Builder: &b}, &ch)
	if w.Code != 201 {
		return // refused when saved: good
	}
	err := s.channels.Schedule(context.Background(), ch.ID)
	got := get[LibraryChannel](t, s, owner, "/v1/admin/library-channels/"+ch.ID)
	if got.State == "building" {
		t.Fatalf("saved (201), the schedule job failed (%v) and the channel reads %q, health %+v", err, got.State, got.Health)
	}
}
