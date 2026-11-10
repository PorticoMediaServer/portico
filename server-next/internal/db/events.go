package db

import (
	"context"
	"database/sql"
)

// Event is a change as one viewer's app may hear of it: which thing to read again, by its
// public id. It never carries the thing itself. PublicID is empty for something deleted, and
// then LibraryID says which library's lists to read again.
type Event struct {
	ID        int64
	Topic     ChangeTopic
	Op        ChangeOp
	PublicID  string
	LibraryID string
}

// Events returns the changes after the cursor that this viewer may hear about: items and
// libraries it may see, and its own profile's state. next is the cursor to ask from next
// time, which moves on even when every change in between belonged to someone else. reread is
// true when the cursor is older than the oldest change kept: the app has missed something and
// must read everything it shows again.
func (d *DB) Events(ctx context.Context, v Viewer, after int64, limit int) (events []Event, next int64, reread bool, err error) {
	changes, reread, head, err := d.Changes(ctx, after, limit)
	if err != nil || reread {
		return nil, head, reread, err
	}
	next = after
	if len(changes) == 0 {
		return nil, next, false, nil
	}
	tx, err := d.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, next, false, err
	}
	defer tx.Rollback()
	acc, err := loadAccess(ctx, tx, v)
	if err != nil {
		return nil, next, false, err
	}
	item, err := tx.PrepareContext(ctx, `SELECT i.public_id, l.public_id FROM items i JOIN libraries l ON l.id = i.library_id
		WHERE i.id = ? AND `+acc.visible("i"))
	if err != nil {
		return nil, next, false, err
	}
	defer item.Close()
	library, err := tx.PrepareContext(ctx, `SELECT l.public_id FROM libraries l WHERE l.id = ? AND `+acc.libraryVisible("l.id"))
	if err != nil {
		return nil, next, false, err
	}
	defer library.Close()
	for _, c := range changes {
		next = c.ID
		e := Event{ID: c.ID, Topic: c.Topic, Op: c.Op}
		switch {
		case c.ProfileID != 0 && c.ProfileID != v.ProfileID:
			continue // another profile's state or access
		case c.Topic == TopicAccess, c.Topic == TopicPreferences:
			// The profile's own access or settings changed: nothing to look up.
		case (c.Topic == TopicItem || c.Topic == TopicState) && c.Op != OpDeleted:
			err = item.QueryRowContext(ctx, c.RefID).Scan(&e.PublicID, &e.LibraryID)
		case c.LibraryID != 0:
			err = library.QueryRowContext(ctx, c.LibraryID).Scan(&e.LibraryID)
			if c.Topic == TopicLibrary {
				e.PublicID = e.LibraryID
			}
		default:
			continue // a topic with no reader on this feed yet
		}
		if err == sql.ErrNoRows {
			err = nil
			continue // not this viewer's to see
		}
		if err != nil {
			return nil, next, false, err
		}
		events = append(events, e)
	}
	return events, next, false, nil
}
