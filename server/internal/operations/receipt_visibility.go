package operations

import (
	"database/sql"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// ReauthorizeReceipt projects a stored outcome through current item authority.
// It only changes the decoded response; callers retain the original receipt and
// never reapply its mutations. Storage/cancellation errors must fail the replay,
// rather than being mistaken for a hidden item or returning unchecked state.
func ReauthorizeReceipt[T any](entries []T, itemID func(T) string, authorize func(string) error, redact func(*T)) error {
	if authorize == nil {
		return nil
	}
	for i := range entries {
		err := authorize(itemID(entries[i]))
		if err == nil {
			continue
		}
		if errors.Is(err, sql.ErrNoRows) || errors.Is(err, identity.ErrContentRestricted) || errors.Is(err, identity.ErrNotVisible) || errors.Is(err, identity.ErrUnauthorized) || errors.Is(err, identity.ErrForbidden) {
			redact(&entries[i])
			continue
		}
		return err
	}
	return nil
}
