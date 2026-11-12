package certclient

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/hosted/schedule"
	"github.com/PorticoMediaServer/portico/server-next/internal/hosted/trust"
)

type nameSpaceAnswer struct {
	NameSpace string `json:"nameSpace"`
}

type orderRequest struct {
	CSR                 []byte `json:"csr"`
	NoUsableCertificate bool   `json:"noUsableCertificate,omitempty"`
}

type orderAnswer struct {
	ID           string `json:"id"`
	State        string `json:"state"`
	Position     int    `json:"position"`
	RetryAfterMs int64  `json:"retryAfterMs"`
	Problem      string `json:"problem"`
	Chain        string `json:"chain"`
}

type failure struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// Messages the owner reads on Remote access while the certificate is on its way.
const (
	msgWorking     = "Getting your secure address. This usually takes under a minute."
	msgQueued      = "Portico is issuing certificates for a lot of new servers. Yours is about %d minutes away. Nothing is needed from you."
	msgPaused      = "The certificate can't be issued just now. Portico will keep trying; nothing is needed from you."
	msgFailed      = "The certificate couldn't be issued. Portico will try again."
	msgUnreachable = "Portico Services can't be reached. Trying again %s."
	msgBusy        = "Portico Services is busy. Trying again %s."
	msgNoIssuing   = "This Portico Services isn't issuing certificates right now. Your Portico address works without one; Enhanced Security waits for it. Portico will ask again %s."
	msgClock       = "This server's clock is %s out, so Portico Services can't accept its requests. Correct the clock; Portico will try again %s."
	msgUnlinked    = "This server is no longer linked to Portico Services, so it has no Portico address. Link it again, or switch to local sign-in."
)

// Step does what is due now and answers when to come back; zero means nothing is due until
// the server is linked (again).
func (c *Client) Step(ctx context.Context) time.Time {
	c.mu.Lock()
	stopped, linkAt := c.stopped, c.linkAt
	c.mu.Unlock()
	if stopped || linkAt.IsZero() {
		return time.Time{}
	}
	now := c.o.Now()
	if linkAt.Unix() != c.state.LinkIssuedAt {
		// A new link: whatever was waited for or ordered belonged to the last one.
		c.state.LinkIssuedAt, c.state.AskNameSpace, c.state.NextTryAt = linkAt.Unix(), true, time.Time{}
		c.orderID, c.early, c.polls, c.failures, c.repeats = "", false, 0, 0, 0
		c.save()
	}
	if c.state.NextTryAt.After(now) {
		if c.Status().State == "off" {
			// A start inside a wait: say where things stand, so a certificate already
			// installed and still good is in use now and not when the wait ends.
			next := c.state.NextTryAt
			c.set(Status{State: "waiting", Message: msgPaused, NextTryAt: &next})
		}
		return c.state.NextTryAt
	}
	next := c.step(ctx, now)
	if !next.IsZero() {
		// Only a wait is kept across a restart. The renewal is worked out from the installed
		// certificate each time, so a server that loses it orders at once, not at the old
		// renewal date.
		c.state.NextTryAt = next
		if c.Status().State == "issued" {
			c.state.NextTryAt = time.Time{}
		}
		c.save()
	}
	return next
}

func (c *Client) step(ctx context.Context, now time.Time) time.Time {
	base := "/v1/servers/" + c.o.ServerID + "/certificate/"
	if c.state.NameSpace == "" || c.state.AskNameSpace {
		c.set(Status{State: "asking", Message: msgWorking})
		var answer nameSpaceAnswer
		if next, ok := c.call(ctx, "POST", base+"name-space", nil, &answer, now); !ok {
			return next
		}
		if !validNameSpace.MatchString(answer.NameSpace) {
			return c.retry(now, fmt.Sprintf(msgUnreachable, at(now.Add(time.Minute), now)))
		}
		if c.state.NameSpace != "" && answer.NameSpace != c.state.NameSpace {
			// Its certificate is for the old name space and no longer usable.
			c.o.Log.Info("this server's new link has a new Portico name space; a certificate for it will be ordered")
		}
		c.mu.Lock()
		c.state.NameSpace, c.state.AskNameSpace = answer.NameSpace, false
		c.mu.Unlock()
		c.save()
	}
	if c.usable(now) {
		if due := renewAt(c.leaf); now.Before(due) {
			c.set(Status{State: "issued"})
			return due
		}
	}
	key, err := c.pendingKey()
	if err != nil {
		c.o.Log.Error("could not make or keep the key for the Portico certificate", "error", err)
		return c.retry(now, msgFailed)
	}
	var answer orderAnswer
	if c.orderID == "" {
		csr, err := request(key, c.state.NameSpace)
		if err != nil {
			return c.retry(now, msgFailed)
		}
		// "No usable certificate" only when that is true and Portico Services has just said
		// a new one is otherwise too early.
		in := orderRequest{CSR: csr, NoUsableCertificate: c.early && !c.usable(now)}
		if next, ok := c.call(ctx, "POST", base+"orders", in, &answer, now); !ok {
			return next
		}
		c.orderID, c.polls, c.early = answer.ID, 0, false
	} else if next, ok := c.call(ctx, "GET", base+"orders/"+c.orderID, nil, &answer, now); !ok {
		return next
	}
	return c.follow(answer, key, now)
}

// follow acts on where an order stands.
func (c *Client) follow(answer orderAnswer, key *ecdsa.PrivateKey, now time.Time) time.Time {
	c.repeats, c.failures = 0, 0
	switch answer.State {
	case "issued":
		c.orderID = ""
		if err := c.install([]byte(answer.Chain), key, c.o.Now()); err != nil {
			c.o.Log.Error("the Portico certificate handed back cannot be used; a new one will be ordered", "error", err)
			os.Remove(c.path(pendingFile))
			return c.retry(now, msgFailed)
		}
		c.failures = 0
		c.o.Log.Info("the Portico certificate is installed", "expires", c.leaf.NotAfter)
		c.set(Status{State: "issued"})
		return renewAt(c.leaf)
	case "failed":
		c.orderID = ""
		return c.retry(now, msgFailed)
	}
	wait := poll.Wait(c.polls, jitter())
	c.polls++
	// The contract's longest estimate is 10 minutes; a longer one is a fault and is not obeyed.
	if retryAfter := min(time.Duration(answer.RetryAfterMs)*time.Millisecond, poll.Ceiling); retryAfter > wait {
		wait = retryAfter
	}
	status := Status{State: "working", Message: msgWorking}
	switch {
	case answer.Problem == "authority_paused":
		status = Status{State: "waiting", Message: msgPaused}
	case answer.State == "queued" && answer.Position > 20:
		// The line moves 20 a minute.
		status = Status{State: "queued", Message: fmt.Sprintf(msgQueued, int(math.Ceil(float64(answer.Position)/20)))}
	}
	next := now.Add(wait)
	status.NextTryAt = &next
	c.set(status)
	return next
}

// retry waits on the change backoff after something that did not work.
func (c *Client) retry(now time.Time, message string) time.Time {
	next := now.Add(schedule.ChangeRetry.Wait(c.failures, jitter()))
	c.failures++
	c.set(Status{State: "waiting", Message: strings.Replace(message, "%s", at(next, now), 1), NextTryAt: &next})
	return next
}

// longestWait is the most the client waits on Portico Services' word. A longer Retry-After is a
// fault (a unit mixed up, say) and is not obeyed: a certificate is never parked for good.
// "Not issuing certificates" has its own 6 hours, under this.
const longestWait = 24 * time.Hour

// validNameSpace is the contract's name space: "ptc-" and 20 of a-z and 2-7.
var validNameSpace = regexp.MustCompile(`^ptc-[a-z2-7]{20}$`)

// wait obeys Portico Services' Retry-After, which sets the pace in place of the backoff, up to
// longestWait. message says it, given when the next try is.
func (c *Client) wait(now time.Time, retryAfter time.Duration, message func(when string) string) time.Time {
	next := now.Add(min(schedule.After(retryAfter), longestWait))
	c.set(Status{State: "waiting", Message: message(at(next, now)), NextTryAt: &next})
	return next
}

// call sends one signed request and reads its answer into out. When it did not work, it has
// told the owner why and answers when to try again.
func (c *Client) call(ctx context.Context, method, path string, in, out any, now time.Time) (time.Time, bool) {
	var body []byte
	if in != nil {
		body, _ = json.Marshal(in)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.o.Services+path, bytes.NewReader(body))
	if err != nil {
		return c.retry(now, msgUnreachable), false
	}
	// Never signed before the link was made: a server whose clock is slow uses the link's
	// time until its own has passed it.
	signedAt := now
	if floor := time.Unix(c.state.LinkIssuedAt, 0); signedAt.Before(floor) {
		signedAt = floor
	}
	stamp, signature := trust.SignRequest(c.o.Key, method, req.URL.RequestURI(), signedAt, body)
	req.Header.Set(trust.RequestTimeHeader, stamp)
	req.Header.Set(trust.RequestSignatureHeader, signature)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.o.HTTP.Do(req)
	if err != nil {
		return c.retry(now, msgUnreachable), false
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 256<<10))
	if err != nil {
		return c.retry(now, msgUnreachable), false
	}
	if res.StatusCode/100 == 2 {
		if json.Unmarshal(raw, out) != nil {
			return c.retry(now, msgUnreachable), false
		}
		// The backoff starts again only when an order has answered (follow), not on any
		// answer: a name space given each time in a loop that goes nowhere is no progress.
		return time.Time{}, true
	}
	var f failure
	json.Unmarshal(raw, &f)
	retryAfter := time.Duration(0)
	if seconds, err := strconv.Atoi(res.Header.Get("Retry-After")); err == nil && seconds > 0 {
		retryAfter = time.Duration(seconds) * time.Second
	}
	switch f.Error.Code {
	case "server_unknown":
		// Portico Services no longer knows this server: stop calling until it is linked again.
		c.o.Log.Warn("Portico Services no longer knows this server; it stops asking for its certificate")
		c.mu.Lock()
		c.stopped = true
		c.mu.Unlock()
		c.set(Status{State: "unlinked", Message: msgUnlinked})
		return time.Time{}, false
	case "busy":
		return c.wait(now, retryAfter, func(when string) string { return fmt.Sprintf(msgBusy, when) }), false
	case "certificates_unavailable":
		// This Portico Services has no certificate authority. Nothing was queued; it is asked
		// again only when it says, never on the short backoff.
		if retryAfter == 0 {
			retryAfter = 6 * time.Hour
		}
		return c.wait(now, retryAfter, func(when string) string { return fmt.Sprintf(msgNoIssuing, when) }), false
	case "slow_down", "replacement_limit":
		if f.Error.Code == "slow_down" && !(method == "POST" && strings.HasSuffix(path, "/orders")) {
			// The ordinary call limits: nothing to say beyond "busy".
			return c.wait(now, retryAfter, func(when string) string { return fmt.Sprintf(msgBusy, when) }), false
		}
		// An order limit: Portico Services' own message says which, in plain words.
		return c.wait(now, retryAfter, func(when string) string { return f.Error.Message + " Portico will ask again " + when + "." }), false
	case "too_early":
		if !c.usable(now) && !c.early {
			// No usable certificate here, so it is not too early for this server: say so.
			c.early = true
			return c.again(now), false
		}
		next := now.Add(min(max(retryAfter, time.Minute), longestWait))
		c.set(Status{State: "issued", NextTryAt: &next})
		return next, false
	case "name_space_missing":
		c.mu.Lock()
		c.state.NameSpace = ""
		c.mu.Unlock()
		return c.again(now), false
	case "not_found":
		c.orderID = ""
		return c.again(now), false
	case "invalid":
		// A request Portico Services would not take: start again with a new key.
		os.Remove(c.path(pendingFile))
		return c.retry(now, msgFailed), false
	case "signature", "signed_before_link":
		next := now.Add(schedule.ChangeRetry.Wait(c.failures, jitter()))
		c.failures++
		if date, err := http.ParseTime(res.Header.Get("Date")); err == nil {
			if off := now.Sub(date).Abs(); off > time.Minute {
				c.set(Status{State: "waiting", Message: fmt.Sprintf(msgClock, plain(off), at(next, now)), NextTryAt: &next})
				return next, false
			}
		}
		// A clock a few seconds slow just after linking passes on the next try; Portico
		// Services was reached, so the owner is not told it could not be.
		c.o.Log.Warn("Portico Services did not accept this server's signature; trying again", "code", f.Error.Code)
		c.set(Status{State: "working", Message: msgWorking, NextTryAt: &next})
		return next, false
	}
	return c.retry(now, msgUnreachable), false
}

// again is when to try once more after an answer that says what to do differently (ask for
// the name space, place the order again): at once the first time, then on the change backoff,
// so an answer Portico Services keeps giving never becomes a call every second.
func (c *Client) again(now time.Time) time.Time {
	c.repeats++
	if c.repeats == 1 {
		return now
	}
	return c.retry(now, msgWorking)
}

// at says when, for the owner: "at 14:32" today, otherwise "on 14 November at 09:12".
func at(t, now time.Time) string {
	t, now = t.Local(), now.Local()
	if t.YearDay() == now.YearDay() && t.Year() == now.Year() {
		return "at " + t.Format("15:04")
	}
	return "on " + t.Format("2 January at 15:04")
}

// plain says a clock difference in words.
func plain(d time.Duration) string {
	switch {
	case d >= 2*time.Hour:
		return fmt.Sprintf("%d hours", int(d.Hours()))
	case d >= time.Hour:
		return "an hour"
	default:
		return fmt.Sprintf("%d minutes", int(math.Ceil(d.Minutes())))
	}
}
