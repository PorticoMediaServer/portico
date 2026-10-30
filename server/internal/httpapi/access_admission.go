package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/playbackv1"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/access"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
)

// This file is the one place the per-member limits meet a running request.
//
//	access schedule       — withSessionAdmission (every session created or
//	                        renewed), admitPlayback and admitChannel.
//	max streams           — admitPlayback, before a playback lease is created,
//	                        counting legacy sessions and live v1 sessions.
//	maximum content rating— admitPlayback, and catalog visibility through
//	                        access.Enforcer.VisibleItem / VisibilityClause.
//	denied labels         — admitPlayback, same visibility pair.
//	remote bitrate cap    — admitPlayback returns it as a delivery clamp.
//	channel policy        — admitChannel, on v1 channel session starts only
//	                        (canonical v1 ids), with the schedule.
//	device approval       — not here: a session exists only on an approved
//	                        device and every request re-checks that
//	                        (identity_devices.approval_state).

// withSessionAdmission prepares the two rules every session this request
// creates or renews must pass, and hands them to the identity service through
// the request context. The identity service applies them at the single place
// a session is created (IssueTx) and at renewal, so no route decides for
// itself whether the rules apply to it.
//
//   - The owner's remote sign-in choice. Locality and the setting are read
//     here, before any transaction exists: the server runs SQLite on one
//     connection, and a settings read inside the issuing transaction would
//     wait on the transaction holding it.
//   - The member's access schedule, read inside the issuing transaction.
func (d Dependencies) withSessionAdmission(r *http.Request) context.Context {
	ctx := r.Context()
	admission := identity.SignInAdmission{Policy: identity.RemoteSignInAllow}
	if d.Console != nil {
		setting, err := d.connectionSetting(ctx)
		switch {
		case err != nil:
			// The setting cannot be read: nobody is admitted from outside
			// until it can.
			admission.Policy = identity.RemoteSignInOff
		case setting != nil:
			admission.Policy = setting.remoteSignIn
		}
		if admission.Policy != identity.RemoteSignInAllow {
			admission.Remote = d.requestIsRemote(r)
		}
	}
	ctx = identity.WithSignInAdmission(ctx, admission)
	if d.Access.Access != nil {
		enforcer := d.enforcer()
		ctx = identity.WithAccountAdmitter(ctx, func(ctx context.Context, tx *sql.Tx, account string) error {
			return enforcer.AdmitAccount(ctx, tx, account)
		})
	}
	return ctx
}

// ErrRemoteSignIn is the connectivity policy's refusal (identity.ErrRemoteSignIn).
var ErrRemoteSignIn = identity.ErrRemoteSignIn

// admitPlayback applies the playback-side limits and returns the clamps a
// permitted request still has to respect.
func (d Dependencies) admitPlayback(r *http.Request, p identity.Principal, item string) (access.Decision, error) {
	if item != "" {
		if err := d.restrictedItem(r, p, item); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return access.Decision{}, identity.ErrContentRestricted
			}
			return access.Decision{}, err
		}
	}
	if d.Access.Access == nil || d.DB == nil {
		return access.Decision{}, nil
	}
	if tx := dbwork.Snapshot(r.Context()); tx != nil {
		return d.admitPlaybackInTx(r, tx, p)
	}
	document, err := d.settingsDocument(r.Context())
	if err != nil {
		return access.Decision{}, err
	}
	// Locality may read settings on a cold cache, so it is decided before the
	// admission transaction opens (one SQLite connection: a read inside would
	// wait on the transaction holding it).
	remote := d.requestIsRemote(r)
	gated2, err := dbwork.Begin(r.Context(), d.DB, dbwork.ClassFrom(r.Context(), dbwork.ClassInteractive))
	if err != nil {
		return access.Decision{}, err
	}
	tx := gated2.Tx()
	defer gated2.Rollback()
	decision, err := d.enforcer().AdmitPlayback(r.Context(), tx, p, access.Admission{Remote: remote, ReplanSessionID: playbackv1.ReplanOf(r.Context()).Session, ReplanMediaID: playbackv1.ReplanOf(r.Context()).Media})
	if err != nil {
		return decision, err
	}
	budgeted, exhausted := false, false
	if remote && document.Effective.UploadCapacityKbps > 0 {
		ceiling, full, err := uploadBudgetCeiling(r.Context(), tx, document.Effective.UploadCapacityKbps, playbackv1.ReplanOf(r.Context()).Session, time.Now())
		if err != nil {
			return decision, err
		}
		decision.MaxVideoBitrateBPS = narrowBitrate(decision.MaxVideoBitrateBPS, ceiling)
		budgeted, exhausted = true, full
		decision.UploadLimited = full
	}
	if err = gated2.Commit(); err != nil {
		return decision, err
	}
	if budgeted {
		d.alertUploadBudget(r.Context(), exhausted)
	}
	return decision, nil
}

// admitPlaybackInTx is a read check within the caller's exact source or accepting
// snapshot. It must not begin/commit a transaction, invoke settings/tool status
// validation or publish an owner alert. The caller retains its writer custody.
func (d Dependencies) admitPlaybackInTx(r *http.Request, tx *sql.Tx, p identity.Principal) (access.Decision, error) {
	ctx := r.Context()
	settings := operations.DefaultSettings()
	if d.Console != nil {
		document, err := d.Console.SettingsTx(ctx, tx, operations.AllowServerScope)
		if err != nil {
			return access.Decision{}, err
		}
		settings = document.Effective
	}
	policy := newLocalityPolicy(settings)
	proxies := policy.proxies
	if d.Console == nil {
		proxies = d.TrustedProxies
	}
	remote := remoteClient(r, proxies, policy)
	decision, err := d.enforcer().AdmitPlayback(ctx, tx, p, access.Admission{Remote: remote,
		ReplanSessionID: playbackv1.ReplanOf(ctx).Session, ReplanMediaID: playbackv1.ReplanOf(ctx).Media})
	if err != nil {
		return decision, err
	}
	if remote && settings.UploadCapacityKbps > 0 {
		ceiling, full, err := uploadBudgetCeiling(ctx, tx, settings.UploadCapacityKbps, playbackv1.ReplanOf(ctx).Session, time.Now())
		if err != nil {
			return decision, err
		}
		decision.MaxVideoBitrateBPS = narrowBitrate(decision.MaxVideoBitrateBPS, ceiling)
		decision.UploadLimited = full
	}
	return decision, ctx.Err()
}

// admitChannel applies the live channel allow/deny policy (canonical v1 ids
// only) with the schedule.
func (d Dependencies) admitChannel(r *http.Request, p identity.Principal, channel string) error {
	if err := d.featureAllowed(r.Context(), p, "live_tv"); err != nil {
		return err
	}
	if d.Access.Access == nil || d.DB == nil || channel == "" {
		return nil
	}
	gated3, err := dbwork.BeginSnapshot(r.Context(), d.DB)
	if err != nil {
		return err
	}
	tx := gated3.Tx()
	defer gated3.Rollback()
	return d.enforcer().AdmitChannel(r.Context(), tx, p, channel)
}
