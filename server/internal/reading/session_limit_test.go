package reading

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

func sessionTestPrincipal(profile string) identity.Principal {
	return identity.Principal{
		Viewer:    identity.Viewer{Authority: "local", AccountID: "account", ProfileID: profile, ServerID: "server", Role: "owner"},
		Hash:      "token-" + profile,
		ExpiresAt: time.Now().Add(time.Hour),
	}
}

// newSessionTestService uses the real baseline schema for the captured
// family/token lookup; sessions themselves stay in memory.
func newSessionTestService(t *testing.T, principals ...identity.Principal) *Service {
	t.Helper()
	c := catalogtest.Open(t)
	s := &Service{DB: c.DB}
	for i, p := range principals {
		family := fmt.Sprintf("family-%d", i)
		c.Exec(`INSERT INTO authorization_session_families(id,server_id,account_id,profile_id,authority,role,epoch,authorization_horizon,current_generation) VALUES(?,?,?,?,?,?,0,'2099-01-01T00:00:00Z',1)`, family, p.ServerID, p.AccountID, p.ProfileID, p.Authority, p.Role)
		c.Exec(`INSERT INTO authorization_family_tokens(token_hash,family_id,generation,expires_at) VALUES(?,?,1,'2099-01-01T00:00:00Z')`, p.Hash, family)
	}
	return s
}

func openSessionForTest(t *testing.T, s *Service, p identity.Principal, item string) *Session {
	t.Helper()
	session, err := s.captureSessionState(p, &Source{ctx: context.Background(), Selection: Selection{ItemID: item, SourceID: "source-" + item, Revision: "revision-" + item}})
	if err != nil {
		t.Fatalf("open %s: %v", item, err)
	}
	return session
}

func TestNinthSessionEvictsLeastRecentlyUsed(t *testing.T) {
	p := sessionTestPrincipal("reader")
	s := newSessionTestService(t, p)
	base := time.Now().Add(-time.Hour)
	sessions := make([]*Session, 8)
	for i := range sessions {
		sessions[i] = openSessionForTest(t, s, p, fmt.Sprintf("item-%d", i))
		sessions[i].lastUsed = base.Add(time.Duration(i) * time.Minute)
	}
	// Reading the oldest session makes it the most recently used one.
	if !sessions[0].touch() {
		t.Fatal("live session did not touch")
	}
	ninth := openSessionForTest(t, s, p, "item-8")
	if !sessions[1].closed {
		t.Fatal("the second-oldest session was not evicted")
	}
	if sessions[0].closed {
		t.Fatal("the just-read session was evicted")
	}
	if _, ok := s.sessions[sessions[1].ID]; ok {
		t.Fatal("evicted session remained in the live map")
	}
	if _, ok := s.sessions[ninth.ID]; !ok {
		t.Fatal("ninth session was not retained")
	}
	if len(s.sessions) != 8 {
		t.Fatalf("live sessions = %d, want 8", len(s.sessions))
	}
	for _, i := range []int{0, 2, 3, 4, 5, 6, 7} {
		if sessions[i].closed {
			t.Fatalf("session %d was evicted", i)
		}
		if _, ok := s.sessions[sessions[i].ID]; !ok {
			t.Fatalf("session %d left the live map", i)
		}
	}
}

func TestSessionEvictionKeepsOtherProfiles(t *testing.T) {
	a, b := sessionTestPrincipal("a"), sessionTestPrincipal("b")
	s := newSessionTestService(t, a, b)
	base := time.Now().Add(-time.Hour)
	for i := 0; i < 8; i++ {
		session := openSessionForTest(t, s, a, fmt.Sprintf("a-item-%d", i))
		session.lastUsed = base.Add(time.Duration(i) * time.Minute)
	}
	other := openSessionForTest(t, s, b, "b-item")
	openSessionForTest(t, s, a, "a-item-8")
	if other.closed {
		t.Fatal("another profile's session was evicted")
	}
	if _, ok := s.sessions[other.ID]; !ok {
		t.Fatal("another profile's session left the live map")
	}
}

func TestEvictedSessionReadsFailAsClosed(t *testing.T) {
	p := sessionTestPrincipal("reader")
	s := newSessionTestService(t, p)
	base := time.Now().Add(-time.Hour)
	sessions := make([]*Session, 8)
	for i := range sessions {
		sessions[i] = openSessionForTest(t, s, p, fmt.Sprintf("item-%d", i))
		sessions[i].lastUsed = base.Add(time.Duration(i) * time.Minute)
	}
	openSessionForTest(t, s, p, "item-8")
	evicted := sessions[0]
	if !evicted.closed {
		t.Fatal("oldest session was not closed")
	}
	if _, err := s.findSession(p, evicted.ID); !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("evicted session lookup = %v, want session expired", err)
	}
	if _, err := s.OpenSession(context.Background(), p, evicted.ID); !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("evicted session open = %v, want session expired", err)
	}
	// An explicit close answers exactly the same way.
	explicit := sessions[1]
	if err := s.CloseSession(p, explicit.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.findSession(p, explicit.ID); !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("closed session lookup = %v, want session expired", err)
	}
}

func TestGlobalSessionCapReturnsBusy(t *testing.T) {
	p := sessionTestPrincipal("new")
	s := newSessionTestService(t, p)
	s.sessions = map[string]*Session{}
	for i := 0; i < 128; i++ {
		life, cancel := context.WithCancel(context.Background())
		id := fmt.Sprintf("session-%03d", i)
		t.Cleanup(cancel)
		s.sessions[id] = &Session{ID: id, owner: fmt.Sprintf("owner-%d", i), life: life, cancel: cancel}
	}
	_, err := s.captureSessionState(p, &Source{ctx: context.Background(), Selection: Selection{ItemID: "item", SourceID: "source"}})
	if !errors.Is(err, ErrSessionsBusy) {
		t.Fatalf("open at the global cap = %v, want ErrSessionsBusy", err)
	}
	if errors.Is(err, ErrLimit) {
		t.Fatal("the global session cap still reports the publication safety limit")
	}
	if len(s.sessions) != 128 {
		t.Fatalf("live sessions = %d, want 128", len(s.sessions))
	}
	for id, session := range s.sessions {
		if session.closed {
			t.Fatalf("session %s was closed for a refused open", id)
		}
	}
}

func TestIdleClosedSessionExpiresOnlyForItsOwner(t *testing.T) {
	p, other := sessionTestPrincipal("reader"), sessionTestPrincipal("other")
	s := newSessionTestService(t, p, other)
	t.Cleanup(func() { _ = s.Close() })
	session := openSessionForTest(t, s, p, "item")
	// The idle timer uses close; no wall-clock wait is needed to exercise it.
	session.close()
	if _, err := s.findSession(other, session.ID); !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("other profile's closed session = %v, want expired", err)
	}
	if _, err := s.findSession(p, session.ID); !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("owned idle-closed session = %v, want session expired", err)
	}
}

func TestForeignSessionMatchesUnknownForSameOrDifferentToken(t *testing.T) {
	p := sessionTestPrincipal("reader")
	s := newSessionTestService(t, p)
	t.Cleanup(func() { _ = s.Close() })
	session := openSessionForTest(t, s, p, "item")
	for _, other := range []identity.Principal{sessionTestPrincipal("other"), p} {
		if other.Viewer.ProfileID == p.Viewer.ProfileID {
			other.Hash = "another-token"
		} else {
			other.Hash = p.Hash
		}
		_, unknown := s.findSession(other, "missing-session")
		_, foreign := s.findSession(other, session.ID)
		if !errors.Is(unknown, ErrSessionExpired) || foreign != unknown {
			t.Fatalf("foreign = %v, unknown = %v", foreign, unknown)
		}
	}
}
