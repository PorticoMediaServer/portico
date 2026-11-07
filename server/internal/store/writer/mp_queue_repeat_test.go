package writer

import (
	"errors"
	"math"
	"strings"
	"testing"
)

func TestMPQueueRepeatRequestBounds(t *testing.T) {
	valid := mpQueueRepeatRequest{queue: "queue", account: "a", profile: "p", device: "d", expectedRevision: 3, expectedCustody: 7, repeat: "all"}
	if err := valid.validate(); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*mpQueueRepeatRequest){
		func(q *mpQueueRepeatRequest) { q.queue = "" },
		func(q *mpQueueRepeatRequest) { q.device = strings.Repeat("d", 129) },
		func(q *mpQueueRepeatRequest) { q.account = "\xff" },
		func(q *mpQueueRepeatRequest) { q.profile = "p\x00" },
		func(q *mpQueueRepeatRequest) { q.expectedRevision = math.MaxInt64 },
		func(q *mpQueueRepeatRequest) { q.expectedCustody = -1 },
		func(q *mpQueueRepeatRequest) { q.repeat = "random" },
	} {
		q := valid
		change(&q)
		if !errors.Is(q.validate(), ErrInvalid) {
			t.Fatal("invalid request accepted", q)
		}
	}
	out, err := applyMPQueueRepeat(t.Context(), nil, mpQueueRepeatInvocation{request: valid, appliedMS: 3000})
	if !errors.Is(err, ErrInvalid) || out != (mpQueueRepeatEffect{}) {
		t.Fatal("nil turn released output", out, err)
	}
}

func TestMPQueuePreparedRetirementClosedPurposes(t *testing.T) {
	for _, kind := range []CommandID{700, 710, 720, 728, 740, 750, 751, 752, 758, 759, 764, 770, 780, 786} {
		if mpQueuePreparedRetirementPurpose(kind) {
			t.Fatal("borrowed purpose", kind)
		}
	}
	for _, kind := range []CommandID{753, 754, 755, 756, 757, 760, 761, 762, 763} {
		if !mpQueuePreparedRetirementPurpose(kind) {
			t.Fatal("missing parent", kind)
		}
	}
	if got, err := composeMPQueuePreparedRetire(t.Context(), nil, mpQueuePreparedRetirement{}); !errors.Is(err, ErrInvalid) || got != (mpSessionRetireProjection{}) {
		t.Fatal(got, err)
	}
}
