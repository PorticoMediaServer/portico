package read

import (
	"context"
	"errors"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

// rsSearchPublicationSnapshots declares only the existing closed native fence
// recipe's query bound. The governor retains all admission, copy, pool and
// lifetime accounting. This creates no grant or alternate statement executor.
type rsSearchPublicationSnapshots struct{ *governor.Snapshots }

func (s rsSearchPublicationSnapshots) Read(ctx context.Context, req governor.Request, budget time.Duration, read func(*governor.ReadScope) error) (governor.SnapshotResult, error) {
	return s.ReadForecasted(ctx, req, budget, governor.SnapshotForecast{NativeRowBytes: s.NativeRowReservation(), MaxQueries: 4096}, read)
}

// publicationFence uses the shared two-snapshot publication driver. Both views
// repeat the exact native bank/engine/library anchors and every retained domain
// proof, after encoding. No MATCH search, census, new issuer or open SQL callback
// is introduced; all referenced definitions belong in the native Capture.
func (r *RSSearchReader) publicationFence(ctx context.Context, req governor.Request, budget time.Duration, out BrowseOutput, p *RSSearchPreparation) (BrowseReadResult, error) {
	if r.pool == nil || p.stamp == nil || req.Budget.RawRows < 4096 {
		return BrowseReadResult{}, ErrUnqualified
	}
	if e := p.stamp.ValidateRetained(ctx, out, p.stamp); e != nil {
		return BrowseReadResult{}, e
	}
	view := p.input.Views[p.library]
	view.Now = view.Now.Add(time.Since(p.started))
	view.Kind = int64(p.input.Group)
	if view.Kind < 0 || p.input.Group == RSSearchReading {
		view.Kind = 0
	}
	view.Limit = 20
	stamps := make([]*FullSearchReadStamp, 0, len(p.anchors)+1)
	stamps = append(stamps, p.stamp)
	for _, s := range p.anchors {
		if s != nil {
			stamps = append(stamps, s)
		}
	}
	root := func(q browseQuery) ([]any, error) {
		current, disposition, e := nativeReadFence(q, view)
		if e != nil {
			return nil, e
		}
		if disposition != SearchResults || current != p.stamp.fence {
			return nil, ErrStale
		}
		libraries, e := nativeRecheckLibraries(q, view, current, stamps)
		if e != nil {
			return nil, e
		}
		for _, proof := range p.stamp.policies {
			if e = nativeValidateRetainedPolicy(q, view, libraries, proof); e != nil {
				return nil, e
			}
		}
		return nil, nil
	}
	// The common driver checks the unchanged principal/birth/library fence.
	// Kind one selects its default leaf recipe, not a catalogue item grant;
	// every actual retained kind is checked by the closed native proof above.
	common := view
	common.Kind = 1
	common.Limit = 20
	_, cost, e := publishRead(ctx, rsSearchPublicationSnapshots{r.pool}, req, budget, out, common, []readStep[struct{}]{func(context.Context, browseQuery, browseFence, *struct{}) error { return nil }}, root)
	return cost, e
}

// Publish performs one native Read per poll and no delivery while Pending.
// encode must produce bounded owned bytes and join its users before returning.
// consume runs only after a fresh shared publication fence; the preparation's
// physical memory and output/LIVE custody remain held until consume returns.
func (r *RSSearchReader) Publish(ctx context.Context, req governor.Request, budget time.Duration, out BrowseOutput, p *RSSearchPreparation, encode func(context.Context, RSSearchPage) ([]byte, error), consume func(context.Context, []byte) error) (done bool, cost *BrowseReadResult, err error) {
	started := time.Now()
	defer func() {
		if cost != nil {
			cost.Elapsed = time.Since(started)
		}
	}()
	if r == nil || r.pool == nil || ctx == nil || p == nil || p.reader != r || encode == nil || consume == nil || budget <= 0 {
		return false, nil, ErrInput
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || out.live == nil || out.Authority != r.authority || p.owner != req.Owner {
		return false, nil, ErrInput
	}
	deadline := earliest(req.Deadline, out.Request.Deadline, p.lease.Deadline(), time.Now().Add(min(budget, time.Second)))
	operation, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	defer func() {
		if err != nil || done {
			err = errors.Join(err, p.close())
		}
	}()
	var page RSSearchPage
	page, cost, err = r.pollLocked(operation, req, time.Until(deadline), out, p, true)
	if err != nil || page.Pending {
		return false, cost, err
	}
	done = true
	var payload []byte
	payload, err = encode(operation, page)
	if err != nil {
		return
	}
	if int64(len(payload)) > out.Request.Need[governor.MemoryBytes]/2 || int64(len(payload)) > p.lease.PayloadBytes()-rsSearchRetainedBytes(p) {
		err = governor.ErrSnapshotBudget
		return
	}
	// Cancellation cannot suppress a security denial observed by the final
	// shared driver. Its detached validation still uses the original deadline.
	validation, stop := context.WithDeadline(context.WithoutCancel(operation), deadline)
	defer stop()
	var fenceCost BrowseReadResult
	fenceCost, err = r.publicationFence(validation, req, time.Until(deadline), out, p)
	if cost == nil {
		cost = &BrowseReadResult{}
	}
	addSearchReadCost(cost, &fenceCost)
	if err != nil {
		return
	}
	if err = operation.Err(); err != nil {
		return
	}
	if err = out.live.checkContext(operation); err != nil {
		return
	}
	if err = r.authority.CheckPreparation(p.lease); err != nil {
		return
	}
	if err = r.authority.HandoffPreparation(p.lease); err != nil {
		return
	}
	err = consume(operation, payload)
	if err == nil {
		err = errors.Join(operation.Err(), out.live.checkContext(operation))
	}
	return
}
