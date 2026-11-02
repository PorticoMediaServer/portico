package identity

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// SetupCodeLogPrefix starts the one log line that shows the setup code, so Docker and
// Unraid users can find it with grep and the docs can quote it.
const SetupCodeLogPrefix = "Portico setup code:"

// SetupOutsideAdmission is how long a correct setup code admits its outside peer for setup.
const SetupOutsideAdmission = 30 * time.Minute

const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// SetupCodeLogLine is the log line that shows code.
func SetupCodeLogLine(code string) string {
	return SetupCodeLogPrefix + " " + code + " (needed only to set up from outside this server's network)"
}

// newSetupCode is 8 Crockford base32 characters (40 bits), shown as XXXX-XXXX.
func newSetupCode() (string, error) {
	raw := make([]byte, 5)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	bits := uint64(raw[0])<<32 | uint64(raw[1])<<24 | uint64(raw[2])<<16 | uint64(raw[3])<<8 | uint64(raw[4])
	out := make([]byte, 8)
	for i := 7; i >= 0; i-- {
		out[i] = crockford[bits&31]
		bits >>= 5
	}
	return string(out[:4]) + "-" + string(out[4:]), nil
}

// normalizedSetupCode reads a typed code the Crockford way: case, spaces and hyphens
// don't matter, and I, L and O read as 1, 1 and 0.
func normalizedSetupCode(code string) (string, bool) {
	var b strings.Builder
	for _, r := range strings.ToUpper(code) {
		switch {
		case r == '-' || r == ' ':
			continue
		case r == 'I' || r == 'L':
			r = '1'
		case r == 'O':
			r = '0'
		}
		if !strings.ContainsRune(crockford, r) {
			return "", false
		}
		b.WriteRune(r)
	}
	return b.String(), b.Len() == 8
}

// RotateSetupCode makes a new setup code while the server is unconfigured and returns it
// for the log; the earlier one stops working. It returns "" once the server has an owner.
func (s *Service) RotateSetupCode(ctx context.Context) (string, error) {
	var code string
	err := dbwork.WithWriteTx(ctx, s.db, dbwork.ClassSecurityFence, func(tx *sql.Tx) error {
		var err error
		code, err = s.rotateSetupCodeTx(ctx, tx)
		return err
	})
	return code, err
}

func (s *Service) rotateSetupCodeTx(ctx context.Context, tx *sql.Tx) (string, error) {
	var accounts int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM accounts`).Scan(&accounts); err != nil || accounts != 0 {
		return "", err
	}
	code, err := newSetupCode()
	if err != nil {
		return "", err
	}
	plain, _ := normalizedSetupCode(code)
	_, err = tx.ExecContext(ctx, `INSERT INTO identity_setup_code(singleton,code_hash,created_ms) VALUES(1,?,?) ON CONFLICT(singleton) DO UPDATE SET code_hash=excluded.code_hash,created_ms=excluded.created_ms`, s.setupHash("setup-code", plain), time.Now().UnixMilli())
	return code, err
}

// AdmitSetupCode checks a setup code typed from outside the server's network. A correct
// code admits peer at origin (both kept only as keyed hashes) for setup for
// SetupOutsideAdmission, and is replaced at once: the new code is returned for the log.
// The origin binds the admission to the address the code was typed at, so a page that
// rebinds another name to this server from the same peer is not admitted. A wrong code
// is ErrUnauthorized; a set-up server ErrConflict.
func (s *Service) AdmitSetupCode(ctx context.Context, code, peer, origin string) (next string, err error) {
	plain, ok := normalizedSetupCode(code)
	if peer == "" || origin == "" {
		return "", ErrUnauthorized
	}
	err = dbwork.WithWriteTx(ctx, s.db, dbwork.ClassSecurityFence, func(tx *sql.Tx) error {
		var accounts int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM accounts`).Scan(&accounts); err != nil {
			return err
		}
		if accounts != 0 {
			return ErrConflict
		}
		var stored string
		if err := tx.QueryRowContext(ctx, `SELECT code_hash FROM identity_setup_code WHERE singleton=1`).Scan(&stored); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrUnauthorized
			}
			return err
		}
		if !ok || subtle.ConstantTimeCompare([]byte(stored), []byte(s.setupHash("setup-code", plain))) != 1 {
			return ErrUnauthorized
		}
		now := time.Now()
		if _, err := tx.ExecContext(ctx, `DELETE FROM identity_setup_outside_peers WHERE expires_ms<=?`, now.UnixMilli()); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO identity_setup_outside_peers(peer_hash,origin_hash,expires_ms) VALUES(?,?,?) ON CONFLICT(peer_hash) DO UPDATE SET origin_hash=excluded.origin_hash,expires_ms=excluded.expires_ms`, s.setupHash("setup-peer", peer), s.setupHash("setup-origin", origin), now.Add(SetupOutsideAdmission).UnixMilli()); err != nil {
			return err
		}
		var err error
		next, err = s.rotateSetupCodeTx(ctx, tx)
		return err
	})
	if err != nil {
		return "", err
	}
	return next, nil
}

// SetupPeerAdmitted reports whether a setup code admitted peer at origin and that
// admission is live. Nothing is admitted once the server has an owner.
func (s *Service) SetupPeerAdmitted(ctx context.Context, peer, origin string) bool {
	if peer == "" || origin == "" {
		return false
	}
	var admitted bool
	err := s.db.QueryRowContext(ctx, `SELECT NOT EXISTS(SELECT 1 FROM accounts) AND EXISTS(SELECT 1 FROM identity_setup_outside_peers WHERE peer_hash=? AND origin_hash=? AND expires_ms>?)`, s.setupHash("setup-peer", peer), s.setupHash("setup-origin", origin), time.Now().UnixMilli()).Scan(&admitted)
	return err == nil && admitted
}
