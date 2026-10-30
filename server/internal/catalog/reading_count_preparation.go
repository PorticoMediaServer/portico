package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/readprep"
)

const ReadingBrowseCountDomain = "reading-browse-count-v1"

// ReadingBrowseCountRequest is the bounded validated original request retained
// in volatile preparation memory. It contains no authority, callback or SQL.
// ReadingCountContinuation retains only a validated content anchor. Original
// signatures, viewer fences, grants and per-caller expiries never enter Key or
// cached payload; ready composition rebinds them from current authority.
type ReadingCountContinuation struct {
	Value string   `json:"value"`
	ID    string   `json:"id"`
	Keys  []string `json:"keys"`
}

type ReadingBrowseCountRequest struct {
	Library      string                    `json:"library"`
	Pivot        string                    `json:"pivot"`
	Query        *BrowseNode               `json:"query"`
	Sort         []BrowseSortSelection     `json:"sort"`
	Limit        int                       `json:"limit"`
	Cursor       string                    `json:"-"`
	Continuation *ReadingCountContinuation `json:"continuation,omitempty"`
	Range        *BrowseRange              `json:"range,omitempty"`
	Seek         *BrowseSeek               `json:"seek,omitempty"`
	EvaluationAt string                    `json:"evaluationAt"`
}

func (q ReadingBrowseCountRequest) BrowseRequest() BrowseRequest {
	return BrowseRequest{Library: q.Library, Pivot: q.Pivot, Query: q.Query, Sort: q.Sort, Limit: q.Limit, Cursor: q.Cursor, Range: q.Range, Seek: q.Seek}
}

// countOnly keeps membership and the captured instant separate from viewport.
func (q ReadingBrowseCountRequest) countOnly() BrowseRequest {
	return BrowseRequest{Library: q.Library, Pivot: q.Pivot, Query: q.Query, Sort: q.Sort, Limit: q.Limit}
}

type ReadingBrowseCountPayload struct {
	Version int                       `json:"version"`
	Request ReadingBrowseCountRequest `json:"request"`
	Count   ReadingCountResult        `json:"count"`
}

func readingPreparationError(err error) error {
	if errors.Is(err, sql.ErrNoRows) || errors.Is(err, ErrReadingProjectionChanged) || errors.Is(err, ErrStaleContinuation) || errors.Is(err, ErrVisibilityBuilding) || errors.Is(err, identity.ErrUnauthorized) || errors.Is(err, identity.ErrForbidden) {
		return readprep.ErrStale
	}
	if errors.Is(err, ErrReadingCountCapacity) {
		return readprep.ErrCapacity
	}
	return err
}

// ReadingBrowseCountBasis is a snapshot-bound poll/admission check, independent
// of the ordinary non-Reading serving/order continuation revision.
func (s *Service) ReadingBrowseCountBasis(viewer Viewer, library string) (string, error) {
	if !viewer.AllowsLibrary(library) {
		return "", readprep.ErrStale
	}
	if dbwork.Snapshot(s.Context()) == nil {
		var out string
		err := dbwork.WithReadSnapshot(s.Context(), s.db, func(ctx context.Context) error {
			var e error
			out, e = s.WithContext(ctx).ReadingBrowseCountBasis(viewer, library)
			return e
		})
		return out, readingPreparationError(err)
	}
	scope, err := s.readingBrowseScope(library)
	if err != nil {
		return "", readingPreparationError(err)
	}
	if scope == nil {
		return "", ErrBrowsePivot
	}
	basis, err := s.browseExactWitness(library, viewer.Profile, viewer.EffectiveRestrictions())
	return basis, readingPreparationError(err)
}

// SubmitReadingBrowseCount uses only the composition root's shared Store. The
// caller must include domain/library in its private identity and reauthorize
// before/after Submit/Poll/Cancel. Workers bind content policy and full witness,
// never the first caller's session or a cached grant.
func (s *Service) SubmitReadingBrowseCount(caller readprep.Caller, viewer Viewer, request BrowseRequest, until time.Time) (readprep.Handle, error) {
	store := s.ReadPreparations()
	if store == nil {
		return "", readprep.ErrCapacity
	}
	if request.HomeRowDisplay != "" || len(request.Rows) > 0 || len(request.RowSources) > 0 {
		return "", browseIssue("query", "prepared Reading browse requires an ordinary leaf query")
	}
	raw, err := json.Marshal(request.Query)
	if err != nil {
		return "", err
	}
	query, err := ParseBrowseQuery(raw, "query")
	if err != nil {
		return "", err
	}
	service, err := s.withBrowseInstant(query, request.Cursor)
	if err != nil {
		return "", readingPreparationError(err)
	}
	seed := ReadingBrowseCountRequest{Library: strings.Clone(request.Library), Pivot: request.Pivot, Query: query, Sort: append([]BrowseSortSelection{}, request.Sort...), Limit: pageLimit(request.Limit), Cursor: request.Cursor, Range: request.Range, Seek: request.Seek, EvaluationAt: service.relativeBrowseTime(query)}
	var basis string
	err = dbwork.WithReadSnapshot(s.Context(), s.db, func(ctx context.Context) error {
		read := service.WithContext(ctx)
		var e error
		basis, e = read.ReadingBrowseCountBasis(viewer, seed.Library)
		if e != nil {
			return e
		}
		scope, e := read.readingBrowseScope(seed.Library)
		if e != nil {
			return e
		}
		scope.point = true
		pivot := seed.Pivot
		if pivot == "" {
			pivot = defaultPivot(scope.kind)
		}
		p, ok := pivotForKind(scope.kind, pivot)
		if !ok || !p.Browsable || p.Aggregate != "" {
			return ErrBrowsePivot
		}
		sorts, e := resolveBrowseSorts(p, seed.Sort)
		if e != nil {
			return e
		}
		seed.Pivot = p.ID
		seed.Sort = sorts
		if seed.Cursor != "" && seed.Range != nil {
			return browseIssue("range", "a direct range cannot include a cursor")
		}
		if seed.Range != nil {
			if seed.Range.Start < 0 || len(seed.Range.Revision) > 128 || len(seed.Range.AnchorID) > 256 {
				return browseIssue("range", "range identity is invalid")
			}
			if seed.Range.Revision != "" {
				original := seed.BrowseRequest()
				original.Profile = viewer.Profile
				original.ViewerFence = viewer.Fence
				original.Restrictions = viewer.EffectiveRestrictions()
				revision, e := read.browseRevision(original, p, sorts)
				if e != nil {
					return e
				}
				if revision != seed.Range.Revision {
					return ErrReadingProjectionChanged
				}
			}
		}
		if seed.Seek != nil {
			if len(seed.Seek.Prefix) == 0 || len(seed.Seek.Prefix) > 8 {
				return browseIssue("seek.prefix", "seek prefix must be between 1 and 8 characters")
			}
			if len(sorts) == 0 || sorts[0].Field != "title" || sorts[0].Direction != "asc" {
				return browseIssue("seek", "seek requires the first sort to be title ascending")
			}
		}
		compiler := browseCompiler{reading: scope, now: service.recommendationNow(service.browseInstant), profile: viewer.Profile, restrictions: viewer.EffectiveRestrictions(), perTitle: true}
		if _, e = compiler.compile(query); e != nil {
			return e
		}
		if seed.Cursor != "" {
			if _, _, e = read.BrowseReadingCarriedTotal(viewer, seed.BrowseRequest()); e != nil {
				return e
			}
			cursor, e := read.readSignedBrowseCursor(seed.Cursor)
			if e != nil {
				return e
			}
			seed.Continuation = &ReadingCountContinuation{Value: cursor.Value, ID: cursor.ID, Keys: append([]string{}, cursor.Keys...)}
			expires := time.Unix(cursor.Expires, 0)
			if until.IsZero() || expires.Before(until) {
				until = expires
			}
		}
		return nil
	})
	if err != nil {
		return "", readingPreparationError(err)
	}
	seed.Cursor = ""
	if seed.Range != nil {
		seed.Range = &BrowseRange{Start: seed.Range.Start, AnchorID: seed.Range.AnchorID}
	}
	encoded, err := json.Marshal(seed)
	if err != nil {
		return "", err
	}
	policy, err := json.Marshal(viewer.EffectiveRestrictions())
	if err != nil {
		return "", err
	}
	// Content equality is independent of private handle/session identity.
	// Full byte equality of the normalized query/window remains mandatory.
	canonical := string(encoded)
	key := readprep.Key{Namespace: ReadingBrowseCountDomain, Basis: basis, Canonical: canonical}
	factoryBytes := int64(8192 + len(encoded)*3 + len(policy)*2 + len(viewer.Profile) + len(basis))
	if factoryBytes > ReadingCountMaximumBytes {
		return "", readprep.ErrCapacity
	}
	plan := readprep.Plan{InitialBytes: factoryBytes + ReadingCountMaximumBytes}
	seedText := string(encoded)
	policyText := string(policy)
	profile := strings.Clone(viewer.Profile)
	// Capture process-owned dependencies only. The submitting Service may carry
	// a request context, snapshot, recommendation composition or explicit tx.
	process := &Service{db: s.db, storage: s.storage, state: s.state}
	plan.New = func(ctx context.Context, b *readprep.Budget) (readprep.Computation, error) {
		if err := b.Resize(factoryBytes + ReadingCountMaximumBytes); err != nil {
			return nil, err
		}
		var private ReadingBrowseCountRequest
		if err := json.Unmarshal([]byte(seedText), &private); err != nil {
			return nil, readprep.ErrInvalid
		}
		var restrictions identity.ContentRestrictions
		if err := json.Unmarshal([]byte(policyText), &restrictions); err != nil {
			return nil, readprep.ErrInvalid
		}
		content := Viewer{Profile: profile, Libraries: []string{private.Library}, Restrictions: restrictions}
		var err error
		clone := *process.WithContext(ctx)
		if private.EvaluationAt != "" {
			clone.browseInstant, err = time.Parse(time.RFC3339Nano, private.EvaluationAt)
			if err != nil {
				return nil, readprep.ErrInvalid
			}
		}
		reduction, err := clone.NewReadingCountReduction(content, private.countOnly())
		if err != nil {
			return nil, readingPreparationError(err)
		}
		if reduction.Witness() != basis {
			return nil, readprep.ErrStale
		}
		work := &readingCountComputation{service: process, seed: private, reduction: reduction, viewer: content, factoryBytes: factoryBytes, storeMaximum: store.MaxBytes()}
		if err = b.Resize(work.AccountedBytes()); err != nil {
			return nil, err
		}
		return work, nil
	}
	return store.SubmitUntil(s.Context(), caller, key, plan, until)
}

type readingCountComputation struct {
	service      *Service
	seed         ReadingBrowseCountRequest
	reduction    *ReadingCountReduction
	viewer       Viewer
	factoryBytes int64
	storeMaximum int64
	payload      []byte
}

func (w *readingCountComputation) AccountedBytes() int64 {
	return w.factoryBytes + ReadingCountMaximumBytes + int64(cap(w.payload))
}
func (w *readingCountComputation) Step(ctx context.Context, b *readprep.Budget) (readprep.StepResult, error) {
	var err error
	resize := func(peak int64, oversized bool) error {
		// The approved O(B) single-scalar evaluation is transient and uncached;
		// its SQL call returns counters only. Ordinary buffers stay under Budget.
		if oversized || peak > w.storeMaximum-w.AccountedBytes() {
			return ErrReadingCountTransientUnit
		}
		return b.Resize(w.AccountedBytes() + peak)
	}
	q, err := w.service.WithContext(ctx).StepReadingCountReduction(w.viewer, w.reduction, resize)
	if err != nil {
		return readprep.StepResult{}, readingPreparationError(err)
	}

	basis, err := w.service.WithContext(ctx).ReadingBrowseCountBasis(w.viewer, w.seed.Library)
	if err != nil {
		return readprep.StepResult{}, readingPreparationError(err)
	}
	if basis != w.reduction.Witness() {
		return readprep.StepResult{}, readprep.ErrStale
	}
	if !q.Done {
		return readprep.StepResult{}, nil
	}
	count, ok := w.reduction.Result()
	if !ok {
		return readprep.StepResult{}, readprep.ErrInvalid
	}
	// Existing input limits bound this typed seed. Reserve escaping, encoding
	// buffers and the returned payload backing capacity before serialization.
	peak := w.AccountedBytes() + w.factoryBytes*8 + 16384
	if err = b.Resize(peak); err != nil {
		return readprep.StepResult{}, err
	}
	w.payload, err = json.Marshal(ReadingBrowseCountPayload{Version: 1, Request: w.seed, Count: count})
	if err != nil {
		return readprep.StepResult{}, err
	}
	if err = b.Resize(w.AccountedBytes()); err != nil {
		return readprep.StepResult{}, err
	}
	return readprep.StepResult{Done: true, Payload: w.payload}, nil
}
