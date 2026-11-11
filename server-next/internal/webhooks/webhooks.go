// Package webhooks sends the owner's webhooks. Any part of the server calls Emit with an
// event; it is queued in the outbox for every switched-on webhook that wants it, and the
// sender delivers each webhook's messages in order, signed, retrying for up to 8 hours,
// across a restart too.
package webhooks

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/secrets"
)

// The events a webhook can choose. Each part of the server sends its own; an event no part
// sends yet can still be chosen and starts arriving when that part is built.
const (
	TitleAdded        = "title.added"
	TitleUpdated      = "title.updated"
	TitleRemoved      = "title.removed"
	ScanStarted       = "scan.started"
	ScanFinished      = "scan.finished"
	PlaybackStarted   = "playback.started"
	PlaybackPaused    = "playback.paused"
	PlaybackResumed   = "playback.resumed"
	PlaybackStopped   = "playback.stopped"
	RecordingFinished = "recording.finished"
	RecordingFailed   = "recording.failed"
	MemberSignedIn    = "member.signed_in"
	BackupSucceeded   = "backup.succeeded"
	BackupFailed      = "backup.failed"
	StorageLow        = "storage.low"
	UpdateAvailable   = "update.available"
	ServerStarted     = "server.started"
	// Test is only ever sent by Send test.
	Test = "webhook.test"
)

// Events lists the events a webhook can choose, in the order the console shows them.
var Events = []string{TitleAdded, TitleUpdated, TitleRemoved, ScanStarted, ScanFinished, PlaybackStarted, PlaybackPaused,
	PlaybackResumed, PlaybackStopped, RecordingFinished, RecordingFailed, MemberSignedIn, BackupSucceeded, BackupFailed,
	StorageLow, UpdateAvailable, ServerStarted}

// Fixed delivery values. The old product had settings for these; nobody needs to choose them.
var (
	// retryAfter is the wait after each failed try. The last try is a little under 8 hours
	// after the message was made.
	retryAfter = []time.Duration{30 * time.Second, 2 * time.Minute, 10 * time.Minute, 30 * time.Minute, time.Hour, 2 * time.Hour, 4 * time.Hour}
	// giveUpAfter: a message still not delivered this long after it was made is given up.
	giveUpAfter = 8 * time.Hour
)

const (
	// attemptTimeout bounds one try from connecting to the end of the answer: an answer of
	// success within it counts as arrived.
	attemptTimeout = 15 * time.Second
	// maxPayload bounds a message. Messages carry names and ids, never lists, so this is far
	// above any real one.
	maxPayload = 64 << 10
	// maxAnswer is how much of the answer is read before the connection is dropped. The
	// answer is never kept or shown.
	maxAnswer = 4 << 10
	// sendingAtOnce is how many webhooks are sent to at the same time.
	sendingAtOnce = 4
)

// Header names. The signature is HMAC-SHA256 keyed with the webhook's secret, over the
// Unix time, a full stop and the exact body: "t=1700000000,v1=<hex>".
const (
	HeaderSignature = "Portico-Signature"
	HeaderEvent     = "Portico-Event"
	HeaderAttempt   = "Portico-Delivery-Attempt"
)

// Sign is the signature header value for body, sent at Unix time t.
func Sign(secret []byte, t int64, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	fmt.Fprintf(mac, "%d.", t)
	mac.Write(body)
	return "t=" + strconv.FormatInt(t, 10) + ",v1=" + hex.EncodeToString(mac.Sum(nil))
}

// Service queues and sends webhook messages.
type Service struct {
	db     *db.DB
	box    *secrets.Box
	log    *slog.Logger
	now    func() time.Time
	server func() (id, name string)
	client *http.Client
	// allow is checkDial; tests replace it to reach a test receiver on loopback.
	allow   func(address string) error
	ownPort atomic.Int32
	queue   chan pending
	wake    chan struct{}
}

// New makes the service. server names this server in every message.
func New(d *db.DB, box *secrets.Box, log *slog.Logger, now func() time.Time, server func() (id, name string)) *Service {
	s := &Service{db: d, box: box, log: log, now: now, server: server, queue: make(chan pending, 1000), wake: make(chan struct{}, 1)}
	s.allow = s.checkDial
	dialer := &net.Dialer{Timeout: 5 * time.Second, Control: s.control}
	s.client = &http.Client{
		Timeout: attemptTimeout,
		// A redirect is never followed: it would take the message somewhere the owner did
		// not type. It counts as not arrived, and the log says where it pointed.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Transport: &http.Transport{
			Proxy:                  nil,
			DialContext:            dialer.DialContext,
			TLSHandshakeTimeout:    5 * time.Second,
			ResponseHeaderTimeout:  attemptTimeout,
			MaxResponseHeaderBytes: 16 << 10,
			DisableKeepAlives:      true,
			TLSClientConfig:        &tls.Config{MinVersion: tls.VersionTLS12},
		},
	}
	return s
}

// ListensOn tells the sender which port Portico itself serves on, so no webhook can call it.
func (s *Service) ListensOn(port int) { s.ownPort.Store(int32(port)) }

// NewSecret makes a webhook secret and seals it to the webhook.
func (s *Service) NewSecret(webhookID string) (plain string, sealed []byte) {
	raw := make([]byte, 32)
	rand.Read(raw)
	plain = "whsec_" + hex.EncodeToString(raw)
	return plain, s.box.Seal("webhook:"+webhookID, []byte(plain))
}

// OpenSecret opens a webhook's sealed secret.
func (s *Service) OpenSecret(webhookID string, sealed []byte) ([]byte, error) {
	return s.box.Open("webhook:"+webhookID, sealed)
}

// Emit queues an event for the webhooks that want it. It never waits: the message is written
// in the background, in the order Emit was called. data is the event's facts; it must never
// carry a key, password or secret.
func (s *Service) Emit(event string, data any) {
	s.EmitWith(event, func(context.Context) (any, error) { return data, nil })
}

// EmitWith is Emit for facts that need looking up (names for ids). facts runs in the
// background, in order with every other message, so the caller never waits for a read.
func (s *Service) EmitWith(event string, facts func(context.Context) (any, error)) {
	if s == nil {
		return
	}
	select {
	case s.queue <- pending{event, s.now(), facts}:
	default:
		s.log.Warn("a webhook message was dropped: too many are being written at once", "event", event)
	}
}

type pending struct {
	event string
	at    time.Time
	facts func(context.Context) (any, error)
}

func (s *Service) message(event string, now time.Time, data any) (db.WebhookMessage, error) {
	id, name := s.server()
	body, err := json.Marshal(struct {
		ID     string    `json:"id"`
		Event  string    `json:"event"`
		At     time.Time `json:"at"`
		Server struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"server"`
		Data any `json:"data"`
	}{ID: db.NewID("evt"), Event: event, At: now.UTC().Truncate(time.Millisecond), Server: struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}{id, name}, Data: data})
	if err == nil && len(body) > maxPayload {
		err = errors.New("the message is too large")
	}
	return db.WebhookMessage{Event: event, Payload: string(body), At: now}, err
}

// Run writes emitted messages and sends what is due, until ctx ends.
func (s *Service) Run(ctx context.Context) {
	go s.write(ctx)
	for {
		sent, err := s.sendDue(ctx)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			s.log.Error("sending webhooks failed", "error", err)
		}
		if sent && err == nil {
			continue // the next message of each webhook may be due at once
		}
		wait := time.Minute
		if next, ok, err := s.db.NextWebhookAttempt(ctx); err == nil && ok {
			wait = min(max(next.Sub(s.now()), 0), wait)
		}
		if err != nil {
			wait = max(wait, 5*time.Second)
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-s.wake:
		case <-timer.C:
		}
		timer.Stop()
	}
}

// write turns emitted events into outbox rows, a batch at a time.
func (s *Service) write(ctx context.Context) {
	for {
		var batch []db.WebhookMessage
		add := func(p pending) {
			data, err := p.facts(ctx)
			if err == nil {
				var m db.WebhookMessage
				if m, err = s.message(p.event, p.at, data); err == nil {
					batch = append(batch, m)
				}
			}
			if err != nil && ctx.Err() == nil {
				s.log.Error("a webhook message could not be made", "event", p.event, "error", err)
			}
		}
		select {
		case <-ctx.Done():
			return
		case p := <-s.queue:
			add(p)
		}
	more:
		for len(batch) < 200 {
			select {
			case p := <-s.queue:
				add(p)
			default:
				break more
			}
		}
		if len(batch) == 0 {
			continue
		}
		if err := s.db.QueueWebhookMessages(context.WithoutCancel(ctx), batch); err != nil {
			s.log.Error("webhook messages could not be queued", "error", err, "messages", len(batch))
			continue
		}
		select {
		case s.wake <- struct{}{}:
		default:
		}
	}
}

// sendDue tries the oldest waiting message of every webhook whose next try is due, a few
// webhooks at a time. It reports whether it tried any.
func (s *Service) sendDue(ctx context.Context) (bool, error) {
	now := s.now()
	if err := s.db.GiveUpWebhookMessages(ctx, now.Add(-giveUpAfter), now, "Not delivered within 8 hours."); err != nil {
		return false, err
	}
	due, err := s.db.DueWebhookMessages(ctx, now)
	if err != nil || len(due) == 0 {
		return false, err
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	var failed error
	slots := make(chan struct{}, sendingAtOnce)
	for _, m := range due {
		wg.Add(1)
		slots <- struct{}{}
		go func() {
			defer func() { <-slots; wg.Done() }()
			a := s.attempt(ctx, m.Webhook, m.URL, m.Secret, m.Event, m.Payload, m.Attempts+1)
			if !a.Delivered {
				a.Next = nextTry(m.Attempts+1, m.CreatedAt, a.Now)
			}
			if err := s.db.FinishWebhookAttempt(context.WithoutCancel(ctx), m, a); err != nil {
				mu.Lock()
				failed = err
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	return true, failed
}

// nextTry is when to try again after the given number of failed tries, or zero to give up.
func nextTry(tries int, created, now time.Time) time.Time {
	if tries > len(retryAfter) {
		return time.Time{}
	}
	next := now.Add(retryAfter[tries-1])
	if next.Sub(created) > giveUpAfter {
		return time.Time{}
	}
	return next
}

// Test sends a test message to a webhook now, whether it is on or off, records it in the
// webhook's log and says how it went.
func (s *Service) Test(ctx context.Context, w db.Webhook) (db.Attempt, error) {
	m, err := s.message(Test, s.now(), map[string]string{"webhook": w.PublicID, "message": "A test from Portico."})
	if err != nil {
		return db.Attempt{}, err
	}
	a := s.attempt(ctx, w.PublicID, w.URL, w.Secret, Test, m.Payload, 1)
	return a, s.db.RecordWebhookTest(context.WithoutCancel(ctx), w.ID, m.Payload, a)
}

// attempt makes one try at delivering a message. Nothing it reads from the answer is kept but
// the status.
func (s *Service) attempt(ctx context.Context, webhook, address string, sealed []byte, event, payload string, try int) db.Attempt {
	secret, err := s.OpenSecret(webhook, sealed)
	if err != nil {
		return db.Attempt{Error: "The secret cannot be opened on this server. Replace the secret.", Now: s.now()}
	}
	ctx, cancel := context.WithTimeout(ctx, attemptTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, address, bytes.NewReader([]byte(payload)))
	if err != nil {
		return db.Attempt{Error: "The address cannot be used.", Now: s.now()}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Portico-Webhooks")
	req.Header.Set(HeaderEvent, event)
	req.Header.Set(HeaderAttempt, strconv.Itoa(try))
	req.Header.Set(HeaderSignature, Sign(secret, s.now().Unix(), []byte(payload)))
	resp, err := s.client.Do(req)
	now := s.now()
	if err != nil {
		return db.Attempt{Error: describe(err), Now: now}
	}
	io.Copy(io.Discard, io.LimitReader(resp.Body, maxAnswer))
	resp.Body.Close()
	a := db.Attempt{Status: resp.StatusCode, Now: now}
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		a.Delivered = true
	case resp.StatusCode >= 300 && resp.StatusCode < 400:
		a.Error = "The address answered with a redirect, which webhooks do not follow. Use the address it points to."
		if to, err := url.Parse(resp.Header.Get("Location")); err == nil && to.Host != "" {
			a.Error = "The address redirects to " + to.Scheme + "://" + to.Host + ", which webhooks do not follow. Use that address."
		}
	default:
		a.Error = "The address answered " + strconv.Itoa(resp.StatusCode) + " " + http.StatusText(resp.StatusCode) + "."
	}
	return a
}

// describe says in a sentence why a try did not get an answer.
func describe(err error) string {
	var refused *dialRefused
	var dns *net.DNSError
	var certificate *tls.CertificateVerificationError
	switch {
	case errors.As(err, &refused):
		return "Refused: " + refused.Error() + "."
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, os.ErrDeadlineExceeded):
		return "No answer within 15 seconds."
	case errors.As(err, &dns):
		return "The address's name could not be found."
	case errors.As(err, &certificate):
		return "The address's certificate is not trusted."
	}
	var op *net.OpError
	if errors.As(err, &op) && op.Op == "dial" {
		return "Could not connect to the address."
	}
	return "The address did not answer properly."
}
