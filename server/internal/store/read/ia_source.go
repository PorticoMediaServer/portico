package read

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

// IASourceInput selects one configured library source for scan preparation.
// The source is always resolved inside the caller's admitted library; no source
// identifier can name another library's configuration. This read is evidence,
// never acceptance, authority or a scan start.
type IASourceInput struct {
	View   BrowseInput
	Source string
}

// IASource is the exact configured source vector: configured and resolved
// native roots, current generation/incarnation and the enabled/health state.
// Values are copied out of one immutable snapshot after the same-snapshot
// octet preflight; the caller must still validate source identity and do IO.
type IASource struct {
	ID, LibraryKey, Kind, Name           string
	ConfiguredRoot, Root, Classification string
	Incarnation, RootIdentity, Health    string
	Generation, Enabled, FollowSymlinks  int64
	IntervalSeconds, IdentityConfirmed   int64
}

// IASourcePage couples the source vector with the library's current root.
// Library is the numeric catalogue identity selected by the caller; LibraryKey
// and LibraryRoot are its exact native values.
type IASourcePage struct {
	Library                 int64
	LibraryKey, LibraryRoot string
	Source                  IASource
}

// MaxSourceRootBytes bounds one retained native root, mirroring the merged
// scan preparation vector. Roots longer than this are refused, not truncated.
const MaxSourceRootBytes = 8192

// ErrSourceRootUnrepresentable is a configuration refusal, never a scan
// poison-name disposition. A root that is not valid UTF-8 cannot be copied into
// a preparation vector or authorize any source IO.
var ErrSourceRootUnrepresentable = errors.New("source root must be valid UTF-8")

// validateSourceRoot mirrors scan.PrepareLibraryRoots for the read side. It
// keeps the exact native path bytes: no cleaning, escaping, Unicode
// normalization or relative rewriting. Errors identify the field but never
// include native path bytes.
func validateSourceRoot(field, root string) error {
	if len(root) == 0 || len(root) > MaxSourceRootBytes {
		return fmt.Errorf("%w: %s has invalid length", ErrUnqualified, field)
	}
	if !utf8.ValidString(root) {
		return fmt.Errorf("%w: %s", ErrSourceRootUnrepresentable, field)
	}
	if strings.IndexByte(root, 0) >= 0 || !filepath.IsAbs(root) || strings.IndexFunc(root, unicode.IsControl) >= 0 {
		return fmt.Errorf("%w: %s must be an absolute path without control characters", ErrUnqualified, field)
	}
	return nil
}

type iaSourceCalls struct {
	n           int
	publication bool
}
type iaSourceSnapshots struct {
	base              browseSnapshots
	scope             *governor.ReadScope
	extra             []iaSourceCalls
	rootCalls         int
	copied, copyLimit int64
}

func (s *iaSourceSnapshots) NativeRowReservation() int64 { return s.base.NativeRowReservation() }
func (s *iaSourceSnapshots) Read(ctx context.Context, r governor.Request, b time.Duration, fn func(*governor.ReadScope) error) (governor.SnapshotResult, error) {
	s.extra = append(s.extra, iaSourceCalls{})
	callback := func(scope *governor.ReadScope) error {
		s.scope = scope
		defer func() { s.scope = nil }()
		return fn(scope)
	}
	// Instrumented test snapshots keep their boundary hooks; production harness
	// composition always supplies the actual pool and this bounded forecast.
	if pool, ok := s.base.(interface {
		ReadForecasted(context.Context, governor.Request, time.Duration, governor.SnapshotForecast, func(*governor.ReadScope) error) (governor.SnapshotResult, error)
	}); ok {
		return pool.ReadForecasted(ctx, r, b, governor.SnapshotForecast{NativeRowBytes: s.NativeRowReservation(), MaxQueries: uint16(min(uint32(4096), r.Budget.RawRows))}, callback)
	}
	return s.base.Read(ctx, r, b, callback)
}
func (s *iaSourceSnapshots) point(name string, columns []int, args ...any) ([]any, error) {
	row, e := s.scope.QueryExactBytes(readStatementID("foreground.ia.source."+name+"_octets"), readStatementID("foreground.ia.source."+name), columns, args...)
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

// IASourcePoint is harness-only and requires ConsumeForeground custody.
// It discloses no SQL, connection, writer or provider capability.
func (r *ForegroundReader) IASourcePoint(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in IASourceInput) (IASourcePage, BrowseReadResult, error) {
	if r == nil {
		return IASourcePage{}, BrowseReadResult{}, ErrInput
	}
	return liveRead(ctx, request, budget, output, r.pool, in.View.Now, func(ctx context.Context, request governor.Request, budget time.Duration, at time.Time) (IASourcePage, BrowseReadResult, error) {
		in.View.Now = at
		return r.iaSourcePoint(ctx, request, budget, output, in)
	})
}
func (r *ForegroundReader) iaSourcePoint(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in IASourceInput) (IASourcePage, BrowseReadResult, error) {
	if r == nil || len(in.Source) == 0 || len(in.Source) > 256 || !utf8.ValidString(in.Source) || in.View.After != nil || in.View.Descending || in.View.KeepArticles {
		return IASourcePage{}, BrowseReadResult{}, ErrInput
	}
	s := &iaSourceSnapshots{base: r.snapshots, copyLimit: output.Request.Need[governor.MemoryBytes] / 4}
	var source []any
	root := func(q browseQuery) ([]any, error) {
		s.rootCalls++
		s.extra[len(s.extra)-1].publication = s.rootCalls%2 == 0
		library, e := s.point("roots", []int{0, 1}, in.View.Library)
		if errors.Is(e, sql.ErrNoRows) {
			return nil, ErrDenied
		}
		if e != nil {
			return nil, e
		}
		libraryKey := library[0].(string)
		if e = validateSourceRoot("library root", library[1].(string)); e != nil {
			return nil, e
		}
		row, e := s.point("point", []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}, in.Source, libraryKey)
		if errors.Is(e, sql.ErrNoRows) {
			return nil, ErrDenied
		}
		if e != nil {
			return nil, e
		}
		if e = validateSourceRoot("configured root", row[4].(string)); e != nil {
			return nil, e
		}
		if e = validateSourceRoot("resolved root", row[5].(string)); e != nil {
			return nil, e
		}
		source = append(append([]any(nil), library...), row...)
		return source, nil
	}
	steps := []readStep[IASourcePage]{func(ctx context.Context, q browseQuery, f browseFence, d *IASourcePage) error {
		if len(source) != 17 {
			return ErrUnqualified
		}
		d.Library = in.View.Library
		d.LibraryKey = source[0].(string)
		d.LibraryRoot = source[1].(string)
		d.Source = IASource{
			ID: source[2].(string), LibraryKey: source[3].(string), Kind: source[4].(string),
			Name: source[5].(string), ConfiguredRoot: source[6].(string), Root: source[7].(string),
			Classification: source[8].(string), Incarnation: source[9].(string),
			RootIdentity: source[10].(string), Health: source[11].(string),
			Generation: source[12].(int64), Enabled: source[13].(int64),
			FollowSymlinks: source[14].(int64), IntervalSeconds: source[15].(int64),
			IdentityConfirmed: source[16].(int64),
		}
		if d.Source.LibraryKey != d.LibraryKey {
			return ErrDenied
		}
		return ctx.Err()
	}}
	page, result, e := publishRead(ctx, s, request, budget, output, in.View, steps, root)
	// Exact point pairs bypass browseQuery, but remain native registered queries.
	// Each attempt runs both pairs in the lookup window and again in publication.
	for _, calls := range s.extra {
		if calls.publication {
			result.PublicationStatements += calls.n
		} else {
			result.LookupStatements += calls.n
		}
	}
	return page, result, e
}
