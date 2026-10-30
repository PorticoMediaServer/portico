package recordingaccess

import (
	"database/sql"
	"encoding/json"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

func directLibraryAllowed(role, raw string, restriction sql.NullString, library string) bool {
	var libraries []string
	if json.Unmarshal([]byte(raw), &libraries) != nil {
		return false
	}
	var narrowed []string
	if restriction.Valid && json.Unmarshal([]byte(restriction.String), &narrowed) != nil {
		return false
	}
	// An owned Recorded TV library is authorized by its private-owner overlay.
	// Empty target still checks current membership/deletion/epoch above.
	if library == "" {
		return true
	}
	contains := func(ids []string) bool {
		for _, id := range ids {
			if id == library {
				return true
			}
		}
		return false
	}
	return (identity.Administrative(role) || contains(libraries)) && (!restriction.Valid || contains(narrowed))
}
