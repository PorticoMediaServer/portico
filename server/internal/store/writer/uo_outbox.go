package writer

import (
	"context"
	"database/sql"
	"encoding/json"
	"math"
	"strconv"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

type uoWebhookIntent struct {
	id, webhook, eventType, subject, data string
	authority, revision, nowMS            int64
}
type uoWebhookOutcome struct {
	job                                           uoOperationalJob
	webhook                                       string
	authority, webhookRevision, nowMS, durationMS int64
	status                                        int64
	delivered                                     bool
	errorCode                                     string
}
type uoServiceOutboxOutcome struct {
	job                                                      uoOperationalJob
	operation, server, claimGeneration, credentialGeneration string
	nowMS                                                    int64
}

func validUOOutboxText(s string, limit int, required bool) bool {
	return len(s) <= limit && (!required || len(s) > 0) && utf8.ValidString(s)
}

// The parent has already proved this target's current event subscription and
// current issuer authority, then classified the original intent as fresh.
// One target intent is one bounded effect, never a recipient/user enumeration.
// HTTP and credential/payload preparation occur outside the writer hold.
func enqueueUOWebhook(ctx context.Context, turn *commandTurn, q uoWebhookIntent) (out string, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if err = turn.checkLifetimes(ctx); err != nil {
		return out, err
	}
	if !validUOOutboxText(q.id, 128, true) || !validUOOutboxText(q.webhook, 128, true) || !validUOOutboxText(q.eventType, 96, true) || !validUOOutboxText(q.subject, 128, false) || !validUOOutboxText(q.data, 64<<10, true) || !json.Valid([]byte(q.data)) || q.authority <= 0 || q.revision <= 0 || q.nowMS <= 0 {
		return out, ErrInvalid
	}
	if _, err = wc008DraftInteger(ctx, turn, uoWebhookGate, q.webhook, q.authority, q.revision); err != nil {
		return out, err
	}
	row, err := wc008DraftQuery(ctx, turn, uoWebhookEnqueue, q.id, q.webhook, q.authority, q.eventType, q.subject, q.data, q.nowMS, q.nowMS)
	if err != nil {
		return out, err
	}
	if err = row.Scan(&out); err != nil {
		return "", err
	}
	if out != q.id {
		return "", ErrResolveRequired
	}
	return out, turn.checkLifetimes(ctx)
}

// The original attempt remains at-least-once externally. A COMMIT failure must
// resolve its saved command result; it must not blindly repeat HTTP. This body
// saves the bounded domain outcome and finishes the SAME exact native lease.
func completeUOWebhook(ctx context.Context, turn *commandTurn, q uoWebhookOutcome) (out uoJobLease, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	validFailure := uoWebhookFailure(q.errorCode) || q.status >= 400 && q.status <= 599 && q.errorCode == "status_"+strconv.FormatInt(q.status, 10)
	if q.job.family != uoJobWebhook || q.status < 0 || q.status > 599 || q.durationMS < 0 || q.durationMS > 86_400_000 || q.nowMS <= 0 || q.authority <= 0 || q.webhookRevision <= 0 || !validUOOutboxText(q.webhook, 128, true) || q.delivered && (q.errorCode != "" || q.status < 200 || q.status >= 300) || !q.delivered && !validFailure {
		return out, ErrInvalid
	}
	if _, err = wc008DraftInteger(ctx, turn, uoWebhookGate, q.webhook, q.authority, q.webhookRevision); err != nil {
		return out, err
	}
	state := "failed"
	finishedMS := q.nowMS
	var retry sql.NullInt64
	if q.delivered {
		state = "delivered"
	} else if q.job.lease.attempts > 0 && q.job.lease.attempts <= 7 && q.errorCode != "payload_too_large" {
		delay := [...]int64{30_000, 120_000, 600_000, 1_800_000, 3_600_000, 7_200_000, 14_400_000}[q.job.lease.attempts-1]
		if q.nowMS > math.MaxInt64-delay {
			return out, ErrInvalid
		}
		state, finishedMS, retry = "pending", 0, sql.NullInt64{Int64: q.nowMS + delay, Valid: true}
	}
	old := q.job.lease
	if _, err = wc008DraftInteger(ctx, turn, uoWebhookComplete, state, finishedMS, q.status, q.durationMS, q.errorCode, q.job.key1, q.webhook, q.authority, old.owner, old.generation, old.expiresMS, old.attempts, old.revision, q.nowMS); err != nil {
		return out, err
	}
	failures, err := wc008DraftInteger(ctx, turn, uoWebhookFailures, q.webhook, q.authority, q.webhookRevision)
	if err != nil {
		return out, err
	}
	nextFailures := int64(0)
	if !q.delivered {
		if failures == math.MaxInt64 {
			return out, ErrInvalid
		}
		nextFailures = failures + 1
	}
	if _, err = wc008DraftInteger(ctx, turn, uoWebhookSetFailures, nextFailures, q.webhook, q.authority, q.webhookRevision, failures); err != nil {
		return out, err
	}
	return applyUOJobLease(ctx, turn, q.job, uoLeaseChange{action: uoLeaseFinish, nowMS: q.nowMS, owner: old.owner, generation: old.generation, nextMS: retry})
}

func uoWebhookFailure(code string) bool {
	switch code {
	case "payload_too_large", "authority_retired", "address_not_valid", "timed_out", "certificate_not_trusted", "could_not_connect", "request_failed", "redirect_not_followed", "http_status":
		return true
	default:
		return false
	}
}

// US owns the signed service intent and security authority. UO only records
// delivery of the exact immutable generation after external transport returns.
func completeUOServiceOutbox(ctx context.Context, turn *commandTurn, q uoServiceOutboxOutcome) (out uoJobLease, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if q.job.family != uoJobServiceOutbox || q.nowMS <= 0 || !validUOOutboxText(q.operation, 128, true) || !validUOOutboxText(q.server, 128, true) || !validUOOutboxText(q.claimGeneration, 128, true) || !validUOOutboxText(q.credentialGeneration, 128, true) {
		return out, ErrInvalid
	}
	old := q.job.lease
	if _, err = wc008DraftInteger(ctx, turn, uoServiceComplete, q.nowMS, q.job.key1, q.operation, q.server, q.claimGeneration, q.credentialGeneration, old.owner, old.generation, old.expiresMS, old.attempts, old.revision, q.nowMS); err != nil {
		return out, err
	}
	return applyUOJobLease(ctx, turn, q.job, uoLeaseChange{action: uoLeaseFinish, nowMS: q.nowMS, owner: old.owner, generation: old.generation})
}

func reclaimUOWebhook(ctx context.Context, turn *commandTurn, id string, revision, finishedMS, cutoffMS int64) (out int64, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if !validUOOutboxText(id, 128, true) || revision <= 0 || finishedMS <= 0 || cutoffMS < finishedMS {
		return 0, ErrInvalid
	}
	return wc008DraftInteger(ctx, turn, uoWebhookReclaim, id, revision, finishedMS, cutoffMS)
}

const (
	uoWebhookGate        stmt.ID = "uo.outbox.webhook.gate"
	uoWebhookEnqueue     stmt.ID = "uo.outbox.webhook.enqueue"
	uoWebhookComplete    stmt.ID = "uo.outbox.webhook.complete"
	uoServiceComplete    stmt.ID = "uo.outbox.service.complete"
	uoWebhookReclaim     stmt.ID = "uo.outbox.webhook.reclaim"
	uoWebhookFailures    stmt.ID = "uo.outbox.webhook.failures"
	uoWebhookSetFailures stmt.ID = "uo.outbox.webhook.set_failures"
)

func uoOutboxDefinitions() []stmt.Definition {
	s := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	d := func(id stmt.ID, kind stmt.Kind, query string, p ...stmt.Param) stmt.Definition {
		return stmt.Definition{ID: id, Kind: kind, Owner: "store/writer UO outbox", SQL: query, Params: p, CallSites: []string{"store/writer/uo_outbox.go"}, Plan: stmt.Plan{Hot: true}}
	}
	return []stmt.Definition{
		d(uoWebhookGate, stmt.Read, `SELECT authority_generation FROM webhooks WHERE id=? AND authority_generation=? AND revision=? AND enabled=1 AND owner_review_required=0`, s("webhook"), i("authority"), i("revision")),
		d(uoWebhookFailures, stmt.Read, `SELECT failures FROM webhooks WHERE id=? AND authority_generation=? AND revision=?`, s("webhook"), i("authority"), i("revision")),
		d(uoWebhookSetFailures, stmt.Write, `UPDATE webhooks SET failures=? WHERE id=? AND authority_generation=? AND revision=? AND failures=? RETURNING failures`, i("next"), s("webhook"), i("authority"), i("revision"), i("old")),
		d(uoWebhookEnqueue, stmt.Write, `INSERT INTO webhook_deliveries(id,webhook_id,authority_generation,type,subject,data,created_ms,job_next_attempt_ms) VALUES(?,?,?,?,?,?,?,?) RETURNING id`, s("id"), s("webhook"), i("authority"), s("type"), s("subject"), s("data"), i("created"), i("due")),
		d(uoWebhookComplete, stmt.Write, `UPDATE webhook_deliveries SET state=?,finished_ms=?,status=?,duration_ms=?,error=? WHERE id=? AND webhook_id=? AND authority_generation=? AND state='pending' AND job_lease_owner=? AND job_lease_generation=? AND job_lease_expires_ms=? AND job_attempts=? AND job_revision=? AND job_lease_expires_ms>? RETURNING job_revision`, s("state"), i("finished"), i("status"), i("duration"), s("error"), s("id"), s("webhook"), i("authority"), i("owner"), i("generation"), i("expiry"), i("attempts"), i("revision"), i("now")),
		d(uoServiceComplete, stmt.Write, `UPDATE identity_service_outbox SET delivered_ms=? WHERE notice_id=? AND operation_id=? AND server_id=? AND claim_generation=? AND credential_generation=? AND delivered_ms=0 AND job_lease_owner=? AND job_lease_generation=? AND job_lease_expires_ms=? AND job_attempts=? AND job_revision=? AND job_lease_expires_ms>? RETURNING job_revision`, i("delivered"), s("notice"), s("operation"), s("server"), s("claim"), s("credential"), i("owner"), i("generation"), i("expiry"), i("attempts"), i("revision"), i("now")),
		d(uoWebhookReclaim, stmt.Write, `DELETE FROM webhook_deliveries WHERE id=? AND job_revision=? AND finished_ms=? AND finished_ms<=? AND state IN('delivered','failed') AND job_lease_owner=0 AND job_lease_expires_ms=0 AND job_next_attempt_ms IS NULL RETURNING job_revision`, s("id"), i("revision"), i("finished"), i("cutoff")),
	}
}
