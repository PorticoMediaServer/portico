package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/playbackv1"
	"github.com/PorticoMediaServer/portico/server/internal/recordingaccess"
	"github.com/PorticoMediaServer/portico/server/internal/social"
	"github.com/PorticoMediaServer/portico/server/internal/supervise"
	"github.com/PorticoMediaServer/portico/server/receiver"
)

// A receiver's held inbox read: how long it waits when it does not say, and the
// most it may ask for. Under a minute, so proxies and device HTTP stacks with a
// sixty-second idle limit never cut it.
const (
	receiverInboxDefaultWait    = 25 * time.Second
	receiverInboxMaxWaitSeconds = 55
)

// CastApplicationSetting is the configuration key holding the published Google
// Cast application id. It has no default: an unset id means this server has not
// been registered with the Cast console and senders must not advertise Cast.
const CastApplicationSetting = "cast.applicationId"

func socialFailure(w http.ResponseWriter, e error) {
	if errors.Is(e, errFeatureRestricted) {
		failure(w, e)
		return
	}

	var f *social.Fault
	if errors.As(e, &f) {
		body := map[string]any{"code": f.Code, "message": f.Message, "retryable": f.Retryable}
		for key, value := range f.Detail {
			body[key] = value
		}
		if f.RetryAfter > 0 {
			w.Header().Set("Retry-After", strconv.Itoa(f.RetryAfter))
		}
		write(w, f.Status, map[string]any{"protocolVersion": social.Protocol, "error": body})
		return
	}
	if status, code, ok := identity.Refusal(e); ok && status != 401 {
		// Identity's internal classification is shared by older edges; this
		// registry declares the canonical API vocabulary, with the same status.
		if code == "forbidden" {
			code = "not_permitted"
		}
		write(w, status, map[string]any{"protocolVersion": social.Protocol, "error": map[string]any{"code": code, "message": e.Error(), "retryable": false}})
		return
	}
	if errors.Is(e, identity.ErrUnauthorized) {
		write(w, 401, map[string]any{"protocolVersion": social.Protocol, "error": map[string]any{"code": "unauthorized", "message": "Authentication is required.", "retryable": false}})
		return
	}
	if errors.Is(e, identity.ErrContentRestricted) {
		write(w, 404, map[string]any{"protocolVersion": social.Protocol, "error": map[string]any{"code": "not_found", "message": "No such resource.", "retryable": false}})
		return
	}
	if errors.Is(e, sql.ErrNoRows) {
		write(w, 404, map[string]any{"protocolVersion": social.Protocol, "error": map[string]any{"code": "not_found", "message": "No such resource.", "retryable": false}})
		return
	}
	write(w, 500, map[string]any{"protocolVersion": social.Protocol, "error": map[string]any{"code": "internal", "message": "The request could not be completed.", "retryable": true}})
}

// socialBody decodes a bounded JSON request body.
func socialBody(w http.ResponseWriter, r *http.Request, out any) bool {
	if err := strictDecodeLimit(w, r, out, 256<<10); err != nil {
		status, message := http.StatusBadRequest, "The request body is not valid JSON for this operation."
		var tooLarge *http.MaxBytesError
		switch {
		case errors.Is(err, errStrictMediaType):
			status, message = http.StatusUnsupportedMediaType, "Send application/json."
		case errors.As(err, &tooLarge):
			status, message = http.StatusRequestEntityTooLarge, "The request body is too large."
		}
		socialFailure(w, &social.Fault{Code: "invalid_request", Status: status, Message: message})
		return false
	}
	return true
}

// Social builds the social playback store with the composition's policy seams
// wired in. It is exported so a test can drive the same store the routes use.
func (d Dependencies) Social() *social.Store {
	if d.social != nil {
		return d.social
	}
	store := social.New(d.DB)
	store.Identity = d.Identity
	if v1 := d.playbackV1(); v1 != nil {
		store.Playback = socialPlayback{d: d, v1: v1}
	}
	store.CastApplicationID = func(ctx context.Context, tx *sql.Tx) string {
		var value string
		if tx != nil {
			_ = tx.QueryRowContext(ctx, `SELECT value FROM configuration WHERE key=?`, CastApplicationSetting).Scan(&value)
			return value
		}
		return persistence.Get(d.DB, CastApplicationSetting)
	}
	// Visibility is the same library policy every other route enforces, evaluated
	// inside the caller's transaction so a group decision cannot race a grant.
	store.Visible = func(ctx context.Context, tx *sql.Tx, v identity.Viewer, itemID string) (bool, error) {
		if err := d.itemRestrictionsTx(ctx, tx, identity.Principal{Viewer: v}, itemID); err != nil {
			if errors.Is(err, identity.ErrContentRestricted) {
				return false, nil
			}
			return false, err
		}
		var library string
		e := tx.QueryRowContext(ctx, `SELECT cl.library_id FROM catalog_entities e JOIN catalog_libraries cl ON cl.id=e.library_id WHERE e.public_id=pid_blob(?)`, itemID).Scan(&library)
		if errors.Is(e, sql.ErrNoRows) {
			return false, nil
		}
		if e != nil {
			return false, e
		}
		policy := recordingaccess.Policy{}
		if d.Hosted != nil {
			policy.Cached = d.Hosted
		}
		if e = policy.AllowedTxContext(ctx, identity.Principal{Viewer: v}, library, tx); e != nil {
			if errors.Is(e, identity.ErrUnauthorized) {
				return false, nil
			}
			return false, e
		}
		return true, nil
	}
	return store
}

func (d Dependencies) socialRoutes(mux *http.ServeMux) {
	if d.DB == nil || d.Identity == nil {
		return
	}
	store := d.Social()
	// A restart must not strand a live group in "reconnecting": when a live
	// group survives the restart, its host timeline still advances with nobody
	// watching.
	supervise.Go("social.sweeper.resume", func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = store.ResumeSweeperIfLive(ctx)
	})
	d.serveCastBundle(mux)
}

type socialHandle func(string, http.HandlerFunc)
type socialViewer func(http.ResponseWriter, *http.Request) (identity.Principal, bool)

func (d Dependencies) groupRoutes(handle socialHandle, viewer socialViewer, store *social.Store) {
	// device is the caller's authenticated device, which a host binds the group
	// to; a token with no device binds nothing.
	device := func(r *http.Request, p identity.Principal) string {
		id, _ := d.deviceOf(r.Context(), p)
		return id
	}
	handle("POST /v1/groups", func(w http.ResponseWriter, r *http.Request) {
		p, ok := viewer(w, r)
		if !ok {
			return
		}
		var body social.CreateGroupRequest
		if !socialBody(w, r, &body) {
			return
		}
		out, e := store.CreateGroup(r.Context(), p, device(r, p), body)
		if e != nil {
			socialFailure(w, e)
			return
		}
		write(w, 201, out)
	})
	handle("GET /v1/groups", func(w http.ResponseWriter, r *http.Request) {
		p, ok := viewer(w, r)
		if !ok {
			return
		}
		out, e := store.ListGroups(r.Context(), p)
		if e != nil {
			socialFailure(w, e)
			return
		}
		write(w, 200, out)
	})
	handle("POST /v1/groups/join", func(w http.ResponseWriter, r *http.Request) {
		p, ok := viewer(w, r)
		if !ok {
			return
		}
		var body social.JoinRequest
		if !socialBody(w, r, &body) {
			return
		}
		out, e := store.Join(r.Context(), p, body)
		if e != nil {
			socialFailure(w, e)
			return
		}
		write(w, 200, out)
	})
	handle("GET /v1/groups/{id}", func(w http.ResponseWriter, r *http.Request) {
		p, ok := viewer(w, r)
		if !ok {
			return
		}
		out, e := store.ReadGroup(r.Context(), p, r.PathValue("id"))
		if e != nil {
			socialFailure(w, e)
			return
		}
		write(w, 200, out)
	})
	handle("POST /v1/groups/{id}/end", func(w http.ResponseWriter, r *http.Request) {
		p, ok := viewer(w, r)
		if !ok {
			return
		}
		var body social.EndRequest
		if !socialBody(w, r, &body) {
			return
		}
		out, e := store.EndGroup(r.Context(), p, r.PathValue("id"), body)
		if e != nil {
			socialFailure(w, e)
			return
		}
		write(w, 200, out)
	})
	handle("POST /v1/groups/{id}/leave", func(w http.ResponseWriter, r *http.Request) {
		p, ok := viewer(w, r)
		if !ok {
			return
		}
		out, e := store.Leave(r.Context(), p, r.PathValue("id"))
		if e != nil {
			socialFailure(w, e)
			return
		}
		write(w, 200, out)
	})
	handle("POST /v1/groups/{id}/heartbeat", func(w http.ResponseWriter, r *http.Request) {
		p, ok := viewer(w, r)
		if !ok {
			return
		}
		out, e := store.Heartbeat(r.Context(), p, device(r, p), r.PathValue("id"))
		if e != nil {
			socialFailure(w, e)
			return
		}
		write(w, 200, out)
	})
	handle("POST /v1/groups/{id}/host-transfer", func(w http.ResponseWriter, r *http.Request) {
		p, ok := viewer(w, r)
		if !ok {
			return
		}
		var body social.TransferRequest
		if !socialBody(w, r, &body) {
			return
		}
		out, e := store.TransferHost(r.Context(), p, device(r, p), r.PathValue("id"), body)
		if e != nil {
			socialFailure(w, e)
			return
		}
		write(w, 200, out)
	})
	handle("POST /v1/groups/{id}/invites", func(w http.ResponseWriter, r *http.Request) {
		p, ok := viewer(w, r)
		if !ok {
			return
		}
		var body social.InviteRequest
		if !socialBody(w, r, &body) {
			return
		}
		out, e := store.CreateInvite(r.Context(), p, r.PathValue("id"), body)
		if e != nil {
			socialFailure(w, e)
			return
		}
		write(w, 201, out)
	})
	handle("DELETE /v1/groups/{id}/invites/{inviteId}", func(w http.ResponseWriter, r *http.Request) {
		p, ok := viewer(w, r)
		if !ok {
			return
		}
		if e := store.RevokeInvite(r.Context(), p, r.PathValue("id"), r.PathValue("inviteId")); e != nil {
			socialFailure(w, e)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	handle("POST /v1/groups/{id}/transport", func(w http.ResponseWriter, r *http.Request) {
		p, ok := viewer(w, r)
		if !ok {
			return
		}
		var body social.TransportRequest
		if !socialBody(w, r, &body) {
			return
		}
		out, e := store.Transport(r.Context(), p, r.PathValue("id"), body)
		if e != nil {
			socialFailure(w, e)
			return
		}
		write(w, 200, out)
	})
	handle("POST /v1/groups/{id}/settings", func(w http.ResponseWriter, r *http.Request) {
		p, ok := viewer(w, r)
		if !ok {
			return
		}
		var body social.SettingsRequest
		if !socialBody(w, r, &body) {
			return
		}
		out, e := store.UpdateSettings(r.Context(), p, r.PathValue("id"), body)
		if e != nil {
			socialFailure(w, e)
			return
		}
		write(w, 200, out)
	})
	handle("POST /v1/groups/{id}/readiness", func(w http.ResponseWriter, r *http.Request) {
		p, ok := viewer(w, r)
		if !ok {
			return
		}
		var body social.ReadinessRequest
		if !socialBody(w, r, &body) {
			return
		}
		out, e := store.ReportReadiness(r.Context(), p, r.PathValue("id"), body)
		if e != nil {
			socialFailure(w, e)
			return
		}
		write(w, 200, out)
	})
	handle("GET /v1/groups/{id}/queue", func(w http.ResponseWriter, r *http.Request) {
		p, ok := viewer(w, r)
		if !ok {
			return
		}
		out, e := store.ReadQueue(r.Context(), p, r.PathValue("id"))
		if e != nil {
			socialFailure(w, e)
			return
		}
		write(w, 200, out)
	})
	handle("POST /v1/groups/{id}/queue", func(w http.ResponseWriter, r *http.Request) {
		p, ok := viewer(w, r)
		if !ok {
			return
		}
		var body social.QueueRequest
		if !socialBody(w, r, &body) {
			return
		}
		out, e := store.MutateQueue(r.Context(), p, r.PathValue("id"), body)
		if e != nil {
			socialFailure(w, e)
			return
		}
		write(w, 200, out)
	})
	// The group stream had no limiter of its own — only the 512-wide realtime
	// lane, which one client's reconnect loop could take a large share of. This is
	// the same per-viewer fence the notification stream has, and for the same
	// reason: no legitimate client holds four group streams at once, and the lane
	// stays the place total capacity is decided.
	groupStreams := &viewerStreamLimiter{held: map[string]int{}}
	handle("GET /v1/groups/{id}/events", func(w http.ResponseWriter, r *http.Request) {
		p, ok := viewer(w, r)
		if !ok {
			return
		}
		if !groupStreams.admit(p.Hash) {
			socialFailure(w, &social.Fault{Code: "too_many_streams", Status: 429, Message: "Too many event streams are open for this viewer. Close one and retry.", Retryable: true, RetryAfter: 5})
			return
		}
		defer groupStreams.release(p.Hash)
		d.groupEventStream(w, r, store, p, r.PathValue("id"))
	})
}

// groupStreamPerViewer is the per-viewer ceiling on open group event streams.
const groupStreamPerViewer = 4

// viewerStreamLimiter bounds how many long-lived streams one viewer may hold.
type viewerStreamLimiter struct {
	mu   sync.Mutex
	held map[string]int
}

func (l *viewerStreamLimiter) admit(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.held[key] >= groupStreamPerViewer {
		return false
	}
	l.held[key]++
	return true
}

func (l *viewerStreamLimiter) release(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.held[key]--; l.held[key] <= 0 {
		delete(l.held, key)
	}
}

// SSEHeartbeat is how often an idle group stream emits a `heartbeat` event, and
// SSETick is how often it re-reads the ledger and advances the host timeline.
const (
	SSEHeartbeat = 15 * time.Second
	SSETick      = 5 * time.Second
)

// groupEventStream serves GET /v1/groups/{id}/events.
//
// Frames: `id:` is the durable ordinal and `event:` the kind. `Last-Event-ID`
// resumes after that ordinal. The opening `group.snapshot` carries the ordinal it
// was taken at, so a client that only ever echoes the last `id:` it saw resumes
// correctly without reading anything out of the body. A resume that has fallen
// out of the retention window gets one `resume-gap` frame followed by a fresh
// snapshot, and the client discards whatever it had. `heartbeat` and `resume-gap`
// carry no id, so neither moves a client's resume point.
func (d Dependencies) groupEventStream(w http.ResponseWriter, r *http.Request, store *social.Store, p identity.Principal, id string) {
	if _, ok := w.(http.Flusher); !ok {
		socialFailure(w, &social.Fault{Code: "streaming_unsupported", Status: 500, Message: "This server cannot stream events."})
		return
	}
	// Membership is checked before a single byte is written: an event stream is
	// not a place to discover you were never in the group.
	snapshot, e := store.ReadGroup(r.Context(), p, id)
	if e != nil {
		socialFailure(w, e)
		return
	}
	after := int64(-1)
	resumed := false
	if raw := r.Header.Get("Last-Event-ID"); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || n < 0 {
			socialFailure(w, &social.Fault{Code: "invalid_request", Status: 400, Message: "Last-Event-ID must be an ordinal."})
			return
		}
		after, resumed = n, true
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	authorize := func() bool {
		// One live session and indexed membership check per event batch or
		// heartbeat, not one full group projection for every event frame.
		current, err := d.principal(r.WithContext(dbwork.WithClass(r.Context(), dbwork.ClassSecurityFence)))
		if err != nil || current.Authority != p.Authority || current.AccountID != p.AccountID || current.ProfileID != p.ProfileID {
			return false
		}
		joined, err := store.StreamMember(r.Context(), current, id)
		return err == nil && joined
	}
	frame := func(ordinal int64, kind string, payload any) bool {
		if err := r.Context().Err(); err != nil {
			return false
		}
		raw, err := json.Marshal(payload)
		if err != nil {
			return false
		}
		var b strings.Builder
		if ordinal >= 0 {
			b.WriteString("id: " + strconv.FormatInt(ordinal, 10) + "\n")
		}
		b.WriteString("event: " + kind + "\ndata: " + string(raw) + "\n\n")
		// A client that has stopped reading must not hold this goroutine, its lane
		// slot and its database poller until the OS notices. The budget covers this
		// frame only; the heartbeat keeps the stream alive in between.
		if err := frameWrite(w, func(out io.Writer) (int, error) {
			return io.WriteString(out, b.String())
		}); err != nil {
			return false
		}
		return true
	}
	if resumed {
		_, floor, err := store.EventsSince(r.Context(), p, id, after)
		if err != nil {
			return
		}
		// A gap is real when the ledger's oldest surviving ordinal is past the
		// client's resume point, and also when nothing is retained at all but the
		// group has since published past it — otherwise that client would wait
		// forever for events that were pruned out from under it.
		if floor > after+1 || (floor == 0 && snapshot.Group.EventOrdinalHint() > after) {
			if !authorize() || !frame(-1, social.EventResumeGap, map[string]any{"reason": "retention", "resumeFrom": strconv.FormatInt(floor, 10)}) {
				return
			}
			resumed = false
		}
	}
	if !resumed {
		if !authorize() || !frame(snapshot.Group.EventOrdinalHint(), social.EventSnapshot, snapshot) {
			return
		}
		after = snapshot.Group.EventOrdinalHint()
	}
	ticker := time.NewTicker(SSETick)
	defer ticker.Stop()
	lifetime := time.NewTimer(30 * time.Minute)
	defer lifetime.Stop()
	beat := time.NewTicker(SSEHeartbeat)
	defer beat.Stop()
	wake, release := store.Subscribe(id)
	defer release()
	// One sweeper for the process, held for as long as any stream is open, not
	// one sweep per stream per tick. See social.Store.HoldSweeper.
	defer store.HoldSweeper()()
	for {
		events, _, err := store.EventsSince(r.Context(), p, id, after)
		if err != nil {
			return
		}
		if len(events) > 0 && !authorize() {
			return
		}
		for _, event := range events {
			if !frame(event.Ordinal, event.Kind, event.Payload) {
				return
			}
			after = event.Ordinal
			if event.Kind == social.EventEnded {
				return
			}
		}
		select {
		case <-r.Context().Done():
			return
		case <-lifetime.C:
			return
		case <-wake:
		case <-ticker.C:
			// A backstop re-read of the ledger. The host timeline is advanced by
			// the process-wide sweeper this stream holds open, which wakes this
			// loop when it changes anything — so this tick exists only for a wake
			// that was never delivered.
		case <-beat.C:
			if !authorize() || !frame(-1, social.EventHeartbeat, map[string]any{"serverTime": time.Now().UTC().Format(time.RFC3339Nano)}) {
				return
			}
		}
	}
}

func (d Dependencies) receiverRoutes(handle socialHandle, viewer socialViewer, store *social.Store) {
	handle("POST /v1/receivers", func(w http.ResponseWriter, r *http.Request) {
		p, ok := viewer(w, r)
		if !ok {
			return
		}
		var body social.ReceiverRequest
		if !socialBody(w, r, &body) {
			return
		}
		body.DeviceID, _ = d.deviceOf(r.Context(), p)
		p.DeviceID = body.DeviceID
		out, e := store.RegisterReceiver(r.Context(), p, body)
		if e != nil {
			socialFailure(w, e)
			return
		}
		write(w, 201, out)
	})
	handle("GET /v1/receivers", func(w http.ResponseWriter, r *http.Request) {
		p, ok := viewer(w, r)
		if !ok {
			return
		}
		out, e := store.ListReceivers(r.Context(), p)
		if e != nil {
			socialFailure(w, e)
			return
		}
		write(w, 200, out)
	})
	handle("POST /v1/receivers/{id}/heartbeat", func(w http.ResponseWriter, r *http.Request) {
		p, ok := viewer(w, r)
		if !ok {
			return
		}
		var body struct {
			ProtocolVersion string `json:"protocolVersion"`
			KeyFingerprint  string `json:"keyFingerprint"`
		}
		if !socialBody(w, r, &body) {
			return
		}
		p.DeviceID, _ = d.deviceOf(r.Context(), p)
		out, e := store.ReceiverHeartbeat(r.Context(), p, r.PathValue("id"), body.KeyFingerprint)
		if e != nil {
			socialFailure(w, e)
			return
		}
		write(w, 200, out)
	})
	handle("GET /v1/receivers/{id}/inbox", func(w http.ResponseWriter, r *http.Request) {
		p, ok := viewer(w, r)
		if !ok {
			return
		}
		out, e := store.ReadReceiverInbox(r.Context(), p, r.PathValue("id"))
		if e != nil {
			socialFailure(w, e)
			return
		}
		write(w, 200, out)
	})
	// The same inbox, held open. A receiver that cannot keep the notifications event
	// stream (a client whose HTTP stack delivers nothing until a response ends) waits
	// here instead: the answer comes as soon as the inbox holds a grant to decide or a
	// handoff to take, or empty when the wait elapses. The inbox stays the source of
	// truth; receiver events only decide when it is read again, exactly as on the stream.
	inboxWaits := &receiverStreamLimiter{held: map[string]int{}}
	handle("GET /v1/receivers/{id}/inbox/wait", func(w http.ResponseWriter, r *http.Request) {
		p, ok := viewer(w, r)
		if !ok {
			return
		}
		wait := receiverInboxDefaultWait
		if raw := r.URL.Query().Get("waitMs"); raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil || n < 0 || n > receiverInboxMaxWaitSeconds*1000 {
				socialFailure(w, &social.Fault{Code: "invalid_request", Status: 400, Message: "waitMs must be between 0 and " + strconv.Itoa(receiverInboxMaxWaitSeconds*1000) + "."})
				return
			}
			wait = time.Duration(n) * time.Millisecond
		}
		if !inboxWaits.admit(p.Hash) {
			socialFailure(w, &social.Fault{Code: "rate_limited", Status: 429, Message: "Too many receivers are waiting for this viewer. Try again shortly.", Retryable: true, RetryAfter: 5})
			return
		}
		defer inboxWaits.release(p.Hash)
		id := r.PathValue("id")
		// Subscribed before the first read, so an arrival between the read and the
		// wait is not missed. Every subscription opens with a resync, which the first
		// read already covers.
		changes, release := social.SubscribeReceiverEvents(d.DB, p)
		defer release()
		select {
		case <-changes:
		default:
		}
		ctx, cancel := context.WithTimeout(r.Context(), wait+10*time.Second)
		defer cancel()
		deadline := time.NewTimer(wait)
		defer deadline.Stop()
		for {
			out, e := store.ReadReceiverInbox(ctx, p, id)
			if e != nil {
				socialFailure(w, e)
				return
			}
			if len(out.Grants) > 0 || len(out.Handoffs) > 0 {
				write(w, 200, out)
				return
			}
			waiting := true
			for waiting {
				select {
				case <-ctx.Done():
					// The receiver went away (or the server is stopping): nothing to answer.
					return
				case <-deadline.C:
					write(w, 200, out)
					return
				case event := <-changes:
					// A hint for another of this viewer's receivers is not this inbox's.
					waiting = event.ReceiverID != "" && event.ReceiverID != id
				}
			}
		}
	})
	handle("DELETE /v1/receivers/{id}", func(w http.ResponseWriter, r *http.Request) {
		p, ok := viewer(w, r)
		if !ok {
			return
		}
		if e := store.RetireReceiver(r.Context(), p, r.PathValue("id")); e != nil {
			socialFailure(w, e)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	handle("GET /v1/receivers/{id}/grants", func(w http.ResponseWriter, r *http.Request) {
		p, ok := viewer(w, r)
		if !ok {
			return
		}
		out, e := store.ListGrants(r.Context(), p, r.PathValue("id"))
		if e != nil {
			socialFailure(w, e)
			return
		}
		write(w, 200, out)
	})
	handle("POST /v1/receivers/{id}/grants", func(w http.ResponseWriter, r *http.Request) {
		p, ok := viewer(w, r)
		if !ok {
			return
		}
		var body social.GrantRequest
		if !socialBody(w, r, &body) {
			return
		}
		body.ControllerDeviceID, _ = d.deviceOf(r.Context(), p)
		out, e := store.RequestGrant(r.Context(), p, r.PathValue("id"), body)
		if e != nil {
			socialFailure(w, e)
			return
		}
		write(w, 201, out)
	})
	handle("POST /v1/receivers/{id}/grants/{grantId}/decision", func(w http.ResponseWriter, r *http.Request) {
		p, ok := viewer(w, r)
		if !ok {
			return
		}
		var body social.GrantDecision
		if !socialBody(w, r, &body) {
			return
		}
		p.DeviceID, _ = d.deviceOf(r.Context(), p)
		out, e := store.DecideGrant(r.Context(), p, r.PathValue("id"), r.PathValue("grantId"), body)
		if e != nil {
			socialFailure(w, e)
			return
		}
		write(w, 200, out)
	})
	handle("POST /v1/handoffs", func(w http.ResponseWriter, r *http.Request) {
		p, ok := viewer(w, r)
		if !ok {
			return
		}
		var body social.HandoffRequest
		if !socialBody(w, r, &body) {
			return
		}
		out, e := store.PrepareHandoff(r.Context(), p, body)
		if e != nil {
			socialFailure(w, e)
			return
		}
		write(w, 201, out)
	})
	handle("GET /v1/handoffs/{id}", func(w http.ResponseWriter, r *http.Request) {
		p, ok := viewer(w, r)
		if !ok {
			return
		}
		out, e := store.ReadHandoff(r.Context(), p, r.PathValue("id"))
		if e != nil {
			socialFailure(w, e)
			return
		}
		write(w, 200, out)
	})
	handle("POST /v1/handoffs/{id}/readiness", func(w http.ResponseWriter, r *http.Request) {
		p, ok := viewer(w, r)
		if !ok {
			return
		}
		var body social.HandoffReadiness
		if !socialBody(w, r, &body) {
			return
		}
		p.DeviceID, _ = d.deviceOf(r.Context(), p)
		out, e := store.ReportHandoffReadiness(r.Context(), p, r.PathValue("id"), body)
		if e != nil {
			socialFailure(w, e)
			return
		}
		write(w, 200, out)
	})
	handle("POST /v1/handoffs/{id}/commit", func(w http.ResponseWriter, r *http.Request) {
		p, ok := viewer(w, r)
		if !ok {
			return
		}
		var body social.HandoffCommit
		if !socialBody(w, r, &body) {
			return
		}
		out, e := store.CommitHandoff(r.Context(), p, r.PathValue("id"), body)
		if e != nil {
			socialFailure(w, e)
			return
		}
		write(w, 200, out)
	})
	handle("POST /v1/handoffs/{id}/rollback", func(w http.ResponseWriter, r *http.Request) {
		p, ok := viewer(w, r)
		if !ok {
			return
		}
		var body social.HandoffRollback
		if !socialBody(w, r, &body) {
			return
		}
		out, e := store.RollbackHandoff(r.Context(), p, r.PathValue("id"), body)
		if e != nil {
			socialFailure(w, e)
			return
		}
		write(w, 200, out)
	})
}

func (d Dependencies) castRoutes(handle socialHandle, viewer socialViewer, store *social.Store) {
	handle("POST /v1/cast/controllers/register", func(w http.ResponseWriter, r *http.Request) {
		p, ok := viewer(w, r)
		if !ok {
			return
		}
		var body social.CastControllerRegister
		if !socialBody(w, r, &body) {
			return
		}
		out, e := store.CastControllerRegister(r.Context(), p, body)
		if e != nil {
			socialFailure(w, e)
			return
		}
		write(w, 200, out)
	})

	handle("POST /v1/cast/controllers/validate", func(w http.ResponseWriter, r *http.Request) {
		p, ok := viewer(w, r)
		if !ok {
			return
		}
		var body social.CastControllerValidation
		if !socialBody(w, r, &body) {
			return
		}
		out, e := store.CastControllerValidate(r.Context(), p, body)
		if e != nil {
			socialFailure(w, e)
			return
		}
		write(w, 200, out)
	})

	handle("POST /v1/cast/controllers/check", func(w http.ResponseWriter, r *http.Request) {
		p, ok := viewer(w, r)
		if !ok {
			return
		}
		var body social.CastControllerCheck
		if !socialBody(w, r, &body) {
			return
		}
		out, e := store.CastControllerCheck(r.Context(), p, body)
		if e != nil {
			socialFailure(w, e)
			return
		}
		write(w, 200, out)
	})

	handle("POST /v1/cast/bootstrap", func(w http.ResponseWriter, r *http.Request) {
		p, ok := viewer(w, r)
		if !ok {
			return
		}
		var body social.CastBootstrapRequest
		if !socialBody(w, r, &body) {
			return
		}
		out, e := store.CastBootstrapStart(r.Context(), p, body)
		if e != nil {
			socialFailure(w, e)
			return
		}
		write(w, 201, out)
	})
	handle("DELETE /v1/cast/bootstrap/{id}", func(w http.ResponseWriter, r *http.Request) {
		p, ok := viewer(w, r)
		if !ok {
			return
		}
		out, err := store.CastBootstrapCancel(r.Context(), p, r.PathValue("id"))
		if err != nil {
			socialFailure(w, err)
			return
		}
		write(w, 200, out)
	})
	handle("GET /v1/cast/bootstrap/{id}", func(w http.ResponseWriter, r *http.Request) {
		p, ok := viewer(w, r)
		if !ok {
			return
		}
		out, e := store.CastBootstrapStatus(r.Context(), p, r.PathValue("id"))
		if e != nil {
			socialFailure(w, e)
			return
		}
		write(w, 200, out)
	})
	// Redeem and reconnect are the receiver's only unauthenticated routes: a
	// Cast receiver page has no bearer until it has redeemed a code. Both are
	// rate limited and both fail closed on an unknown or spent credential.
	handle("POST /v1/cast/preview", func(w http.ResponseWriter, r *http.Request) {
		var body social.CastPreviewRequest
		if !socialBody(w, r, &body) {
			return
		}
		body.Source = clientNetwork(clientAddress(r, d.trustedProxyPrefixes(r.Context())))
		out, e := store.CastPreview(r.Context(), body)
		if e != nil {
			socialFailure(w, e)
			return
		}
		write(w, 200, out)
	})
	handle("POST /v1/cast/redeem", func(w http.ResponseWriter, r *http.Request) {
		var body social.CastRedeemRequest
		if !socialBody(w, r, &body) {
			return
		}
		body.Source = clientNetwork(clientAddress(r, d.trustedProxyPrefixes(r.Context())))
		out, e := store.CastRedeem(r.Context(), body)
		if e != nil {
			socialFailure(w, e)
			return
		}
		write(w, 200, out)
	})
	handle("POST /v1/cast/reconnect", func(w http.ResponseWriter, r *http.Request) {
		var body social.CastReconnectRequest
		if !socialBody(w, r, &body) {
			return
		}
		body.Source = clientNetwork(clientAddress(r, d.trustedProxyPrefixes(r.Context())))
		out, e := store.CastReconnect(r.Context(), body)
		if e != nil {
			socialFailure(w, e)
			return
		}
		write(w, 200, out)
	})
	handle("DELETE /v1/cast/devices/{id}", func(w http.ResponseWriter, r *http.Request) {
		p, ok := viewer(w, r)
		if !ok {
			return
		}
		if e := store.RevokeCastDevice(r.Context(), p, r.PathValue("id"), r.Header.Get("If-Match")); e != nil {
			socialFailure(w, e)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	handle("GET /v1/cast/configuration", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := viewer(w, r); !ok {
			return
		}
		write(w, 200, map[string]any{"protocolVersion": social.Protocol, "applicationId": persistence.Get(d.DB, CastApplicationSetting), "receiverUrl": "/receiver/cast/"})
	})
	handle("PUT /v1/cast/configuration", func(w http.ResponseWriter, r *http.Request) {
		p, e := d.owner(r)
		if e != nil {
			socialFailure(w, e)
			return
		}
		_ = p
		var body struct {
			ProtocolVersion string `json:"protocolVersion"`
			ApplicationID   string `json:"applicationId"`
		}
		if !socialBody(w, r, &body) {
			return
		}
		if len(body.ApplicationID) > 64 || strings.ContainsAny(body.ApplicationID, " \t\r\n") {
			socialFailure(w, &social.Fault{Code: "invalid_request", Status: 400, Message: "A Cast application id is a short opaque token."})
			return
		}
		if e = persistence.Set(d.DB, CastApplicationSetting, body.ApplicationID); e != nil {
			socialFailure(w, e)
			return
		}
		write(w, 200, map[string]any{"protocolVersion": social.Protocol, "applicationId": body.ApplicationID, "receiverUrl": "/receiver/cast/"})
	})
	// The Cast receiver application is a static bundle compiled into the server
	// binary, so every GOOS and the Docker image serve the identical bytes with
	// no build step and no external host.
}

func (d Dependencies) serveCastBundle(mux *http.ServeMux) {
	files := http.StripPrefix("/receiver/", http.FileServerFS(receiver.Files()))
	mux.Handle("GET /receiver/cast/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// CAF loads its player from gstatic and injects player styles. Media and
		// credentials remain on this server; only Google's Cast host may frame it.
		policy := castReceiverCSP
		if r.URL.Path == "/receiver/cast/" || r.URL.Path == "/receiver/cast/index.html" {
			policy = castReceiverPageCSP
		}
		w.Header().Set("Content-Security-Policy", policy)
		w.Header().Del("X-Frame-Options") // CSP supplies the narrower Cast exception.
		files.ServeHTTP(w, r)
	}))
}

const castReceiverCSP = "default-src 'none'; script-src 'self' https://www.gstatic.com/cast/; style-src 'self' 'unsafe-inline'; connect-src 'self' https://www.gstatic.com/cast/; img-src 'self' blob: data:; font-src 'self'; media-src 'self' blob:; worker-src 'self' blob:; frame-ancestors https://www.gstatic.com; base-uri 'none'; object-src 'none'; form-action 'self'"

// CAF 3.0.0156 loads Google's hosted Shaka 4.15.56 itself. Only the receiver
// document permits that script path and its empty caption style (CSSOM rules
// are not hash-bound). Page attributes/nonempty styles stay blocked; assets and
// every other page retain policy.
var castReceiverPageCSP = strings.Replace(strings.Replace(castReceiverCSP,
	"script-src 'self' https://www.gstatic.com/cast/;",
	"script-src 'self' https://www.gstatic.com/cast/ https://ajax.googleapis.com/ajax/libs/shaka-player/4.15.56/;", 1),
	"style-src 'self' 'unsafe-inline';", "style-src 'self'; style-src-elem 'self' https://www.gstatic.com 'sha256-47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU=';", 1)

// socialPlayback is the social store's view of v1 sessions (B8a): a handoff's
// source and a group's host session, with the item's restrictions applied to a
// source the caller hands off.
type socialPlayback struct {
	d  Dependencies
	v1 *playbackv1.Service
}

func (a socialPlayback) SessionTx(ctx context.Context, tx *sql.Tx, p identity.Principal, id string) (social.SessionFacts, error) {
	f, err := a.v1.SocialSessionTx(ctx, tx, p, id)
	if err != nil {
		return social.SessionFacts{}, err
	}
	if f.ItemID != "" {
		if err = a.d.itemRestrictionsTx(ctx, tx, p, f.ItemID); err != nil {
			return social.SessionFacts{}, err
		}
	}
	return social.SessionFacts(f), nil
}

func (a socialPlayback) DeviceSessionTx(ctx context.Context, tx *sql.Tx, device string) (social.SessionFacts, bool, error) {
	f, present, err := a.v1.DeviceSessionTx(ctx, tx, device)
	return social.SessionFacts(f), present, err
}

func (a socialPlayback) EndSessionTx(ctx context.Context, tx *sql.Tx, p identity.Principal, id, reason string) (func(), error) {
	return a.v1.EndSessionTx(ctx, tx, p, id, reason)
}
