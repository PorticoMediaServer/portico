package playbackv1

import (
	"context"
	"testing"
)

// MU-9: with Shuffle and Repeat all, each lap is a fresh order. The plain
// :advance wrap bumps the lap, as the prepared transition's wrap does, and a
// Previous back over the wrap returns to the lap just played.
func TestShuffleRepeatAllWrapStartsANewLap(t *testing.T) {
	f := newCarryFixture(t, true)
	ctx := context.Background()
	q, err := f.service.CreateQueue(ctx, f.caller, "lap-create-queue-00001", CreateQueueRequest{Segments: []SegmentInput{{Source: Selector{Items: &ItemList{IDs: []string{f.items[0].Public, f.items[1].Public}}}, Order: &Order{Mode: "shuffle"}}}, StartPlayback: &StartPlayback{State: "playing"}})
	if err != nil || q.Queue.Shuffle == nil {
		t.Fatalf("create: %+v %v", q, err)
	}
	q, err = f.service.UpdateQueue(ctx, f.caller, q.Queue.ID, q.Queue.Revision, QueueChange{Repeat: "all"})
	if err != nil {
		t.Fatal(err)
	}
	lap := func(r QueueReply) int64 { return r.Queue.Shuffle.Lap }
	start := lap(q)
	next, err := f.service.Advance(ctx, f.caller, q.Queue.ID, q.Queue.Revision, AdvanceRequest{Reason: "next"})
	if err != nil || lap(next) != start || next.Queue.Current.Position != 1 {
		t.Fatalf("within the lap: %+v %v", next.Queue, err)
	}
	wrapped, err := f.service.Advance(ctx, f.caller, q.Queue.ID, next.Queue.Revision, AdvanceRequest{Reason: "next"})
	if err != nil || wrapped.Queue.Current.Position != 0 || lap(wrapped) != start+1 {
		t.Fatalf("the wrap must start a new lap: %+v %v", wrapped.Queue, err)
	}
	back, err := f.service.Advance(ctx, f.caller, q.Queue.ID, wrapped.Queue.Revision, AdvanceRequest{Reason: "previous"})
	if err != nil || back.Queue.Current.Position != 1 || lap(back) != start {
		t.Fatalf("previous over the wrap returns to the lap just played: %+v %v", back.Queue, err)
	}
}
