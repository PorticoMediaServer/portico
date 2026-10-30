package catalog

import (
	"context"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// BrowseReadingCarriedTotal validates the complete ordinary Reading cursor path
// before exposing its signed count. It issues no count query, even for legacy
// cursors with absent totals. The bool means a validated total was present;
// otherwise the caller must prepare the exact count and discard the probe page.
// This is an opt-in adapter seam; existing foreground routing is unchanged.
func (s *Service) BrowseReadingCarriedTotal(viewer Viewer, request BrowseRequest) (BrowseResult, bool, error) {
	if request.Cursor == "" {
		return BrowseResult{}, false, nil
	}
	if dbwork.Snapshot(s.Context()) == nil {
		var page BrowseResult
		var present bool
		err := dbwork.WithReadSnapshot(s.Context(), s.db, func(ctx context.Context) error {
			var e error
			page, present, e = s.WithContext(ctx).BrowseReadingCarriedTotal(viewer, request)
			return e
		})
		return page, present, err
	}
	signed, err := s.readSignedBrowseCursor(request.Cursor)
	if err != nil {
		return BrowseResult{}, false, err
	}
	if signed.Expires <= time.Now().Unix() {
		return BrowseResult{}, false, ErrReadingProjectionChanged
	}
	if signed.Total < 0 {
		return BrowseResult{}, false, ErrCursor
	}
	witness, err := s.browseExactWitness(request.Library, viewer.Profile, viewer.EffectiveRestrictions())
	if err != nil {
		return BrowseResult{}, false, err
	}
	decoded, err := s.decodeCursor(request.Cursor, signed.Scope)
	if err != nil {
		return BrowseResult{}, false, err
	}
	present := decoded.TotalPresent || decoded.Total > 0
	count := ReadingCountResult{Library: request.Library, Profile: viewer.Profile, Witness: witness, QueryFingerprint: queryFingerprint(request.Query), RawTotal: decoded.Total, Filtered: request.Query != nil, Threshold: browseTotalThreshold}
	if browseRelative(request.Query) {
		count.EvaluationAt = signed.EvaluationAt
		s, err = s.withBrowseInstant(request.Query, request.Cursor)
		if err != nil {
			return BrowseResult{}, false, err
		}
	}
	// An absent count validates the original cursor without hydrating a
	// speculative adjacent page or any personal state. Only its exact anchor
	// is read; existing signature/scope/fingerprint/readiness checks still run.
	if !present {
		request.Profile = viewer.Profile
		request.ViewerFence = viewer.Fence
		request.Restrictions = viewer.EffectiveRestrictions()
		request.viewer = viewer
		request.preparedTotal = &count
		request.readingValidateOnly = true
		_, _, e := s.browseSelect(request)
		return BrowseResult{}, false, e
	}
	page, err := s.BrowseReadingWithPreparedCount(viewer, request, count)
	if err != nil {
		return BrowseResult{}, false, err
	}
	return page, true, nil
}
