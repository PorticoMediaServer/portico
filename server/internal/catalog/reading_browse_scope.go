package catalog

import (
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/readingcap"
)

// readingBrowseScope is a private, snapshot-bound dispatch context. Accepted
// values are library facets, never audiobook Author entities. Its integer
// identifiers come only from the database, not from query operands.
type readingBrowseScope struct {
	// point is used only by the bounded count reducer, whose raw driver has
	// already selected one entity. Never materialize a whole attribution set.
	point      bool
	mobi       bool
	library    int64
	kind       string
	generation compactcatalog.ReadingGeneration
}

// ErrReadingProjectionChanged preserves the existing catalog stale cause while
// the HTTP boundary uses the registered revision_mismatch refusal for Reading.
var ErrReadingProjectionChanged = fmt.Errorf("%w: Reading projection changed", ErrStaleContinuation)

func isReadingLibrary(kind string) bool { return kind == "book" || kind == "comic" }

func readingBrowseField(field string) bool {
	switch field {
	case "title", "year", "dateAdded", "author", "series":
		return true
	}
	return false
}

func (s *Service) bindReadingBrowse(library string, c *browseCompiler) (string, error) {
	if library == "" {
		return "1", nil
	}
	r, err := s.readingBrowseScope(library)
	if err != nil || r == nil {
		return "1", err
	}
	c.reading = r
	c.count, c.ids = nil, nil
	return r.eligibleSQL("e.entity_id"), nil
}

func (s *Service) readingBrowseScope(library string) (*readingBrowseScope, error) {
	lib, err := s.library(library)
	if err != nil || !isReadingLibrary(lib.Kind) {
		return nil, err
	}
	// Requiring a carried transaction prevents a ready check and its consumers
	// from silently using different states. Mutation selectors carry a writer.
	if dbwork.Snapshot(s.Context()) == nil && !dbwork.InWriteTx(s.Context()) && s.readTx == nil {
		return nil, ErrReadingProjectionChanged
	}
	out := &readingBrowseScope{kind: lib.Kind, mobi: lib.Kind == "book" && s.readingMobiConfigured()}
	var rule int
	err = s.read().QueryRow(`SELECT c.id,a.namespace,a.input_generation,a.ready_generation,a.publication_rule_version
 FROM catalog_libraries c JOIN reading_attribution_state a ON a.library_id=c.id
 WHERE c.library_id=? AND c.retired=0`, library).Scan(&out.library, &out.generation.Namespace, &out.generation.Input, &out.generation.Ready, &rule)
	if errors.Is(err, sql.ErrNoRows) || err == nil && (out.generation.Ready == 0 || out.generation.Input != out.generation.Ready || rule != compactcatalog.ReadingProjectionRuleVersion) {
		return nil, ErrReadingProjectionChanged
	}
	return out, err
}

func (r *readingBrowseScope) fence() string {
	value := fmt.Sprintf("reading:%d:%d:%d:%s", r.generation.Namespace, r.generation.Input, r.generation.Ready, r.kind)
	if r.mobi {
		value += ":mobi-v1"
	}
	// The admitted engine domain is part of the selection readiness this fence
	// guards, so a build that enables another local format cannot reuse a page,
	// facet or saved witness computed without it.
	if readingcap.CB7() {
		value += ":cb7-v1"
	}
	if readingcap.CBR() {
		value += ":cbr-v1"
	}
	return value
}

func (r *readingBrowseScope) memberScope() string {
	return "m.library_id=" + strconv.FormatInt(r.library, 10) + " AND m.generation=" + strconv.FormatInt(r.generation.Ready, 10)
}

// eligibleSQL is the Reading list's exact publication/link/format choice. It
// intentionally retains an unavailable title: availability orders the primary
// publication but is not an extra visibility requirement.
func (r *readingBrowseScope) eligibleSQL(entity string) string {
	kind, formats := "20", r.sourceFormatsSQL()
	if r.kind == "comic" {
		kind = "21"
	}
	return `EXISTS(SELECT 1 FROM catalog_entities reading_title
 JOIN reading_publications reading_publication ON reading_publication.item_id=reading_title.id
 JOIN catalog_asset_links reading_link ON reading_link.entity_id=reading_title.id AND reading_link.asset_id=reading_publication.asset_id
 WHERE reading_title.id=` + entity + ` AND reading_title.library_id=` + strconv.FormatInt(r.library, 10) + `
 AND reading_title.kind=` + kind + ` AND reading_title.retired=0 AND reading_publication.format IN(` + formats + `) AND ` + readingcap.LocalComicArchiveSQL("reading_publication") + `
 AND reading_publication.asset_id=` + r.primaryPublicationSQL("reading_title.id") + `)`
}

func (r *readingBrowseScope) primaryPublicationSQL(item string) string {
	return `(SELECT candidate.asset_id FROM reading_publications candidate WHERE candidate.item_id=` + item + ` AND candidate.format IN(` + r.sourceFormatsSQL() + `) AND ` + readingcap.LocalComicArchiveSQL("candidate") + `
 ORDER BY EXISTS(SELECT 1 FROM inventory_item_availability available WHERE available.item_id=candidate.item_id
 AND available.asset_id=(SELECT token FROM catalog_assets WHERE id=candidate.asset_id) AND available.available=1) DESC,candidate.asset_id LIMIT 1)`
}

// predicate drives the same normalized ready members whether the caller is a
// normal page, an optimized query or a job selector. No fallback to audiobook
// columns or the generic accent/punctuation fold exists in this context.
func (r *readingBrowseScope) predicate(node BrowseNode, entity string) (string, []any, error) {
	if node.Field != "author" && node.Field != "series" {
		return "", nil, browseIssue("query.field", "not a Reading attribution field")
	}
	column := "m." + node.Field + "_key"
	base := `SELECT m.item_id FROM reading_attribution_members m WHERE ` + r.memberScope() + ` AND `
	positive := func(condition string) string {
		if r.point {
			return `EXISTS(` + base + `m.item_id=` + entity + ` AND ` + condition + `)`
		}
		return entity + ` IN(` + base + condition + `)`
	}
	negative := func(condition string) string {
		if r.point {
			return `NOT EXISTS(` + base + `m.item_id=` + entity + ` AND ` + condition + `)`
		}
		return entity + ` NOT IN(` + base + condition + `)`
	}
	normalize := func(v any) (string, error) {
		text, ok := v.(string)
		if !ok {
			return "", browseIssue("query.value", "Reading attribution requires a string")
		}
		return compactcatalog.ReadingValueKey(text), nil
	}
	switch node.Operator {
	case "is-present":
		return positive(column + " IS NOT NULL"), nil, nil
	case "is-missing":
		return negative(column + " IS NOT NULL"), nil, nil
	case "equals", "contains", "not-equals", "starts-with":
		value, err := normalize(node.Value)
		if err != nil {
			return "", nil, err
		}
		if node.Operator == "not-equals" {
			return negative(column + "=? COLLATE BINARY"), []any{value}, nil
		}
		if node.Operator == "starts-with" {
			return positive("substr(" + column + ",1,length(?))=? COLLATE BINARY"), []any{value, value}, nil
		}
		return positive(column + "=? COLLATE BINARY"), []any{value}, nil
	case "in", "not-in", "contains-any", "contains-all":
		values, ok := node.Value.([]any)
		if !ok || len(values) == 0 || len(values) > BrowseMaximumValues {
			return "", nil, browseIssue("query.value", "Reading attribution requires a bounded value list")
		}
		args := make([]any, 0, len(values))
		for _, v := range values {
			value, err := normalize(v)
			if err != nil {
				return "", nil, err
			}
			args = append(args, value)
		}
		if node.Operator == "contains-all" {
			parts := make([]string, len(args))
			for i := range parts {
				parts[i] = positive(column + "=? COLLATE BINARY")
			}
			return "(" + strings.Join(parts, " AND ") + ")", args, nil
		}
		condition := column + " IN(" + placeholders(len(args)) + ")"
		if node.Operator == "not-in" {
			return negative(condition), args, nil
		}
		return positive(condition), args, nil
	}
	return "", nil, browseIssue("query.operator", "operator is not supported for Reading attribution")
}

// Generic Reading browse keeps the existing signed cursor envelope and scope,
// but binds complete sort keys by digest rather than transmitting an unbounded
// stored sort title. The current eligible anchor reconstructs the exact keys.
// The separate ordered-series endpoint and PRDSER01 protocol are unchanged.
func readingBrowseBoundedKeys(keys []string) []string {
	raw, _ := json.Marshal(keys)
	return []string{fmt.Sprintf("reading-keys-v1:%x", sha256.Sum256(raw))}
}

func (s *Service) readingBrowseCursorKeys(where string, args []any, sorts []BrowseSortSelection, keys []string, anchor string) ([]string, error) {
	id, err := strconv.ParseInt(anchor, 10, 64)
	if err != nil || id <= 0 || strconv.FormatInt(id, 10) != anchor || len(keys) != 1 {
		return nil, ErrStaleContinuation
	}
	rows, err := s.queryBrowseRows(`SELECT `+browseRowColumns+` FROM catalog_browse_rows e JOIN catalog_entities ce ON ce.id=e.entity_id WHERE `+where+` AND e.entity_id=?`, append(append([]any{}, args...), id)...)
	if err != nil {
		return nil, err
	}
	if len(rows) != 1 {
		return nil, ErrStaleContinuation
	}
	full := make([]string, len(sorts))
	for i, sort := range sorts {
		full[i] = browseSortValue(rows[0], sort.Field)
	}
	if readingBrowseBoundedKeys(full)[0] != keys[0] {
		return nil, ErrStaleContinuation
	}
	return full, nil
}

// Metadata offering only. The Reading service still authorizes and validates
// every exact source acquisition; this setting never grants source access.
// Main supplies the outcome of the actual qualified converter constructor.
func (s *Service) ConfigureReadingMobi(available bool) {
	if s != nil && s.state != nil {
		s.state.readingMobi.Store(available)
	}
}
func (s *Service) readingMobiConfigured() bool {
	return s != nil && s.state != nil && s.state.readingMobi.Load()
}

// Batch group projection serializes this same captured set. The array and
// single-library SQL never infer format support from member metadata.
func (r *readingBrowseScope) sourceFormats() []string {
	if r.kind == "comic" {
		return readingcap.ComicFormats()
	}
	if r.mobi {
		return []string{"epub", "pdf", "mobi", "azw3"}
	}
	return []string{"epub", "pdf"}
}
func (r *readingBrowseScope) sourceFormatsSQL() string {
	formats := r.sourceFormats()
	for i := range formats {
		formats[i] = "'" + formats[i] + "'"
	}
	return strings.Join(formats, ",")
}
