package sqliteconn

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

func TestEngineAttestationMatchesHeldNativeConnection(t *testing.T) {
	_, db, cfg := fixture(t)
	conn := assertConn(t, db, cfg)
	actual, err := AttestEngine(t.Context(), conn)
	if err != nil || actual.Version != "3.53.4" || actual.SourceID == "" {
		t.Fatal("native engine", actual, err)
	}
	// Independent setup-only SQL compares the actual native functions. This
	// is not registered command SQL or a SEARCH-only grammar exception.
	var expected EngineIdentity
	if err := conn.QueryRowContext(t.Context(), "SELECT sqlite_version(),sqlite_source_id()").Scan(&expected.Version, &expected.SourceID); err != nil || actual != expected {
		t.Fatal("getter differs from held SQLite", expected, actual, err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	if stale, err := AttestEngine(t.Context(), conn); !errors.Is(err, sql.ErrConnDone) || stale != (EngineIdentity{}) {
		t.Fatal("closed connection served stale engine", stale, err)
	}
	conn = assertConn(t, db, cfg)
	defer conn.Close()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if value, err := AttestEngine(ctx, conn); !errors.Is(err, context.Canceled) || value != (EngineIdentity{}) {
		t.Fatal("canceled observation served engine", value, err)
	}
	for _, c := range []struct {
		ctx  context.Context
		conn *sql.Conn
	}{{nil, conn}, {t.Context(), nil}} {
		if value, err := AttestEngine(c.ctx, c.conn); !errors.Is(err, ErrPolicy) || value != (EngineIdentity{}) {
			t.Fatal("invalid engine source accepted", value, err)
		}
	}
}
