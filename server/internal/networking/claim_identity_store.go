package networking

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"math"
	"time"
)

// DurableIdentity is root's exact protected-key publication projection. Root
// must have synced and reopened this key incarnation before staging its pointer.
// No private bytes are stored here. This is not accepted from an HTTP body.
type DurableIdentity struct {
	ServerID       string
	PublicKey      []byte
	KeyIncarnation string
}
type IdentitySnapshot struct {
	DurableIdentity
	ResetGeneration int64
}

func validDurableIdentity(v DurableIdentity) bool {
	id, e := ServerIdentity(v.PublicKey)
	return e == nil && id == v.ServerID && validID(v.KeyIncarnation)
}
func sameIdentity(a, b DurableIdentity) bool {
	return a.ServerID == b.ServerID && a.KeyIncarnation == b.KeyIncarnation && bytes.Equal(a.PublicKey, b.PublicKey)
}
func readCurrentIdentityTx(ctx context.Context, tx *sql.Tx) (IdentitySnapshot, sql.NullString, sql.NullString, error) {
	var v IdentitySnapshot
	var active, installed sql.NullString
	e := tx.QueryRowContext(ctx, `SELECT i.server_id,i.public_key,k.key_incarnation,i.reset_generation,i.active_operation_id,i.installed_operation_id FROM networking_claim_identity i JOIN networking_server_identities k ON k.server_id=i.server_id AND k.public_key=i.public_key WHERE i.singleton=1`).Scan(&v.ServerID, &v.PublicKey, &v.KeyIncarnation, &v.ResetGeneration, &active, &installed)
	if e != nil {
		return v, active, installed, e
	}
	if !validDurableIdentity(v.DurableIdentity) || v.ResetGeneration < 0 {
		return v, active, installed, ErrInvalid
	}
	if installed.Valid && (!active.Valid || installed.String != active.String) {
		return v, active, installed, ErrInvalid
	}
	if active.Valid {
		var server, stage string
		var key []byte
		var generation int64
		if e = tx.QueryRowContext(ctx, `SELECT server_id,public_key,local_generation,stage FROM networking_claim_intents WHERE operation_id=?`, active.String).Scan(&server, &key, &generation, &stage); e != nil {
			return v, active, installed, ErrInvalid
		}
		if server != v.ServerID || !bytes.Equal(key, v.PublicKey) || generation < 0 || stage == "cancelled" {
			return v, active, installed, ErrInvalid
		}
		if stage == "cancel_pending" {
			if generation == math.MaxInt64 || v.ResetGeneration != generation+1 {
				return v, active, installed, ErrInvalid
			}
		} else if v.ResetGeneration != generation {
			return v, active, installed, ErrInvalid
		}
		if installed.Valid && stage != "installed" {
			return v, active, installed, ErrInvalid
		}
	}
	return v, active, installed, nil
}

// StageIdentityTx never commits. Root MUST hold the operations AuthorityGate
// lease across key publication and this transaction, update its sole canonical
// application identity in this same transaction (or remain held), and use
// Lease.Commit(tx.Commit). A staged return alone is not a published identity.
// The transaction acquires SQLite writer ownership before reading any pointer.
func StageIdentityTx(ctx context.Context, tx *sql.Tx, expected *IdentitySnapshot, next DurableIdentity) (IdentitySnapshot, error) {
	if tx == nil || !validDurableIdentity(next) {
		return IdentitySnapshot{}, ErrInvalid
	}
	if e := ctx.Err(); e != nil {
		return IdentitySnapshot{}, e
	}
	var version int
	if e := tx.QueryRowContext(ctx, `SELECT version FROM networking_claim_schema WHERE singleton=1`).Scan(&version); e != nil || version != 3 {
		return IdentitySnapshot{}, ErrInvalid
	}
	if _, e := tx.ExecContext(ctx, `UPDATE networking_claim_identity SET reset_generation=reset_generation WHERE singleton=1`); e != nil {
		return IdentitySnapshot{}, e
	}
	current, active, installed, e := readCurrentIdentityTx(ctx, tx)
	if expected == nil {
		if !errors.Is(e, sql.ErrNoRows) {
			if e != nil {
				return IdentitySnapshot{}, e
			}
			return IdentitySnapshot{}, ErrStale
		}
		var histories int
		if e = tx.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM networking_server_identities)+(SELECT count(*) FROM networking_claim_intents)+(SELECT count(*) FROM networking_claim_cancellations)`).Scan(&histories); e != nil {
			return IdentitySnapshot{}, e
		}
		if histories != 0 {
			return IdentitySnapshot{}, ErrStale
		}
		current = IdentitySnapshot{DurableIdentity: next, ResetGeneration: 0}
	} else {
		if e != nil {
			if errors.Is(e, sql.ErrNoRows) {
				return IdentitySnapshot{}, ErrStale
			}
			return IdentitySnapshot{}, e
		}
		if !validDurableIdentity(expected.DurableIdentity) || expected.ResetGeneration < 0 || !sameIdentity(current.DurableIdentity, expected.DurableIdentity) || current.ResetGeneration != expected.ResetGeneration {
			return IdentitySnapshot{}, ErrStale
		}
		if sameIdentity(current.DurableIdentity, next) {
			return current, ctx.Err()
		}
		if active.Valid || installed.Valid || current.ResetGeneration == math.MaxInt64 || next.ServerID == current.ServerID {
			return IdentitySnapshot{}, ErrStale
		}
		var retained int
		if e = tx.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM networking_claim_cancellations WHERE state='pending')+(SELECT count(*) FROM networking_claim_credentials)+(SELECT count(*) FROM networking_claim_intents WHERE stage<>'cancelled')`).Scan(&retained); e != nil {
			return IdentitySnapshot{}, e
		}
		if retained != 0 {
			return IdentitySnapshot{}, ErrStale
		}
		current = IdentitySnapshot{DurableIdentity: next, ResetGeneration: current.ResetGeneration + 1}
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO networking_server_identities(server_id,public_key,key_incarnation,created_at) VALUES(?,?,?,?)`, next.ServerID, next.PublicKey, next.KeyIncarnation, time.Now().UTC().Format(time.RFC3339Nano)); e != nil {
		return IdentitySnapshot{}, e
	}
	if expected == nil {
		_, e = tx.ExecContext(ctx, `INSERT INTO networking_claim_identity(singleton,server_id,public_key,reset_generation) VALUES(1,?,?,0)`, next.ServerID, next.PublicKey)
	} else {
		_, e = tx.ExecContext(ctx, `UPDATE networking_claim_identity SET server_id=?,public_key=?,reset_generation=? WHERE singleton=1`, next.ServerID, next.PublicKey, current.ResetGeneration)
	}
	if e != nil {
		return IdentitySnapshot{}, e
	}
	if e = ctx.Err(); e != nil {
		return IdentitySnapshot{}, e
	}
	current.PublicKey = append([]byte(nil), current.PublicKey...)
	return current, nil
}

// CurrentIdentityTx returns the current public/durable-key reference under root's
// transaction. It does not issue a signing or credential-use permit; callers
// must hold and recheck the single operations lifecycle lease separately.
func CurrentIdentityTx(ctx context.Context, tx *sql.Tx) (IdentitySnapshot, error) {
	if tx == nil {
		return IdentitySnapshot{}, ErrInvalid
	}
	if e := ctx.Err(); e != nil {
		return IdentitySnapshot{}, e
	}
	v, _, _, e := readCurrentIdentityTx(ctx, tx)
	if errors.Is(e, sql.ErrNoRows) {
		return IdentitySnapshot{}, ErrStale
	}
	if e != nil {
		return IdentitySnapshot{}, e
	}
	v.PublicKey = append([]byte(nil), v.PublicKey...)
	return v, ctx.Err()
}

// IdentityCodeTx derives the presentation value from the current protected-key
// publication in the caller's transaction. The caller retains its existing
// sign-in kind, main-profile and permission checks; this stores no setting.
func IdentityCodeTx(ctx context.Context, tx *sql.Tx) (string, error) {
	current, e := CurrentIdentityTx(ctx, tx)
	if e != nil {
		return "", e
	}
	code := IdentityCode(current.ServerID)
	if code == "" {
		return "", ErrInvalid
	}
	return code, nil
}
