package mediaanalysis

import (
	"fmt"
	"os"
	"sync"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/decoder"
	"github.com/PorticoMediaServer/portico/server-next/internal/mediaexec"
)

// The test executable dispatches the same confinement helpers as the server.
func TestMain(m *testing.M) {
	for _, helper := range []func([]string) (bool, error){decoder.RunHelper, mediaexec.RunHelper} {
		if handled, err := helper(os.Args[1:]); handled {
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			os.Exit(0)
		}
	}
	os.Exit(m.Run())
}

type episodeDecodeInput struct {
	file           *os.File
	observed       os.FileInfo
	mu             sync.Mutex
	bytes, maxRead int64
	closed         int
}

func (i *episodeDecodeInput) Evidence() string { return "observed:controlled-two-episode-video" }
func (i *episodeDecodeInput) Size() int64      { return i.observed.Size() }
func (i *episodeDecodeInput) Close() error {
	i.mu.Lock()
	i.closed++
	i.mu.Unlock()
	return i.file.Close()
}
