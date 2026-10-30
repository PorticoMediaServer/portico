package reading

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// TextIndexSearch is the private query seam over the persisted EPUB text index.
// Visible is mandatory: it returns the server-built visibility/scope predicate
// and its bound arguments for the column "s.item_id", using the same service
// visibility function an item acquisition uses. It is called in the same read
// snapshot before the query, and the predicate is applied before ORDER/LIMIT, so
// hidden or restricted matches never influence a page, a continuation or the
// work accounting. A session caller constrains it to its one actual item; a
// global caller supplies its actual authorized ready membership. Authorize
// remains as per-item defence in depth. Fence is mandatory too: it is the
// caller's opaque, stable token for the current viewer, restriction, library and
// session/catalog revision. A cursor minted under another fence is refused. The
// query text reaches SQLite only as a bound FTS parameter and as a literal
// re-check, never as SQL.
type TextIndexSearch struct {
	Query  string
	Limit  int
	Cursor string
	// Fence is a caller-supplied scope token; FenceFunc computes the same token
	// inside the query snapshot when the caller needs same-snapshot facts.
	Fence     string
	FenceFunc func(ctx context.Context, tx *sql.Tx) (string, error)
	Principal identity.Principal
	Visible   Visibility
	Authorize func(ctx context.Context, tx *sql.Tx, itemID int64) (bool, error)
	// Accept re-admits the caller's current authority and the captured
	// observation inside the query snapshot. A session search uses it to prove
	// the retained revision is still the current one, so a replacement refuses
	// late results instead of retargeting the latest source.
	Accept func(ctx context.Context, tx *sql.Tx) error
	// Observation pins the search to one captured publication observation. Nil
	// searches the current canonical primary version.
	Observation *TextIndexObservation
	// Project resolves the page's exact index tuples to public identity and
	// bounded presentation facts inside the query snapshot, in one batch.
	Project func(ctx context.Context, tx *sql.Tx, hits []TextIndexHit) ([]IndexedBodyHit, error)
}

// TextIndexObservation is one captured publication observation. ObjectID is
// the inventory object id and Revision its inventory revision, not the public
// publication revision.
type TextIndexObservation struct {
	ItemID   int64
	AssetID  int64
	ObjectID string
	Revision string
}

// TextIndexCoverage is the exact index state for the capture. An absent index is
// reported as pending, never as "no matches".
type TextIndexCoverage struct {
	Status string
	Reason string
}

// TextIndexHit is one exact literal hit inside an indexed section. Offsets are
// expressed in the normalized stored text, and Match is a UTF-16 range inside
// Excerpt so a client can mark it without extracting text from the server.
type TextIndexHit struct {
	ItemID int64
	// AssetID/ObjectID/Revision are the exact index observation keys. A global
	// page projection must resolve these exact keys, never another observation
	// that happens to share the asset token.
	AssetID      int64
	ObjectID     string
	Revision     string
	SectionPath  string
	SectionIndex int
	UTF16Offset  int
	Excerpt      string
	MatchStart   int
	MatchLength  int
}

type TextIndexResult struct {
	Hits          []TextIndexHit
	Next          string
	Coverage      TextIndexCoverage
	IndexRevision int64
	// Fence is the exact same-snapshot scope token the cursor was minted with.
	Fence string
	// Projected is filled inside the same snapshot by Project when the caller
	// needs public identity/title/cover for the page. It is never written later.
	Projected []IndexedBodyHit
}

type textIndexCursor struct {
	Version int    `json:"v"`
	Epoch   int64  `json:"e"`
	Query   string `json:"q"`
	Fence   string `json:"f"`
	Section int64  `json:"s"`
	Offset  int    `json:"o"`
}

type textIndexSpan struct {
	start, end int
}

const (
	textIndexExcerptRunes     = 160
	maxTextIndexRequest       = 16 << 20
	textIndexRequestBudget    = 2 * time.Second
	maxTextIndexScanPositions = 8 << 20
)

// textIndexSearchSQL keeps only a hit whose exact observation, publication,
// asset, source and availability are still current for the canonical primary
// version and this publication's own local book library. The index rule is
// required too, so a row awaiting re-preparation after a rule bump cannot
// answer. The caller's Authorize callback remains the principal/library/
// restriction admission; this is the shared eligibility join.
func (s *Service) textIndexSearchSQL(scope string, observation *TextIndexObservation) string {
	if observation != nil {
		return `SELECT s.id,s.item_id,s.asset_id,s.section_index,s.section_path,s.text,x.inventory_object_id,x.source_revision
 FROM reading_text_fts
 JOIN reading_text_sections s ON s.id=reading_text_fts.rowid
 JOIN reading_text_index_state x ON x.item_id=s.item_id AND x.asset_id=s.asset_id AND x.outcome='indexed' AND x.index_rule_version=?
 JOIN inventory_objects o ON o.id=x.inventory_object_id AND o.revision=x.source_revision AND o.retired=0 AND o.state='available' AND o.unsupported_reason=''
 JOIN catalog_assets a ON a.id=x.asset_id AND a.token=o.asset_id AND a.available=1 AND o.size=a.size AND o.modified_ns=a.modified_ns
 JOIN reading_publications p ON p.item_id=x.item_id AND p.asset_id=x.asset_id AND p.format='epub'
 JOIN catalog_asset_links link ON link.entity_id=x.item_id AND link.asset_id=x.asset_id AND link.available=1
 JOIN catalog_entities e ON e.id=x.item_id AND e.retired=0 AND e.kind=20
 JOIN catalog_libraries cl ON cl.id=e.library_id AND cl.retired=0
 JOIN libraries lib ON lib.id=cl.library_id AND lib.kind='book'
 JOIN library_sources src ON src.id=o.source_id AND src.library_id=cl.library_id AND src.incarnation=o.root_incarnation AND src.enabled=1 AND src.kind='local' AND src.health NOT IN('offline','root_changed','removing')
 JOIN inventory_item_availability available ON available.item_id=x.item_id AND available.asset_id=a.token AND available.available=1
 WHERE x.item_id=? AND x.asset_id=? AND x.inventory_object_id=? AND x.source_revision=?
 AND reading_text_fts MATCH ? AND reading_text_fts.rowid>=? AND (` + scope + `)
 ORDER BY reading_text_fts.rowid LIMIT ?`
	}
	return `SELECT s.id,s.item_id,s.asset_id,s.section_index,s.section_path,s.text,x.inventory_object_id,x.source_revision
 FROM reading_text_fts
 JOIN reading_text_sections s ON s.id=reading_text_fts.rowid
 JOIN reading_text_index_state x ON x.item_id=s.item_id AND x.asset_id=s.asset_id AND x.outcome='indexed' AND x.index_rule_version=?
 JOIN inventory_objects o ON o.id=x.inventory_object_id AND o.revision=x.source_revision AND o.retired=0 AND o.state='available' AND o.unsupported_reason=''
 JOIN catalog_assets a ON a.id=s.asset_id AND a.token=o.asset_id AND a.available=1 AND o.size=a.size AND o.modified_ns=a.modified_ns
 JOIN reading_publications p ON p.item_id=s.item_id AND p.asset_id=s.asset_id AND p.format='epub'
 JOIN catalog_asset_links link ON link.entity_id=s.item_id AND link.asset_id=s.asset_id AND link.available=1
 JOIN catalog_entities e ON e.id=s.item_id AND e.retired=0 AND e.kind=20
 JOIN catalog_libraries cl ON cl.id=e.library_id AND cl.retired=0
 JOIN libraries lib ON lib.id=cl.library_id AND lib.kind='book'
 JOIN library_sources src ON src.id=o.source_id AND src.library_id=cl.library_id AND src.incarnation=o.root_incarnation AND src.enabled=1 AND src.kind='local' AND src.health NOT IN('offline','root_changed','removing')
 JOIN inventory_item_availability available ON available.item_id=s.item_id AND available.asset_id=a.token AND available.available=1
 WHERE reading_text_fts MATCH ? AND reading_text_fts.rowid>=? AND p.asset_id=` + s.primaryPublicationSQL() + `
 AND o.id=` + publicationChosenObject + `
 AND (` + scope + `)
 ORDER BY reading_text_fts.rowid LIMIT ?`
}

// textIndexValidateQuery applies the literal-query and page bounds shared by
// every caller, before any format-specific work.
func textIndexValidateQuery(query string, limit int) (string, [][]rune, error) {
	if limit < 1 || limit > 50 {
		return "", nil, ErrInvalid
	}
	query = strings.TrimSpace(query)
	if query == "" || len(query) > 4096 || !utf8.ValidString(query) || utf16Count(query) > 1024 {
		return "", nil, ErrInvalid
	}
	tokens := textIndexTokens(query)
	if !textIndexSearchable(tokens) {
		return "", nil, ErrInvalid
	}
	return query, tokens, nil
}

// SearchTextIndex answers one literal, bounded page of the persisted EPUB text
// index. A single token matches a whole Unicode word; several tokens match a
// contiguous literal phrase. Every candidate is re-checked against the stored
// section text, so FTS tokenization or diacritic folding can only add candidate
// work, never an inexact hit. One request bounds both the section text it
// re-scans and its elapsed time; a page that stops early returns a continuation
// at its exact position instead of dropping or inventing hits.
func (s *Service) SearchTextIndex(ctx context.Context, search TextIndexSearch) (out TextIndexResult, err error) {
	if s == nil || s.DB == nil {
		return out, ErrUnavailable
	}
	if search.Authorize == nil || search.Visible == nil {
		return out, ErrInvalid
	}
	if search.Observation != nil && search.Accept == nil {
		return out, ErrInvalid
	}
	query, tokens, err := textIndexValidateQuery(search.Query, search.Limit)
	if err != nil {
		return out, err
	}
	fence := search.Fence
	if fence == "" && search.FenceFunc == nil {
		return out, ErrInvalid
	}
	if fence != "" && (len(fence) > 512 || !utf8.ValidString(fence)) {
		return out, ErrInvalid
	}
	cursor, err := textIndexDecodeCursor(search.Cursor)
	if err != nil {
		return out, err
	}
	gate, err := dbwork.BeginSnapshot(ctx, s.DB)
	if err != nil {
		return out, err
	}
	defer gate.Rollback()
	// Deferred before the scan so every page exit (limit, budget stop, scan cap
	// or exhaustion) projects inside this snapshot; it runs before the rollback.
	defer func() {
		if err != nil || search.Project == nil || len(out.Hits) == 0 {
			return
		}
		out.Projected, err = search.Project(ctx, gate.Tx(), out.Hits)
	}()
	if search.Accept != nil {
		if err = search.Accept(ctx, gate.Tx()); err != nil {
			return out, err
		}
	}
	if search.FenceFunc != nil {
		if fence, err = search.FenceFunc(ctx, gate.Tx()); err != nil {
			return out, err
		}
	}
	if fence == "" || len(fence) > 512 || !utf8.ValidString(fence) {
		return out, ErrInvalid
	}
	queryHash := textIndexQueryHash(query)
	fenceHash := textIndexQueryHash(fence)
	out.Fence = fence
	epoch, err := textIndexEpochTx(ctx, gate.Tx())
	if err != nil {
		return out, err
	}
	out.IndexRevision = epoch
	if search.Observation != nil {
		if out.Coverage, err = s.textIndexCoverageTx(ctx, gate.Tx(), *search.Observation); err != nil {
			return out, err
		}
	}
	if cursor != nil && (cursor.Epoch != epoch || cursor.Query != queryHash || cursor.Fence != fenceHash) {
		return out, ErrInvalid
	}

	var after int64
	afterOffset := -1
	if cursor != nil {
		after, afterOffset = cursor.Section, cursor.Offset
	}
	// The visibility predicate is part of the query, before ORDER/LIMIT, so only
	// admitted candidates can consume the scan window, the work budget or a
	// continuation.
	scope, scopeArgs, err := search.Visible(ctx, gate.Tx(), search.Principal, "s.item_id")
	if err != nil {
		return out, err
	}
	if strings.TrimSpace(scope) == "" {
		return out, ErrInvalid
	}
	statement := s.textIndexSearchSQL(scope, search.Observation)
	queryArgs := []any{readingIndexRuleVersion}
	if search.Observation != nil {
		queryArgs = append(queryArgs, search.Observation.ItemID, search.Observation.AssetID, search.Observation.ObjectID, search.Observation.Revision)
	}
	queryArgs = append(queryArgs, textIndexPhrase(tokens), after)
	queryArgs = append(queryArgs, scopeArgs...)
	queryArgs = append(queryArgs, maxTextIndexScan)

	authorized := map[int64]bool{}
	deadline := time.Now().Add(textIndexRequestBudget)
	positions := maxTextIndexScanPositions
	work := 0
	scanned := 0
	var processed int64
	processedAny := false
	rows, err := gate.Tx().QueryContext(ctx, statement, queryArgs...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		if err = ctx.Err(); err != nil {
			return out, err
		}
		var id, item, asset int64
		var index int
		var path, text, objectID, sourceRevision string
		if err = rows.Scan(&id, &item, &asset, &index, &path, &text, &objectID, &sourceRevision); err != nil {
			return out, err
		}
		scanned++
		// A -1 offset means this section was fully consumed by an earlier page;
		// a non-negative offset is the exact rune position to resume scanning.
		if cursor != nil && id == after && afterOffset == -1 {
			processed, processedAny = id, true
			continue
		}
		// Always process one section per request so an exhausted budget cannot
		// stall pagination; later sections wait for the next page.
		if processedAny && (work+len(text) > maxTextIndexRequest || time.Now().After(deadline)) {
			out.Next = textIndexEncodeCursor(textIndexCursor{Version: 1, Epoch: epoch, Query: queryHash, Fence: fenceHash, Section: processed, Offset: -1})
			return out, nil
		}
		work += len(text)
		allowed, known := authorized[item]
		if !known {
			allowed, err = search.Authorize(ctx, gate.Tx(), item)
			if err != nil {
				return out, err
			}
			authorized[item] = allowed
		}
		if !allowed {
			processed, processedAny = id, true
			continue
		}
		runes := []rune(text)
		folded := textIndexFold(runes)
		start := 0
		if cursor != nil && id == after && afterOffset > 0 {
			start = afterOffset
		}
		// UTF-16 offsets are counted once per rune in match order. Spans arrive
		// with non-decreasing starts, so countedRune only moves forward and no
		// hit re-scans or re-allocates its whole prefix.
		countedRune, countedUnits := 0, 0
		var countedErr error
		next, done, err := textIndexScanSpans(ctx, folded, tokens, start, deadline, &positions, func(span textIndexSpan) bool {
			for countedRune < span.start {
				if countedRune&4095 == 0 {
					if ctxErr := ctx.Err(); ctxErr != nil {
						countedErr = ctxErr
						return false
					}
				}
				countedUnits += utf16.RuneLen(runes[countedRune])
				countedRune++
			}
			excerpt, matchStart, length := textIndexExcerpt(runes, span)
			out.Hits = append(out.Hits, TextIndexHit{
				ItemID:       item,
				AssetID:      asset,
				ObjectID:     objectID,
				Revision:     sourceRevision,
				SectionPath:  path,
				SectionIndex: index,
				UTF16Offset:  countedUnits,
				Excerpt:      excerpt,
				MatchStart:   matchStart,
				MatchLength:  length,
			})
			return len(out.Hits) < search.Limit && !time.Now().After(deadline)
		})
		if countedErr != nil {
			return out, countedErr
		}
		if err != nil {
			return out, err
		}
		if !done {
			out.Next = textIndexEncodeCursor(textIndexCursor{Version: 1, Epoch: epoch, Query: queryHash, Fence: fenceHash, Section: id, Offset: next})
			return out, nil
		}
		processed, processedAny = id, true
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	if scanned == maxTextIndexScan && processed > 0 {
		out.Next = textIndexEncodeCursor(textIndexCursor{Version: 1, Epoch: epoch, Query: queryHash, Fence: fenceHash, Section: processed, Offset: -1})
	}
	return out, nil
}

// textIndexCoverageTx reports the exact determined outcome for a captured
// observation. An existing exact state wins even when the observation is now
// outside the background producer's domain. With no exact state, "pending" is
// only truthful for a local canonical-primary book EPUB, the one selection
// PrepareTextIndex is scheduled for, including the background canonical
// inventory object; a remote source or an out-of-window version is reported as
// unsupported/source or unsupported/version instead of implying work the worker
// will never schedule.
func (s *Service) textIndexCoverageTx(ctx context.Context, tx *sql.Tx, observation TextIndexObservation) (TextIndexCoverage, error) {
	var outcome, reason string
	err := tx.QueryRowContext(ctx, `SELECT outcome,reason FROM reading_text_index_state
 WHERE item_id=? AND asset_id=? AND inventory_object_id=? AND source_revision=? AND index_rule_version=?`,
		observation.ItemID, observation.AssetID, observation.ObjectID, observation.Revision, readingIndexRuleVersion).Scan(&outcome, &reason)
	if err == nil {
		return TextIndexCoverage{Status: outcome, Reason: reason}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return TextIndexCoverage{}, err
	}
	var local int
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM inventory_objects o
 JOIN library_sources src ON src.id=o.source_id AND src.incarnation=o.root_incarnation
 WHERE o.id=? AND o.revision=? AND src.kind='local')`, observation.ObjectID, observation.Revision).Scan(&local)
	if err != nil {
		return TextIndexCoverage{}, err
	}
	if local == 0 {
		return TextIndexCoverage{Status: "unsupported", Reason: "source"}, nil
	}
	// The producer window is narrower than "canonical asset": it also requires
	// the captured inventory object/revision to be the background canonical
	// object. A retained same-asset observation on another object is not
	// scheduled, so it is a version determination rather than pending. The
	// actual window SQL is reused rather than restated.
	var scheduled int
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_entities e
 CROSS JOIN catalog_libraries cl ON cl.id=e.library_id
 CROSS JOIN libraries lib ON lib.id=cl.library_id
 CROSS JOIN reading_publications p ON p.item_id=e.id AND p.asset_id=?
 CROSS JOIN catalog_asset_links link ON link.entity_id=e.id AND link.asset_id=p.asset_id
 CROSS JOIN catalog_assets a ON a.id=link.asset_id
 CROSS JOIN inventory_objects o ON o.asset_id=a.token
 CROSS JOIN library_sources s ON s.id=o.source_id AND s.library_id=cl.library_id
 WHERE e.id=? AND o.id=? AND o.revision=? AND `+textIndexWorkerDomain+` AND `+backgroundCanonicalObject+`
 AND p.asset_id=`+s.primaryPublicationSQL()+`)`,
		observation.AssetID, observation.ItemID, observation.ObjectID, observation.Revision).Scan(&scheduled)
	if err != nil {
		return TextIndexCoverage{}, err
	}
	if scheduled == 0 {
		return TextIndexCoverage{Status: "unsupported", Reason: "version"}, nil
	}
	return TextIndexCoverage{Status: "pending"}, nil
}

// textIndexTokens folds the trimmed query per rune. Per-rune lowercasing keeps
// the rune count, so rune offsets found in folded text are valid in the original
// stored text.
func textIndexTokens(query string) [][]rune {
	fields := strings.Fields(query)
	tokens := make([][]rune, 0, len(fields))
	for _, field := range fields {
		folded := textIndexFold([]rune(field))
		if len(folded) > 0 {
			tokens = append(tokens, folded)
		}
	}
	return tokens
}

// textIndexSearchable refuses a query that has no letter, number or mark. Such
// a phrase has no FTS token and would only burn a scan.
func textIndexSearchable(tokens [][]rune) bool {
	for _, token := range tokens {
		for _, r := range token {
			if textIndexWordRune(r) {
				return true
			}
		}
	}
	return false
}

func textIndexFold(runes []rune) []rune {
	folded := make([]rune, len(runes))
	for index, r := range runes {
		folded[index] = unicode.ToLower(r)
	}
	return folded
}

// textIndexPhrase builds an FTS5 phrase from already folded tokens. Quoting the
// whole phrase makes every FTS operator literal; the only escape needed is a
// doubled double quote.
func textIndexPhrase(tokens [][]rune) string {
	parts := make([]string, len(tokens))
	for index, token := range tokens {
		parts[index] = strings.ReplaceAll(string(token), `"`, `""`)
	}
	return `"` + strings.Join(parts, " ") + `"`
}

// textIndexScanSpans walks matches from start and calls visit for each. It
// allocates no span slice. Every few thousand examined positions it checks the
// caller context and the request deadline, and it counts positions against the
// per-request work budget, so a section with FTS candidates but no literal hit
// still stops at the bound. When it stops, next is the exact next unexamined
// rune position: resuming there neither repeats a match nor skips one. done is
// true only when the section was scanned to its end.
func textIndexScanSpans(ctx context.Context, folded []rune, tokens [][]rune, start int, deadline time.Time, positions *int, visit func(textIndexSpan) bool) (next int, done bool, err error) {
	if len(tokens) == 0 {
		return len(folded), true, nil
	}
	first := tokens[0]
	index := start
	if index < 0 {
		index = 0
	}
	for iteration := 0; index+len(first) <= len(folded); iteration++ {
		// Always examine at least one position so a nearly expired deadline
		// cannot return the same continuation forever.
		if iteration > 0 && iteration&4095 == 0 {
			if err = ctx.Err(); err != nil {
				return index, false, err
			}
			if time.Now().After(deadline) {
				return index, false, nil
			}
		}
		if positions != nil {
			if *positions <= 0 {
				return index, false, nil
			}
			*positions--
		}
		if !textIndexMatchAt(folded, index, first) {
			index++
			continue
		}
		end := index + len(first)
		if len(tokens) == 1 {
			if textIndexWordBefore(folded, index) || textIndexWordAfter(folded, end) {
				index++
				continue
			}
			if !visit(textIndexSpan{index, end}) {
				return index + 1, false, nil
			}
			index++
			continue
		}
		ok := true
		for _, token := range tokens[1:] {
			after := end
			space := false
			for after < len(folded) && unicode.IsSpace(folded[after]) {
				after++
				space = true
			}
			if !space || !textIndexMatchAt(folded, after, token) {
				ok = false
				break
			}
			end = after + len(token)
		}
		if !ok || textIndexWordBefore(folded, index) || textIndexWordAfter(folded, end) {
			index++
			continue
		}
		if !visit(textIndexSpan{index, end}) {
			return end, false, nil
		}
		index = end
	}
	return len(folded), true, nil
}

func textIndexMatchAt(folded []rune, offset int, token []rune) bool {
	if offset < 0 || offset+len(token) > len(folded) {
		return false
	}
	for index, r := range token {
		if folded[offset+index] != r {
			return false
		}
	}
	return true
}

func textIndexWordBefore(folded []rune, offset int) bool {
	return offset > 0 && textIndexWordRune(folded[offset-1])
}

func textIndexWordAfter(folded []rune, offset int) bool {
	return offset < len(folded) && textIndexWordRune(folded[offset])
}

func textIndexWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsNumber(r) || unicode.IsMark(r)
}

// textIndexExcerpt returns a bounded window around one match plus the match's
// UTF-16 range inside that window.
func textIndexExcerpt(runes []rune, span textIndexSpan) (string, int, int) {
	start := span.start - textIndexExcerptRunes
	if start < 0 {
		start = 0
	}
	end := span.end + textIndexExcerptRunes
	if end > len(runes) {
		end = len(runes)
	}
	excerpt := string(runes[start:end])
	prefix := 0
	if start > 0 {
		excerpt = "…" + excerpt
		prefix = 1
	}
	if end < len(runes) {
		excerpt += "…"
	}
	return excerpt, prefix + utf16Count(string(runes[start:span.start])), utf16Count(string(runes[span.start:span.end]))
}

func textIndexQueryHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:12])
}

func textIndexEncodeCursor(cursor textIndexCursor) string {
	encoded, _ := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(encoded)
}

func textIndexDecodeCursor(value string) (*textIndexCursor, error) {
	if value == "" {
		return nil, nil
	}
	if len(value) > 512 {
		return nil, ErrInvalid
	}
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(raw) > 384 {
		return nil, ErrInvalid
	}
	var cursor textIndexCursor
	if err = json.Unmarshal(raw, &cursor); err != nil || cursor.Version != 1 || cursor.Epoch < 0 || cursor.Section < 0 || cursor.Offset < -1 || cursor.Query == "" || cursor.Fence == "" {
		return nil, ErrInvalid
	}
	return &cursor, nil
}
