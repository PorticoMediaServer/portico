package db

// SetRareTag lets a test send tag-filtered lists down the path for common tags or the path
// for rare ones, whatever the size of its library.
func SetRareTag(uses int) { rareTag = uses }

// SetChangesKeepRows lets a test prune the changes table without writing 200,000 rows first.
func SetChangesKeepRows(rows int) { changesKeepRows = rows }

// ChooseMatch marks a title's match as chosen by a person, which only the metadata editor's
// own writes do.
func ChooseMatch(d *DB, publicID string) error {
	_, err := d.writeConn.Exec(`UPDATE items SET match_state = ? WHERE public_id = ?`, MatchChosen, publicID)
	return err
}
