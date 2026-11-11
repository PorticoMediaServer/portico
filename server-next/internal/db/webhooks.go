package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"time"
)

// Webhook limits, fixed by the product: there are no settings for them.
const (
	// MaxWebhooks is how many webhooks the server holds.
	MaxWebhooks = 20
	// webhookWaiting is how many messages one webhook keeps waiting; newer ones are dropped
	// and counted, so a webhook that is down for long cannot fill the disk.
	webhookWaiting = 1000
	// webhookLog is how many finished deliveries each webhook keeps for its log.
	webhookLog = 200
)

// ErrWebhookLimit: the server already has MaxWebhooks webhooks.
var ErrWebhookLimit = errors.New("too many webhooks")

// Webhook is one of the owner's outgoing webhooks. Secret is sealed with the server key.
type Webhook struct {
	ID              int64
	PublicID        string
	Name            string
	URL             string
	Events          []string
	Secret          []byte
	Enabled         bool
	Dropped         int64
	Waiting         int64
	LastDeliveredAt time.Time
	// LastError is set while the webhook is in trouble: its last attempt did not arrive.
	LastError string
	CreatedAt time.Time
}

const webhookColumns = `w.id, w.public_id, w.name, w.url, w.events, w.secret, w.enabled, w.dropped,
	(SELECT count(*) FROM outbox o WHERE o.webhook_id = w.id AND o.state = 'waiting'),
	ifnull(w.last_delivered_at, 0), ifnull(w.last_error, ''), w.created_at`

func scanWebhook(row interface{ Scan(...any) error }) (Webhook, error) {
	var w Webhook
	var events string
	var delivered, created int64
	err := row.Scan(&w.ID, &w.PublicID, &w.Name, &w.URL, &events, &w.Secret, &w.Enabled, &w.Dropped, &w.Waiting, &delivered, &w.LastError, &created)
	if err != nil {
		return w, notFound(err)
	}
	json.Unmarshal([]byte(events), &w.Events)
	w.CreatedAt = time.UnixMilli(created)
	if delivered != 0 {
		w.LastDeliveredAt = time.UnixMilli(delivered)
	}
	return w, nil
}

// Webhooks lists every webhook, oldest first. There are at most MaxWebhooks.
func (d *DB) Webhooks(ctx context.Context) ([]Webhook, error) {
	rows, err := d.read.QueryContext(ctx, `SELECT `+webhookColumns+` FROM webhooks w ORDER BY w.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Webhook
	for rows.Next() {
		w, err := scanWebhook(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// WebhookByID returns one webhook, or ErrNotFound.
func (d *DB) WebhookByID(ctx context.Context, publicID string) (Webhook, error) {
	return scanWebhook(d.read.QueryRowContext(ctx, `SELECT `+webhookColumns+` FROM webhooks w WHERE w.public_id = ?`, publicID))
}

// NewWebhook is a webhook to add. PublicID is chosen by the caller, because the secret is
// sealed to it before the row exists.
type NewWebhook struct {
	ActorID        int64
	PublicID       string
	Name           string
	URL            string
	Events         []string
	Secret         []byte
	IdempotencyKey string
	RequestHash    string
	Now            time.Time
}

// CreateWebhook adds a webhook, switched on. The same request repeated with its
// Idempotency-Key answers the webhook it made the first time.
func (d *DB) CreateWebhook(ctx context.Context, n NewWebhook) (Webhook, error) {
	var out Webhook
	err := d.write(ctx, User, func(tx *sql.Tx) error {
		ref, err := createdBefore(tx, n.ActorID, n.IdempotencyKey, n.RequestHash)
		if err != nil {
			return err
		}
		if ref != "" {
			out, err = scanWebhook(tx.QueryRow(`SELECT `+webhookColumns+` FROM webhooks w WHERE w.public_id = ?`, ref))
			return err
		}
		var count int
		if err := tx.QueryRow(`SELECT count(*) FROM webhooks`).Scan(&count); err != nil {
			return err
		}
		if count >= MaxWebhooks {
			return ErrWebhookLimit
		}
		events, _ := json.Marshal(n.Events)
		at := n.Now.UnixMilli()
		if _, err := tx.Exec(`INSERT INTO webhooks (public_id, name, url, events, secret, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			n.PublicID, n.Name, n.URL, string(events), n.Secret, at, at); err != nil {
			return err
		}
		if err := audit(tx, n.ActorID, "webhook.create", n.PublicID, n.URL, n.Now); err != nil {
			return err
		}
		if err := rememberCreated(tx, n.ActorID, n.IdempotencyKey, n.RequestHash, n.PublicID, n.Now); err != nil {
			return err
		}
		out, err = scanWebhook(tx.QueryRow(`SELECT `+webhookColumns+` FROM webhooks w WHERE w.public_id = ?`, n.PublicID))
		return err
	})
	return out, err
}

// WebhookEdit changes a webhook. A nil field is left as it is.
type WebhookEdit struct {
	ActorID  int64
	PublicID string
	Name     *string
	URL      *string
	Events   []string
	Enabled  *bool
	// Secret, when set, replaces the sealed secret.
	Secret []byte
	Now    time.Time
}

// UpdateWebhook changes a webhook. Turning it off drops the messages it has waiting; they
// would be stale by the time it is turned on again.
func (d *DB) UpdateWebhook(ctx context.Context, e WebhookEdit) (Webhook, error) {
	var out Webhook
	err := d.write(ctx, User, func(tx *sql.Tx) error {
		var id int64
		if err := tx.QueryRow(`SELECT id FROM webhooks WHERE public_id = ?`, e.PublicID).Scan(&id); err != nil {
			return notFound(err)
		}
		set := func(column string, value any) error {
			_, err := tx.Exec(`UPDATE webhooks SET `+column+` = ?, revision = revision + 1, updated_at = ? WHERE id = ?`, value, e.Now.UnixMilli(), id)
			return err
		}
		var err error
		if e.Name != nil {
			err = errors.Join(err, set("name", *e.Name))
		}
		if e.URL != nil {
			err = errors.Join(err, set("url", *e.URL))
		}
		if e.Events != nil {
			events, _ := json.Marshal(e.Events)
			err = errors.Join(err, set("events", string(events)))
		}
		if e.Secret != nil {
			err = errors.Join(err, set("secret", e.Secret))
		}
		if e.Enabled != nil {
			err = errors.Join(err, set("enabled", *e.Enabled))
			if !*e.Enabled {
				_, dropErr := tx.Exec(`DELETE FROM outbox WHERE webhook_id = ? AND state = 'waiting'`, id)
				err = errors.Join(err, dropErr, set("last_error", nil))
			}
		}
		if err != nil {
			return err
		}
		if err := audit(tx, e.ActorID, "webhook.change", e.PublicID, "", e.Now); err != nil {
			return err
		}
		out, err = scanWebhook(tx.QueryRow(`SELECT `+webhookColumns+` FROM webhooks w WHERE w.id = ?`, id))
		return err
	})
	return out, err
}

// DeleteWebhook removes a webhook with its waiting messages and its log. Deleting one that
// is already gone changes nothing.
func (d *DB) DeleteWebhook(ctx context.Context, actorID int64, publicID string, now time.Time) error {
	return d.write(ctx, User, func(tx *sql.Tx) error {
		res, err := tx.Exec(`DELETE FROM webhooks WHERE public_id = ?`, publicID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil
		}
		return audit(tx, actorID, "webhook.delete", publicID, "", now)
	})
}

// WebhookMessage is one event for the webhooks: queued for each switched-on webhook that
// wants it.
type WebhookMessage struct {
	Event   string
	Payload string
	At      time.Time
}

// QueueWebhookMessages queues messages for every switched-on webhook that wants them, in
// the order given. A webhook that already has its fill waiting drops the message and
// counts it.
func (d *DB) QueueWebhookMessages(ctx context.Context, messages []WebhookMessage) error {
	return d.write(ctx, Background, func(tx *sql.Tx) error {
		rows, err := tx.Query(`SELECT w.id, w.events, (SELECT count(*) FROM outbox o WHERE o.webhook_id = w.id AND o.state = 'waiting')
			FROM webhooks w WHERE w.enabled = 1`)
		if err != nil {
			return err
		}
		type target struct {
			id      int64
			events  []string
			waiting int
		}
		var targets []*target
		for rows.Next() {
			var t target
			var events string
			if err := rows.Scan(&t.id, &events, &t.waiting); err != nil {
				rows.Close()
				return err
			}
			json.Unmarshal([]byte(events), &t.events)
			targets = append(targets, &t)
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return err
		}
		for _, m := range messages {
			for _, t := range targets {
				if !slices.Contains(t.events, m.Event) {
					continue
				}
				if t.waiting >= webhookWaiting {
					if _, err := tx.Exec(`UPDATE webhooks SET dropped = dropped + 1 WHERE id = ?`, t.id); err != nil {
						return err
					}
					continue
				}
				if _, err := tx.Exec(`INSERT INTO outbox (webhook_id, event, payload, created_at) VALUES (?, ?, ?, ?)`,
					t.id, m.Event, m.Payload, m.At.UnixMilli()); err != nil {
					return err
				}
				t.waiting++
			}
		}
		return nil
	})
}

// OutMessage is a waiting message the sender is to try now.
type OutMessage struct {
	ID        int64
	WebhookID int64
	// Webhook is the webhook's public id, which its secret is sealed to.
	Webhook   string
	URL       string
	Secret    []byte
	Event     string
	Payload   string
	Attempts  int
	CreatedAt time.Time
}

// heads are the oldest waiting message of each switched-on webhook: messages to one webhook
// go in order, so only its oldest may be tried.
const heads = `FROM webhooks w JOIN outbox o ON o.id = (SELECT min(id) FROM outbox WHERE webhook_id = w.id AND state = 'waiting')
	WHERE w.enabled = 1`

// DueWebhookMessages returns the messages to try now: the oldest waiting one of each
// switched-on webhook, when its next attempt is due.
func (d *DB) DueWebhookMessages(ctx context.Context, now time.Time) ([]OutMessage, error) {
	rows, err := d.read.QueryContext(ctx, `SELECT o.id, w.id, w.public_id, w.url, w.secret, o.event, o.payload, o.attempts, o.created_at
		`+heads+` AND ifnull(o.next_attempt_at, 0) <= ?`, now.UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OutMessage
	for rows.Next() {
		var m OutMessage
		var created int64
		if err := rows.Scan(&m.ID, &m.WebhookID, &m.Webhook, &m.URL, &m.Secret, &m.Event, &m.Payload, &m.Attempts, &created); err != nil {
			return nil, err
		}
		m.CreatedAt = time.UnixMilli(created)
		out = append(out, m)
	}
	return out, rows.Err()
}

// NextWebhookAttempt is when the next waiting message is due, and false when none waits.
func (d *DB) NextWebhookAttempt(ctx context.Context) (time.Time, bool, error) {
	var next sql.NullInt64
	err := d.read.QueryRowContext(ctx, `SELECT min(ifnull(o.next_attempt_at, 0)) `+heads).Scan(&next)
	if err != nil || !next.Valid {
		return time.Time{}, false, err
	}
	return time.UnixMilli(next.Int64), true, nil
}

// Attempt is the result of one try at delivering a message.
type Attempt struct {
	Delivered bool
	// Status is the HTTP status the address answered, 0 when it did not answer.
	Status int
	Error  string
	// Next is when to try again; zero gives the message up.
	Next time.Time
	Now  time.Time
}

// FinishWebhookAttempt records one try at a message. A message that has meanwhile been
// dropped (its webhook turned off or deleted) is left alone.
func (d *DB) FinishWebhookAttempt(ctx context.Context, m OutMessage, a Attempt) error {
	return d.write(ctx, Background, func(tx *sql.Tx) error {
		at := a.Now.UnixMilli()
		var status, next, finished, lastError any
		if a.Status != 0 {
			status = a.Status
		}
		if a.Error != "" {
			lastError = a.Error
		}
		state := "waiting"
		switch {
		case a.Delivered:
			state, finished = "delivered", at
		case a.Next.IsZero():
			state, finished = "gave_up", at
		default:
			next = a.Next.UnixMilli()
		}
		res, err := tx.Exec(`UPDATE outbox SET state = ?, attempts = attempts + 1, next_attempt_at = ?, last_status = ?, last_error = ?, finished_at = ?
			WHERE id = ? AND state = 'waiting'`, state, next, status, lastError, finished, m.ID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil
		}
		if a.Delivered {
			_, err = tx.Exec(`UPDATE webhooks SET last_delivered_at = ?, last_error = NULL WHERE id = ?`, at, m.WebhookID)
		} else {
			_, err = tx.Exec(`UPDATE webhooks SET last_error = ? WHERE id = ?`, a.Error, m.WebhookID)
		}
		if err != nil || state == "waiting" {
			return err
		}
		return trimWebhookLog(tx, m.WebhookID)
	})
}

// GiveUpWebhookMessages gives up every waiting message made before the cutoff, without
// another try: a webhook that has been down for hours does not then spend a try on each.
func (d *DB) GiveUpWebhookMessages(ctx context.Context, before, now time.Time, reason string) error {
	return d.write(ctx, Background, func(tx *sql.Tx) error {
		rows, err := tx.Query(`UPDATE outbox SET state = 'gave_up', finished_at = ?, last_error = ifnull(last_error, ?)
			WHERE state = 'waiting' AND created_at < ? RETURNING webhook_id`, now.UnixMilli(), reason, before.UnixMilli())
		if err != nil {
			return err
		}
		var hooks []int64
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			if !slices.Contains(hooks, id) {
				hooks = append(hooks, id)
			}
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return err
		}
		for _, id := range hooks {
			if err := trimWebhookLog(tx, id); err != nil {
				return err
			}
		}
		return nil
	})
}

// RecordWebhookTest puts a test message in the webhook's log, already finished.
func (d *DB) RecordWebhookTest(ctx context.Context, webhookID int64, payload string, a Attempt) error {
	return d.write(ctx, User, func(tx *sql.Tx) error {
		state := "gave_up"
		if a.Delivered {
			state = "delivered"
		}
		var status, lastError any
		if a.Status != 0 {
			status = a.Status
		}
		if a.Error != "" {
			lastError = a.Error
		}
		if _, err := tx.Exec(`INSERT INTO outbox (webhook_id, event, payload, state, attempts, last_status, last_error, created_at, finished_at)
			VALUES (?, 'webhook.test', ?, ?, 1, ?, ?, ?, ?)`, webhookID, payload, state, status, lastError, a.Now.UnixMilli(), a.Now.UnixMilli()); err != nil {
			return err
		}
		return trimWebhookLog(tx, webhookID)
	})
}

// trimWebhookLog keeps a webhook's newest finished deliveries and drops older ones.
func trimWebhookLog(tx *sql.Tx, webhookID int64) error {
	_, err := tx.Exec(`DELETE FROM outbox WHERE webhook_id = ? AND state != 'waiting' AND id < ifnull((SELECT id FROM outbox
		WHERE webhook_id = ? AND state != 'waiting' ORDER BY id DESC LIMIT 1 OFFSET ?), 0)`, webhookID, webhookID, webhookLog-1)
	return err
}

// Delivery is one line of a webhook's log.
type Delivery struct {
	Event      string
	State      string
	Attempts   int
	Status     int
	Error      string
	CreatedAt  time.Time
	FinishedAt time.Time
	NextAt     time.Time
}

// WebhookDeliveries is a webhook's log, newest first: the messages still waiting and the
// last webhookLog finished.
func (d *DB) WebhookDeliveries(ctx context.Context, publicID string) ([]Delivery, error) {
	rows, err := d.read.QueryContext(ctx, `SELECT o.event, o.state, o.attempts, ifnull(o.last_status, 0), ifnull(o.last_error, ''),
		o.created_at, ifnull(o.finished_at, 0), ifnull(o.next_attempt_at, 0)
		FROM webhooks w JOIN outbox o ON o.webhook_id = w.id WHERE w.public_id = ? ORDER BY o.id DESC LIMIT ?`, publicID, webhookLog+webhookWaiting)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Delivery
	for rows.Next() {
		var l Delivery
		var created, finished, next int64
		if err := rows.Scan(&l.Event, &l.State, &l.Attempts, &l.Status, &l.Error, &created, &finished, &next); err != nil {
			return nil, err
		}
		l.CreatedAt = time.UnixMilli(created)
		if finished != 0 {
			l.FinishedAt = time.UnixMilli(finished)
		}
		if next != 0 {
			l.NextAt = time.UnixMilli(next)
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// AccountNames are the names a webhook message carries for a person and a profile.
func (d *DB) AccountNames(ctx context.Context, userID, profileID int64) (userPublicID, username, profilePublicID, profileName string, err error) {
	err = d.read.QueryRowContext(ctx, `SELECT u.public_id, u.username, ifnull(p.public_id, ''), ifnull(p.name, '')
		FROM users u LEFT JOIN profiles p ON p.id = ? AND p.user_id = u.id WHERE u.id = ?`, profileID, userID).
		Scan(&userPublicID, &username, &profilePublicID, &profileName)
	return userPublicID, username, profilePublicID, profileName, notFound(err)
}
