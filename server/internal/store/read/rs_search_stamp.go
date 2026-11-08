package read

import (
	"context"
	"unsafe"
)

// RD alone issues stamps after its fresh final SQL and physical cleanup. Even
// the first page must have a CURRENT read-bound stamp; an old stamp never grants
// permission. The same comparison also covers native pending continuations.
func rsNativeSearchCustody(ctx context.Context, out BrowseOutput, _ FullSearchInput, current FullSearchPage, _ []FullSearchHit, prior *FullSearchReadStamp) error {
	if current.Stamp == nil {
		return ErrUnqualified
	}
	if prior == nil {
		prior = current.Stamp
	}
	return current.Stamp.ValidateRetained(ctx, out, prior)
}

func rsSearchFenceBytes(f nativeSearchFence) int64 {
	n := int64(unsafe.Sizeof(f))
	p := f.permission
	for _, s := range []string{f.state, f.engineVersion, f.engineSource, f.profile.RecipeVersion, f.profile.EngineVersion, f.profile.UnicodeFingerprint, p.database, p.token, p.account, p.profile, p.membership, p.lifetime, p.library, p.role, p.memberLibraries, p.profileLibraries, p.profileLabels, p.expiresAt, p.horizon, p.root, p.bridgeRoot, p.policyWitness} {
		n += int64(len(s))
	}
	return n
}
func rsSearchHitBytes(h FullSearchHit) int64 {
	return 2 * int64(len(h.Item.Title)+len(h.PersonName)+len(h.SortKey)+len(h.AddedAt)+len(h.permission.witness))
}
func rsSearchRetainedBytes(p *RSSearchPreparation) int64 {
	// The bounded slice's old/new/output overlap is prospectively funded. Native
	// stamp metadata is measured by its issuer, not a second resource ledger.
	n := 4096 + 2*int64(p.input.Capacity)*int64(unsafe.Sizeof(FullSearchHit{}))
	for _, v := range p.input.Views {
		n += 4 * int64(len(v.Database)+len(v.TokenHash))
	}
	n += 4 * int64(len(p.input.Query.Fold()))
	if p.input.Group == 0 {
		n += NativePreviewCandidates * 64
	}
	for _, h := range p.hits {
		n += rsSearchHitBytes(h)
	}
	cursorBytes := func(c *FullSearchCursor) int64 {
		if c == nil {
			return 0
		}
		m := int64(unsafe.Sizeof(*c)) + int64(len(c.query)+len(c.after.lead.Revision))
		if v := c.native; v != nil {
			m += int64(unsafe.Sizeof(*v)) + rsSearchFenceBytes(v.fence) + int64(cap(v.counts))*8 + int64(cap(v.top))*int64(unsafe.Sizeof(nativeRankHit{}))
			if v.expansion != nil {
				m += int64(unsafe.Sizeof(*v.expansion)) + int64(len(v.expansion.source.Revision)) + 2*int64(unsafe.Sizeof(ownerCursor{}))
			}
		}
		return m
	}
	n += cursorBytes(p.after) + cursorBytes(p.cursorBase)
	n += int64(cap(p.streams)) * int64(unsafe.Sizeof(rsSearchStream{}))
	for _, s := range p.streams {
		n += cursorBytes(s.after)
	}
	n += int64(cap(p.anchors)) * int64(unsafe.Sizeof((*FullSearchReadStamp)(nil)))
	for _, anchor := range p.anchors {
		n += 2 * anchor.MetadataBytes()
	}
	n += 2 * p.stamp.MetadataBytes()
	if c := p.input.After; c != nil {
		n += int64(unsafe.Sizeof(*c)) + cursorBytes(c.rank)
		if c.order != nil {
			n += int64(unsafe.Sizeof(*c.order)) + 2*int64(len(c.order.SortKey)+len(c.order.AddedAt))
		}
		if c.anchor != p.stamp {
			n += 2 * c.anchor.MetadataBytes()
		}
	}
	return n
}
