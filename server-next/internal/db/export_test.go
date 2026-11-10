package db

// SetRareTag lets a test send tag-filtered lists down the path for common tags or the path
// for rare ones, whatever the size of its library.
func SetRareTag(uses int) { rareTag = uses }

// SetChangesKeepRows lets a test prune the changes table without writing 200,000 rows first.
func SetChangesKeepRows(rows int) { changesKeepRows = rows }
