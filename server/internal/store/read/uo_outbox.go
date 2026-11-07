package read

import (
	"context"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Private, current authorized delivery facts from one admitted snapshot. Secret
// material stays in the charged delivery owner and never enters a generic event
// or public status result. The transport must recheck authority before send.
type uoWebhookRead struct {
	id, webhook                                                                   string
	authority, webhookRevision, owner, generation, jobRevision, nowMS, byteBudget int64
}
type uoWebhookDelivery struct{ url, secret, kind, subject, data string }
type uoServiceRead struct {
	id, operation, server, claim, credential       string
	owner, generation, revision, nowMS, byteBudget int64
}
type uoServiceDelivery struct{ payload, signature []byte }
type uoOutboxQuery func(stmt.ID, ...any) ([][]any, error)

func uoExactOutboxPoint(ctx context.Context, query uoOutboxQuery, lengthID, payloadID stmt.ID, budget int64, args ...any) ([]any, error) {
	if ctx == nil || query == nil || budget <= 0 || budget > 512<<10 {
		return nil, ErrInput
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	sizes, err := query(lengthID, args...)
	if err != nil {
		return nil, err
	}
	if len(sizes) != 1 {
		return nil, ErrDenied
	}
	var total int64
	for _, v := range sizes[0] {
		n, ok := v.(int64)
		if !ok || n < 0 || n > budget-total {
			return nil, ErrUnqualified
		}
		total += n
	}
	rows, err := query(payloadID, args...)
	if err != nil {
		return nil, err
	}
	if len(rows) != 1 || len(rows[0]) != len(sizes[0]) {
		return nil, ErrStale
	}
	for i, v := range rows[0] {
		var n int
		switch v := v.(type) {
		case string:
			n = len(v)
			if !utf8.ValidString(v) {
				return nil, ErrUnqualified
			}
		case []byte:
			n = len(v)
		default:
			return nil, ErrUnqualified
		}
		if int64(n) != sizes[0][i].(int64) {
			return nil, ErrStale
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return rows[0], nil
}
func readUOWebhookDelivery(ctx context.Context, query uoOutboxQuery, q uoWebhookRead) (uoWebhookDelivery, error) {
	if q.id == "" || len(q.id) > 128 || q.webhook == "" || len(q.webhook) > 128 || q.authority <= 0 || q.webhookRevision <= 0 || q.owner <= 0 || q.generation <= 0 || q.jobRevision <= 0 || q.nowMS <= 0 {
		return uoWebhookDelivery{}, ErrInput
	}
	row, err := uoExactOutboxPoint(ctx, query, uoWebhookReadOctets, uoWebhookReadPoint, q.byteBudget, q.id, q.webhook, q.authority, q.webhookRevision, q.owner, q.generation, q.jobRevision, q.nowMS)
	if err != nil {
		return uoWebhookDelivery{}, err
	}
	if len(row) != 5 {
		return uoWebhookDelivery{}, ErrUnqualified
	}
	var out uoWebhookDelivery
	dst := []*string{&out.url, &out.secret, &out.kind, &out.subject, &out.data}
	for i := range dst {
		s, ok := row[i].(string)
		if !ok {
			return uoWebhookDelivery{}, ErrUnqualified
		}
		*dst[i] = s
	}
	return out, nil
}
func readUOServiceDelivery(ctx context.Context, query uoOutboxQuery, q uoServiceRead) (uoServiceDelivery, error) {
	if q.id == "" || len(q.id) > 128 || q.operation == "" || len(q.operation) > 128 || q.server == "" || len(q.server) > 128 || q.claim == "" || len(q.claim) > 128 || q.credential == "" || len(q.credential) > 128 || q.owner <= 0 || q.generation <= 0 || q.revision <= 0 || q.nowMS <= 0 {
		return uoServiceDelivery{}, ErrInput
	}
	row, err := uoExactOutboxPoint(ctx, query, uoServiceReadOctets, uoServiceReadPoint, q.byteBudget, q.id, q.operation, q.server, q.claim, q.credential, q.owner, q.generation, q.revision, q.nowMS)
	if err != nil {
		return uoServiceDelivery{}, err
	}
	if len(row) != 2 {
		return uoServiceDelivery{}, ErrUnqualified
	}
	p, ok := row[0].([]byte)
	if !ok {
		return uoServiceDelivery{}, ErrUnqualified
	}
	sig, ok := row[1].([]byte)
	if !ok || len(sig) != 64 {
		return uoServiceDelivery{}, ErrUnqualified
	}
	return uoServiceDelivery{payload: p, signature: sig}, nil
}

const (
	uoWebhookReadOctets stmt.ID = "read.uo.outbox.webhook.octets"
	uoWebhookReadPoint  stmt.ID = "read.uo.outbox.webhook.point"
	uoServiceReadOctets stmt.ID = "read.uo.outbox.service.octets"
	uoServiceReadPoint  stmt.ID = "read.uo.outbox.service.point"
)

func uoOutboxReadDefinitions() []stmt.Definition {
	s := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	webParams := []stmt.Param{s("delivery"), s("webhook"), i("authority"), i("webhook_revision"), i("owner"), i("generation"), i("job_revision"), i("now")}
	serviceParams := []stmt.Param{s("notice"), s("operation"), s("server"), s("claim"), s("credential"), i("owner"), i("generation"), i("revision"), i("now")}
	webSuffix := ` FROM webhook_deliveries AS d CROSS JOIN webhooks AS w ON w.id=d.webhook_id WHERE d.id=? AND w.id=? AND w.authority_generation=? AND w.revision=? AND d.authority_generation=w.authority_generation AND w.enabled=1 AND w.owner_review_required=0 AND d.state='pending' AND d.job_lease_owner=? AND d.job_lease_generation=? AND d.job_revision=? AND d.job_lease_expires_ms>?`
	serviceSuffix := ` FROM identity_service_outbox WHERE notice_id=? AND operation_id=? AND server_id=? AND claim_generation=? AND credential_generation=? AND delivered_ms=0 AND job_lease_owner=? AND job_lease_generation=? AND job_revision=? AND job_lease_expires_ms>?`
	d := func(id stmt.ID, query string, p []stmt.Param) stmt.Definition {
		return stmt.Definition{ID: id, Kind: stmt.Read, Owner: "store/read UO outbox", SQL: query, Params: p, CallSites: []string{"store/read/uo_outbox.go"}, Plan: stmt.Plan{Hot: true}}
	}
	return []stmt.Definition{
		d(uoWebhookReadOctets, `SELECT octet_length(w.url),octet_length(w.secret),octet_length(d.type),octet_length(d.subject),octet_length(d.data)`+webSuffix, webParams),
		d(uoWebhookReadPoint, `SELECT w.url,w.secret,d.type,d.subject,d.data`+webSuffix, webParams),
		d(uoServiceReadOctets, `SELECT octet_length(payload),octet_length(signature)`+serviceSuffix, serviceParams),
		d(uoServiceReadPoint, `SELECT payload,signature`+serviceSuffix, serviceParams),
	}
}
