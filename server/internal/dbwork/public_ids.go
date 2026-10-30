package dbwork

import (
	"database/sql/driver"
	"fmt"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/entityid"
	"github.com/PorticoMediaServer/portico/server/internal/thirdparty/sqlite"
)

// pid(public_id) is a catalogue entity's public id and pid_blob(text) its
// stored form (NULL when malformed); see package entityid.
func init() {
	sqlite.MustRegisterDeterministicScalarFunction("pid", 1, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		raw, ok := args[0].([]byte)
		if !ok || len(raw) != entityid.Size {
			return nil, nil
		}
		return entityid.Encode(raw), nil
	})
	sqlite.MustRegisterDeterministicScalarFunction("pid_blob", 1, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		public, ok := args[0].(string)
		if strictIDs && (!ok && args[0] != nil || ok && integerText(public)) {
			// An integer entity id where a public id belongs matches nothing,
			// silently: the SQL returns no row and the caller reports not
			// found. Tests fail loudly instead.
			return nil, fmt.Errorf("pid_blob given an integer id (%v), not a public id", args[0])
		}
		if !ok {
			return nil, nil
		}
		raw, valid := entityid.Decode(public)
		if !valid {
			return nil, nil
		}
		return raw, nil
	})
}

// strictIDs is on in test binaries: there a public-id lookup given an integer
// (or its decimal text) is a bug in the caller, never a user's bad input.
var strictIDs = testing.Testing()

func integerText(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
