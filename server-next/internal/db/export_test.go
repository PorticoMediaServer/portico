package db

// SetRareTag lets a test send tag-filtered lists down the path for common tags or the path
// for rare ones, whatever the size of its library.
func SetRareTag(uses int) { rareTag = uses }

// SetChangesKeepRows lets a test prune the changes table without writing 200,000 rows first.
func SetChangesKeepRows(rows int) { changesKeepRows = rows }

// SetWALLimits lets a test reach the WAL policy's sizes with a small database. It returns
// a function that puts the real ones back.
func SetWALLimits(drain, insist int64) (restore func()) {
	oldDrain, oldInsist := walDrainBytes, walInsistBytes
	walDrainBytes, walInsistBytes = drain, insist
	return func() { walDrainBytes, walInsistBytes = oldDrain, oldInsist }
}
