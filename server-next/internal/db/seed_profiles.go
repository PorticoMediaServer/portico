package db

import (
	"context"
	"database/sql"
	"time"
)

// SeedProfile describes a member with one profile, for the seeder and for tests. Sign-in owns
// real accounts; an account made here has no password and cannot sign in.
type SeedProfile struct {
	Name         string
	MaxRatingAge *int     // nil is no rating limit
	Libraries    []int64  // nil is every library; otherwise only these
	BlockedTags  []string // public ids of tags the profile must never be shown
}

// CreateSeedProfile adds the account and its profile.
func (d *DB) CreateSeedProfile(ctx context.Context, p SeedProfile) (Viewer, error) {
	var v Viewer
	now := time.Now().UnixMilli()
	err := d.write(ctx, Background, func(tx *sql.Tx) error {
		res, err := tx.Exec(`INSERT INTO users (public_id, username, role, created_at, updated_at)
			VALUES (?, ?, 'member', ?, ?)`, NewID("usr"), p.Name, now, now)
		if err != nil {
			return err
		}
		v.UserID, _ = res.LastInsertId()
		res, err = tx.Exec(`INSERT INTO profiles (public_id, user_id, name, max_rating_age, all_libraries, access_changed_at, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, NewID("prf"), v.UserID, p.Name, p.MaxRatingAge, p.Libraries == nil, now, now, now)
		if err != nil {
			return err
		}
		v.ProfileID, _ = res.LastInsertId()
		for _, library := range p.Libraries {
			if _, err := tx.Exec(`INSERT INTO library_access (library_id, profile_id) VALUES (?, ?)`, library, v.ProfileID); err != nil {
				return err
			}
		}
		for _, tag := range p.BlockedTags {
			if _, err := tx.Exec(`INSERT INTO profile_restrictions (profile_id, tag_id) SELECT ?, id FROM tags WHERE public_id = ?`,
				v.ProfileID, tag); err != nil {
				return err
			}
		}
		return nil
	})
	return v, err
}

// SeedViewers returns the profiles CreateSeedProfile made, oldest first, so a load run can
// read and write as them.
func (d *DB) SeedViewers(ctx context.Context) ([]Viewer, error) {
	rows, err := d.read.QueryContext(ctx, `SELECT p.user_id, p.id FROM profiles p JOIN users u ON u.id = p.user_id
		WHERE u.password_hash IS NULL AND u.role = 'member' ORDER BY p.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Viewer
	for rows.Next() {
		var v Viewer
		if err := rows.Scan(&v.UserID, &v.ProfileID); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
