package mediaanalysis

import (
	"os"
	"sync"

	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
)

// An object gate covers the seal/reference gap and physical read completion
// across processes sharing this analysis directory. It does not confer source,
// playback, or account authority, and never covers prepared-media objects.
type artifactCustody struct {
	locks   *livechannels.PhysicalLocks
	mu      sync.Mutex
	readers map[string]*analysisReaderLease
}
type analysisReaderLease struct {
	file *os.File
	refs int
}

func (c *artifactCustody) exclusive(digest string) (*os.File, error) {
	return c.locks.Lock(livechannels.Allocation{ID: analysisLockID("analysis-object", digest), Generation: 1})
}
func (c *artifactCustody) read(digest string) (func(), error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.readers == nil {
		c.readers = map[string]*analysisReaderLease{}
	}
	lease := c.readers[digest]
	if lease == nil {
		file, err := c.exclusive(digest)
		if err != nil {
			return nil, err
		}
		lease = &analysisReaderLease{file: file}
		c.readers[digest] = lease
	}
	lease.refs++
	var once sync.Once
	return func() {
		once.Do(func() {
			c.mu.Lock()
			defer c.mu.Unlock()
			lease.refs--
			if lease.refs == 0 {
				delete(c.readers, digest)
				_ = lease.file.Close()
			}
		})
	}, nil
}

// PhysicalLocks accepts exactly 24 bytes encoded as lowercase hex. Analysis
// publication/source IDs remain full SHA-256 values; only the lock adapter narrows.
func analysisLockID(parts ...string) string { return token(parts...)[:48] }
