package read

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// Inert exact domain metadata. Actual Reader Capture audits native builtins;
// returned source identity is measured on the executing physical connection.
func nativeEngineDefinition() stmt.Definition {
	return stmt.Definition{ID: "read.full.native.engine", SQL: "SELECT sqlite_version(),sqlite_source_id() FROM search_revision WHERE singleton=?", Owner: "store/read", CallSites: []string{"store/read/native_full_search.go:NativeFullSearchReader.Read"}, Kind: stmt.Read, Params: []stmt.Param{{Name: "singleton", Type: stmt.Integer}}, Cohorts: []stmt.Cohort{{Name: "present", Bindings: []any{int64(1)}}, {Name: "empty", Bindings: []any{int64(2)}}}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"search_revision"}}}
}
