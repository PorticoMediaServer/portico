package api

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/auth"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/playback"
	"github.com/PorticoMediaServer/portico/server-next/internal/webhooks"
)

// Webhook is one of the owner's outgoing webhooks. Its secret is shown only when it is made
// or replaced.
type Webhook struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	URL     string   `json:"url"`
	Events  []string `json:"events"`
	Enabled bool     `json:"enabled"`
	// Trouble is set while the webhook is in trouble: why its last try did not arrive.
	Trouble string `json:"trouble,omitempty"`
	// Waiting is how many messages wait to be delivered; Dropped how many were left out
	// because 1,000 were already waiting.
	Waiting         int64      `json:"waiting"`
	Dropped         int64      `json:"dropped"`
	LastDeliveredAt *time.Time `json:"lastDeliveredAt,omitempty"`
	CreatedAt       time.Time  `json:"createdAt"`
}

// WebhookCreate adds a webhook. The address is http or https, may be on the home network,
// and carries no username or password.
type WebhookCreate struct {
	Name   string   `json:"name"`
	URL    string   `json:"url"`
	Events []string `json:"events"`
}

// WebhookEdit changes a webhook; a field left out stays as it is. Turning a webhook off
// drops the messages it has waiting.
type WebhookEdit struct {
	Name    *string  `json:"name,omitempty"`
	URL     *string  `json:"url,omitempty"`
	Events  []string `json:"events,omitempty"`
	Enabled *bool    `json:"enabled,omitempty"`
}

// WebhookSecret is a webhook with its signing secret, shown in this answer only.
type WebhookSecret struct {
	Webhook Webhook `json:"webhook"`
	Secret  string  `json:"secret"`
}

// WebhookTest is how a test message went.
type WebhookTest struct {
	Delivered bool `json:"delivered"`
	// Status is what the address answered, absent when it did not answer.
	Status int    `json:"status,omitempty"`
	Error  string `json:"error,omitempty"`
}

// WebhookDelivery is one line of a webhook's log.
type WebhookDelivery struct {
	Event string `json:"event"`
	// State is waiting, delivered or gave_up.
	State      string     `json:"state"`
	Tries      int        `json:"tries"`
	Status     int        `json:"status,omitempty"`
	Error      string     `json:"error,omitempty"`
	CreatedAt  time.Time  `json:"createdAt"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
	NextTryAt  *time.Time `json:"nextTryAt,omitempty"`
}

func (s *Server) webhookRoutes() {
	Handle(s, "GET /v1/admin/webhooks", Owner, s.listWebhooks)
	Handle(s, "POST /v1/admin/webhooks", Owner, s.createWebhook)
	Handle(s, "PATCH /v1/admin/webhooks/{id}", Owner, s.editWebhook)
	Handle(s, "DELETE /v1/admin/webhooks/{id}", Owner, func(r *http.Request, _ None) (None, error) {
		return None{}, s.db.DeleteWebhook(r.Context(), CallerFrom(r.Context()).UserID, r.PathValue("id"), s.now())
	})
	Handle(s, "POST /v1/admin/webhooks/{id}/test", Owner, s.testWebhook)
	Handle(s, "POST /v1/admin/webhooks/{id}/secret", Owner, s.replaceWebhookSecret)
	Handle(s, "GET /v1/admin/webhooks/{id}/deliveries", Owner, s.webhookDeliveries)
}

func webhookOut(w db.Webhook) Webhook {
	out := Webhook{ID: w.PublicID, Name: w.Name, URL: w.URL, Events: w.Events, Enabled: w.Enabled, Trouble: w.LastError,
		Waiting: w.Waiting, Dropped: w.Dropped, CreatedAt: w.CreatedAt.UTC()}
	if out.Events == nil {
		out.Events = []string{}
	}
	if !w.LastDeliveredAt.IsZero() {
		at := w.LastDeliveredAt.UTC()
		out.LastDeliveredAt = &at
	}
	return out
}

func (s *Server) listWebhooks(r *http.Request, _ None) (Page[Webhook], error) {
	hooks, err := s.db.Webhooks(r.Context())
	out := Page[Webhook]{Items: make([]Webhook, 0, len(hooks))}
	for _, w := range hooks {
		out.Items = append(out.Items, webhookOut(w))
	}
	return out, err
}

func checkWebhookEvents(events []string) error {
	if len(events) == 0 {
		return ErrField("events", "Choose at least one event.")
	}
	for i, e := range events {
		if !slices.Contains(webhooks.Events, e) || slices.Contains(events[:i], e) {
			return ErrField("events", "Choose each event at most once, from the list the server offers.")
		}
	}
	return nil
}

func checkWebhookURL(raw string) error {
	if why := webhooks.ValidURL(raw); why != "" {
		return ErrField("url", why)
	}
	return nil
}

func (s *Server) createWebhook(r *http.Request, req WebhookCreate) (WebhookSecret, error) {
	if !auth.ValidName(req.Name) {
		return WebhookSecret{}, ErrField("name", "Give the webhook a name of at most 120 characters.")
	}
	if err := errors.Join(checkWebhookURL(req.URL), checkWebhookEvents(req.Events)); err != nil {
		return WebhookSecret{}, firstError(err)
	}
	key, err := idempotencyKey(r)
	if err != nil {
		return WebhookSecret{}, err
	}
	id := db.NewID("whk")
	_, sealed := s.webhooks.NewSecret(id)
	w, err := s.db.CreateWebhook(r.Context(), db.NewWebhook{ActorID: CallerFrom(r.Context()).UserID, PublicID: id, Name: req.Name, URL: req.URL,
		Events: req.Events, Secret: sealed, IdempotencyKey: key, RequestHash: requestHash(req), Now: s.now()})
	switch {
	case errors.Is(err, db.ErrWebhookLimit):
		return WebhookSecret{}, ErrConflict("webhook_limit", "There are 20 webhooks already. Delete one first.")
	case errors.Is(err, db.ErrKeyReused):
		return WebhookSecret{}, errKeyReused()
	case err != nil:
		return WebhookSecret{}, err
	}
	// A repeated request answers the webhook it made, with the same secret: the reply to the
	// first may have been lost, and the server can still open it.
	secret, err := s.webhooks.OpenSecret(w.PublicID, w.Secret)
	return WebhookSecret{Webhook: webhookOut(w), Secret: string(secret)}, err
}

// firstError is the first refusal of several checks joined together.
func firstError(err error) error {
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		return joined.Unwrap()[0]
	}
	return err
}

func (s *Server) editWebhook(r *http.Request, req WebhookEdit) (Webhook, error) {
	if req.Name != nil && !auth.ValidName(*req.Name) {
		return Webhook{}, ErrField("name", "Give the webhook a name of at most 120 characters.")
	}
	if req.URL != nil {
		if err := checkWebhookURL(*req.URL); err != nil {
			return Webhook{}, err
		}
	}
	if req.Events != nil {
		if err := checkWebhookEvents(req.Events); err != nil {
			return Webhook{}, err
		}
	}
	w, err := s.db.UpdateWebhook(r.Context(), db.WebhookEdit{ActorID: CallerFrom(r.Context()).UserID, PublicID: r.PathValue("id"),
		Name: req.Name, URL: req.URL, Events: req.Events, Enabled: req.Enabled, Now: s.now()})
	if errors.Is(err, db.ErrNotFound) {
		return Webhook{}, ErrNotFound()
	}
	return webhookOut(w), err
}

func (s *Server) replaceWebhookSecret(r *http.Request, _ None) (WebhookSecret, error) {
	id := r.PathValue("id")
	plain, sealed := s.webhooks.NewSecret(id)
	w, err := s.db.UpdateWebhook(r.Context(), db.WebhookEdit{ActorID: CallerFrom(r.Context()).UserID, PublicID: id, Secret: sealed, Now: s.now()})
	if errors.Is(err, db.ErrNotFound) {
		return WebhookSecret{}, ErrNotFound()
	}
	return WebhookSecret{Webhook: webhookOut(w), Secret: plain}, err
}

func (s *Server) testWebhook(r *http.Request, _ None) (WebhookTest, error) {
	w, err := s.db.WebhookByID(r.Context(), r.PathValue("id"))
	if errors.Is(err, db.ErrNotFound) {
		return WebhookTest{}, ErrNotFound()
	}
	if err != nil {
		return WebhookTest{}, err
	}
	a, err := s.webhooks.Test(r.Context(), w)
	return WebhookTest{Delivered: a.Delivered, Status: a.Status, Error: a.Error}, err
}

func (s *Server) webhookDeliveries(r *http.Request, _ None) (Page[WebhookDelivery], error) {
	if _, err := s.db.WebhookByID(r.Context(), r.PathValue("id")); errors.Is(err, db.ErrNotFound) {
		return Page[WebhookDelivery]{}, ErrNotFound()
	} else if err != nil {
		return Page[WebhookDelivery]{}, err
	}
	log, err := s.db.WebhookDeliveries(r.Context(), r.PathValue("id"))
	out := Page[WebhookDelivery]{Items: make([]WebhookDelivery, 0, len(log))}
	for _, l := range log {
		d := WebhookDelivery{Event: l.Event, State: l.State, Tries: l.Attempts, Status: l.Status, Error: l.Error, CreatedAt: l.CreatedAt.UTC()}
		if !l.FinishedAt.IsZero() {
			at := l.FinishedAt.UTC()
			d.FinishedAt = &at
		}
		if !l.NextAt.IsZero() && l.State == "waiting" {
			at := l.NextAt.UTC()
			d.NextTryAt = &at
		}
		out.Items = append(out.Items, d)
	}
	return out, err
}

// Webhooks is the webhook sender, for the server's start-up to tell it Portico's own port.
func (s *Server) Webhooks() *webhooks.Service { return s.webhooks }

// startWebhooks starts the sender, announces the start, and sends playback events. Playback
// events go for everyone, a person who paused their history included: the owner sees every
// play anyway.
func (s *Server) startWebhooks(ctx context.Context) {
	go s.webhooks.Run(ctx)
	s.webhooks.Emit(webhooks.ServerStarted, map[string]string{"version": s.version})
	s.playback.Notify(func(event string, p playback.Session) {
		name := map[string]string{playback.StatePlaying: webhooks.PlaybackStarted, playback.StatePaused: webhooks.PlaybackPaused,
			"resumed": webhooks.PlaybackResumed, playback.StateEnded: webhooks.PlaybackStopped}[event]
		if name == "" {
			return
		}
		s.webhooks.EmitWith(name, func(ctx context.Context) (any, error) { return s.playbackFacts(ctx, p) })
	})
}

// ref is a named thing in a webhook message.
type ref struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// playbackFacts is what a playback message says: the title, who is watching and where.
func (s *Server) playbackFacts(ctx context.Context, p playback.Session) (any, error) {
	type message struct {
		Title      ref    `json:"title"`
		Kind       string `json:"kind"`
		Show       *ref   `json:"show,omitempty"`
		Person     ref    `json:"person"`
		Profile    ref    `json:"profile"`
		Device     ref    `json:"device"`
		PositionMs int64  `json:"positionMs"`
	}
	m := message{Title: ref{p.Item.PublicID, p.Item.Title}, Kind: p.Item.Kind.String(), Device: ref{p.Device.PublicID, p.Device.Name}, PositionMs: p.PositionMs}
	if p.Item.Root != nil {
		m.Show = &ref{p.Item.Root.PublicID, p.Item.Root.Title}
	}
	var err error
	m.Person.ID, m.Person.Name, m.Profile.ID, m.Profile.Name, err = s.db.AccountNames(ctx, p.UserID, p.ProfileID)
	return m, err
}
