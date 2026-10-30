package httpapi

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/apievents"
	"github.com/PorticoMediaServer/portico/server/internal/eventfeed"
	"github.com/PorticoMediaServer/portico/server/internal/notify"
)

// eventStart is the cursor a client holds before the events a test produces:
// its first read, with no cursor, stands at the newest position.
func eventStart(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var page eventfeed.Page
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil || len(page.Events) != 0 || !strings.HasPrefix(page.NextAfter, "e1.") {
		t.Fatalf("first event read: %d %s", w.Code, w.Body.String())
	}
	return page.NextAfter
}

func TestEventsLongPollEnforcesProfileScope(t *testing.T) {
	d, _, owner, member := notificationFixture(t)
	d.Events = eventfeed.New(d.DB)
	mux := http.NewServeMux()
	d.foundationRoutes(mux)
	if got := notificationRequest(mux, "GET", "", "/v1/events?waitMs=0", ""); got.Code != 401 {
		t.Fatalf("anonymous: %d %s", got.Code, got.Body.String())
	}
	for _, bad := range []string{"bad%20cursor", "e1.a%2Fb", strings.Repeat("a", 129)} {
		if got := notificationRequest(mux, "GET", member.AccessToken, "/v1/events?after="+bad+"&waitMs=0", ""); got.Code != 400 || !strings.Contains(got.Body.String(), "invalid_cursor") {
			t.Fatalf("bad cursor %q: %d %s", bad, got.Code, got.Body.String())
		}
	}
	memberStart := eventStart(t, notificationRequest(mux, "GET", member.AccessToken, "/v1/events?waitMs=0", ""))
	ownerStart := eventStart(t, notificationRequest(mux, "GET", owner.AccessToken, "/v1/events?waitMs=0", ""))
	raiseNotice(t, d, notify.ProfileScope("local", "owner", "owner-profile"), "owner-only", "Owner only")
	raiseNotice(t, d, notify.ProfileScope("local", "member", "member-profile"), "member-only", "Member only")
	request := httptest.NewRequest("GET", "/v1/events?after="+memberStart+"&waitMs=0", nil)
	request.Header.Set("Authorization", "Bearer "+member.AccessToken)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, request)
	if w.Code != 200 {
		t.Fatalf("member: %d %s", w.Code, w.Body.String())
	}
	var page eventfeed.Page
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil || len(page.Events) != 1 || page.Events[0].Type != "notification.changed" || page.NextAfter != page.Events[0].ID {
		t.Fatalf("member event scope: %+v %v", page, err)
	}
	// A member's cursor is the member's: the owner presenting it is resynced,
	// not shown the member's history.
	w = notificationRequest(mux, "GET", owner.AccessToken, "/v1/events?after="+memberStart+"&waitMs=0", "")
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil || len(page.Events) != 1 || page.Events[0].Type != "stream.resync" {
		t.Fatalf("a borrowed cursor must resync: %+v %v", page, err)
	}
	w = notificationRequest(mux, "GET", owner.AccessToken, "/v1/events?after="+ownerStart+"&waitMs=0", "")
	if w.Code != 200 {
		t.Fatalf("owner: %d %s", w.Code, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil || len(page.Events) != 1 {
		t.Fatalf("owner must not receive member profile events: %+v %v", page, err)
	}
}

func TestEventsLongPollReadsCanonicalDeviceRing(t *testing.T) {
	d, _, _, member := notificationFixture(t)
	d.Events = eventfeed.New(d.DB)
	mux := http.NewServeMux()
	d.foundationRoutes(mux)
	start := eventStart(t, notificationRequest(mux, "GET", member.AccessToken, "/v1/events?waitMs=0", ""))
	tx, err := d.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err = apievents.Append(tx, apievents.DeviceAudience(member.DeviceID), "session.updated", "session", "playback-1", "2", map[string]any{"state": "playing"}); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	w := notificationRequest(mux, "GET", member.AccessToken, "/v1/events?after="+start+"&waitMs=0", "")
	if w.Code != 200 {
		t.Fatalf("canonical ring: %d %s", w.Code, w.Body.String())
	}
	var page eventfeed.Page
	if err = json.Unmarshal(w.Body.Bytes(), &page); err != nil || len(page.Events) != 1 || page.Events[0].Type != "session.updated" || page.Events[0].Resource.ID != "playback-1" || string(page.Events[0].Data) != `{"state":"playing"}` {
		t.Fatalf("device event: %+v %v", page, err)
	}
}

func TestEventsStreamDeliversAfterCommitAndRetiresDuplicate(t *testing.T) {
	d, _, _, member := notificationFixture(t)
	d.Events = eventfeed.New(d.DB)
	mux := http.NewServeMux()
	d.foundationRoutes(mux)
	server := httptest.NewServer(mux)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	open := func() *http.Response {
		req, _ := http.NewRequestWithContext(ctx, "GET", server.URL+"/v1/events", nil)
		req.Header.Set("Authorization", "Bearer "+member.AccessToken)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	first := open()
	defer first.Body.Close()
	if first.StatusCode != 200 || !strings.HasPrefix(first.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("first stream: %d", first.StatusCode)
	}
	reader := bufio.NewReader(first.Body)
	if line, err := reader.ReadString('\n'); err != nil || !strings.HasPrefix(line, "retry:") {
		t.Fatalf("stream hello: %q %v", line, err)
	}
	// A stream that opens with nothing waiting says where it stands, so a
	// client has a position to resume from before the first real event.
	next := func() (event, id string) {
		t.Helper()
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				t.Fatal(err)
			}
			if strings.HasPrefix(line, "id: ") {
				id = strings.TrimSpace(strings.TrimPrefix(line, "id: "))
			}
			if strings.HasPrefix(line, "event: ") {
				return strings.TrimSpace(strings.TrimPrefix(line, "event: ")), id
			}
		}
	}
	if event, id := next(); event != "stream.opened" || id == "" {
		t.Fatalf("opening position: %q %q", event, id)
	}
	raiseNotice(t, d, notify.ProfileScope("local", "member", "member-profile"), "new", "New notice")
	d.Events.Wake()
	if event, _ := next(); event != "notification.changed" {
		t.Fatalf("unexpected event: %q", event)
	}
	// A reconnect takes over: it is admitted at once and the stream it
	// replaces ends, so a device that changed network is not refused.
	second := open()
	defer second.Body.Close()
	if second.StatusCode != 200 {
		t.Fatalf("a reconnecting stream was refused: %d", second.StatusCode)
	}
	for {
		if _, err := reader.ReadString('\n'); err != nil {
			break
		}
		if ctx.Err() != nil {
			t.Fatal("the replaced stream stayed open")
		}
	}
}

func TestEventsStreamStopsAfterSessionRevocation(t *testing.T) {
	d, _, _, member := notificationFixture(t)
	d.Events = eventfeed.New(d.DB)
	mux := http.NewServeMux()
	d.foundationRoutes(mux)
	server := httptest.NewServer(mux)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", server.URL+"/v1/events", nil)
	req.Header.Set("Authorization", "Bearer "+member.AccessToken)
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatalf("stream: %d", response.StatusCode)
	}
	if err = d.Identity.LogoutToken(context.Background(), member.AccessToken); err != nil {
		t.Fatal(err)
	}
	d.Events.Wake()
	reader := bufio.NewReader(response.Body)
	for {
		_, err = reader.ReadString('\n')
		if err != nil {
			if ctx.Err() != nil {
				t.Fatal("revoked stream stayed live until client timeout")
			}
			return
		}
	}
}

// SRV017: an installation parks at most four long polls. The fifth is refused
// at once with a paced, retryable answer that says nothing about the others; a
// read that does not park is still served, and a finished poll frees its
// permit.
func TestFifthParkedPollIsRefusedWithAPacedRetry(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	d, _, _, member := notificationFixture(t)
	d.Events = eventfeed.New(d.DB)
	mux := http.NewServeMux()
	d.foundationRoutes(mux)
	server := httptest.NewServer(mux)
	defer server.Close()
	poll := func(ctx context.Context, wait string) *http.Response {
		req, _ := http.NewRequestWithContext(ctx, "GET", server.URL+"/v1/events?waitMs="+wait, nil)
		req.Header.Set("Authorization", "Bearer "+member.AccessToken)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			t.Error(err)
			return nil
		}
		return resp
	}
	parked := make([]context.CancelFunc, 0, eventfeed.ParkedPolls)
	done := make(chan struct{}, eventfeed.ParkedPolls)
	for i := 0; i < eventfeed.ParkedPolls; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		parked = append(parked, cancel)
		go func() {
			defer func() { done <- struct{}{} }()
			// A probe below may hold a permit for its millisecond while this
			// one arrives; then this one was the fifth, and parks again.
			for ctx.Err() == nil {
				resp := poll(ctx, "25000")
				if resp == nil {
					return
				}
				resp.Body.Close()
				if resp.StatusCode != 429 {
					return
				}
				time.Sleep(5 * time.Millisecond)
			}
		}()
	}
	defer func() {
		for _, cancel := range parked {
			cancel()
		}
	}()
	// A one-millisecond wait parks too: until all four are held it is
	// admitted and answers empty at once; then it is the fifth.
	var refused *http.Response
	for deadline := time.Now().Add(5 * time.Second); refused == nil; {
		if time.Now().After(deadline) {
			t.Fatal("a fifth parked poll was never refused")
		}
		resp := poll(context.Background(), "1")
		if resp.StatusCode == 429 {
			refused = resp
			break
		}
		resp.Body.Close()
	}
	defer refused.Body.Close()
	var body struct {
		Error map[string]any `json:"error"`
	}
	if err := json.NewDecoder(refused.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Error["code"] != "event_wait_limit" || body.Error["retryable"] != true || len(body.Error) != 3 || refused.Header.Get("Retry-After") != "2" || refused.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("refusal: %v %v", body, refused.Header)
	}
	if read := poll(context.Background(), "0"); read.StatusCode != 200 {
		t.Fatalf("a read that does not park was refused: %d", read.StatusCode)
	} else {
		read.Body.Close()
	}
	parked[0]()
	<-done
	for deadline := time.Now().Add(5 * time.Second); ; {
		resp := poll(context.Background(), "1")
		resp.Body.Close()
		if resp.StatusCode == 200 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("a finished poll did not free its permit")
		}
	}
}

// SRV018 on a stream: a reader behind by pages that hit the byte budget gets
// them one per turn, paced, in order, with nothing lost and no resync; a
// reader more than a page of raw rows behind gets one resync and no history.
func TestEventStreamPacesBudgetPagesAndResyncsFarBehindReaders(t *testing.T) {
	d, _, _, member := notificationFixture(t)
	d.Events = eventfeed.New(d.DB)
	old := eventContinuation
	eventContinuation = 300 * time.Millisecond
	defer func() { eventContinuation = old }()
	mux := http.NewServeMux()
	d.foundationRoutes(mux)
	server := httptest.NewServer(mux)
	defer server.Close()
	start := eventStart(t, notificationRequest(mux, "GET", member.AccessToken, "/v1/events?waitMs=0", ""))
	audience := apievents.DeviceAudience(member.DeviceID)
	payload := strings.Repeat("x", eventfeed.EventDataBytes-64)
	appendEvents := func(n int, data any) {
		tx, err := d.DB.Begin()
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < n; i++ {
			if err = apievents.Append(tx, audience, "session.updated", "session", fmt.Sprint(i), "1", data); err != nil {
				_ = tx.Rollback()
				t.Fatal(err)
			}
		}
		if err = tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	appendEvents(40, map[string]string{"note": payload})
	type frame struct {
		event, id string
		at        time.Time
	}
	stream := func(cursor string) (func() frame, func()) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		req, _ := http.NewRequestWithContext(ctx, "GET", server.URL+"/v1/events", nil)
		req.Header.Set("Authorization", "Bearer "+member.AccessToken)
		req.Header.Set("Last-Event-ID", cursor)
		resp, err := http.DefaultClient.Do(req)
		if err != nil || resp.StatusCode != 200 {
			t.Fatalf("stream: %v %v", resp, err)
		}
		reader := bufio.NewReader(resp.Body)
		return func() frame {
				t.Helper()
				var f frame
				for {
					line, err := reader.ReadString('\n')
					if err != nil {
						t.Fatal(err)
					}
					switch {
					case strings.HasPrefix(line, "id: "):
						f.id = strings.TrimSpace(strings.TrimPrefix(line, "id: "))
					case strings.HasPrefix(line, "event: "):
						f.event = strings.TrimSpace(strings.TrimPrefix(line, "event: "))
					case line == "\n" && f.event != "":
						f.at = time.Now()
						return f
					}
				}
			}, func() {
				cancel()
				resp.Body.Close()
			}
	}
	next, stop := stream(start)
	var frames []frame
	for len(frames) < 40 {
		f := next()
		if f.event == "stream.resync" {
			t.Fatal("a reader inside the row bound was resynced")
		}
		if f.event == "session.updated" {
			frames = append(frames, f)
		}
	}
	stop()
	gaps := 0
	for i := 1; i < len(frames); i++ {
		if frames[i].at.Sub(frames[i-1].at) >= 250*time.Millisecond {
			gaps++
		}
	}
	if gaps < 2 {
		t.Fatalf("budget pages were not paced: %d paced turns", gaps)
	}
	appendEvents(eventfeed.PageLimit+1, nil)
	next, stop = stream(frames[len(frames)-1].id)
	defer stop()
	if f := next(); f.event != "stream.resync" || !strings.HasPrefix(f.id, "e1.") {
		t.Fatalf("far-behind reader: %+v", f)
	}
}

// H09: a quiet /v1/events stream must outlive its own frame deadline. The public
// listener is HTTP/2 over TLS, and there an expired write deadline does not wait
// for the next heartbeat: it resets the stream, which the browser reports as
// ERR_HTTP2_PROTOCOL_ERROR. The deadline used to be armed before every frame and
// never cleared, so a heartbeat paced slower than the budget — 25 s against 20 s
// — arrived after the stream had already been reset.
func TestAQuietEventStreamOutlivesTheFrameDeadline(t *testing.T) {
	previousFrame, previousHeartbeat := streamFrameDeadline, eventHeartbeat
	streamFrameDeadline, eventHeartbeat = 120*time.Millisecond, 350*time.Millisecond
	defer func() { streamFrameDeadline, eventHeartbeat = previousFrame, previousHeartbeat }()
	d, _, _, member := notificationFixture(t)
	d.Events = eventfeed.New(d.DB)
	mux := http.NewServeMux()
	d.foundationRoutes(mux)
	server := httptest.NewUnstartedServer(mux)
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, "GET", server.URL+"/v1/events", nil)
	request.Header.Set("Authorization", "Bearer "+member.AccessToken)
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 || response.ProtoMajor != 2 {
		t.Fatalf("stream: %d %s", response.StatusCode, response.Proto)
	}
	reader := bufio.NewReader(response.Body)
	// A frame ends at its blank line. A comment-only frame — the heartbeat — is
	// named by its comment, so both kinds are read the same way.
	next := func() (string, time.Time) {
		t.Helper()
		kind := ""
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				t.Fatalf("the quiet stream ended: %v", err)
			}
			switch {
			case strings.HasPrefix(line, ":"):
				kind = strings.TrimSpace(strings.TrimPrefix(line, ":"))
			case strings.HasPrefix(line, "event: "):
				kind = strings.TrimSpace(strings.TrimPrefix(line, "event: "))
			}
			if line == "\n" {
				return kind, time.Now()
			}
		}
	}
	var opened time.Time
	for deadline := time.Now().Add(5 * time.Second); opened.IsZero(); {
		if kind, at := next(); kind == "stream.opened" {
			opened = at
		}
		if time.Now().After(deadline) {
			t.Fatal("the stream never announced its position")
		}
	}
	// Nothing happens now. One frame deadline passes in silence; the stream must
	// still be there when the heartbeat is due.
	kind, beat := next()
	if kind != "heartbeat" {
		t.Fatalf("quiet stream: %q after %s", kind, beat.Sub(opened))
	}
	if quiet := beat.Sub(opened); quiet <= streamFrameDeadline {
		t.Fatalf("the first heartbeat came %s after the last frame, inside the %s frame deadline: the quiet window was never exercised", quiet, streamFrameDeadline)
	}
	// Each frame clears its own deadline, so the next quiet window is just as
	// healthy as the first.
	if kind, second := next(); kind != "heartbeat" || second.Sub(beat) <= streamFrameDeadline {
		t.Fatalf("second quiet window: %q after %s", kind, second.Sub(beat))
	}
	// Alive is not enough: a real event still reaches the stream afterwards.
	raiseNotice(t, d, notify.ProfileScope("local", "member", "member-profile"), "quiet", "Raised after the quiet window")
	d.Events.Wake()
	if kind, _ := next(); kind != "notification.changed" {
		t.Fatalf("event after the quiet window: %q", kind)
	}
}
