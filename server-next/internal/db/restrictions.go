package db

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// ErrUnknownLabel: a label named in the request does not exist.
var ErrUnknownLabel = errors.New("unknown label")

// Restrictions are what a profile is kept from seeing. They only narrow what its account may
// see: a library the account is not given stays out of reach whatever the profile's own list says.
type Restrictions struct {
	// MaxRatingAge is the highest age rating shown, or nil for no limit. Under a limit a
	// title with no rating is hidden unless AllowUnrated is set.
	MaxRatingAge *int
	AllowUnrated bool
	// BlockedLabels are the public ids of labels whose titles are never shown.
	BlockedLabels []string
	// AllLibraries, or else Libraries (public ids), says which libraries the profile is given.
	AllLibraries bool
	Libraries    []string
	// AccountAllLibraries and AccountLibraries are what the account itself is given, so a
	// screen can say where the account is the stricter of the two.
	AccountAllLibraries bool
	AccountLibraries    []string
	// AllowDownloads lets the profile download, within its account's own switch
	// (AccountAllowDownloads, read-only here). Nil in a change leaves it as it is.
	AllowDownloads        *bool
	AccountAllowDownloads bool
	// AccountMaxRatingAge, AccountAllowUnrated and AccountBlockedLabels are what the whole
	// account is held to (see AccountLimits). They apply as well as the profile's own.
	AccountMaxRatingAge  *int
	AccountAllowUnrated  bool
	AccountBlockedLabels []string
}

func publicIDs(q interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}, query string, args ...any) ([]string, error) {
	rows, err := q.QueryContext(context.Background(), query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// labelIDs resolves the public ids of labels, refusing any that does not exist.
func labelIDs(tx *sql.Tx, publicIDs []string) ([]int64, error) {
	var out []int64
	for _, label := range publicIDs {
		var tag int64
		err := tx.QueryRow(`SELECT id FROM tags WHERE public_id = ? AND kind = ?`, label, TagLabel).Scan(&tag)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrUnknownLabel
		}
		if err != nil {
			return nil, err
		}
		out = append(out, tag)
	}
	return out, nil
}

func restrictions(tx *sql.Tx, userID int64, publicID string) (int64, Restrictions, error) {
	var out Restrictions
	var id int64
	var allowDownloads bool
	err := tx.QueryRow(`SELECT p.id, p.max_rating_age, p.allow_unrated, p.all_libraries, u.all_libraries, p.allow_downloads, u.allow_downloads,
			u.max_rating_age, u.allow_unrated
		FROM profiles p JOIN users u ON u.id = p.user_id WHERE p.user_id = ? AND p.public_id = ?`, userID, publicID).
		Scan(&id, &out.MaxRatingAge, &out.AllowUnrated, &out.AllLibraries, &out.AccountAllLibraries, &allowDownloads, &out.AccountAllowDownloads,
			&out.AccountMaxRatingAge, &out.AccountAllowUnrated)
	out.AllowDownloads = &allowDownloads
	if errors.Is(err, sql.ErrNoRows) {
		return 0, out, ErrNotFound
	}
	if err != nil {
		return 0, out, err
	}
	if out.BlockedLabels, err = publicIDs(tx, `SELECT t.public_id FROM profile_restrictions r JOIN tags t ON t.id = r.tag_id WHERE r.profile_id = ? ORDER BY t.norm_name`, id); err != nil {
		return 0, out, err
	}
	if out.Libraries, err = publicIDs(tx, `SELECT l.public_id FROM library_access a JOIN libraries l ON l.id = a.library_id WHERE a.profile_id = ? ORDER BY l.id`, id); err != nil {
		return 0, out, err
	}
	if out.AccountLibraries, err = publicIDs(tx, `SELECT l.public_id FROM library_access a JOIN libraries l ON l.id = a.library_id WHERE a.user_id = ? ORDER BY l.id`, userID); err != nil {
		return 0, out, err
	}
	out.AccountBlockedLabels, err = publicIDs(tx, `SELECT t.public_id FROM account_restrictions r JOIN tags t ON t.id = r.tag_id WHERE r.user_id = ? ORDER BY t.norm_name`, userID)
	return id, out, err
}

// ProfileRestrictions returns what one of the account's profiles is kept from seeing.
func (d *DB) ProfileRestrictions(ctx context.Context, userID int64, publicID string) (Restrictions, error) {
	var out Restrictions
	tx, err := d.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	_, out, err = restrictions(tx, userID, publicID)
	return out, err
}

// SetProfileRestrictions replaces what a profile is kept from seeing. The profile's open
// apps are told at once to throw away what they have cached; nobody is signed out. An
// unknown library is ErrUnknownLibrary and an unknown label ErrUnknownLabel.
func (d *DB) SetProfileRestrictions(ctx context.Context, userID int64, publicID string, r Restrictions, now time.Time) (Restrictions, error) {
	var out Restrictions
	err := d.write(ctx, User, func(tx *sql.Tx) error {
		id, _, err := restrictions(tx, userID, publicID)
		if err != nil {
			return err
		}
		libraries, err := libraryIDs(tx, r.Libraries)
		if err != nil {
			return err
		}
		labels, err := labelIDs(tx, r.BlockedLabels)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE profiles SET max_rating_age = ?, allow_unrated = ?, all_libraries = ?, allow_downloads = coalesce(?, allow_downloads),
			updated_at = ? WHERE id = ?`, r.MaxRatingAge, r.AllowUnrated, r.AllLibraries, r.AllowDownloads, now.UnixMilli(), id); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM profile_restrictions WHERE profile_id = ?`, id); err != nil {
			return err
		}
		for _, tag := range labels {
			if _, err := tx.Exec(`INSERT INTO profile_restrictions (profile_id, tag_id) VALUES (?, ?) ON CONFLICT DO NOTHING`, id, tag); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(`DELETE FROM library_access WHERE profile_id = ?`, id); err != nil {
			return err
		}
		for _, library := range libraries {
			if _, err := tx.Exec(`INSERT INTO library_access (library_id, profile_id) VALUES (?, ?) ON CONFLICT DO NOTHING`, library, id); err != nil {
				return err
			}
		}
		if err := accessChanged(tx, now, `id = ?`, id); err != nil {
			return err
		}
		_, out, err = restrictions(tx, userID, publicID)
		return err
	})
	return out, err
}
