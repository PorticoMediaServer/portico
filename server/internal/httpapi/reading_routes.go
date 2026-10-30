package httpapi

import (
	"database/sql"
	"net/http"
	"net/url"
	"slices"
	"strconv"

	"github.com/PorticoMediaServer/portico/server/internal/downloads"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/reading"
)

func readingQuery(r *http.Request, allowed ...string) bool {
	if len(r.URL.RawQuery) > 8192 {
		return false
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return false
	}
	for name, values := range query {
		if len(values) != 1 || !slices.Contains(allowed, name) {
			return false
		}
	}
	return true
}

func (d Dependencies) readingRoutes(mux *http.ServeMux) {
	register := func(pattern string, query []string, handler http.HandlerFunc) {
		d.httpHandle(mux, pattern, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "private, no-store")
			if !readingQuery(r, query...) {
				readingFailure(w, reading.ErrInvalid)
				return
			}
			handler(w, r)
		})
	}
	register("GET /v1/libraries/{libraryId}/reading/groups/{groupKind}/{seriesId}/my-list", nil, d.readingSavedGroup)
	register("PUT /v1/libraries/{libraryId}/reading/groups/{groupKind}/{seriesId}/my-list", nil, d.readingSetSavedGroup(&personalLimiter{}))
	register("DELETE /v1/reading/saved-groups/{referenceId}", nil, d.readingRemoveSavedGroup(&personalLimiter{}))
	register("GET /v1/items/{itemId}/reading/favorite", nil, d.readingFavorite)
	register("PUT /v1/items/{itemId}/reading/favorite", nil, d.readingSetFavorite(&personalLimiter{}))
	register("GET /v1/items/{itemId}/reading/my-list", nil, d.readingSaved)
	register("PUT /v1/items/{itemId}/reading/my-list", nil, d.readingSetSaved(&personalLimiter{}))
	register("GET /v1/items/{itemId}/reading/mark", nil, d.readingMark)
	register("PUT /v1/items/{itemId}/reading/mark", nil, d.readingSetMark(&personalLimiter{}))
	register("GET /v1/libraries/{libraryId}/reading/mark-all", nil, d.readingMarkAllProjection)
	register("POST /v1/libraries/{libraryId}/reading/mark-all", nil, d.readingSetMarkAll(&personalLimiter{}))
	register("GET /v1/reading/items", []string{"libraryId", "cursor", "limit"}, d.listReadingItems)
	register("GET /v1/reading/search", []string{"q", "cursor", "limit", "libraryId"}, d.searchReadingIndex)
	register("GET /v1/reading/discover", []string{"libraryId", "limit", "seriesLimit", "shortReadsLimit", "newIssuesLimit", "newspaperLimit", "row", "cursor"}, d.readingDiscover)
	register("GET /v1/libraries/{libraryId}/reading/series/{seriesId}", []string{"cursor", "limit"}, d.readingSeries)
	register("GET /v1/libraries/{libraryId}/reading/periodicals", []string{"publicationType", "cursor", "limit"}, d.readingPeriodicalGroups)
	register("GET /v1/libraries/{libraryId}/reading/periodicals/{seriesId}/issues", []string{"publicationType", "issueDate", "year", "month", "order", "cursor", "limit"}, d.readingPeriodicalIssues)
	register("GET /v1/reading/continue", []string{"libraryId", "cursor", "limit"}, d.readingContinue)
	register("GET /v1/items/{itemId}/reading/versions", nil, d.readingVersions)
	register("POST /v1/items/{itemId}/reading/reread", nil, d.readingReread)
	register("POST /v1/reading/offline/progress", nil, d.readingOfflineProgress)
	register("POST /v1/reading/offline/progress/read", nil, d.readingOfflineProgressRead)
	register("POST /v1/reading/sessions", nil, d.prepareReadingSession)
	register("POST /v1/reading/sessions/{sessionId}/file-save", nil, d.readingFileSave)
	register("GET /v1/reading/sessions/{sessionId}/annotations", []string{"cursor", "revision"}, d.readingAnnotations)
	register("PUT /v1/reading/sessions/{sessionId}/annotations", nil, d.readingMutateAnnotation)
	register("GET /v1/reading/sessions/{sessionId}/progress", nil, d.readingSessionProgress)
	register("GET /v1/reading/sessions/{sessionId}/search", []string{"q", "cursor", "limit"}, d.searchReadingSession)
	register("PUT /v1/reading/sessions/{sessionId}/progress", nil, d.readingSaveSessionProgress)
	register("DELETE /v1/reading/sessions/{sessionId}", nil, d.closeReadingSession)
	register("GET /v1/reading/sessions/{sessionId}/source", nil, d.serveReadingSource)
	register("HEAD /v1/reading/sessions/{sessionId}/source", nil, d.serveReadingSource)
	register("GET /v1/reading/sessions/{sessionId}/assets", []string{"path"}, d.serveReadingAsset)
	register("GET /v1/reading/sessions/{sessionId}/pages/{pageIndex}", []string{"width"}, d.serveReadingPage)
}

func (d Dependencies) readingPrincipal(w http.ResponseWriter, r *http.Request) (identity.Principal, bool) {
	p, err := d.strictPrincipalFor(r)
	if err != nil {
		readingFailure(w, err)
		return p, false
	}
	return p, true
}

// readingItemProjection publishes only the accepted attribution facts already
// selected in this same snapshot. It never derives from archive metadata,
// titles or paths and never re-reads per item.
func readingItemProjection(row reading.LibraryRow) ReadingItem {
	out := ReadingItem{CoverURL: row.CoverURL, ID: row.ID, LibraryID: row.LibraryID, Title: row.Title, Format: ReadingSourceFormat(row.Format), SourceID: row.SourceID, InventoryAvailable: row.InventoryAvailable}
	if row.MarkKnown {
		out.ReadingMark = &ReadingItemMark{ItemID: row.ID, Read: row.MarkRead, Revision: row.MarkRevision}
	}
	if row.PublicationType != nil {
		out.PublicationType = &ReadingPublicationTypeValue{Value: ReadingPublicationType(row.PublicationType.Value), Source: ReadingPublicationTypeSource(row.PublicationType.Source)}
	}
	if row.Author != nil {
		out.Author = &ReadingAuthor{Value: row.Author.Value, Source: ReadingAttributionSource(row.Author.Source)}
	}
	if row.Series != nil {
		series := &ReadingSeries{ID: row.Series.ID, Name: row.Series.Name, Source: ReadingAttributionSource(row.Series.Source)}
		if row.Series.Number != nil {
			number := *row.Series.Number
			series.Number = &number
		}
		out.Series = series
	}
	return out
}

func (d Dependencies) listReadingItems(w http.ResponseWriter, r *http.Request) {
	p, ok := d.readingPrincipal(w, r)
	if !ok {
		return
	}
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		var err error
		if limit, err = strconv.Atoi(raw); err != nil {
			readingFailure(w, reading.ErrInvalid)
			return
		}
	}
	page, err := d.readingService().List(r.Context(), p, r.URL.Query().Get("libraryId"), r.URL.Query().Get("cursor"), limit)
	if err != nil {
		readingFailure(w, err)
		return
	}
	items := []ReadingItem{}
	for _, item := range page.Items {
		items = append(items, readingItemProjection(item))
	}
	if items, err = readingBudget(items, readingItemsPageBytes, page.Truncate); err != nil {
		readingFailure(w, err)
		return
	}
	write(w, 200, ReadingItemsResponse{Items: items, NextCursor: page.NextCursor})
}

func (d Dependencies) readingContinue(w http.ResponseWriter, r *http.Request) {
	p, ok := d.readingPrincipal(w, r)
	if !ok {
		return
	}
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		var err error
		if limit, err = strconv.Atoi(raw); err != nil {
			readingFailure(w, reading.ErrInvalid)
			return
		}
	}
	page, err := d.readingService().Continue(r.Context(), p, r.URL.Query().Get("libraryId"), r.URL.Query().Get("cursor"), limit)
	if err != nil {
		readingFailure(w, err)
		return
	}
	items := []ReadingContinueItem{}
	for _, row := range page.Items {
		v := row.Item
		items = append(items, ReadingContinueItem{Conversion: readingConversionProjection(row.Conversion), Item: readingItemProjection(v), Progress: *readingProgressProjection(&row.Progress)})
	}
	if items, err = readingBudget(items, readingPageBytes, page.Truncate); err != nil {
		readingFailure(w, err)
		return
	}
	write(w, 200, ReadingContinueResponse{Items: items, NextCursor: page.NextCursor})
}

func (d Dependencies) readingVersions(w http.ResponseWriter, r *http.Request) {
	p, ok := d.readingPrincipal(w, r)
	if !ok {
		return
	}
	choice, err := d.readingService().VersionChoice(r.Context(), p, r.PathValue("itemId"))
	if err != nil {
		readingFailure(w, err)
		return
	}
	out := ReadingVersionsResponse{ChosenSourceID: choice.Chosen, Versions: []ReadingVersion{}}
	for _, version := range choice.Versions {
		out.Versions = append(out.Versions, ReadingVersion{version.SourceID, ReadingSourceFormat(version.Format), version.Size, version.InventoryAvailable})
	}
	write(w, 200, out)
}

func (d Dependencies) prepareReadingSession(w http.ResponseWriter, r *http.Request) {
	p, ok := d.readingPrincipal(w, r)
	if !ok {
		return
	}
	var request ReadingSessionRequest
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if err := decode(w, r, &request); err != nil {
		readingFailure(w, err)
		return
	}
	if request.ItemID == "" || request.SourceID == "" {
		readingFailure(w, reading.ErrInvalid)
		return
	}
	session, document, err := d.readingService().Prepare(r.Context(), p, request.ItemID, request.SourceID)
	if err != nil {
		readingFailure(w, err)
		return
	}
	v, m := document.Selection, document.Metadata
	out := ReadingSessionResponse{CanAnnotate: reading.CanAnnotate(p), Sequential: document.Sequential, SessionID: session.ID, ItemID: v.ItemID, SourceID: v.SourceID, Revision: v.Revision, Format: ReadingFormat(v.Format), Size: v.Size, Metadata: ReadingMetadata{Title: m.Title, Author: m.Author, Language: m.Language, Direction: ReadingDirection(m.Direction), CoverURL: m.CoverURL}, Spine: document.Spine, PageNames: document.PageNames}
	out.Conversion = readingConversionProjection(document.Conversion)
	readingManifestFactsProjection(&out.Metadata, m)
	// This optional offer is descriptive only; the transfer rechecks the exact
	// captured source and live download policy before and after each extent.
	saveSize := v.Size
	if document.Conversion != nil {
		saveSize = document.Conversion.OriginalSize
	}
	if saveSize > 0 && saveSize <= 64<<20 {
		tx, err := d.DB.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true})
		if err == nil {
			out.CanSaveFile, _ = downloads.ProfileAllowsDownloads(tx, p.Viewer)
			tx.Rollback()
		}
	}
	if document.PublicationType != nil {
		out.Metadata.PublicationType = &ReadingPublicationTypeValue{Value: ReadingPublicationType(document.PublicationType.Value), Source: ReadingPublicationTypeSource(document.PublicationType.Source)}
	}
	for _, entry := range document.Contents {
		out.Contents = append(out.Contents, ReadingContentsEntry{entry.Title, entry.Section, entry.Fragment, entry.Depth})
	}
	for _, resource := range document.Resources {
		out.Resources = append(out.Resources, ReadingResource{resource.Path, resource.MIME, resource.Size})
	}
	readingHeaders(w, v.Revision)
	write(w, 200, out)
}

func readingProgressProjection(progress *reading.Progress) *ReadingProgress {
	if progress == nil {
		return nil
	}
	l := progress.Location
	return &ReadingProgress{progress.SourceID, progress.SourceRevision, ReadingLocation{ReadingFormat(l.Format), l.CFI, l.Section, l.PageIndex, l.Progression}, progress.Revision, progress.ObservedAt, progress.Stale}
}

func (d Dependencies) readingSessionProgress(w http.ResponseWriter, r *http.Request) {
	f, ok := d.openReadingBytes(w, r)
	if !ok {
		return
	}
	defer f.Close()
	progress, err := f.Progress()
	if err != nil {
		readingFailure(w, err)
		return
	}
	if err = f.Validate(); err != nil {
		readingFailure(w, err)
		return
	}
	write(w, 200, ReadingProgressResponse{readingProgressProjection(progress)})
}

func (d Dependencies) searchReadingSession(w http.ResponseWriter, r *http.Request) {
	p, ok := d.readingPrincipal(w, r)
	if !ok {
		return
	}
	// Literal section search is CPU-heavy. Reuse the existing per-viewer and
	// global search cap rather than adding a reader-specific limiter.
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
	beforeFence, err := d.browseFence(r, p)
	if err != nil {
		failure(w, err)
		return
	}
	result, err := d.readingService().SearchSession(r.Context(), p, r.PathValue("sessionId"),
		r.URL.Query().Get("q"), r.URL.Query().Get("cursor"), limit)
	if err != nil {
		readingFailure(w, err)
		return
	}
	// The service rechecks the retained actor/source/version and the live
	// visibility predicate in a fresh snapshot. The handler separately compares
	// its viewer/restriction fence before writing, so a scope change between the
	// two reads refuses the page instead of publishing it under the old scope.
	// browseFence is not a catalogue-generation proof.
	afterFence, err := d.browseFence(r, p)
	if err != nil {
		failure(w, err)
		return
	}
	if afterFence != beforeFence {
		policyError(w, "revision_mismatch")
		return
	}
	hits := make([]ReadingSearchHit, 0, len(result.Hits))
	for _, hit := range result.Hits {
		hits = append(hits, ReadingSearchHit{
			SectionPath:  hit.SectionPath,
			SectionIndex: hit.SectionIndex,
			UTF16Offset:  hit.UTF16Offset,
			Excerpt:      hit.Excerpt,
			MatchStart:   hit.MatchStart,
			MatchLength:  hit.MatchLength,
		})
	}
	readingHeaders(w, result.SourceRevision)
	write(w, 200, ReadingSearchResponse{
		SessionID:      result.SessionID,
		SourceID:       result.SourceID,
		SourceRevision: result.SourceRevision,
		IndexRevision:  result.IndexRevision,
		Coverage:       ReadingSearchCoverage{Status: result.Coverage.Status, Reason: result.Coverage.Reason},
		Hits:           hits,
		Next:           result.Next,
	})
}

func (d Dependencies) readingSaveSessionProgress(w http.ResponseWriter, r *http.Request) {
	f, ok := d.openReadingBytes(w, r)
	if !ok {
		return
	}
	defer f.Close()
	var request ReadingProgressRequest
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	if err := decode(w, r, &request); err != nil {
		readingFailure(w, err)
		return
	}
	l := request.Location
	progress, saved, err := f.SaveProgress(request.ExpectedRevision, reading.Location{Format: string(l.Format), CFI: l.CFI, Section: l.Section, PageIndex: l.PageIndex, Progression: l.Progression})
	if err != nil {
		readingFailure(w, err)
		return
	}
	if err = f.Validate(); err != nil {
		readingFailure(w, err)
		return
	}
	write(w, 200, ReadingProgressSavedResponse{readingProgressProjection(progress), saved})
}

func (d Dependencies) closeReadingSession(w http.ResponseWriter, r *http.Request) {
	p, ok := d.readingPrincipal(w, r)
	if !ok {
		return
	}
	if err := d.readingService().CloseSessionContext(r.Context(), p, r.PathValue("sessionId")); err != nil {
		readingFailure(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
