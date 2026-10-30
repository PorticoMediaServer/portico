package hosted

import (
	"context"
	"crypto/ed25519"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	trust "github.com/PorticoMediaServer/portico/server/internal/hostedtrust"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

type Member struct {
	AccountID         string   `json:"accountId"`
	ProfileID         string   `json:"profileId"`
	Role              string   `json:"role"`
	AllowedLibraries  []string `json:"allowedLibraries"`
	ProfileName       string   `json:"profileName,omitempty"`
	Username          string   `json:"username,omitempty"`
	PINRevision       int64    `json:"pinRevision,omitempty"`
	ProfileRevision   int64    `json:"profileRevision,omitempty"`
	TrustRevision     int64    `json:"trustRevision,omitempty"`
	AccountEpoch      int64    `json:"accountEpoch,omitempty"`
	AllLibraries      bool     `json:"allLibraries,omitempty"`
	LibraryRestricted bool     `json:"libraryRestricted,omitempty"`
}
type ProfileErasure struct {
	ID        string `json:"id"`
	AccountID string `json:"accountId"`
	ProfileID string `json:"profileId"`
	Revision  int64  `json:"revision"`
}
type Policy struct {
	OperationID          string           `json:"operationId"`
	ClaimGeneration      string           `json:"claimGeneration"`
	CredentialGeneration string           `json:"credentialGeneration"`
	ErasurePending       bool             `json:"erasurePending,omitempty"`
	ServerID             string           `json:"serverId"`
	Revision             int64            `json:"revision"`
	IssuedAt             string           `json:"issuedAt"`
	ExpiresAt            string           `json:"expiresAt"`
	Members              []Member         `json:"members"`
	ProfileErasures      []ProfileErasure `json:"profileErasures,omitempty"`
}
type Signed = trust.Envelope
type Service struct {
	reachMu     sync.Mutex
	reachUntil  time.Time
	reachResult ServicesReachability
	db          *sql.DB
	identity    *identity.Service
	origin      string
	key         ed25519.PublicKey
	keyID       string
	client      *http.Client
	current     *currentClaims
	// wake is "something changed, look again". It is what replaced a ten-second
	// ticker that asked the database whether the outbox was empty whether or not
	// anything had been written to it; an idle server now makes no query at all
	// between one due deadline and the next.
	wake chan struct{}

	// The server name Hosted last saw (A61): bootName at start, then each queued rename.
	nameMu        sync.Mutex
	bootName      string
	publishedName *string
}

// wakeControl asks the control loop to look again without forcing a call to
// Hosted. Signalling is what a write to the outbox does instead of leaving the
// loop to discover it on a timer.
func (s *Service) wakeControl() {
	if s == nil || s.wake == nil {
		return
	}
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func New(db *sql.DB, id *identity.Service, origin, pin, keyID string) (*Service, error) {
	key, e := base64.RawURLEncoding.DecodeString(pin)
	if pin != "" && (e != nil || len(key) != ed25519.PublicKeySize) {
		return nil, errors.New("invalid pinned hosted key")
	}
	// A15: the root ID must name the pinned key.
	if pin != "" && keyID != trust.KeyID(key) {
		return nil, errors.New("hosted root ID does not match the pinned key")
	}
	if origin != "" {
		if e = validateOrigin(origin); e != nil {
			return nil, e
		}
		u, _ := url.Parse(origin)
		if trust.IsDevelopmentRoot(key) && strings.EqualFold(u.Hostname(), "web.getportico.tv") {
			return nil, errors.New("development root cannot use production Hosted origin")
		}
	}
	svc := &Service{db: db, identity: id, origin: strings.TrimRight(origin, "/"), key: key, keyID: keyID, wake: make(chan struct{}, 1), client: &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	if id != nil {
		svc.bootName = id.Name()
	}
	return svc, nil
}
func validateOrigin(raw string) error {
	u, e := url.Parse(raw)
	if e != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" {
		return errors.New("invalid hosted origin")
	}
	if u.Scheme == "https" {
		return nil
	}
	ip := net.ParseIP(u.Hostname())
	if u.Scheme == "http" && ip != nil && ip.IsLoopback() {
		return nil
	}
	return errors.New("hosted origin requires HTTPS or explicit loopback HTTP")
}
func (s *Service) Configured() bool { return s.origin != "" && len(s.key) == ed25519.PublicKeySize }

type policyReader interface{ QueryRow(string, ...any) *sql.Row }

func (s *Service) load() (Policy, error) { return s.loadFrom(s.db) }
func (s *Service) loadFrom(q policyReader) (Policy, error) {
	var raw, expires string
	var p Policy
	query := `SELECT payload,expires_at FROM policy WHERE server_id=?`
	if s.current != nil {
		query = `SELECT p.payload,p.expires_at FROM policy p JOIN networking_claim_identity ci ON ci.singleton=1 AND ci.server_id=p.server_id JOIN networking_claim_intents i ON i.operation_id=ci.installed_operation_id AND i.operation_id=ci.active_operation_id AND i.stage='installed' AND i.installation_acknowledged=1 AND i.server_id=ci.server_id AND i.public_key=ci.public_key AND i.local_generation=ci.reset_generation WHERE p.server_id=? AND json_extract(p.payload,'$.operationId')=i.operation_id AND json_extract(p.payload,'$.claimGeneration')=i.claim_generation AND json_extract(p.payload,'$.credentialGeneration')=i.credential_generation AND COALESCE(json_extract(p.payload,'$.erasurePending'),0)=0 AND NOT EXISTS(SELECT 1 FROM networking_claim_authority a WHERE a.operation_id=i.operation_id AND a.state<>'active')`
	}
	e := q.QueryRow(query, s.identity.ID()).Scan(&raw, &expires)
	if e != nil {
		return p, identity.ErrUnauthorized
	}
	expiry, parseErr := time.Parse(time.RFC3339Nano, expires)
	if parseErr != nil || !expiry.After(time.Now()) {
		return p, identity.ErrUnauthorized
	}
	e = json.Unmarshal([]byte(raw), &p)
	return p, e
}
func (s *Service) Allowed(principal identity.Principal, library string) error {
	return s.allowed(principal, library, s.db)
}
func (s *Service) AllowedTx(principal identity.Principal, library string, tx *sql.Tx) error {
	return s.allowed(principal, library, tx)
}
func (s *Service) allowed(principal identity.Principal, library string, q policyReader) error {
	if principal.Authority == "local" {
		return identity.DirectAllowed(q, principal, library)
	}
	p, e := s.loadFrom(q)
	if e != nil {
		return e
	}
	var revoked int
	var restricted string
	e = q.QueryRow(`SELECT revoked,allowed_libraries FROM restrictions WHERE profile_id=?`, principal.ProfileID).Scan(&revoked, &restricted)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return e
	}
	if revoked != 0 {
		return identity.ErrUnauthorized
	}
	if library != "" && restricted != "" {
		var allowed []string
		if json.Unmarshal([]byte(restricted), &allowed) != nil || !contains(allowed, library) {
			return identity.ErrNotVisible
		}
	}
	member := false
	for _, m := range p.Members {
		if m.AccountID == principal.AccountID && m.ProfileID == principal.ProfileID {
			member = true
			if library == "" || ((m.Role == "owner" || m.AllLibraries) && !m.LibraryRestricted) || contains(m.AllowedLibraries, library) {
				return nil
			}
		}
	}
	if member {
		// A current member asking for a library it is not given: hidden, and
		// its session stays valid.
		return identity.ErrNotVisible
	}
	return identity.ErrUnauthorized
}
func contains(v []string, s string) bool {
	for _, x := range v {
		if x == s {
			return true
		}
	}
	return false
}

// ServicesReachability is a bounded liveness observation, not account or claim
// authority. Configuration alone never means Portico Services answered.
type ServicesReachability struct {
	Configured bool   `json:"configured"`
	Reachable  bool   `json:"reachable"`
	CheckedAt  string `json:"checkedAt,omitempty"`
}

func (s *Service) ServicesReachability(ctx context.Context) ServicesReachability {
	if s == nil || !s.Configured() {
		return ServicesReachability{}
	}
	if s.identity != nil {
		kind, e := s.identity.SignInKind(ctx)
		if e != nil || kind == identity.SignInLocal {
			return ServicesReachability{}
		}
	}
	s.reachMu.Lock()
	defer s.reachMu.Unlock()
	if time.Now().Before(s.reachUntil) {
		return s.reachResult
	}
	out := ServicesReachability{Configured: true, CheckedAt: time.Now().UTC().Format(time.RFC3339)}
	bounded, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, e := http.NewRequestWithContext(bounded, http.MethodGet, s.origin+"/health", nil)
	if e == nil {
		req.Header.Set("Accept", "application/json")
		client := *s.client
		client.Jar = nil
		client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		if response, err := client.Do(req); err == nil {
			if response.StatusCode == http.StatusOK {
				var health struct {
					Alive bool `json:"alive"`
				}
				raw, err := io.ReadAll(io.LimitReader(response.Body, 257))
				out.Reachable = err == nil && len(raw) <= 256 && json.Unmarshal(raw, &health) == nil && health.Alive
			}
			response.Body.Close()
		}
	}
	if ctx.Err() == nil {
		s.reachResult = out
		s.reachUntil = time.Now().Add(30 * time.Second)
	}
	return out
}
