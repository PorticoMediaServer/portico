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
func rsSearchRetainedBytes(p *RSSearchPreparation) int64 {
	// Prospective DTO slice capacity and temporary transfer overlap, not a new
	// resource ledger. The existing lease and output grant remain sole custody.
	n := 4096 + 2*int64(p.input.Capacity)*int64(unsafe.Sizeof(FullSearchHit{}))
	for _, h := range p.hits {
		n += int64(len(h.Item.Title)+len(h.PersonName)+len(h.SortKey)+len(h.AddedAt)) * 2
	}
	if c := p.after; c != nil {
		n += int64(unsafe.Sizeof(*c) + uintptr(len(c.query)+len(c.after.lead.Revision)))
		if v := c.native; v != nil {
			n += int64(unsafe.Sizeof(*v)) + rsSearchFenceBytes(v.fence) + int64(len(v.counts))*8 + int64(len(v.top))*int64(unsafe.Sizeof(nativeRankHit{}))
			if v.expansion != nil {
				n += int64(unsafe.Sizeof(*v.expansion)) + int64(len(v.expansion.source.Revision)) + 2*int64(unsafe.Sizeof(ownerCursor{}))
			}
		}
	}
	if c := p.cursorBase; c != nil {
		n += int64(unsafe.Sizeof(*c)) + int64(len(c.query)+len(c.after.lead.Revision))
	}
	countStamp := func(s *FullSearchReadStamp) {
		if s == nil {
			return
		}
		n += 2 * (int64(unsafe.Sizeof(*s)) + int64(len(s.query)) + rsSearchFenceBytes(s.fence))
		for _, proof := range s.policies {
			n += int64(unsafe.Sizeof(proof)) + int64(len(proof.witness))
		}
		for _, proof := range s.checked {
			n += int64(unsafe.Sizeof(proof)) + int64(len(proof.witness))
		}
	}
	countStamp(p.stamp)
	for _, s := range p.stamps {
		if s != p.stamp {
			countStamp(s)
		}
	}

	return n
}
