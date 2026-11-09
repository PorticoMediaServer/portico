// Package diskspace answers one question: how many bytes are left on the volume
// holding this path.
//
// Free-space checks existed only for recordings and host telemetry, so nothing
// stopped a conversion, a prepared encode, an artwork fetch or a subtitle render
// from filling the volume that also holds server.sqlite and its write-ahead log.
// A full volume is the one failure that turns "a stream did not start" into
// "every write fails", and SQLITE_FULL reaching a caller is a far worse day than
// an honest refusal to start a producer.
package diskspace

// Free reports the bytes available to this process on the volume holding path.
// An error means the question could not be answered — never that the volume is
// full — so a caller that cannot measure must not refuse work.
func Free(path string) (int64, error) { return freeBytes(path) }

// ProducerFloor is how much room any producer of new files wants before it
// starts. It is the same number for conversions, prepared encodes, artwork and
// subtitle renders, because the thing being protected is the same in every
// case: the volume that also holds server.sqlite and its write-ahead log.
//
// Two gigabytes is beyond-target behaviour, not a working limit. A home server
// with less than this free is already in trouble; what this changes is which
// failure the owner gets — a producer that declines to start and says so, or
// SQLITE_FULL reaching every writer at once.
const ProducerFloor int64 = 2 << 30

// Room reports whether path's volume has at least floor bytes free. A volume
// that cannot be measured is never treated as full: an unanswerable question
// must not be the reason a person cannot play something.
func Room(path string, floor int64) bool {
	free, err := freeBytes(path)
	if err != nil {
		return true
	}
	return free >= floor
}
