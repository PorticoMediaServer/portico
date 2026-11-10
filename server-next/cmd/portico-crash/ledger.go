package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/api"
)

// outcome is what the client was told about one write.
type outcome int

const (
	saved   outcome = iota // a 2xx: it must be there after any later kill
	refused                // an error answer: it must never show up
	unknown                // no answer, because the server died: either is fine
)

func outcomeOf(status int, err error) outcome {
	switch {
	case status >= 200 && status < 300:
		return saved
	case status != 0:
		return refused
	}
	return unknown
}

var client = &http.Client{Timeout: 30 * time.Second}

// call sends one JSON request. status is 0 when no answer came back.
func call(method, url, token string, body, out any) (int, error) {
	return callKeyed(method, url, token, "", body, out)
}

func callKeyed(method, url, token, key string, body, out any) (int, error) {
	var reader io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		reader = bytes.NewReader(raw)
	}
	r, err := http.NewRequest(method, url, reader)
	if err != nil {
		return 0, err
	}
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	if key != "" {
		r.Header.Set("Idempotency-Key", key)
	}
	resp, err := client.Do(r)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		// The status came but the body did not: the server died while answering.
		return 0, err
	}
	if resp.StatusCode >= 300 {
		return resp.StatusCode, errors.New(string(raw))
	}
	if out != nil && len(raw) > 0 {
		return resp.StatusCode, json.Unmarshal(raw, out)
	}
	return resp.StatusCode, nil
}

// write is one progress report: the position is unique across the whole run, so what the
// server holds afterwards says exactly which write it kept.
type write struct {
	position int64
	outcome  outcome
}

type profileCreate struct {
	name, key, id string
	outcome       outcome
}

type signIn struct {
	device, refreshToken string
}

// ledger is everything the client was told, and what it must find after a restart.
type ledger struct {
	mu         sync.Mutex
	next       int64
	writes     map[string][]write // per item, in the order they were sent
	dirty      map[string]bool    // items written since the last look
	profiles   []profileCreate    // not yet looked at after a restart
	signIns    []signIn           // saved, not yet looked at after a restart
	everything bool               // look at every item, not only the ones written lately
	tally      [3]int
	reasons    map[string]bool
}

func newLedger() *ledger {
	return &ledger{writes: map[string][]write{}, dirty: map[string]bool{}, reasons: map[string]bool{}}
}

func (l *ledger) nextPosition() int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.next++
	return l.next
}

func (l *ledger) wrote(id string, position int64, o outcome) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.writes[id] = append(l.writes[id], write{position, o})
	l.dirty[id] = true
	l.tally[o]++
}

// firstRefusal is true once per kind of refusal, so the log says why writes were refused.
func (l *ledger) firstRefusal(status int, err error) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	reason := fmt.Sprint(status, err)
	if i := strings.Index(reason, `"requestId"`); i > 0 {
		reason = reason[:i]
	}
	if l.reasons[reason] {
		return false
	}
	l.reasons[reason] = true
	return true
}

func (l *ledger) counts() (savedWrites, refusedWrites, unanswered int) {
	return l.tally[saved], l.tally[refused], l.tally[unknown]
}

func (l *ledger) signedIn(device, refreshToken string, o outcome) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if o == saved {
		l.signIns = append(l.signIns, signIn{device, refreshToken})
	}
}

// createProfile creates a profile with an Idempotency-Key and remembers what it was told.
func (l *ledger) createProfile(base, token, name, key string) {
	var p api.Profile
	status, err := callKeyed("POST", base+"/v1/profiles", token, key, api.ProfileCreate{Name: name}, &p)
	l.mu.Lock()
	defer l.mu.Unlock()
	l.profiles = append(l.profiles, profileCreate{name: name, key: key, id: p.ID, outcome: outcomeOf(status, err)})
}

// allowed says which positions an item may hold: the last saved write's, or that of any
// unanswered write sent after it. Nothing written means no position at all (0).
func allowed(history []write) map[int64]bool {
	ok := map[int64]bool{0: true}
	for _, w := range history {
		switch w.outcome {
		case saved:
			ok = map[int64]bool{w.position: true}
		case unknown:
			ok[w.position] = true
		}
	}
	return ok
}

// verify looks, through the restarted server, at everything written since the last look.
func (l *ledger) verify(p person, h *harness) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	look := l.dirty
	if l.everything {
		look = map[string]bool{}
		for id := range l.writes {
			look[id] = true
		}
	}
	for id := range look {
		var item api.Item
		if status, err := call("GET", h.base+"/v1/items/"+id, p.token, nil, &item); status != 200 {
			return fmt.Errorf("reading item %s answered %d: %v", id, status, err)
		}
		ok := allowed(l.writes[id])
		if !ok[item.State.PositionMs] {
			return fmt.Errorf("item %s is at %d ms; the client was told otherwise (history %v)", id, item.State.PositionMs, l.writes[id])
		}
		// What is there now is what the next write builds on: treat it as saved.
		l.writes[id] = []write{{item.State.PositionMs, saved}}
	}
	l.dirty = map[string]bool{}

	// A profile create is sent again with its key after the restart, as an app does with a
	// request whose answer it never had. Saved or not before, there is exactly one afterwards.
	for _, c := range l.profiles {
		var again api.Profile
		status, err := callKeyed("POST", h.base+"/v1/profiles", p.token, c.key, api.ProfileCreate{Name: c.name}, &again)
		if status != 200 && status != 201 {
			return fmt.Errorf("creating profile %q again after the restart answered %d: %v", c.name, status, err)
		}
		if c.outcome == saved && again.ID != c.id {
			return fmt.Errorf("profile %q was saved as %s before the kill, and its repeat answered %s", c.name, c.id, again.ID)
		}
		var list api.Page[api.Profile]
		if status, err := call("GET", h.base+"/v1/profiles", p.token, nil, &list); status != 200 {
			return fmt.Errorf("listing profiles answered %d: %v", status, err)
		}
		same := 0
		for _, q := range list.Items {
			if q.Name == c.name {
				same++
			}
		}
		if same != 1 {
			return fmt.Errorf("%d profiles are called %q after a create, a kill and a repeat", same, c.name)
		}
		// Room for the next one: an account holds eight.
		if status, err := call("DELETE", h.base+"/v1/profiles/"+again.ID, p.token, nil, nil); status != 204 {
			return fmt.Errorf("deleting profile %q answered %d: %v", c.name, status, err)
		}
	}
	l.profiles = nil

	// A sign-in the server answered is still good after the kill.
	for _, s := range l.signIns {
		var renewed api.Session
		body := api.RefreshRequest{RefreshToken: s.refreshToken, InstallationID: device(s.device).InstallationID, RequestID: "after-" + s.device}
		if status, err := call("POST", h.base+"/v1/auth/refresh", "", body, &renewed); status != 200 {
			return fmt.Errorf("the sign-in on %s was answered before the kill but its renewal answered %d: %v", s.device, status, err)
		}
	}
	l.signIns = nil
	return nil
}
