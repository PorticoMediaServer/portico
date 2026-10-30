package metadata

import (
	"context"
	"database/sql"
	"reflect"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/readprep"
)

const bulkPreviewMemoryTargets = 200
const bulkPreviewMemoryBytes int64 = 8 << 20

// BulkPreviewMemory accepts ONLY a verified, repeatable explicit-target source
// of at most 200 targets. Query-to-explicit conversion belongs to the caller.
// It uses no accumulator database, TEMP table, scratch file or grouped vector.
//
// reserve receives a conservative TOTAL additional live-byte allowance, before
// retained allocations. Pass Result.ResizeDelivery (Reserve requires a total-to-
// delta adapter). Charges only grow and include source/observation overlap, map
// growth, copied strings, sorting, output and two worst-case JSON delivery copies.
// The caller MUST keep its Result lease until serialization/transport finishes,
// and release it on success AND failure. This method never releases that lease.
//
// The existing editor's singleton snapshot/source vector is an indivisible O(B)
// transient exception: it materializes before its size can be measured. Each is
// preflighted before revision serialization or retention; an oversized vector
// returns ErrCapacity and is never cached. This is not a metadata truncation or
// an allocation exception for the retained aggregate. No strict bound on that
// existing singleton reader's transient allocations or time is claimed.
func (s *Service) BulkPreviewMemory(ctx context.Context, source BulkPreviewSource, authorize func(context.Context, *sql.Tx) error, reserve func(int64) error) (BulkMetadataPreview, error) {
	if source == nil || authorize == nil || reserve == nil || dbwork.Snapshot(ctx) != nil {
		return BulkMetadataPreview{}, ErrRepairInput
	}
	m := bulkPreviewMemory{reserve: reserve}
	// Covers schemas, fixed frames/closures, map headers and bounded bookkeeping.
	if err := m.charge(64 << 10); err != nil {
		return BulkMetadataPreview{}, err
	}
	m.initAggregate()
	checkedSource := func(ctx context.Context, tx *sql.Tx, after int64) (catalog.BulkMetadataSelectionPage, error) {
		page, err := source(ctx, tx, after)
		if err != nil {
			return page, err
		}
		selection := page.Selection
		if len(selection.Targets) > catalog.BulkMetadataPageSize || len(selection.Targets) == 0 && (!page.Done || strings.TrimSpace(selection.SQL) != "") {
			return catalog.BulkMetadataSelectionPage{}, ErrRepairInput
		}
		// Source-owned vectors may be indivisible. Preflight them immediately;
		// clone the two strings the pipeline retains, avoiding backing substrings.
		bytes := int64(len(page.Fence)) + int64(len(selection.Revision)) + int64(len(selection.SQL))
		if len(selection.Args) > int(bulkPreviewMemoryBytes/256) {
			return catalog.BulkMetadataSelectionPage{}, readprep.ErrCapacity
		}
		bytes += 8*int64(len(selection.Args)) + 8*int64(len(selection.Targets))
		for _, arg := range selection.Args {
			switch v := arg.(type) {
			case string:
				bytes += int64(len(v))
			case []byte:
				bytes += int64(len(v))
			case nil, bool, int, int32, int64, uint, uint32, uint64, float64:
			default:
				return catalog.BulkMetadataSelectionPage{}, ErrRepairInput
			}
		}
		for _, target := range selection.Targets {
			bytes += int64(len(target.Kind) + len(target.ContentKind) + len(target.ID) + len(target.LibraryID))
		}
		if bytes > bulkPreviewMemoryBytes/32 {
			return catalog.BulkMetadataSelectionPage{}, readprep.ErrCapacity
		}
		if err = m.charge(4096 + 32*bytes); err != nil {
			return catalog.BulkMetadataSelectionPage{}, err
		}
		page.Fence = strings.Clone(page.Fence)
		page.Selection.Revision = strings.Clone(selection.Revision)
		return page, nil
	}
	return s.bulkPreview(ctx, checkedSource, authorize, m.page, m.add, m.fields)
}

type bulkPreviewMemory struct {
	reserve         func(int64) error
	used, transient int64
	targets         int
	ids             [bulkPreviewMemoryTargets]string
	aggregate       bulkPreviewAggregate
	reserved        int64
}

func (m *bulkPreviewMemory) charge(bytes int64) error {
	if bytes < 0 || bytes > bulkPreviewMemoryBytes-m.used-m.transient-m.aggregate.used {
		return readprep.ErrCapacity
	}
	if err := m.reserveTotal(m.used + m.transient + m.aggregate.used + bytes); err != nil {
		return err
	}
	m.used += bytes
	return nil
}

// These are known, acyclic editor structures. Count capacities and every string
// occurrence (even shared literals/backings). 256 bytes/node bounds map bucket
// growth, headers and reflection/reader bookkeeping; 32 bytes/source byte covers
// overlapping reader/observation/digest and escaped JSON buffers. Stop early,
// before arithmetic can overflow. No input-sized measuring buffer is allocated.
func bulkPreviewMemoryFootprint(v reflect.Value, left int64) int64 {
	if !v.IsValid() || left < 256 {
		return 256
	}
	n := int64(256)
	add := func(child reflect.Value) { n += bulkPreviewMemoryFootprint(child, left-n) }
	switch v.Kind() {
	case reflect.String:
		if int64(v.Len()) > (left-n)/32 {
			return left + 1
		}
		n += 32 * int64(v.Len())
	case reflect.Pointer, reflect.Interface:
		if !v.IsNil() {
			add(v.Elem())
		}
	case reflect.Struct:
		for i := 0; i < v.NumField() && n <= left; i++ {
			add(v.Field(i))
		}
	case reflect.Map:
		it := v.MapRange()
		for it.Next() && n <= left {
			add(it.Key())
			add(it.Value())
		}
	case reflect.Slice:
		n += int64(v.Cap()) * 256
		for i := 0; i < v.Len() && n <= left; i++ {
			add(v.Index(i))
		}
	}
	return n
}

func (m *bulkPreviewMemory) preflight(values ...any) error {
	var n int64
	for _, value := range values {
		n += bulkPreviewMemoryFootprint(reflect.ValueOf(value), bulkPreviewMemoryBytes-n)
		if n > bulkPreviewMemoryBytes {
			return readprep.ErrCapacity
		}
	}
	if n > m.transient {
		delta := n - m.transient
		if err := m.charge(delta); err != nil {
			return err
		}
		m.used -= delta
		m.transient = n
	}
	return nil
}

// Same CAS inputs/order as repairRevision, with preflight before its digest
// buffers. Credit/publication fences also contain arbitrary source strings;
// checking only editor-visible fields would miss that live memory.
func (m *bulkPreviewMemory) revision(ctx context.Context, tx *sql.Tx, target RepairTarget, snapshot RepairSnapshot, entity repairEntity) (string, error) {
	if err := m.preflight(snapshot, entity); err != nil {
		return "", err
	}
	id, err := resolveEntity(ctx, tx, target.ID)
	if err != nil {
		return "", err
	}
	var last int64
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(id),0) FROM metadata_owner_history WHERE kind=? AND entity_id=?`, target.Kind, id).Scan(&last); err != nil {
		return "", err
	}
	var creditFence []any
	if target.Kind == "item" {
		creditFence, err = creditRepairFence(ctx, tx, id)
		if err != nil {
			return "", err
		}
	}
	if err = m.preflight(snapshot, entity, creditFence); err != nil {
		return "", err
	}
	return publicationDigest([]any{target, entity.library, entity.stamp, snapshot, last, creditFence}), nil
}

func (m *bulkPreviewMemory) page(ctx context.Context, tx *sql.Tx, selection catalog.BulkMetadataSelection) (page bulkPreviewPage, err error) {
	if tx == nil || len(selection.Targets) == 0 || len(selection.Targets) > catalog.BulkMetadataPageSize || len(selection.Targets) > bulkPreviewMemoryTargets-m.targets {
		return page, ErrRepairInput
	}
	first := selection.Targets[0]
	if err = m.charge(16 << 10); err != nil { // schema and page slice growth
		return page, err
	}
	out, err := previewSchema(first.ContentKind)
	if err != nil {
		return page, err
	}
	for _, target := range selection.Targets {
		kind, e := compactcatalog.ParseKind(target.ContentKind)
		if e != nil || target.ContentKind != first.ContentKind || compactcatalog.OwnerNamespace(kind) != target.Kind || target.ID == "" || len(target.ID) > 256 || target.LibraryID == "" || len(target.LibraryID) > 256 {
			return page, ErrRepairInput
		}
		for _, id := range m.ids[:m.targets] {
			if id == target.ID {
				return page, ErrRepairInput
			}
		}
		observations, result, e := bulkPreviewTargetTx(ctx, tx, target, out.ContentKind, out.Schema, true, m.revision)
		if e != nil {
			return page, e
		}
		bytes := int64(2048 + 256*len(observations))
		for _, o := range observations {
			bytes += 32 * int64(len(o.value))
			for _, entry := range o.entries {
				bytes += 256 + 32*int64(len(entry))
			}
		}
		if err = m.charge(bytes); err != nil {
			return page, err
		}
		m.ids[m.targets] = strings.Clone(target.ID)
		m.targets++
		out.Targets = append(out.Targets, BulkPreviewTarget{strings.Clone(result.Kind), strings.Clone(result.ID), strings.Clone(result.LibraryID), strings.Clone(result.ExpectedRevision)})
		page.observations = append(page.observations, observations...)
	}
	out.Total = int64(len(selection.Targets))
	page.preview = out
	return page, nil
}

// Preserve the original small adapter's monotonically growing delivery lease.
// Released aggregate keys can reduce live memory without prematurely reducing
// the reservation held through conversion, encoding and the actual write.
func (m *bulkPreviewMemory) reserveTotal(n int64) error {
	if n < 0 || n > bulkPreviewMemoryBytes {
		return readprep.ErrCapacity
	}
	n = max(n, m.reserved)
	if err := m.reserve(n); err != nil {
		return err
	}
	m.reserved = n
	return nil
}
func (m *bulkPreviewMemory) initAggregate() {
	m.aggregate.reserve = func(n int64) error {
		if n > bulkPreviewMemoryBytes-m.used-m.transient {
			return readprep.ErrCapacity
		}
		return m.reserveTotal(m.used + m.transient + n)
	}
}
func (m *bulkPreviewMemory) add(ctx context.Context, out *BulkMetadataPreview, page bulkPreviewPage) error {
	m.initAggregate()
	if err := m.aggregate.add(ctx, page.observations); err != nil {
		return err
	}
	if err := m.charge(256 * int64(len(page.preview.Targets))); err != nil {
		return err
	}
	if err := previewAddCount(&out.Total, page.preview.Total); err != nil {
		return err
	}
	out.Targets = append(out.Targets, page.preview.Targets...)
	return nil
}
func (m *bulkPreviewMemory) fields(ctx context.Context, schema []RepairFieldSpec) (map[string]BulkPreviewField, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.initAggregate()
	return m.aggregate.result(schema)
}
