package writer

import "time"

const (
	maxInventoryBatchFiles   = 8
	inventoryBatchHoldTarget = 8 * time.Millisecond
)

// Between-original feedback only. This policy grants no authority, resources,
// deadline extension or permission to change an already frozen command.
type inventoryBatchPolicy struct {
	width int
	short int
}

func (p *inventoryBatchPolicy) limit(maximum int) int {
	if maximum < 1 || maximum > maxInventoryBatchFiles {
		return 1
	}
	if p.width == 0 {
		p.width = min(2, maximum)
	}
	p.width = max(1, min(p.width, maximum))
	return p.width
}

func (p *inventoryBatchPolicy) observe(maximum, files int, hold, wait time.Duration, applied bool) {
	if maximum < 1 || maximum > maxInventoryBatchFiles {
		p.width, p.short = 1, 0
		return
	}
	width := p.limit(maximum)
	if !applied || hold <= 0 || wait < 0 || hold > inventoryBatchHoldTarget || wait > inventoryBatchHoldTarget {
		p.width, p.short = max(1, width/2), 0
		return
	}
	// Short pages (including a drain at EOF) cannot justify growing the width.
	// Use half the working hold budget as growth headroom; an in-range slow
	// sample clears the streak instead of allowing a delayed increase.
	if files != width || hold > inventoryBatchHoldTarget/2 || wait > inventoryBatchHoldTarget/2 {
		p.short = 0
		return
	}
	p.short++
	if p.short == 8 {
		p.width, p.short = min(width+1, maximum), 0
	}
}
