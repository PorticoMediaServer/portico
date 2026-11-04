package reading

import (
	"errors"
	"fmt"
)

// ErrMemberUnreadable identifies a rejected member's content, independently
// of source/currentness failures that must retire the publication session.
var ErrMemberUnreadable = errors.New("reading member unreadable")

func unreadableMember(err error) error {
	return fmt.Errorf("%w: %w", ErrMemberUnreadable, err)
}

func memberReadError(err error) error {
	if errors.Is(err, ErrLimit) {
		return unreadableMember(err)
	}
	return err
}
