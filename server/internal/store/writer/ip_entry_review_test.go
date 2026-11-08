package writer

import (
	"context"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
)

type ipEntryReviewCloseConn struct {
	writerDriverConn
	fired   *bool
	hookErr *error
}

func (c *ipEntryReviewCloseConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	rows, err := c.writerDriverConn.QueryContext(ctx, query, args)
	if err != nil || !strings.Contains(query, "FROM metadata_edit_receipts") {
		return rows, err
	}
	return &receiptCloseRows{Rows: rows, hook: func() {
		if *c.fired {
			return
		}
		*c.fired = true
		_, *c.hookErr = c.writerDriverConn.ExecContext(ctx, `UPDATE authorization_session_families SET revoked=1 WHERE id='issuer-family'`, nil)
	}}, nil
}
func TestIPEntryFinalRecheckAfterReceipt(t *testing.T) {
	for _, mode := range []string{"fresh", "replay", "retired"} {
		t.Run(mode, func(t *testing.T) {
			w, r, p, q := ipEntryFixture(t, false)
			if mode != "fresh" {
				seedIPEntryHistory(t, w, q, mode == "retired")
			}
			var sequence int
			var name, path string
			if err := w.db.QueryRowContext(t.Context(), `PRAGMA database_list`).Scan(&sequence, &name, &path); err != nil {
				t.Fatal(err)
			}
			if err := w.db.Close(); err != nil {
				t.Fatal(err)
			}
			connector, err := sqliteconn.Connector(path, w.config, sqliteconn.ExistingFile)
			if err != nil {
				t.Fatal(err)
			}
			fired := false
			var hookErr error
			w.openPool(connector, func(conn driver.Conn) driver.Conn {
				return &ipEntryReviewCloseConn{writerDriverConn: conn.(writerDriverConn), fired: &fired, hookErr: &hookErr}
			})
			turn, b := ipEntryTurn(t, w, r, q)
			out, err := enterIPCommand(t.Context(), turn, q, p, t.Context(), b)
			if !fired || hookErr != nil {
				t.Fatal("native receipt-close revocation did not run", hookErr)
			}
			if out != nil || !errors.Is(err, ErrDisclosureDenied) {
				t.Fatal("receipt released after same-turn revocation", out, err)
			}
		})
	}
}
func TestIPEntryScopeAndOwnerKind(t *testing.T) {
	for _, mode := range []string{"library_key", "subject_kind", "owner_kind", "person_owner_kind"} {
		t.Run(mode, func(t *testing.T) {
			w, r, p, q := ipEntryFixture(t, mode == "person_owner_kind")
			switch mode {
			case "library_key":
				q.libraryKey = "foreign-library"
			case "subject_kind":
				q.targetKind = 2
			default:
				seedIPEntryHistory(t, w, q, true)
				// Keep the current target/scope and original receipt identity untouched.
				// A corrupt retained kind must deny even below the ACK floor.
				wc008NativeExec(t, w, `PRAGMA ignore_check_constraints=ON; UPDATE metadata_edit_owners SET kind='show'; PRAGMA ignore_check_constraints=OFF`)
			}
			turn, b := ipEntryTurn(t, w, r, q)
			out, err := enterIPCommand(t.Context(), turn, q, p, t.Context(), b)
			want := ErrDisclosureDenied
			if strings.Contains(mode, "owner_kind") {
				want = ErrConflict
			}
			if out != nil || !errors.Is(err, want) {
				t.Fatal("foreign scope/kind admitted", out, err)
			}
		})
	}
}
func TestIPEntryInactiveOwnerCannotAdmitNext(t *testing.T) {
	w, r, p, q := ipEntryFixture(t, false)
	seedIPEntryHistory(t, w, q, true)
	wc008NativeExec(t, w, `UPDATE metadata_edit_owners SET retired_ms=1`)
	q.reference.Identity.Sequence = 2
	q.reference.Identity.OperationID = [16]byte{5}
	turn, b := ipEntryTurn(t, w, r, q)
	out, err := enterIPCommand(t.Context(), turn, q, p, t.Context(), b)
	if out != nil || !errors.Is(err, ErrConflict) {
		t.Fatal("retired owner admitted fresh next sequence", out, err)
	}
}
func TestIPEntryFreshSubjectRetirement(t *testing.T) {
	for _, table := range []string{"catalog_entities", "catalog_libraries"} {
		t.Run(table, func(t *testing.T) {
			w, r, p, q := ipEntryFixture(t, false)
			turn, b := ipEntryTurn(t, w, r, q)
			entry, err := enterIPCommand(t.Context(), turn, q, p, t.Context(), b)
			if err != nil {
				t.Fatal(err)
			}
			if err = entry.checkFreshSubject(t.Context(), turn, [16]byte{4}, 1, 1, 1, ""); err != nil {
				t.Fatal("live positive control", err)
			}
			mutation := `UPDATE catalog_entities SET retired=1 WHERE id=778`
			if table == "catalog_libraries" {
				mutation = `UPDATE catalog_libraries SET retired=1 WHERE id=777`
			}
			if _, err = turn.transaction.conn.ExecContext(t.Context(), mutation); err != nil {
				t.Fatal(err)
			}
			if err = entry.checkFreshSubject(t.Context(), turn, [16]byte{4}, 1, 1, 1, ""); !errors.Is(err, ErrDisclosureDenied) {
				t.Fatal("retired source admitted", err)
			}
		})
	}
}
func ipEntryReviewProviderFixture(t *testing.T, w *executor) {
	t.Helper()
	for _, query := range []string{
		`INSERT INTO metadata_providers(code,adapter,enabled) VALUES('tmdb','tmdb',1),('musicbrainz','musicbrainz',1)`,
		`INSERT INTO metadata_provider_policy(library_id,provider,incarnation,revision,enabled,priority,region,algorithm,refresh_mode) VALUES(777,'tmdb',X'09000000000000000000000000000000',1,1,1,'US','v1','fill_missing'),(777,'musicbrainz',X'09000000000000000000000000000000',1,1,1,'US','v1','fill_missing')`,
		`INSERT INTO screen_metadata_consent(singleton,revision,confirmed) VALUES(1,1,1) ON CONFLICT(singleton) DO UPDATE SET revision=1,confirmed=1`,
		`INSERT INTO metadata_runtime_settings(singleton,online_lookups,lookup_epoch) VALUES(1,1,3) ON CONFLICT(singleton) DO UPDATE SET online_lookups=1,lookup_epoch=3`,
	} {
		wc008NativeExec(t, w, query)
	}
}
func TestIPEntryProviderEnablementAndOnlineConsent(t *testing.T) {
	for _, mode := range []string{"policy_disabled", "provider_disabled", "online_disabled"} {
		t.Run(mode, func(t *testing.T) {
			w, r, p, q := ipEntryFixture(t, false)
			ipEntryReviewProviderFixture(t, w)
			turn, b := ipEntryTurn(t, w, r, q)
			entry, err := enterIPCommand(t.Context(), turn, q, p, t.Context(), b)
			if err != nil {
				t.Fatal(err)
			}
			provider, consent := "tmdb", int64(1)
			if mode == "online_disabled" {
				provider, consent = "musicbrainz", 3
			}
			if err = entry.checkFreshProvider(t.Context(), turn, provider, [16]byte{9}, 1, consent); err != nil {
				t.Fatal("enabled positive control", err)
			}
			mutation := `UPDATE metadata_provider_policy SET enabled=0 WHERE provider='tmdb'`
			if mode == "provider_disabled" {
				mutation = `UPDATE metadata_providers SET enabled=0 WHERE code='tmdb'`
			}
			if mode == "online_disabled" {
				mutation = `UPDATE metadata_runtime_settings SET online_lookups=0`
			}
			if _, err = turn.transaction.conn.ExecContext(t.Context(), mutation); err != nil {
				t.Fatal(err)
			}
			if err = entry.checkFreshProvider(t.Context(), turn, provider, [16]byte{9}, 1, consent); !errors.Is(err, ErrDisclosureDenied) {
				t.Fatal("disabled provider/online source admitted", err)
			}
		})
	}
}
func TestIPEntryPersonTokenRevisionAndProviderGate(t *testing.T) {
	w, r, p, q := ipEntryFixture(t, true)
	turn, b := ipEntryTurn(t, w, r, q)
	entry, err := enterIPCommand(t.Context(), turn, q, p, t.Context(), b)
	if err != nil {
		t.Fatal(err)
	}
	if err = entry.checkFreshSubject(t.Context(), turn, [16]byte{}, 0, 0, 1, "person-token"); err != nil {
		t.Fatal("person positive control", err)
	}
	if err = entry.checkFreshSubject(t.Context(), turn, [16]byte{}, 0, 0, 1, "wrong-token"); !errors.Is(err, ErrDisclosureDenied) {
		t.Fatal("foreign person token accepted", err)
	}
	if err = entry.checkFreshSubject(t.Context(), turn, [16]byte{}, 0, 0, 2, "person-token"); !errors.Is(err, ErrDisclosureDenied) {
		t.Fatal("stale person revision accepted", err)
	}
	if err = entry.checkFreshProvider(t.Context(), turn, "tmdb", [16]byte{9}, 1, 1); !errors.Is(err, ErrInvalid) {
		t.Fatal("person borrowed entity provider gate", err)
	}
}
func TestIPEntryMidturnSubjectAndProviderRechecks(t *testing.T) {
	for _, gate := range []string{"subject", "provider"} {
		t.Run(gate, func(t *testing.T) {
			w, r, p, q := ipEntryFixture(t, false)
			ipEntryReviewProviderFixture(t, w)
			turn, b := ipEntryTurn(t, w, r, q)
			entry, err := enterIPCommand(t.Context(), turn, q, p, t.Context(), b)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = turn.transaction.conn.ExecContext(t.Context(), `UPDATE authorization_session_families SET revoked=1`); err != nil {
				t.Fatal(err)
			}
			if gate == "subject" {
				delete(turn.steps, ipEntryFreshEntity)
				err = entry.checkFreshSubject(t.Context(), turn, [16]byte{4}, 1, 1, 1, "")
			} else {
				delete(turn.steps, ipEntryPolicy)
				err = entry.checkFreshProvider(t.Context(), turn, "tmdb", [16]byte{9}, 1, 1)
			}
			// Removing the gate's recheck reaches its deliberately absent source step and
			// returns Invalid instead. Authority must deny BEFORE any source/policy IO.
			if !errors.Is(err, ErrDisclosureDenied) {
				t.Fatal("current authority recheck did not precede source/policy lookup", err)
			}
		})
	}
}
func TestIPEntryExpirySynchronousAndPurposeBinding(t *testing.T) {
	for _, mode := range []string{"token_expiry", "horizon_expiry", "synchronous", "purpose"} {
		t.Run(mode, func(t *testing.T) {
			w, r, p, q := ipEntryFixture(t, false)
			if strings.HasSuffix(mode, "expiry") {
				witness := p.witness
				now := w.now().Format(time.RFC3339)
				if mode == "token_expiry" {
					witness.tokenExpiry = now
					wc008NativeExec(t, w, `UPDATE authorization_family_tokens SET expires_at=?`, now)
				} else {
					witness.horizon = now
					wc008NativeExec(t, w, `UPDATE authorization_session_families SET authorization_horizon=?`, now)
				}
				var err error
				p, err = prepareUSOwnerAuthority(witness)
				if err != nil {
					t.Fatal(err)
				}
			}
			turn, b := ipEntryTurn(t, w, r, q)
			want := ErrDisclosureDenied
			if mode == "synchronous" {
				turn.transaction.settings.Synchronous = int(sqliteconn.Full)
				want = ErrInvalid
			}
			if mode == "purpose" {
				q.purpose = IPFieldLock
				want = ErrInvalid
			}
			out, err := enterIPCommand(t.Context(), turn, q, p, t.Context(), b)
			if out != nil || !errors.Is(err, want) {
				t.Fatal("invalid purpose/durability/expiry admitted", out, err)
			}
		})
	}
}
func TestIPEntryResolveNextSequenceHasNoEffects(t *testing.T) {
	w, r, p, q := ipEntryFixture(t, false)
	seedIPEntryHistory(t, w, q, true)
	q.action = ipEntryResolve
	q.reference.Identity.Sequence = 2
	q.reference.Identity.OperationID = [16]byte{5}
	turn, b := ipEntryTurn(t, w, r, q)
	entry, err := enterIPCommand(t.Context(), turn, q, p, t.Context(), b)
	if err != nil || entry == nil || entry.decision != receiptFresh {
		t.Fatal(entry, err)
	}
	// This private prefix reports classification only. The body must translate
	// Resolve+Fresh to Pending and must never proceed to its mutation path.
	var last, ack, receipts int64
	if err = turn.transaction.conn.QueryRowContext(t.Context(), `SELECT last_command_sequence,acknowledged_command_sequence FROM metadata_edit_owners WHERE id=900`).Scan(&last, &ack); err != nil {
		t.Fatal(err)
	}
	if err = turn.transaction.conn.QueryRowContext(t.Context(), `SELECT count(*) FROM metadata_edit_receipts WHERE owner_id=900`).Scan(&receipts); err != nil {
		t.Fatal(err)
	}
	if last != 1 || ack != 1 || receipts != 1 {
		t.Fatal("resolve mutated history", last, ack, receipts)
	}
}
