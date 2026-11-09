package read

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// IACoverageInput selects one inventory run directory for coverage preparation.
// Run is the job id, Source is the configured library_sources id and Directory is
// the exact native relative path; all are resolved inside the caller's admitted
// library. This read is evidence, never acceptance, authority or a scan start.
type IACoverageInput struct {
	View                   BrowseInput
	Run, Source, Directory string
}

// IACoverageDirectory is the exact durable directory row. State is the native
// pending/done/blocked frontier state; Identity and Revision are never
// substituted for one another and may be empty on an unresolved row.
type IACoverageDirectory struct {
	Path, Identity, Revision, Cursor, State string
	Pages, Skipped                          int64
}

// IACoveragePage couples the admitted source with one exact directory row and the
// bounded descendant frontier. Pending and Blocked are partial/unreadable
// evidence; Protected is an unrepresentable refusal; Skipped is a listing that
// omitted entries. Sealed is true only for a done row with no descendant pending,
// blocked, protected or skipped directory: an empty or incomplete window is never
// an absence proof.
type IACoveragePage struct {
	Library    int64
	LibraryKey string
	Run        string
	Source     string
	Directory  IACoverageDirectory
	Pending    bool
	Blocked    bool
	Protected  bool
	Skipped    bool
	Sealed     bool
	RawRows    int
}

const (
	maxIACoverageRunBytes       = 512
	maxIACoverageSourceBytes    = 256
	maxIACoverageDirectoryBytes = 16384
	maxIACoverageKeyBytes       = 512
)

type iaCoverageCalls struct {
	n           int
	publication bool
}
type iaCoverageSnapshots struct {
	base              browseSnapshots
	scope             *governor.ReadScope
	extra             []iaCoverageCalls
	rootCalls         int
	copied, copyLimit int64
}

func (s *iaCoverageSnapshots) NativeRowReservation() int64 {
	return s.base.NativeRowReservation()
}
func (s *iaCoverageSnapshots) Read(ctx context.Context, r governor.Request, b time.Duration, fn func(*governor.ReadScope) error) (governor.SnapshotResult, error) {
	s.extra = append(s.extra, iaCoverageCalls{})
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

// point is the exact two-query byte preflight: matching octet lengths first,
// then the payload with identical bindings in the same immutable snapshot.
func (s *iaCoverageSnapshots) point(name string, columns []int, args ...any) ([]any, error) {
	row, e := s.scope.QueryExactBytes(readStatementID("foreground.ia.coverage."+name+"_octets"),
		readStatementID("foreground.ia.coverage."+name), columns, args...)
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

// exists is one bounded existence point on an exact indexed frontier.
func (s *iaCoverageSnapshots) exists(name string, args ...any) (bool, error) {
	rows, e := s.scope.Query(readStatementID("foreground.ia.coverage."+name), args...)
	s.extra[len(s.extra)-1].n++
	if e != nil {
		return false, e
	}
	s.copied += 32
	if s.copied > s.copyLimit {
		return false, governor.ErrSnapshotBudget
	}
	return len(rows) > 0, nil
}

// IACoverageDirectoryState is harness-only and requires ConsumeForeground
// custody. It discloses no SQL, connection, writer or provider capability.
func (r *ForegroundReader) IACoverageDirectoryState(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in IACoverageInput) (IACoveragePage, BrowseReadResult, error) {
	if r == nil {
		return IACoveragePage{}, BrowseReadResult{}, ErrInput
	}
	return liveRead(ctx, request, budget, output, r.pool, in.View.Now, func(ctx context.Context, request governor.Request, budget time.Duration, at time.Time) (IACoveragePage, BrowseReadResult, error) {
		in.View.Now = at
		return r.iaCoverageDirectoryState(ctx, request, budget, output, in)
	})
}

func (r *ForegroundReader) iaCoverageDirectoryState(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in IACoverageInput) (IACoveragePage, BrowseReadResult, error) {
	if r == nil || in.View.Library <= 0 || !iaCoverageReadText(in.Run, maxIACoverageRunBytes) ||
		!iaCoverageReadText(in.Source, maxIACoverageSourceBytes) || !iaCoverageReadText(in.Directory, maxIACoverageDirectoryBytes) ||
		!iaCoverageReadPath(in.Directory) ||
		in.View.After != nil || in.View.Descending || in.View.KeepArticles {
		return IACoveragePage{}, BrowseReadResult{}, ErrInput
	}
	s := &iaCoverageSnapshots{base: r.snapshots, copyLimit: output.Request.Need[governor.MemoryBytes] / 4}
	var source []any
	root := func(q browseQuery) ([]any, error) {
		s.rootCalls++
		s.extra[len(s.extra)-1].publication = s.rootCalls%2 == 0
		library, e := s.point("library", []int{0}, in.View.Library)
		if errors.Is(e, sql.ErrNoRows) {
			return nil, ErrDenied
		}
		if e != nil {
			return nil, e
		}
		key := library[0].(string)
		if !iaCoverageReadText(key, maxIACoverageKeyBytes) {
			return nil, ErrUnqualified
		}
		row, e := s.point("source", []int{0}, in.Source, key)
		if errors.Is(e, sql.ErrNoRows) {
			return nil, ErrDenied
		}
		if e != nil {
			return nil, e
		}
		if row[0].(string) != in.Source {
			return nil, ErrDenied
		}
		source = append(append([]any(nil), library...), row...)
		return source, nil
	}
	steps := []readStep[IACoveragePage]{func(ctx context.Context, q browseQuery, f browseFence, d *IACoveragePage) error {
		if len(source) != 2 {
			return ErrUnqualified
		}
		d.Library = in.View.Library
		d.LibraryKey = source[0].(string)
		d.Run, d.Source = in.Run, in.Source
		row, e := s.point("directory", []int{0, 1, 2, 3, 4}, in.Run, in.Directory)
		if errors.Is(e, sql.ErrNoRows) {
			return ErrDenied
		}
		if e != nil {
			return e
		}
		if len(row) != 7 || row[0].(string) != in.Directory {
			return ErrDenied
		}
		directory := IACoverageDirectory{
			Path: row[0].(string), Identity: row[1].(string), Revision: row[2].(string),
			Cursor: row[3].(string), State: row[4].(string), Pages: row[5].(int64), Skipped: row[6].(int64),
		}
		if !iaCoverageReadPath(directory.Path) || !iaCoverageReadTextAllowEmpty(directory.Identity, maxIACoverageKeyBytes) ||
			!iaCoverageReadTextAllowEmpty(directory.Revision, maxIACoverageKeyBytes) ||
			!iaCoverageReadTextAllowEmpty(directory.Cursor, maxIACoverageKeyBytes) ||
			(directory.State != "pending" && directory.State != "done" && directory.State != "blocked") ||
			directory.Pages < 0 || directory.Skipped < 0 {
			return ErrUnqualified
		}
		d.Directory = directory
		low, high := iaCoverageReadBounds(directory.Path)
		for _, check := range []struct {
			name string
			set  *bool
		}{
			{"pending", &d.Pending},
			{"blocked", &d.Blocked},
			{"protected", &d.Protected},
		} {
			found, e := s.exists(check.name, in.Run, low, high)
			if e != nil {
				return e
			}
			*check.set = found
		}
		// The selected directory is checked separately at its exact point; the
		// bounded subtree window excludes it for non-root paths.
		skippedSubtree, e := s.exists("skipped", in.Run, low, high)
		if e != nil {
			return e
		}
		d.Skipped = directory.Skipped != 0 || skippedSubtree
		// A done row alone never proves absence. Any skip in the exact directory or
		// its sealed subtree is incomplete coverage, so it can never seal.
		d.Sealed = directory.State == "done" && !d.Pending && !d.Blocked && !d.Protected && !d.Skipped
		return ctx.Err()
	}}
	page, result, e := publishRead(ctx, s, request, budget, output, in.View, steps, root)
	// Exact point pairs bypass browseQuery, but remain native registered queries.
	for _, calls := range s.extra {
		if calls.publication {
			result.PublicationStatements += calls.n
		} else {
			result.LookupStatements += calls.n
		}
	}
	return page, result, e
}

func iaCoverageReadText(value string, limit int) bool {
	return len(value) > 0 && iaCoverageReadTextAllowEmpty(value, limit)
}

// iaCoverageReadPath is the native relative path shape the writer applies,
// including the root "." that inventory_directories stores for the source root.
func iaCoverageReadPath(value string) bool {
	if value == "." {
		return true
	}
	return inventorySourcePath(value)
}

func iaCoverageReadTextAllowEmpty(value string, limit int) bool {
	if len(value) > limit || !utf8.ValidString(value) || strings.IndexByte(value, 0) >= 0 {
		return false
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

// Half-open native path bounds select only descendants of exactly this
// directory. UTF-8 cannot produce 0xff, so the root window stays complete without
// a LIKE pattern or a temporary sort.
func iaCoverageReadBounds(directory string) (low, high string) {
	if directory == "." {
		return "", "\xff"
	}
	return directory + "/", directory + "0"
}

// SQL shapes for the coverage preparation. All targets are embedded inventory
// relations: no draft group is substituted for this read.
const iaCoverageLibrarySQL = `SELECT library_id FROM catalog_libraries WHERE id=?`
const iaCoverageLibraryOctetsSQL = `SELECT octet_length(library_id) FROM catalog_libraries WHERE id=?`
const iaCoverageSourceSQL = `SELECT id FROM library_sources WHERE id=? AND library_id=?`
const iaCoverageSourceOctetsSQL = `SELECT octet_length(id) FROM library_sources WHERE id=? AND library_id=?`
const iaCoverageDirectorySQL = `SELECT relative_path,identity,revision,cursor,state,pages,skipped FROM inventory_directories WHERE job_id=? AND relative_path=?`
const iaCoverageDirectoryOctetsSQL = `SELECT octet_length(relative_path),octet_length(identity),octet_length(revision),octet_length(cursor),octet_length(state) FROM inventory_directories WHERE job_id=? AND relative_path=?`
const iaCoveragePendingSQL = `SELECT 1 FROM inventory_directories INDEXED BY inventory_directories_pending WHERE job_id=? AND state='pending' AND relative_path>=? AND relative_path<? LIMIT 1`
const iaCoverageBlockedSQL = `SELECT 1 FROM inventory_directories INDEXED BY inventory_directories_pending WHERE job_id=? AND state='blocked' AND relative_path>=? AND relative_path<? LIMIT 1`
const iaCoverageProtectedSQL = `SELECT 1 FROM inventory_scan_issues INDEXED BY inventory_scan_issues_protected WHERE job_id=? AND protect=1 AND relative_path>=? AND relative_path<? LIMIT 1`
const iaCoverageSkippedSQL = `SELECT 1 FROM inventory_directories INDEXED BY inventory_directories_skipped WHERE job_id=? AND skipped>0 AND relative_path>=? AND relative_path<? LIMIT 1`

// IACoverageHarnessDefinitions is inert harness composition, not registration or
// authorization. Capture these exact definitions on the actual reader first.
func IACoverageHarnessDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	s := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	add := func(name, sql string, params []stmt.Param, present, empty []any, p stmt.Plan) stmt.Definition {
		return stmt.Definition{ID: readStatementID("foreground.ia.coverage." + name), SQL: sql, Params: params, Kind: stmt.Read,
			Owner: "store/read/ia_coverage", CallSites: []string{"store/read/ia_coverage.go:IACoverageDirectoryState"},
			Cohorts: []stmt.Cohort{{Name: "present", Bindings: present}, {Name: "empty", Bindings: empty}}, Plan: p}
	}
	libraryPlan := stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_libraries"}, Seeks: []string{"rowid=?"}}
	sourcePlan := stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_library_sources_1"}, Seeks: []string{"id=?"}}
	directoryPlan := stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_inventory_directories_1"}, Seeks: []string{"job_id=? AND relative_path=?"}}
	pendingPlan := stmt.Plan{Hot: true, Indexes: []string{"inventory_directories_pending"}, Seeks: []string{"job_id=? AND state=? AND relative_path>? AND relative_path<?"}}
	protectedPlan := stmt.Plan{Hot: true, Indexes: []string{"inventory_scan_issues_protected"}, Seeks: []string{"job_id=? AND relative_path>? AND relative_path<?"}}
	skippedPlan := stmt.Plan{Hot: true, Indexes: []string{"inventory_directories_skipped"}, Seeks: []string{"job_id=? AND relative_path>? AND relative_path<?"}}
	return []stmt.Definition{
		add("library_octets", iaCoverageLibraryOctetsSQL, []stmt.Param{i("library")}, []any{int64(1)}, []any{int64(9999999)}, libraryPlan),
		add("library", iaCoverageLibrarySQL, []stmt.Param{i("library")}, []any{int64(1)}, []any{int64(9999999)}, libraryPlan),
		add("source_octets", iaCoverageSourceOctetsSQL, []stmt.Param{s("source"), s("library")}, []any{"source-1", "movies"}, []any{"missing", "movies"}, sourcePlan),
		add("source", iaCoverageSourceSQL, []stmt.Param{s("source"), s("library")}, []any{"source-1", "movies"}, []any{"missing", "movies"}, sourcePlan),
		add("directory_octets", iaCoverageDirectoryOctetsSQL, []stmt.Param{s("run"), s("directory")}, []any{"run-1", "Coverage"}, []any{"run-1", "Missing"}, directoryPlan),
		add("directory", iaCoverageDirectorySQL, []stmt.Param{s("run"), s("directory")}, []any{"run-1", "Coverage"}, []any{"run-1", "Missing"}, directoryPlan),
		add("pending", iaCoveragePendingSQL, []stmt.Param{s("run"), s("low"), s("high")}, []any{"run-1", "Coverage/", "Coverage0"}, []any{"run-1", "Root/", "Root0"}, pendingPlan),
		add("blocked", iaCoverageBlockedSQL, []stmt.Param{s("run"), s("low"), s("high")}, []any{"run-1", "Coverage/", "Coverage0"}, []any{"run-1", "Root/", "Root0"}, pendingPlan),
		add("protected", iaCoverageProtectedSQL, []stmt.Param{s("run"), s("low"), s("high")}, []any{"run-1", "Coverage/", "Coverage0"}, []any{"run-1", "Root/", "Root0"}, protectedPlan),
		add("skipped", iaCoverageSkippedSQL, []stmt.Param{s("run"), s("low"), s("high")}, []any{"run-1", "Coverage/", "Coverage0"}, []any{"run-1", "Root/", "Root0"}, skippedPlan),
	}
}
