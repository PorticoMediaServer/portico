package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/apikit/apierror"
	"github.com/PorticoMediaServer/portico/server/internal/access"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

func TestCredentialLookupThrottlesInvalidSecretsBeforeSQL(t *testing.T) {
	f := administrationFixtureNew(t)
	key := issueCredential(t, f, "/v1/admin/api-keys", f.owner, `{"operationId":"credential-admission-1","name":"Script","scopes":["read"]}`)
	if w := f.call("GET", "/v1/me", key.Secret, ""); w.Code != 200 {
		t.Fatalf("valid credential: %d %s", w.Code, w.Body)
	}
	paths := []string{"/v1/me", "/v1/libraries", "/v1/system"}
	prefixes := []string{"pk.", "pat.", "x."}
	for i := 0; i < 128; i++ {
		ctx, measure := dbwork.Measure(context.Background())
		r := httptest.NewRequest("GET", paths[i%len(paths)], nil).WithContext(ctx)
		r.Header.Set("Authorization", fmt.Sprintf("Bearer %sunknown-%d", prefixes[i%len(prefixes)], i))
		r.Header.Set("X-Forwarded-For", fmt.Sprintf("192.0.2.%d", i)) // Untrusted headers cannot rotate the budget.
		w := httptest.NewRecorder()
		f.handler.ServeHTTP(w, r)
		if i < rateAttemptsPerAddress {
			if w.Code != 401 {
				t.Fatalf("invalid attempt %d: %d %s", i, w.Code, w.Body)
			}
		} else {
			if w.Code != 429 || w.Header().Get("Retry-After") == "" {
				t.Fatalf("invalid attempt %d was not throttled: %d %s", i, w.Code, w.Body)
			}
			if n := measure().Statements; n != 0 {
				t.Fatalf("throttled attempt reached SQL: %d statements", n)
			}
		}
	}
	// Known credentials still resolve live after their cache expires, even when
	// bogus secrets from the same address have used up the failure budget.
	f.deps.credentials.mu.Lock()
	for digest, entry := range f.deps.credentials.resolved {
		entry.expires = time.Now().Add(-time.Second)
		f.deps.credentials.resolved[digest] = entry
	}
	f.deps.credentials.mu.Unlock()
	if w := f.call("GET", "/v1/me", key.Secret, ""); w.Code != 200 {
		t.Fatalf("known credential refused: %d %s", w.Code, w.Body)
	}
	r := httptest.NewRequest("GET", "/v1/me", nil)
	r.RemoteAddr = "192.0.2.222:1234"
	r.Header.Set("Authorization", "Bearer pk.some-other-unknown-secret")
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatalf("another address inherited the failure budget: %d", w.Code)
	}
}

func TestCredentialResolutionBoundsConcurrencyAndDeadline(t *testing.T) {
	gate := newCredentialGate()
	started := make(chan struct{}, credentialResolveConcurrency)
	finish := make(chan struct{})
	results := make(chan error, credentialResolveConcurrency)
	lookup := func(ctx context.Context) (access.ResolvedCredential, error) {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > credentialResolveBudget {
			t.Error("lookup has no bounded deadline")
		}
		started <- struct{}{}
		select {
		case <-finish:
		case <-ctx.Done():
			return access.ResolvedCredential{}, ctx.Err()
		}
		return access.ResolvedCredential{}, identity.ErrUnauthorized
	}
	for i := 0; i < credentialResolveConcurrency; i++ {
		go func(i int) {
			_, err := gate.resolve(context.Background(), fmt.Sprint(i), "192.0.2.1", lookup)
			results <- err
		}(i)
	}
	for i := 0; i < credentialResolveConcurrency; i++ {
		<-started
	}
	_, err := gate.resolve(context.Background(), "another", "192.0.2.2", func(context.Context) (access.ResolvedCredential, error) {
		t.Error("excess lookup reached SQL boundary")
		return access.ResolvedCredential{}, nil
	})
	var refusal *apierror.Error
	if !errors.As(err, &refusal) || refusal.Code != "credential_busy" {
		t.Errorf("excess lookup: %v", err)
	}
	close(finish)
	for i := 0; i < credentialResolveConcurrency; i++ {
		if err := <-results; !errors.Is(err, identity.ErrUnauthorized) {
			t.Error(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := gate.resolve(ctx, "cancelled", "192.0.2.1", lookup); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled lookup: %v", err)
	}
}

func TestCredentialResolutionHonorsCallerDeadline(t *testing.T) {
	gate := newCredentialGate()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	_, err := gate.resolve(ctx, "key", "192.0.2.1", func(ctx context.Context) (access.ResolvedCredential, error) {
		<-ctx.Done()
		return access.ResolvedCredential{}, ctx.Err()
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline: %v", err)
	}
	if len(gate.resolving) != 0 {
		t.Fatal("deadline left a resolver slot held")
	}
}
