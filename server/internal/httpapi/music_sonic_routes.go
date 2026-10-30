package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"
)

func decodeSonicSettings(w http.ResponseWriter, r *http.Request) (SonicSettingsChange, error) {
	var out SonicSettingsChange
	var fields map[string]json.RawMessage
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if err := lyricDecode(w, r, &fields); err != nil {
		return out, errStrictJSON
	}
	revision, ok := fields["expectedRevision"]
	if !ok || bytes.Equal(bytes.TrimSpace(revision), []byte("null")) {
		return out, errRevisionRequired
	}
	value, ok := fields["enabled"]
	if !ok || len(fields) != 2 || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
		return out, errStrictJSON
	}
	if json.Unmarshal(value, &out.Enabled) != nil || json.Unmarshal(revision, &out.ExpectedRevision) != nil || out.ExpectedRevision < 1 {
		return out, errStrictJSON
	}
	return out, nil
}

func decodeSonicRetry(w http.ResponseWriter, r *http.Request) error {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1024))
	if err != nil {
		return errStrictJSON
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return nil
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	var fields map[string]json.RawMessage
	if err = lyricDecode(w, r, &fields); err != nil || fields == nil || len(fields) != 0 {
		return errStrictJSON
	}
	return nil
}

func (d Dependencies) sonicAnalysisRoutes(mux *http.ServeMux) {
	for _, pattern := range []string{"GET /v1/admin/libraries/{id}/sonic-analysis", "PUT /v1/admin/libraries/{id}/sonic-analysis", "POST /v1/admin/libraries/{id}/sonic-analysis/retry"} {
		d.httpHandle(mux, pattern, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
			defer cancel()
			r = r.WithContext(ctx)
			// The mount's contract and the producer's transactional check both
			// enforce ownership. Refuse before parsing a mutation body as well.
			if _, err := d.owner(r); err != nil {
				failure(w, err)
				return
			}
			switch r.Method {
			case http.MethodGet:
				out, err := d.sonicStatusRequest(r)
				if err != nil {
					analysisFailure(w, err)
					return
				}
				write(w, 200, out)
			case http.MethodPut:
				change, err := decodeSonicSettings(w, r)
				if err != nil {
					failure(w, err)
					return
				}
				out, err := d.sonicSettingsRequest(r, change)
				if err != nil {
					analysisFailure(w, err)
					return
				}
				write(w, 200, out)
			case http.MethodPost:
				if err := decodeSonicRetry(w, r); err != nil {
					failure(w, err)
					return
				}
				out, err := d.sonicRetryRequest(r)
				if err != nil {
					analysisFailure(w, err)
					return
				}
				write(w, 200, out)
			}
		})
	}
}
