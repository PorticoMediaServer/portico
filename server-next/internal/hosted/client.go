package hosted

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/hosted/trust"
)

// Origin is where Portico Services answers.
const Origin = "https://web.getportico.tv"

// rootPEM is the public half of Portico Services' offline root key
// (deployment/hosted/root-public-key.pem in portico-internal). Everything Portico Services
// signs is checked against it, without calling anyone. It is not a secret.
const rootPEM = `-----BEGIN PUBLIC KEY-----
MCowBQYDK2VwAyEAmD0kQgS+0iEk+ZToKZmpGWNWmoT9Z7EzkIipPtvJ1O0=
-----END PUBLIC KEY-----
`

// Root is the root key this server is built with.
func Root() ed25519.PublicKey {
	if OfficialRoot != "" {
		key, err := linkedRoot()
		if err != nil {
			panic(err)
		}
		return key
	}
	key, err := trust.ParseRoot([]byte(rootPEM))
	if err != nil {
		panic("the built-in Portico Services root key is damaged")
	}
	return key
}

// Options are what a Client needs.
type Options struct {
	// Origin is the base address; empty is the real one.
	Origin string
	HTTP   *http.Client
	// ServerID and Key are the server's identity. The key signs every request, so there is
	// no server password to keep or steal.
	ServerID string
	Key      ed25519.PrivateKey
	// Root is what signed documents are checked against; nil is the built-in root.
	Root ed25519.PublicKey
	Now  func() time.Time
}

// Client is the real Service, and the calls a linked server makes in the background.
type Client struct {
	o Options

	mu sync.Mutex
	// certificate is the newest signing certificate seen. Its list of withdrawn keys is
	// what every later document is checked against.
	certificate *trust.Certificate
	// finished are the approvals already finished, until each runs out: an approval works once.
	finished map[string]time.Time
	// started are the approvals this server asked for and may still ask about.
	started map[string]*started
}

// started is one approval being waited for.
type started struct {
	expires   time.Time
	token     string
	pollAfter time.Duration
	polledAt  time.Time
	// returned is the approval once it has been given.
	returned string
}

func New(o Options) *Client {
	if o.Origin == "" {
		o.Origin = Origin
	}
	if o.HTTP == nil {
		o.HTTP = &http.Client{}
	}
	// A signed request is never sent on to wherever an answer points.
	client := *o.HTTP
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	o.HTTP = &client
	if o.Root == nil {
		o.Root = Root()
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return &Client{o: o, finished: map[string]time.Time{}, started: map[string]*started{}}
}

// Certificate is the newest signing certificate seen, or nil.
func (c *Client) Certificate() *trust.Certificate {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.certificate
}

// Keep takes a signing certificate that came with an answer or was saved before a restart.
// One that the root did not sign, or that is no newer than the one held, changes nothing.
func (c *Client) Keep(seen *trust.Certificate) {
	if seen == nil {
		return
	}
	// The dates are not checked here: a certificate that has run out still names the keys
	// withdrawn up to its day, and no document is accepted on its strength.
	if _, err := seen.Verify(c.o.Root, trust.Documents, seen.NotBefore); err != nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if trust.Newer(c.certificate, seen) {
		c.certificate = seen
	}
}

func (c *Client) verifier() trust.Verifier {
	v := trust.Verifier{Root: c.o.Root}
	if held := c.Certificate(); held != nil {
		v.Withdrawn = held.Revoked
	}
	return v
}

// refusal is Portico Services' error format.
type refusal struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// call sends one signed request. floor is the issue time of the approval that made this
// server's link: a request is never signed with an earlier time, so a server whose clock is
// slow is not taken for a copy of an old request. key is the Idempotency-Key, or "".
func (c *Client) call(ctx context.Context, method, path string, floor time.Time, key string, in, out any) error {
	var body []byte
	if in != nil {
		var err error
		if body, err = json.Marshal(in); err != nil {
			return err
		}
	}
	now := c.o.Now()
	at := now
	if at.Before(floor) {
		at = floor
	}
	req, err := http.NewRequestWithContext(ctx, method, c.o.Origin+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	timeHeader, signature := trust.SignRequest(c.o.Key, method, path, at, body)
	req.Header.Set(trust.RequestTimeHeader, timeHeader)
	req.Header.Set(trust.RequestSignatureHeader, signature)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	resp, err := c.o.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrNotConnected, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return fmt.Errorf("%w: %v", ErrNotConnected, err)
	}
	if resp.StatusCode/100 == 2 {
		if out != nil && json.Unmarshal(raw, out) != nil {
			return fmt.Errorf("%w: an answer that could not be read", ErrNotConnected)
		}
		return nil
	}
	var refused refusal
	_ = json.Unmarshal(raw, &refused)
	code := refused.Error.Code
	switch {
	case resp.StatusCode == http.StatusServiceUnavailable || resp.StatusCode == http.StatusTooManyRequests:
		seconds, _ := strconv.Atoi(resp.Header.Get("Retry-After"))
		return &BusyError{Wait: time.Duration(seconds) * time.Second}
	case resp.StatusCode == http.StatusUnauthorized:
		// The Date header shows how far this server's clock is out.
		if theirs, err := http.ParseTime(resp.Header.Get("Date")); err == nil {
			if off := now.Sub(theirs); off > time.Minute || off < -time.Minute {
				return &ClockError{Off: off}
			}
		}
	case code == "server_unknown" || code == "unlinked":
		return ErrUnknownServer
	case code == "approval_gone":
		return ErrDeclined
	}
	return &RefusedError{Status: resp.StatusCode, Code: code, Detail: string(raw[:min(len(raw), 2000)])}
}

func randomKey() string {
	raw := make([]byte, 16)
	_, _ = rand.Read(raw)
	return hex.EncodeToString(raw)
}

func (c *Client) publicKey() string {
	return base64.RawURLEncoding.EncodeToString(c.o.Key.Public().(ed25519.PublicKey))
}

// startTimeout is how long a person is kept waiting for Portico Services before being told
// it can't be reached.
const startTimeout = 10 * time.Second

func (c *Client) StartApproval(ctx context.Context, req ApprovalRequest) (Approval, error) {
	ctx, cancel := context.WithTimeout(ctx, startTimeout)
	defer cancel()
	in := map[string]string{"serverId": req.ServerID, "publicKey": c.publicKey(), "serverName": req.ServerName,
		"purpose": string(req.Purpose), "returnUrl": req.ReturnURL}
	var out struct {
		ID          string    `json:"id"`
		Code        string    `json:"code"`
		ApproveURL  string    `json:"approveUrl"`
		ExpiresAt   time.Time `json:"expiresAt"`
		PollAfterMs int64     `json:"pollAfterMs"`
	}
	if err := c.call(ctx, "POST", "/v1/approvals", time.Time{}, randomKey(), in, &out); err != nil {
		return Approval{}, err
	}
	if out.ID == "" || out.ApproveURL == "" {
		return Approval{}, fmt.Errorf("%w: an approval with no address", ErrNotConnected)
	}
	approval := Approval{ID: out.ID, URL: out.ApproveURL, Code: out.Code, PollToken: randomKey(), ExpiresAt: out.ExpiresAt,
		PollAfter: max(time.Duration(out.PollAfterMs)*time.Millisecond, time.Second)}
	now := c.o.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	for id, s := range c.started {
		if !now.Before(s.expires) {
			delete(c.started, id)
		}
	}
	c.started[approval.ID] = &started{expires: approval.ExpiresAt, token: approval.PollToken, pollAfter: approval.PollAfter}
	return approval, nil
}

// PollApproval asks only about an approval this server started and that has not run out,
// only for whoever holds the token made with it, and asks Portico Services no more often
// than it said to: a page that asks sooner is told the approval is still waiting. So nobody
// can make this server sign requests about approvals that are not its own, call faster than
// the contract's pace, or collect an approval started by somebody else.
func (c *Client) PollApproval(ctx context.Context, id, pollToken string) (string, error) {
	now := c.o.Now()
	c.mu.Lock()
	s := c.started[id]
	if s == nil || !now.Before(s.expires) || subtle.ConstantTimeCompare([]byte(s.token), []byte(pollToken)) != 1 {
		c.mu.Unlock()
		return "", ErrDeclined
	}
	if s.returned != "" || now.Sub(s.polledAt) < s.pollAfter {
		returned := s.returned
		c.mu.Unlock()
		return returned, nil
	}
	s.polledAt = now
	c.mu.Unlock()

	ctx, cancel := context.WithTimeout(ctx, startTimeout)
	defer cancel()
	var out struct {
		State    string `json:"state"`
		Approval string `json:"approval"`
	}
	if err := c.call(ctx, "GET", "/v1/approvals/"+id, time.Time{}, "", nil, &out); err != nil {
		return "", err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	switch {
	case out.State == "waiting":
		return "", nil
	case out.State == "approved" && out.Approval != "":
		s.returned = out.Approval
		return out.Approval, nil
	}
	delete(c.started, id)
	return "", ErrDeclined
}

func (c *Client) FinishApproval(_ context.Context, serverID, returned string) (Decision, error) {
	envelope, err := trust.ParseReturned(returned)
	if err != nil {
		return Decision{}, ErrDeclined
	}
	// The purpose is read from the approval and then checked with everything else; the
	// caller compares it with the step it is finishing.
	var head struct {
		Purpose string `json:"purpose"`
	}
	if payload, err := base64.RawURLEncoding.DecodeString(envelope.Payload); err != nil || json.Unmarshal(payload, &head) != nil {
		return Decision{}, ErrDeclined
	}
	now := c.o.Now()
	approval, err := c.verifier().OpenApproval(envelope, serverID, head.Purpose, now)
	if err != nil || serverID != c.o.ServerID {
		return Decision{}, ErrDeclined
	}
	c.Keep(envelope.Certificate)
	c.mu.Lock()
	defer c.mu.Unlock()
	for id, until := range c.finished {
		if !now.Before(until) {
			delete(c.finished, id)
		}
	}
	if _, done := c.finished[approval.ApprovalID]; done {
		return Decision{}, ErrDeclined
	}
	c.finished[approval.ApprovalID] = approval.ExpiresAt
	return Decision{Account: Account{ID: approval.AccountID, Username: approval.Username, DisplayName: approval.DisplayName},
		ServerName: approval.ServerName, Purpose: Purpose(approval.Purpose), IssuedAt: approval.IssuedAt}, nil
}

func (c *Client) VerifyIdentity(serverID, challenge, returned string) (Account, []Picture, error) {
	envelope, err := trust.ParseReturned(returned)
	if err != nil || serverID != c.o.ServerID {
		return Account{}, nil, trust.ErrTrust
	}
	identity, err := c.verifier().OpenIdentity(envelope, serverID, challenge, c.o.Now())
	if err != nil {
		return Account{}, nil, err
	}
	c.Keep(envelope.Certificate)
	return Account{ID: identity.AccountID, Username: identity.Username, DisplayName: identity.DisplayName, Email: identity.Email,
			PasswordChecked: identity.PasswordChecked, ProviderChecked: identity.ProviderChecked, Audience: identity.Audience},
		identity.Pictures, nil
}

// OpenNote checks a note addressed to this server. The caller applies it by the revision
// rules and acknowledges it either way.
func (c *Client) OpenNote(envelope trust.Envelope) (trust.Note, error) {
	note, err := c.verifier().OpenNote(envelope, c.o.ServerID, c.o.Now())
	if err != nil {
		return trust.Note{}, err
	}
	c.Keep(envelope.Certificate)
	return note, nil
}

// Ack signs "this server has applied everything up to these numbers".
func (c *Client) Ack(revision, fleet int64) trust.SignedAck {
	ack := trust.Ack{ServerID: c.o.ServerID, Revision: revision, Fleet: fleet}
	if held := c.Certificate(); held != nil {
		ack.CertificateRevision = held.Revision
	}
	return trust.SignAck(c.o.Key, ack)
}

// CheckIn is what a server sends once a day: its numbers.
type CheckIn struct {
	DocumentRevision    int64  `json:"documentRevision"`
	NoteRevision        int64  `json:"noteRevision"`
	Fleet               int64  `json:"fleet"`
	CertificateRevision uint64 `json:"certificateRevision"`
}

// CheckInAnswer is everything Portico Services has for the server, or nothing.
type CheckInAnswer struct {
	// DocumentWanted says Portico Services does not hold the server's document at that revision.
	DocumentWanted bool `json:"documentWanted"`
	// Note is present when the server is behind; the caller opens it with OpenNote.
	Note *trust.Envelope `json:"note,omitempty"`
}

// backgroundTimeout bounds a call nobody is waiting for.
const backgroundTimeout = 30 * time.Second

// CheckIn makes the daily call. since is the issue time of the approval that made the link.
func (c *Client) CheckIn(ctx context.Context, since time.Time, in CheckIn) (CheckInAnswer, error) {
	ctx, cancel := context.WithTimeout(ctx, backgroundTimeout)
	defer cancel()
	if held := c.Certificate(); held != nil {
		in.CertificateRevision = held.Revision
	}
	var out struct {
		CheckInAnswer
		Certificate *trust.Certificate `json:"certificate"`
	}
	if err := c.call(ctx, "POST", "/v1/servers/"+c.o.ServerID+"/check-in", since, "", in, &out); err != nil {
		return CheckInAnswer{}, err
	}
	c.Keep(out.Certificate)
	return out.CheckInAnswer, nil
}

// Member is one Portico Account on this server, as the document names it.
type Member struct {
	AccountID string `json:"accountId"`
	// Role is "owner" for the owner and nobody else, "admin" or "member".
	Role string `json:"role"`
}

// Address is one address the server publishes, and the port it is reached at there.
type Address struct {
	Address string `json:"address"`
	Port    int    `json:"port"`
}

// Document is everything Portico Services knows about this server. It is sent whole when it
// changes.
type Document struct {
	Revision       int64  `json:"revision"`
	Name           string `json:"name"`
	OwnerAccountID string `json:"ownerAccountId"`
	// Addresses are the addresses apps may reach the server at, each with its own port: a
	// home address at the port the server listens on, the public one at the port the router
	// maps. At most 8, in the order apps should try them.
	Addresses []Address `json:"addresses"`
	Members   []Member  `json:"members"`
}

// Stored is Portico Services' answer to a document.
type Stored struct {
	// Revision is the revision Portico Services now holds.
	Revision int64 `json:"revision"`
	// NotMembers are accounts the document named that left through their Portico Account, or
	// no longer exist. The server marks each one Left.
	NotMembers []string `json:"notMembers"`
}

// SendDocument sends the server's document. since is as for CheckIn.
func (c *Client) SendDocument(ctx context.Context, since time.Time, doc Document) (Stored, error) {
	ctx, cancel := context.WithTimeout(ctx, backgroundTimeout)
	defer cancel()
	if doc.Addresses == nil {
		doc.Addresses = []Address{}
	}
	var out Stored
	err := c.call(ctx, "PUT", "/v1/servers/"+c.o.ServerID+"/document", since, "", doc, &out)
	return out, err
}

// Leave says "this server has left" for the link whose approval was issued at linkIssuedAt.
// Portico Services removes only that link or an older one. An answer that the server is
// already unknown is success too.
func (c *Client) Leave(ctx context.Context, linkIssuedAt time.Time) error {
	ctx, cancel := context.WithTimeout(ctx, backgroundTimeout)
	defer cancel()
	in := map[string]any{"publicKey": c.publicKey(), "linkIssuedAt": linkIssuedAt.UTC()}
	err := c.call(ctx, "POST", "/v1/servers/"+c.o.ServerID+"/leave", linkIssuedAt, "", in, nil)
	if errors.Is(err, ErrUnknownServer) {
		return nil
	}
	return err
}
