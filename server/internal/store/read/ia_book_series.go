package read

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/bookseries"
)

// IABookSeriesInput selects one book's accepted local series projection. Entity
// is the kind-8 book subject in the caller's admitted library. This read is
// preparation evidence for RS, never acceptance, authority or provider data.
type IABookSeriesInput struct {
	View   BrowseInput
	Entity int64
}

// IABookSeriesHead is the exact accepted source head. Present is false when the
// book has no accepted projection, which is distinct from an accepted empty
// cohort (Present true, MemberCount zero). Asset, InfoEpoch and PolicyRevision
// are the exact accepted audiobook source witness; Revision is the projection
// revision.
type IABookSeriesHead struct {
	Present                            bool
	Asset, InfoEpoch, PolicyRevision   int64
	Revision, MemberCount, MemberBytes int64
	SourceRevision                     string
	Digest                             [32]byte
}

// IABookSeriesPair is one exact accepted name/position pair. Position may be
// empty; neither value is trimmed, folded or normalized.
type IABookSeriesPair struct {
	Name, Position string
}

// IABookSeriesPage couples the admitted book with its exact source head and
// complete ordered cohort. Exhausted proves the whole cohort was consumed; a
// head whose members do not match its count/bytes/digest is refused, never
// served truncated.
type IABookSeriesPage struct {
	Entity    BrowseItem
	Kind      int64
	Head      IABookSeriesHead
	Pairs     []IABookSeriesPair
	Exhausted bool
	RawRows   int
}

type iaBookSeriesCalls struct {
	n           int
	publication bool
}
type iaBookSeriesSnapshots struct {
	base              browseSnapshots
	scope             *governor.ReadScope
	extra             []iaBookSeriesCalls
	rootCalls         int
	copied, copyLimit int64
}

func (s *iaBookSeriesSnapshots) NativeRowReservation() int64 { return s.base.NativeRowReservation() }
func (s *iaBookSeriesSnapshots) Read(ctx context.Context, r governor.Request, b time.Duration, fn func(*governor.ReadScope) error) (governor.SnapshotResult, error) {
	s.extra = append(s.extra, iaBookSeriesCalls{})
	callback := func(scope *governor.ReadScope) error {
		s.scope = scope
		defer func() { s.scope = nil }()
		return fn(scope)
	}
	if pool, ok := s.base.(interface {
		ReadForecasted(context.Context, governor.Request, time.Duration, governor.SnapshotForecast, func(*governor.ReadScope) error) (governor.SnapshotResult, error)
	}); ok {
		return pool.ReadForecasted(ctx, r, b, governor.SnapshotForecast{NativeRowBytes: s.NativeRowReservation(), MaxQueries: uint16(min(uint32(4096), r.Budget.RawRows))}, callback)
	}
	return s.base.Read(ctx, r, b, callback)
}

// point is the exact two-query byte preflight: matching octet lengths first,
// then the payload with identical bindings in the same immutable snapshot.
func (s *iaBookSeriesSnapshots) point(name string, columns []int, args ...any) ([]any, error) {
	row, e := s.scope.QueryExactBytes(readStatementID("foreground.ia.book_series."+name+"_octets"), readStatementID("foreground.ia.book_series."+name), columns, args...)
	s.extra[len(s.extra)-1].n += 2
	if e != nil {
		return nil, e
	}
	s.copied += 64 + int64(len(row))*32
	for _, v := range row {
		switch v := v.(type) {
		case string:
			s.copied += 2 * int64(len(v))
		case []byte:
			s.copied += 3 * int64(len(v))
		}
	}
	if s.copied > s.copyLimit {
		return nil, governor.ErrSnapshotBudget
	}
	return row, nil
}

// IABookSeries is harness-only and requires ConsumeForeground custody. It
// discloses no SQL, connection, writer or provider capability.
func (r *ForegroundReader) IABookSeries(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in IABookSeriesInput) (IABookSeriesPage, BrowseReadResult, error) {
	if r == nil {
		return IABookSeriesPage{}, BrowseReadResult{}, ErrInput
	}
	return liveRead(ctx, request, budget, output, r.pool, in.View.Now, func(ctx context.Context, request governor.Request, budget time.Duration, at time.Time) (IABookSeriesPage, BrowseReadResult, error) {
		in.View.Now = at
		return r.iaBookSeries(ctx, request, budget, output, in)
	})
}

func (r *ForegroundReader) iaBookSeries(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in IABookSeriesInput) (IABookSeriesPage, BrowseReadResult, error) {
	if r == nil || in.Entity <= 0 || in.View.Library <= 0 || in.View.Kind != 8 || in.View.After != nil || in.View.Descending || in.View.KeepArticles {
		return IABookSeriesPage{}, BrowseReadResult{}, ErrInput
	}
	s := &iaBookSeriesSnapshots{base: r.snapshots, copyLimit: output.Request.Need[governor.MemoryBytes] / 4}
	var source []any
	root := func(q browseQuery) ([]any, error) {
		s.rootCalls++
		s.extra[len(s.extra)-1].publication = s.rootCalls%2 == 0
		row, e := s.point("entity", []int{1, 2, 3, 8}, in.Entity, in.View.Library)
		if errors.Is(e, sql.ErrNoRows) {
			return nil, ErrDenied
		}
		if e != nil {
			return nil, e
		}
		source = row
		return row, nil
	}
	steps := []readStep[IABookSeriesPage]{func(ctx context.Context, q browseQuery, f browseFence, d *IABookSeriesPage) error {
		if len(source) != 9 || !admittedItem(source, f) || source[7].(int64) != 8 {
			return ErrDenied
		}
		item, e := itemValue(source)
		if e != nil {
			return ErrUnqualified
		}
		d.Entity, d.Kind = item, 8
		row, e := s.point("head", []int{4, 9}, in.Entity, in.View.Library)
		if errors.Is(e, sql.ErrNoRows) {
			// No accepted head is not an accepted empty cohort.
			d.Exhausted = true
			return ctx.Err()
		}
		if e != nil {
			return e
		}
		if len(row) != 10 || row[0].(int64) != in.View.Library || row[1].(int64) != 8 {
			return ErrUnqualified
		}
		asset, epoch, policy := row[2].(int64), row[3].(int64), row[5].(int64)
		revision, count, bytes := row[6].(int64), row[7].(int64), row[8].(int64)
		sourceRevision := row[4].(string)
		digest := row[9].([]byte)
		if asset <= 0 || epoch <= 0 || sourceRevision == "" || policy <= 0 || revision <= 0 ||
			count < 0 || count > maxIABookSeriesMembers || bytes < 0 || len(digest) != 32 {
			return ErrUnqualified
		}
		d.Head = IABookSeriesHead{Present: true, Asset: asset, InfoEpoch: epoch, PolicyRevision: policy,
			Revision: revision, MemberCount: count, MemberBytes: bytes, SourceRevision: sourceRevision}
		copy(d.Head.Digest[:], digest)
		if count == 0 {
			if err := iaBookSeriesVerify(d.Head, nil); err != nil {
				return err
			}
			d.Exhausted = true
			return ctx.Err()
		}
		rows, e := q("foreground.ia.book_series.members", in.Entity, count+1)
		if e != nil {
			return e
		}
		if int64(len(rows)) != count {
			return ErrUnqualified
		}
		for i, key := range rows {
			if len(key) != 1 || key[0].(int64) != int64(i) {
				return ErrUnqualified
			}
		}
		d.RawRows = len(rows)
		pairs := make([]bookseries.Pair, 0, count)
		for i := int64(0); i < count; i++ {
			row, e := s.point("member", []int{1, 2}, in.Entity, i)
			if errors.Is(e, sql.ErrNoRows) {
				return ErrUnqualified
			}
			if e != nil {
				return e
			}
			if len(row) != 3 || row[0].(int64) != i {
				return ErrUnqualified
			}
			pairs = append(pairs, bookseries.Pair{Name: row[1].(string), Position: row[2].(string)})
		}
		for _, pair := range pairs {
			d.Pairs = append(d.Pairs, IABookSeriesPair{Name: pair.Name, Position: pair.Position})
		}
		if err := iaBookSeriesVerify(d.Head, d.Pairs); err != nil {
			return err
		}
		d.Exhausted = true
		return ctx.Err()
	}}
	page, result, e := publishRead(ctx, s, request, budget, output, in.View, steps, root)
	// Exact point pairs bypass browseQuery but remain native registered queries.
	for _, calls := range s.extra {
		if calls.publication {
			result.PublicationStatements += calls.n
		} else {
			result.LookupStatements += calls.n
		}
	}
	return page, result, e
}

// iaBookSeriesVerify proves the served cohort is the exact accepted one: the
// head is present, the ordered pair count matches, and the canonical bytes and
// digest of the decoded pairs match the head. An accepted empty cohort must
// carry the canonical empty digest. A tampered or truncated projection is
// refused, never served.
func iaBookSeriesVerify(head IABookSeriesHead, pairs []IABookSeriesPair) error {
	if !head.Present || head.Revision <= 0 || head.MemberCount != int64(len(pairs)) || head.MemberBytes < 0 {
		return ErrUnqualified
	}
	converted := make([]bookseries.Pair, len(pairs))
	for i, pair := range pairs {
		converted[i] = bookseries.Pair{Name: pair.Name, Position: pair.Position}
	}
	count, retained, digest, err := bookseries.Cohort(converted)
	if err != nil || count != head.MemberCount || retained != head.MemberBytes || digest != head.Digest {
		return ErrUnqualified
	}
	return nil
}
