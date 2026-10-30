package httpapi

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/recordingaccess"
	"strconv"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
)

func directoryQuery(q url.Values) (livechannels.DirectoryQuery, error) {
	out := livechannels.DirectoryQuery{Kind: "all", Sort: "number", Limit: 50}
	for key, values := range q {
		if len(values) != 1 {
			return out, livechannels.ErrInvalid
		}
		switch key {
		case "kind", "sourceId", "search", "group", "favorites", "includeHidden", "includeServerHidden", "sort", "offset", "limit", "revision", "timezone", "anchorChannelId", "anchorSourceId", "anchorProvenance", "direction":
		default:
			return out, livechannels.ErrInvalid
		}
	}
	if q.Has("kind") {
		out.Kind = q.Get("kind")
	}
	if q.Has("sort") {
		out.Sort = q.Get("sort")
	}
	out.SourceID = q.Get("sourceId")
	out.Search = q.Get("search")
	out.Group = q.Get("group")
	out.Revision = q.Get("revision")
	out.AnchorChannelID = q.Get("anchorChannelId")
	out.AnchorSourceID = q.Get("anchorSourceId")
	out.AnchorProvenance = q.Get("anchorProvenance")
	out.Direction = q.Get("direction")
	for key, target := range map[string]*int{"offset": &out.Offset, "limit": &out.Limit} {
		if q.Has(key) {
			v, err := strconv.Atoi(q.Get(key))
			if err != nil || strconv.Itoa(v) != q.Get(key) {
				return out, livechannels.ErrInvalid
			}
			*target = v
		}
	}
	for key, target := range map[string]*bool{"favorites": &out.FavoritesOnly, "includeHidden": &out.IncludeHidden, "includeServerHidden": &out.IncludeServerHidden} {
		if q.Has(key) {
			if q.Get(key) != "true" && q.Get(key) != "false" {
				return out, livechannels.ErrInvalid
			}
			*target = q.Get(key) == "true"
		}
	}
	if zone := q.Get("timezone"); zone != "" {
		if _, err := time.LoadLocation(zone); err != nil {
			return out, livechannels.ErrInvalid
		}
	}
	if out.Direction != "" {
		if q.Has("offset") || (q.Has("limit") && out.Limit != 1) {
			return out, livechannels.ErrInvalid
		}
		out.Limit = 1
	}
	return out, nil
}

func (d Dependencies) guideDirectoryAuthority(p identity.Principal, restrictionFence string) livechannels.DirectoryAuthority {
	return func(ctx context.Context, tx *sql.Tx) (livechannels.DirectoryScope, error) {
		fence, allowed, err := d.liveAuthority(p)(ctx, tx, false)
		if err != nil {
			return livechannels.DirectoryScope{}, err
		}
		scope := livechannels.DirectoryScope{Owner: channelOwner(p), Viewer: channelSubject(p), AllowedLiveSources: []string{}, LibraryRows: []livechannels.DirectoryRow{}}
		rows, err := tx.QueryContext(ctx, `SELECT id FROM live_sources WHERE state='active' ORDER BY id`)
		if err != nil {
			return scope, livechannels.ErrUnavailable
		}
		for rows.Next() {
			var id string
			if err = rows.Scan(&id); err != nil {
				rows.Close()
				return scope, livechannels.ErrUnavailable
			}
			if allowed(id, "") {
				scope.AllowedLiveSources = append(scope.AllowedLiveSources, id)
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return scope, livechannels.ErrUnavailable
		}
		libraryFence := ""
		if d.LibraryChannels != nil {
			scope.LibraryRows, libraryFence, err = d.LibraryChannels.DirectoryRowsTx(ctx, tx, d.libraryAuthority(p), channelSubject(p))
			if err != nil {
				return scope, err
			}
		}
		hash := sha256.Sum256([]byte(fence + ":" + libraryFence + ":" + restrictionFence))
		scope.Fence = hex.EncodeToString(hash[:])
		return scope, nil
	}
}

func (d Dependencies) annotateDirectory(ctx context.Context, channels []livechannels.Channel) error {
	if d.Administration != nil {
		ids := []string{}
		for _, c := range channels {
			if c.Provenance == livechannels.LiveSource {
				ids = append(ids, "live:"+c.SourceID+":"+c.ID)
			}
		}
		paths, err := d.Administration.ChannelLogoPaths(ctx, ids)
		if err != nil {
			return err
		}
		for i := range channels {
			if channels[i].Provenance == livechannels.LiveSource {
				channels[i].LogoPath = paths["live:"+channels[i].SourceID+":"+channels[i].ID]
			}
		}
	}
	available, reason := d.guideDeliveryAvailability()
	for i := range channels {
		c := &channels[i]
		c.TuneAvailable = available && c.Generation != ""
		c.TuneUnavailableReason = reason
		if available && c.Generation == "" {
			c.TuneUnavailableReason = "schedule_preparing"
		}
		if c.Provenance == livechannels.LiveSource {
			c.RecordAvailable = d.DVR != nil && d.DVR.CaptureAvailable() && c.Generation != ""
			if c.RecordAvailable {
				c.RecordUnavailableReason = ""
			}
		}
	}
	return nil
}
func (d Dependencies) guideDeliveryAvailability() (bool, string) {
	if d.PlaybackRuntime != nil && d.PlaybackRuntime.Linear != nil {
		return d.PlaybackRuntime.Linear.Available()
	}
	return false, "channel_runtime_unavailable"
}

func (d Dependencies) guideDirectoryRoutes(mux *http.ServeMux, store *livechannels.Store, handle func(bool, func(context.Context, http.ResponseWriter, *http.Request, identity.Principal) (any, error)) http.HandlerFunc) {
	d.guideFeatureRoutes(mux, store, handle)
	d.httpHandle(mux, "GET /v1/guide/channels", handle(false, func(ctx context.Context, _ http.ResponseWriter, r *http.Request, p identity.Principal) (any, error) {
		query, err := directoryQuery(r.URL.Query())
		if err != nil {
			return nil, err
		}
		if (query.IncludeHidden || query.IncludeServerHidden) && !channelOwner(p) {
			return nil, livechannels.ErrDenied
		}
		_, fence, err := d.viewerRestrictions(r, p)
		if err != nil {
			return nil, err
		}
		query.DeliveryAvailable, _ = d.guideDeliveryAvailability()
		directory, err := store.ChannelDirectory(ctx, d.guideDirectoryAuthority(p, fence), query)
		if err != nil {
			return nil, err
		}
		if err = d.annotateViewerDirectory(ctx, p, directory.Channels); err != nil {
			return nil, err
		}
		_, current, err := d.viewerRestrictions(r, p)
		if err != nil {
			return nil, err
		}
		if current != fence {
			return nil, livechannels.ErrCursor
		}
		return map[string]any{"protocolVersion": "1.0", "serverId": d.Identity.ID(), "directory": directory}, nil
	}))
	d.httpHandle(mux, "GET /v1/guide/sources", handle(false, func(ctx context.Context, _ http.ResponseWriter, r *http.Request, p identity.Principal) (any, error) {
		q := r.URL.Query()
		for key, values := range q {
			if len(values) != 1 || (key != "timezone" && key != "includeHidden" && key != "includeServerHidden") {
				return nil, livechannels.ErrInvalid
			}
		}
		query, err := directoryQuery(q)
		if err != nil {
			return nil, err
		}
		if (query.IncludeHidden || query.IncludeServerHidden) && !channelOwner(p) {
			return nil, livechannels.ErrDenied
		}
		_, fence, err := d.viewerRestrictions(r, p)
		if err != nil {
			return nil, err
		}
		summary, err := store.ChannelSourceSummary(ctx, d.guideDirectoryAuthority(p, fence), query.IncludeHidden, time.Now(), query.IncludeServerHidden)
		if err != nil {
			return nil, err
		}
		available, reason := d.guideDeliveryAvailability()
		for i := range summary.Sources {
			s := &summary.Sources[i]
			s.RecordAvailable = false
			if s.Provenance == livechannels.LiveSource {
				channels := []livechannels.Channel{{SourceID: s.ID, Provenance: s.Provenance, Generation: s.Generation}}
				if err = d.annotateViewerDirectory(ctx, p, channels); err != nil {
					return nil, err
				}
				s.RecordAvailable = channels[0].RecordAvailable
			}
			if !available {
				s.UnavailableReasons[reason] = s.ChannelCount
			}
		}
		_, current, err := d.viewerRestrictions(r, p)
		if err != nil {
			return nil, err
		}
		if current != fence {
			return nil, livechannels.ErrCursor
		}
		return map[string]any{"protocolVersion": "1.0", "serverId": d.Identity.ID(), "viewerFence": summary.ViewerFence, "revision": summary.Revision, "sources": summary.Sources}, nil
	}))
}

func (d Dependencies) guideFeatureRoutes(mux *http.ServeMux, store *livechannels.Store, handle func(bool, func(context.Context, http.ResponseWriter, *http.Request, identity.Principal) (any, error)) http.HandlerFunc) {
	for _, route := range []string{"on-now", "now-next"} {
		onNow := route == "on-now"
		d.httpHandle(mux, "GET /v1/guide/"+route, handle(false, func(ctx context.Context, _ http.ResponseWriter, r *http.Request, p identity.Principal) (any, error) {
			q := r.URL.Query()
			for k, v := range q {
				if len(v) != 1 || !strings.Contains("|kind|sourceId|group|cursor|limit|", "|"+k+"|") {
					return nil, livechannels.ErrInvalid
				}
			}
			query := livechannels.DirectoryQuery{Kind: q.Get("kind"), SourceID: q.Get("sourceId"), Group: q.Get("group"), Limit: 37}
			if query.Kind == "" {
				query.Kind = "all"
			}
			if q.Has("limit") {
				n, e := strconv.Atoi(q.Get("limit"))
				if e != nil {
					return nil, livechannels.ErrInvalid
				}
				query.Limit = n
			}
			restrictions, fence, e := d.viewerRestrictions(r, p)
			if e != nil {
				return nil, e
			}
			read := func(ctx context.Context, gq livechannels.GuideQuery) (livechannels.Guide, error) {
				gq.Viewer = channelSubject(p)
				if restrictions.Active() {
					gq.ProgrammeAllowed = func(rating string) bool { return catalog.RatingAllowed(restrictions, rating) }
				}
				if gq.Kind == livechannels.LibraryChannel {
					if d.LibraryChannels == nil {
						return livechannels.Guide{}, livechannels.ErrUnavailable
					}
					return d.LibraryChannels.Guide(ctx, d.libraryAuthority(p), gq, store.ContinuationKey("library-guide-v1"))
				}
				guide, err := store.Guide(ctx, d.liveAuthority(p), gq)
				if err == nil && d.DVR != nil && d.featureAllowed(ctx, p, "dvr") == nil {
					err = d.DVR.AnnotateGuide(ctx, d.liveAuthority(p), channelSubject(p), &guide)
				}
				return guide, err
			}
			page, e := store.NowNextPage(ctx, d.guideDirectoryAuthority(p, fence), read, livechannels.GuidePageQuery{Directory: query, Cursor: q.Get("cursor"), OnNow: onNow})
			if e != nil {
				return nil, e
			}
			if e = d.annotateViewerDirectory(ctx, p, page.Channels); e != nil {
				return nil, e
			}
			_, after, e := d.viewerRestrictions(r, p)
			if e != nil {
				return nil, e
			}
			if after != fence {
				return nil, livechannels.ErrCursor
			}
			return struct {
				ProtocolVersion string                 `json:"protocolVersion"`
				ServerID        string                 `json:"serverId"`
				Page            livechannels.GuidePage `json:"page"`
			}{"1.0", d.Identity.ID(), page}, nil
		}))
	}
	d.httpHandle(mux, "GET /v1/guide/airings", handle(false, func(ctx context.Context, _ http.ResponseWriter, r *http.Request, p identity.Principal) (any, error) {
		q := r.URL.Query()
		for k, v := range q {
			if len(v) != 1 || !strings.Contains("|search|seriesId|title|channelId|sourceId|kind|start|end|cursor|limit|", "|"+k+"|") {
				return nil, livechannels.ErrInvalid
			}
		}
		query := livechannels.AiringsQuery{Search: q.Get("search"), SeriesID: q.Get("seriesId"), Title: q.Get("title"), ChannelID: q.Get("channelId"), SourceID: q.Get("sourceId"), Kind: q.Get("kind"), Cursor: q.Get("cursor"), Limit: 30}
		var e error
		query.Start, e = time.Parse(time.RFC3339, q.Get("start"))
		if e != nil {
			return nil, livechannels.ErrInvalid
		}
		query.End, e = time.Parse(time.RFC3339, q.Get("end"))
		if e != nil {
			return nil, livechannels.ErrInvalid
		}
		if q.Has("limit") {
			query.Limit, e = strconv.Atoi(q.Get("limit"))
			if e != nil {
				return nil, livechannels.ErrInvalid
			}
		}
		restrictions, fence, e := d.viewerRestrictions(r, p)
		if e != nil {
			return nil, e
		}
		if restrictions.Active() {
			query.ProgrammeAllowed = func(rating string) bool { return catalog.RatingAllowed(restrictions, rating) }
		}
		page, e := store.Airings(ctx, d.guideDirectoryAuthority(p, fence), d.airingProjection(p), query)
		if e != nil {
			return nil, e
		}
		_, after, e := d.viewerRestrictions(r, p)
		if e != nil {
			return nil, e
		}
		if after != fence {
			return nil, livechannels.ErrCursor
		}
		return struct {
			ProtocolVersion string                   `json:"protocolVersion"`
			ServerID        string                   `json:"serverId"`
			Airings         livechannels.AiringsPage `json:"airings"`
		}{"1.0", d.Identity.ID(), page}, nil
	}))
	for _, method := range []string{"GET", "PUT", "DELETE"} {
		pattern := method + " /v1/guide/reminders"
		if method == "DELETE" {
			pattern += "/{id}"
		}
		d.httpHandle(mux, pattern, handle(false, func(ctx context.Context, w http.ResponseWriter, r *http.Request, p identity.Principal) (any, error) {
			restrictions, fence, e := d.viewerRestrictions(r, p)
			if e != nil {
				return nil, e
			}
			predicate := func(rating string) bool { return catalog.RatingAllowed(restrictions, rating) }
			a := d.guideDirectoryAuthority(p, fence)
			var result any
			if r.Method == "GET" {
				q := r.URL.Query()
				for k, v := range q {
					if len(v) != 1 || k != "cursor" && k != "limit" {
						return nil, livechannels.ErrInvalid
					}
				}
				limit := 30
				if q.Has("limit") {
					limit, e = strconv.Atoi(q.Get("limit"))
					if e != nil {
						return nil, livechannels.ErrInvalid
					}
				}
				result, e = store.Reminders(ctx, a, d.airingProjection(p), q.Get("cursor"), limit, predicate)
			} else {
				if r.URL.RawQuery != "" {
					return nil, livechannels.ErrInvalid
				}
				if r.Method == "PUT" {
					var in livechannels.ReminderInput
					if e = liveDecode(w, r, &in, 4096); e != nil {
						return nil, e
					}
					result, e = store.SaveReminder(ctx, a, d.airingProjection(p), in, predicate)
				} else {
					var in struct {
						ExpectedRevision int64 `json:"expectedRevision"`
					}
					if e = liveDecode(w, r, &in, 4096); e != nil {
						return nil, e
					}
					e = store.DeleteReminder(ctx, a, r.PathValue("id"), in.ExpectedRevision)
					result = map[string]bool{"deleted": e == nil}
				}
			}
			if e != nil {
				return nil, e
			}
			_, after, e := d.viewerRestrictions(r, p)
			if e != nil {
				return nil, e
			}
			if after != fence {
				return nil, livechannels.ErrCursor
			}
			return map[string]any{"protocolVersion": "1.0", "serverId": d.Identity.ID(), "result": result}, nil
		}))
	}
}
func (d Dependencies) airingProjection(p identity.Principal) livechannels.AiringProjection {
	return func(ctx context.Context, tx *sql.Tx, v []livechannels.Airing) ([]livechannels.Airing, error) {
		if d.LibraryChannels == nil {
			return nil, livechannels.ErrDenied
		}
		return d.LibraryChannels.ProjectAiringsTx(ctx, tx, d.libraryAuthority(p), v)
	}
}

// Source presence and a running capture adapter do not grant recording rights.
func (d Dependencies) annotateViewerDirectory(ctx context.Context, p identity.Principal, channels []livechannels.Channel) error {
	if e := d.annotateDirectory(ctx, channels); e != nil {
		return e
	}
	gate, e := dbwork.BeginSnapshot(ctx, d.DB)
	if e != nil {
		return e
	}
	defer gate.Rollback()
	policy := recordingaccess.Policy{}
	if d.Hosted != nil {
		policy.Cached = d.Hosted
	}
	for i := range channels {
		ch := &channels[i]
		if !ch.RecordAvailable {
			continue
		}
		e = policy.Durable(ctx, gate.Tx(), channelSubject(p), ch.SourceID, ch.ID)
		if errors.Is(e, livechannels.ErrDenied) {
			ch.RecordAvailable = false
			ch.RecordUnavailableReason = "recording-not-permitted"
		} else if e != nil {
			return e
		}
	}
	return nil
}
