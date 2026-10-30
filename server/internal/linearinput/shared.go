package linearinput

import (
	"context"
	"io"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
	"github.com/PorticoMediaServer/portico/server/internal/remotemedia"
	"github.com/PorticoMediaServer/portico/server/internal/supervise"
)

// Readers have independent private endpoints, output sinks and cancellation.
// Only acquisition and its bounded byte ring belong to the channel.
type sharedInput struct {
	cancel     context.CancelFunc
	fence      string
	closing    bool
	retired    chan struct{}
	retireOnce sync.Once
	ready      chan struct{}
	gateway    *Gateway
	err        error
	readers    int
	idle       *time.Timer
	idleDone   chan struct{}
}

var inputs = struct {
	sync.Mutex
	entries map[string]*sharedInput
}{entries: map[string]*sharedInput{}}

const inputGrace = 3 * time.Second

func Open(ctx context.Context, in livechannels.Input) (*Gateway, error) {
	key := in.SourceID + "\n" + in.ChannelID
	fence := in.Fence()
	// Anonymous inputs are used by internal fixtures and have no channel identity.
	if in.SourceID == "" || in.ChannelID == "" {
		return openInput(ctx, in)
	}
	inputs.Lock()
	entry := inputs.entries[key]
	if entry != nil && entry.fence != fence && !entry.closing {
		entry.closing = true
		supervise.Go("linearinput.channel.replace", func() { retireInput(key, entry) })
	}
	if entry != nil && entry.closing {
		retired := entry.retired
		inputs.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-retired:
			return Open(ctx, in)
		}
	}
	if entry != nil {
		select {
		case <-entry.ready:
			if entry.err != nil || entry.gateway != nil && entry.gateway.Snapshot().Ended {
				entry.closing = true
				supervise.Go("linearinput.channel.failed", func() { retireInput(key, entry) })
				retired := entry.retired
				inputs.Unlock()
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-retired:
					return Open(ctx, in)
				}
			}
		default:
		}
	}
	if entry == nil {
		life, cancel := context.WithCancel(context.Background())
		entry = &sharedInput{cancel: cancel, fence: fence, retired: make(chan struct{}), ready: make(chan struct{})}
		inputs.entries[key] = entry
		supervise.Go("linearinput.channel.open", func() {
			// Acquisition is bounded; the channel lifetime is detached from the first reader.
			g, e := openInput(life, in)
			inputs.Lock()
			entry.gateway, entry.err = g, e
			close(entry.ready)
			inputs.Unlock()
		})
	}
	if entry.idle != nil {
		if entry.idle.Stop() {
			close(entry.idleDone)
		}
		entry.idle = nil
		entry.idleDone = nil
	}
	entry.readers++
	inputs.Unlock()
	release := func() { releaseInput(key, entry) }
	select {
	case <-ctx.Done():
		release()
		return nil, ctx.Err()
	case <-entry.ready:
	}
	if entry.err != nil {
		release()
		return nil, entry.err
	}
	child, cancel := context.WithCancel(ctx)
	span := entry.gateway.Snapshot()
	baseline := span.Gaps
	if span.Reconnecting {
		baseline = max(0, baseline-1)
	}
	g := &Gateway{gapBaseline: baseline, ctx: child, cancel: cancel, shared: entry.gateway, release: release, policy: in.Policy, resources: map[string]resource{}, reverse: map[string]string{}, clients: map[*remotemedia.Client]bool{}, bodies: map[io.ReadCloser]bool{}, changed: make(chan struct{}), hls: entry.gateway.hls}
	var e error
	g.secret, e = token()
	if e != nil {
		g.Close()
		return nil, e
	}
	g.v4, e = net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if e != nil {
		g.Close()
		return nil, ErrUnavailable
	}
	port := g.v4.Addr().(*net.TCPAddr).Port
	g.v6, e = net.ListenTCP("tcp6", &net.TCPAddr{IP: net.IPv6loopback, Port: port})
	if e != nil {
		g.Close()
		return nil, ErrUnavailable
	}
	g.address = "http://127.0.0.1:" + strconv.Itoa(port)
	if g.hls {
		entry.gateway.mu.Lock()
		g.resources[g.secret] = entry.gateway.resources[entry.gateway.secret]
		entry.gateway.mu.Unlock()
	}
	g.http = &http.Server{Handler: http.HandlerFunc(g.serve), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 10 * time.Second, MaxHeaderBytes: 8 << 10, BaseContext: func(net.Listener) context.Context { return child }}
	g.wg.Add(2)
	supervise.Go("linearinput.reader.serve4", func() { defer g.wg.Done(); _ = g.http.Serve(g.v4) })
	supervise.Go("linearinput.reader.serve6", func() { defer g.wg.Done(); _ = g.http.Serve(g.v6) })
	return g, nil
}
func releaseInput(key string, entry *sharedInput) {
	inputs.Lock()
	entry.readers--
	if entry.readers != 0 {
		inputs.Unlock()
		return
	}
	done := make(chan struct{})
	entry.idleDone = done
	entry.idle = time.AfterFunc(inputGrace, func() {
		inputs.Lock()
		if entry.readers != 0 || entry.idleDone != done {
			inputs.Unlock()
			close(done)
			return
		}
		entry.closing = true
		inputs.Unlock()
		retireInput(key, entry)
		close(done)
	})
	inputs.Unlock()
	// The last reader retains its durable tuner allocation until acquisition is
	// physically closed, including the grace interval, or another reader joins.
	<-done
}

func retireInput(key string, entry *sharedInput) {
	entry.retireOnce.Do(func() {
		entry.cancel()
		<-entry.ready
		if entry.gateway != nil {
			_ = entry.gateway.Close()
		}
		inputs.Lock()
		if inputs.entries[key] == entry {
			delete(inputs.entries, key)
		}
		close(entry.retired)
		inputs.Unlock()
	})
}
