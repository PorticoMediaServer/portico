package read

import (
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"testing"
)

func TestRSStatementNamesPreserveCapturedNamespace(t *testing.T) {
	for name, want := range map[string]stmt.ID{
		"rs.people.credit_octets":             "read.browse.rs.people.credit_octets",
		"read.browse.rs.people.credit_octets": "read.browse.rs.people.credit_octets",
		"full.native.item":                    "read.full.native.item",
		"read.full.native.item":               "read.full.native.item",
	} {
		if got := readStatementID(name); got != want {
			t.Fatalf("%s: %s != %s", name, got, want)
		}
	}
}
