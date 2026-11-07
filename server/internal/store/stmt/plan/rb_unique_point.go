package plan

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// RB's optional joined points need forced UNIQUE keys on tiny analyzed tables.
// SQLite may report the COMPLETE unique key followed by another rowid equality.
// The complete-key SQL equalities and native key metadata are already checked
// by the caller. That unique prefix proves <=1 row; the extra equality can only
// restrict it further, whether or not it aliases one of the declared columns.
// This is not a prefix/range/scan permission. Existing command policies and
// qualifications stay on their original path: only these two RB read IDs opt in.
func rbAdditionalRowIDPoint(d stmt.Definition, a AutoIndexAudit, native []keyCondition) bool {
	if d.ID != "read.browse.rb.channel.check" && d.ID != "read.browse.rb.home.binding.links" {
		return false
	}
	n := len(a.Binding.Columns)
	if a.WithoutRowid || n == 0 || len(native) != n+1 || native[n] != (keyCondition{"rowid", "="}) {
		return false
	}
	return nativeKeyPrefix(a, native[:n], n, "")
}
