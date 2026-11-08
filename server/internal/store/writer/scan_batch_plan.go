package writer

import (
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const inventoryBatchWALBytes = 128 << 20
const inventoryBatchIOBytes = 1 << 30

func inventoryPlanBatchFiles(p effectivePlan) int {
	if p.inventoryBatchFiles == 0 {
		return 1
	}
	return p.inventoryBatchFiles
}

// Conservatively repeat the complete per-file effect inventory. Shared run,
// job, directory and receipt writes only execute once but keep their spare
// reservation. The same plan already funds replay at the maximum width.
func reserveInventoryBatchSteps(remaining Work, steps []statementStep, files int) (Work, error) {
	if files < 1 || files > maxInventoryBatchFiles {
		return Work{}, ErrInvalid
	}
	left, err := reserveInventoryDriverSteps(remaining, steps)
	if err != nil {
		return Work{}, err
	}
	for _, step := range steps {
		for range (files - 1) * inventoryFreshMaxUses(step.id) {
			if !fits(step.work, left) {
				return Work{}, ErrQuantum
			}
			left = subtract(left, step.work)
		}
	}
	return left, nil
}

// Operating forecast, never an automatic enable or qualification receipt.
// An independent full Capture/engine/keeper composition is still required.
// Keep the 25ms admission target and 3s runtime ceiling unchanged. Timing
// forecasts are intentionally separate from the adaptive 8ms working target.
func inventoryBatchForecast(registration stmt.Receipt, engine sqliteconn.EngineIdentity, pageSize int32, device uint64) (effectivePlan, error) {
	p, err := inventoryN1Forecast(registration, engine, pageSize, device)
	if err != nil {
		return effectivePlan{}, err
	}
	p.inventoryBatchFiles = maxInventoryBatchFiles
	p.recipe.limit.Rows *= maxInventoryBatchFiles
	p.recipe.limit.VMOps *= maxInventoryBatchFiles
	p.recipe.limit.Pages *= maxInventoryBatchFiles
	// Preserve EVERY per-file physical allowance; widen the complete unit instead.
	p.recipe.limit.WALBytes = inventoryBatchWALBytes
	p.need[governor.WALBytes] = int64(p.recipe.limit.WALBytes)
	p.need[governor.IOBytes] = inventoryBatchIOBytes
	for i := range p.io {
		p.io[i].Bytes = p.need[governor.IOBytes]
	}
	for i := range p.steps {
		work := &p.steps[i].work
		work.Hold = 10 * time.Microsecond
		for _, def := range inventoryDriverCommandDefinitions() {
			if def.ID == p.steps[i].id && def.Kind == stmt.Write {
				work.Hold = 50 * time.Microsecond
			}
		}
	}
	p.inventoryRecords.work.Hold = 50 * time.Microsecond
	return p, nil
}
