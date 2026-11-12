package api

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"github.com/PorticoMediaServer/portico/server-next/internal/hosted"
	"net/http"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/auth"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/hosted/trust"
)

// Signing in with a Portico Account, and the notes Portico Services sends this server.
//
// A sign-in is three steps. The app asks this server for a challenge; it takes the challenge
// to Portico Services, which signs "this is account A, speaking to server S, answering this
// challenge"; and it brings that note back here. The server checks the note against the root
// key it was built with, without calling anyone, and then decides from its own tables whether
// the account may come in. Portico Services proves who a person is and nothing more.

func (s *Server) porticoRoutes() {
	Handle(s, "POST /v1/auth/portico/challenge", Public, s.porticoChallenge)
	Handle(s, "POST /v1/auth/portico/exchange", Public, s.porticoExchange)
	Handle(s, "POST /v1/hosted/notices", Public, s.hostedNotice)
	Handle(s, "POST /v1/hosted/approvals/{id}/poll", Public, s.porticoApprovalPoll)
	Handle(s, "POST /v1/setup/local-sign-in", Public, s.localSignInReset)
	Handle(s, "GET /v1/admin/portico", Owner, s.porticoLinkState).Scope = ScopeSession
	Handle(s, "POST /v1/admin/portico/link", Owner, s.porticoLinkAgain).Scope = ScopeSession
	Handle(s, "POST /v1/admin/portico/link/finish", Owner, s.porticoLinkFinish).Scope = ScopeSession
}

// PorticoChallengeRequest asks for a sign-in challenge. It says nothing: the route has a body
// so that it comes under the rule for every route that needs no sign-in, which takes only a
// request sent as application/json ("{}" here). A page on another site cannot send that
// unasked, so it cannot ask a linked server for a challenge.
type PorticoChallengeRequest struct{}

// PorticoChallenge is what the app takes to Portico Services to ask for a sign-in note.
type PorticoChallenge struct {
	Challenge string `json:"challenge"`
	// ServerID is this server's id, which the app names when it asks.
	ServerID  string    `json:"serverId"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// PorticoExchange brings the sign-in note back.
type PorticoExchange struct {
	// Challenge is the one this server gave.
	Challenge string `json:"challenge"`
	// Identity is the signed note exactly as Portico Services answered it.
	Identity json.RawMessage `json:"identity"`
	// Invitation is an invitation's code, for a person joining this server.
	Invitation string     `json:"invitation,omitempty"`
	Device     DeviceInfo `json:"device"`
}

// HostedNotice carries a signed note from Portico Services, pushed by it or carried by an app.
type HostedNotice struct {
	Note json.RawMessage `json:"note"`
}

// HostedNoticeAck is this server's signed word on what it has now applied.
type HostedNoticeAck struct {
	Ack trust.SignedAck `json:"ack"`
}

// porticoChallengeLife is how long a challenge can be answered: as long as a sign-in note lasts.
const porticoChallengeLife = 5 * time.Minute

var porticoChallengeEncoding = base64.RawURLEncoding

func porticoChallengeSecret() []byte {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		panic(err)
	}
	return key
}

// newChallenge makes a challenge for a sign-in under the link issued at linkIssuedAt. The
// challenge carries its own end time and that link, signed with a key made when the server
// started, so the server keeps nothing for a challenge nobody answers.
func (l *porticoLink) newChallenge(linkIssuedAt int64, now time.Time) (string, time.Time) {
	expires := now.Add(porticoChallengeLife)
	body := make([]byte, 32)
	if _, err := rand.Read(body[:16]); err != nil {
		panic(err)
	}
	binary.BigEndian.PutUint64(body[16:], uint64(expires.Unix()))
	binary.BigEndian.PutUint64(body[24:], uint64(linkIssuedAt))
	mac := hmac.New(sha256.New, l.challengeKey)
	mac.Write(body)
	return porticoChallengeEncoding.EncodeToString(body) + "." + porticoChallengeEncoding.EncodeToString(mac.Sum(nil)), expires
}

// openChallenge answers the link a challenge was made under and when it ends, for a
// challenge this server made since it started that has not run out.
func (l *porticoLink) openChallenge(challenge string, now time.Time) (linkIssuedAt int64, expires time.Time, ok bool) {
	head, tail, found := strings.Cut(challenge, ".")
	body, err := porticoChallengeEncoding.DecodeString(head)
	sum, err2 := porticoChallengeEncoding.DecodeString(tail)
	if !found || err != nil || err2 != nil || len(body) != 32 {
		return 0, time.Time{}, false
	}
	mac := hmac.New(sha256.New, l.challengeKey)
	mac.Write(body)
	expires = time.Unix(int64(binary.BigEndian.Uint64(body[16:])), 0)
	if !hmac.Equal(sum, mac.Sum(nil)) || !now.Before(expires) {
		return 0, time.Time{}, false
	}
	return int64(binary.BigEndian.Uint64(body[24:])), expires, true
}

// spend marks a challenge as answered. It answers false for one answered before: a sign-in
// note is good for one sign-in, so a copy of it changes nothing.
func (l *porticoLink) spend(challenge string, expires, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for c, until := range l.used {
		if !now.Before(until) {
			delete(l.used, c)
		}
	}
	if _, done := l.used[challenge]; done {
		return false
	}
	l.used[challenge] = expires
	return true
}

func errPorticoSignIn() *Error {
	return refusal(http.StatusUnauthorized, "portico_sign_in_invalid", "That Portico Account sign-in didn't work. Try again.", false)
}

func errPorticoUnlinked() *Error {
	return ErrConflict("portico_unlinked", "This server is no longer linked to a Portico Account. Its owner needs to link it again.")
}

// linkForSignIn is the standing link, for a route only a Portico Accounts server answers.
func (s *Server) linkForSignIn(r *http.Request) (db.Link, error) {
	link, linked, err := s.db.Link(r.Context())
	switch {
	case err != nil:
		return link, err
	case !linked:
		return link, errLocalSignIn()
	case link.Unlinked:
		return link, errPorticoUnlinked()
	}
	return link, nil
}

func (s *Server) porticoChallenge(r *http.Request, _ PorticoChallengeRequest) (PorticoChallenge, error) {
	link, err := s.linkForSignIn(r)
	if err != nil {
		return PorticoChallenge{}, err
	}
	challenge, expires := s.link.newChallenge(link.IssuedAt, s.now())
	return PorticoChallenge{Challenge: challenge, ServerID: s.id, ExpiresAt: expires.UTC()}, nil
}

// porticoChallengeOpen says whether a challenge is one this server made, still running, under
// the link that stands now, and not yet answered: a note asked for under an earlier link
// signs nobody in. It uses nothing up.
func (s *Server) porticoChallengeOpen(r *http.Request, challenge string, now time.Time) (db.Link, time.Time, error) {
	link, err := s.linkForSignIn(r)
	if err != nil {
		return link, time.Time{}, err
	}
	issuedAt, expires, ok := s.link.openChallenge(challenge, now)
	s.link.mu.Lock()
	_, answered := s.link.used[challenge]
	s.link.mu.Unlock()
	if !ok || issuedAt != link.IssuedAt || answered {
		return link, time.Time{}, errPorticoSignIn()
	}
	return link, expires, nil
}

// porticoProven checks a sign-in note: Portico Services' word that this is that Portico
// Account, answering a challenge this server gave under the link that stands now, used once.
// It grants nothing; the caller decides from the server's own tables what the account may do.
func (s *Server) porticoProven(r *http.Request, challenge string, identity json.RawMessage, now time.Time) (hosted.Account, db.Link, error) {
	link, expires, err := s.porticoChallengeOpen(r, challenge, now)
	if err != nil {
		return hosted.Account{}, link, err
	}
	var envelope trust.Envelope
	if json.Unmarshal(identity, &envelope) != nil {
		return hosted.Account{}, link, ErrField("identity", "Send the sign-in note exactly as Portico Services answered it.")
	}
	account, _, err := s.hosted.VerifyIdentity(s.id, challenge, trust.Returned(envelope))
	if err != nil || account.ID == "" || !s.link.spend(challenge, expires, now) {
		return hosted.Account{}, link, errPorticoSignIn()
	}
	return account, link, nil
}

func (s *Server) porticoExchange(r *http.Request, req PorticoExchange) (SignInResponse, error) {
	ctx, now := r.Context(), s.now()
	session, refresh, err := s.newSession(r, req.Device, now)
	if err != nil {
		return SignInResponse{}, err
	}
	account, link, err := s.porticoProven(r, req.Challenge, req.Identity, now)
	if err != nil {
		return SignInResponse{}, err
	}
	// An invitation's code can be guessed at, so wrong ones count against where they came
	// from, as they do on the invitation routes.
	limits := s.sourceLimit(r)
	if req.Invitation != "" {
		if wait, err := s.db.LimitWait(ctx, now, limits...); err != nil {
			return SignInResponse{}, err
		} else if wait > 0 {
			return SignInResponse{}, errTooManyAttempts(wait)
		}
	}
	in := db.PorticoSignIn{LinkIssuedAt: link.IssuedAt, AccountID: account.ID, Email: account.Email, Session: session, Now: now}
	in.Username, _ = auth.Username(account.Username)
	if _, usable := auth.Username(account.Username); !usable {
		in.Username = ""
	}
	if in.ProfileName = strings.TrimSpace(account.DisplayName); !auth.ValidName(in.ProfileName) {
		if in.ProfileName = account.Username; !auth.ValidName(in.ProfileName) {
			in.ProfileName = "Me"
		}
	}
	if len(in.Email) > 320 {
		in.Email = ""
	}
	if req.Invitation != "" {
		in.InvitationHash = invitationHash(req.Invitation)
	}
	st, joined, err := s.db.SignInWithPortico(ctx, in)
	switch {
	case errors.Is(err, db.ErrNotMember) && req.Invitation != "":
		if err := s.db.RecordFailure(ctx, now, limits...); err != nil {
			return SignInResponse{}, err
		}
		return SignInResponse{}, errNoInvitation()
	case errors.Is(err, db.ErrNotMember):
		return SignInResponse{}, refusal(http.StatusForbidden, "not_a_member",
			"Your Portico Account isn't on this server. Ask the server's owner for an invitation.", false)
	case errors.Is(err, db.ErrLeft):
		return SignInResponse{}, refusal(http.StatusForbidden, "left_server",
			"You left this server, or your Portico Account was removed from it. Ask the server's owner to let you back in.", false)
	case errors.Is(err, db.ErrAccountUnavailable):
		return SignInResponse{}, refusal(http.StatusForbidden, "account_off",
			"Your account on this server is turned off. Ask the server's owner.", false)
	case errors.Is(err, db.ErrNoLink):
		return SignInResponse{}, errPorticoSignIn()
	case err != nil:
		return SignInResponse{}, err
	}
	if joined {
		// The member list changed: Portico Services is sent the server's document.
		s.porticoWake()
	}
	s.keepCertificate(r, link)
	return s.signedIn(ctx, st, now, refresh)
}

// keepCertificate saves a newer signing certificate that came with a document, so the keys
// it withdraws stay refused after a restart.
func (s *Server) keepCertificate(r *http.Request, link db.Link) {
	held := s.link.client.Certificate()
	if held == nil || held.Revision <= link.CertificateRevision {
		return
	}
	raw, _ := json.Marshal(held)
	if _, err := s.db.UpdateLink(r.Context(), link.IssuedAt, s.now(), func(l *db.Link) {
		if held.Revision > l.CertificateRevision {
			l.Certificate, l.CertificateRevision = raw, held.Revision
		}
	}); err != nil && !errors.Is(err, db.ErrNoLink) {
		s.log.Error("could not keep Portico Services' signing certificate", "error", err)
	}
}

// hostedNotice takes a note from Portico Services, whoever carried it. It needs no sign-in:
// the note proves itself, and it can only say what Portico Services signed for this server.
// A note that checks out is always acknowledged with what is now applied, also when it was
// old or a repeat and changed nothing. A server with local sign-in has no such route.
func (s *Server) hostedNotice(r *http.Request, req HostedNotice) (HostedNoticeAck, error) {
	if _, linked, err := s.db.Link(r.Context()); err != nil {
		return HostedNoticeAck{}, err
	} else if !linked {
		return HostedNoticeAck{}, ErrNotFound()
	}
	var envelope trust.Envelope
	if json.Unmarshal(req.Note, &envelope) != nil {
		return HostedNoticeAck{}, ErrField("note", "Send the note exactly as Portico Services signed it.")
	}
	ack, err := s.applyNote(r.Context(), envelope)
	switch {
	case errors.Is(err, trust.ErrTrust):
		return HostedNoticeAck{}, refusal(http.StatusForbidden, "note_invalid", "That note was not signed by Portico Services for this server.", false)
	case errors.Is(err, db.ErrNoLink):
		return HostedNoticeAck{}, ErrNotFound()
	}
	return HostedNoticeAck{Ack: ack}, err
}
