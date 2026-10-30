package httpapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/lyrics"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"strconv"
	"time"
	"unicode/utf8"
)

// LyricsFetchJobKind is the console job kind registered for library lyric
// acquisition. The HTTP route and the scheduler registration must agree.
const LyricsFetchJobKind = "library-lyrics-fetch"

func lyricDecode(w http.ResponseWriter, r *http.Request, out any) error {
	typ, params, e := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if e != nil || typ != "application/json" || params["charset"] != "" && params["charset"] != "utf-8" {
		return lyrics.ErrInput
	}
	raw, e := io.ReadAll(http.MaxBytesReader(w, r.Body, 2<<20))
	if e != nil || !utf8.Valid(raw) {
		return lyrics.ErrInput
	}
	// Reject duplicate keys (including nested objects) before typed decoding.
	tokens := json.NewDecoder(bytes.NewReader(raw))
	if e = strictJSONValue(tokens, 0); e != nil {
		return lyrics.ErrInput
	}
	if _, e = tokens.Token(); e != io.EOF {
		return lyrics.ErrInput
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if e = d.Decode(out); e != nil {
		return lyrics.ErrInput
	}
	var trailing any
	if d.Decode(&trailing) != io.EOF {
		return lyrics.ErrInput
	}
	return requireRevisionJSON(raw, out)
}
func lyricFailure(w http.ResponseWriter, e error) {
	if writerRefused(w, e) {
		return
	}
	status, code := 0, ""
	switch {
	case errors.Is(e, lyrics.ErrConflict):
		status, code = 409, "lyrics_conflict"
	case errors.Is(e, lyrics.ErrSource):
		status, code = 409, "lyrics_source_changed"
	case errors.Is(e, lyrics.ErrInput):
		status, code = 400, "invalid_lyrics"
	case errors.Is(e, lyrics.ErrCapacity):
		status, code = 429, "lyrics_capacity"
	case errors.Is(e, lyrics.ErrUnavailable):
		status, code = 503, "lyrics_unavailable"
	}
	if status == 0 {
		failure(w, e)
		return
	}
	write(w, status, map[string]any{"error": map[string]any{"code": code, "message": publicErrorMessage(code), "retryable": status == 503}})
}

// lyricV1 maps a v1 session id in a lyrics target to the media session that
// presents it (NEW-35; playback_v1_presentation.go), read-only: the target
// then names the media session, and the lyrics store's session check runs with
// the binding that media session holds. Ownership is the v1 rule. An id that
// isn't a live v1 session of this viewer stays as it is (a legacy media id, or
// unknown and answered by the store as not found).
func (d Dependencies) lyricV1(ctx context.Context, a *lyrics.Access, t *lyrics.Target) (v1Presentation, bool, error) {
	if t.SessionID == "" {
		return v1Presentation{}, false, nil
	}
	p := identity.Principal{Viewer: identity.Viewer{Authority: a.Actor.Authority, AccountID: a.Actor.AccountID, ProfileID: a.Actor.ProfileID}, Hash: a.Actor.SessionHash}
	presentation, v1, e := d.resolveV1Presentation(ctx, p, t.SessionID)
	if e != nil || !v1 {
		return presentation, false, e
	}
	t.SessionID = presentation.media
	a.Actor.SessionHash = presentation.as(p).Hash
	return presentation, true, nil
}
func (d Dependencies) lyricAccess(r *http.Request) (lyrics.Access, error) {
	p, library, fence, e := d.detailAccess(r)
	if e != nil {
		return lyrics.Access{}, e
	}
	return lyrics.Access{Actor: lyrics.Actor{Authority: p.Authority, AccountID: p.AccountID, ProfileID: p.ProfileID, Owner: p.Authority == "local" && p.Role == "owner", SessionHash: p.Hash}, ServerID: d.Identity.ID(), LibraryID: library, ItemID: r.PathValue("id"), ViewerFence: fence, Authorize: func(tx *sql.Tx) error {
		if p.Authority == "local" && p.Role == "owner" {
			if err := d.manualMetadataAuthorize(r.Context(), tx, p); err != nil {
				return err
			}
		}
		return d.resourceAuthorize(tx, p, library)
	}}, nil
}
func (d Dependencies) lyricRoutes(mux *http.ServeMux) {
	rate := &personalLimiter{}
	routes := []struct{ pattern, action string }{
		{"GET /v1/items/{id}/lyrics", "list"},
		{"GET /v1/items/{id}/lyrics/{resource}/revisions/{revision}", "read"},
		{"POST /v1/items/{id}/lyrics", "replace"},
		{"PATCH /v1/items/{id}/lyrics/{resource}/offset", "offset"},
		{"DELETE /v1/items/{id}/lyrics/{resource}", "delete"},
		{"POST /v1/items/{id}/lyrics/search", "search"},
		{"PUT /v1/items/{id}/lyrics/selection", "selection"},
	}
	for _, route := range routes {
		d.httpHandle(mux, route.pattern, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			if d.Lyrics == nil {
				lyricFailure(w, lyrics.ErrUnavailable)
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
			defer cancel()
			r = r.WithContext(ctx)
			a, e := d.lyricAccess(r)
			if e != nil {
				lyricFailure(w, e)
				return
			}
			if r.Method != "GET" && !rate.allow(a.Actor.Authority+":"+a.Actor.AccountID+":"+a.Actor.ProfileID) {
				w.Header().Set("Retry-After", "60")
				lyricFailure(w, lyrics.ErrCapacity)
				return
			}
			var out any
			if r.Method == "GET" {
				q := r.URL.Query()
				for key, values := range q {
					if len(values) != 1 || len(values[0]) > 256 || (key != "sourceId" && key != "sourceVersion" && key != "sessionId") {
						lyricFailure(w, lyrics.ErrInput)
						return
					}
				}
				target := lyrics.Target{SourceID: q.Get("sourceId"), SourceVersion: q.Get("sourceVersion"), SessionID: q.Get("sessionId")}
				var presentation v1Presentation
				var v1 bool
				if presentation, v1, e = d.lyricV1(ctx, &a, &target); e != nil {
					lyricFailure(w, e)
					return
				}
				if route.action == "list" {
					var view lyrics.View
					view, e = d.Lyrics.View(ctx, a, target)
					if v1 && e == nil {
						// Answered in the v1 id and presentation generation the client holds.
						view.Scope.SessionID, view.Scope.SessionGeneration = presentation.session, int64(presentation.generation)
					}
					out = view
				} else {
					var rev int64
					rev, e = strconv.ParseInt(r.PathValue("revision"), 10, 64)
					if e != nil || rev < 1 || rev > 128 {
						lyricFailure(w, lyrics.ErrInput)
						return
					}
					out, e = d.Lyrics.Read(ctx, a, target, r.PathValue("resource"), rev)
				}
			} else {
				if r.URL.RawQuery != "" {
					lyricFailure(w, lyrics.ErrInput)
					return
				}
				switch route.action {
				case "search":
					var in lyrics.SearchInput
					if e = lyricDecode(w, r, &in); e == nil {
						if _, _, e = d.lyricV1(ctx, &a, &in.Target); e == nil {
							out, e = d.Lyrics.Search(ctx, a, in, d.LyricsProbe)
						}
					}
				case "selection":
					var in lyrics.Choose
					if e = lyricDecode(w, r, &in); e == nil {
						if _, _, e = d.lyricV1(ctx, &a, &in.Target); e == nil {
							out, e = d.Lyrics.Choose(ctx, a, in)
						}
					}
				default:
					var in lyrics.Mutation
					if e = lyricDecode(w, r, &in); e == nil {
						_, _, e = d.lyricV1(ctx, &a, &in.Target)
					}
					if e == nil {
						if pathID := r.PathValue("resource"); pathID != "" {
							if in.ResourceID != "" && in.ResourceID != pathID {
								lyricFailure(w, lyrics.ErrInput)
								return
							}
							in.ResourceID = pathID
						}
						out, e = d.Lyrics.Publish(ctx, a, in, route.action)
					}
				}
			}
			if e != nil {
				lyricFailure(w, e)
				return
			}
			after, e := d.lyricAccess(r)
			if e != nil {
				lyricFailure(w, e)
				return
			}
			if after.ViewerFence != a.ViewerFence || after.LibraryID != a.LibraryID {
				lyricFailure(w, identity.ErrNotVisible)
				return
			}
			write(w, 200, out)
		})
	}
	// Bulk acquisition is an owner action on a library, not on one item, and it
	// produces an ordinary console job so progress is visible where every other
	// background job already is.
	d.httpHandle(mux, "POST /v1/libraries/{id}/lyrics/fetch", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		// Owner first: a member is refused before anything about this server's
		// lyrics setup is revealed.
		p, e := d.owner(r)
		if e != nil {
			failure(w, e)
			return
		}
		if d.Lyrics == nil || d.LyricsBulk == nil || d.Scheduler == nil {
			lyricFailure(w, lyrics.ErrUnavailable)
			return
		}
		if r.URL.RawQuery != "" {
			lyricFailure(w, lyrics.ErrInput)
			return
		}
		var in struct {
			IdempotencyKey string `json:"idempotencyKey"`
		}
		if e = lyricDecode(w, r, &in); e != nil {
			lyricFailure(w, e)
			return
		}
		if e = d.allowedLibrary(r.Context(), p, r.PathValue("id")); e != nil {
			failure(w, e)
			return
		}
		job, e := d.Scheduler.Enqueue(r.Context(), p, d.consoleAuthority(p, true), operations.RunJob{Kind: LyricsFetchJobKind, Resource: r.PathValue("id"), IdempotencyKey: in.IdempotencyKey})
		if e != nil {
			consoleError(w, e)
			return
		}
		write(w, 202, job)
	})
	d.httpHandle(mux, "PUT /v1/lyrics/provider", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if d.Lyrics == nil {
			lyricFailure(w, lyrics.ErrUnavailable)
			return
		}
		p, e := d.owner(r)
		if e != nil {
			failure(w, e)
			return
		}
		var in struct {
			Enabled          bool  `json:"enabled"`
			ExpectedRevision int64 `json:"expectedRevision"`
		}
		if e = lyricDecode(w, r, &in); e != nil {
			lyricFailure(w, e)
			return
		}
		out, e := d.Lyrics.SetProvider(r.Context(), in.Enabled, in.ExpectedRevision, func(tx *sql.Tx) error {
			if e := d.manualMetadataAuthorize(r.Context(), tx, p); e != nil {
				return e
			}
			return d.resourceAuthorize(tx, p, "")
		})
		if e != nil {
			lyricFailure(w, e)
			return
		}
		write(w, 200, out)
	})
}
