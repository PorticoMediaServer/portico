// Package requestinput marks errors caused by caller-supplied values. Internal
// decoding, storage and dependency failures must remain ordinary errors.
package requestinput

import "errors"

type Error struct{ detail string }

func (e *Error) Error() string { return e.detail }
func New(detail string) error  { return &Error{detail: detail} }
func Is(err error) bool {
	var input *Error
	return errors.As(err, &input)
}
