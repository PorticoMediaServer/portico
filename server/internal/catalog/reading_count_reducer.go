package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

const (
	ReadingCountMaximumRaw   = 4096
	ReadingCountMaximumBytes = 4 << 20
	ReadingCountYieldAfter   = 10 * time.Millisecond
)

// ReadingCountReduction is caller-owned volatile work, never persisted. Callers
// serialize Step calls and own demand expiry, deduplication, byte reservations,
// cancellation and live authority. No partial count is exposed as a result.
// A legal oversized scalar is one lossless O(B) transient evaluation unit,
// outside the ordinary quantum byte ceiling, followed immediately by a yield.
// Its payload is never retained in this state or cached.
type ReadingCountReduction struct {
	library, profile, viewerFence, witness       string
	evaluationAt                                 string
	originalExpiry                               time.Time
	queryFingerprint                             string
	predicate                                    string
	predicateArgs                                []any
	attributeSQL                                 string
	attributeArgs                                []any
	scope                                        readingBrowseScope
	filtered, restricted, blockUnrated           bool
	maxAge                                       *int
	threshold                                    int
	classID, classGeneration, servingGeneration  int64
	retaining                                    bool
	queryUnits                                   int
	memoryBytes                                  int64
	phase                                        readingCountPhase
	afterEntity, entity, afterPublication, asset int64
	afterObject, object                          int64
	pendingBytes                                 int64
	afterMember, member                          int64
	afterSource                                  int
	previousMembers, anyMember, knownRating      bool
	afterTerm                                    int64
	count                                        int
	done                                         bool
}

type ReadingCountQuantum struct {
	Done              bool
	Total             int
	TotalIsLowerBound bool
	Raw               int
	Bytes             int64
	Oversized         bool
}

func (r *ReadingCountReduction) Witness() string {
	if r == nil {
		return ""
	}
	return r.witness
}

// EvaluationAt is the captured relative-query instant, separate from the shared
// base witness. Cache identity must include it when it is nonempty.
func (r *ReadingCountReduction) EvaluationAt() string {
	if r == nil {
		return ""
	}
	return r.evaluationAt
}

// OriginalExpiry is nonzero only for an admitted signed continuation. An
// adapter must use the same ceiling for a preparation handle, never renew it.
func (r *ReadingCountReduction) OriginalExpiry() time.Time {
	if r == nil {
		return time.Time{}
	}
	return r.originalExpiry
}

// MemoryBytes is a conservative reservation for this reduction's retained
// SQL/arguments/cursors. Step also limits the logical source bytes it examines.
func (r *ReadingCountReduction) MemoryBytes() int64 {
	if r == nil {
		return 0
	}
	return r.memoryBytes
}

type readingCountPhase uint8

const (
	countRaw readingCountPhase = iota
	countBrowse
	countQuerySize
	countQuery
	countPublication
	countArchive
	countArchiveValue
	countPublishedClass
	countMembership
	countMemberLive
	countAttribute
	countAttributeValue
)

type readingCountBudget struct {
	raw   int
	bytes int64
	yield time.Duration
}

var defaultReadingCountBudget = readingCountBudget{ReadingCountMaximumRaw, ReadingCountMaximumBytes, ReadingCountYieldAfter}

// NewReadingCountReduction validates a Reading leaf query and captures its
// complete basis in a short snapshot. Query/authority identity stays separate
// from Witness. No worker, cache, pending handle, or database result is created.
func (s *Service) NewReadingCountReduction(viewer Viewer, request BrowseRequest) (*ReadingCountReduction, error) {
	if !viewer.AllowsLibrary(request.Library) {
		return nil, sql.ErrNoRows
	}
	if request.Range != nil || request.Seek != nil || request.HomeRowDisplay != "" || len(request.Rows) != 0 || len(request.RowSources) != 0 {
		return nil, browseIssue("query", "count reduction requires an ordinary Reading leaf query")
	}
	if dbwork.Snapshot(s.Context()) != nil || dbwork.InWriteTx(s.Context()) || s.readTx != nil {
		return nil, fmt.Errorf("Reading count admission requires its own short snapshot")
	}
	raw, err := json.Marshal(request.Query)
	if err != nil {
		return nil, err
	}
	query, err := ParseBrowseQuery(raw, "query")
	if err != nil {
		return nil, err
	}
	s, err = s.withBrowseInstant(query, request.Cursor)
	if err != nil {
		return nil, err
	}
	var out *ReadingCountReduction
	err = dbwork.WithReadSnapshot(s.Context(), s.db, func(ctx context.Context) error {
		read := s.WithContext(ctx)
		scope, e := read.readingBrowseScope(request.Library)
		if e != nil {
			return e
		}
		if scope == nil {
			return ErrBrowsePivot
		}
		pivot := request.Pivot
		if pivot == "" {
			pivot = defaultPivot(scope.kind)
		}
		p, ok := pivotForKind(scope.kind, pivot)
		if !ok || !p.Browsable || p.Aggregate != "" {
			return ErrBrowsePivot
		}
		sorts, e := resolveBrowseSorts(p, request.Sort)
		if e != nil {
			return e
		}
		_ = sorts
		scope.point = true
		r := &ReadingCountReduction{library: strings.Clone(request.Library), profile: strings.Clone(viewer.Profile), viewerFence: strings.Clone(viewer.Fence), scope: *scope, evaluationAt: s.relativeBrowseTime(query), queryFingerprint: queryFingerprint(query), filtered: query != nil, threshold: browseTotalThreshold, queryUnits: 1}
		policy := viewer.EffectiveRestrictions()
		r.restricted, r.blockUnrated = policy.Active(), policy.BlockUnrated
		if policy.MaximumAge != nil {
			age := *policy.MaximumAge
			r.maxAge = &age
		}
		policyBytes := int64(4096 + len(policy.ProfileID))
		for _, labels := range [][]string{policy.BlockedLabels, policy.MemberDeniedLabels} {
			for _, label := range labels {
				policyBytes += int64(len(label))*2 + 64
			}
		}
		if policyBytes > ReadingCountMaximumBytes {
			return ErrReadingCountCapacity
		}
		if request.Cursor != "" {
			_, present, checkErr := read.BrowseReadingCarriedTotal(viewer, request)
			e = checkErr
			if e != nil {
				return e
			}
			signed, e := read.readSignedBrowseCursor(request.Cursor)
			if e != nil {
				return e
			}
			r.originalExpiry = time.Unix(signed.Expires, 0)
			if present {
				decoded, e := read.decodeCursor(request.Cursor, signed.Scope)
				if e != nil {
					return e
				}
				r.count = decoded.Total
				r.done = true
			}
		}
		r.witness, e = read.browseExactWitness(r.library, r.profile, policy)
		if e != nil {
			return e
		}
		e = read.read().QueryRow(`SELECT generation,retaining FROM catalog_generations WHERE singleton=1`).Scan(&r.servingGeneration, &r.retaining)
		if e != nil {
			return e
		}
		if r.restricted {
			key, _ := visibilityClassKey(r.library, policy)
			e = read.read().QueryRow(`SELECT id,active_generation FROM compact_visibility_classes WHERE class_key=? AND library_id=? AND retired=0`, key, scope.library).Scan(&r.classID, &r.classGeneration)
			if e != nil && !errors.Is(e, sql.ErrNoRows) {
				return e
			}
		}
		compiler := browseCompiler{reading: scope, now: s.recommendationNow(s.browseInstant), profile: viewer.Profile, restrictions: policy, perTitle: true}
		r.predicate, e = compiler.compile(query)
		if e != nil {
			return e
		}
		r.predicateArgs = compiler.args
		// Every attribution predicate is one PK probe; conservatively charge every
		// leaf/value, even when SQL short-circuits before visiting it.
		var units func(*BrowseNode) int
		units = func(n *BrowseNode) int {
			if n == nil {
				return 0
			}
			v := 0
			if n.Field == "author" || n.Field == "series" {
				v = 1
				if a, ok := n.Value.([]any); ok {
					v += len(a)
				}
			}
			v += units(n.Not)
			for i := range n.All {
				v += units(&n.All[i])
			}
			for i := range n.Any {
				v += units(&n.Any[i])
			}
			return v
		}
		r.queryUnits += units(query)
		deny := []string{"0"}
		if len(policy.BlockedLabels) > 0 {
			deny = append(deny, "(term.field_id=2 AND term.value_key IN("+placeholders(len(policy.BlockedLabels))+"))")
			for _, label := range policy.BlockedLabels {
				r.attributeArgs = append(r.attributeArgs, strings.ToLower(label))
			}
		}
		if len(policy.MemberDeniedLabels) > 0 {
			deny = append(deny, "(term.field_id IN(2,3) AND lower(ae.source_value) IN("+placeholders(len(policy.MemberDeniedLabels))+"))")
			for _, label := range policy.MemberDeniedLabels {
				r.attributeArgs = append(r.attributeArgs, strings.ToLower(label))
			}
		}
		r.attributeSQL = `SELECT term.field_id,ra.minimum_age,(` + strings.Join(deny, " OR ") + `) FROM catalog_item_attribute_edges ae JOIN catalog_attribute_terms term ON term.id=ae.term_id LEFT JOIN content_rating_ages ra ON ra.value_key=term.value_key WHERE ae.item_id=? AND ae.term_id=?`
		r.memoryBytes = int64(4096 + len(r.predicate) + len(r.attributeSQL) + len(r.library) + len(r.profile) + len(r.viewerFence) + len(r.witness) + len(r.evaluationAt) + len(r.queryFingerprint))
		for _, args := range [][]any{r.predicateArgs, r.attributeArgs} {
			for _, arg := range args {
				r.memoryBytes += 32
				if text, ok := arg.(string); ok {
					r.memoryBytes += int64(len(text))
				}
			}
		}
		if r.MemoryBytes() > ReadingCountMaximumBytes {
			return ErrReadingCountCapacity
		}
		after, e := read.browseExactWitness(r.library, r.profile, policy)
		if e != nil {
			return e
		}
		if after != r.witness {
			return ErrReadingProjectionChanged
		}
		out = r
		return nil
	})
	return out, err
}

var ErrReadingCountCapacity = errors.New("Reading count retained state exceeds memory capacity")

var ErrReadingCountTransientUnit = errors.New("Reading count scalar admitted as transient uncached unit")

// ReadingCountResize admits peak reducer memory BEFORE a scalar evaluation.
// oversized identifies the approved single O(B) transient exception: it is not
// ordinary cached state. The callback is called again with retained bytes after
// evaluation, after SQLite has released the scalar. Adapters add their own live
// request/payload buffers and use the shared foundation's Budget.Resize.
type ReadingCountResize func(peak int64, oversized bool) error

// StepReadingCountReduction performs one read-only quantum. Its raw driver is
// catalog_entities' integer PK, BEFORE library/kind/query/restriction filters.
// This conservative first backend visits other libraries too. Every nested row
// and EOF probe is charged; no LIMIT sits after a potentially unbounded filter.
func (s *Service) StepReadingCountReduction(viewer Viewer, r *ReadingCountReduction, resize ...ReadingCountResize) (ReadingCountQuantum, error) {
	return s.stepReadingCountReduction(viewer, r, defaultReadingCountBudget, resize...)
}
func (s *Service) stepReadingCountReduction(viewer Viewer, r *ReadingCountReduction, budget readingCountBudget, resize ...ReadingCountResize) (ReadingCountQuantum, error) {
	var out ReadingCountQuantum
	if r == nil || !viewer.AllowsLibrary(r.library) || viewer.Profile != r.profile || viewer.Fence != r.viewerFence {
		return out, ErrStaleContinuation
	}
	if budget.raw < r.queryUnits+4 || budget.raw > ReadingCountMaximumRaw || budget.bytes < 1024 || budget.bytes > ReadingCountMaximumBytes || budget.yield <= 0 || budget.yield > ReadingCountYieldAfter {
		return out, fmt.Errorf("invalid Reading count quantum budget")
	}
	if dbwork.Snapshot(s.Context()) != nil || dbwork.InWriteTx(s.Context()) || s.readTx != nil {
		return out, fmt.Errorf("Reading count step requires its own short snapshot")
	}
	next := *r
	err := dbwork.WithReadSnapshot(s.Context(), s.db, func(ctx context.Context) error {
		read := s.WithContext(ctx)
		witness, e := read.browseExactWitness(r.library, r.profile, viewer.EffectiveRestrictions())
		if e != nil {
			return e
		}
		if witness != r.witness {
			return ErrReadingProjectionChanged
		}
		started := time.Now()
		for !next.done && out.Raw+next.queryUnits+4 <= budget.raw && time.Since(started) < budget.yield {
			if e = ctx.Err(); e != nil {
				return e
			}
			bytes := int64(64)
			scalar := next.phase == countQuery || next.phase == countAttributeValue || next.phase == countArchiveValue
			if scalar {
				bytes += next.pendingBytes
			}
			oversized := bytes > budget.bytes
			// A probe never allocates the scalar. Save its byte length and
			// yield before the exceptional unit if this quantum did other work.
			if oversized && out.Raw > 0 || !oversized && out.Bytes+bytes > budget.bytes {
				break
			}
			if scalar && len(resize) > 0 && resize[0] != nil {
				// Text normalization may expand Unicode; the reservation is
				// deliberately conservative. No scalar survives the SQL call.
				if e = resize[0](next.MemoryBytes()+bytes*16, oversized); e != nil {
					if errors.Is(e, ErrReadingCountTransientUnit) {
						oversized = true
					} else {
						return e
					}
				}
			}
			workErr := next.unit(read, &out)
			if scalar && len(resize) > 0 && resize[0] != nil {
				if e = resize[0](next.MemoryBytes(), false); e != nil {
					return e
				}
			}
			if workErr != nil {
				return workErr
			}
			if oversized {
				out.Oversized = true
				break
			}
		}
		after, e := read.browseExactWitness(r.library, r.profile, viewer.EffectiveRestrictions())
		if e != nil {
			return e
		}
		if after != r.witness {
			return ErrReadingProjectionChanged
		}
		return ctx.Err()
	})
	if err != nil {
		return out, err
	}
	*r = next
	out.Done = next.done
	if next.done {
		out.Total = next.count
		if next.filtered && next.count > next.threshold {
			out.Total = next.threshold
			out.TotalIsLowerBound = true
		}
	}
	return out, nil
}

func (r *ReadingCountReduction) accept() {
	r.count++
	if r.filtered && r.count > r.threshold {
		r.done = true
	}
	r.phase = countRaw
	r.afterObject = 0
}
func (r *ReadingCountReduction) nextMember() {
	r.phase = countMembership
	r.afterTerm = 0
	r.knownRating = false
}

func (r *ReadingCountReduction) unit(s *Service, q *ReadingCountQuantum) error {
	q.Raw++ // includes rejected input and EOF, not just accepted rows
	q.Bytes += 64
	switch r.phase {
	case countRaw:
		var library int64
		var kind, retired int
		err := s.read().QueryRow(`SELECT id,library_id,kind,retired FROM catalog_entities WHERE id>? ORDER BY id LIMIT 1`, r.afterEntity).Scan(&r.entity, &library, &kind, &retired)
		if errors.Is(err, sql.ErrNoRows) {
			r.done = true
			return nil
		}
		if err != nil {
			return err
		}
		r.afterEntity = r.entity
		wanted := 20
		if r.scope.kind == "comic" {
			wanted = 21
		}
		if library == r.scope.library && kind == wanted && retired == 0 {
			r.phase = countBrowse
		}
		return nil
	case countBrowse:
		var library int64
		var kind int
		err := s.read().QueryRow(`SELECT library_id,kind FROM catalog_browse_rows WHERE entity_id=?`, r.entity).Scan(&library, &kind)
		// Serving resolution is two PK probes at most, including absence overlays.
		q.Raw += 2
		if errors.Is(err, sql.ErrNoRows) {
			r.phase = countRaw
			return nil
		}
		if err != nil {
			return err
		}
		wanted := 20
		if r.scope.kind == "comic" {
			wanted = 21
		}
		if library != r.scope.library || kind != wanted {
			r.phase = countRaw
			return nil
		}
		if r.filtered {
			r.phase = countQuerySize
		} else {
			r.afterPublication = 0
			r.phase = countPublication
		}
		return nil
	case countQuerySize:
		var bytes int64
		err := s.read().QueryRow(`SELECT length(CAST(e.title AS BLOB))+COALESCE(length(CAST(e.added_text AS BLOB)),0)+COALESCE((SELECT COALESCE(length(CAST(m.author_key AS BLOB)),0)+COALESCE(length(CAST(m.series_key AS BLOB)),0) FROM reading_attribution_members m WHERE m.library_id=? AND m.generation=? AND m.item_id=e.entity_id),0) FROM catalog_browse_rows e WHERE e.entity_id=?`, r.scope.library, r.scope.generation.Ready, r.entity).Scan(&bytes)
		q.Raw += 3
		if err != nil {
			return err
		}
		r.pendingBytes = bytes
		r.phase = countQuery
		return nil
	case countQuery:
		q.Bytes += r.pendingBytes
		var matches bool
		err := s.read().QueryRow(`SELECT COALESCE((`+r.predicate+`),0) FROM catalog_browse_rows e WHERE e.entity_id=?`, append(append([]any{}, r.predicateArgs...), r.entity)...).Scan(&matches)
		q.Raw += r.queryUnits + 2
		if err != nil {
			return err
		}
		if !matches {
			r.phase = countRaw
			return nil
		}
		r.afterPublication = 0
		r.phase = countPublication
		return nil
	case countPublication:
		var format string
		err := s.read().QueryRow(`SELECT asset_id,format FROM reading_publications WHERE item_id=? AND asset_id>? ORDER BY asset_id LIMIT 1`, r.entity, r.afterPublication).Scan(&r.asset, &format)
		if errors.Is(err, sql.ErrNoRows) {
			r.phase = countRaw
			return nil
		}
		if err != nil {
			return err
		}
		r.afterPublication = r.asset
		if !containsString(r.scope.sourceFormats(), format) {
			return nil
		}
		// Counts read no per-version attribution: the chosen primary belongs to
		// exactly this eligible set, so existence of any member is equivalent to
		// existence of the availability-ordered primary. FK closure supplies links.
		if format != "cb7" && format != "cbr" {
			r.phase = countPublishedClass
			return nil
		}
		r.afterObject = 0
		r.phase = countArchive
		return nil
	case countArchive:
		// Integer rowid is the raw cursor, before asset/source filtering. It
		// avoids retaining an arbitrarily large legal object-id/asset token.
		// This deliberately trades extra charged raw visits for bounded state.
		err := s.read().QueryRow(`SELECT rowid FROM inventory_objects WHERE rowid>? ORDER BY rowid LIMIT 1`, r.afterObject).Scan(&r.object)
		if errors.Is(err, sql.ErrNoRows) {
			r.phase = countPublication
			return nil
		}
		if err != nil {
			return err
		}
		r.afterObject = r.object
		var bytes int64
		err = s.read().QueryRow(`SELECT (SELECT length(CAST(token AS BLOB)) FROM catalog_assets WHERE id=?)+length(CAST(o.asset_id AS BLOB))+length(CAST(o.source_id AS BLOB))+length(CAST(o.root_incarnation AS BLOB))+COALESCE((SELECT length(CAST(source.incarnation AS BLOB))+length(CAST(source.library_id AS BLOB))+length(CAST(source.kind AS BLOB)) FROM library_sources source WHERE source.id=o.source_id),0) FROM inventory_objects o WHERE rowid=?`, r.asset, r.object).Scan(&bytes)
		q.Raw += 2
		if err != nil {
			return err
		}
		r.pendingBytes = bytes
		r.phase = countArchiveValue
		return nil
	case countArchiveValue:
		q.Bytes += r.pendingBytes
		var local bool
		err := s.read().QueryRow(`SELECT o.asset_id=a.token AND o.retired=0 AND o.size=a.size AND o.modified_ns=a.modified_ns AND EXISTS(SELECT 1 FROM library_sources source WHERE source.id=o.source_id AND source.library_id=? AND source.kind='local' AND source.incarnation=o.root_incarnation) FROM inventory_objects o JOIN catalog_assets a ON a.id=? WHERE o.rowid=?`, r.library, r.asset, r.object).Scan(&local)
		q.Raw += 2
		if err != nil {
			return err
		}
		if local {
			r.phase = countPublishedClass
		} else {
			r.phase = countArchive
		}
		return nil
	case countPublishedClass:
		if !r.restricted {
			r.accept()
			return nil
		}
		// Same optional published-class prefilter as browseSelect; the live member
		// predicate below remains authoritative. Class readiness is never inferred.
		if r.classID != 0 && r.classGeneration > 0 {
			var visible bool
			err := s.read().QueryRow(`SELECT EXISTS(SELECT 1 FROM compact_visibility_rows WHERE class_id=? AND generation=? AND entity_id=?)`, r.classID, r.classGeneration, r.entity).Scan(&visible)
			if err != nil {
				return err
			}
			if !visible {
				r.phase = countRaw
				return nil
			}
		}
		r.afterMember = 0
		r.afterSource = -1
		r.previousMembers = false
		r.anyMember = false
		r.phase = countMembership
		return nil
	case countMembership:
		return r.membershipUnit(s, q)
	case countMemberLive:
		var live bool
		err := s.read().QueryRow(`SELECT EXISTS(SELECT 1 FROM catalog_entities ci JOIN catalog_libraries lib ON lib.id=ci.library_id WHERE ci.id=? AND lib.retired=0)`, r.member).Scan(&live)
		q.Raw++
		if err != nil {
			return err
		}
		if !live {
			r.nextMember()
			return nil
		}
		r.afterTerm = 0
		r.knownRating = false
		r.phase = countAttribute
		return nil
	case countAttribute:
		var bytes, termBytes int64
		err := s.read().QueryRow(`SELECT term_id,length(CAST(source_value AS BLOB)) FROM catalog_item_attribute_edges WHERE item_id=? AND term_id>? ORDER BY term_id LIMIT 1`, r.member, r.afterTerm).Scan(&r.afterTerm, &bytes)
		if errors.Is(err, sql.ErrNoRows) {
			if !r.blockUnrated || r.knownRating {
				r.accept()
			} else {
				r.nextMember()
			}
			return nil
		}
		if err != nil {
			return err
		}
		err = s.read().QueryRow(`SELECT length(CAST(value_key AS BLOB)) FROM catalog_attribute_terms WHERE id=?`, r.afterTerm).Scan(&termBytes)
		q.Raw++
		if err != nil {
			return err
		}
		r.pendingBytes = bytes + termBytes
		r.phase = countAttributeValue
		return nil
	case countAttributeValue:
		q.Bytes += r.pendingBytes
		var field int
		var age sql.NullInt64
		var denied bool
		args := append(append([]any{}, r.attributeArgs...), r.member, r.afterTerm)
		err := s.read().QueryRow(r.attributeSQL, args...).Scan(&field, &age, &denied)
		q.Raw += 2
		if err != nil {
			return err
		}
		if denied || field == 1 && age.Valid && r.maxAge != nil && age.Int64 > int64(*r.maxAge) {
			r.nextMember()
			return nil
		}
		if field == 1 && age.Valid && age.Int64 >= 0 {
			r.knownRating = true
		}
		r.phase = countAttribute
		return nil
	}
	return fmt.Errorf("invalid Reading count phase")
}

// Walk physical base and retained membership streams separately. A LIMIT over
// the serving UNION could skip arbitrary masked/rejected history internally;
// explicit mask probes charge those rows too. Both walks use their full PK.
func (r *ReadingCountReduction) membershipUnit(s *Service, q *ReadingCountQuantum) error {
	tx := dbwork.Snapshot(s.Context())
	var item int64
	var source, present int
	var err error
	if r.previousMembers {
		err = tx.QueryRowContext(s.Context(), `SELECT item_id,source,present FROM catalog_browse_memberships_previous WHERE generation=? AND entity_id=? AND (item_id,source)>(?,?) ORDER BY item_id,source LIMIT 1`, r.servingGeneration, r.entity, r.afterMember, r.afterSource).Scan(&item, &source, &present)
	} else {
		err = tx.QueryRowContext(s.Context(), `SELECT item_id,source FROM catalog_browse_memberships WHERE entity_id=? AND (item_id,source)>(?,?) ORDER BY item_id,source LIMIT 1`, r.entity, r.afterMember, r.afterSource).Scan(&item, &source)
	}
	if errors.Is(err, sql.ErrNoRows) {
		if !r.previousMembers && r.retaining {
			r.previousMembers = true
			r.afterMember = 0
			r.afterSource = -1
			return nil
		}
		if !r.anyMember {
			r.accept()
		} else {
			r.phase = countRaw
		}
		return nil
	}
	if err != nil {
		return err
	}
	r.afterMember, r.afterSource = item, source
	if r.previousMembers {
		if present == 0 {
			return nil
		}
	} else if r.retaining {
		err = tx.QueryRowContext(s.Context(), `SELECT present FROM catalog_browse_memberships_previous WHERE generation=? AND entity_id=? AND item_id=? AND source=?`, r.servingGeneration, r.entity, item, source).Scan(&present)
		q.Raw++
		if err == nil {
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	r.anyMember = true
	r.member = item
	r.phase = countMemberLive
	return nil
}
