package read

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

func uoNativeReadQuery(t *testing.T, f *browseFixture, defs []stmt.Definition) uoOutboxQuery {
	t.Helper()
	for n := range defs {
		var args []any
		for _, p := range defs[n].Params {
			if p.Type == stmt.Text {
				args = append(args, "fixture")
			} else {
				args = append(args, int64(1))
			}
		}
		defs[n].Cohorts = []stmt.Cohort{{Name: "native-component", Bindings: args}}
	}
	registry, err := stmttest.New(t, defs)
	if err != nil {
		t.Fatal(err)
	}
	return func(id stmt.ID, args ...any) (out [][]any, err error) {
		d, err := registry.Lookup(id)
		if err != nil {
			return nil, err
		}
		b, err := d.Bind(args...)
		if err != nil {
			return nil, err
		}
		rows, err := b.QueryContext(t.Context(), f.conn)
		if err != nil {
			return nil, err
		}
		defer func() { err = errors.Join(err, rows.Close()) }()
		cols, err := rows.Columns()
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			v := make([]any, len(cols))
			dest := make([]any, len(cols))
			for n := range v {
				dest[n] = &v[n]
			}
			if err := rows.Scan(dest...); err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		return out, rows.Err()
	}
}

func TestUOOutboxReadNativeGenerationExpiryAndBytes(t *testing.T) {
	f := newBrowseSource(t)
	f.exec(t, `INSERT INTO webhooks(id,name,url,secret,events,created_ms,updated_ms) VALUES('hook','Fixture','https://example.invalid','test-only','[]',1,1)`)
	f.exec(t, `INSERT INTO webhook_deliveries(id,webhook_id,type,data,created_ms,job_lease_owner,job_lease_generation,job_lease_expires_ms,job_revision) VALUES('delivery','hook','download.completed','{}',1,7,2,500,3)`)
	f.exec(t, `INSERT INTO identity_service_outbox(notice_id,operation_id,server_id,claim_generation,credential_generation,payload,signature,created_ms,job_lease_owner,job_lease_generation,job_lease_expires_ms,job_revision) VALUES('notice','op','server','claim','credential',x'0102',zeroblob(64),1,7,2,500,3)`)
	base := uoNativeReadQuery(t, f, uoOutboxReadDefinitions())
	q := uoWebhookRead{id: "delivery", webhook: "hook", authority: 1, webhookRevision: 1, owner: 7, generation: 2, jobRevision: 3, nowMS: 100, byteBudget: 4096}
	out, err := readUOWebhookDelivery(t.Context(), base, q)
	if err != nil || out.secret != "test-only" || out.data != "{}" {
		t.Fatal(out, err)
	}
	for _, mutate := range []func(*uoWebhookRead){func(q *uoWebhookRead) { q.generation++ }, func(q *uoWebhookRead) { q.authority++ }, func(q *uoWebhookRead) { q.nowMS = 500 }} {
		bad := q
		mutate(&bad)
		if _, err := readUOWebhookDelivery(t.Context(), base, bad); !errors.Is(err, ErrDenied) {
			t.Fatal("stale delivery disclosed", err)
		}
	}
	payload := false
	query := func(id stmt.ID, args ...any) ([][]any, error) {
		if id == uoWebhookReadPoint {
			payload = true
		}
		return base(id, args...)
	}
	f.exec(t, `UPDATE webhook_deliveries SET data=? WHERE id='delivery'`, strings.Repeat("x", 4097))
	if _, err := readUOWebhookDelivery(t.Context(), query, q); !errors.Is(err, ErrUnqualified) || payload {
		t.Fatal("oversize copied", payload, err)
	}
	s := uoServiceRead{id: "notice", operation: "op", server: "server", claim: "claim", credential: "credential", owner: 7, generation: 2, revision: 3, nowMS: 100, byteBudget: 66}
	service, err := readUOServiceDelivery(t.Context(), base, s)
	if err != nil || len(service.payload) != 2 || len(service.signature) != 64 {
		t.Fatal(service, err)
	}
	s.credential = "successor"
	if _, err := readUOServiceDelivery(t.Context(), base, s); !errors.Is(err, ErrDenied) {
		t.Fatal("foreign service generation disclosed", err)
	}
}

func TestUOOutboxReadCanceledBeforeQuery(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	calls := 0
	query := func(stmt.ID, ...any) ([][]any, error) { calls++; return nil, nil }
	_, err := uoExactOutboxPoint(ctx, query, uoServiceReadOctets, uoServiceReadPoint, 1)
	if !errors.Is(err, context.Canceled) || calls != 0 {
		t.Fatal(calls, err)
	}
}
