package dbwork

import (
	"database/sql/driver"
	"encoding/json"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/thirdparty/sqlite"
)

// This callback only transforms its text arguments using the current
// connection's public tokenizer API. Its internal SELECT fts5(?1) retrieves a
// pointer, reads no document/table and cannot write. It acquires no other pooled
// connection, registers no extension, and performs no external I/O. Consequently
// the statementHead.plainSelect exact-wake exemption still holds.
func init() {
	sqlite.MustRegisterDeterministicScalarFunction("portico_search_match", 3, func(ctx *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		search, searchOK := args[0].(string)
		kind, kindOK := args[1].(string)
		encoded, queryOK := args[2].(string)
		if !searchOK || !kindOK || !queryOK {
			return nil, errors.New("portico_search_match requires three TEXT arguments")
		}
		// Escaping can expand every raw byte sixfold. Bound JSON parsing itself
		// before allocation; parent charges JSON/binding/serialization separately.
		if len(encoded) > 6*sqlite.SearchMatchMaxQueryBytes+3*sqlite.SearchMatchMaxPhrases+2 {
			return nil, sqlite.ErrSearchMatchCapacity
		}
		var phrases []string
		if err := json.Unmarshal([]byte(encoded), &phrases); err != nil {
			return nil, err
		}
		if phrases == nil {
			return nil, errors.New("portico_search_match requires a JSON string array")
		}
		result, err := ctx.MatchSearchDocument(search, kind, phrases)
		if err != nil {
			return nil, err
		}
		out, err := json.Marshal(result)
		return string(out), err
	})
}
