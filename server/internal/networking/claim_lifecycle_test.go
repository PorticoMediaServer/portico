package networking

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/authoritygate"
)

func fixtureClaimContext(t *testing.T) context.Context {
	t.Helper()
	gate, e := authoritygate.NewAuthorityGate(strings.Repeat("a", 64), false)
	if e != nil {
		t.Fatal(e)
	}
	lease, e := gate.Acquire(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(lease.Close)
	return context.WithValue(lease.Context(), claimAuthorityKey{}, LifecycleLease(lease))
}
func lifecycleRunner(t *testing.T) (*authoritygate.AuthorityGate, *AuthorityRunner) {
	t.Helper()
	gate, e := authoritygate.NewAuthorityGate(strings.Repeat("a", 64), false)
	if e != nil {
		t.Fatal(e)
	}
	runner, e := NewAuthorityRunner(func(ctx context.Context) (LifecycleLease, error) { return gate.Acquire(ctx) })
	if e != nil {
		t.Fatal(e)
	}
	return gate, runner
}
func TestClaimLifecycleUngatedCallsRefused(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f := fixtureCurrentSQL(t)
	if _, e := f.store.Prepare(context.Background(), f.binding, f.owner); !errors.Is(e, ErrUnavailable) {
		t.Fatalf("ungated SQLite mutation allowed: %v", e)
	}
	tr, v, p, _ := httpFixture(t)
	calls := 0
	tr.client.Transport = claimRoundTrip(func(*http.Request) (*http.Response, error) { calls++; return reply(200, exactCommit), nil })
	if _, e := tr.Finalize(context.Background(), v, p); !errors.Is(e, ErrUnavailable) || calls != 0 {
		t.Fatal("ungated HTTP sent")
	}
}
func TestClaimLifecycleLateInstallRejectedBeforeDrain(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f := fixtureCurrentSQL(t)
	v := f.retrieving(t)
	result := resultFor(t, v)
	defer result.Credential.Clear()
	gate, runner := lifecycleRunner(t)
	started, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- runner.Do(context.Background(), func(ctx context.Context) error {
			close(started)
			<-release
			_, e := f.store.Install(ctx, v, result)
			return e
		})
	}()
	<-started
	gate.BeginQuiesce()
	wait, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	if e := gate.WaitDrained(wait); !errors.Is(e, context.DeadlineExceeded) {
		t.Fatal("restore drained a live callback")
	}
	cancel()
	close(release)
	if e := <-done; e == nil {
		t.Fatal("late install reported success")
	}
	if e := gate.WaitDrained(context.Background()); e != nil {
		t.Fatal(e)
	}
	var n int
	if e := f.db.QueryRow(`SELECT count(*) FROM networking_claim_credentials`).Scan(&n); e != nil || n != 0 {
		t.Fatal("late result installed a credential")
	}
	loaded, e := f.store.Load(context.Background(), v.OperationID)
	if e != nil || loaded.Stage != Retrieving {
		t.Fatal("late callback changed durable claim stage")
	}
}

type lifecycleSigner func(context.Context, Binding, []byte) ([]byte, error)

func (s lifecycleSigner) Sign(ctx context.Context, b Binding, raw []byte) ([]byte, error) {
	return s(ctx, b, raw)
}
func TestClaimLifecycleLateSignatureCleared(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f := fixtureCurrentSQL(t)
	gate, runner := lifecycleRunner(t)
	started, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	var produced []byte
	signer, e := NewFencedSigner(lifecycleSigner(func(_ context.Context, _ Binding, raw []byte) ([]byte, error) {
		close(started)
		<-release
		produced = ed25519.Sign(f.localKey, raw)
		return produced, nil
	}))
	if e != nil {
		t.Fatal(e)
	}
	go func() {
		done <- runner.Do(context.Background(), func(ctx context.Context) error {
			sig, e := signer.Sign(ctx, f.binding, []byte("bound-test-payload"))
			if len(sig) != 0 {
				t.Error("late signature escaped")
			}
			return e
		})
	}()
	<-started
	gate.BeginQuiesce()
	close(release)
	if e := <-done; e == nil {
		t.Fatal("late signer succeeded")
	}
	for _, b := range produced {
		if b != 0 {
			t.Fatal("discarded signature was not cleared")
		}
	}
	if e := gate.WaitDrained(context.Background()); e != nil {
		t.Fatal(e)
	}
}
func TestClaimLifecycleLateHTTPResultDiscarded(t *testing.T) {
	gate, runner := lifecycleRunner(t)
	tr, v, p, _ := httpFixture(t)
	started, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	tr.client.Transport = claimRoundTrip(func(*http.Request) (*http.Response, error) {
		close(started)
		<-release
		return reply(200, strings.TrimSuffix(exactCommit, "}")+`,"serverCredential":"`+strings.Repeat("x", 32)+`"}`), nil
	})
	go func() {
		done <- runner.Do(context.Background(), func(ctx context.Context) error {
			result, e := tr.Retrieve(ctx, v, p)
			if result.Credential != nil {
				result.Credential.Clear()
				t.Error("late HTTP credential escaped")
			}
			return e
		})
	}()
	<-started
	gate.BeginQuiesce()
	close(release)
	if e := <-done; e == nil {
		t.Fatal("late HTTP result succeeded")
	}
	if e := gate.WaitDrained(context.Background()); e != nil {
		t.Fatal(e)
	}
}
func TestClaimLifecycleCommitAdmissionFenced(t *testing.T) {
	gate, runner := lifecycleRunner(t)
	called := false
	e := runner.Do(context.Background(), func(ctx context.Context) error {
		gate.BeginQuiesce()
		return commitClaimTx(ctx, func() error { called = true; return nil })
	})
	if e == nil || called {
		t.Fatal("commit admitted after quiesce")
	}
	if e = gate.WaitDrained(context.Background()); e != nil {
		t.Fatal(e)
	}
}

func fixtureCurrentSQL(t *testing.T) *sqlFixture {
	t.Helper()
	dir := t.TempDir()
	if e := os.Chmod(dir, 0700); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(dir, "claims.sqlite")
	db := openFixtureDB(t, path)
	if e := installNetworkingClaimsForTest(t, context.Background(), db); e != nil {
		t.Fatal(e)
	}
	pub, key, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	id, e := ServerIdentity(pub)
	if e != nil {
		t.Fatal(e)
	}
	_, hosted, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`CREATE TABLE fixture_owners(id TEXT PRIMARY KEY,profile TEXT,epoch INTEGER,allowed INTEGER);INSERT INTO fixture_owners VALUES('local_owner','local_profile',0,1);CREATE TABLE fixture_server_authority(server_id TEXT PRIMARY KEY,account_id TEXT,allowed INTEGER)`); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`INSERT INTO fixture_server_authority VALUES(?,'hosted_owner',1)`, id); e != nil {
		t.Fatal(e)
	}
	cipher, e := OpenClaimCipher(context.Background(), db, dir)
	if e != nil {
		t.Fatal(e)
	}
	f := &sqlFixture{db: db, path: path, dir: dir, cipher: cipher, binding: Binding{OperationID: "claim_one", ServerID: id, AccountID: "hosted_owner", PublicKey: pub, LocalGeneration: 0}, owner: LocalOwner{AccountID: "local_owner", ProfileID: "local_profile", Epoch: 0}, localKey: key, hostedKey: hosted, now: time.Date(2026, 9, 6, 1, 0, 0, 0, time.UTC)}
	stageIdentityFixture(t, db, nil, DurableIdentity{ServerID: id, PublicKey: pub, KeyIncarnation: "key1"})
	f.store = f.storeFor(t, db, cipher)
	return f
}
