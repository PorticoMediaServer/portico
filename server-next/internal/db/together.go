package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// GroupTitle is a title a Watch Together group can play.
type GroupTitle struct {
	ID       int64
	PublicID string
	Kind     ItemKind
	Title    string
}

// GroupTitles returns, in the order asked, those of the titles the viewer may see that a
// group can play: films, episodes, extras, songs and audiobooks. Live channels and books are
// not titles a group plays.
func (d *DB) GroupTitles(ctx context.Context, v Viewer, publicIDs []string) ([]GroupTitle, error) {
	if len(publicIDs) == 0 {
		return nil, nil
	}
	tx, err := d.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	acc, err := loadAccess(ctx, tx, v)
	if err != nil {
		return nil, err
	}
	kinds := fmt.Sprintf("(%d, %d, %d, %d, %d)", KindMovie, KindEpisode, KindExtra, KindTrack, KindAudiobook)
	find, err := tx.PrepareContext(ctx, `SELECT i.id, i.kind, i.title FROM items i WHERE i.public_id = @id AND i.kind IN `+kinds+` AND `+
		acc.visible("i"))
	if err != nil {
		return nil, err
	}
	defer find.Close()
	var out []GroupTitle
	for _, publicID := range publicIDs {
		t := GroupTitle{PublicID: publicID}
		err := find.QueryRowContext(ctx, append(v.args(), sql.Named("id", publicID))...).Scan(&t.ID, &t.Kind, &t.Title)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, nil
}

// GroupMember is how a profile appears in a group, and whether it may be in one at all: both
// its own Watch Together switch and its account's must be on.
type GroupMember struct {
	Username    string
	ProfileName string
	Allowed     bool
}

// GroupMemberOf reads a profile's name, its account's username and its Watch Together switches.
func (d *DB) GroupMemberOf(ctx context.Context, profileID int64) (GroupMember, error) {
	var out GroupMember
	err := d.read.QueryRowContext(ctx, `SELECT u.username, p.name, u.allow_watch_together AND p.allow_watch_together
		FROM profiles p JOIN users u ON u.id = p.user_id WHERE p.id = ?`, profileID).Scan(&out.Username, &out.ProfileName, &out.Allowed)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrNotFound
	}
	return out, err
}
