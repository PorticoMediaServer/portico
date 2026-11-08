package read

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"testing/synctest"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

// Interpose at the application callback boundary, after business rows have
// closed but while its original snapshot is still active. This reproduces the
// Rows.Close commit race without introducing another native opener/wrapper.
type browseBoundarySnapshots struct {
	*governor.Snapshots
	phase                     int
	afterBusiness, afterFence func()
	cancelLookup              bool
}

func (s *browseBoundarySnapshots) Read(ctx context.Context, request governor.Request, budget time.Duration, read func(*governor.ReadScope) error) (governor.SnapshotResult, error) {
	s.phase++
	phase := s.phase
	lookupContext, cancel := context.WithCancel(ctx)
	defer cancel()
	return s.Snapshots.Read(lookupContext, request, budget, func(scope *governor.ReadScope) error {
		err := read(scope)
		if phase == 1 {
			if s.afterBusiness != nil {
				s.afterBusiness()
			}
			if s.cancelLookup {
				cancel()
			}
		} else if phase == 2 && s.afterFence != nil {
			s.afterFence()
		}
		return err
	})
}

func requireNoBrowsePage(t *testing.T, page BrowsePage) {
	t.Helper()
	if !reflect.DeepEqual(page, BrowsePage{}) {
		t.Fatalf("unsuccessful publication leaked a page: %+v", page)
	}
}

func TestBrowsePublicationRejectsMidPageAuthorityChanges(t *testing.T) {
	for _, tt := range []struct {
		name, sql string
		want      error
	}{
		{"revoke", `UPDATE authorization_session_families SET revoked=1 WHERE id='f'`, ErrDenied},
		{"token-retire", `UPDATE authorization_family_tokens SET retired=1 WHERE token_hash='token'`, ErrDenied},
		{"library-retire", `UPDATE catalog_libraries SET retired=1 WHERE id=1`, ErrDenied},
		{"library-remove", `DELETE FROM libraries WHERE id='movies'`, ErrDenied},
		{"membership-remove", `UPDATE direct_memberships SET allowed_libraries='[]' WHERE account_id='a'`, ErrDenied},
		{"profile-access-remove", `UPDATE direct_profiles SET allowed_libraries='[]' WHERE id='p'`, ErrDenied},
		{"library-revision", `UPDATE libraries SET data_revision=data_revision+1 WHERE id='movies'`, ErrStale},
		{"policy-introduced", `INSERT INTO access_limits(account_id,policy_present) VALUES('a',1)`, ErrUnqualified},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := newBrowseFixture(t)
			observed := &browseBoundarySnapshots{Snapshots: f.snapshots, afterBusiness: func() { f.exec(t, tt.sql) }}
			f.reader.snapshots = observed
			page, result, err := f.window(t, t.Context(), f.input)
			if !errors.Is(err, tt.want) || observed.phase != 2 || result.PublicationStatements == 0 {
				t.Fatalf("no fresh publication denial: phase%d result=%+v err=%v want=%v", observed.phase, result, err, tt.want)
			}
			requireNoBrowsePage(t, page)
			if usage := f.output.Authority.Usage()[governor.Foreground]; usage[governor.Reader] != 0 || usage[governor.MemoryBytes] != 8<<20 {
				t.Fatal("native cleanup refunded retained output or kept a reader", usage)
			}
		})
	}
}

func TestBrowsePublicationCancellationNeverReleasesPage(t *testing.T) {
	for _, phase := range []string{"after-business", "after-final-fence", "lookup-lifetime"} {
		t.Run(phase, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				f := newBrowseFixture(t)
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				observed := &browseBoundarySnapshots{Snapshots: f.snapshots}
				switch phase {
				case "after-business":
					observed.afterBusiness = cancel
				case "after-final-fence":
					observed.afterFence = cancel
				case "lookup-lifetime":
					observed.cancelLookup = true
				}
				f.reader.snapshots = observed
				page, _, err := f.window(t, ctx, f.input)
				if !errors.Is(err, context.Canceled) {
					t.Fatal("cancellation was lost at publication", err)
				}
				requireNoBrowsePage(t, page)
				// A cancelled phase cannot poison a later independently authorized read.
				f.reader.snapshots = f.snapshots
				if _, _, err := f.window(t, t.Context(), f.input); err != nil {
					t.Fatal("snapshot was not reusable after cancellation", err)
				}
			})
		})
	}
}

func TestBrowsePublicationDenialWinsOverCancellation(t *testing.T) {
	// Preserve the configured one-second/cleanup limits while isolating the ordering
	// assertion from instrumentation and canonical Reader startup CPU time.
	synctest.Test(t, testBrowsePublicationDenialWinsOverCancellation)
}

func testBrowsePublicationDenialWinsOverCancellation(t *testing.T) {
	f := newBrowseFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	f.reader.snapshots = &browseBoundarySnapshots{Snapshots: f.snapshots, afterBusiness: func() {
		f.exec(t, `UPDATE authorization_session_families SET revoked=1 WHERE id='f'`)
		cancel()
	}}
	page, result, err := f.window(t, ctx, f.input)
	if result.PublicationStatements != 2 {
		t.Fatalf("fresh authority/principal fence did not run: %+v, err=%v", result, err)
	}
	if !errors.Is(err, ErrDenied) {
		t.Fatal("current denial did not win over cancellation", err)
	}
	requireNoBrowsePage(t, page)
}

func TestBrowsePublicationRequiresOutputCustody(t *testing.T) {
	f := newBrowseFixture(t)
	f.output.Grant = nil
	page, _, err := f.window(t, t.Context(), f.input)
	if !errors.Is(err, ErrInput) {
		t.Fatal("unreserved output accepted", err)
	}
	requireNoBrowsePage(t, page)
}

func TestBrowsePublicationRejectsUnversionedPolicyDrift(t *testing.T) {
	f := newBrowseFixture(t)
	f.exec(t, `INSERT INTO profile_restrictions(profile_id,maximum_age) VALUES('p',18)`)
	f.reader.snapshots = &browseBoundarySnapshots{Snapshots: f.snapshots, afterBusiness: func() {
		f.exec(t, `UPDATE profile_restrictions SET maximum_age=12 WHERE profile_id='p'`)
	}}
	page, _, err := f.window(t, t.Context(), f.input)
	if !errors.Is(err, ErrDenied) {
		t.Fatal("unversioned item-policy change released old results", err)
	}
	requireNoBrowsePage(t, page)
}

func TestBrowsePublicationChecksExpiryAfterFinalRead(t *testing.T) {
	synctest.Test(t, testBrowsePublicationChecksExpiryAfterFinalRead)
}

func testBrowsePublicationChecksExpiryAfterFinalRead(t *testing.T) {
	f := newBrowseFixture(t)
	f.exec(t, `UPDATE authorization_family_tokens SET expires_at=? WHERE token_hash='token'`, f.input.Now.Add(30*time.Millisecond).Format(time.RFC3339Nano))
	wait, cancel := context.WithTimeout(t.Context(), 60*time.Millisecond)
	defer cancel()
	f.reader.snapshots = &browseBoundarySnapshots{Snapshots: f.snapshots, afterFence: func() { <-wait.Done() }}
	page, _, err := f.window(t, t.Context(), f.input)
	if !errors.Is(err, ErrDenied) {
		t.Fatal("token expiry during publication cleanup was ignored", err)
	}
	requireNoBrowsePage(t, page)
}

func TestBrowsePublicationProtectedDeadlineNeverReleasesPage(t *testing.T) {
	synctest.Test(t, testBrowsePublicationProtectedDeadlineNeverReleasesPage)
}

func testBrowsePublicationProtectedDeadlineNeverReleasesPage(t *testing.T) {
	f := newBrowseFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	f.reader.snapshots = &browseBoundarySnapshots{Snapshots: f.snapshots, afterBusiness: func() { <-ctx.Done() }}
	page, result, err := f.window(t, ctx, f.input)
	if !errors.Is(err, context.DeadlineExceeded) || result.PublicationStatements != 0 {
		t.Fatal("protected expiry continued into publication", err, result)
	}
	requireNoBrowsePage(t, page)
}

func TestBrowsePublicationRechecksOutputGrant(t *testing.T) {
	f := newBrowseFixture(t)
	f.reader.snapshots = &browseBoundarySnapshots{Snapshots: f.snapshots, afterFence: f.output.Authority.EnterStatic}
	page, _, err := f.window(t, t.Context(), f.input)
	if !errors.Is(err, governor.ErrGrant) {
		t.Fatal("fenced output grant released a page", err)
	}
	requireNoBrowsePage(t, page)
}
