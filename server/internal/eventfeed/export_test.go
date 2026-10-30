package eventfeed

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// readerBinding is the binding Read derives for p, read outside the page.
func (h *Hub) readerBinding(ctx context.Context, p identity.Principal, administrative bool) (binding, error) {
	var device string
	err := h.DB.QueryRowContext(ctx, `SELECT device_id FROM identity_device_families b JOIN authorization_family_tokens t ON t.family_id=b.family_id WHERE t.token_hash=?`, p.Hash).Scan(&device)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return binding{}, err
	}
	return cursorBinding(p, device, administrative), nil
}

// cursorAt is the cursor this reader would hold at a ring position.
func (h *Hub) cursorAt(ctx context.Context, p identity.Principal, administrative bool, position int64) (string, error) {
	b, err := h.readerBinding(ctx, p, administrative)
	if err != nil {
		return "", err
	}
	return h.keys.seal(position, b), nil
}

// readAt reads from a ring position as the reader's own cursor would, and
// returns the page with every id and cursor opened back to its position, so a
// test can name positions. supplied=false is a first read with no cursor.
func (h *Hub) readAt(ctx context.Context, p identity.Principal, administrative bool, after int64, supplied bool) (Page, error) {
	cursor := ""
	if supplied {
		var err error
		if cursor, err = h.cursorAt(ctx, p, administrative, after); err != nil {
			return Page{}, err
		}
	}
	page, err := h.Read(ctx, p, administrative, cursor)
	if err != nil {
		return page, err
	}
	return h.decoded(ctx, p, administrative, page)
}

func (h *Hub) decoded(ctx context.Context, p identity.Principal, administrative bool, page Page) (Page, error) {
	b, err := h.readerBinding(ctx, p, administrative)
	if err != nil {
		return page, err
	}
	position := func(raw string) (string, error) {
		n, ok := h.keys.open(raw, b)
		if !ok {
			return "", fmt.Errorf("cursor %q does not open for its own reader", raw)
		}
		return strconv.FormatInt(n, 10), nil
	}
	out := page
	out.Events = make([]Event, len(page.Events))
	for i, event := range page.Events {
		if len(event.ID) > cursorMaxLength || !CursorWellFormed(event.ID) {
			return page, fmt.Errorf("event id %q is not a well-formed cursor", event.ID)
		}
		if event.ID, err = position(event.ID); err != nil {
			return page, err
		}
		out.Events[i] = event
	}
	if out.NextAfter, err = position(page.NextAfter); err != nil {
		return page, err
	}
	return out, nil
}
