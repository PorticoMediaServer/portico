package read

import (
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"strings"
)

func statementID(name string) stmt.ID { return stmt.ID("read.browse." + name) }

// Foreground statement names are a closed compiled recipe, never user SQL.
func readStatementID(name string) stmt.ID {
	if strings.HasPrefix(name, "read.") {
		return stmt.ID(name)
	}
	if strings.HasPrefix(name, "foreground.") || strings.HasPrefix(name, "full.") || strings.HasPrefix(name, "rs.") {
		return stmt.ID("read." + name)
	}
	return statementID(name)
}
