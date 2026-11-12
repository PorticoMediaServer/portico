package api

// Tests written by the security review of this area (public #2520), kept as it wrote them.

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// answering gives chosen requests a canned answer and passes the rest to the fake.
type answering struct {
	answer func(*http.Request) *http.Response
}

func (a answering) RoundTrip(r *http.Request) (*http.Response, error) {
	if resp := a.answer(r); resp != nil {
		if r.Body != nil {
			r.Body.Close()
		}
		resp.Request = r
		return resp, nil
	}
	return http.DefaultTransport.RoundTrip(r)
}

func canned(status int, header map[string]string, body string) *http.Response {
	resp := &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
	for k, v := range header {
		resp.Header.Set(k, v)
	}
	return resp
}

func (p *porticoRig) servicesAnswer(answer func(*http.Request) *http.Response) {
	attach(p.s, &http.Client{Transport: answering{answer}}, p.fake.URL, p.fake, func() time.Time { return p.clock })
}

// The contract author's second point on this PR: a document Portico Services refuses is not
// "Portico Services can't be reached … nothing is needed from you". Something is needed.
func TestReviewARefusedDocumentIsNotCalledUnreachable(t *testing.T) {
	for _, refused := range []struct {
		status int
		code   string
	}{{422, "invalid"}, {409, "owner_not_approved"}} {
		p := newPorticoRig(t)
		p.link(accountJustin)
		p.servicesAnswer(func(r *http.Request) *http.Response {
			if r.Method == "PUT" && strings.HasSuffix(r.URL.Path, "/document") {
				return canned(refused.status, nil, `{"error":{"code":"`+refused.code+`","message":"refused","retry":false,"requestId":"req_1"}}`)
			}
			return nil
		})
		if code, _, _ := p.signIn(accountSam, p.invite(), 1); code != 200 {
			t.Fatal("sam could not join")
		}
		p.step()
		var state PorticoLinkState
		call(t, p.s, "GET", "/v1/admin/portico", p.owner.AccessToken, nil, &state)
		if state.Problem == msgPorticoUnreachable || state.Problem == "" {
			t.Errorf("a document refused as %d %s is shown to the owner as %q", refused.status, refused.code, state.Problem)
		}
	}
}

// "Retry after" is obeyed as it stands, and the next try is saved. An answer that says years
// (a fault at Portico Services) must not end this server's check-ins for good.
func TestReviewAnEndlessRetryAfterDoesNotEndTheCheckIns(t *testing.T) {
	p := newPorticoRig(t)
	p.link(accountJustin)
	p.servicesAnswer(func(r *http.Request) *http.Response {
		if strings.HasSuffix(r.URL.Path, "/check-in") {
			return canned(503, map[string]string{"Retry-After": "1576800000"}, `{"error":{"code":"busy","message":"busy","retry":true}}`)
		}
		return nil
	})
	p.run(25 * time.Hour)
	link, _, _ := p.s.db.Link(context.Background())
	if wait := time.Unix(link.CheckInAt, 0).Sub(p.clock); wait > 7*24*time.Hour {
		t.Errorf("after one answer of Retry-After 50 years, the next check-in is %s away, and that is saved across restarts", wait.Round(time.Hour))
	}
}

// A note that changes nothing must not set the background work going: anyone holding one
// valid note (every member's app carries them) can send it as often as they like.
func TestReviewARepeatedNoteDoesNotWakeTheBackgroundWork(t *testing.T) {
	p := newPorticoRig(t)
	p.link(accountJustin)
	note := p.fake.Note(p.s.id)
	if code, _ := p.notice(note); code != 200 {
		t.Fatalf("the note was answered %d", code)
	}
	select {
	case <-p.s.link.wake:
	default:
	}
	if code, _ := p.notice(note); code != 200 {
		t.Fatalf("the repeat was answered %d", code)
	}
	select {
	case <-p.s.link.wake:
		t.Error("a repeated note, which changed nothing, woke the background work (a read of every member each time)")
	default:
	}
}
