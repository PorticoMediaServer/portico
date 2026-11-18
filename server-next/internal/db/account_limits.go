package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/auth"
)

// Where the limits an account is held to (AccountLimits) are applied to signing in and to
// playing. What they hide from browsing is in viewer.go.

var (
	// ErrOutsideHours: the account has viewing hours and this is not one of them. Nothing was
	// changed: a sign-in already held is kept for later.
	ErrOutsideHours = errors.New("outside the account's viewing hours")
	// ErrAwayFromHome: the account is limited to the home network and the request came from
	// somewhere else. Nothing was changed: a sign-in already held is kept for later.
	ErrAwayFromHome = errors.New("the account is limited to the home network")
)

// accountOpen says whether an account may be used now from where the request came:
// ErrAwayFromHome, ErrOutsideHours or nil. Every way of getting a token asks it (a new
// sign-in, a renewal, choosing a profile) and so does every request made with a key.
func accountOpen(ctx context.Context, q queryer, userID int64, atHome bool, now time.Time) error {
	var homeOnly bool
	var stored sql.NullString
	if err := q.QueryRowContext(ctx, `SELECT home_only, viewing_hours FROM users WHERE id = ?`, userID).Scan(&homeOnly, &stored); err != nil {
		return err
	}
	if homeOnly && !atHome {
		return ErrAwayFromHome
	}
	if stored.Valid {
		var hours auth.ViewingHours
		if err := json.Unmarshal([]byte(stored.String), &hours); err != nil {
			return err
		}
		if open, _ := hours.Open(now); !open {
			return ErrOutsideHours
		}
	}
	return nil
}

// AccountOpen is accountOpen for a request made with a key.
func (d *DB) AccountOpen(ctx context.Context, userID int64, atHome bool, now time.Time) error {
	return accountOpen(ctx, d.read, userID, atHome, now)
}
