package subtitlevideo

import (
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
	"strings"
)

// Only marked actor directories whose inherited physical custody can be acquired
// are orphans. Unknown legacy directories and live decoder owners are retained.
func (r *Runtime) reconcileOrphans() error {
	entries, e := os.ReadDir(r.directory)
	if e != nil {
		return e
	}
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "subtitle-") {
			continue
		}
		id := strings.TrimPrefix(entry.Name(), "subtitle-")
		dir := filepath.Join(r.directory, entry.Name())
		marker, e := os.ReadFile(filepath.Join(dir, "owner"))
		if e != nil || string(marker) != id {
			continue
		}
		lock, e := r.custody.Lock(livechannels.Allocation{ID: id, Generation: 1})
		if e != nil {
			continue
		}
		e = os.RemoveAll(dir)
		lock.Close()
		if e != nil {
			return e
		}
	}
	return nil
}
