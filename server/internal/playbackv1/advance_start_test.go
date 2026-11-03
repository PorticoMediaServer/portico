package playbackv1

import (
	"context"
	"errors"
	"testing"
)

// MU-7: a chapter in another book part starts that part at the chapter, in the
// same :advance that moves the queue, never at the part's stored resume point.
func TestAdvanceToAChosenEntryStartsAtTheGivenPosition(t *testing.T) {
	f := newCarryFixture(t, false)
	ctx := context.Background()
	q := f.queue()
	early := int64(1000)
	for _, req := range []AdvanceRequest{{Reason: "next", StartPositionMs: &early}, {Reason: "entry", EntryID: q.Queue.Current.EntryID, StartPositionMs: new(int64(-1))}} {
		var field *FieldError
		if _, err := f.service.Advance(ctx, f.caller, q.Queue.ID, q.Queue.Revision, req); !errors.As(err, &field) || field.Path != "startPositionMs" {
			t.Fatalf("%+v: got %v, want a startPositionMs field error", req, err)
		}
	}
	next, err := f.service.Advance(ctx, f.caller, q.Queue.ID, q.Queue.Revision, AdvanceRequest{Reason: "next"})
	if err != nil || next.Queue.Current == nil {
		t.Fatalf("next: %+v %v", next, err)
	}
	back, err := f.service.Advance(ctx, f.caller, q.Queue.ID, next.Queue.Revision, AdvanceRequest{Reason: "previous"})
	if err != nil {
		t.Fatalf("previous: %v", err)
	}
	at := int64(754000)
	jump, err := f.service.Advance(ctx, f.caller, q.Queue.ID, back.Queue.Revision, AdvanceRequest{Reason: "entry", EntryID: next.Queue.Current.EntryID, StartPositionMs: &at})
	if err != nil || jump.Session == nil || jump.Queue.Current == nil || jump.Queue.Current.EntryID != next.Queue.Current.EntryID {
		t.Fatalf("entry jump: %+v %v", jump, err)
	}
	if jump.Session.Presentation.StartPositionMs != at {
		t.Fatalf("entry jump started at %d ms, want %d", jump.Session.Presentation.StartPositionMs, at)
	}
}
