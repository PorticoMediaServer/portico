package db

import (
	"database/sql/driver"

	"modernc.org/sqlite"
)

// A shuffled order is every item's place under a seed: portico_shuffle(id, seed), a value
// that mixes the two so well that any two seeds give unrelated orders, an album's tracks
// included. It is computed for each row as the list is read, and a page's cursor carries the
// seed, so one order holds across the pages of one choosing and the next choosing is another.
//
// It is a bijection of the id for a given seed (every step below can be undone on 63-bit
// values), so no two items share a place and a walk that reads "places after the last one"
// meets each item once. Places are never negative.
func init() {
	sqlite.MustRegisterDeterministicScalarFunction("portico_shuffle", 2,
		func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
			id, _ := args[0].(int64)
			seed, _ := args[1].(int64)
			return shufflePlace(id, seed), nil
		})
}

const shuffleMask = 1<<63 - 1

// shufflePlace is an item's place in the order a seed decides.
func shufflePlace(id, seed int64) int64 {
	x := (uint64(id) ^ uint64(seed)*0x9e3779b97f4a7c15) & shuffleMask
	x ^= x >> 30
	x = x * 0xbf58476d1ce4e5b9 & shuffleMask
	x ^= x >> 27
	x = x * 0x94d049bb133111eb & shuffleMask
	x ^= x >> 31
	return int64(x)
}

// shuffleKey is the SQL for an item's place in the order a seed decides.
func shuffleKey(seed int64) string {
	return `portico_shuffle(i.id, ` + itoa(seed) + `)`
}
