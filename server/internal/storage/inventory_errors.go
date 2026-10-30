package storage

import (
	"encoding/json"
	"errors"
	"io"
	"os"
)

// Process exit text is not evidence that a folder was removed. The helper
// returns closed error codes so all platforms can widen a missing target to its
// readable parent without confusing a permission error with absence.
func inventoryErrorCode(err error) string {
	switch {
	case errors.Is(err, os.ErrNotExist):
		return "not_found"
	case errors.Is(err, os.ErrPermission):
		return "unreadable"
	case errors.Is(err, ErrInventoryChanged):
		return "changed"
	}
	return "unreadable"
}
func inventoryCodeError(code string) error {
	switch code {
	case "":
		return nil
	case "not_found":
		return os.ErrNotExist
	case "changed":
		return ErrInventoryChanged
	case "unreadable":
		return os.ErrPermission
	}
	return ErrInventoryChanged
}
func inventoryFailure(err error, out io.Writer) error {
	return json.NewEncoder(out).Encode(InventoryPage{ErrorCode: inventoryErrorCode(err)})
}
