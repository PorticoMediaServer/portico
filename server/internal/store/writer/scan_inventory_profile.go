package writer

import (
	"fmt"
	"runtime"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// inventoryN1Forecast is the initial Linux ordinary-file operating forecast,
// not a qualification receipt or permission to install the accepting path.
// Only trusted composition supplies the real Capture, held-engine and device
// facts. There is no caller-selected quota, default registration or new grant.
//
// The population is written only by the new layer's bounded producers. The
// 32MiB wire limit remains unchanged. Preparation and existing-record capacity
// checks can refuse an unusually large legal request before effects. Hold is
// the admission target only: target misses report evidence; the shared 3s
// safety ceiling and original deadlines govern runtime failure and custody.
// These are realistic-use forecasts, not universal worst-case IO/time claims.
func inventoryN1Forecast(registration stmt.Receipt, engine sqliteconn.EngineIdentity, pageSize int32, device uint64) (effectivePlan, error) {
	if runtime.GOOS != "linux" || pageSize != 4096 || registration == nil || engine.Version == "" || engine.SourceID == "" {
		return effectivePlan{}, &Refusal{Code: RefusalUnqualified}
	}
	p := effectivePlan{
		registration: registration,
		recipe: turnRecipe{
			limit:    Work{16384, 131072, 16384, 32 << 20, 25 * time.Millisecond},
			setup:    Work{32, 1024, 32, 65536, time.Millisecond},
			terminal: Work{32, 1024, 512, 2 << 20, 8 * time.Millisecond},
		},
		headerSync:        Work{WALBytes: 32, Hold: 5 * time.Millisecond},
		inventoryPageSize: pageSize,
		inventoryEngine:   engine,
		// SAME grant, nonborrowable preparation/native-result partitions. The
		// original ready holding stays independently charged until release.
		inventoryMemory: inventoryMemoryBudget{8 << 20, 8 << 20},
	}
	read := Work{8, 256, 64, 0, 50 * time.Microsecond}
	write := Work{512, 4096, 256, 512 << 10, 500 * time.Microsecond}
	// This is a working envelope, not a new schema/input limit. It includes
	// ALL five preserved rows; each rewrite owns its complete reserved work.
	p.inventoryRecords = inventoryRecordEnvelope{32 << 10, write}
	defs := inventoryDriverCommandDefinitions()
	p.steps = make([]statementStep, 0, len(defs))
	for _, def := range defs {
		work := read
		if def.Kind == stmt.Write {
			work = write
		} else if def.Kind != stmt.Read {
			return effectivePlan{}, ErrInvalid
		}
		p.steps = append(p.steps, statementStep{def.ID, work})
	}
	p.need[governor.MemoryBytes] = 16 << 20
	p.need[governor.CPUMilli] = 1000
	p.need[governor.WALBytes] = int64(p.recipe.limit.WALBytes)
	p.need[governor.IOBytes] = 128 << 20
	p.io = []governor.DeviceIO{{DeviceID: fmt.Sprintf("filesystem-device:%d", device), Class: governor.Seeking, Outstanding: 1, Bytes: p.need[governor.IOBytes]}}
	return p, nil
}
