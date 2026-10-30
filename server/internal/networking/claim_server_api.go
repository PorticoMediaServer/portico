package networking

import (
	"context"
	"database/sql"
	"net/http"
)

type ServerOperation string

const (
	SendHeartbeat ServerOperation = "heartbeat"
	// PushMembers reports this server's Portico Account members to Hosted's
	// discovery index (Spec — Hosted at Scale). Membership is decided here.
	PushMembers               ServerOperation = "members-push"
	PublishServerEvent        ServerOperation = "server-event"
	RegisterEndpoint          ServerOperation = "endpoint-register"
	PublishRoutes             ServerOperation = "routes-publish"
	ProveRoute                ServerOperation = "route-prove"
	ResolveReturnRegistration ServerOperation = "return-registration"
)

// CallServer is restricted to declared current installed-server control-plane
// operations. No caller-selected URL, path or bearer is accepted.
func (t *HTTPTransport) CallServer(ctx context.Context, v Intent, operation ServerOperation, in, out any) error {
	if v.Stage != Installed || !v.InstallationAcknowledged {
		return ErrStale
	}
	suffix := ""
	switch operation {
	case PushMembers:
		suffix = "/members"
	case PublishServerEvent:
		suffix = "/events"
	case SendHeartbeat:
		suffix = "/heartbeat"
	case RegisterEndpoint:
		suffix = "/endpoint"
	case PublishRoutes:
		suffix = "/routes"
	case ProveRoute:
		suffix = "/routes/prove"
	case ResolveReturnRegistration:
		suffix = "/return-registration"
	default:
		return ErrInvalid
	}
	secret, e := t.credentials.InstalledCredential(ctx, v)
	if e != nil {
		return e
	}
	defer secret.Clear()
	requestBound := claimHTTPBound
	if operation == PublishRoutes {
		requestBound = 64 << 10
	}
	if operation == PushMembers {
		// Hosted's body limit for a membership push (MemberPushBody).
		requestBound = 1 << 20
	}
	if e = t.boundedControlRequest(ctx, http.MethodPost, "/v1/servers/"+v.ServerID+suffix, in, out, secret, requestBound, 1<<20); e != nil {
		return e
	}
	return t.credentials.Current(ctx, v)
}

// WithInstalledTransaction binds root policy/publication to the current exact
// operation and credential under the same leased writer transaction.
func (s *SQLiteStore) WithInstalledTransaction(ctx context.Context, v Intent, apply func(context.Context, *sql.Tx) error) error {
	if apply == nil {
		return ErrInvalid
	}
	gated, e := s.tx(ctx)
	if e != nil {
		return e
	}
	tx := gated.Tx()
	defer gated.Rollback()
	if _, e = s.positive(ctx, tx, v); e != nil {
		return e
	}
	if e = apply(ctx, tx); e != nil {
		return e
	}
	return commitClaimDatabaseTx(ctx, gated)
}
