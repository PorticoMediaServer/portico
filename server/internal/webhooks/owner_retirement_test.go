package webhooks

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

func cachedTest(t *testing.T, s *Store, id string) queued {
	t.Helper()
	var q queued
	if err := s.DB.QueryRow(`SELECT d.id,d.webhook_id,d.type,d.data,d.attempts,d.created_ms,w.url,w.secret,d.authority_generation FROM webhook_deliveries d JOIN webhooks w ON w.id=d.webhook_id WHERE d.id=?`, id).Scan(&q.id, &q.webhook, &q.event, &q.data, &q.attempts, &q.created, &q.url, &q.secret, &q.generation); err != nil {
		t.Fatal(err)
	}
	return q
}

func TestOwnerTransferRetiresTestsOnlyOnCommitAndRequiresFreshApproval(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, db := open(t)
	ctx := context.Background()
	r := newReceiver(t)
	oldSecret := "old-owner-signing-secret"
	in := Create{Name: "Automation", URL: r.server.URL, Events: []string{ServerStarted}, Secret: oldSecret}
	issued, err := s.Create(ctx, nil, in)
	if err != nil {
		t.Fatal(err)
	}
	job, err := s.SendTest(ctx, nil, issued.Webhook.ID)
	if err != nil {
		t.Fatal(err)
	}
	q := cachedTest(t, s, job.ID)
	life, release, err := s.admitSend(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	refused := errors.New("ownership transaction refused")
	if err = dbwork.WithWriteTx(ctx, db, dbwork.ClassSecurityFence, func(tx *sql.Tx) error {
		if e := s.RetireOwnerTx(ctx, tx); e != nil {
			return e
		}
		return refused
	}); !errors.Is(err, refused) {
		t.Fatal(err)
	}
	if life.Err() != nil {
		t.Fatal("rolled-back transfer cancelled current-owner work")
	}
	if err = dbwork.WithWriteTx(ctx, db, dbwork.ClassSecurityFence, func(tx *sql.Tx) error { return s.RetireOwnerTx(ctx, tx) }); err != nil {
		t.Fatal(err)
	}
	if life.Err() == nil {
		t.Fatal("committed transfer left old sending admission alive")
	}
	w, err := s.Get(ctx, nil, issued.Webhook.ID)
	if err != nil || w.Enabled || !w.RequiresOwnerReview {
		t.Fatalf("retired integration: %+v %v", w, err)
	}
	if _, _, err = s.admitSend(ctx, q); !errors.Is(err, errRetired) {
		t.Fatal("cached test was admitted", err)
	}
	if _, err = s.SendTest(ctx, nil, w.ID); err == nil {
		t.Fatal("test bypassed owner review")
	}
	enabled := true
	if _, err = s.Update(ctx, nil, w.ID, Change{ExpectedRevision: w.Revision, Enabled: &enabled}); err == nil {
		t.Fatal("bare enable inherited authority")
	}
	// Review and rotation are existing separate controls. Neither alone enables.
	review, err := s.Update(ctx, nil, w.ID, Change{ExpectedRevision: w.Revision, URL: &in.URL, Events: &in.Events})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Update(ctx, nil, w.ID, Change{ExpectedRevision: review.Webhook.Revision, Secret: oldSecret}); err == nil {
		t.Fatal("old secret reused")
	}
	rotated, err := s.Update(ctx, nil, w.ID, Change{ExpectedRevision: review.Webhook.Revision, RotateSecret: true})
	if err != nil || rotated.Secret == "" || rotated.Webhook.Enabled {
		t.Fatal("rotation did not stay disabled", err)
	}
	active, err := s.Update(ctx, nil, w.ID, Change{ExpectedRevision: rotated.Webhook.Revision, Enabled: &enabled})
	if err != nil || !active.Webhook.Enabled || active.Webhook.RequiresOwnerReview {
		t.Fatal("fresh approval refused", err)
	}
	if _, err = s.Update(ctx, nil, w.ID, Change{ExpectedRevision: active.Webhook.Revision, Secret: oldSecret}); err == nil {
		t.Fatal("retired secret restored after enable")
	}
	d := NewDispatcher(s)
	d.Step(ctx)
	if r.count() != 0 {
		t.Fatal("old backlog replayed after re-enable")
	}
	if _, err = s.SendTest(ctx, nil, w.ID); err != nil {
		t.Fatal(err)
	}
	d.Step(ctx)
	if r.count() != 1 {
		t.Fatal("fresh test was not sent exactly once")
	}
	var state, reason string
	if err = db.QueryRow(`SELECT state,error FROM webhook_deliveries WHERE id=?`, job.ID).Scan(&state, &reason); err != nil || state != "failed" || reason != "owner_changed" {
		t.Fatal(state, reason, err)
	}
}

type retirementTransport func(*http.Request) (*http.Response, error)

func (f retirementTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestRetiredInflightResponseCannotSettleNewGeneration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, db := open(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	w, err := s.Create(ctx, nil, Create{Name: "Automation", URL: "https://example.test/hook", Events: []string{ServerStarted}})
	if err != nil {
		t.Fatal(err)
	}
	job, err := s.SendTest(ctx, nil, w.Webhook.ID)
	if err != nil {
		t.Fatal(err)
	}
	q := cachedTest(t, s, job.ID)
	started := make(chan *http.Request, 1)
	finish := make(chan struct{})
	d := NewDispatcher(s)
	d.Client = &http.Client{Transport: retirementTransport(func(r *http.Request) (*http.Response, error) {
		started <- r
		select {
		case <-finish:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return &http.Response{StatusCode: 204, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(""))}, nil
	})}
	done := make(chan bool, 1)
	go func() { done <- d.attempt(ctx, q) }()
	var request *http.Request
	select {
	case request = <-started:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if err = dbwork.WithWriteTx(ctx, db, dbwork.ClassSecurityFence, func(tx *sql.Tx) error { return s.RetireOwnerTx(ctx, tx) }); err != nil {
		t.Fatal(err)
	}
	if request.Context().Err() == nil {
		t.Fatal("inflight request not cancelled on commit")
	}
	close(finish)
	select {
	case accepted := <-done:
		if accepted {
			t.Fatal("late success accepted")
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	var state, reason string
	if err = db.QueryRow(`SELECT state,error FROM webhook_deliveries WHERE id=?`, job.ID).Scan(&state, &reason); err != nil || state != "failed" || reason != "owner_changed" {
		t.Fatal(state, reason, err)
	}
	current, err := s.Get(ctx, nil, w.Webhook.ID)
	if err != nil || current.Failing != 0 {
		t.Fatal("late result mutated retired state", err)
	}
}
