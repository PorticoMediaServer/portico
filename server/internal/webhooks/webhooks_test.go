package webhooks

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func open(t *testing.T) (*Store, *sql.DB) {
	t.Helper()
	db, err := persistence.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return New(db), db
}

type received struct {
	header http.Header
	body   []byte
}

type receiver struct {
	mu     sync.Mutex
	seen   []received
	status int
	server *httptest.Server
}

func newReceiver(t *testing.T) *receiver {
	r := &receiver{status: 204}
	r.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		body, _ := io.ReadAll(q.Body)
		r.mu.Lock()
		r.seen = append(r.seen, received{q.Header.Clone(), body})
		status := r.status
		r.mu.Unlock()
		if status >= 300 && status < 400 {
			w.Header().Set("Location", "http://127.0.0.1:1/elsewhere")
		}
		w.WriteHeader(status)
	}))
	t.Cleanup(r.server.Close)
	return r
}

func (r *receiver) count() int { r.mu.Lock(); defer r.mu.Unlock(); return len(r.seen) }
func (r *receiver) last() received {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.seen[len(r.seen)-1]
}
func (r *receiver) answer(status int) { r.mu.Lock(); r.status = status; r.mu.Unlock() }

// verify is the check a receiver is documented to make.
func verify(secret string, header http.Header, body []byte) bool {
	var t, v1 string
	for _, part := range strings.Split(header.Get(HeaderSignature), ",") {
		key, value, _ := strings.Cut(part, "=")
		switch key {
		case "t":
			t = value
		case "v1":
			v1 = value
		}
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(t + "." + string(body)))
	want := hex.EncodeToString(mac.Sum(nil))
	return t != "" && hmac.Equal([]byte(want), []byte(v1))
}

func TestWebhookValidationRefusesWhatItShould(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	store, _ := open(t)
	ctx := context.Background()
	good := Create{Name: "Home automation", URL: "http://192.168.1.20:8123/api/webhook/abc", Events: []string{PlaybackStarted}}
	for name, bad := range map[string]Create{
		"no name":        {URL: good.URL, Events: good.Events},
		"control chars":  {Name: "a\nb", URL: good.URL, Events: good.Events},
		"no url":         {Name: "x", Events: good.Events},
		"file url":       {Name: "x", URL: "file:///etc/passwd", Events: good.Events},
		"no host":        {Name: "x", URL: "http:///path", Events: good.Events},
		"credentials":    {Name: "x", URL: "https://user:secret@example.com/hook", Events: good.Events},
		"fragment":       {Name: "x", URL: "https://example.com/hook#x", Events: good.Events},
		"relative":       {Name: "x", URL: "/hook", Events: good.Events},
		"no events":      {Name: "x", URL: good.URL},
		"unknown event":  {Name: "x", URL: good.URL, Events: []string{"everything"}},
		"test event":     {Name: "x", URL: good.URL, Events: []string{Test}},
		"repeated event": {Name: "x", URL: good.URL, Events: []string{TitleAdded, TitleAdded}},
		"short secret":   {Name: "x", URL: good.URL, Events: good.Events, Secret: "short"},
		"spaced secret":  {Name: "x", URL: good.URL, Events: good.Events, Secret: "sixteen characters long"},
		"very long url":  {Name: "x", URL: "https://example.com/" + strings.Repeat("a", 3000), Events: good.Events},
	} {
		if _, err := store.Create(ctx, nil, bad); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%s accepted: %v", name, err)
		}
	}
	// A private address is the owner's choice and is accepted.
	made, err := store.Create(ctx, nil, good)
	if err != nil || !strings.HasPrefix(made.Secret, "whsec_") || !made.Webhook.Enabled {
		t.Fatalf("create: %v %+v", err, made.Webhook)
	}
	list, err := store.List(ctx, nil)
	if err != nil || len(list.Items) != 1 || len(list.Events) != len(Events) {
		t.Fatalf("list: %v %+v", err, list)
	}
	raw, _ := json.Marshal(list)
	if strings.Contains(string(raw), made.Secret) || list.Items[0].SecretHint != made.Secret[len(made.Secret)-4:] {
		t.Fatalf("the list carries the secret: %s", raw)
	}
	// Rotation shows a new secret once; a chosen secret is not echoed back.
	rotated, err := store.Update(ctx, nil, made.Webhook.ID, Change{ExpectedRevision: made.Webhook.Revision, RotateSecret: true})
	if err != nil || rotated.Secret == "" || rotated.Secret == made.Secret {
		t.Fatalf("rotate: %v", err)
	}
	chosen, err := store.Update(ctx, nil, made.Webhook.ID, Change{ExpectedRevision: rotated.Webhook.Revision, Secret: "a-secret-of-my-own-choosing"})
	if err != nil || chosen.Secret != "" || chosen.Webhook.SecretHint != "sing" {
		t.Fatalf("chosen secret: %v %+v", err, chosen)
	}
	if _, err = store.Update(ctx, nil, made.Webhook.ID, Change{Secret: "a-secret-of-my-own-choosing", RotateSecret: true}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("rotate and set together: %v", err)
	}
	if _, err = store.Update(ctx, nil, "missing", Change{}); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("update of a missing webhook: %v", err)
	}
	if err = store.Delete(ctx, nil, "missing"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("delete of a missing webhook: %v", err)
	}
	denied := errors.New("denied")
	if _, err = store.Create(ctx, func(context.Context, *sql.Tx) error { return denied }, good); !errors.Is(err, denied) {
		t.Fatalf("unauthorised create: %v", err)
	}
	for i := 1; i < MaxWebhooks; i++ {
		if _, err = store.Create(ctx, nil, good); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = store.Create(ctx, nil, good); !errors.Is(err, ErrCapacity) {
		t.Fatalf("more than the maximum: %v", err)
	}
}

func TestDeliveryIsSignedRetriedAndLogged(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	store, db := open(t)
	ctx := context.Background()
	target := newReceiver(t)
	made, err := store.Create(ctx, nil, Create{Name: "Receiver", URL: target.server.URL + "/hook?token=in-the-address", Events: []string{ServerStarted, BackupFailed}})
	if err != nil {
		t.Fatal(err)
	}
	dispatcher := NewDispatcher(store)
	dispatcher.Server = func() (string, string) { return "server-1", "Living room" }
	if err = store.Emit(ctx, ServerStarted, "", map[string]any{"version": "1.2.3"}); err != nil {
		t.Fatal(err)
	}
	// An event nobody subscribed to queues nothing; an unknown one is refused.
	if err = store.Emit(ctx, UpdateAvailable, "", nil); err != nil {
		t.Fatal(err)
	}
	if err = store.Emit(ctx, "made.up", "", nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown event: %v", err)
	}
	dispatcher.Step(ctx)
	if target.count() != 1 {
		t.Fatalf("deliveries: %d", target.count())
	}
	got := target.last()
	if !verify(made.Secret, got.header, got.body) || verify("another-secret-entirely", got.header, got.body) {
		t.Fatal("the signature does not verify with the secret, or verifies with another")
	}
	tampered := append([]byte{}, got.body...)
	tampered[len(tampered)-2] ^= 1
	if verify(made.Secret, got.header, tampered) {
		t.Fatal("a changed body still verifies")
	}
	var envelope struct {
		ID, Type, CreatedAt string
		Server              map[string]string
		Data                map[string]any
	}
	if err = json.Unmarshal(got.body, &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Type != ServerStarted || envelope.ID == "" || envelope.ID != got.header.Get(HeaderEventID) || got.header.Get(HeaderEvent) != ServerStarted || got.header.Get(HeaderAttempt) != "1" || envelope.Server["name"] != "Living room" || envelope.Data["version"] != "1.2.3" {
		t.Fatalf("envelope: %s %v", got.body, got.header)
	}
	if strings.Contains(string(got.body), made.Secret) || strings.Contains(string(got.body), "in-the-address") {
		t.Fatal("the payload carries the secret or the address")
	}
	// A failure is retried later with the same event id and a higher attempt.
	target.answer(500)
	if err = store.Emit(ctx, BackupFailed, "", map[string]any{"kind": "scheduled"}); err != nil {
		t.Fatal(err)
	}
	dispatcher.Step(ctx)
	failed := target.last()
	log, err := store.Deliveries(ctx, nil, made.Webhook.ID, "", 0)
	if err != nil || len(log.Items) != 2 || log.Items[0].State != "pending" || log.Items[0].Attempts != 1 || log.Items[0].Error != "status_500" || log.Items[0].Status != 500 || log.Items[0].NextAttempt == "" || log.Items[1].State != "delivered" {
		t.Fatalf("log after a failure: %v %+v", err, log.Items)
	}
	if strings.Contains(log.Items[0].Error, "127.0.0.1") {
		t.Fatal("the log carries the address")
	}
	// Not due yet, and the webhook is resting: nothing is sent.
	dispatcher.Step(ctx)
	if target.count() != 2 {
		t.Fatalf("a retry was sent before it was due: %d", target.count())
	}
	clock := time.Now().Add(31 * time.Second)
	store.Now = func() time.Time { return clock }
	target.answer(200)
	dispatcher.Step(ctx)
	retried := target.last()
	if target.count() != 3 || retried.header.Get(HeaderEventID) != failed.header.Get(HeaderEventID) || retried.header.Get(HeaderAttempt) != "2" || !verify(made.Secret, retried.header, retried.body) {
		t.Fatalf("retry: %d %v", target.count(), retried.header)
	}
	if string(retried.body) != string(failed.body) {
		t.Fatal("a retry changed the body")
	}
	// A redirect is never followed, and after the last delay the delivery is
	// given up and says so.
	target.answer(302)
	if err = store.Emit(ctx, ServerStarted, "", nil); err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt <= len(retryDelays); attempt++ {
		dispatcher.Step(ctx)
		clock = clock.Add(5 * time.Hour)
	}
	log, err = store.Deliveries(ctx, nil, made.Webhook.ID, "", 1)
	if err != nil || len(log.Items) != 1 || log.Items[0].State != "failed" || log.Items[0].Attempts != len(retryDelays)+1 || log.Items[0].Error != "redirect_not_followed" || log.NextCursor == "" {
		t.Fatalf("given up: %v %+v", err, log)
	}
	more, err := store.Deliveries(ctx, nil, made.Webhook.ID, log.NextCursor, 1)
	if err != nil || len(more.Items) != 1 || more.Items[0].ID == log.Items[0].ID {
		t.Fatalf("second page of the log: %v %+v", err, more)
	}
	if _, err = store.Deliveries(ctx, nil, made.Webhook.ID, "not-a-cursor", 1); !errors.Is(err, ErrInvalid) {
		t.Fatalf("forged cursor: %v", err)
	}
	if _, err = store.Deliveries(ctx, nil, made.Webhook.ID, "", 1000); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unbounded log page: %v", err)
	}
	list, _ := store.List(ctx, nil)
	if list.Items[0].Failing == 0 {
		t.Fatalf("a failing webhook is not reported as failing: %+v", list.Items[0])
	}
	// Send test reaches a webhook that is turned off and subscribed to nothing
	// of the kind, and turning one off abandons what was waiting.
	target.answer(200)
	off := false
	if _, err = store.Update(ctx, nil, made.Webhook.ID, Change{ExpectedRevision: made.Webhook.Revision, Enabled: &off}); err != nil {
		t.Fatal(err)
	}
	if err = store.Emit(ctx, ServerStarted, "", nil); err != nil {
		t.Fatal(err)
	}
	before := target.count()
	clock = clock.Add(time.Hour)
	dispatcher.Step(ctx)
	if target.count() != before {
		t.Fatal("a webhook that is turned off was sent an event")
	}
	test, err := store.SendTest(ctx, nil, made.Webhook.ID)
	if err != nil || test.Event != Test || test.State != "pending" {
		t.Fatalf("send test: %v %+v", err, test)
	}
	dispatcher.Step(ctx)
	if target.count() != before+1 || target.last().header.Get(HeaderEvent) != Test {
		t.Fatalf("the test was not sent: %d", target.count())
	}
	if _, err = store.SendTest(ctx, nil, "missing"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("test of a missing webhook: %v", err)
	}
	// Deleting the webhook removes its queue and log.
	if err = store.Delete(ctx, nil, made.Webhook.ID); err != nil {
		t.Fatal(err)
	}
	var left int
	if err = db.QueryRow(`SELECT count(*) FROM webhook_deliveries`).Scan(&left); err != nil || left != 0 {
		t.Fatalf("deliveries outlived their webhook: %d %v", left, err)
	}
}

func TestCatalogueChangesBecomeEventsOnlyForSubscribers(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	store, db := open(t)
	ctx := context.Background()
	c := catalogtest.New(t, db)
	library := c.Library("movies", "Movies", "movie", "/movies")
	// With no webhook nothing is queued and nothing lingers.
	c.Movie(library, "/movies/before.mkv", "Before", 1999)
	var rows int
	if err := db.QueryRow(`SELECT (SELECT count(*) FROM webhook_deliveries)+(SELECT count(*) FROM webhook_events)`).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("events without a webhook: %d %v", rows, err)
	}
	target := newReceiver(t)
	titles, err := store.Create(ctx, nil, Create{Name: "Titles", URL: target.server.URL, Events: []string{TitleAdded, TitleUpdated, TitleRemoved}})
	if err != nil {
		t.Fatal(err)
	}
	other, err := store.Create(ctx, nil, Create{Name: "Scans only", URL: target.server.URL, Events: []string{ScanStarted}})
	if err != nil {
		t.Fatal(err)
	}
	film := c.Movie(library, "/movies/film.mkv", "film.2001.1080p", 2001)
	// Several changes before the first attempt are one delivery, and an update
	// to a title whose "added" has not gone out yet is part of that "added".
	for _, title := range []string{"Film", "The Film"} {
		if _, err = db.Exec(`UPDATE catalog_entities SET title=? WHERE id=?`, title, film.ID); err != nil {
			t.Fatal(err)
		}
	}
	count := func(webhook, event string) int {
		t.Helper()
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM webhook_deliveries WHERE webhook_id=? AND (?='' OR type=?)`, webhook, event, event).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if count(titles.Webhook.ID, TitleAdded) != 1 || count(titles.Webhook.ID, TitleUpdated) != 0 || count(other.Webhook.ID, "") != 0 || count(titles.Webhook.ID, "") != 1 {
		t.Fatalf("queued: added=%d updated=%d other=%d", count(titles.Webhook.ID, TitleAdded), count(titles.Webhook.ID, TitleUpdated), count(other.Webhook.ID, ""))
	}
	if err = db.QueryRow(`SELECT count(*) FROM webhook_events`).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("the event table is not empty at rest: %d %v", rows, err)
	}
	// A new title waits a little so the match can settle, then goes out with
	// the title it has by then and a public id.
	dispatcher := NewDispatcher(store)
	dispatcher.Step(ctx)
	if target.count() != 0 {
		t.Fatal("a title event went out before its settling time")
	}
	clock := time.Now().Add(time.Minute)
	store.Now = func() time.Time { return clock }
	dispatcher.Step(ctx)
	if target.count() != 1 {
		t.Fatalf("title deliveries: %d", target.count())
	}
	var envelope struct {
		Type string
		Data map[string]any
	}
	if err = json.Unmarshal(target.last().body, &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Type != TitleAdded || envelope.Data["title"] != "The Film" || envelope.Data["itemId"] != film.Public || envelope.Data["kind"] != "movie" || envelope.Data["libraryId"] != "movies" || envelope.Data["entity"] != nil {
		t.Fatalf("title payload: %s", target.last().body)
	}
	// After it has gone out, a change is an update; removal is a removal.
	if _, err = db.Exec(`UPDATE catalog_entities SET year=2002 WHERE id=?`, film.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE catalog_entities SET retired=1 WHERE id=?`, film.ID); err != nil {
		t.Fatal(err)
	}
	if count(titles.Webhook.ID, TitleUpdated) != 1 || count(titles.Webhook.ID, TitleRemoved) != 1 {
		t.Fatalf("after the change: updated=%d removed=%d", count(titles.Webhook.ID, TitleUpdated), count(titles.Webhook.ID, TitleRemoved))
	}
	// A full queue takes no more and counts what it missed.
	if _, err = db.Exec(`WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i<?) INSERT INTO webhook_deliveries(id,webhook_id,type,next_attempt_ms,created_ms) SELECT 'fill-'||i,?,'title.added',0,0 FROM n`, MaxQueued, titles.Webhook.ID); err != nil {
		t.Fatal(err)
	}
	c.Movie(library, "/movies/overflow.mkv", "Overflow", 2003)
	list, err := store.List(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range list.Items {
		if item.ID == titles.Webhook.ID && (item.Dropped != 1 || item.Waiting != MaxQueued+2) {
			t.Fatalf("overflow: dropped=%d waiting=%d", item.Dropped, item.Waiting)
		}
	}
	_ = strconv.Itoa
}
