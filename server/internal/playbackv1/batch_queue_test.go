package playbackv1

import (
	"context"
	"errors"
	"testing"
)

func TestBatchQueueImplementsRequiredPlacementAndRevisionContract(t *testing.T) {
	f := newCarryFixture(t, false)
	ctx := context.Background()
	source := func(ids ...string) Selector { return Selector{Items: &ItemList{IDs: ids}} }
	q, err := f.service.CreateQueue(ctx, f.caller, "batch-create-0000001", CreateQueueRequest{Selector: ptrSelector(source(f.items[0].Public, f.items[1].Public))})
	if err != nil {
		t.Fatal(err)
	}
	added, err := f.service.BatchQueue(ctx, f.caller, q.Queue.ID, q.Queue.Revision, BatchQueueRequest{Placement: "next", Source: source(f.items[1].Public, f.items[0].Public)})
	if err != nil {
		t.Fatal(err)
	}
	if added.Queue.Total != 4 {
		t.Fatalf("total %+v", added.Queue)
	}
	if _, err = f.service.BatchQueue(ctx, f.caller, q.Queue.ID, q.Queue.Revision, BatchQueueRequest{Placement: "end", Source: source(f.items[0].Public)}); !errors.Is(err, ErrRevision) {
		t.Fatalf("stale revision %v", err)
	}
	before, err := f.service.Queue(ctx, f.caller, q.Queue.ID)
	if err != nil {
		t.Fatal(err)
	}
	if before.Total != 4 || before.Revision != added.Queue.Revision {
		t.Fatal("refusal mutated queue")
	}
	if _, err = f.service.BatchQueue(ctx, f.caller, q.Queue.ID, before.Revision, BatchQueueRequest{Placement: "after", Source: source(f.items[0].Public)}); err == nil {
		t.Fatal("invalid placement accepted")
	}
	foreign := f.caller
	foreign.Principal.ProfileID = "another-profile"
	if _, err = f.service.BatchQueue(ctx, foreign, q.Queue.ID, before.Revision, BatchQueueRequest{Placement: "end", Source: source(f.items[0].Public)}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign queue %v", err)
	}
}
func ptrSelector(s Selector) *Selector { return &s }
