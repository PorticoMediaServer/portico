package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// What a queue was started from, by kind: one title or container (a show, season, album,
// artist or audiobook), a playlist or collection, one of the profile's Mixes, a radio started
// from a song, album or artist, titles named one by one, or a library view or Home row.
const (
	QueueFromContainer = "container"
	QueueFromList      = "list"
	QueueFromMix       = "mix"
	QueueFromRadio     = "radio"
	QueueFromItems     = "items"
	QueueFromView      = "view"
)

// QueueSourceRef is what a queue was started from: a kind and, for the kinds that name one, the
// public id of what it was. Kind is "" for a queue started before sources were kept.
type QueueSourceRef struct {
	Kind string `json:"kind"`
	ID   string `json:"id,omitempty"`
}

// Text is the source as stored with the queue.
func (r QueueSourceRef) Text() string {
	if r.Kind == "" {
		return ""
	}
	raw, _ := json.Marshal(r)
	return string(raw)
}

// queueSource reads a queue's stored source and names it as the viewer may see it now: a
// title by its title and a list by its name, only while the viewer may see them, and a radio by
// what it started from. A mix, named titles and a view have no name of their own here.
func queueSource(ctx context.Context, tx *sql.Tx, v Viewer, acc access, text string) (QueueSourceRef, string, error) {
	var ref QueueSourceRef
	if text == "" || json.Unmarshal([]byte(text), &ref) != nil {
		return QueueSourceRef{}, "", nil
	}
	var label string
	var err error
	switch ref.Kind {
	case QueueFromContainer, QueueFromRadio:
		err = tx.QueryRowContext(ctx, `SELECT i.title FROM items i WHERE i.public_id = @id AND `+acc.visible("i"),
			append(v.args(), sql.Named("id", ref.ID))...).Scan(&label)
	case QueueFromList:
		err = tx.QueryRowContext(ctx, `SELECT l.name FROM lists l WHERE l.public_id = @id AND `+acc.readable(),
			append(v.args(), sql.Named("id", ref.ID))...).Scan(&label)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return ref, "", nil
	}
	return ref, label, err
}
