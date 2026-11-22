package db

import (
	"strconv"
	"strings"
)

// copyKey says which suggestions are copies of one title the server holds in two libraries (a
// 4K library beside an HD one): the same kind, title and year. A title with no year is never
// taken for a copy of another by its name alone, so ok is false for it.
func copyKey(it Item) (key string, ok bool) {
	if it.Year <= 0 {
		return "", false
	}
	return strconv.Itoa(int(it.Kind)) + "|" + strings.ToLower(strings.TrimSpace(it.Title)) + "|" + strconv.Itoa(it.Year), true
}

// copyPartition is copyKey in SQL for the items row aliased i: rows in one partition are copies.
var copyPartition = copyPartitionOf("i")

// copyPartitionOf is copyKey in SQL for the items row with this alias.
func copyPartitionOf(alias string) string {
	return `CASE WHEN ifnull(` + alias + `.year, 0) > 0 THEN ` + alias + `.kind || '|' || lower(trim(` + alias + `.title)) || '|' || ` + alias +
		`.year ELSE 'id|' || ` + alias + `.id END`
}
