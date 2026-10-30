package preparedmedia

import (
	"os"

	"github.com/PorticoMediaServer/portico/server/internal/decoder"
	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
)

// Admission and sandbox-retirement are distinct locks. Holding admission lets
// the Linux runner drop its own copy of the inherited lock and wait for init's
// last copy without another worker entering the same attempt in that interval.
type attemptCustody struct {
	file    *os.File
	sandbox *decoder.PreparedFileCustody
}

func (s *Service) acquireCustody(id string) (*attemptCustody, error) {
	file, e := s.locks.Lock(livechannels.Allocation{ID: preparedLockID("attempt", id), Generation: 1})
	if e != nil {
		return nil, e
	}
	out := &attemptCustody{file: file}
	if s.fileSandbox() {
		child, e := s.locks.Lock(livechannels.Allocation{ID: preparedLockID("sandbox", id), Generation: 1})
		if e != nil {
			file.Close()
			return nil, e
		}
		out.sandbox = decoder.NewPreparedFileCustody(child)
	}
	return out, nil
}
func (c *attemptCustody) Close() {
	if c.sandbox != nil {
		_ = c.sandbox.Close()
	}
	_ = c.file.Close()
}

// Do not pass a 64-character job/content hash to the 48-character inherited-lock
// API. Namespaces prevent admission, init retirement and object gates colliding.
func preparedLockID(kind, id string) string {
	return hash([]string{"prepared-custody-v1", kind, id})[:48]
}
