package networking

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/serverreturn"
)

type SecurityActorReader func(context.Context, *sql.Tx) (account, binding string, expires time.Time, err error)

// securityReturnIdentityTx proves the exact current installed route tuple. It
// never uses the sign-in challenge or session-producing return exchange.
func (h *RouteIdentityHandler) securityReturnIdentityTx(ctx context.Context, tx *sql.Tx, c serverreturn.SecurityContext) (IdentitySnapshot, error) {
	id, e := CurrentIdentityTx(ctx, tx)
	if e != nil {
		return id, e
	}
	if id.ServerID != c.ServerID {
		return id, ErrStale
	}
	stored, e := loadIntentTx(ctx, tx, c.ClaimOperationID)
	if e != nil {
		return id, e
	}
	if !stored.InstallationAcknowledged {
		return id, ErrStale
	}
	if e = GuardInstalledClaim(ctx, tx, stored.Intent); e != nil {
		return id, e
	}
	var generation int64
	if e = tx.QueryRowContext(ctx, `SELECT generation FROM networking_remote_state WHERE singleton=1`).Scan(&generation); e != nil {
		return id, e
	}
	claim, ce := strconv.ParseInt(stored.ClaimGeneration, 10, 64)
	credential, ke := strconv.ParseInt(stored.CredentialGeneration, 10, 64)
	if ce != nil || ke != nil || claim < 1 || credential < 1 || generation < 1 || c.RegistrationID != serverreturn.RegistrationID(id.ServerID, stored.OperationID, claim, credential, generation, c.Origin) {
		return id, ErrStale
	}
	return id, nil
}

func signSecurity(key ed25519.PrivateKey, value any) (serverreturn.SecuritySigned, error) {
	raw, e := json.Marshal(value)
	if e != nil {
		return serverreturn.SecuritySigned{}, e
	}
	return serverreturn.SecuritySigned{Payload: base64.RawURLEncoding.EncodeToString(raw), Signature: base64.RawURLEncoding.EncodeToString(ed25519.Sign(key, raw))}, nil
}

// SignSecurityReturn captures a durable original opening. Reissuing that exact
// request returns its original signed bytes/expiry; another opening cancels its
// predecessor in the same writer. The caller cannot choose key or actor facts.
func (h *RouteIdentityHandler) SignSecurityReturn(parent context.Context, q serverreturn.SecurityContext, read SecurityActorReader) (out serverreturn.SecuritySigned, err error) {
	if h == nil || h.keys == nil || h.runner == nil || read == nil || !q.AuthorityExpiresAt.IsZero() || q.SettingsScope != "" || q.SettingsSection != "" || q.Kind != "" || q.PublicKey != "" || q.AccountID != "" || q.ActorBinding != "" || !q.IssuedAt.IsZero() {
		return out, ErrInvalid
	}
	request, _ := json.Marshal(q)
	requestHash := serverreturn.SecurityDigest(request)
	err = h.runner.Do(parent, func(ctx context.Context) error {
		g, e := dbwork.Begin(ctx, h.keys.db, dbwork.ClassSecurityFence)
		if e != nil {
			return e
		}
		defer g.Rollback()
		tx := g.Tx()
		account, binding, authorityExpires, e := read(ctx, tx)
		if e != nil {
			return e
		}
		id, e := h.securityReturnIdentityTx(ctx, tx, q)
		if e != nil {
			return e
		}
		key, e := h.keys.read(id.KeyIncarnation)
		if e != nil {
			return e
		}
		defer clear(key)
		if !bytes.Equal(key.Public().(ed25519.PublicKey), id.PublicKey) {
			return ErrStale
		}
		var priorHash, priorActor, phase string
		var expiry int64
		e = tx.QueryRowContext(ctx, `SELECT request_hash,actor_binding,phase,payload,signature,expires_ms FROM identity_security_return_openings WHERE operation_id=?`, q.OperationID).Scan(&priorHash, &priorActor, &phase, &out.Payload, &out.Signature, &expiry)
		if e == nil {
			if priorHash != requestHash || priorActor != binding || phase != "prepared" || expiry <= time.Now().UnixMilli() {
				return ErrStale
			}
			if _, _, e = serverreturn.VerifySecurityContext(out, id.PublicKey, time.Now()); e != nil {
				return ErrStale
			}
			return g.Commit()
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		q.AuthorityExpiresAt = authorityExpires.UTC()
		q.ExpiresAt = minSecurityTime(q.ExpiresAt, authorityExpires)
		q.SettingsScope = "account"
		q.SettingsSection = "profile"
		q.Kind = serverreturn.SecurityContextKind
		q.PublicKey = base64.RawURLEncoding.EncodeToString(id.PublicKey)
		q.AccountID = account
		q.ActorBinding = binding
		q.IssuedAt = time.Now().UTC()
		out, e = signSecurity(key, q)
		if e != nil {
			return e
		}
		if _, _, e = serverreturn.VerifySecurityContext(out, id.PublicKey, time.Now()); e != nil {
			return ErrInvalid
		}
		if _, e = tx.ExecContext(ctx, `UPDATE identity_security_return_openings SET phase='cancelled' WHERE actor_binding=? AND phase IN('prepared','confirmed')`, binding); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, `INSERT INTO identity_security_return_openings(operation_id,actor_binding,request_hash,opening_id,task_id,phase,payload,signature,expires_ms) VALUES(?,?,?,?,?,'prepared',?,?,?)`, q.OperationID, binding, requestHash, q.OpeningID, q.TaskID, out.Payload, out.Signature, q.ExpiresAt.UnixMilli()); e != nil {
			return e
		}
		if !q.ExpiresAt.After(time.Now()) || !authorityExpires.After(time.Now()) {
			return ErrStale
		}
		return g.Commit()
	})
	if err != nil {
		return serverreturn.SecuritySigned{}, err
	}
	return
}

func (h *RouteIdentityHandler) ConfirmSecurityReturn(parent context.Context, original serverreturn.SecuritySigned, transaction, code string, read SecurityActorReader) (out serverreturn.SecuritySigned, err error) {
	if h == nil || h.keys == nil || h.runner == nil || read == nil || len(transaction) > 128 || len(transaction) < 20 || len(code) != 47 || code[:4] != "scr_" {
		return out, ErrInvalid
	}
	err = h.runner.Do(parent, func(ctx context.Context) error {
		g, e := dbwork.Begin(ctx, h.keys.db, dbwork.ClassSecurityFence)
		if e != nil {
			return e
		}
		defer g.Rollback()
		tx := g.Tx()
		account, binding, authorityExpires, e := read(ctx, tx)
		if e != nil {
			return e
		}
		id, e := CurrentIdentityTx(ctx, tx)
		if e != nil {
			return e
		}
		c, raw, e := serverreturn.VerifySecurityContext(original, id.PublicKey, time.Now())
		if e != nil || c.AccountID != account || c.ActorBinding != binding || !c.AuthorityExpiresAt.Equal(authorityExpires) {
			return ErrStale
		}
		if _, e = h.securityReturnIdentityTx(ctx, tx, c); e != nil {
			return e
		}
		var payload, signature, phase, priorTransaction, priorCode string
		e = tx.QueryRowContext(ctx, `SELECT payload,signature,phase,transaction_id,code_digest,confirmation_payload,confirmation_signature FROM identity_security_return_openings WHERE operation_id=? AND actor_binding=? AND opening_id=? AND task_id=? AND expires_ms>?`, c.OperationID, binding, c.OpeningID, c.TaskID, time.Now().UnixMilli()).Scan(&payload, &signature, &phase, &priorTransaction, &priorCode, &out.Payload, &out.Signature)
		if e != nil || payload != original.Payload || signature != original.Signature {
			return ErrStale
		}
		if phase == "confirmed" {
			if priorTransaction != transaction || priorCode != serverreturn.SecurityDigest([]byte(code)) {
				return ErrStale
			}
			if _, e = serverreturn.VerifySecurityConfirmation(out, id.PublicKey, serverreturn.SecurityDigest(raw), transaction, code, time.Now()); e != nil {
				return ErrStale
			}
			return g.Commit()
		}
		if phase != "prepared" {
			return ErrStale
		}
		now := time.Now().UTC()
		expires := minSecurityTime(c.ExpiresAt, now.Add(time.Minute))
		key, e := h.keys.read(id.KeyIncarnation)
		if e != nil {
			return e
		}
		defer clear(key)
		if !bytes.Equal(key.Public().(ed25519.PublicKey), id.PublicKey) {
			return ErrStale
		}
		out, e = signSecurity(key, serverreturn.SecurityConfirmation{Kind: serverreturn.SecurityConfirmationKind, ContextDigest: serverreturn.SecurityDigest(raw), TransactionID: transaction, CodeDigest: serverreturn.SecurityDigest([]byte(code)), IssuedAt: now, ExpiresAt: expires})
		if e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, `UPDATE identity_security_return_openings SET phase='confirmed',transaction_id=?,code_digest=?,confirmation_payload=?,confirmation_signature=? WHERE operation_id=?`, transaction, serverreturn.SecurityDigest([]byte(code)), out.Payload, out.Signature, c.OperationID); e != nil {
			return e
		}
		return g.Commit()
	})
	if err != nil {
		return serverreturn.SecuritySigned{}, err
	}
	return
}

func minSecurityTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

func (h *RouteIdentityHandler) CancelSecurityReturn(parent context.Context, original serverreturn.SecuritySigned, read SecurityActorReader) error {
	if h == nil || h.keys == nil || h.runner == nil || read == nil {
		return ErrInvalid
	}
	return h.runner.Do(parent, func(ctx context.Context) error {
		g, e := dbwork.Begin(ctx, h.keys.db, dbwork.ClassSecurityFence)
		if e != nil {
			return e
		}
		defer g.Rollback()
		tx := g.Tx()
		account, binding, authorityExpires, e := read(ctx, tx)
		if e != nil {
			return e
		}
		id, e := CurrentIdentityTx(ctx, tx)
		if e != nil {
			return e
		}
		c, _, e := serverreturn.VerifySecurityContext(original, id.PublicKey, time.Now())
		if e != nil || c.AccountID != account || c.ActorBinding != binding || !c.AuthorityExpiresAt.Equal(authorityExpires) {
			return ErrStale
		}
		result, e := tx.ExecContext(ctx, `UPDATE identity_security_return_openings SET phase='cancelled' WHERE operation_id=? AND opening_id=? AND task_id=? AND actor_binding=? AND payload=? AND signature=?`, c.OperationID, c.OpeningID, c.TaskID, binding, original.Payload, original.Signature)
		if e != nil {
			return e
		}
		n, e := result.RowsAffected()
		if e != nil || n != 1 {
			return ErrStale
		}
		return g.Commit()
	})
}

// CheckSecurityReturn checks only the captured UI return scope. It grants no
// server-settings authority and performs no provider/password reauthentication.
func (h *RouteIdentityHandler) CheckSecurityReturn(parent context.Context, original serverreturn.SecuritySigned, read SecurityActorReader) error {
	if h == nil || h.keys == nil || h.runner == nil || read == nil {
		return ErrInvalid
	}
	return h.runner.Do(parent, func(ctx context.Context) error {
		g, e := dbwork.BeginSnapshot(ctx, h.keys.db)
		if e != nil {
			return e
		}
		defer g.Rollback()
		tx := g.Tx()
		account, binding, authority, e := read(ctx, tx)
		if e != nil {
			return e
		}
		id, e := CurrentIdentityTx(ctx, tx)
		if e != nil {
			return e
		}
		c, _, e := serverreturn.VerifySecurityContext(original, id.PublicKey, time.Now())
		if e != nil || c.AccountID != account || c.ActorBinding != binding || !c.AuthorityExpiresAt.Equal(authority) {
			return ErrStale
		}
		if _, e = h.securityReturnIdentityTx(ctx, tx, c); e != nil {
			return e
		}
		var present bool
		e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM identity_security_return_openings WHERE operation_id=? AND opening_id=? AND task_id=? AND actor_binding=? AND payload=? AND signature=? AND phase='confirmed' AND expires_ms>?)`, c.OperationID, c.OpeningID, c.TaskID, binding, original.Payload, original.Signature, time.Now().UnixMilli()).Scan(&present)
		if e != nil {
			return e
		}
		if !present || !c.ExpiresAt.After(time.Now()) || !authority.After(time.Now()) {
			return ErrStale
		}
		return g.Commit()
	})
}
