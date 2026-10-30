package webhooks

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"github.com/PorticoMediaServer/portico/server/internal/supervise"
	"strconv"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/entityid"
	"github.com/PorticoMediaServer/portico/server/internal/worker"
)

// Delivery rules, in one place:
//
//   - One POST per attempt, JSON, at most maxBody bytes, signed.
//   - 2xx is delivered. Anything else, including a redirect (never followed),
//     is a failed attempt.
//   - A failed attempt is retried after the next delay in retryDelays; when
//     they run out the delivery is given up and stays in the log as failed.
//   - Deliveries to one webhook go in order, one at a time. A webhook that is
//     failing is left alone for a while so a dead address costs one timeout per
//     pause, not one per waiting delivery.
//   - Every attempt is bounded: connect, TLS, response headers and total time,
//     and the response body is read to a small limit and thrown away.
var retryDelays = []time.Duration{30 * time.Second, 2 * time.Minute, 10 * time.Minute, 30 * time.Minute, time.Hour, 2 * time.Hour, 4 * time.Hour}

const (
	attemptTimeout   = 15 * time.Second
	maxBody          = 64 << 10
	maxResponseRead  = 64 << 10
	perStep          = 64
	perWebhookStep   = 8
	parallelWebhooks = 4
	maxPause         = 10 * time.Minute
	idlePoll         = 20 * time.Second
)

// Signature headers. The signature is HMAC-SHA256, keyed with the webhook's
// secret, over the timestamp, a full stop, and the exact request body.
const (
	HeaderSignature = "Portico-Signature"
	HeaderEvent     = "Portico-Event"
	HeaderEventID   = "Portico-Event-Id"
	HeaderAttempt   = "Portico-Delivery-Attempt"
)

// Sign returns the Portico-Signature header value for body at unix time t.
func Sign(secret string, t int64, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(strconv.FormatInt(t, 10)))
	mac.Write([]byte{'.'})
	mac.Write(body)
	return "t=" + strconv.FormatInt(t, 10) + ",v1=" + hex.EncodeToString(mac.Sum(nil))
}

// Dispatcher sends what is queued.
type Dispatcher struct {
	Store *Store
	// Server names this server in every payload.
	Server func() (id, name string)
	// UserAgent identifies the sender; it carries the server version.
	UserAgent string
	// Client is replaced in tests. The default follows no redirect and uses no
	// proxy: the owner's URL is exactly where the request goes.
	Client *http.Client

	mu     sync.Mutex
	paused map[string]time.Time
	streak map[string]int
	signal *worker.Signal
}

func NewDispatcher(store *Store) *Dispatcher {
	d := &Dispatcher{Store: store, paused: map[string]time.Time{}, streak: map[string]int{}, signal: worker.NewSignal(), UserAgent: "Portico-Webhooks"}
	transport := &http.Transport{
		Proxy:                  nil,
		DialContext:            (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
		TLSHandshakeTimeout:    5 * time.Second,
		ResponseHeaderTimeout:  10 * time.Second,
		MaxResponseHeaderBytes: 16 << 10,
		DisableKeepAlives:      true,
		TLSClientConfig:        &tls.Config{MinVersion: tls.VersionTLS12},
	}
	d.Client = &http.Client{Transport: transport, Timeout: attemptTimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if store != nil {
		store.Wake = d.signal.Wake
	}
	return d
}

// Run sends until ctx ends. It wakes when a delivery is queued and otherwise
// looks again when the next retry is due.
func (d *Dispatcher) Run(ctx context.Context) {
	unregister := dbwork.WakeOnTables(d.signal, "webhook_deliveries", "webhook_events", "webhooks")
	defer unregister()
	worker.Run(ctx, "webhooks.dispatch", d.signal, func(ctx context.Context) time.Duration {
		return d.Step(dbwork.WithClass(ctx, dbwork.ClassMaintenance))
	})
}

type queued struct {
	id, webhook, event, data, url, secret string
	attempts                              int
	created                               int64
	generation                            int64
}

// Step sends what is due and returns how long until there may be more.
func (d *Dispatcher) Step(ctx context.Context) time.Duration {
	store := d.Store
	if store == nil || store.DB == nil {
		return 0
	}
	now := store.now()
	// Select destinations before deliveries: one destination's backlog must
	// never occupy the whole pass while another has something ready to send.
	rows, err := dbwork.Query(ctx, store.DB, `SELECT w.id FROM webhooks w
 WHERE EXISTS(SELECT 1 FROM webhook_deliveries d WHERE d.webhook_id=w.id AND d.state='pending' AND d.authority_generation=w.authority_generation AND w.owner_review_required=0 AND (w.enabled=1 OR d.type=?)) ORDER BY w.id LIMIT ?`, Test, MaxWebhooks)
	if err != nil {
		return idlePoll
	}
	order := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			break
		}
		if !d.isPaused(id, store.Now()) {
			order = append(order, id)
		}
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return idlePoll
	}
	byWebhook := map[string][]queued{}
	share := min(perWebhookStep, perStep/max(1, len(order)))
	for _, id := range order {
		rows, err = dbwork.Query(ctx, store.DB, `SELECT d.id,d.webhook_id,d.type,d.data,d.attempts,d.created_ms,w.url,w.secret,d.next_attempt_ms,d.authority_generation FROM webhook_deliveries d JOIN webhooks w ON w.id=d.webhook_id
 WHERE d.webhook_id=? AND d.state='pending' AND d.authority_generation=w.authority_generation AND w.owner_review_required=0 AND (w.enabled=1 OR d.type=?) ORDER BY d.created_ms,d.rowid LIMIT ?`, id, Test, share)
		if err != nil {
			return idlePoll
		}
		for rows.Next() {
			var q queued
			var due int64
			if err = rows.Scan(&q.id, &q.webhook, &q.event, &q.data, &q.attempts, &q.created, &q.url, &q.secret, &due, &q.generation); err != nil {
				break
			}
			// A head waiting for its retry or initial delay holds back everything
			// after it. rowid keeps insertion order for events in the same millisecond.
			if due > now {
				break
			}
			byWebhook[id] = append(byWebhook[id], q)
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
		if err != nil {
			return idlePoll
		}
	}
	slots := make(chan struct{}, parallelWebhooks)
	var group sync.WaitGroup
	for _, webhook := range order {
		if d.isPaused(webhook, store.Now()) {
			continue
		}
		batch := byWebhook[webhook]
		if len(batch) > perWebhookStep {
			batch = batch[:perWebhookStep]
		}
		group.Add(1)
		slots <- struct{}{}
		supervise.Go("webhooks.delivery", func() {
			defer group.Done()
			defer func() { <-slots }()
			for _, q := range batch {
				if ctx.Err() != nil || !d.attempt(ctx, q) {
					return
				}
			}
		})
	}
	group.Wait()
	// When is the next thing due? Nothing waiting and nobody subscribed means
	// there is nothing to wake for but a new row.
	var next sql.NullInt64
	var enabled int
	if err = dbwork.QueryRow(ctx, store.DB, `SELECT (SELECT min(d.next_attempt_ms) FROM webhooks w JOIN webhook_deliveries d ON d.id=(SELECT h.id FROM webhook_deliveries h WHERE h.webhook_id=w.id AND h.state='pending' AND h.authority_generation=w.authority_generation AND w.owner_review_required=0 AND (w.enabled=1 OR h.type=?) ORDER BY h.created_ms,h.rowid LIMIT 1)),(SELECT count(*) FROM webhooks WHERE enabled=1)`, Test).Scan(&next, &enabled); err != nil {
		return idlePoll
	}
	if !next.Valid {
		if enabled == 0 {
			return 0
		}
		return idlePoll
	}
	wait := time.Duration(next.Int64-store.now()) * time.Millisecond
	if wait < time.Second {
		wait = time.Second
	}
	if wait > idlePoll {
		wait = idlePoll
	}
	return wait
}

func (d *Dispatcher) isPaused(webhook string, now time.Time) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	until, ok := d.paused[webhook]
	if ok && now.Before(until) {
		return true
	}
	delete(d.paused, webhook)
	return false
}

func (d *Dispatcher) settle(webhook string, delivered bool, now time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if delivered {
		delete(d.streak, webhook)
		delete(d.paused, webhook)
		return
	}
	d.streak[webhook]++
	pause := 15 * time.Second << min(d.streak[webhook]-1, 6)
	if pause > maxPause {
		pause = maxPause
	}
	d.paused[webhook] = now.Add(pause)
	if len(d.paused) > 4*MaxWebhooks {
		for key, until := range d.paused {
			if now.After(until) {
				delete(d.paused, key)
				delete(d.streak, key)
			}
		}
	}
}

// attempt makes one attempt and records it. It reports whether the webhook's
// queue should continue this step.
func (d *Dispatcher) attempt(ctx context.Context, q queued) bool {
	store := d.Store
	life, release, err := store.admitSend(ctx, q)
	if err != nil {
		return false
	}
	defer release()
	body, err := d.payload(life, q)
	status, code := 0, ""
	started := time.Now()
	if err != nil {
		code = "payload_too_large"
	} else {
		status, code = d.post(life, q, body)
	}
	elapsed := time.Since(started).Milliseconds()
	now := store.now()
	delivered := code == ""
	attempts := q.attempts + 1
	accepted := false
	err = dbwork.WithWriteTx(ctx, store.DB, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
		current, e := currentDeliveryTx(ctx, tx, q)
		if e != nil || !current {
			return e
		}
		accepted = true
		switch {
		case delivered:
			if _, e := tx.ExecContext(ctx, `UPDATE webhook_deliveries SET state='delivered',attempts=?,finished_ms=?,status=?,duration_ms=?,error='' WHERE id=? AND state='pending'`, attempts, now, status, elapsed, q.id); e != nil {
				return e
			}
			if _, e := tx.ExecContext(ctx, `UPDATE webhooks SET failures=0 WHERE id=? AND failures<>0`, q.webhook); e != nil {
				return e
			}
		case attempts > len(retryDelays) || code == "payload_too_large":
			if _, e := tx.ExecContext(ctx, `UPDATE webhook_deliveries SET state='failed',attempts=?,finished_ms=?,status=?,duration_ms=?,error=? WHERE id=? AND state='pending'`, attempts, now, status, elapsed, code, q.id); e != nil {
				return e
			}
			if _, e := tx.ExecContext(ctx, `UPDATE webhooks SET failures=failures+1 WHERE id=?`, q.webhook); e != nil {
				return e
			}
		default:
			next := now + retryDelays[attempts-1].Milliseconds()
			if _, e := tx.ExecContext(ctx, `UPDATE webhook_deliveries SET attempts=?,next_attempt_ms=?,status=?,duration_ms=?,error=? WHERE id=? AND state='pending'`, attempts, next, status, elapsed, code, q.id); e != nil {
				return e
			}
			if _, e := tx.ExecContext(ctx, `UPDATE webhooks SET failures=failures+1 WHERE id=?`, q.webhook); e != nil {
				return e
			}
		}
		// The log keeps the most recent finished deliveries and no more.
		return pruneDeliveryLog(ctx, tx, q.webhook)
	})
	if err != nil || !accepted {
		return false
	}
	d.settle(q.webhook, delivered, store.Now())
	if !delivered && attempts > len(retryDelays) {
		// Neither the address nor the secret is ever logged.
		log.Printf("Webhook %.8s: gave up on a %s delivery after %d attempts (%s)", q.webhook, q.event, attempts, code)
	}
	return delivered
}

// payload builds the request body: the same envelope for every event.
func (d *Dispatcher) payload(ctx context.Context, q queued) ([]byte, error) {
	data := map[string]any{}
	if json.Unmarshal([]byte(q.data), &data) != nil {
		data = map[string]any{}
	}
	d.present(ctx, q.event, data)
	server := map[string]string{}
	if d.Server != nil {
		id, name := d.Server()
		server["id"], server["name"] = id, name
	}
	body, err := json.Marshal(struct {
		ID        string            `json:"id"`
		Type      string            `json:"type"`
		CreatedAt string            `json:"createdAt"`
		Server    map[string]string `json:"server"`
		Data      map[string]any    `json:"data"`
	}{q.id, q.event, stamp(q.created), server, data})
	if err != nil || len(body) > maxBody {
		return nil, errors.New("payload too large")
	}
	return body, nil
}

// present turns what the schema's triggers recorded into what is published:
// public ids instead of internal ones, kind names instead of numbers, and, for
// a title that has been matched since it was added, the title it has now.
func (d *Dispatcher) present(ctx context.Context, event string, data map[string]any) {
	public := func(key, as string) string {
		raw, _ := data[key].(string)
		delete(data, key)
		decoded, err := hex.DecodeString(raw)
		if err != nil || len(decoded) != entityid.Size {
			return ""
		}
		id := entityid.Encode(decoded)
		data[as] = id
		return raw
	}
	entity := public("entity", "itemId")
	if _, ok := data["parent"]; ok {
		public("parent", "parentId")
	}
	if number, ok := data["kind"].(float64); ok {
		if name, err := compactcatalog.Kind(int(number)).Name(); err == nil {
			data["kind"] = name
		} else {
			delete(data, "kind")
		}
	}
	for key, value := range data {
		if value == nil {
			delete(data, key)
		}
	}
	if entity != "" && (event == TitleAdded || event == TitleUpdated) && d.Store != nil {
		var title string
		var year int
		blob, _ := hex.DecodeString(entity)
		if dbwork.QueryRow(ctx, d.Store.DB, `SELECT title,year FROM catalog_entities WHERE public_id=? AND retired=0`, blob).Scan(&title, &year) == nil {
			data["title"], data["year"] = title, year
		}
	}
	if year, ok := data["year"].(float64); ok && year == 0 {
		delete(data, "year")
	}
	if year, ok := data["year"].(int); ok && year == 0 {
		delete(data, "year")
	}
}

// post sends one attempt. It returns the HTTP status (0 when there was no
// response) and a short reason for a failure, or "" for a delivery. The reason
// never contains the address.
func (d *Dispatcher) post(ctx context.Context, q queued, body []byte) (int, string) {
	if ctx.Err() != nil {
		return 0, "authority_retired"
	}
	if !ValidURL(q.url) {
		return 0, "address_not_valid"
	}
	ctx, cancel := context.WithTimeout(ctx, attemptTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, q.url, bytes.NewReader(body))
	if err != nil {
		return 0, "address_not_valid"
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", d.UserAgent)
	request.Header.Set(HeaderEvent, q.event)
	request.Header.Set(HeaderEventID, q.id)
	request.Header.Set(HeaderAttempt, strconv.Itoa(q.attempts+1))
	request.Header.Set(HeaderSignature, Sign(q.secret, d.Store.Now().Unix(), body))
	response, err := d.Client.Do(request)
	if err != nil {
		var netError net.Error
		var certificate *tls.CertificateVerificationError
		var dial *net.OpError
		switch {
		case errors.Is(err, context.DeadlineExceeded), errors.As(err, &netError) && netError.Timeout():
			return 0, "timed_out"
		case errors.As(err, &certificate):
			return 0, "certificate_not_trusted"
		case errors.As(err, &dial) && dial.Op == "dial":
			return 0, "could_not_connect"
		}
		return 0, "request_failed"
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxResponseRead))
	switch {
	case response.StatusCode >= 200 && response.StatusCode < 300:
		return response.StatusCode, ""
	case response.StatusCode >= 300 && response.StatusCode < 400:
		return response.StatusCode, "redirect_not_followed"
	}
	return response.StatusCode, fmt.Sprintf("status_%d", response.StatusCode)
}
