package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// A profile's recent searches are one of its preferences: a list of what was searched for,
// newest first. Another preference turns remembering off, which also hides what is kept.
const (
	prefSearchHistory  = "search.history"
	prefSearchRemember = "search.rememberHistory"
	searchHistorySize  = 20
)

func searchHistory(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, profileID int64) ([]string, error) {
	var remember, history sql.NullString
	err := q.QueryRowContext(ctx, `SELECT (SELECT value FROM preferences WHERE profile_id = @p AND key = @remember),
		(SELECT value FROM preferences WHERE profile_id = @p AND key = @history)`,
		sql.Named("p", profileID), sql.Named("remember", prefSearchRemember), sql.Named("history", prefSearchHistory)).Scan(&remember, &history)
	if err != nil || remember.String == "false" || !history.Valid {
		return nil, err
	}
	var out []string
	return out, json.Unmarshal([]byte(history.String), &out)
}

// SearchHistory returns the profile's recent searches, newest first, or none when the
// profile has turned remembering off.
func (d *DB) SearchHistory(ctx context.Context, v Viewer) ([]string, error) {
	return searchHistory(ctx, d.read, v.ProfileID)
}

// RememberSearch puts a search at the top of the profile's recent searches. A search already
// there moves up instead of appearing twice, whatever its case or spacing. Nothing is kept
// for a profile that has turned remembering off.
func (d *DB) RememberSearch(ctx context.Context, v Viewer, text string) error {
	text = strings.Join(strings.Fields(text), " ")
	if _, err := parseSearch(text); err != nil {
		return err
	}
	return d.write(ctx, User, func(tx *sql.Tx) error {
		var remember string
		err := tx.QueryRow(`SELECT value FROM preferences WHERE profile_id = ? AND key = ?`, v.ProfileID, prefSearchRemember).Scan(&remember)
		if err != nil && !errors.Is(err, sql.ErrNoRows) || remember == "false" {
			return err
		}
		old, err := searchHistory(ctx, tx, v.ProfileID)
		if err != nil {
			return err
		}
		kept := []string{text}
		for _, earlier := range old {
			if !strings.EqualFold(earlier, text) && len(kept) < searchHistorySize {
				kept = append(kept, earlier)
			}
		}
		return setSearchHistory(tx, v.ProfileID, kept)
	})
}

// ForgetSearches clears the profile's recent searches, or removes one of them when text is given.
func (d *DB) ForgetSearches(ctx context.Context, v Viewer, text string) error {
	return d.write(ctx, User, func(tx *sql.Tx) error {
		var kept []string
		if text = strings.Join(strings.Fields(text), " "); text != "" {
			old, err := searchHistory(ctx, tx, v.ProfileID)
			if err != nil {
				return err
			}
			for _, earlier := range old {
				if !strings.EqualFold(earlier, text) {
					kept = append(kept, earlier)
				}
			}
		}
		return setSearchHistory(tx, v.ProfileID, kept)
	})
}

func setSearchHistory(tx *sql.Tx, profileID int64, history []string) error {
	if len(history) == 0 {
		_, err := tx.Exec(`DELETE FROM preferences WHERE profile_id = ? AND key = ?`, profileID, prefSearchHistory)
		return err
	}
	raw, _ := json.Marshal(history)
	_, err := tx.Exec(`INSERT INTO preferences (profile_id, key, value, updated_at) VALUES (?, ?, ?, ?)
		ON CONFLICT DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		profileID, prefSearchHistory, string(raw), time.Now().UnixMilli())
	return err
}
