package playback

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

func TestGrantResolutionHonorsRequestCancellationWithoutRevokingGrant(t *testing.T) {
	_, s, p, _, item, _ := activityFixture(t)
	session, err := s.Create(p, item, "automatic", "grant-context")
	if err != nil {
		t.Fatal(err)
	}
	grant := strings.TrimPrefix(session.StreamURL, "/v1/media/")
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, resolve := range []func(context.Context, string) (string, identity.Principal, string, error){s.ResolveGrantContext, s.ResolveDecodeGrantContext} {
		if _, _, _, err = resolve(cancelled, grant); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled grant lookup: %v", err)
		}
	}
	if _, _, _, err = s.ResolveGrant(grant); err != nil {
		t.Fatalf("cancelled request revoked live grant: %v", err)
	}
	// Cancellation must also reach continuation checks after the snapshot opens,
	// without being misreported as presentation_ended (which prompts a replan).
	s.ContinueAuthorityTx = func(ctx context.Context, tx *sql.Tx, p identity.Principal, item string) (identity.Principal, error) {
		<-ctx.Done()
		return p, ctx.Err()
	}
	timed, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, _, _, err = s.ResolveGrantContext(timed, grant); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("continuation ignored/misreported deadline: %v", err)
	}
	s.ContinueAuthorityTx = nil
	if _, _, _, err = s.ResolveGrantContext(context.Background(), grant); err != nil {
		t.Fatalf("timed out request ended live grant: %v", err)
	}
}

func TestProgressAbandonsWriterQueueAtRequestDeadline(t *testing.T) {
	_, s, p, _, item, _ := activityFixture(t)
	session, err := s.Create(p, item, "automatic", "progress-context")
	if err != nil {
		t.Fatal(err)
	}
	var originalSequence int64
	if err = s.db.QueryRow(`SELECT sequence FROM playback_sessions WHERE id=?`, session.ID).Scan(&originalSequence); err != nil {
		t.Fatal(err)
	}
	release, err := dbwork.WriteGate().Acquire(context.Background(), dbwork.ClassInteractive)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- s.ProgressContext(ctx, p, session.ID, session.Generation, 1, 12, "playing") }()
	select {
	case err = <-result:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("report lost deadline: %v", err)
		}
	case <-time.After(time.Second):
		release()
		<-result
		t.Fatal("report still queued after request expired")
	}
	release()
	current, err := s.Get(p, session.ID)
	if err != nil || current.State == "ended" {
		t.Fatalf("request cancellation stopped live playback: %+v %v", current, err)
	}
	var sequence int64
	if err = s.db.QueryRow(`SELECT sequence FROM playback_sessions WHERE id=?`, session.ID).Scan(&sequence); err != nil || sequence != originalSequence {
		t.Fatalf("cancelled report executed later: seq=%d err=%v", sequence, err)
	}
}
