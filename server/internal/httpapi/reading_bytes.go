package httpapi

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/PorticoMediaServer/portico/apikit/apierror"
	"github.com/PorticoMediaServer/portico/server/internal/contentaccess"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/reading"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// NewReadingService lets the server composition retain the same authorized
// service for HTTP, family retirement and shutdown. It creates no routes.
func NewReadingService(d Dependencies) *reading.Service {
	d.Reading = nil
	return d.readingService()
}

func (d Dependencies) readingService() *reading.Service {
	if d.Reading != nil {
		return d.Reading
	}
	return &reading.Service{DB: d.DB, Storage: d.Storage, Mounts: d.Mounts, Remote: d.RemoteSources, Changed: func(library, source string) {
		if d.Ingestion != nil {
			// A detected upstream replacement queues existing source-level
			// inventory reconciliation. Its real job/root/policy fences still
			// govern scanning; the reader receives no background decode grant.
			_, _ = d.Ingestion.QueueContext(context.Background(), library, source, nil)
		}
	}, SeriesScope: d.readingSeriesScopeTx, Authorize: func(ctx context.Context, tx *sql.Tx, p identity.Principal, library, item string) error {
		if d.Identity == nil {
			return identity.ErrUnauthorized
		}
		live, err := d.Identity.PrincipalByDigestTx(ctx, tx, p.Hash)
		if err != nil {
			return err
		}
		if live.Viewer != p.Viewer {
			return identity.ErrUnauthorized
		}
		if err = live.PermitsRequest(ctx); err != nil {
			return err
		}
		if err = d.allowedLibraryTx(ctx, live, library, tx); err != nil {
			return err
		}
		return contentaccess.VisibleItemTx(ctx, tx, live, item)
	}, Visible: func(ctx context.Context, tx *sql.Tx, p identity.Principal, itemSQL string) (string, []any, error) {
		if d.Identity == nil {
			return "", nil, identity.ErrUnauthorized
		}
		live, err := d.Identity.PrincipalByDigestTx(ctx, tx, p.Hash)
		if err != nil {
			return "", nil, err
		}
		if live.Viewer != p.Viewer {
			return "", nil, identity.ErrUnauthorized
		}
		if err = live.PermitsRequest(ctx); err != nil {
			return "", nil, err
		}
		return contentaccess.VisibleItemsSQL(ctx, tx, live, itemSQL, func(library string) error { return d.allowedLibraryTx(ctx, live, library, tx) })
	}}
}

func readingFailure(w http.ResponseWriter, err error) {
	// Services below Reading return already-registered public errors too, such
	// as an invalid offline receipt. Answer those with their definite status
	// and code; anything unregistered still falls through to the sanitized
	// internal failure below.
	var public *apierror.Error
	if errors.As(err, &public) {
		policyErrors.Write(w, w.Header().Get("X-Request-Id"), public)
		return
	}
	switch {
	case errors.Is(err, reading.ErrChanged), errors.Is(err, reading.ErrConflict), errors.Is(err, storage.ErrObservedSourceLost), errors.Is(err, storage.ErrRemoteChanged):
		policyError(w, "revision_mismatch")
	case errors.Is(err, reading.ErrUnavailable):
		// Match the concealed content-restriction response: availability must
		// not distinguish an absent publication from an inaccessible one.
		failure(w, sql.ErrNoRows)
	case errors.Is(err, reading.ErrInvalid):
		policyError(w, "invalid_request")
	case errors.Is(err, reading.ErrLimit):
		policyError(w, "body_too_large")
	case errors.Is(err, storage.ErrBusy):
		policyError(w, "request_in_progress")
	default:
		failure(w, err)
	}
}

func readingHeaders(w http.ResponseWriter, revision string) {
	w.Header().Set("ETag", `"`+revision+`"`)
	w.Header().Set("Cache-Control", "private, no-store, no-transform")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'; base-uri 'none'; form-action 'none'")
	w.Header().Set("Referrer-Policy", "no-referrer")
}

func (d Dependencies) openReadingBytes(w http.ResponseWriter, r *http.Request) (*reading.Source, bool) {
	return d.openReadingRepresentation(w, r, false)
}
func (d Dependencies) openReadingOriginalBytes(w http.ResponseWriter, r *http.Request) (*reading.Source, bool) {
	return d.openReadingRepresentation(w, r, true)
}

// The session/identity/source authorization is shared. Only the representation's
// exact revision differs: original bytes never borrow the derived coordinate ID.
func (d Dependencies) openReadingRepresentation(w http.ResponseWriter, r *http.Request, original bool) (*reading.Source, bool) {
	p, err := d.strictPrincipalFor(r)
	if err != nil {
		readingFailure(w, err)
		return nil, false
	}
	// A concrete publication revision is mandatory, including for HEAD. A
	// wildcard cannot establish which source the engine actually opened.
	match := r.Header.Get("If-Match")
	if match == "" {
		policyError(w, "revision_required")
		return nil, false
	}
	if len(r.Header.Values("If-Match")) != 1 || len(match) != 66 || match[0] != '"' || match[len(match)-1] != '"' {
		policyError(w, "revision_mismatch")
		return nil, false
	}
	sessionID := r.PathValue("sessionId")
	if sessionID == "" {
		sessionID = r.URL.Query().Get("sessionId")
	}
	f, err := d.readingService().OpenSession(r.Context(), p, sessionID)
	if err != nil {
		readingFailure(w, err)
		return nil, false
	}
	// Public routes identify the captured session. The earlier private handler
	// seam also names an item and source, which must agree when supplied.
	revision := f.PublicationSelection().Revision
	if original {
		revision = f.Revision
	}
	if (r.PathValue("id") != "" && f.ItemID != r.PathValue("id")) || (r.URL.Query().Has("sourceId") && f.SourceID != r.URL.Query().Get("sourceId")) || revision != match[1:len(match)-1] {
		f.Close()
		policyError(w, "revision_mismatch")
		return nil, false
	}
	readingHeaders(w, revision)
	return f, true
}

// readingRange accepts only one explicit finite byte interval. It never
// converts an unsupported range to a full download or shortens the declared
// interval. The client PDF adapter currently asks for 64 KiB chunks.
func readingRange(raw string, size int64) (start, end int64, ok bool) {
	if len(raw) > 96 || !strings.HasPrefix(raw, "bytes=") || strings.Contains(raw, ",") {
		return
	}
	a, b, found := strings.Cut(strings.TrimPrefix(raw, "bytes="), "-")
	if !found || a == "" || b == "" {
		return
	}
	for _, s := range []string{a, b} {
		for _, c := range s {
			if c < '0' || c > '9' {
				return
			}
		}
	}
	var err error
	start, err = strconv.ParseInt(a, 10, 64)
	if err != nil {
		return
	}
	end, err = strconv.ParseInt(b, 10, 64)
	ok = err == nil && start >= 0 && end >= start && end < size && end-start < 2<<20
	return
}

func (d Dependencies) serveReadingSource(w http.ResponseWriter, r *http.Request) {
	f, ok := d.openReadingBytes(w, r)
	if !ok {
		return
	}
	defer f.Close()
	if f.Format != "pdf" {
		policyError(w, "not_found")
		return
	}
	if f.Sequential() {
		d.serveReadingSequential(w, r, f)
		return
	}
	var header [5]byte
	if _, err := f.ReadAt(header[:], 0); err != nil {
		readingFailure(w, err)
		return
	}
	if string(header[:]) != "%PDF-" {
		readingFailure(w, f.StopAndRecheck(reading.ErrInvalid))
		return
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", `attachment; filename="publication.pdf"`)
	w.Header().Set("Accept-Ranges", "bytes")
	if r.Method == http.MethodHead {
		w.Header().Set("Content-Length", strconv.FormatInt(f.Size, 10))
		return
	}
	start, end, ok := readingRange(r.Header.Get("Range"), f.Size)
	if !ok {
		w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", f.Size))
		w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
		return
	}
	stream, err := f.PDFRange(start, end+1)
	if err != nil {
		readingFailure(w, err)
		return
	}
	// Read the first bounded block before committing headers, so revocation or
	// source replacement during setup is an error envelope, never a fake 206.
	first := make([]byte, min(int(end-start+1), 64<<10))
	if _, err = io.ReadFull(stream, first); err != nil {
		readingFailure(w, err)
		return
	}
	w.Header().Set("Content-Length", strconv.FormatInt(end-start+1, 10))
	w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, f.Size))
	body := withRollingDeadline(w)
	defer body.release()
	body.WriteHeader(http.StatusPartialContent)
	if _, err = body.Write(first); err != nil {
		return
	}
	if _, err = io.Copy(body, stream); err != nil {
		panic(http.ErrAbortHandler)
	}
}

// A factual range-unsupported PDF is a different transport: exactly one
// progressive200 from byte zero, no Range or ReaderAt/full-file buffering.
func (d Dependencies) serveReadingSequential(w http.ResponseWriter, r *http.Request, f *reading.Source) {
	if r.Header.Get("Range") != "" {
		w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", f.Size))
		w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
		return
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", `attachment; filename="publication.pdf"`)
	w.Header().Set("Accept-Ranges", "none")
	if r.Method == http.MethodHead {
		if err := f.CheckPDF(); err != nil {
			readingFailure(w, err)
			return
		}
		w.Header().Set("Content-Length", strconv.FormatInt(f.Size, 10))
		return
	}
	stream, err := f.OpenFromStart()
	if err != nil {
		readingFailure(w, err)
		return
	}
	defer stream.Close()
	firstSize := 64 << 10
	if f.Size < int64(firstSize) {
		firstSize = int(f.Size)
	}
	first := make([]byte, firstSize)
	if _, err = io.ReadFull(stream, first); err != nil {
		readingFailure(w, err)
		return
	}
	if len(first) < 5 || string(first[:5]) != "%PDF-" {
		readingFailure(w, f.StopAndRecheck(reading.ErrInvalid))
		return
	}
	if err = f.Validate(); err != nil {
		readingFailure(w, err)
		return
	}
	w.Header().Set("Content-Length", strconv.FormatInt(f.Size, 10))
	body := withRollingDeadline(w)
	defer body.release()
	body.WriteHeader(http.StatusOK)
	if _, err = body.Write(first); err != nil {
		return
	}
	if _, err = io.Copy(body, stream); err != nil {
		panic(http.ErrAbortHandler)
	}
}

func (d Dependencies) serveReadingAsset(w http.ResponseWriter, r *http.Request) {
	f, ok := d.openReadingBytes(w, r)
	if !ok {
		return
	}
	defer f.Close()
	if f.PublicationSelection().Format != "epub" {
		policyError(w, "not_found")
		return
	}
	e, err := f.EPUB()
	if err != nil {
		readingFailure(w, f.StopAndRecheck(err))
		return
	}
	found := false
	path := r.PathValue("asset")
	if path == "" {
		path = r.URL.Query().Get("path")
	}
	for _, resource := range e.Resources {
		if resource.Path == path {
			found = true
			break
		}
	}
	if !found {
		readingFailure(w, reading.ErrInvalid)
		return
	}
	b, mime, err := e.Asset(f.Context(), path)
	if err != nil {
		readingFailure(w, f.StopAndRecheck(err))
		return
	}
	serveReadingBlob(w, r, f, mime, b)
}

func (d Dependencies) serveReadingPage(w http.ResponseWriter, r *http.Request) {
	f, ok := d.openReadingBytes(w, r)
	if !ok {
		return
	}
	defer f.Close()
	if f.Format != "cbz" && f.Format != "cb7" && f.Format != "cbr" {
		policyError(w, "not_found")
		return
	}
	pageIndex := r.PathValue("pageIndex")
	if pageIndex == "" {
		pageIndex = r.PathValue("page")
	}
	index, err := strconv.Atoi(pageIndex)
	if err != nil || index < 0 {
		readingFailure(w, reading.ErrInvalid)
		return
	}
	width := 0
	if raw := r.URL.Query().Get("width"); raw != "" {
		width, err = strconv.Atoi(raw)
		if err != nil || width < 32 || width > 2048 {
			readingFailure(w, reading.ErrInvalid)
			return
		}
	}
	c, err := f.Comic()
	if err != nil {
		readingFailure(w, f.StopAndRecheck(err))
		return
	}
	if index >= len(c.Names) {
		readingFailure(w, reading.ErrInvalid)
		return
	}
	p, err := c.Page(f.Context(), index, width)
	if err != nil {
		readingFailure(w, f.StopAndRecheck(err))
		return
	}
	serveReadingBlob(w, r, f, p.Image.MIME, p.Bytes)
}

func serveReadingBlob(w http.ResponseWriter, r *http.Request, f *reading.Source, mime string, b []byte) {
	if err := f.Validate(); err != nil {
		readingFailure(w, err)
		return
	}
	revision := f.PublicationSelection().Revision
	readingHeaders(w, revision)
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Content-Length", strconv.Itoa(len(b)))
	if r.Header.Get("If-None-Match") == `"`+revision+`"` {
		w.Header().Del("Content-Length")
		w.WriteHeader(http.StatusNotModified)
		return
	}
	if r.Method == http.MethodHead {
		return
	}
	body := withRollingDeadline(w)
	defer body.release()
	reader := &guardedReader{Reader: bytes.NewReader(b), check: f.Validate}
	if _, err := io.Copy(body, reader); err != nil {
		panic(http.ErrAbortHandler)
	}
}
