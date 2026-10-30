package connectivity

import (
	"context"
	"github.com/PorticoMediaServer/portico/server/internal/supervise"
	"sync"
)

const ServiceType = "_portico._tcp.local."

type AdvertiserOptions struct {
	Instance, Host string
	Port           int
	TXT            []string
}

// Advertiser is only the owner-setting controller. Networking.Discovery is the
// single DNS-SD implementation and owns probing, identity and private addresses.
//
// A52: the run is supervised (restarted after a panic or an early return) under
// the process context given to Bind, so shutdown cancels it; Apply(false)
// waits for it to stop without holding the lock Status (an HTTP handler) needs.
type Advertiser struct {
	applyMu sync.Mutex // serializes Apply, so a stop finishes before a restart
	mu      sync.Mutex
	enabled bool
	parent  context.Context
	run     func(context.Context)
	cancel  context.CancelFunc
	done    chan struct{}
}

func NewAdvertiser(AdvertiserOptions) *Advertiser { return &Advertiser{} }

// Bind supplies the DNS-SD run and the process context it lives under.
func (a *Advertiser) Bind(parent context.Context, run func(context.Context)) {
	if a == nil {
		return
	}
	a.mu.Lock()
	a.parent, a.run = parent, run
	a.startLocked()
	a.mu.Unlock()
}
func (a *Advertiser) startLocked() {
	if !a.enabled || a.run == nil || a.cancel != nil {
		return
	}
	parent := a.parent
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	a.cancel = cancel
	a.done = make(chan struct{})
	done, run := a.done, a.run
	supervise.Go("connectivity.discovery.advertiser", func() {
		defer close(done)
		supervise.Loop(ctx, "connectivity.discovery.advertiser", run)
	})
}
func (a *Advertiser) Apply(enabled bool) {
	if a == nil {
		return
	}
	a.applyMu.Lock()
	defer a.applyMu.Unlock()
	a.mu.Lock()
	a.enabled = enabled
	var stopping chan struct{}
	if !enabled && a.cancel != nil {
		a.cancel()
		stopping = a.done
		a.cancel, a.done = nil, nil
	}
	a.startLocked()
	a.mu.Unlock()
	if stopping != nil {
		<-stopping
	}
}
func (a *Advertiser) Close() { a.Apply(false) }
func (a *Advertiser) Status(enabled bool) Discovery {
	if a == nil {
		return Discovery{Enabled: enabled, State: "unavailable", Service: ServiceType}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	state := "stopped"
	if a.cancel != nil {
		select {
		case <-a.done:
			state = "unavailable"
		default:
			state = "advertising"
		}
	}
	return Discovery{Enabled: enabled, Supported: a.run != nil, State: state, Service: ServiceType}
}
