package identity

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/thirdparty/sqlite"
)

// Interpose only at the driver boundary: the normal login, family guards and
// projection still read the real fixture database. No production hooks are needed.
type directReadConnector struct {
	driver.Connector
	beforeQuery func(context.Context, string) error
}

func (c directReadConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.Connector.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return directReadConn{Conn: conn, beforeQuery: c.beforeQuery}, nil
}

type directReadConn struct {
	driver.Conn
	beforeQuery func(context.Context, string) error
}

func (c directReadConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	return c.Conn.(driver.ConnBeginTx).BeginTx(ctx, opts)
}

func (c directReadConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if err := c.beforeQuery(ctx, query); err != nil {
		return nil, err
	}
	return c.Conn.(driver.QueryerContext).QueryContext(ctx, query, args)
}

func directReadService(t *testing.T, s *Service, db *sql.DB, beforeQuery func(context.Context, string) error) *Service {
	t.Helper()
	var seq int
	var name, path string
	if err := db.QueryRow(`PRAGMA database_list`).Scan(&seq, &name, &path); err != nil {
		t.Fatal(err)
	}
	connector, err := sqlite.NewConnector(path)
	if err != nil {
		t.Fatal(err)
	}
	readDB := sql.OpenDB(directReadConnector{connector, beforeQuery})
	t.Cleanup(func() { readDB.Close() })
	readService := *s
	readService.db = readDB
	return &readService
}

func TestDirectMePreservesQueryFailuresAndMissingIdentity(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, db, login := directFixture(t)
	for _, stage := range []string{
		"SELECT f.id,f.server_id",                   // Family token lookup.
		"SELECT d.id FROM identity_device_families", // Live device binding.
		"SELECT device_id FROM identity_device_families",
		"SELECT is_primary FROM direct_profiles",
		"SELECT a.username,a.profile_id",
		"SELECT id,name,is_primary,position", // Projection after authentication.
	} {
		t.Run(stage, func(t *testing.T) {
			for _, cause := range []error{context.DeadlineExceeded, dbwork.ErrReadPressure, errors.New("unexpected read failure"), sql.ErrNoRows} {
				t.Run(cause.Error(), func(t *testing.T) {
					injected := fmt.Errorf("direct read fixture: %w", cause)
					reached := false
					reader := directReadService(t, s, db, func(_ context.Context, query string) error {
						if strings.HasPrefix(query, stage) {
							reached = true
							return injected
						}
						return nil
					})
					out, err := reader.DirectMe(context.Background(), login.AccountToken)
					if !reached {
						t.Fatal("query fault was not exercised")
					}
					want := injected
					if cause == sql.ErrNoRows && !strings.HasPrefix(stage, "SELECT id,name") {
						want = ErrUnauthorized
					}
					if !errors.Is(err, want) || !reflect.DeepEqual(out, DirectSnapshot{}) {
						t.Fatalf("got snapshot=%+v error=%v; want empty snapshot and %v", out, err, want)
					}
				})
			}
		})
	}
}

func TestDirectMeSnapshotLifetimeNormalizesTransactionEnd(t *testing.T) {
	s, db, login := directFixture(t)
	for _, stage := range []string{"SELECT f.id,f.server_id", "SELECT id,name,is_primary,position"} {
		t.Run(stage, func(t *testing.T) {
			reached := false
			reader := directReadService(t, s, db, func(ctx context.Context, query string) error {
				if !strings.HasPrefix(query, stage) {
					return nil
				}
				reached = true
				if _, ok := ctx.Deadline(); !ok || dbwork.Snapshot(ctx) == nil || ctx.Value(factorSetupContextKey{}) != true {
					t.Error("query did not receive the bounded snapshot and factor-setup context")
					return errors.New("missing snapshot context")
				}
				<-ctx.Done()
				// Force the database/sql watchdog race: a statement that follows
				// rollback reports ErrTxDone instead of its snapshot's deadline.
				return fmt.Errorf("expired fixture transaction: %w", sql.ErrTxDone)
			})
			ctx := dbwork.WithClass(context.Background(), dbwork.ClassBackgroundMedia)
			out, err := reader.DirectMe(ctx, login.AccountToken)
			if !reached || !errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrUnauthorized) || !reflect.DeepEqual(out, DirectSnapshot{}) {
				t.Fatalf("reached=%v snapshot=%+v error=%v; want empty snapshot and deadline", reached, out, err)
			}
			if ctx.Err() != nil {
				t.Fatal("snapshot expiry canceled the original request context")
			}
		})
	}
}

func TestDirectMeReadsCommittedStateWhileWriterHeld(t *testing.T) {
	s, db, login := directFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	writer, err := dbwork.Begin(ctx, db, dbwork.ClassInteractive)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Rollback()
	if _, err = writer.Tx().ExecContext(writer.Context(), `UPDATE direct_profiles SET name='Uncommitted' WHERE id=?`, login.Account.PrimaryProfileID); err != nil {
		t.Fatal(err)
	}
	out, err := s.DirectMe(ctx, login.AccountToken)
	if err != nil {
		t.Fatalf("read waited for held writer: %v", err)
	}
	if out.Account.ID != login.Account.ID || !reflect.DeepEqual(out.Profiles, login.Profiles) || out.CanManage != login.CanManage {
		t.Fatalf("read did not retain committed account authority and profiles: %+v", out)
	}
}

func TestDirectMeStillRejectsInvalidAuthority(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change string
	}{
		{"revoked", `UPDATE authorization_session_families SET revoked=1`},
		{"expired", `UPDATE authorization_family_tokens SET expires_at='2000-01-01T00:00:00Z'`},
		{"disabled", `UPDATE direct_memberships SET disabled=1`},
		{"stale epoch", `UPDATE accounts SET epoch=epoch+1`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, db, login := directFixture(t)
			if _, err := db.Exec(tc.change); err != nil {
				t.Fatal(err)
			}
			out, err := s.DirectMe(context.Background(), login.AccountToken)
			if !errors.Is(err, ErrUnauthorized) || !reflect.DeepEqual(out, DirectSnapshot{}) {
				t.Fatalf("invalid authority returned snapshot=%+v error=%v", out, err)
			}
		})
	}
}
