package decoder

import "os"

// PreparedFileOwnership owns a second, stable per-job lock distinct from the
// worker's admission lock. Linux sandbox init, never the codec, inherits it.
// A caller must hold its admission lock while this lease is transferred and
// reacquired, and obtain both locks before recovery or staging deletion.
type PreparedFileOwnership struct{ file *os.File }

func NewPreparedFileOwnership(f *os.File) *PreparedFileOwnership {
	return &PreparedFileOwnership{file: f}
}
func (c *PreparedFileOwnership) Close() error {
	if c == nil || c.file == nil {
		return nil
	}
	e := c.file.Close()
	c.file = nil
	return e
}
