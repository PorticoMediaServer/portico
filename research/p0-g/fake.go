package governor

import (
	"errors"
	"time"
)

// All scheduling time is fake. No time.Now, sleeps, SQL, external IO or processes.
type Clock struct{ MS int64 }

func (c *Clock) Now() time.Time { return time.UnixMilli(c.MS) }

type holding struct {
	request Request
	cancel  bool
}
type Fake struct {
	Clock           *Clock
	Floors          [4]Amounts
	Used            [4]Amounts
	Live            map[uint64]holding
	Physical        map[uint64]holding // independent mechanical supervisor inventory
	epoch, sequence uint64
	degraded        bool
}

func NewFake(c *Clock) *Fake {
	g := &Fake{Clock: c, Live: map[uint64]holding{}, Physical: map[uint64]holding{}, epoch: 1}
	// Resources partitioned inside ONE governor, integer sizes for a tiny model.
	g.Floors[Recovery] = Amounts{1, 1, 16, 500, 16, 16, 16, 8}
	g.Floors[Foreground] = Amounts{8, 4, 128, 2000, 128, 128, 128, 8}
	g.Floors[ScanMinimum] = Amounts{2, 1, 32, 500, 16, 32, 16, 8}
	g.Floors[Background] = Amounts{1, 1, 32, 500, 16, 32, 16, 8}
	return g
}
func (g *Fake) Try(r Request) Decision {
	refuse := func(code RefusalCode) Decision {
		return Decision{Refusal: &Refusal{code, g.Clock.Now().Add(100 * time.Millisecond)}}
	}
	if r.Class > Background {
		return refuse(Invalid)
	}
	if !r.Deadline.After(g.Clock.Now()) {
		return refuse(Deadline)
	}
	if r.Writer && (r.MaxHold <= 0 || r.MaxHold > 25*time.Millisecond) {
		return refuse(Unqualified)
	}
	if g.degraded && r.Class == Background {
		return refuse(FailSafe)
	}
	for k, n := range r.Need {
		if n < 0 {
			return refuse(Invalid)
		}
		if n > g.Floors[r.Class][k]-g.Used[r.Class][k] {
			return refuse(Capacity)
		}
	}
	if len(r.IO) > 8 {
		return refuse(Invalid)
	}
	ioBytes := int64(0)
	for i, io := range r.IO {
		for _, prior := range r.IO[:i] {
			if prior.DeviceID == io.DeviceID {
				return refuse(Invalid)
			}
		}
		if io.DeviceID == "" || io.Outstanding != 1 || io.Bytes < 0 {
			return refuse(Invalid)
		}
		if io.Bytes > r.Need[IOBytes]-ioBytes {
			return refuse(Invalid)
		}
		ioBytes += io.Bytes
		for _, h := range g.Physical {
			for _, held := range h.request.IO {
				if held.DeviceID == io.DeviceID {
					return refuse(Capacity)
				}
			}
		}
	}
	if r.Writer {
		for _, h := range g.Physical {
			if h.request.Writer {
				return refuse(Capacity)
			}
		}
	}
	g.sequence++
	grant := &Grant{id: g.sequence, epoch: g.epoch}
	h := holding{request: r}
	g.Live[grant.id] = h
	g.Physical[grant.id] = h
	for k, n := range r.Need {
		g.Used[r.Class][k] += n
	}
	return Decision{Grant: grant}
}
func (g *Fake) Cancel(p *Grant) {
	if h, ok := g.Live[p.id]; ok && p.epoch == g.epoch {
		h.cancel = true
		g.Live[p.id] = h
	}
}
func (g *Fake) Retired(p *Grant) error {
	h, ok := g.Physical[p.id]
	if !ok {
		return errors.New("duplicate/unknown physical retirement")
	}
	// Captured old epochs may retire exact holdings, never authorize new work.
	delete(g.Physical, p.id)
	delete(g.Live, p.id)
	for k, n := range h.request.Need {
		g.Used[h.request.Class][k] -= n
	}
	return nil
}
func (g *Fake) Valid(p *Grant) bool {
	if p == nil || p.epoch != g.epoch {
		return false
	}
	h, ok := g.Live[p.id]
	return ok && !h.cancel && h.request.Deadline.After(g.Clock.Now())
}

// Independent fail-safe supervisor reconciles from physical inventory, never from
// stale counters or cancellation. This fake inventory stands for OS/driver handles.
func (g *Fake) Watchdog() bool {
	var truth [4]Amounts
	for _, h := range g.Physical {
		for k, n := range h.request.Need {
			truth[h.request.Class][k] += n
		}
	}
	if truth == g.Used {
		return false
	}
	g.degraded = true
	g.epoch++
	g.Used = truth
	return true
}
func (g *Fake) Check() error {
	var truth [4]Amounts
	for _, h := range g.Physical {
		for k, n := range h.request.Need {
			truth[h.request.Class][k] += n
		}
	}
	if truth != g.Used {
		return errors.New("physical accounting mismatch")
	}
	for cl, a := range g.Used {
		for k, n := range a {
			if n < 0 || n > g.Floors[cl][k] {
				return errors.New("partition floor exceeded")
			}
		}
	}
	return nil
}

// Emergency entry bypasses a nonresponsive actor, with its backend epoch fenced.
// It never clears live handles. The real supervisor must own this inventory.
func (g *Fake) EnterStatic() {
	g.degraded = true
	g.epoch++
	var truth [4]Amounts
	for _, h := range g.Physical {
		for k, n := range h.request.Need {
			truth[h.request.Class][k] += n
		}
	}
	g.Used = truth
}
func (g *Fake) Recover() bool {
	if !g.degraded || len(g.Physical) != 0 || g.Check() != nil {
		return false
	}
	g.epoch++
	g.degraded = false
	return true
}

func (g *Fake) IOCompleted(p *Grant, device string) error {
	h, ok := g.Physical[p.id]
	if !ok {
		return errors.New("unknown IO grant")
	}
	for i, io := range h.request.IO {
		if io.DeviceID == device {
			h.request.IO = append(append([]DeviceIO(nil), h.request.IO[:i]...), h.request.IO[i+1:]...)
			h.request.Need[IOBytes] -= io.Bytes
			g.Used[h.request.Class][IOBytes] -= io.Bytes
			g.Physical[p.id] = h
			if logical, ok := g.Live[p.id]; ok {
				logical.request = h.request
				g.Live[p.id] = logical
			}
			return nil
		}
	}
	return errors.New("duplicate/unknown IO completion")
}
