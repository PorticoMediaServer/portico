package httpapi

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
	librarychannels "github.com/PorticoMediaServer/portico/server/internal/livechannels/library"
)

// Capture a permission projection inside the same transaction as guide/config
// reads. Closures use this map, not nested queries while SQLite rows are open.
func (d Dependencies) libraryAuthority(expected identity.Principal) librarychannels.Authority {
	return func(ctx context.Context, tx *sql.Tx, owner bool) (librarychannels.Scope, error) {
		p, e := d.Identity.ReauthorizeTx(ctx, tx, expected)
		if e != nil {
			return librarychannels.Scope{}, livechannels.ErrDenied
		}
		localOwner := channelOwner(p)
		if localOwner {
			if e = d.ownerAuthorityTx(ctx, tx, p); e != nil {
				return librarychannels.Scope{}, livechannels.ErrDenied
			}
		}
		if owner && !localOwner {
			return librarychannels.Scope{}, livechannels.ErrDenied
		}
		if e = d.allowedLibraryTx(ctx, p, "", tx); e != nil {
			return librarychannels.Scope{}, livechannels.ErrDenied
		}
		rows, e := tx.QueryContext(ctx, `SELECT id FROM libraries WHERE NOT EXISTS(SELECT 1 FROM catalog_libraries gone WHERE gone.library_id=libraries.id AND gone.retired=1) ORDER BY id`)
		if e != nil {
			return librarychannels.Scope{}, librarychannels.ErrUnavailable
		}
		ids := []string{}
		for rows.Next() {
			var id string
			if rows.Scan(&id) != nil {
				rows.Close()
				return librarychannels.Scope{}, librarychannels.ErrUnavailable
			}
			ids = append(ids, id)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return librarychannels.Scope{}, librarychannels.ErrUnavailable
		}
		allowed := map[string]bool{}
		binding := p.ServerID + ":" + p.Hash + ":" + strconv.Itoa(p.Epoch)
		for _, id := range ids {
			e = d.allowedLibraryTx(ctx, p, id, tx)
			if e == nil {
				allowed[id] = true
				binding += ":" + id
			} else if !errors.Is(e, identity.ErrUnauthorized) {
				return librarychannels.Scope{}, librarychannels.ErrUnavailable
			}
		}
		h := sha256.Sum256([]byte(binding))
		// The lineup is shared; each profile's content restrictions turn a title it
		// may not watch into a slate in its own guide and refuse tuning into it.
		// The guide applies them through the principal's content fence (one set
		// query per window, C55), so no separate per-item callback is needed.
		return librarychannels.Scope{Fence: hex.EncodeToString(h[:]), Owner: localOwner, Principal: p, AllowsLibrary: func(id string) bool { return allowed[id] }}, nil
	}
}

func libraryFailure(w http.ResponseWriter, e error) {
	if writerRefused(w, e) {
		return
	}
	// A builder field that is wrong says which one and why (the owner's words, not a code).
	var issue *librarychannels.ValidationError
	if errors.As(e, &issue) {
		write(w, 400, map[string]any{"error": map[string]any{"code": "invalid_library_channel", "message": issue.Message, "path": issue.Path, "retryable": false}})
		return
	}
	status, code := 503, "library_channels_unavailable"
	switch {
	case errors.Is(e, librarychannels.ErrInvalid), errors.Is(e, livechannels.ErrInvalid):
		status, code = 400, "invalid_library_channel"
	case errors.Is(e, librarychannels.ErrConflict), errors.Is(e, livechannels.ErrConflict):
		status, code = 409, "library_channel_changed"
	case errors.Is(e, librarychannels.ErrInUse):
		status, code = 409, "channel_in_use"
	case errors.Is(e, librarychannels.ErrOverlay):
		status, code = 422, "overlay_unavailable"
	case errors.Is(e, librarychannels.ErrNoLibraries):
		status, code = 422, "library_channel_no_libraries"
	case errors.Is(e, librarychannels.ErrLocalTime):
		status, code = 422, "library_channel_timezone_invalid"
	case errors.Is(e, librarychannels.ErrTemplateEmpty):
		status, code = 422, "template_inapplicable"
	case errors.Is(e, identity.ErrUnauthorized), errors.Is(e, librarychannels.ErrDenied):
		liveFailure(w, e)
		return
	}
	message := "Library Channels are temporarily unavailable."
	if status == 503 {
		// The client can only say "try again"; the owner and support need the cause.
		log.Printf("library channels request failed (503): %v", e)
	}
	if status != 503 {
		message = publicErrorMessage(code)
	}
	write(w, status, map[string]any{"error": map[string]any{"code": code, "message": message, "retryable": status == 503}})
}

func (d Dependencies) libraryChannelRoutes(mux *http.ServeMux) {
	s := d.LibraryChannels
	if s == nil {
		return
	}
	slots := make(chan struct{}, 2)
	handle := func(owner bool, work func(context.Context, http.ResponseWriter, *http.Request, identity.Principal) (any, error)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			if r.URL.RawQuery != "" {
				libraryFailure(w, librarychannels.ErrInvalid)
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
			defer cancel()
			p, e := d.channelPrincipal(ctx, r, owner)
			if e != nil {
				libraryFailure(w, e)
				return
			}
			// Reads retain live authority checks and the shared bounded read
			// pool, while expensive mutations keep their two-slot resource fence.
			if r.Method != http.MethodGet {
				select {
				case slots <- struct{}{}:
					defer func() { <-slots }()
				case <-ctx.Done():
					closeUnreadUpload(w, r)
					failure(w, ctx.Err())
					return
				}
			}
			out, e := work(ctx, w, r, p)
			if e != nil {
				libraryFailure(w, e)
				return
			}
			current, e := d.channelPrincipal(ctx, r, owner)
			if e != nil || current != p {
				libraryFailure(w, librarychannels.ErrDenied)
				return
			}
			if ctx.Err() != nil {
				libraryFailure(w, librarychannels.ErrUnavailable)
				return
			}
			write(w, 200, out)
		}
	}
	envelope := func(key string, value any) any {
		return map[string]any{"protocolVersion": librarychannels.ProtocolVersion, "serverId": d.Identity.ID(), key: value}
	}
	d.httpHandle(mux, "GET /v1/admin/library-channels", handle(true, func(ctx context.Context, w http.ResponseWriter, r *http.Request, p identity.Principal) (any, error) {
		v, e := s.List(ctx, d.libraryAuthority(p))
		return envelope("channels", v), e
	}))
	d.httpHandle(mux, "POST /v1/admin/library-channels", handle(true, func(ctx context.Context, w http.ResponseWriter, r *http.Request, p identity.Principal) (any, error) {
		var in librarychannels.SaveInput
		if e := liveDecode(w, r, &in, 1<<20); e != nil {
			return nil, e
		}
		v, e := s.Save(ctx, d.libraryAuthority(p), in)
		return envelope("channel", v), e
	}))
	d.httpHandle(mux, "POST /v1/admin/library-channels/defaults", handle(true, func(ctx context.Context, w http.ResponseWriter, r *http.Request, p identity.Principal) (any, error) {
		var in struct {
			Timezone string `json:"timezone"`
		}
		if err := liveDecode(w, r, &in, 4096); err != nil {
			return nil, err
		}
		v, err := s.Defaults(ctx, d.libraryAuthority(p), in.Timezone)
		return envelope("config", v), err
	}))
	d.httpHandle(mux, "POST /v1/admin/library-channels/preview", handle(true, func(ctx context.Context, w http.ResponseWriter, r *http.Request, p identity.Principal) (any, error) {
		var in librarychannels.Config
		if e := liveDecode(w, r, &in, 1<<20); e != nil {
			return nil, e
		}
		v, e := s.Preview(ctx, d.libraryAuthority(p), in)
		return envelope("preview", v), e
	}))
	d.httpHandle(mux, "POST /v1/admin/library-channels/reorder", handle(true, func(ctx context.Context, w http.ResponseWriter, r *http.Request, p identity.Principal) (any, error) {
		var in librarychannels.ReorderInput
		if e := liveDecode(w, r, &in, 32768); e != nil {
			return nil, e
		}
		e := s.Reorder(ctx, d.libraryAuthority(p), in)
		return envelope("saved", e == nil), e
	}))
	d.httpHandle(mux, "POST /v1/admin/library-channels/{id}/regenerate", handle(true, func(ctx context.Context, w http.ResponseWriter, r *http.Request, p identity.Principal) (any, error) {
		var in struct {
			RequestID        string `json:"requestId"`
			ExpectedRevision int64  `json:"expectedRevision"`
			Boundary         string `json:"boundary"`
		}
		if e := liveDecode(w, r, &in, 4096); e != nil {
			return nil, e
		}
		v, e := s.Regenerate(ctx, d.libraryAuthority(p), r.PathValue("id"), librarychannels.Mutation{RequestID: in.RequestID, ExpectedRevision: in.ExpectedRevision}, in.Boundary)
		return envelope("channel", v), e
	}))
	d.httpHandle(mux, "POST /v1/admin/library-channels/{id}/delete", handle(true, func(ctx context.Context, w http.ResponseWriter, r *http.Request, p identity.Principal) (any, error) {
		var in librarychannels.Mutation
		if e := liveDecode(w, r, &in, 4096); e != nil {
			return nil, e
		}
		e := s.Delete(ctx, d.libraryAuthority(p), r.PathValue("id"), in)
		return envelope("deleted", e == nil), e
	}))
	d.httpHandle(mux, "GET /v1/admin/library-channels/templates", handle(true, func(ctx context.Context, w http.ResponseWriter, r *http.Request, p identity.Principal) (any, error) {
		v, e := s.Templates(ctx, d.libraryAuthority(p))
		return envelope("templates", v), e
	}))
	d.httpHandle(mux, "POST /v1/admin/library-channels/templates/{id}/install", handle(true, func(ctx context.Context, w http.ResponseWriter, r *http.Request, p identity.Principal) (any, error) {
		var in struct {
			RequestID string `json:"requestId"`
			Timezone  string `json:"timezone"`
		}
		if e := liveDecode(w, r, &in, 4096); e != nil {
			return nil, e
		}
		if in.Timezone == "" {
			// Older clients omit it; the server's own zone is a sane default the owner can change.
			in.Timezone = time.Local.String()
			if in.Timezone == "Local" || in.Timezone == "" {
				in.Timezone = "UTC"
			}
		}
		v, e := s.InstallTemplate(ctx, d.libraryAuthority(p), r.PathValue("id"), in.RequestID, in.Timezone)
		return envelope("channel", v), e
	}))
	d.httpHandle(mux, "POST /v1/library-channels/preferences", handle(false, func(ctx context.Context, w http.ResponseWriter, r *http.Request, p identity.Principal) (any, error) {
		var in livechannels.PreferenceInput
		if e := liveDecode(w, r, &in, 4096); e != nil {
			return nil, e
		}
		v, e := s.SetPreference(ctx, d.libraryAuthority(p), channelSubject(p), in)
		return envelope("revision", v), e
	}))
}
