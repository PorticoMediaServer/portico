package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
	"strconv"
	"strings"
	"time"
)

// Source administration requires a current server owner. Consumers use
// current identity plus the cached server membership policy.
func channelOwner(p identity.Principal) bool {
	return p.Role == "owner" && p.Authority == "local"
}
func channelSubject(p identity.Principal) livechannels.Owner {
	return livechannels.Owner{Authority: p.Authority, AccountID: p.AccountID, ProfileID: p.ProfileID}
}
func (d Dependencies) channelPrincipal(ctx context.Context, r *http.Request, owner bool) (identity.Principal, error) {
	headers := r.Header.Values("Authorization")
	if len(headers) != 1 || !strings.HasPrefix(headers[0], "Bearer ") {
		return identity.Principal{}, identity.ErrUnauthorized
	}
	token := strings.TrimPrefix(headers[0], "Bearer ")
	p, e := d.Identity.AuthenticateContext(ctx, token)
	if e != nil {
		return p, e
	}
	if owner {
		return d.ownerContext(ctx, r)
	}
	if e = d.featureAllowed(ctx, p, requestFeature(r)); e == nil {
		d.admission.rememberCredential(token, p)
	}
	return p, e
}
func (d Dependencies) liveAuthority(expected identity.Principal) livechannels.Authority {
	return func(ctx context.Context, tx *sql.Tx, owner bool) (string, func(string, string) bool, error) {
		current, e := d.Identity.ReauthorizeTx(ctx, tx, expected)
		if e != nil {
			if errors.Is(e, identity.ErrUnauthorized) {
				return "", nil, livechannels.ErrDenied
			}
			return "", nil, livechannels.ErrUnavailable
		}
		localOwner := channelOwner(current)
		if localOwner {
			if e = d.ownerAuthorityTx(ctx, tx, current); e != nil {
				return "", nil, livechannels.ErrDenied
			}
		}
		if owner && !localOwner {
			return "", nil, livechannels.ErrDenied
		}
		if d.Hosted != nil {
			if e = d.allowedLibraryTx(ctx, current, "", tx); e != nil {
				return "", nil, livechannels.ErrDenied
			}
		} else if current.Authority != "local" {
			return "", nil, livechannels.ErrDenied
		}
		rows, e := tx.QueryContext(ctx, `SELECT s.id,s.revision,COALESCE(x.viewer_access,'owner-only') FROM live_sources s LEFT JOIN live_source_settings x ON x.source_id=s.id ORDER BY s.id`)
		if e != nil {
			return "", nil, livechannels.ErrUnavailable
		}
		allowed := map[string]bool{}
		parts := current.ServerID + ":" + current.Hash + ":" + strconv.Itoa(current.Epoch)
		for rows.Next() {
			var sid, access string
			var revision int64
			if rows.Scan(&sid, &revision, &access) != nil {
				rows.Close()
				return "", nil, livechannels.ErrUnavailable
			}
			allowed[sid] = localOwner || access == "server-members"
			parts += fmt.Sprintf(":%s:%d:%s", sid, revision, access)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return "", nil, livechannels.ErrUnavailable
		}
		f := sha256.Sum256([]byte(parts))
		return hex.EncodeToString(f[:]), func(sid, _ string) bool { return localOwner || allowed[sid] }, nil
	}
}
func liveFailure(w http.ResponseWriter, e error) {
	if writerRefused(w, e) {
		return
	}
	if revisionFailure(w, e) {
		return
	}
	if errors.Is(e, catalog.ErrVisibilityBuilding) {
		// A catalogue change is still publishing: the shared retryable code.
		failure(w, e)
		return
	}
	if errors.Is(e, errFeatureRestricted) {
		failure(w, e)
		return
	}

	status, code, message := 503, "guide_unavailable", livechannels.ErrUnavailable.Error()
	var lan *livechannels.LANConfirmationRequired
	if errors.As(e, &lan) {
		// CD-06: the exact roots the owner must confirm, sent back unchanged in
		// confirmedLanRoots on the next preview.
		write(w, 422, map[string]any{"error": map[string]any{"code": "source_lan_confirmation_required", "message": lan.Error(), "retryable": false, "confirmRoots": lan.Roots}})
		return
	}
	switch {
	// CD-51: a refused but valid session is 403 (or a hidden 404), never 401.
	case errors.Is(e, identity.ErrNotVisible):
		status, code, message = 404, "not_found", publicErrorMessage("not_found")
	case errors.Is(e, identity.ErrForbidden):
		status, code, message = 403, "forbidden", identity.ErrForbidden.Error()
	case errors.Is(e, identity.ErrUnauthorized):
		status, code, message = 401, "authentication_required", "Authentication is required."
	case errors.Is(e, livechannels.ErrDirectoryAnchorNotFound):
		status, code, message = 404, "not_found", publicErrorMessage("not_found")
	case errors.Is(e, livechannels.ErrChannelIdentifierConflict):
		status, code, message = 409, "channel_identifier_conflict", livechannels.ErrChannelIdentifierConflict.Error()
	case errors.Is(e, livechannels.ErrDenied):
		status, code, message = 403, "channel_permission_denied", livechannels.ErrDenied.Error()
	case errors.Is(e, livechannels.ErrInvalid):
		status, code, message = 400, "invalid_channel_input", livechannels.ErrInvalid.Error()
	case errors.Is(e, livechannels.ErrConflict):
		status, code, message = 409, "channel_revision_conflict", livechannels.ErrConflict.Error()
	case errors.Is(e, livechannels.ErrCursor):
		status, code, message = 409, "guide_refresh_required", livechannels.ErrCursor.Error()
	case errors.Is(e, livechannels.ErrDependencies):
		status, code, message = 409, "source_has_dependencies", livechannels.ErrDependencies.Error()
	case errors.Is(e, livechannels.ErrNetworkPolicy):
		status, code, message = 422, "source_network_policy", livechannels.ErrNetworkPolicy.Error()
	case errors.Is(e, livechannels.ErrSourceAuthentication):
		status, code, message = 422, "source_credentials_required", livechannels.ErrSourceAuthentication.Error()
	case errors.Is(e, livechannels.ErrSourceUnavailable):
		status, code, message = 503, "source_unavailable", livechannels.ErrSourceUnavailable.Error()
	case errors.Is(e, livechannels.ErrCapacity), errors.Is(e, livechannels.ErrReservation):
		status, code, message = 409, "source_capacity_unavailable", publicErrorMessage("source_capacity_unavailable")
	}
	write(w, status, map[string]any{"error": map[string]any{"code": code, "message": message, "retryable": status == 503}})
}
func liveDecode(w http.ResponseWriter, r *http.Request, value any, max int64) error {
	r.Body = http.MaxBytesReader(w, r.Body, max)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	var raw json.RawMessage
	if e := dec.Decode(&raw); e != nil {
		return livechannels.ErrInvalid
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		return livechannels.ErrInvalid
	}
	strict := json.NewDecoder(bytes.NewReader(raw))
	strict.DisallowUnknownFields()
	if err := strict.Decode(value); err != nil {
		return livechannels.ErrInvalid
	}
	return requireRevisionJSON(raw, value)
}

// Registered by the local HTTP composition root; no external service calls.
func (d Dependencies) liveChannelRoutes(mux *http.ServeMux, store *livechannels.Store) {
	if store == nil {
		return
	}
	slots := make(chan struct{}, 2)
	handle := func(owner bool, work func(context.Context, http.ResponseWriter, *http.Request, identity.Principal) (any, error)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
			defer cancel()
			p, e := d.channelPrincipal(ctx, r, owner)
			if e != nil {
				liveFailure(w, e)
				return
			}
			// Guide/directory reads already use HTTP admission and the bounded
			// read pool. Keep the extra two slots for expensive mutations only.
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
			result, e := work(ctx, w, r, p)
			if e != nil {
				liveFailure(w, e)
				return
			}
			current, e := d.channelPrincipal(ctx, r, owner)
			if e != nil {
				liveFailure(w, e)
				return
			}
			if current != p {
				liveFailure(w, livechannels.ErrDenied)
				return
			}
			if ctx.Err() != nil {
				liveFailure(w, livechannels.ErrUnavailable)
				return
			}
			write(w, 200, result)
		}
	}
	d.guideDirectoryRoutes(mux, store, handle)

	d.httpHandle(mux, "POST /v1/admin/live-sources/{id}/enabled", handle(true, func(ctx context.Context, w http.ResponseWriter, r *http.Request, p identity.Principal) (any, error) {
		if r.URL.RawQuery != "" {
			return nil, livechannels.ErrInvalid
		}
		var in struct {
			ExpectedRevision int64  `json:"expectedRevision"`
			Enabled          *bool  `json:"enabled"`
			RequestID        string `json:"requestId"`
		}
		if e := liveDecode(w, r, &in, 4096); e != nil {
			return nil, e
		}
		if in.Enabled == nil {
			return nil, livechannels.ErrInvalid
		}
		e := store.SetEnabled(ctx, d.liveAuthority(p), r.PathValue("id"), in.ExpectedRevision, *in.Enabled, in.RequestID)
		return map[string]any{"protocolVersion": "1.0", "serverId": d.Identity.ID(), "saved": e == nil}, e
	}))

	d.httpHandle(mux, "POST /v1/admin/live-sources/{id}/refresh", handle(true, func(ctx context.Context, w http.ResponseWriter, r *http.Request, p identity.Principal) (any, error) {
		if r.URL.RawQuery != "" {
			return nil, livechannels.ErrInvalid
		}
		var in struct {
			ExpectedRevision int64 `json:"expectedRevision"`
		}
		if e := liveDecode(w, r, &in, 4096); e != nil {
			return nil, e
		}
		e := store.RequestRefresh(ctx, d.liveAuthority(p), r.PathValue("id"), in.ExpectedRevision)
		return map[string]any{"protocolVersion": "1.0", "serverId": d.Identity.ID(), "scheduled": e == nil}, e
	}))
	d.httpHandle(mux, "POST /v1/admin/live-sources/{id}/remove", handle(true, func(ctx context.Context, w http.ResponseWriter, r *http.Request, p identity.Principal) (any, error) {
		if r.URL.RawQuery != "" {
			return nil, livechannels.ErrInvalid
		}
		var in struct {
			ExpectedRevision int64  `json:"expectedRevision"`
			RequestID        string `json:"requestId"`
		}
		if e := liveDecode(w, r, &in, 4096); e != nil {
			return nil, e
		}
		e := store.Remove(ctx, d.liveAuthority(p), r.PathValue("id"), in.ExpectedRevision, in.RequestID)
		return map[string]any{"protocolVersion": "1.0", "serverId": d.Identity.ID(), "removed": e == nil}, e
	}))
	d.httpHandle(mux, "POST /v1/channels/preferences", handle(false, func(ctx context.Context, w http.ResponseWriter, r *http.Request, p identity.Principal) (any, error) {
		if r.URL.RawQuery != "" {
			return nil, livechannels.ErrInvalid
		}
		var in livechannels.PreferenceInput
		if e := liveDecode(w, r, &in, 4096); e != nil {
			return nil, e
		}
		revision, e := store.SetPreference(ctx, d.liveAuthority(p), channelSubject(p), in)
		return map[string]any{"protocolVersion": "1.0", "serverId": d.Identity.ID(), "revision": revision}, e
	}))
	d.httpHandle(mux, "GET /v1/guide", handle(false, func(ctx context.Context, w http.ResponseWriter, r *http.Request, p identity.Principal) (any, error) {
		q := r.URL.Query()
		for key, values := range q {
			switch key {
			case "start", "end", "timezone", "kind", "search", "sourceId", "limit", "cursor", "favorites", "includeHidden", "includeServerHidden", "group", "channels", "programmes":
			default:
				return nil, livechannels.ErrInvalid
			}
			if len(values) != 1 {
				return nil, livechannels.ErrInvalid
			}
		}
		start, e1 := time.Parse(time.RFC3339, q.Get("start"))
		end, e2 := time.Parse(time.RFC3339, q.Get("end"))
		limit, e3 := strconv.Atoi(q.Get("limit"))
		if e1 != nil || e2 != nil || e3 != nil {
			return nil, livechannels.ErrInvalid
		}
		if (q.Get("favorites") != "" && q.Get("favorites") != "true" && q.Get("favorites") != "false") || (q.Get("includeHidden") != "" && q.Get("includeHidden") != "true" && q.Get("includeHidden") != "false") {
			return nil, livechannels.ErrInvalid
		}
		if q.Has("includeServerHidden") && q.Get("includeServerHidden") != "true" && q.Get("includeServerHidden") != "false" {
			return nil, livechannels.ErrInvalid
		}
		if q.Get("includeServerHidden") == "true" && !channelOwner(p) {
			return nil, livechannels.ErrDenied
		}
		if q.Get("programmes") != "" && q.Get("programmes") != "none" {
			return nil, livechannels.ErrInvalid
		}
		var channelIDs []string
		if raw := q.Get("channels"); raw != "" {
			channelIDs = strings.Split(raw, ",")
			if len(channelIDs) > 50 {
				return nil, livechannels.ErrInvalid
			}
			for _, id := range channelIDs {
				if id == "" || len(id) > 256 {
					return nil, livechannels.ErrInvalid
				}
			}
		}
		query := livechannels.GuideQuery{ChannelIDs: channelIDs, NoProgrammes: q.Get("programmes") == "none", Viewer: channelSubject(p), FavoritesOnly: q.Get("favorites") == "true", IncludeHidden: q.Get("includeHidden") == "true", IncludeServerHidden: q.Get("includeServerHidden") == "true", Group: q.Get("group"), Start: start, End: end, Timezone: q.Get("timezone"), Kind: livechannels.Provenance(q.Get("kind")), Search: q.Get("search"), SourceID: q.Get("sourceId"), Limit: limit, Cursor: q.Get("cursor")}
		// Live-source programmes follow the profile's content restrictions
		// like library items (Channels spec §8.1); Library Channels apply
		// theirs per scheduled item.

		restrictions, restrictionFence, err := d.viewerRestrictions(r, p)
		if err != nil {
			return nil, err
		}
		if restrictions.Active() {
			query.ProgrammeAllowed = func(rating string) bool { return catalog.RatingAllowed(restrictions, rating) }
		}
		query.MarkChannels = func(ctx context.Context, tx *sql.Tx, channels []livechannels.Channel) error {
			return store.ChannelMarksTx(ctx, tx, d.guideDirectoryAuthority(p, restrictionFence), channels)
		}

		var guide livechannels.Guide
		var e error
		if query.Kind == livechannels.LibraryChannel {
			if d.LibraryChannels == nil {
				return nil, livechannels.ErrUnavailable
			}
			guide, e = d.LibraryChannels.Guide(ctx, d.libraryAuthority(p), query, store.ContinuationKey("library-guide-v1"))
		} else {
			guide, e = store.Guide(ctx, d.guideReadAuthority(p, restrictionFence), query)
		}
		if e == nil {
			e = d.annotateViewerDirectory(ctx, p, guide.Channels)
		}

		if e == nil && d.DVR != nil && query.Kind != livechannels.LibraryChannel && d.featureAllowed(ctx, p, "dvr") == nil {
			e = d.DVR.AnnotateGuide(ctx, d.liveAuthority(p), channelSubject(p), &guide)
		}
		if e == nil {
			_, after, err := d.viewerRestrictions(r, p)
			if err != nil {
				return nil, err
			}
			if after != restrictionFence {
				return nil, livechannels.ErrCursor
			}
		}

		return map[string]any{"protocolVersion": "1.0", "serverId": d.Identity.ID(), "guide": guide}, e
	}))
}

func (d Dependencies) guideReadAuthority(p identity.Principal, restrictionFence string) livechannels.Authority {
	return func(ctx context.Context, tx *sql.Tx, owner bool) (string, func(string, string) bool, error) {
		fence, allowed, e := d.liveAuthority(p)(ctx, tx, owner)
		if e != nil {
			return "", nil, e
		}
		return fence + ":" + restrictionFence, allowed, nil
	}
}
