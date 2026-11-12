package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/certclient"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/hosted"
	"github.com/PorticoMediaServer/portico/server-next/internal/hosted/schedule"
	"github.com/PorticoMediaServer/portico/server-next/internal/hosted/trust"
)

// The link between this server and Portico Services: keeping it, and keeping it up to date.
// The contract is Portico Services' API.md. Everything here asks first whether the server is
// linked (db.Link) or still owes its one "this server has left" (db.Leave). A server with
// local sign-in has neither, so nothing in this file ever sends it anywhere.

// porticoLink is what the running server keeps for its link. What must survive a restart is
// in the database (db.Link, db.Leave).
type porticoLink struct {
	// client makes the calls; request handlers reach it through Server.hosted.
	client *hosted.Client
	// wake tells the background work that something changed.
	wake chan struct{}
	// run starts the background work; stop ends it, and done closes when it has ended.
	run  func()
	stop func()
	done chan struct{}
	// random draws the spread of every wait, in [0, 1).
	random func() float64
	// onLinked is told, with the issue time of the link's approval, each time the server
	// turns out to be linked: at start, and after every new link. A time it has not seen
	// before means a new link. onUnlinked is told once when a link it was told of has ended:
	// the server left, or the link is over. The certificate client's Linked and Unlinked go
	// here, so that it too stops calling Portico Services.
	onLinked   func(issuedAt time.Time)
	onUnlinked func()
	// published is the port and addresses the server publishes in its document, once remote
	// access finds them; nil publishes none.
	published func() []hosted.Address

	mu sync.Mutex
	// urgent says the owner has just changed something by hand that the document carries,
	// so the next document does not wait out the gap between address changes.
	urgent bool
	// started says the first pass since the process started has been made.
	started bool
	// told is the link onLinked was last told about, by its issue time.
	told int64
	// problem is why the last call to Portico Services failed, in plain words, or "".
	problem string
	// challengeKey signs the sign-in challenges of this run; used are the challenges already
	// answered, until each runs out.
	challengeKey []byte
	used         map[string]time.Time
	// resetCode is the setup code that switches the server to local sign-in, when the owner
	// asked for one from the data folder; see portico_reset.go.
	resetCode string
}

// porticoTransport carries the server's calls to Portico Services; nil is the ordinary one.
// The tests of this package set it to one that refuses, so no test can reach the real service.
var porticoTransport http.RoundTripper

func (l *porticoLink) setProblem(problem string) {
	l.mu.Lock()
	l.problem = problem
	l.mu.Unlock()
}

// porticoTick is how often a linked server looks, in its own database only, at whether its
// document has changed. It is not a call to anyone.
const porticoTick = time.Minute

// startPortico builds the client and the routes. It contacts nothing. The background work
// is started by startSetup, after the hooks it calls have been set.
func (s *Server) startPortico(ctx context.Context) error {
	ctx, stop := context.WithCancel(ctx)
	s.link = &porticoLink{wake: make(chan struct{}, 1), stop: stop, done: make(chan struct{}), random: rand.Float64,
		challengeKey: porticoChallengeSecret(), used: map[string]time.Time{},
		client: hosted.New(hosted.Options{HTTP: &http.Client{Transport: porticoTransport}, ServerID: s.id, Key: s.identity.key,
			Now: func() time.Time { return s.now() }})}
	s.hosted = s.link.client
	if link, linked, err := s.db.Link(ctx); err != nil {
		return err
	} else if linked {
		s.link.client.Keep(parseCertificate(link.Certificate))
	}
	if err := s.loadSignInReset(ctx); err != nil {
		return err
	}
	s.porticoRoutes()
	s.link.run = func() { s.goBackground(func() { s.runPortico(ctx) }) }
	return nil
}

func parseCertificate(raw json.RawMessage) *trust.Certificate {
	var c trust.Certificate
	if len(raw) == 0 || json.Unmarshal(raw, &c) != nil {
		return nil
	}
	return &c
}

// porticoWake makes the background work look now: after a link is made or ended, or a
// member changed. Without it the work looks within a minute anyway.
func (s *Server) porticoWake() {
	select {
	case s.link.wake <- struct{}{}:
	default:
	}
}

func (s *Server) runPortico(ctx context.Context) {
	defer close(s.link.done)
	for ctx.Err() == nil {
		next := s.porticoStep(ctx)
		timer := time.NewTimer(max(next.Sub(s.now()), time.Second))
		select {
		case <-ctx.Done():
		case <-s.link.wake:
		case <-timer.C:
		}
		timer.Stop()
	}
}

func sooner(a, b time.Time) time.Time {
	if b.IsZero() || (!a.IsZero() && a.Before(b)) {
		return a
	}
	return b
}

// retryWait is how long to wait after a call failed. Portico Services saying "retry after"
// is obeyed as it stands and does not count as a failed try; anything else backs off, doubling
// to the policy's ceiling, each wait drawn between half the step and all of it. A "busy" that
// names no wait is one of those: it is what a proxy in front of Portico Services says when
// Services is down or swamped, so nobody is setting a pace to obey.
func retryWait(policy schedule.Retry, tries *int, err error, r float64) time.Duration {
	var busy *hosted.BusyError
	if errors.As(err, &busy) && busy.Wait > 0 {
		// Never longer than a day: one answer that says years, a fault at Portico Services,
		// must not silence this server for good.
		return min(schedule.After(busy.Wait), 24*time.Hour)
	}
	wait := policy.Wait(*tries, r)
	*tries++
	return wait
}

// planCheckIn sets when a link's next check-in is due when none is planned, or when the
// server has just started and the planned one has passed. Starting is not a reason to call:
// the server waits for its own second of the day, unless Portico Services has not answered
// it for more than a day, and then it checks in at a random moment in the next hour.
func planCheckIn(l *db.Link, serverID string, now time.Time, r float64) {
	if wait, missed := schedule.StartDelay(time.Unix(l.LastContact, 0), now, r); missed {
		l.CheckInAt = now.Add(wait).Unix()
	} else {
		l.CheckInAt = schedule.NextCheckIn(serverID, now).Unix()
	}
}

// checkedIn records how a check-in went. After a good one the server is back on its own
// second; after a failed one it waits as retryWait says, which is what keeps a fleet that
// failed together from returning together.
func checkedIn(l *db.Link, serverID string, now time.Time, err error, r float64) {
	if err == nil {
		l.LastContact, l.CheckInTries = now.Unix(), 0
		l.CheckInAt = schedule.NextCheckIn(serverID, now).Unix()
		return
	}
	l.CheckInAt = now.Add(retryWait(schedule.CheckInRetry, &l.CheckInTries, err, r)).Unix()
}

// porticoStep does what is due now and answers when to look again.
func (s *Server) porticoStep(ctx context.Context) time.Time {
	now := s.now()
	next := now.Add(porticoTick)
	if leave, leaving, err := s.db.Leave(ctx); err != nil {
		s.log.Error("could not read the queued leave", "error", err)
	} else if leaving {
		next = sooner(next, s.sendLeave(ctx, leave, now))
	}
	link, linked, err := s.db.Link(ctx)
	if err != nil {
		s.log.Error("could not read the link to Portico Services", "error", err)
		return next
	}
	s.link.mu.Lock()
	first := !s.link.started
	s.link.started = true
	live := linked && !link.Unlinked
	tell := live && s.link.told != link.IssuedAt && s.link.onLinked != nil
	ended := !live && s.link.told != 0
	if tell {
		s.link.told = link.IssuedAt
	} else if ended {
		s.link.told = 0
	}
	s.link.mu.Unlock()
	if ended && s.link.onUnlinked != nil {
		s.link.onUnlinked()
	}
	if !live {
		return next
	}
	if tell {
		s.link.onLinked(time.Unix(link.IssuedAt, 0))
	}
	update := func(change func(*db.Link)) bool {
		link, err = s.db.UpdateLink(ctx, link.IssuedAt, now, change)
		if err != nil && !errors.Is(err, db.ErrNoLink) {
			s.log.Error("could not save the link to Portico Services", "error", err)
		}
		return err == nil
	}
	if link.CheckInAt == 0 || (first && link.CheckInAt <= now.Unix()) {
		if !update(func(l *db.Link) { planCheckIn(l, s.id, now, s.link.random()) }) {
			return next
		}
	}

	// The document: has anything in it changed, and is a changed one due to be sent?
	doc, digest, people, err := s.porticoDocument(ctx)
	if err != nil {
		s.log.Error("could not read this server's members for Portico Services", "error", err)
		return next
	}
	s.link.mu.Lock()
	urgent := s.link.urgent
	s.link.urgent = false
	s.link.mu.Unlock()
	if digest != link.DocumentDigest || urgent {
		if !update(func(l *db.Link) {
			if l.DocumentDigest != "" && digest != l.DocumentDigest {
				l.DocumentRevision++
			}
			switch {
			case l.DocumentTries > 0:
				// A wait already running after a failure stays: a change made while
				// Portico Services is away is not a reason to try sooner.
			case people != l.PeopleDigest || urgent || l.DocumentSent == 0:
				// A first document, a change of name, owner or members, or a change the
				// owner just made by hand goes at once.
				l.DocumentAt = 0
			default:
				// Only the addresses changed, by themselves. However often a network's
				// address flips, Portico Services hears of it at most once in
				// addressGap; when the wait ends the document sent is the one that is
				// true then, never a queue of the ones in between.
				l.DocumentAt = max(l.DocumentAt, l.DocumentSentAt+int64(addressGap/time.Second))
			}
			l.DocumentDigest, l.PeopleDigest = digest, people
		}) {
			return next
		}
	}
	// While Portico Services says it does not know this server, only the daily check-in asks.
	if link.DocumentSent < link.DocumentRevision && doc.OwnerAccountID != "" && link.Unknown == 0 {
		if due := documentDue(link); due.After(now) {
			next = sooner(next, due)
		} else if !s.sendDocument(ctx, &link, doc, now, update) {
			return next
		} else if link.DocumentSent < link.DocumentRevision {
			next = sooner(next, documentDue(link))
		}
	}

	if link.CheckInAt > now.Unix() {
		return sooner(next, time.Unix(link.CheckInAt, 0))
	}
	answer, failed := s.link.client.CheckIn(ctx, time.Unix(link.IssuedAt, 0), hosted.CheckIn{DocumentRevision: link.DocumentSent,
		NoteRevision: link.NoteRevision, Fleet: link.Fleet})
	s.porticoProblem(failed)
	if errors.Is(failed, hosted.ErrUnknownServer) {
		if update(func(l *db.Link) { unknownServer(l, s.id, now) }) && link.Unlinked {
			s.log.Warn("Portico Services has not known this server on three check-ins: its link has ended")
			return now
		}
		return sooner(next, time.Unix(link.CheckInAt, 0))
	}
	if !update(func(l *db.Link) {
		checkedIn(l, s.id, now, failed, s.link.random())
		if failed == nil {
			l.Unknown, l.UnknownAt = 0, 0
		}
		// A newer signing certificate that came with the answer is kept across restarts.
		if held := s.link.client.Certificate(); held != nil && held.Revision > l.CertificateRevision {
			l.Certificate, _ = json.Marshal(held)
			l.CertificateRevision = held.Revision
		}
		if failed == nil && answer.DocumentWanted && l.DocumentSent == l.DocumentRevision {
			// Portico Services does not hold the document this server thinks it sent.
			l.DocumentSent = 0
		}
	}) {
		return next
	}
	if failed != nil {
		return sooner(next, time.Unix(link.CheckInAt, 0))
	}
	if answer.Note != nil {
		if _, err := s.applyNote(ctx, *answer.Note); err != nil {
			s.log.Warn("the note in a check-in answer did not check out", "error", err)
		}
	}
	if answer.DocumentWanted {
		return now
	}
	return sooner(next, time.Unix(link.CheckInAt, 0))
}

// sendDocument sends the document and records what came of it. It answers false when the
// link is gone or has ended.
func (s *Server) sendDocument(ctx context.Context, link *db.Link, doc hosted.Document, now time.Time, update func(func(*db.Link)) bool) bool {
	doc.Revision = link.DocumentRevision
	stored, err := s.link.client.SendDocument(ctx, time.Unix(link.IssuedAt, 0), doc)
	s.porticoProblem(err)
	if !update(func(l *db.Link) {
		switch {
		case err != nil:
			// "I do not know this server" included: the daily check-in is what judges that.
			l.DocumentAt = now.Add(retryWait(schedule.ChangeRetry, &l.DocumentTries, err, s.link.random())).Unix()
		case stored.Revision > l.DocumentRevision:
			// Portico Services holds a later revision than this server knows of (a database
			// put back from a backup): go above it and send again, at once the first time
			// and on the usual backoff if it keeps happening.
			l.DocumentRevision, l.DocumentAt = stored.Revision+1, 0
			if l.DocumentTries > 0 {
				l.DocumentAt = now.Add(schedule.ChangeRetry.Wait(l.DocumentTries, s.link.random())).Unix()
			}
			l.DocumentTries++
			l.LastContact, l.DocumentSentAt = now.Unix(), now.Unix()
		default:
			l.DocumentSent, l.DocumentAt, l.DocumentTries = doc.Revision, 0, 0
			l.LastContact, l.DocumentSentAt = now.Unix(), now.Unix()
		}
	}) {
		return false
	}
	if err == nil {
		if err := s.db.MarkLeft(ctx, link.IssuedAt, stored.NotMembers, now); err != nil {
			s.log.Error("could not mark the accounts Portico Services says have left", "error", err)
		}
	}
	return true
}

// sendLeave tries the one "this server has left" and answers when to try again; the zero
// time when there is nothing more to do. After its give-up time it is dropped unsent, and
// the server never contacts Portico Services again.
func (s *Server) sendLeave(ctx context.Context, leave db.QueuedLeave, now time.Time) time.Time {
	if now.Unix() >= leave.GiveUpAt {
		if err := s.db.SaveLeave(ctx, leave, true, now); err != nil {
			s.log.Error("could not drop the queued leave", "error", err)
		}
		return time.Time{}
	}
	if leave.TryAt > now.Unix() {
		return time.Unix(leave.TryAt, 0)
	}
	err := s.link.client.Leave(ctx, time.Unix(leave.LinkIssuedAt, 0))
	if err != nil {
		leave.TryAt = now.Add(retryWait(schedule.ChangeRetry, &leave.Tries, err, s.link.random())).Unix()
	}
	if err := s.db.SaveLeave(ctx, leave, err == nil, now); err != nil {
		s.log.Error("could not save the queued leave", "error", err)
	}
	if err == nil {
		return time.Time{}
	}
	return time.Unix(min(leave.TryAt, leave.GiveUpAt), 0)
}

// porticoServerName is the server's name as Portico Services is given it, which must not be empty.
func (s *Server) porticoServerName() string {
	if name := strings.TrimSpace(s.serverName()); name != "" {
		return name
	}
	return "Portico"
}

// addressGap is the shortest time between two documents sent only because the server's
// addresses changed. Portico Services is sized for about one call a server a day; a home
// connection whose address flips every minute must not turn that into one a minute.
const addressGap = 5 * time.Minute

// documentGap is the shortest time between any two documents Portico Services has taken from
// this server, whatever changed: an owner adding ten people one after another costs Portico
// Services a few calls, not ten. The first document of a link is never held back.
const documentGap = time.Minute

// documentDue is when a document waiting to be sent may go.
func documentDue(l db.Link) time.Time {
	at := l.DocumentAt
	if l.DocumentSentAt != 0 {
		at = max(at, l.DocumentSentAt+int64(documentGap/time.Second))
	}
	return time.Unix(at, 0)
}

// porticoPublished is the addresses for the server's document, home ones first, as
// PublishedAddresses gives them.
func (s *Server) porticoPublished() []hosted.Address {
	out := []hosted.Address{}
	for _, a := range s.PublishedAddresses() {
		out = append(out, hosted.Address{Address: a.Address, Port: a.Port})
	}
	return out
}

// porticoCertificate takes the certificate client's word on where the Portico address and
// its certificate stand, and tells remote access. The zone is set only once the certificate
// for it can be served: a set zone is an address Enhanced security answers at, in force at
// once, and every name published under it must be one the server can prove. Until then the
// zone is given as an address still waiting for its certificate, so Enhanced waits, works as
// Standard and says why.
func (s *Server) porticoCertificate(st certclient.Status) {
	zone := ""
	if st.NameSpace != "" && st.State != "off" && st.State != "unlinked" {
		zone = st.NameSpace + "." + certclient.Zone
	}
	switch {
	case zone != "" && st.CertificateReady:
		s.SetPorticoZone(zone, s.portico.GetCertificate)
		s.SetPorticoAddress("", false, "")
	case zone != "":
		s.SetPorticoZone("", nil)
		s.SetPorticoAddress(zone, false, st.Message)
	default:
		s.SetPorticoZone("", nil)
		s.SetPorticoAddress("", false, "")
	}
}

// porticoUnlinked is told when the link has ended: the certificate client stops asking, and
// the server stops publishing and answering at its Portico names.
func (s *Server) porticoUnlinked() {
	s.portico.Unlinked()
	s.porticoCertificate(certclient.Status{State: "off"})
}

// porticoChangedByOwner says the owner has just changed, by hand, something the document
// carries (the public port, remote access on or off): the document goes at once.
func (s *Server) porticoChangedByOwner() {
	s.link.mu.Lock()
	s.link.urgent = true
	s.link.mu.Unlock()
	s.porticoWake()
}

// porticoDocument is this server's document as it stands, with a digest of it that changes
// when anything in it does and a digest of everything in it but the addresses.
func (s *Server) porticoDocument(ctx context.Context) (doc hosted.Document, digest, people string, err error) {
	name := s.porticoServerName()
	members, err := s.db.PorticoMembers(ctx)
	if err != nil {
		return hosted.Document{}, "", "", err
	}
	// Portico Services takes at most 5,000 members; the owner comes first.
	members = members[:min(len(members), 5000)]
	doc = hosted.Document{Name: name, Addresses: []hosted.Address{}, Members: make([]hosted.Member, 0, len(members))}
	for _, m := range members {
		if m.Role == "owner" {
			doc.OwnerAccountID = m.AccountID
		}
		doc.Members = append(doc.Members, hosted.Member{AccountID: m.AccountID, Role: m.Role})
	}
	hash := func() string {
		raw, _ := json.Marshal(doc)
		sum := sha256.Sum256(raw)
		return hex.EncodeToString(sum[:])
	}
	people = hash()
	if s.link.published != nil {
		if doc.Addresses = s.link.published(); doc.Addresses == nil {
			doc.Addresses = []hosted.Address{}
		}
		doc.Addresses = doc.Addresses[:min(len(doc.Addresses), 8)]
	}
	return doc, hash(), people, nil
}

// Messages the owner reads when the link has a problem.
const (
	msgPorticoUnreachable = "Portico Services can't be reached. This server will keep trying; nothing is needed from you."
	msgPorticoBusy        = "Portico Services is busy. This server will try again shortly."
	msgPorticoClock       = "This server's clock is wrong, so Portico Services can't accept its requests. Correct the clock."
	msgPorticoUnlinked    = "This server is no longer linked to a Portico Account. Link it again, or switch to local sign-in."
	msgPorticoUnknown     = "Portico Services doesn't recognise this server right now. People who are signed in are not affected. " +
		"If this lasts three days, nobody will be able to sign in with a Portico Account until you link this server again."
	msgPorticoOwner = "Portico Services can't list this server: the Portico Account of its owner has not approved it. " +
		"Change owner again and have the new owner approve with their Portico Account, or link this server again."
	msgPorticoDetails = "Portico Services didn't accept this server's details, so the server isn't listed in people's apps. " +
		"Give the server a name of at most 120 characters on one line; if that doesn't help, link this server again."
)

// unknownDays is how many check-ins, at least a day apart, must find Portico Services not
// knowing this server before its link is taken to have ended.
const unknownDays = 3

// unknownServer records a check-in answered with "I do not know this server". The answer
// carries no signature and Portico Services' own records can be lost, so one such answer
// changes nothing but what the owner is shown. The next check-in is on the server's own
// second, a day on; the link ends at the third.
func unknownServer(l *db.Link, serverID string, now time.Time) {
	if l.UnknownAt == 0 || now.Unix()-l.UnknownAt >= int64(24*time.Hour/time.Second) {
		l.Unknown++
		l.UnknownAt = now.Unix()
	}
	l.CheckInTries = 0
	l.CheckInAt = schedule.NextCheckIn(serverID, now).Unix()
	if l.Unknown >= unknownDays {
		l.Unlinked = true
	}
}

// porticoProblem keeps why the last background call failed, for the owner.
func (s *Server) porticoProblem(err error) {
	var busy *hosted.BusyError
	var clock *hosted.ClockError
	var refused *hosted.RefusedError
	switch {
	case err == nil, errors.Is(err, hosted.ErrUnknownServer):
		// "Unknown" is shown from the link itself, which counts it.
		s.link.setProblem("")
	case errors.As(err, &refused):
		// An answer, and a no: trying again changes nothing until the owner does something.
		if refused.Code == "owner_not_approved" {
			s.link.setProblem(msgPorticoOwner)
		} else {
			s.link.setProblem(msgPorticoDetails)
		}
		s.log.Warn("Portico Services refused a request from this server", "status", refused.Status, "code", refused.Code, "answer", refused.Detail)
	case errors.As(err, &busy):
		s.link.setProblem(msgPorticoBusy)
	case errors.As(err, &clock):
		s.link.setProblem(msgPorticoClock)
	default:
		s.link.setProblem(msgPorticoUnreachable)
		s.log.Warn("a call to Portico Services failed", "error", err)
	}
}

// applyNote checks a note from Portico Services, applies it by the link's rules and signs
// the acknowledgement of what is now applied. The push, an app carrying the note and the
// check-in's answer all end here; the server cannot and need not tell which it was.
func (s *Server) applyNote(ctx context.Context, envelope trust.Envelope) (trust.SignedAck, error) {
	note, err := s.link.client.OpenNote(envelope)
	if err != nil {
		return trust.SignedAck{}, err
	}
	in := db.Note{Revision: note.Revision, Fleet: note.Fleet, Left: note.Left, Unlinked: note.Unlinked}
	if held := s.link.client.Certificate(); held != nil {
		in.Certificate, _ = json.Marshal(held)
		in.CertificateRevision = held.Revision
	}
	link, changed, err := s.db.ApplyNote(ctx, in, s.now())
	if err != nil {
		return trust.SignedAck{}, err
	}
	if changed {
		// Only then: a note anyone can send again must not set the background work going.
		s.porticoWake()
	}
	return s.link.client.Ack(link.NoteRevision, link.Fleet), nil
}

// porticoError answers a failure to get or use a Portico approval, in the contract's four
// words. Each is shown with "Try again".
func porticoError(err error) error {
	var busy *hosted.BusyError
	var clock *hosted.ClockError
	var refused *hosted.RefusedError
	switch {
	case errors.As(err, &busy):
		e := refusal(http.StatusServiceUnavailable, "services_busy", "Portico Services is busy. Try again in a minute.", true)
		e.RetryAfterMs = schedule.After(busy.Wait).Milliseconds()
		return e
	case errors.As(err, &clock):
		return refusal(http.StatusServiceUnavailable, "clock_wrong",
			"This server's clock is "+clockOff(clock.Off)+" out, so Portico Services can't accept its requests. Correct the server's clock, then try again.", false)
	case errors.Is(err, hosted.ErrDeclined):
		return ErrConflict("approval_declined", "The server wasn't approved. Nothing has changed; start again.")
	case errors.As(err, &refused):
		return refusal(http.StatusBadGateway, "services_refused", "Portico Services didn't accept this server's details. "+
			"Give the server a name of at most 120 characters on one line, then try again.", false)
	case errors.Is(err, hosted.ErrNotConnected), errors.Is(err, hosted.ErrUnknownServer):
		e := refusal(http.StatusServiceUnavailable, "services_unreachable", "Portico Services can't be reached. Try again later.", true)
		e.RetryAfterMs = 30_000
		return e
	}
	return err
}

// clockOff says a clock difference as a person would: "3 hours", "12 minutes".
func clockOff(off time.Duration) string {
	if off < 0 {
		off = -off
	}
	plural := func(n int, unit string) string {
		if n == 1 {
			return "1 " + unit
		}
		return strconv.Itoa(n) + " " + unit + "s"
	}
	switch {
	case off >= 48*time.Hour:
		return plural(int(off/(24*time.Hour)), "day")
	case off >= 90*time.Minute:
		return plural(int(off.Round(time.Hour)/time.Hour), "hour")
	}
	return plural(max(int(off.Round(time.Minute)/time.Minute), 1), "minute")
}

// PorticoLinkState is where this server's link to Portico Services stands, for its owner.
type PorticoLinkState struct {
	// State is "local" (no link: the server uses local sign-in), "linked", or "unlinked"
	// (the owner removed this server from their Portico Account).
	State string `json:"state"`
	// AccountID is the Portico Account that owns the server.
	AccountID string     `json:"accountId,omitempty"`
	LinkedAt  *time.Time `json:"linkedAt,omitempty"`
	// LastContactAt is the last time Portico Services answered this server.
	LastContactAt *time.Time `json:"lastContactAt,omitempty"`
	// Listed says Portico Services has this server's current name and members.
	Listed bool `json:"listed"`
	// Problem says in plain words what is wrong, or is absent.
	Problem string `json:"problem,omitempty"`
}

// PorticoApproval is where the owner approves this server with their Portico Account.
type PorticoApproval struct {
	// ApprovalID names the approval when asking whether it has been given.
	ApprovalID  string `json:"approvalId"`
	ApprovalURL string `json:"approvalUrl"`
	// Code is what the person types or matches when approving on another device.
	Code string `json:"code"`
	// PollToken goes with ApprovalID when asking whether the approval has been given. Only
	// the page that started the approval has it.
	PollToken   string    `json:"pollToken"`
	ExpiresAt   time.Time `json:"expiresAt"`
	PollAfterMs int64     `json:"pollAfterMs"`
}

// PorticoApprovalPoll is how an approval stands. Approval is present once State is "approved".
type PorticoApprovalPoll struct {
	// State is "waiting" or "approved".
	State       string `json:"state"`
	Approval    string `json:"approval,omitempty"`
	PollAfterMs int64  `json:"pollAfterMs"`
}

// PorticoApprovalPollRequest asks how an approval stands.
type PorticoApprovalPollRequest struct {
	// PollToken is the one that came with the approval when it was started.
	PollToken string `json:"pollToken"`
}

// PorticoLinkRequest starts linking again; PorticoLinkFinish brings the approval back.
type PorticoLinkRequest struct {
	// ReturnURL is the page the person's browser comes back to with the approval.
	ReturnURL string `json:"returnUrl"`
}

type PorticoLinkFinish struct {
	Approval string `json:"approval"`
}

func approvalOut(a hosted.Approval) PorticoApproval {
	return PorticoApproval{ApprovalID: a.ID, ApprovalURL: a.URL, Code: a.Code, PollToken: a.PollToken, ExpiresAt: a.ExpiresAt.UTC(), PollAfterMs: a.PollAfter.Milliseconds()}
}

func (s *Server) porticoLinkState(r *http.Request, _ None) (PorticoLinkState, error) {
	link, linked, err := s.db.Link(r.Context())
	if err != nil || !linked {
		return PorticoLinkState{State: "local"}, err
	}
	at := func(seconds int64) *time.Time { t := time.Unix(seconds, 0).UTC(); return &t }
	out := PorticoLinkState{State: "linked", AccountID: link.AccountID, LinkedAt: at(link.IssuedAt), LastContactAt: at(link.LastContact),
		Listed: link.DocumentSent == link.DocumentRevision}
	s.link.mu.Lock()
	out.Problem = s.link.problem
	s.link.mu.Unlock()
	switch {
	case link.Unlinked:
		out.State, out.Problem, out.Listed = "unlinked", msgPorticoUnlinked, false
	case link.Unknown > 0:
		out.Problem, out.Listed = msgPorticoUnknown, false
	}
	return out, nil
}

// porticoLinkAgain asks Portico Services where the owner approves this server again. It is
// for a server that uses Portico Accounts whose link has ended or needs making afresh; the
// kind of sign-in does not change and nobody is signed out. The server saves nothing until
// the approval comes back.
func (s *Server) porticoLinkAgain(r *http.Request, req PorticoLinkRequest) (PorticoApproval, error) {
	if u, err := url.Parse(req.ReturnURL); err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || len(req.ReturnURL) > 500 {
		return PorticoApproval{}, ErrField("returnUrl", "Send the full address of the page to come back to.")
	}
	if _, linked, err := s.db.Link(r.Context()); err != nil {
		return PorticoApproval{}, err
	} else if !linked {
		return PorticoApproval{}, errLocalSignIn()
	}
	approval, err := s.hosted.StartApproval(r.Context(), hosted.ApprovalRequest{ServerID: s.id, ServerName: s.porticoServerName(),
		Purpose: hosted.PurposeSwitch, ReturnURL: req.ReturnURL})
	if err != nil {
		return PorticoApproval{}, porticoError(err)
	}
	return approvalOut(approval), nil
}

func errLocalSignIn() *Error {
	return ErrConflict("local_sign_in", "This server uses local sign-in, so it has no link to a Portico Account.")
}

// porticoLinkFinish makes the new link from the approval that came back. Only the Portico
// Account that owns the standing link can approve it: the owner's sign-in alone never hands
// the server to another account.
func (s *Server) porticoLinkFinish(r *http.Request, req PorticoLinkFinish) (PorticoLinkState, error) {
	ctx, now := r.Context(), s.now()
	if req.Approval == "" || len(req.Approval) > 8192 {
		return PorticoLinkState{}, ErrField("approval", "Send the approval exactly as it came back.")
	}
	if _, linked, err := s.db.Link(ctx); err != nil {
		return PorticoLinkState{}, err
	} else if !linked {
		return PorticoLinkState{}, errLocalSignIn()
	}
	decision, err := s.hosted.FinishApproval(ctx, s.id, req.Approval)
	if err == nil && decision.Purpose != hosted.PurposeSwitch {
		err = hosted.ErrDeclined
	}
	if err != nil {
		return PorticoLinkState{}, porticoError(err)
	}
	err = s.db.LinkServer(ctx, db.NewLink{OwnerID: CallerFrom(ctx).UserID, AccountID: decision.Account.ID, IssuedAt: decision.IssuedAt, Now: now})
	switch {
	case errors.Is(err, db.ErrNotLinkOwner), errors.Is(err, db.ErrConflict):
		return PorticoLinkState{}, refusal(http.StatusForbidden, "not_the_linked_account",
			"This server belongs to another Portico Account. Approve with the Portico Account that owns it.", false)
	case errors.Is(err, db.ErrStaleApproval):
		return PorticoLinkState{}, porticoError(hosted.ErrDeclined)
	case errors.Is(err, db.ErrNotPermitted):
		return PorticoLinkState{}, ErrNotPermitted()
	case err != nil:
		return PorticoLinkState{}, err
	}
	s.porticoWake()
	return s.porticoLinkState(r, None{})
}

// porticoApprovalPoll says whether an approval this server started has been given, for a
// person who approves on another device: the page asks here every few seconds while it is
// open, and hands the approval that comes back to the step it is finishing. It needs the
// token that came with the approval, which only the page that started it has: whoever holds
// an approval can finish its step, so it must not come back to anyone else.
func (s *Server) porticoApprovalPoll(r *http.Request, req PorticoApprovalPollRequest) (PorticoApprovalPoll, error) {
	returned, err := s.hosted.PollApproval(r.Context(), r.PathValue("id"), req.PollToken)
	if err != nil {
		return PorticoApprovalPoll{}, porticoError(err)
	}
	out := PorticoApprovalPoll{State: "waiting", Approval: returned, PollAfterMs: 3000}
	if returned != "" {
		out.State = "approved"
	}
	return out, nil
}
