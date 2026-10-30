package catalog

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// ReadingCountResult is a typed INTERNAL preparation payload. It is not an API
// response, grant, cursor, or client input. Only completed reducer output may be
// put in the shared memory store; public ready responses remain BrowseResult.
type ReadingCountResult struct {
	Library          string
	Profile          string
	Witness          string
	QueryFingerprint string
	EvaluationAt     string
	RawTotal         int
	Filtered         bool
	Threshold        int
	ExpiresAt        int64 `json:"-"`
}

func (r *ReadingCountReduction) Result() (ReadingCountResult, bool) {
	if r == nil || !r.done {
		return ReadingCountResult{}, false
	}
	return ReadingCountResult{Library: r.library, Profile: r.profile, Witness: r.witness, QueryFingerprint: r.queryFingerprint, EvaluationAt: r.evaluationAt, RawTotal: r.count, Filtered: r.filtered, Threshold: r.threshold}, true
}

// BrowseReadingWithPreparedCount composes the original page using a completed,
// typed server-owned memory result. The caller still supplies current authority
// and personal-state admission. This opt-in hook changes no foreground route.
func (s *Service) BrowseReadingWithPreparedCount(viewer Viewer, request BrowseRequest, count ReadingCountResult, resize ...ReadingCountResize) (BrowseResult, error) {
	if count.ExpiresAt > 0 && count.ExpiresAt <= time.Now().Unix() {
		return BrowseResult{}, ErrReadingProjectionChanged
	}
	if request.Library != count.Library || viewer.Profile != count.Profile || !viewer.AllowsLibrary(request.Library) || count.Witness == "" || count.RawTotal < 0 || count.Threshold != browseTotalThreshold || count.Filtered != (request.Query != nil) || count.QueryFingerprint != queryFingerprint(request.Query) {
		return BrowseResult{}, ErrReadingProjectionChanged
	}
	if browseRelative(request.Query) {
		at, err := time.Parse(time.RFC3339Nano, count.EvaluationAt)
		if err != nil {
			return BrowseResult{}, ErrReadingProjectionChanged
		}
		clone := *s
		clone.browseInstant = at
		s = &clone
	} else if count.EvaluationAt != "" {
		return BrowseResult{}, ErrReadingProjectionChanged
	}
	if dbwork.Snapshot(s.Context()) == nil {
		if dbwork.InWriteTx(s.Context()) || s.readTx != nil {
			return BrowseResult{}, fmt.Errorf("prepared Reading page requires a read snapshot")
		}
		var out BrowseResult
		err := dbwork.WithReadSnapshot(s.Context(), s.db, func(ctx context.Context) error {
			var e error
			out, e = s.WithContext(ctx).BrowseReadingWithPreparedCount(viewer, request, count, resize...)
			return e
		})
		return out, err
	}
	witness, err := s.browseExactWitness(request.Library, viewer.Profile, viewer.EffectiveRestrictions())
	if err != nil {
		return BrowseResult{}, err
	}
	if witness != count.Witness {
		return BrowseResult{}, ErrReadingProjectionChanged
	}
	// Query identity and full witness have been proved in this same snapshot;
	// BrowseEntities still checks its normal scope, cursor, expiry and anchor.
	if len(resize) > 0 {
		s = s.WithReadingCountReadMemory(resize[0])
	}
	request.preparedTotal = &count
	return s.BrowseEntities(viewer, request)
}

// BrowsePreparedReadingCountPayload binds content-only cached facts to the
// current viewer and this caller handle's original expiry. No cached grant or
// another session's cursor is reused.
func (s *Service) BrowsePreparedReadingCountPayload(viewer Viewer, payload ReadingBrowseCountPayload, expires time.Time, server string, personal func(*sql.Tx) error, resize ...ReadingCountResize) (BrowseResult, error) {
	if dbwork.Snapshot(s.Context()) == nil {
		var page BrowseResult
		err := dbwork.WithReadSnapshot(s.Context(), s.db, func(ctx context.Context) error {
			var e error
			page, e = s.WithContext(ctx).BrowsePreparedReadingCountPayload(viewer, payload, expires, server, personal, resize...)
			return e
		})
		return page, err
	}
	basis, err := s.ReadingBrowseCountBasis(viewer, payload.Request.Library)
	if err != nil {
		return BrowseResult{}, err
	}
	if payload.Version != 1 || basis != payload.Count.Witness {
		return BrowseResult{}, ErrReadingProjectionChanged
	}
	if payload.Request.EvaluationAt != "" {
		at, e := time.Parse(time.RFC3339Nano, payload.Request.EvaluationAt)
		if e != nil {
			return BrowseResult{}, ErrReadingProjectionChanged
		}
		clone := *s
		clone.browseInstant = at
		s = &clone
	}
	request := payload.Request.BrowseRequest()
	request.ServerID = server
	request.ReadingPersonalAuthorize = personal
	request.Profile = viewer.Profile
	request.ViewerFence = viewer.Fence
	request.Restrictions = viewer.EffectiveRestrictions()
	if payload.Request.Continuation != nil {
		scope, e := s.readingBrowseScope(request.Library)
		if e != nil {
			return BrowseResult{}, e
		}
		pivotID := request.Pivot
		if pivotID == "" {
			pivotID = defaultPivot(scope.kind)
		}
		pivot, ok := pivotForKind(scope.kind, pivotID)
		if !ok {
			return BrowseResult{}, ErrBrowsePivot
		}
		sorts, e := resolveBrowseSorts(pivot, request.Sort)
		if e != nil {
			return BrowseResult{}, e
		}
		revision, e := s.browseRevision(request, pivot, sorts)
		if e != nil {
			return BrowseResult{}, e
		}
		anchor := payload.Request.Continuation
		cursor := cursorValue{Scope: cursorScope{Library: request.Library, Viewer: viewer.Fence, Profile: viewer.Profile, View: "browse:" + pivot.ID, Sort: canonicalSort(sorts), Category: queryFingerprint(request.Query) + "\x00display:", Search: seekPrefix(request.Seek), Limit: pageLimit(request.Limit)}, EvaluationAt: payload.Request.EvaluationAt, Value: anchor.Value, ID: anchor.ID, Keys: anchor.Keys, Fingerprint: revision, Total: payload.Count.RawTotal, TotalPresent: true, Expires: expires.Unix()}
		request.Cursor, e = s.encodeCursor(cursor)
		if e != nil {
			return BrowseResult{}, e
		}
	}
	payload.Count.ExpiresAt = expires.Unix()
	return s.BrowseReadingWithPreparedCount(viewer, request, payload.Count, resize...)
}
