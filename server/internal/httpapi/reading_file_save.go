package httpapi

import (
	"context"
	"database/sql"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/downloads"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/reading"
)

// This is an authenticated, from-start transfer, not a public download grant.
// The original Reader session owns every byte and closing it cancels the read.
// A disconnected client must explicitly start a new authenticated request; no
// ranges, redirects, detached leases or automatic replay are supported.
type ReadingFileSaveRequest struct {
	OperationID ReadingFileSaveOperation `json:"operationId"`
}

type ReadingFileSaveOperation string

func (ReadingFileSaveOperation) JSONSchema() map[string]any {
	return map[string]any{"type": "string", "pattern": "^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$"}
}

func (d Dependencies) readingFileSave(w http.ResponseWriter, r *http.Request) {
	var request ReadingFileSaveRequest
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if err := decode(w, r, &request); err != nil {
		readingFailure(w, err)
		return
	}
	// Correlation only: this read has no durable mutation or replay grant.
	if len(request.OperationID) != 36 || r.Header.Get("Range") != "" {
		readingFailure(w, reading.ErrInvalid)
		return
	}
	for i, c := range request.OperationID {
		if (i == 8 || i == 13 || i == 18 || i == 23) && c == '-' {
			continue
		}
		if i == 8 || i == 13 || i == 18 || i == 23 || !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			readingFailure(w, reading.ErrInvalid)
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()
	r = r.WithContext(ctx)
	p, ok := d.readingPrincipal(w, r)
	if !ok {
		return
	}
	f, ok := d.openReadingOriginalBytes(w, r)
	if !ok {
		return
	}
	defer f.Close()
	check := func() error {
		tx, err := d.DB.BeginTx(f.Context(), &sql.TxOptions{ReadOnly: true})
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if err = f.CheckSelectionTx(f.Context(), tx); err != nil {
			return err
		}
		allowed, err := downloads.ProfileAllowsDownloads(tx, p.Viewer)
		if err != nil {
			return err
		}
		if !allowed {
			return identity.ErrForbidden
		}
		return f.Context().Err()
	}
	if err := check(); err != nil {
		readingFailure(w, err)
		return
	}
	// The browser fallback is deliberately bounded. No large in-memory response
	// or native/offline entitlement is inferred from having a Reading session.
	if f.Size <= 0 || f.Size > 64<<20 {
		readingFailure(w, reading.ErrLimit)
		return
	}
	var stream io.ReadCloser
	if f.Sequential() {
		var err error
		stream, err = f.OpenFromStart()
		if err != nil {
			readingFailure(w, err)
			return
		}
		defer stream.Close()
	}
	// A fixed neutral name cannot mislabel an archive/converted publication's
	// physical bytes with the Reader's logical format or disclose a source path.
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="publication"`)
	w.Header().Set("Accept-Ranges", "none")
	w.Header().Set("X-Reading-Item", f.ItemID)
	w.Header().Set("X-Reading-Source", f.SourceID)
	w.Header().Set("Content-Length", strconv.FormatInt(f.Size, 10))
	body := withRollingDeadline(w)
	defer body.release()
	buffer := make([]byte, 64<<10)
	started := false
	fail := func(err error) {
		if started {
			panic(http.ErrAbortHandler)
		}
		w.Header().Del("Content-Length")
		w.Header().Del("Content-Disposition")
		readingFailure(w, err)
	}
	for offset := int64(0); offset < f.Size; {
		if err := check(); err != nil {
			fail(err)
			return
		}
		chunk := buffer
		if remaining := f.Size - offset; remaining < int64(len(chunk)) {
			chunk = chunk[:remaining]
		}
		var n int
		var err error
		if stream != nil {
			n, err = io.ReadFull(stream, chunk)
		} else {
			n, err = f.ReadAt(chunk, offset)
		}
		if err != nil && !(err == io.EOF && n == len(chunk)) {
			fail(err)
			return
		}
		if n != len(chunk) {
			fail(reading.ErrChanged)
			return
		}
		if err = check(); err == nil {
			err = f.Validate()
		}
		if err != nil {
			fail(err)
			return
		}
		started = true
		if _, err = body.Write(chunk); err != nil {
			return
		}
		offset += int64(n)
	}
}
