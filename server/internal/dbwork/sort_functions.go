package dbwork

import (
	"database/sql/driver"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/facettext"
	"github.com/PorticoMediaServer/portico/server/internal/sorttext"
	"github.com/PorticoMediaServer/portico/server/internal/thirdparty/sqlite"
)

func init() {
	sqlite.MustRegisterDeterministicScalarFunction("portico_fold_text", 1, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		if args[0] == nil {
			return nil, nil
		}
		value, _ := args[0].(string)
		return sorttext.Fold(value), nil
	})
	sqlite.MustRegisterDeterministicScalarFunction("portico_search_text", 1, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		value, _ := args[0].(string)
		return facettext.SearchDocument(value), nil
	})
	sqlite.MustRegisterDeterministicScalarFunction("portico_search_prefixes", 2, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		document, _ := args[0].(string)
		title, _ := args[1].(string)
		return facettext.SearchPrefixes(document, title), nil
	})
	sqlite.MustRegisterDeterministicScalarFunction("portico_search_fold", 1, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		value, _ := args[0].(string)
		return facettext.SearchFold(value), nil
	})
	sqlite.MustRegisterDeterministicScalarFunction("portico_sort_title", 3, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		title, _ := args[0].(string)
		explicit, _ := args[1].(string)
		language, _ := args[2].(string)
		return sorttext.Key(title, explicit, language), nil
	})
	// SQLite's lower() folds ASCII only; an author's identity key folds as Go
	// does, so SQL that names an author uses this.
	sqlite.MustRegisterDeterministicScalarFunction("portico_author_key", 1, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		name, _ := args[0].(string)
		return AuthorIdentityKey(name), nil
	})
	sqlite.MustRegisterDeterministicScalarFunction("portico_letter", 1, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		title, _ := args[0].(string)
		return sorttext.Letter(title), nil
	})
}

// AuthorIdentityKey is an author's identity key (compactcatalog.AuthorKey).
func AuthorIdentityKey(name string) string { return "author:" + strings.ToLower(name) }
