package api

import (
	"github.com/PorticoMediaServer/portico/server-next/internal/playback"
	"testing"
	"time"
)

type splitLoadEngine struct{ playback.NoEngine }

func (splitLoadEngine) ConversionLoad() (int, int) { return 2, 1 }

func TestDashboardLoadExposesSplitConversionSeries(t *testing.T) {
	s := newTestServer(t)
	owner := onMain(t, s, setup(t, s)).AccessToken
	engine := splitLoadEngine{}
	s.playback.SetEngine(engine, engine)
	deadline := time.Now().Add(10 * time.Second)
	for {
		load := get[Load](t, s, owner, "/v1/admin/load")
		hardware, processor, total := load.Series["hardwareConversions"], load.Series["processorConversions"], load.Series["conversions"]
		if len(hardware) > 0 && hardware[len(hardware)-1].V == 2 {
			if len(processor) == 0 || processor[len(processor)-1].V != 1 || len(total) == 0 || total[len(total)-1].V != 3 {
				t.Fatal(load)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("running conversion counts did not reach the load samples", load)
		}
		time.Sleep(25 * time.Millisecond)
	}
	day := get[Load](t, s, owner, "/v1/admin/load?range=24h")
	for _, name := range []string{"hardwareConversions", "processorConversions", "conversions"} {
		if len(day.Series[name]) == 0 {
			t.Fatal("the hourly series lost", name, day)
		}
	}
}
