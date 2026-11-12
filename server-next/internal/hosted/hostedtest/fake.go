// Package hostedtest is a fake Portico Services for tests. It answers the routes a server
// calls as Portico Services' API.md describes them, checks every request's signature the way
// the real service does, and signs real documents with a root made for the test. Nothing in
// it reaches the network; tests never call the real service.
package hostedtest

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/hosted/trust"
)

// Call is one request the fake received.
type Call struct {
	Method, Path string
	At           time.Time
	// Status is what it was answered with.
	Status int
}

type approval struct {
	id, code, serverID, name, purpose string
	key                               ed25519.PublicKey
	state                             string // waiting, approved, declined
	account                           Account
	decidedAt                         time.Time
}

type server struct {
	key   ed25519.PublicKey
	owner string
	// since is the issue time of the approval that made the link.
	since time.Time
	// linked: the server has sent its first document.
	linked, unlinked                        bool
	noteRevision, ackedRevision, fleetAcked int64
	documentRevision                        int64
	document                                map[string]any
	members                                 map[string]string // account id -> role
	left                                    []string          // departures the server has not confirmed
	gone                                    map[string]bool   // accounts that left or were deleted
	approvedOwners                          map[string]bool
}

// Account is a Portico Account in the fake.
type Account struct{ ID, Username, DisplayName, Email string }

// Services is the fake. Its exported fields are set before use.
type Services struct {
	*httptest.Server
	// Now is the fake's clock.
	Now func() time.Time

	mu        sync.Mutex
	rootKey   ed25519.PrivateKey
	signKey   ed25519.PrivateKey
	cert      *trust.Certificate
	fleet     int64
	approvals map[string]*approval
	servers   map[string]*server
	calls     []Call
	down      bool
	busy      time.Duration
	seq       int
}

// New starts a fake Portico Services that stops with the test.
func New(t testing.TB) *Services {
	t.Helper()
	rootPublic, rootKey, _ := ed25519.GenerateKey(rand.Reader)
	_ = rootPublic
	f := &Services{Now: time.Now, rootKey: rootKey, approvals: map[string]*approval{}, servers: map[string]*server{}}
	f.NewSigningKey(1, nil)
	f.Server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.Server.Close)
	return f
}

// Root is the public half of the fake's root key, for the client under test.
func (f *Services) Root() ed25519.PublicKey { return f.rootKey.Public().(ed25519.PublicKey) }

// NewSigningKey replaces the working signing key with a new one certified at revision,
// naming withdrawn as the keys no longer to be believed. It answers the old key's id.
func (f *Services) NewSigningKey(revision uint64, withdrawn []string) (oldKeyID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.cert != nil {
		oldKeyID = f.cert.KeyID
	}
	public, key, _ := ed25519.GenerateKey(rand.Reader)
	now := f.Now()
	cert, err := trust.Certify(f.rootKey, public, trust.Documents, now.Add(-time.Hour), now.Add(300*24*time.Hour), revision, withdrawn)
	if err != nil {
		panic(err)
	}
	f.signKey, f.cert = key, cert
	return oldKeyID
}

// RaiseFleet says there is news for every server, as a withdrawn signing key is.
func (f *Services) RaiseFleet() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fleet = f.Now().Unix()
}

// Sign signs any document as Portico Services would. Tests use it to make documents the
// real service would never sign: for another server, out of date, with chosen numbers.
func (f *Services) Sign(document any) trust.Envelope {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sign(document)
}

func (f *Services) sign(document any) trust.Envelope {
	payload, _ := json.Marshal(document)
	envelope, err := trust.Sign(f.signKey, f.cert, payload)
	if err != nil {
		panic(err)
	}
	return envelope
}

// Down makes every request fail without an answer, as an outage does.
func (f *Services) Down(down bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.down = down
}

// Busy makes every request answer "503 busy" with that Retry-After; zero ends it.
func (f *Services) Busy(wait time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.busy = wait
}

// Calls are the requests received so far.
func (f *Services) Calls() []Call {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Call(nil), f.calls...)
}

// Count is how many requests were received for a method and a path ending.
func (f *Services) Count(method, suffix string) int {
	n := 0
	for _, c := range f.Calls() {
		if c.Method == method && strings.HasSuffix(c.Path, suffix) {
			n++
		}
	}
	return n
}

// Decide answers a waiting approval as the person signed in to account would. It answers
// the approval as the browser would carry it back, or "" when declined or refused.
func (f *Services) Decide(approvalID string, account Account, approve bool) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	a := f.approvals[approvalID]
	if a == nil || a.state != "waiting" {
		return ""
	}
	if !approve {
		a.state = "declined"
		return ""
	}
	existing := f.servers[a.serverID]
	// A server whose link it has itself confirmed cannot be approved by another account.
	if existing != nil && existing.linked && !existing.unlinked && existing.owner != account.ID && a.purpose != trust.PurposeChangeOwner {
		return ""
	}
	a.state, a.account, a.decidedAt = "approved", account, f.Now().UTC().Truncate(time.Second)
	if a.purpose == trust.PurposeChangeOwner {
		if existing != nil {
			existing.approvedOwners[account.ID] = true
		}
	} else {
		// A setup or switch approval makes a new link: its numbers start at its issue time.
		s := &server{key: a.key, owner: account.ID, since: a.decidedAt, noteRevision: a.decidedAt.Unix(), ackedRevision: a.decidedAt.Unix(),
			members: map[string]string{}, gone: map[string]bool{}, approvedOwners: map[string]bool{account.ID: true}}
		if existing != nil {
			s.gone = existing.gone
		}
		f.servers[a.serverID] = s
	}
	return f.signedApproval(a)
}

func (f *Services) signedApproval(a *approval) string {
	return trust.Returned(f.sign(trust.Approval{Kind: trust.ApprovalKind, ApprovalID: a.id, ServerID: a.serverID, ServerName: a.name,
		Purpose: a.purpose, AccountID: a.account.ID, Username: a.account.Username, DisplayName: a.account.DisplayName,
		IssuedAt: a.decidedAt, ExpiresAt: a.decidedAt.Add(10 * time.Minute)}))
}

// LastApproval is the id of the approval asked for most recently.
func (f *Services) LastApproval() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return "apr_" + strconv.Itoa(f.seq)
}

// Identity signs a sign-in note: account, speaking to the server, answering its challenge.
// It is what POST /v1/servers/{id}/identity answers an app.
func (f *Services) Identity(account Account, serverID, challenge string) trust.Envelope {
	now := f.Now().UTC()
	return f.Sign(trust.Identity{Kind: trust.IdentityKind, AccountID: account.ID, Username: account.Username, DisplayName: account.DisplayName,
		Email: account.Email, ServerID: serverID, Challenge: challenge, Nonce: "n", IssuedAt: now, ExpiresAt: now.Add(5 * time.Minute),
		Pictures: []trust.Picture{}})
}

// MemberLeft records that an account left the server through its Portico Account.
func (f *Services) MemberLeft(serverID, accountID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := f.servers[serverID]
	s.noteRevision = max(s.noteRevision+1, f.Now().Unix())
	s.left = append(s.left, accountID)
	s.gone[accountID] = true
	delete(s.members, accountID)
}

// Unlink is the owner removing the server from their account.
func (f *Services) Unlink(serverID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := f.servers[serverID]
	s.noteRevision = max(s.noteRevision+1, f.Now().Unix())
	s.unlinked = true
}

// Forget drops everything Portico Services holds about a server, as starting again on an
// empty database does. Nothing signed says so: the server just meets "server_unknown".
func (f *Services) Forget(serverID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.servers, serverID)
}

// Note is the note Portico Services has for a server now: what a push, an app and the
// check-in would all carry.
func (f *Services) Note(serverID string) trust.Envelope {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.note(serverID, f.servers[serverID])
}

func (f *Services) note(serverID string, s *server) trust.Envelope {
	now := f.Now().UTC()
	left := append([]string{}, s.left...)
	return f.sign(trust.Note{Kind: trust.NoteKind, ServerID: serverID, Revision: s.noteRevision, Fleet: f.fleet, Left: left,
		Unlinked: s.unlinked, IssuedAt: now, ExpiresAt: now.Add(trust.NoteLifetime)})
}

// Ack takes a server's signed acknowledgement of a pushed note, as Portico Services does:
// only the server's own key can confirm a delivery.
func (f *Services) Ack(serverID string, signed trust.SignedAck) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := f.servers[serverID]
	if s == nil {
		return fmt.Errorf("no such server")
	}
	ack, err := trust.VerifyAck(s.key, serverID, signed)
	if err != nil {
		return err
	}
	f.acknowledge(s, ack.Revision, ack.Fleet)
	return nil
}

func (f *Services) acknowledge(s *server, revision, fleet int64) {
	if revision > s.ackedRevision && revision <= s.noteRevision {
		s.ackedRevision = revision
		if revision == s.noteRevision {
			s.left = nil
		}
	}
	s.fleetAcked = max(s.fleetAcked, min(fleet, f.fleet))
}

// Linked says whether the server has confirmed its link by sending a document, and who
// Portico Services holds as its members.
func (f *Services) Linked(serverID string) (linked bool, members map[string]string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := f.servers[serverID]
	if s == nil {
		return false, nil
	}
	members = map[string]string{}
	for id, role := range s.members {
		members[id] = role
	}
	return s.linked && !s.unlinked, members
}

// Document is the document Portico Services holds for the server, or nil.
func (f *Services) Document(serverID string) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	if s := f.servers[serverID]; s != nil {
		return s.document
	}
	return nil
}

func refuse(w http.ResponseWriter, status int, code string) int {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": code, "message": code, "retry": status >= 500 || status == 429}})
	return status
}

func answer(w http.ResponseWriter, status int, body any) int {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
	return status
}

func (f *Services) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	defer f.mu.Unlock()
	now := f.Now()
	call := Call{Method: r.Method, Path: r.URL.Path, At: now}
	defer func() { f.calls = append(f.calls, call) }()
	if f.down {
		// No answer at all: the connection is dropped.
		if hijacker, ok := w.(http.Hijacker); ok {
			if conn, _, err := hijacker.Hijack(); err == nil {
				conn.Close()
			}
		}
		return
	}
	if f.busy > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(int(f.busy/time.Second)))
		call.Status = refuse(w, http.StatusServiceUnavailable, "busy")
		return
	}
	call.Status = f.route(w, r, body, now)
}

// signedBy checks a request's two headers against a key, as trust.VerifyRequest does for
// the real service, and answers 401 with Date when they do not check out.
func (f *Services) signedBy(w http.ResponseWriter, r *http.Request, key ed25519.PublicKey, body []byte, now time.Time) bool {
	if trust.VerifyRequest(key, r.Method, r.URL.RequestURI(), r.Header.Get(trust.RequestTimeHeader),
		r.Header.Get(trust.RequestSignatureHeader), body, now) != nil {
		w.Header().Set("Date", now.UTC().Format(http.TimeFormat))
		return false
	}
	return true
}

// newServerKey is the check for the two calls that carry their own key: the id must be the
// key's digest and the signature the key's.
func (f *Services) newServerKey(w http.ResponseWriter, r *http.Request, serverID string, body []byte, now time.Time) (ed25519.PublicKey, bool) {
	var in struct {
		PublicKey string `json:"publicKey"`
	}
	_ = json.Unmarshal(body, &in)
	key, err := base64.RawURLEncoding.Strict().DecodeString(in.PublicKey)
	if err != nil || len(key) != ed25519.PublicKeySize {
		return nil, false
	}
	if id, _ := trust.ServerID(key); id != serverID {
		return nil, false
	}
	return key, f.signedBy(w, r, key, body, now)
}

func (f *Services) route(w http.ResponseWriter, r *http.Request, body []byte, now time.Time) int {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	switch {
	case r.Method == "POST" && r.URL.Path == "/v1/approvals":
		var in struct{ ServerID, ServerName, Purpose, ReturnURL string }
		_ = json.Unmarshal(body, &in)
		key, ok := f.newServerKey(w, r, in.ServerID, body, now)
		if !ok {
			return refuse(w, http.StatusUnauthorized, "signature")
		}
		if len(r.Header.Get("Idempotency-Key")) < 16 || in.ServerName == "" || in.ReturnURL == "" ||
			(in.Purpose != trust.PurposeSetup && in.Purpose != trust.PurposeSwitch && in.Purpose != trust.PurposeChangeOwner) {
			return refuse(w, http.StatusUnprocessableEntity, "invalid")
		}
		f.seq++
		a := &approval{id: "apr_" + strconv.Itoa(f.seq), code: fmt.Sprintf("ABCD-%04d", f.seq), serverID: in.ServerID, name: in.ServerName,
			purpose: in.Purpose, key: key, state: "waiting"}
		f.approvals[a.id] = a
		return answer(w, http.StatusCreated, map[string]any{"id": a.id, "code": a.code, "approveUrl": f.URL + "/approve/" + a.id,
			"expiresAt": now.Add(10 * time.Minute).UTC(), "pollAfterMs": 3000})

	case r.Method == "GET" && len(parts) == 3 && parts[1] == "approvals":
		a := f.approvals[parts[2]]
		if a == nil {
			return refuse(w, http.StatusNotFound, "approval_gone")
		}
		if !f.signedBy(w, r, a.key, nil, now) {
			return refuse(w, http.StatusUnauthorized, "signature")
		}
		out := map[string]any{"id": a.id, "state": a.state, "serverId": a.serverID, "serverName": a.name, "purpose": a.purpose, "pollAfterMs": 3000}
		if a.state == "approved" {
			out["approval"] = f.signedApproval(a)
		}
		return answer(w, http.StatusOK, out)

	case r.Method == "POST" && len(parts) == 4 && parts[1] == "servers" && parts[3] == "leave":
		if _, ok := f.newServerKey(w, r, parts[2], body, now); !ok {
			return refuse(w, http.StatusUnauthorized, "signature")
		}
		var in struct {
			LinkIssuedAt time.Time `json:"linkIssuedAt"`
		}
		_ = json.Unmarshal(body, &in)
		leaving, _ := trust.RequestTime(r.Header.Get(trust.RequestTimeHeader))
		if !in.LinkIssuedAt.IsZero() && in.LinkIssuedAt.Before(leaving) {
			leaving = in.LinkIssuedAt
		}
		// Only that link or an older one is removed.
		if s := f.servers[parts[2]]; s != nil && !s.since.After(leaving) {
			delete(f.servers, parts[2])
		}
		return answer(w, http.StatusOK, map[string]bool{"left": true})

	case len(parts) == 4 && parts[1] == "servers" && (parts[3] == "check-in" && r.Method == "POST" || parts[3] == "document" && r.Method == "PUT"):
		s := f.servers[parts[2]]
		if s == nil {
			return refuse(w, http.StatusNotFound, "server_unknown")
		}
		if !f.signedBy(w, r, s.key, body, now) {
			return refuse(w, http.StatusUnauthorized, "signature")
		}
		if signedAt, _ := trust.RequestTime(r.Header.Get(trust.RequestTimeHeader)); signedAt.Before(s.since) {
			w.Header().Set("Date", now.UTC().Format(http.TimeFormat))
			return refuse(w, http.StatusUnauthorized, "signed_before_link")
		}
		if parts[3] == "check-in" {
			return f.checkIn(w, parts[2], s, body, now)
		}
		return f.storeDocument(w, s, body)
	}
	return refuse(w, http.StatusNotFound, "not_found")
}

func (f *Services) checkIn(w http.ResponseWriter, id string, s *server, body []byte, now time.Time) int {
	var in struct {
		DocumentRevision, NoteRevision, Fleet int64
		CertificateRevision                   uint64
	}
	if json.Unmarshal(body, &in) != nil {
		return refuse(w, http.StatusBadRequest, "bad_json")
	}
	f.acknowledge(s, in.NoteRevision, in.Fleet)
	out := map[string]any{"documentWanted": in.DocumentRevision != s.documentRevision, "serverTime": now.UTC()}
	if s.ackedRevision < s.noteRevision || s.fleetAcked < f.fleet {
		out["note"] = f.note(id, s)
	} else if in.CertificateRevision < f.cert.Revision {
		out["certificate"] = f.cert
	}
	return answer(w, http.StatusOK, out)
}

func (f *Services) storeDocument(w http.ResponseWriter, s *server, body []byte) int {
	var in struct {
		Revision       int64
		Name           string
		OwnerAccountID string
		Port           int
		Addresses      []struct {
			Address string
			Port    int
		}
		Members []struct{ AccountID, Role string }
	}
	if json.Unmarshal(body, &in) != nil {
		return refuse(w, http.StatusBadRequest, "bad_json")
	}
	if s.unlinked {
		return refuse(w, http.StatusConflict, "unlinked")
	}
	owners := 0
	for _, m := range in.Members {
		if (m.Role == "owner") != (m.AccountID == in.OwnerAccountID) || (m.Role != "owner" && m.Role != "admin" && m.Role != "member") {
			return refuse(w, http.StatusUnprocessableEntity, "invalid")
		}
		if m.Role == "owner" {
			owners++
		}
	}
	if in.Revision < 1 || in.Name == "" || owners != 1 || in.Addresses == nil || len(in.Addresses) > 8 {
		return refuse(w, http.StatusUnprocessableEntity, "invalid")
	}
	if !s.approvedOwners[in.OwnerAccountID] {
		return refuse(w, http.StatusConflict, "owner_not_approved")
	}
	notMembers := []string{}
	if in.Revision > s.documentRevision {
		s.documentRevision, s.owner, s.linked = in.Revision, in.OwnerAccountID, true
		_ = json.Unmarshal(body, &s.document)
		s.members = map[string]string{}
		for _, m := range in.Members {
			if s.gone[m.AccountID] {
				continue
			}
			s.members[m.AccountID] = m.Role
		}
	}
	for _, m := range in.Members {
		if s.gone[m.AccountID] {
			notMembers = append(notMembers, m.AccountID)
		}
	}
	return answer(w, http.StatusOK, map[string]any{"revision": s.documentRevision, "stored": s.documentRevision == in.Revision, "notMembers": notMembers})
}
