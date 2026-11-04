package governor

import (
	"context"
	"errors"
	"testing"
	"time"
)

type cursor struct {
	After int64
	Phase uint8
}
type fakeJobs struct {
	gov     *Fake
	row     Job[cursor]
	fence   SourceFence
	receipt string
	effects int
}

var _ Jobs[cursor] = (*fakeJobs)(nil)

func (s *fakeJobs) Ready(_ context.Context, p *Grant, q ReadyQuery) ([]Ready, error) {
	if !s.gov.Valid(p) || q.Limit == 0 {
		return nil, errors.New("invalid read")
	}
	if s.row.NextAttempt.After(s.gov.Clock.Now()) {
		return nil, nil
	}
	return []Ready{{Key: s.row.Key, Revision: s.row.Revision, NotBefore: s.row.NextAttempt, Fence: s.fence}}, nil
}
func (s *fakeJobs) writer(p *Grant) bool {
	if !s.gov.Valid(p) {
		return false
	}
	h := s.gov.Live[p.id]
	return h.request.Writer && h.request.Key == s.row.Key
}
func (s *fakeJobs) Claim(_ context.Context, p *Grant, r Ready, o OwnerID, ttl time.Duration) (Job[cursor], error) {
	if !s.writer(p) || r.Key != s.row.Key || r.Revision != s.row.Revision || r.Fence != s.fence || s.row.NextAttempt.After(s.gov.Clock.Now()) || s.row.Lease.ExpiresAt.After(s.gov.Clock.Now()) {
		return Job[cursor]{}, errors.New("stale claim")
	}
	s.row.Attempts++
	s.row.Revision++
	s.row.Lease = Lease{Key: s.row.Key, Generation: s.row.Lease.Generation + 1, Owner: o, ExpiresAt: s.gov.Clock.Now().Add(ttl)}
	return s.row, nil
}
func (s *fakeJobs) valid(l Lease) bool {
	return l == s.row.Lease && l.ExpiresAt.After(s.gov.Clock.Now())
}
func (s *fakeJobs) Renew(_ context.Context, p *Grant, l Lease, ttl time.Duration) (Lease, error) {
	if !s.writer(p) || !s.valid(l) {
		return Lease{}, errors.New("stale renewal")
	}
	s.row.Lease.ExpiresAt = s.gov.Clock.Now().Add(ttl)
	return s.row.Lease, nil
}
func (s *fakeJobs) Advance(_ context.Context, p *Grant, l Lease, a Advance[cursor]) (Job[cursor], error) {
	if !s.writer(p) || !s.valid(l) || a.ExpectedRevision != s.row.Revision || a.Fence != s.fence || a.Expected != s.row.Cursor {
		return Job[cursor]{}, errors.New("stale advance")
	}
	// One atomic fake transaction; no wake/response callback owns accepted state.
	s.row.Cursor = a.Next
	s.row.Revision++
	s.row.NextAttempt = a.NextAttempt
	if a.Outcome == Applied {
		s.receipt = string(a.Receipt)
		s.effects++
		s.row.Lease.ExpiresAt = time.UnixMilli(0)
	}
	return s.row, nil
}
func TestLateSameCursorSourceChange(t *testing.T) {
	c := &Clock{}
	g := NewFake(c)
	ctx := context.Background()
	key := JobKey{Domain: 41, ID: "6:source2:17"}
	s := &fakeJobs{gov: g, row: Job[cursor]{Key: key, Revision: 1}, fence: SourceFence{SourceID: "s", Incarnation: "01-hex", Revision: "opaque-8"}}
	r := request(c, ScanMinimum, Amounts{})
	r.Writer = true
	r.MaxHold = time.Millisecond
	r.Key = key
	p := granted(t, g, r)
	ready := Ready{Key: key, Revision: 1, Fence: s.fence}
	j, e := s.Claim(ctx, p, ready, 7, time.Second)
	require(t, e == nil, "claim")
	// Invalidation changes revision but intentionally leaves cursor untouched.
	s.row.Revision++
	s.fence.Revision = "opaque-9"
	a := Advance[cursor]{ExpectedRevision: j.Revision, Fence: ready.Fence, Expected: j.Cursor, Next: cursor{After: 1}, Outcome: Applied, Receipt: "accepted-1"}
	_, e = s.Advance(ctx, p, j.Lease, a)
	require(t, e != nil && s.receipt == "" && s.effects == 0 && s.row.Cursor == j.Cursor, "R-02 stale same-cursor source applied")
	// Even updating queue revision alone cannot mask a changed source witness.
	a.ExpectedRevision = s.row.Revision
	_, e = s.Advance(ctx, p, j.Lease, a)
	require(t, e != nil && s.effects == 0, "stale source fence accepted")
	a.Fence = s.fence
	_, e = s.Advance(ctx, p, j.Lease, a)
	require(t, e == nil && s.effects == 1 && s.receipt == "accepted-1", "valid acceptance")
	_, e = s.Advance(ctx, p, j.Lease, a)
	require(t, e != nil && s.effects == 1, "response-loss replay duplicated commit")
	_ = g.Retired(p)
}
func TestLeaseReclaimAndNextAttempt(t *testing.T) {
	c := &Clock{}
	g := NewFake(c)
	ctx := context.Background()
	key := JobKey{1, "string-primary-key"}
	s := &fakeJobs{gov: g, row: Job[cursor]{Key: key, Revision: 1, NextAttempt: time.UnixMilli(20)}}
	r := request(c, ScanMinimum, Amounts{})
	r.Writer = true
	r.MaxHold = time.Millisecond
	r.Key = key
	p := granted(t, g, r)
	ready := Ready{Key: key, Revision: 1}
	_, e := s.Claim(ctx, p, ready, 1, time.Millisecond)
	require(t, e != nil && s.row.Attempts == 0, "early attempt consumed")
	c.MS = 20
	j, e := s.Claim(ctx, p, ready, 1, time.Millisecond)
	require(t, e == nil, "eligible claim")
	c.MS = 22
	ready.Revision = s.row.Revision
	k, e := s.Claim(ctx, p, ready, 2, time.Second)
	require(t, e == nil && k.Attempts == 2, "expired lease reclaim")
	_, e = s.Renew(ctx, p, j.Lease, time.Second)
	require(t, e != nil, "old owner renewed successor")
	_ = g.Retired(p)
}

// Byte vectors are checked before archive extraction, buffer allocation or decode.
// RGBA64 worst-case planes; explicit bounded codec scratch/output, not heap alone.
func mediaBytes(input, width, height, outW, outH, scratch, encoded uint64) (uint64, bool) {
	const max = ^uint64(0)
	mul := func(a, b uint64) (uint64, bool) {
		if b != 0 && a > max/b {
			return 0, false
		}
		return a * b, true
	}
	pixels, ok := mul(width, height)
	if !ok {
		return 0, false
	}
	src, ok := mul(pixels, 8)
	if !ok {
		return 0, false
	}
	pixels, ok = mul(outW, outH)
	if !ok {
		return 0, false
	}
	dst, ok := mul(pixels, 8)
	if !ok {
		return 0, false
	}
	total := input
	for _, v := range []uint64{src, dst, scratch, encoded} {
		if v > max-total {
			return 0, false
		}
		total += v
	}
	return total, true
}
func TestMediaByteBudget(t *testing.T) {
	for _, v := range [][2]uint64{{^uint64(0), 2}, {1 << 63, 8}} {
		_, ok := mediaBytes(32, v[0], v[1], 32, 32, 0, 0)
		require(t, !ok, "dimension overflow accepted")
	}
	_, ok := mediaBytes(^uint64(0), 1, 1, 1, 1, 1, 1)
	require(t, !ok, "sum overflow accepted")
	small, ok := mediaBytes(1<<20, 1000, 1000, 320, 320, 4<<20, 1<<20)
	require(t, ok && small < 32<<20, "small page estimate")
	big, ok := mediaBytes(32<<20, 8000, 5000, 1000, 1000, 16<<20, 8<<20)
	require(t, ok && big > 128<<20, "40MP resident decode wrongly fits 128MiB")
	c := &Clock{}
	g := NewFake(c)
	g.Floors[Foreground][MemoryBytes] = 128 << 20
	// Seven independent small readers progress; large decode promptly refused.
	ps := []*Grant{}
	for i := 0; i < 7; i++ {
		ps = append(ps, granted(t, g, request(c, Foreground, Amounts{0, 0, int64(small)})))
	}
	d := g.Try(request(c, Foreground, Amounts{0, 0, int64(big)}))
	require(t, d.Refusal != nil, "oversized page stalled others")
	for _, p := range ps {
		before := g.Used
		g.Cancel(p)
		require(t, g.Used == before, "slow body released bytes")
		_ = g.Retired(p)
	}
	require(t, g.Check() == nil, "media byte leak")
}
