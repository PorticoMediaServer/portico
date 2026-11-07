package governor

import (
	"errors"
	"testing"
	"time"
)

func TestCheckpointOwedTurnCannotBeOvertaken(t *testing.T) {
	g, a, clock, wal := checkpointSetup(t)
	writer := grant(t, g, checkpointWriter(clock, Foreground))
	ticket, err := a.queueCheckpoint(clock.now().Add(50*time.Millisecond), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-ticket.ready:
		t.Fatal("pending ticket claimed physical writer exit")
	default:
	}
	g.Cancel(writer)
	select {
	case <-ticket.ready:
		t.Fatal("cancel woke checkpoint before native return")
	default:
	}
	clock.ms.Add(25)
	retire(t, a, writer)
	select {
	case <-ticket.ready:
	default:
		t.Fatal("physical return lost checkpoint wake")
	}
	publish(t, wal, walSample(4, 4), clock.now())
	for i := 0; i < 32; i++ {
		refused(t, g, checkpointWriter(clock, Foreground), Capacity)
	}
	r := checkpointRequest(clock)
	d := a.tryCheckpointTurn(r, ticket)
	if d.Grant == nil {
		t.Fatal("owed checkpoint starved", d)
	}
	a.cancelCheckpointTicket(ticket) // unused metadata cancellation must not release native CP.
	refused(t, g, checkpointWriter(clock, Foreground), Capacity)
	retire(t, a, d.Grant)
	publish(t, wal, walSample(4, 4), clock.now())
	retire(t, a, grant(t, g, checkpointWriter(clock, Foreground)))
	if a.Usage() != [4]Amounts{} {
		t.Fatal(a.Usage())
	}
}

func TestCheckpointTicketCancellationExpiryAndSharedPacing(t *testing.T) {
	g, a, clock, wal := checkpointSetup(t)
	ticket, err := a.queueCheckpoint(clock.now().Add(50*time.Millisecond), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	cfg := g.(*ledger).config
	_, other, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	other.cancelCheckpointTicket(ticket)
	refused(t, g, checkpointWriter(clock, Foreground), Capacity)
	clock.ms.Add(51)
	publish(t, wal, walSample(4, 4), clock.now())
	retire(t, a, grant(t, g, checkpointWriter(clock, Foreground)))
	if _, err := other.queueCheckpoint(clock.now().Add(50*time.Millisecond), time.Second); !errors.Is(err, ErrCheckpointCadence) {
		t.Fatal("new driver/authority reset pacing", err)
	}
	clock.ms.Store(1000)
	ticket, err = a.queueCheckpoint(clock.now().Add(50*time.Millisecond), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	a.cancelCheckpointTicket(ticket)
	publish(t, wal, walSample(4, 4), clock.now())
	retire(t, a, grant(t, g, checkpointWriter(clock, Foreground)))
	if _, err = a.queueCheckpoint(clock.now().Add(50*time.Millisecond), time.Second); !errors.Is(err, ErrCheckpointCadence) {
		t.Fatal("cancel reset budget", err)
	}
}

// Deterministic SERVICE control under declared <=5ms writer and CP exits.
// This tests admission order/time accounting, not actual device latency. Every
// rolling second keeps >=90% useful FG service while an owed CP completes with
// bounded gaps. A native miss is separately a negative/fenced control.
func TestCheckpointFairnessSustainedForeground(t *testing.T) {
	g, a, clock, wal := checkpointSetup(t)
	policy := CheckpointPolicy{Interval: 50 * time.Millisecond, MaxWait: 50 * time.Millisecond, MaxHold: 25 * time.Millisecond}
	spacing := checkpointSpacing(policy)
	const horizon = 6000
	service := make([]bool, horizon)
	var writer, cp *Grant
	var ticket *checkpointTicket
	writerEnd, cpEnd, admitAt := 0, 0, 0
	lastCP, completed := 0, 0
	nextDue := 0
	for ms := 0; ms < horizon; ms++ {
		clock.ms.Store(int64(ms))
		if writer != nil && ms == writerEnd {
			retire(t, a, writer)
			writer = nil
			publish(t, wal, walSample(4, 4), clock.now())
		}
		if cp != nil && ms == cpEnd {
			retire(t, a, cp)
			cp = nil
			completed++
			if ms-lastCP > int(spacing.Milliseconds())+60 {
				t.Fatal("checkpoint starved", lastCP, ms)
			}
			lastCP = ms
			publish(t, wal, walSample(4, 4), clock.now())
		}
		if ticket == nil && cp == nil && ms >= nextDue {
			var err error
			ticket, err = a.queueCheckpoint(clock.now().Add(policy.MaxWait), spacing)
			if err != nil {
				t.Fatal(err)
			}
			nextDue = ms + int(spacing.Milliseconds())
			admitAt = ms + 49 // MaxWait50ms: exercise a bounded but nearly worst handoff.
		}
		if ticket != nil && writer == nil && ms >= admitAt {
			r := checkpointRequest(clock)
			r.MaxHold = policy.MaxHold
			d := a.tryCheckpointTurn(r, ticket)
			if d.Grant == nil {
				t.Fatal("ready CP failed", d)
			}
			cp = d.Grant
			cpEnd = ms + 25
			ticket = nil
		}
		if writer == nil && cp == nil && ticket == nil {
			writer = grant(t, g, checkpointWriter(clock, Foreground))
			writerEnd = ms + 5
		}
		service[ms] = writer != nil
	}
	if writer != nil {
		retire(t, a, writer)
	}
	if cp != nil {
		retire(t, a, cp)
	}
	if ticket != nil {
		a.cancelCheckpointTicket(ticket)
	}
	for start := 0; start+1000 <= horizon; start++ {
		useful := 0
		for _, on := range service[start : start+1000] {
			if on {
				useful++
			}
		}
		if useful < 900 {
			t.Fatal("CP violated foreground floor", start, useful)
		}
	}
	if completed < 4 || horizon-lastCP > int(spacing.Milliseconds())+60 {
		t.Fatal("missing useful checkpoint service", completed, lastCP)
	}
	if a.Usage() != [4]Amounts{} {
		t.Fatal("service control leaked custody")
	}
}
