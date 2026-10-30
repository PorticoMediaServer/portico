package reading

// truncatePage shortens a page to its first n entries and replaces its
// continuation with the one after the last entry kept. Callers bound response
// bytes this way; a short page with a cursor is an ordinary page.
func truncatePage[T any](entries *[]T, next *string, cursorAt func(int) (string, error), n int) error {
	if n < 1 || n > len(*entries) || cursorAt == nil {
		return ErrInvalid
	}
	if n == len(*entries) {
		return nil
	}
	cursor, err := cursorAt(n - 1)
	if err != nil {
		return err
	}
	*entries, *next = (*entries)[:n], cursor
	return nil
}
