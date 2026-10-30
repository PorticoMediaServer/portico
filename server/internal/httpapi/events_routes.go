package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/server/internal/apievents"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/eventfeed"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/social"
	"github.com/PorticoMediaServer/portico/server/internal/supervise"
)

var eventHeartbeat = 25 * time.Second

// eventContinuation paces a stream's next page after one that stopped at the
// byte budget (More): a reader catching up is served page by page, never in a
// tight loop that holds the connection's writer.
var eventContinuation = time.Second

const eventStreamLife = 30 * time.Minute

func eventFailure(w http.ResponseWriter, status int, code, message string) {
	consoleWrite(w, status, map[string]any{"error": map[string]any{
		"code": code, "message": message, "retryable": status == 409 || status == 429 || status == 503,
	}})
}

func (d Dependencies) eventAuthority(r *http.Request) (identity.Principal, bool, error) {
	p, err := d.strictPrincipalFor(r)
	if err != nil {
		return p, false, err
	}
	if err = d.checkCapacityReader(r, p); err != nil {
		return p, false, err
	}
	if credential, ok := identity.APICredentialFrom(r.Context()); ok {
		granted := make([]apikit.Scope, 0, len(credential.Scopes))
		for _, scope := range credential.Scopes {
			granted = append(granted, apikit.Scope(scope))
		}
		if !apikit.ScopeAllows(granted, apikit.ScopeAdmin) {
			return p, false, nil
		}
	}
	// The owner and administrators hear the administration audience (lists,
	// sessions, tasks); each is re-checked against the live membership.
	switch p.Role {
	case "owner":
		_, err = d.owner(r)
	case "admin":
		_, err = d.administrator(r)
	default:
		return p, false, nil
	}
	if err != nil {
		if errors.Is(err, identity.ErrUnauthorized) || errors.Is(err, identity.ErrForbidden) {
			return p, false, nil
		}
		return p, false, err
	}
	return p, true, nil
}

func (d Dependencies) events(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if err := consoleQuery(r, "after", "waitMs"); err != nil {
		eventFailure(w, 400, "invalid_request", "This event query cannot be used.")
		return
	}
	longPoll := r.URL.Query().Has("waitMs")
	if !longPoll && r.URL.Query().Has("after") {
		eventFailure(w, 400, "invalid_request", "Stream resumes with Last-Event-ID.")
		return
	}
	raw := r.Header.Get("Last-Event-ID")
	if longPoll {
		raw = r.URL.Query().Get("after")
	}
	if !eventfeed.CursorWellFormed(raw) {
		eventFailure(w, 400, "invalid_cursor", "This event cursor cannot be used.")
		return
	}
	wait := 25000
	if longPoll {
		var err error
		wait, err = strconv.Atoi(r.URL.Query().Get("waitMs"))
		if err != nil || wait < 0 || wait > 25000 {
			eventFailure(w, 400, "invalid_request", "waitMs must be between 0 and 25000.")
			return
		}
	}
	p, owner, err := d.eventAuthority(r)
	if err != nil {
		consoleError(w, err)
		return
	}
	r, err = d.withCapacityAdmission(r, p)
	if err != nil {
		consoleError(w, err)
		return
	}
	// A parked poll holds a realtime slot for up to 25 s. One installation
	// gets ParkedPolls of them; the next is refused before any work, and the
	// client reads without parking until it may wait again (SRV017).
	if longPoll && wait > 0 {
		key, err := d.Events.InstallationKey(r.Context(), p)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				eventFailure(w, 401, "unauthorized", "Authentication is required.")
			} else {
				consoleError(w, err)
			}
			return
		}
		release, admitted := d.Events.AdmitPoll(key)
		if !admitted {
			w.Header().Set("Retry-After", "2")
			eventFailure(w, 429, "event_wait_limit", "This device already has its event waits open. Read without waiting, then wait again.")
			return
		}
		defer release()
	}
	bridgeCtx, stopBridge := context.WithCancel(r.Context())
	defer stopBridge()
	d.bridgeReceiverEvents(bridgeCtx, p)
	wake, release := d.Events.SubscribeForContext(r.Context(), p, owner)
	defer release()
	page, err := d.Events.Read(r.Context(), p, owner, raw)
	if err != nil {
		consoleError(w, err)
		return
	}
	if longPoll {
		d.eventLongPoll(w, r, p, wake, page, wait)
		return
	}
	d.eventStream(w, r, p, wake, page)
}

// A long poll answers with the first page that has anything in it, or empty
// at the deadline. Every page is one bounded read; a page that stopped at the
// byte budget is answered at once and the client paces its continuation.
func (d Dependencies) eventLongPoll(w http.ResponseWriter, r *http.Request, p identity.Principal, wake <-chan struct{}, page eventfeed.Page, wait int) {
	if len(page.Events) == 0 && wait > 0 {
		timer := time.NewTimer(time.Duration(wait) * time.Millisecond)
		defer timer.Stop()
		for len(page.Events) == 0 {
			select {
			case <-r.Context().Done():
				return
			case <-timer.C:
				goto answer
			case <-wake:
			}
			fresh, owner, err := d.eventAuthority(r)
			if err != nil {
				consoleError(w, err)
				return
			}
			if fresh.Hash != p.Hash {
				eventFailure(w, 401, "unauthorized", "Authentication is required.")
				return
			}
			page, err = d.Events.Read(r.Context(), fresh, owner, page.NextAfter)
			if err != nil {
				consoleError(w, err)
				return
			}
		}
	}
answer:
	if _, _, err := d.eventAuthority(r); err != nil {
		consoleError(w, err)
		return
	}
	consoleWrite(w, 200, page)
}

func (d Dependencies) eventStream(w http.ResponseWriter, r *http.Request, p identity.Principal, wake <-chan struct{}, page eventfeed.Page) {
	if _, ok := w.(http.Flusher); !ok {
		eventFailure(w, 503, "stream_unavailable", "This connection cannot stream events.")
		return
	}
	key, err := d.Events.InstallationKey(r.Context(), p)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			eventFailure(w, 401, "unauthorized", "Authentication is required.")
		} else {
			consoleError(w, err)
		}
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	release, admitted := d.Events.AdmitStream(key, cancel)
	if !admitted {
		eventFailure(w, 409, "event_stream_exists", "An existing stream was retired; reconnect using your last event ID.")
		return
	}
	defer release()
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(200)
	lastSent := ""
	send := func(e eventfeed.Event) bool {
		if e.Type == "capacity.changed" {
			if err := d.checkCapacityReader(r.WithContext(ctx), p); err != nil {
				return false
			}
		}
		b, err := json.Marshal(e)
		if err != nil {
			return false
		}
		if err = frameWrite(w, func(out io.Writer) (int, error) {
			return fmt.Fprintf(out, "id: %s\nevent: %s\ndata: %s\n\n", e.ID, e.Type, b)
		}); err != nil {
			return false
		}
		lastSent = e.ID
		return true
	}
	if err = frameWrite(w, func(out io.Writer) (int, error) {
		return fmt.Fprint(out, "retry: 1000\n\n")
	}); err != nil {
		return
	}
	// A stream that opens with nothing waiting says where it stands. Without
	// this a client learns its position only from the first real event, and a
	// connection that drops before one arrives would resume from "now" and miss
	// whatever happened in between.
	if len(page.Events) == 0 && page.NextAfter != "" {
		if !send(eventfeed.Event{ID: page.NextAfter, Type: "stream.opened", At: time.Now().UTC().Format(time.RFC3339)}) {
			return
		}
	}
	// One page per turn. A page that stopped at the byte budget schedules its
	// continuation eventContinuation later; wakes meanwhile only re-check
	// authority, because the continuation reads everything they announced.
	flushPage := func() bool {
		fresh, _, err := d.eventAuthority(r.WithContext(ctx))
		if err != nil || fresh.Hash != p.Hash {
			return false
		}
		for _, e := range page.Events {
			if !send(e) {
				return false
			}
		}
		if !page.More && page.NextAfter != "" && lastSent != page.NextAfter {
			if !send(eventfeed.Event{ID: page.NextAfter, Type: "stream.position"}) {
				return false
			}
		}
		return true
	}
	if !flushPage() {
		return
	}
	continuation := time.NewTimer(eventContinuation)
	defer continuation.Stop()
	pending := page.More
	if !pending {
		continuation.Stop()
	}
	heartbeat := time.NewTicker(eventHeartbeat)
	defer heartbeat.Stop()
	deadline, err := d.Events.TokenDeadline(ctx, p)
	if err != nil {
		return
	}
	life := time.Until(deadline)
	if life < 0 {
		life = 0
	}
	if life > eventStreamLife {
		life = eventStreamLife
	}
	end := time.NewTimer(life)
	defer end.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-end.C:
			send(eventfeed.Event{ID: page.NextAfter, Type: "stream.closed", Data: json.RawMessage(`{"reconnectAfterMs":1000}`)})
			return
		case <-heartbeat.C:
			if err := frameWrite(w, func(out io.Writer) (int, error) {
				return fmt.Fprint(out, ": heartbeat\n\n")
			}); err != nil {
				return
			}
			continue
		case <-continuation.C:
			pending = false
		case <-wake:
			if pending {
				if fresh, _, err := d.eventAuthority(r.WithContext(ctx)); err != nil || fresh.Hash != p.Hash {
					return
				}
				continue
			}
		}
		fresh, owner, err := d.eventAuthority(r.WithContext(ctx))
		if err != nil || fresh.Hash != p.Hash {
			return
		}
		page, err = d.Events.Read(ctx, fresh, owner, page.NextAfter)
		if err != nil {
			return
		}
		if !flushPage() {
			return
		}
		if page.More {
			pending = true
			continuation.Reset(eventContinuation)
		}
	}
}

func (d Dependencies) bridgeReceiverEvents(ctx context.Context, p identity.Principal) {
	changes, release := social.SubscribeReceiverEvents(d.DB, p)
	supervise.Go("api.receiver-events", func() {
		defer release()
		first := true
		for {
			select {
			case <-ctx.Done():
				return
			case event := <-changes:
				if first && event.Kind == "resync" {
					first = false
					continue
				}
				first = false
				data, err := json.Marshal(event)
				if err != nil {
					continue
				}
				_ = dbwork.WithWriteTx(ctx, d.DB, dbwork.ClassInteractive, func(tx *sql.Tx) error {
					_, err := tx.ExecContext(ctx, `INSERT INTO api_change_pending(audience,type,resource_kind,resource_id,revision,data) VALUES(?,'receiver.inbox_changed','receiver',?,'',?) ON CONFLICT(audience,type,resource_kind,resource_id) DO UPDATE SET data=excluded.data`, apievents.ProfileAudience(p.Authority, p.AccountID, p.ProfileID), event.ReceiverID, string(data))
					return err
				})
			}
		}
	})
}
