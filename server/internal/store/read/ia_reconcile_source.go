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

// IAReconcileSourceInput selects one retained inventory object for reconcile
// preparation. Source is a configured library_sources id and Object is the
// durable inventory_objects id; both are resolved inside the caller's admitted
// library. This read is evidence, never acceptance, authority or a scan start.
type IAReconcileSourceInput struct {
	View   BrowseInput
	Source string
	Object string
}

// IAReconcileSource is the exact retained object vector. ObjectIdentity may be
// empty on an adapter without stable native ids; Revision is never substituted
// for it. State is the exact current inventory state and must be available.
type IAReconcileSource struct {
	ObjectID, SourceID, RelativePath string
	ObjectIdentity, Revision         string
	DirectoryPath, State             string
	Size, ModifiedNS                 int64
}

// IAReconcilePosition is one other retained placement of the same native
// identity, size and mtime. It is candidate evidence only: the writer re-reads
// the same bounded window before any accept.
type IAReconcilePosition struct {
	ObjectID, RelativePath, ObjectIdentity, Revision string
	Size, ModifiedNS                                 int64
}

// IAReconcileSourcePage couples the admitted source with the retained object and
// a bounded identity window. WindowComplete proves the raw window was not
// truncated; an incomplete window can never justify a move. Candidates keep
// their exact native paths and revisions and are never deduplicated.
type IAReconcileSourcePage struct {
	Library        int64
	LibraryKey     string
	Source         string
	Accepted       IAReconcileSource
	Candidates     []IAReconcilePosition
	WindowComplete bool
	RawRows        int
}

const (
	maxIAReconcileSourceBytes = 256
	maxIAReconcileObjectBytes = 512
	maxIAReconcileCandidates  = 3
	maxIAReconcilePathBytes   = 16384
)

type iaReconcileSourceCalls struct {
	n           int
	publication bool
}
type iaReconcileSourceSnapshots struct {
	base              browseSnapshots
	scope             *governor.ReadScope
	extra             []iaReconcileSourceCalls
	rootCalls         int
	copied, copyLimit int64
}

func (s *iaReconcileSourceSnapshots) NativeRowReservation() int64 {
	return s.base.NativeRowReservation()
}
func (s *iaReconcileSourceSnapshots) Read(ctx context.Context, r governor.Request, b time.Duration, fn func(*governor.ReadScope) error) (governor.SnapshotResult, error) {
	s.extra = append(s.extra, iaReconcileSourceCalls{})
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
func (s *iaReconcileSourceSnapshots) point(name string, columns []int, args ...any) ([]any, error) {
	row, e := s.scope.QueryExactBytes(readStatementID("foreground.ia.reconcile."+name+"_octets"),
		readStatementID("foreground.ia.reconcile."+name), columns, args...)
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

// IAReconcileSourceObject is harness-only and requires ConsumeForeground
// custody. It discloses no SQL, connection, writer or provider capability.
func (r *ForegroundReader) IAReconcileSourceObject(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in IAReconcileSourceInput) (IAReconcileSourcePage, BrowseReadResult, error) {
	if r == nil {
		return IAReconcileSourcePage{}, BrowseReadResult{}, ErrInput
	}
	return liveRead(ctx, request, budget, output, r.pool, in.View.Now, func(ctx context.Context, request governor.Request, budget time.Duration, at time.Time) (IAReconcileSourcePage, BrowseReadResult, error) {
		in.View.Now = at
		return r.iaReconcileSourceObject(ctx, request, budget, output, in)
	})
}

func (r *ForegroundReader) iaReconcileSourceObject(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in IAReconcileSourceInput) (IAReconcileSourcePage, BrowseReadResult, error) {
	if r == nil || in.View.Library <= 0 || !iaReconcileSourceText(in.Source, maxIAReconcileSourceBytes) ||
		!iaReconcileSourceText(in.Object, maxIAReconcileObjectBytes) || in.View.After != nil || in.View.Descending || in.View.KeepArticles {
		return IAReconcileSourcePage{}, BrowseReadResult{}, ErrInput
	}
	s := &iaReconcileSourceSnapshots{base: r.snapshots, copyLimit: output.Request.Need[governor.MemoryBytes] / 4}
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
		if !iaReconcileSourceText(key, maxIAReconcileSourceBytes) {
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
	steps := []readStep[IAReconcileSourcePage]{func(ctx context.Context, q browseQuery, f browseFence, d *IAReconcileSourcePage) error {
		if len(source) != 2 {
			return ErrUnqualified
		}
		d.Library = in.View.Library
		d.LibraryKey = source[0].(string)
		d.Source = in.Source
		row, e := s.point("object", []int{0, 1, 2, 3, 4, 7, 8}, in.Object, in.Source)
		if errors.Is(e, sql.ErrNoRows) {
			return ErrDenied
		}
		if e != nil {
			return e
		}
		if len(row) != 9 || row[0].(string) != in.Object || row[1].(string) != in.Source {
			return ErrDenied
		}
		accepted := IAReconcileSource{
			ObjectID: row[0].(string), SourceID: row[1].(string), RelativePath: row[2].(string),
			ObjectIdentity: row[3].(string), Revision: row[4].(string), Size: row[5].(int64),
			ModifiedNS: row[6].(int64), DirectoryPath: row[7].(string), State: row[8].(string),
		}
		if !iaReconcileSourceText(accepted.RelativePath, maxIAReconcilePathBytes) || !inventorySourcePath(accepted.RelativePath) ||
			!iaReconcileSourceText(accepted.Revision, maxIAReconcileObjectBytes) || accepted.Size < 0 ||
			accepted.State != "available" || accepted.ObjectIdentity != "" && !iaReconcileSourceText(accepted.ObjectIdentity, maxIAReconcileObjectBytes) ||
			!iaReconcileSourceText(accepted.DirectoryPath, maxIAReconcilePathBytes) {
			return ErrUnqualified
		}
		d.Accepted = accepted
		d.WindowComplete = true
		if accepted.ObjectIdentity == "" {
			return ctx.Err()
		}
		rows, e := q("foreground.ia.reconcile.window", in.Source, accepted.ObjectIdentity, accepted.Size, accepted.ModifiedNS, int64(maxIAReconcileCandidates))
		if e != nil {
			return e
		}
		if len(rows) > maxIAReconcileCandidates {
			return ErrUnqualified
		}
		d.RawRows = len(rows)
		d.WindowComplete = len(rows) < maxIAReconcileCandidates
		for _, position := range rows {
			if len(position) != 6 {
				return ErrUnqualified
			}
			p := IAReconcilePosition{ObjectID: position[0].(string), RelativePath: position[1].(string),
				ObjectIdentity: position[2].(string), Revision: position[3].(string), Size: position[4].(int64), ModifiedNS: position[5].(int64)}
			if !iaReconcileSourceText(p.ObjectID, maxIAReconcileObjectBytes) || !inventorySourcePath(p.RelativePath) ||
				p.ObjectIdentity != accepted.ObjectIdentity || p.Size != accepted.Size || p.ModifiedNS != accepted.ModifiedNS ||
				!iaReconcileSourceText(p.Revision, maxIAReconcileObjectBytes) {
				return ErrUnqualified
			}
			d.Candidates = append(d.Candidates, p)
		}
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

func iaReconcileSourceText(value string, limit int) bool {
	if len(value) == 0 || len(value) > limit || !utf8.ValidString(value) || strings.IndexByte(value, 0) >= 0 {
		return false
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

// inventorySourcePath is the same native relative-path shape the writer's
// inventoryPath applies; it never cleans, escapes or rewrites the bytes.
func inventorySourcePath(value string) bool {
	if len(value) == 0 || len(value) > maxIAReconcilePathBytes || strings.IndexByte(value, 0) >= 0 || strings.HasPrefix(value, "/") ||
		strings.Contains(value, `\`) || value == "." || strings.HasPrefix(value, "../") || strings.Contains(value, "/../") || strings.HasSuffix(value, "/..") {
		return false
	}
	for _, part := range strings.Split(value, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

// SQL shapes for the reconcile-source preparation. All targets are embedded
// inventory relations: no draft group is substituted for this read.
const iaReconcileLibrarySQL = `SELECT library_id FROM catalog_libraries WHERE id=?`
const iaReconcileLibraryOctetsSQL = `SELECT octet_length(library_id) FROM catalog_libraries WHERE id=?`
const iaReconcileSourceSQL = `SELECT id FROM library_sources WHERE id=? AND library_id=?`
const iaReconcileSourceOctetsSQL = `SELECT octet_length(id) FROM library_sources WHERE id=? AND library_id=?`
const iaReconcileObjectSQL = `SELECT id,source_id,relative_path,object_identity,revision,size,modified_ns,directory_path,state FROM inventory_objects WHERE id=? AND source_id=? AND state='available' AND retired=0`
const iaReconcileObjectOctetsSQL = `SELECT octet_length(id),octet_length(source_id),octet_length(relative_path),octet_length(object_identity),octet_length(revision),octet_length(directory_path),octet_length(state) FROM inventory_objects WHERE id=? AND source_id=? AND state='available' AND retired=0`
const iaReconcileWindowSQL = `SELECT id,relative_path,object_identity,revision,size,modified_ns FROM inventory_objects INDEXED BY inventory_objects_identity WHERE source_id=? AND object_identity=? AND size=? AND modified_ns=? AND retired=0 ORDER BY asset_id LIMIT ?`

// IAReconcileSourceHarnessDefinitions is inert harness composition, not
// registration or authorization. Capture these exact definitions on the actual
// reader first.
func IAReconcileSourceHarnessDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	s := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	add := func(name, sql string, params []stmt.Param, present, empty []any, p stmt.Plan) stmt.Definition {
		return stmt.Definition{ID: readStatementID("foreground.ia.reconcile." + name), SQL: sql, Params: params, Kind: stmt.Read,
			Owner: "store/read/ia_reconcile_source", CallSites: []string{"store/read/ia_reconcile_source.go:IAReconcileSourceObject"},
			Cohorts: []stmt.Cohort{{Name: "present", Bindings: present}, {Name: "empty", Bindings: empty}}, Plan: p}
	}
	libraryPlan := stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_libraries"}, Seeks: []string{"rowid=?"}}
	sourcePlan := stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_library_sources_1"}, Seeks: []string{"id=?"}}
	objectPlan := stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_inventory_objects_1"}, Seeks: []string{"id=?"}}
	windowPlan := stmt.Plan{Hot: true, Indexes: []string{"inventory_objects_identity"}, Seeks: []string{"object_identity=? AND size=? AND modified_ns=?"}}
	return []stmt.Definition{
		add("library_octets", iaReconcileLibraryOctetsSQL, []stmt.Param{i("library")}, []any{int64(1)}, []any{int64(9999999)}, libraryPlan),
		add("library", iaReconcileLibrarySQL, []stmt.Param{i("library")}, []any{int64(1)}, []any{int64(9999999)}, libraryPlan),
		add("source_octets", iaReconcileSourceOctetsSQL, []stmt.Param{s("source"), s("library")}, []any{"source-1", "movies"}, []any{"missing", "movies"}, sourcePlan),
		add("source", iaReconcileSourceSQL, []stmt.Param{s("source"), s("library")}, []any{"source-1", "movies"}, []any{"missing", "movies"}, sourcePlan),
		add("object_octets", iaReconcileObjectOctetsSQL, []stmt.Param{s("object"), s("source")}, []any{"object-1", "source-1"}, []any{"missing", "source-1"}, objectPlan),
		add("object", iaReconcileObjectSQL, []stmt.Param{s("object"), s("source")}, []any{"object-1", "source-1"}, []any{"missing", "source-1"}, objectPlan),
		add("window", iaReconcileWindowSQL, []stmt.Param{s("source"), s("identity"), i("size"), i("modified_ns"), i("limit")},
			[]any{"source-1", "ino:41", int64(42), int64(7), int64(maxIAReconcileCandidates)},
			[]any{"source-1", "ino:missing", int64(42), int64(7), int64(maxIAReconcileCandidates)}, windowPlan),
	}
}
