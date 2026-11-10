package streaming

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

type fixedSettings struct{ cfg DeliveryConfiguration }

func (f fixedSettings) Delivery() DeliveryConfiguration { return f.cfg }

// Runs only in a child selected by the throttle test. Publish real TS fixtures,
// then stay alive until the production watcher cancels and reaps this process.
func TestHLSThrottleProducer(t *testing.T) {
	if os.Getenv("PORTICO_TEST_THROTTLE_PRODUCER") != "1" {
		return
	}
	args := os.Args[len(os.Args)-3:]
	from, err := strconv.Atoi(args[2])
	if err != nil {
		t.Fatal(err)
	}
	for i := from; i < from+4; i++ {
		if from == 0 && i == 2 {
			for {
				_, err := os.Stat(filepath.Join(args[0], "release"))
				if err == nil {
					break
				}
				if !os.IsNotExist(err) {
					t.Fatal(err)
				}
				time.Sleep(10 * time.Millisecond)
			}
		}
		raw, err := os.ReadFile(filepath.Join(args[0], hlsSegmentFile(i)))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fmt.Sprintf(args[1], i), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	for {
		time.Sleep(time.Hour)
	}
}
