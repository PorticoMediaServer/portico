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
// code is replaced at once (the new code is returned for the log) and admits nothing by
// itself: the caller binds the setup token it then issues with AdmitSetupToken, so the
// address alone never admits anyone (a neighbour behind the same CGNAT or shared address
// reaches the same peer and origin, but never holds that token). A wrong code is
// ErrUnauthorized; a set-up server ErrConflict.
func (s *Service) AdmitSetupCode(ctx context.Context, code string) (next string, err error) {
	plain, ok := normalizedSetupCode(code)
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
		var err error
		next, err = s.rotateSetupCodeTx(ctx, tx)
		return err
	})
	if err != nil {
		return "", err
	}
	return next, nil
}

// AdmitSetupToken binds an outside admission to the setup token a correct setup code
// issued, to the peer it came from and the origin it reached (all three kept only as keyed
// hashes), for SetupOutsideAdmission. Later setup steps are admitted from outside only when
// they present that token from that peer at that origin.
func (s *Service) AdmitSetupToken(ctx context.Context, token, peer, origin string) error {
	if token == "" || peer == "" || origin == "" {
		return ErrUnauthorized
	}
	return dbwork.WithWriteTx(ctx, s.db, dbwork.ClassSecurityFence, func(tx *sql.Tx) error {
		var accounts int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM accounts`).Scan(&accounts); err != nil {
			return err
		}
		if accounts != 0 {
			return ErrConflict
		}
		now := time.Now()
		if _, err := tx.ExecContext(ctx, `DELETE FROM identity_setup_outside_admissions WHERE expires_ms<=?`, now.UnixMilli()); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO identity_setup_outside_admissions(token_hash,peer_hash,origin_hash,expires_ms) VALUES(?,?,?,?) ON CONFLICT(token_hash) DO UPDATE SET peer_hash=excluded.peer_hash,origin_hash=excluded.origin_hash,expires_ms=excluded.expires_ms`,
			s.setupHash("setup-admission", token), s.setupHash("setup-peer", peer), s.setupHash("setup-origin", origin), now.Add(SetupOutsideAdmission).UnixMilli())
		return err
	})
}

// SetupTokenAdmitted reports whether token was issued through a setup code to peer at
// origin, and that admission is live. The peer and origin alone never admit, and nothing
// is admitted once the server has an owner.
func (s *Service) SetupTokenAdmitted(ctx context.Context, token, peer, origin string) bool {
	if token == "" || peer == "" || origin == "" {
		return false
	}
	var admitted bool
	err := s.db.QueryRowContext(ctx, `SELECT NOT EXISTS(SELECT 1 FROM accounts) AND EXISTS(SELECT 1 FROM identity_setup_outside_admissions WHERE token_hash=? AND peer_hash=? AND origin_hash=? AND expires_ms>?)`,
		s.setupHash("setup-admission", token), s.setupHash("setup-peer", peer), s.setupHash("setup-origin", origin), time.Now().UnixMilli()).Scan(&admitted)
	return err == nil && admitted
}
