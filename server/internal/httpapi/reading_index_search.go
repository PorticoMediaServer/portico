package httpapi

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/contentaccess"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/reading"
)

// indexedReadingVisibility is the live ready Reading membership intersected
// with the canonical service visibility predicate (current principal, viewer,
// permitted request, authorized libraries and content restrictions). Both are
// evaluated inside the query snapshot; the optional single library narrows the
// membership. No request-time restriction snapshot is substituted.
func (d Dependencies) indexedReadingVisibility(service *reading.Service, library string) reading.Visibility {
	return func(ctx context.Context, tx *sql.Tx, p identity.Principal, itemSQL string) (string, []any, error) {
		if service == nil || service.Visible == nil {
			return "", nil, identity.ErrForbidden
		}
		filter, args, err := service.Visible(ctx, tx, p, itemSQL)
		if err != nil {
			return "", nil, err
		}
		if strings.TrimSpace(filter) == "" {
			return "", nil, identity.ErrForbidden
		}
		libraryClause := "1"
		membershipArgs := []any{compactcatalog.ReadingProjectionRuleVersion}
		if library != "" {
			libraryClause = "cl.library_id=?"
			membershipArgs = append(membershipArgs, library)
		}
		membership := `EXISTS(SELECT 1 FROM reading_attribution_members m
 JOIN reading_attribution_state st ON st.library_id=m.library_id
 JOIN catalog_libraries cl ON cl.id=m.library_id
 WHERE m.item_id=` + itemSQL + `
 AND m.generation=st.ready_generation AND st.ready_generation>0 AND st.input_generation=st.ready_generation
 AND st.publication_rule_version=? AND cl.retired=0 AND ` + libraryClause + `)`
		return "(" + filter + ") AND " + membership, append(args, membershipArgs...), nil
	}
}

// indexedReadingFence captures, inside the query snapshot, the live principal,
// content restrictions, currently authorized library set and ready
// namespace/input/ready/rule rows, plus the original optional library filter. It
// is hashed to a compact deterministic scope token (the search cursor limit is
// 512 bytes), and the same computation is repeated before the page is written.
func (d Dependencies) indexedReadingFence(service *reading.Service, p identity.Principal, library string) func(ctx context.Context, tx *sql.Tx) (string, error) {
	return func(ctx context.Context, tx *sql.Tx) (string, error) {
		if service == nil {
			return "", reading.ErrUnavailable
		}
		if d.Identity == nil {
			return "", identity.ErrUnauthorized
		}
		live, err := d.Identity.PrincipalByDigestTx(ctx, tx, p.Hash)
		if err != nil {
			return "", err
		}
		if live.Viewer != p.Viewer {
			return "", identity.ErrUnauthorized
		}
		if err = live.PermitsRequest(ctx); err != nil {
			return "", err
		}
		restrictions, err := contentaccess.RestrictionsTx(ctx, tx, live.Viewer)
		if err != nil {
			return "", err
		}
		base, err := d.browseFenceTx(ctx, tx, live, restrictions)
		if err != nil {
			return "", err
		}
		rows, err := tx.QueryContext(ctx, `SELECT library_id FROM catalog_libraries WHERE retired=0 ORDER BY library_id`)
		if err != nil {
			return "", err
		}
		libraries := []string{}
		for rows.Next() {
			var id string
			if err = rows.Scan(&id); err != nil {
				rows.Close()
				return "", err
			}
			if d.allowedLibraryTx(ctx, live, id, tx) == nil {
				libraries = append(libraries, id)
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return "", err
		}
		parts := []string{base, "formats=" + service.SourceFormatScope(), "library=" + library, "allowed=" + strings.Join(libraries, ",")}
		if len(libraries) > 0 {
			marks := strings.TrimSuffix(strings.Repeat("?,", len(libraries)), ",")
			args := make([]any, 0, len(libraries))
			for _, id := range libraries {
				args = append(args, id)
			}
			ready, err := tx.QueryContext(ctx, `SELECT cl.library_id,st.namespace,st.input_generation,st.ready_generation,st.publication_rule_version
 FROM catalog_libraries cl JOIN reading_attribution_state st ON st.library_id=cl.id
 WHERE cl.library_id IN (`+marks+`) ORDER BY cl.library_id`, args...)
			if err != nil {
				return "", err
			}
			for ready.Next() {
				var id string
				var namespace, input, readyGeneration, rule int64
				if err = ready.Scan(&id, &namespace, &input, &readyGeneration, &rule); err != nil {
					ready.Close()
					return "", err
				}
				parts = append(parts, fmt.Sprintf("%s:%d:%d:%d:%d", id, namespace, input, readyGeneration, rule))
			}
			err = ready.Err()
			ready.Close()
			if err != nil {
				return "", err
			}
		}
		return fmt.Sprintf("%x", sha256.Sum256([]byte(strings.Join(parts, "|")))), nil
	}
}

func (d Dependencies) indexedReadingFenceValue(ctx context.Context, service *reading.Service, p identity.Principal, library string) (string, error) {
	g, err := dbwork.BeginSnapshot(ctx, d.DB)
	if err != nil {
		return "", err
	}
	defer g.Rollback()
	return d.indexedReadingFence(service, p, library)(ctx, g.Tx())
}

func (d Dependencies) searchReadingIndex(w http.ResponseWriter, r *http.Request) {
	p, ok := d.readingPrincipal(w, r)
	if !ok {
		return
	}
	if d.admission != nil {
		done, ok := d.admission.acquireSearch(identity.PersonalKey(p.Viewer))
		if !ok {
			w.Header().Set("Retry-After", "1")
			write(w, 429, map[string]any{"error": map[string]any{"code": "search_busy", "message": "Too many searches are already running. Try again in a moment.", "retryable": true}})
			return
		}
		defer done()
	}
	limit := 20
	if raw := r.URL.Query().Get("limit"); raw != "" {
		var err error
		if limit, err = strconv.Atoi(raw); err != nil {
			readingFailure(w, reading.ErrInvalid)
			return
		}
	}
	library := r.URL.Query().Get("libraryId")
	service := d.readingService()
	visible := d.indexedReadingVisibility(service, library)
	fence := d.indexedReadingFence(service, p, library)
	page, err := service.SearchIndexedBody(r.Context(), reading.IndexedBodySearch{
		Query:     r.URL.Query().Get("q"),
		Limit:     limit,
		Cursor:    r.URL.Query().Get("cursor"),
		Library:   library,
		Principal: p,
		Visible:   visible,
		Fence:     fence,
		// The live SQL visibility predicate is the admission. This per-item
		// callback is defence in depth; the fresh batch proof below rechecks the
		// live scope and exact tuples before any hit is written.
		Authorize: func(context.Context, *sql.Tx, int64) (bool, error) { return true, nil },
	})
	if err != nil {
		readingFailure(w, err)
		return
	}
	if err = service.RecheckIndexedBody(r.Context(), p, visible, page.Hits); err != nil {
		readingFailure(w, err)
		return
	}
	nowFence, err := d.indexedReadingFenceValue(r.Context(), service, p, library)
	if err != nil {
		failure(w, err)
		return
	}
	if nowFence != page.Fence {
		policyError(w, "revision_mismatch")
		return
	}
	hits := make([]ReadingIndexedSearchHit, 0, len(page.Hits))
	for _, hit := range page.Hits {
		hits = append(hits, ReadingIndexedSearchHit{
			LibraryID:      hit.LibraryID,
			ItemID:         hit.ItemID,
			SourceID:       hit.SourceID,
			SourceRevision: hit.SourceRevision,
			Title:          hit.Title,
			CoverURL:       hit.CoverURL,
			SectionPath:    hit.SectionPath,
			SectionIndex:   hit.SectionIndex,
			UTF16Offset:    hit.UTF16Offset,
			Excerpt:        hit.Excerpt,
			MatchStart:     hit.MatchStart,
			MatchLength:    hit.MatchLength,
		})
	}
	write(w, 200, ReadingIndexedSearchResponse{
		IndexedOnly:   true,
		IndexRevision: page.IndexRevision,
		Anchor:        ReadingIndexedSearchAnchor{Normalization: readingIndexedNormalization, OffsetUnit: "utf16", MatchUnit: "utf16"},
		Hits:          hits,
		Next:          page.Next,
	})
}

// readingIndexedNormalization names the closed extraction contract, not just a
// whitespace pass over DOM textContent. See TEXT-INDEX.md: admitted spine XHTML
// body character data only, script/style/template excluded, one ASCII space at
// the start and end of each textBlock element, inline boundaries concatenated,
// then whitespace runs collapsed to one space and both edges trimmed.
const readingIndexedNormalization = "xhtml-body-blockspace-fields-v1"
