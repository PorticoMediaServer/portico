package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
)

// Notifications and feedback use bounded reads and writes. Live hints share /v1/events.

func noticeScopeEnvelope(d Dependencies, p identity.Principal) map[string]string {
	return map[string]string{
		"serverId":    d.Identity.ID(),
		"viewerFence": fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%s:%d", identity.PersonalKey(p.Viewer), p.Epoch)))),
	}
}

// noticeBody decodes a request body strictly: unknown fields are a 400 rather
// than a silently ignored instruction from a client that expects them to apply.
func noticeBody(r *http.Request, v any) error {
	var raw json.RawMessage
	if e := decode(nil, r, &raw); e != nil {
		return &operations.ValidationError{Fields: []string{"request"}}
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if e := dec.Decode(v); e != nil {
		return &operations.ValidationError{Fields: []string{"request"}}
	}
	return nil
}

func noticeLimit(r *http.Request) (int, error) {
	raw := r.URL.Query().Get("limit")
	if raw == "" {
		return 0, nil
	}
	n, e := strconv.Atoi(raw)
	if e != nil || n < 1 || n > 100 {
		return 0, operations.ErrInvalid
	}
	return n, nil
}

func (d Dependencies) notificationRoutes(mux *http.ServeMux) {
	if d.Console == nil || d.DB == nil {
		return
	}
	slots := make(chan struct{}, 8)
	type action func(*http.Request, identity.Principal, operations.Authorize) (any, error)
	register := func(pattern string, owner bool, query []string, fn action) {
		d.projectedHandle(mux, pattern, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			ctx, cancel := context.WithTimeout(r.Context(), 7*time.Second)
			defer cancel()
			r = r.WithContext(ctx)
			var p identity.Principal
			var e error
			if owner {
				p, e = d.owner(r)
			} else {
				p, e = d.principal(r)
			}
			if e != nil {
				consoleError(w, e)
				return
			}
			if e = consoleQuery(r, query...); e != nil {
				consoleError(w, e)
				return
			}
			select {
			case slots <- struct{}{}:
				defer func() { <-slots }()
			default:
				consoleError(w, operations.ErrCapacity)
				return
			}
			auth := d.consoleAuthority(p, owner)
			out, e := fn(r, p, auth)
			if e != nil {
				consoleError(w, e)
				return
			}
			// Re-check authorization after the read: a session revoked mid-request
			// must not receive the document it was building.
			if e = d.ConsoleCheck(ctx, auth); e != nil {
				consoleError(w, e)
				return
			}
			consoleWrite(w, 200, map[string]any{"scope": noticeScopeEnvelope(d, p), "data": out})
		})
	}

	register("GET /v1/notifications/capabilities", false, nil, func(r *http.Request, p identity.Principal, a operations.Authorize) (any, error) {
		return operations.NoticeVocabularyDocument(), nil
	})
	register("GET /v1/notifications/inbox", false, []string{"audience", "state", "cursor", "limit"}, func(r *http.Request, p identity.Principal, a operations.Authorize) (any, error) {
		limit, e := noticeLimit(r)
		if e != nil {
			return nil, e
		}
		q := r.URL.Query()
		inbox, e := d.Console.Notices(r.Context(), p, a, operations.NoticeQuery{
			Audience: q.Get("audience"), State: q.Get("state"), Cursor: q.Get("cursor"), Limit: limit,
		})
		if e != nil {
			return nil, e
		}
		if e = d.visibleNoticeActions(r, p, inbox.Items); e != nil {
			return nil, e
		}
		return inbox, nil
	})
	register("GET /v1/notifications/unread-count", false, []string{"audience"}, func(r *http.Request, p identity.Principal, a operations.Authorize) (any, error) {
		return d.Console.NoticeSummary(r.Context(), p, a, r.URL.Query().Get("audience"))
	})
	register("POST /v1/notifications/inbox/actions", false, nil, func(r *http.Request, p identity.Principal, a operations.Authorize) (any, error) {
		var c operations.NoticeBatch
		if e := noticeBody(r, &c); e != nil {
			return nil, e
		}
		return d.Console.ApplyNotices(r.Context(), p, a, c)
	})
	register("POST /v1/admin/notifications/broadcast", true, nil, func(r *http.Request, p identity.Principal, a operations.Authorize) (any, error) {
		var c operations.NoticeBroadcast
		if e := noticeBody(r, &c); e != nil {
			return nil, e
		}
		return d.Console.BroadcastNotice(r.Context(), p, a, c)
	})
	register("GET /v1/admin/notifications/settings", true, nil, func(r *http.Request, p identity.Principal, a operations.Authorize) (any, error) {
		return d.Console.NotificationSettings(r.Context(), a)
	})
	register("PATCH /v1/admin/notifications/settings", true, nil, func(r *http.Request, p identity.Principal, a operations.Authorize) (any, error) {
		var c operations.NotificationSettingsChange
		if e := noticeBody(r, &c); e != nil {
			return nil, e
		}
		return d.Console.ApplyNotificationSettings(r.Context(), p, a, c)
	})

	// Feedback.
	register("GET /v1/feedback/capabilities", false, nil, func(r *http.Request, p identity.Principal, a operations.Authorize) (any, error) {
		return d.Console.FeedbackCapabilitiesDocument(r.Context(), p, a)
	})
	register("POST /v1/feedback/reports", false, nil, func(r *http.Request, p identity.Principal, a operations.Authorize) (any, error) {
		var c operations.FeedbackSubmission
		if e := noticeBody(r, &c); e != nil {
			return nil, e
		}
		return d.Console.SubmitFeedback(r.Context(), p, a, c)
	})
	// These four are registered as whole literals rather than composed from a
	// prefix, so a source scan of the routing table finds them and the published
	// document has to carry them.
	listReports := func(owner bool) action {
		return func(r *http.Request, p identity.Principal, a operations.Authorize) (any, error) {
			limit, e := noticeLimit(r)
			if e != nil {
				return nil, e
			}
			q := r.URL.Query()
			return d.Console.FeedbackReports(r.Context(), p, a, owner, operations.FeedbackFilter{
				Status: q.Get("status"), Kind: q.Get("kind"), Category: q.Get("category"),
				Reporter: q.Get("reporter"), Cursor: q.Get("cursor"), Limit: limit,
			})
		}
	}
	readReport := func(owner bool) action {
		return func(r *http.Request, p identity.Principal, a operations.Authorize) (any, error) {
			return d.Console.FeedbackReportDocument(r.Context(), p, a, owner, r.PathValue("id"))
		}
	}
	listQuery := []string{"status", "kind", "category", "reporter", "cursor", "limit"}
	register("GET /v1/feedback/reports", false, listQuery, listReports(false))
	register("GET /v1/feedback/reports/{id}", false, nil, readReport(false))
	register("GET /v1/admin/feedback/reports", true, listQuery, listReports(true))
	register("GET /v1/admin/feedback/reports/{id}", true, nil, readReport(true))
	register("POST /v1/admin/feedback/reports/{id}/status", true, nil, func(r *http.Request, p identity.Principal, a operations.Authorize) (any, error) {
		var c operations.FeedbackTransition
		if e := noticeBody(r, &c); e != nil {
			return nil, e
		}
		return d.Console.TransitionFeedback(r.Context(), p, a, r.PathValue("id"), c)
	})

}
