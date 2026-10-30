package main

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/supervise"
)

// Shutdown was a LIFO chain of defers and several of them blocked forever:
// `cancel(); <-scanDone; <-retentionDone`, `stopSetup(); <-setupDone`, the
// certificate, network and discovery joins. If the scanner was inside a blocked
// ffprobe or a hung network mount, SIGTERM produced a process that never exited;
// systemd then escalated to SIGKILL on its own timeout, at which point the
// generated-media cleanup, the write-ahead-log checkpoint and db.Close never ran
// at all. A shutdown that hangs is a shutdown that corrupts nothing and cleans
// nothing — the worst of both.
//
// One barrier instead. Every long-lived loop registers a name, how to stop it
// and a channel that closes when it has. Shutdown stops them all, waits for all
// of them under a single budget, and says by name which ones did not finish —
// and then proceeds, because proceeding is what lets the checkpoint and the
// close happen.
//
// The recording drain is deliberately not part of this and keeps its own
// fifteen seconds: it protects capture that cannot be recovered, and it is the
// one thing worth waiting for on its own terms.

// drainBudget is how long every registered loop has, together.
const drainBudget = 15 * time.Second

type drainLoop struct {
	name string
	stop func()
	done <-chan struct{}
}

// drainBarrier collects the loops a shutdown has to wait for.
type drainBarrier struct {
	mu    sync.Mutex
	loops []drainLoop
}

// add registers one loop. stop may be nil when the loop ends with the root
// context, which most of them do.
func (b *drainBarrier) add(name string, stop func(), done <-chan struct{}) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.loops = append(b.loops, drainLoop{name: name, stop: stop, done: done})
}

// wait stops every loop and waits for all of them under one budget, logging
// whichever did not finish. It never blocks longer than the budget.
func (b *drainBarrier) wait() {
	b.mu.Lock()
	loops := append([]drainLoop(nil), b.loops...)
	b.mu.Unlock()
	if len(loops) == 0 {
		return
	}
	for _, loop := range loops {
		if loop.stop != nil {
			loop.stop()
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), drainBudget)
	defer cancel()
	var wait sync.WaitGroup
	outstanding := make([]string, len(loops))
	for index, loop := range loops {
		wait.Add(1)
		index, loop := index, loop
		supervise.Go("cmd.drain."+loop.name, func() {
			defer wait.Done()
			select {
			case <-loop.done:
			case <-ctx.Done():
				outstanding[index] = loop.name
			}
		})
	}
	finished := make(chan struct{})
	supervise.Go("cmd.drain.join", func() { wait.Wait(); close(finished) })
	select {
	case <-finished:
	case <-ctx.Done():
		<-finished
	}
	var stuck []string
	for _, name := range outstanding {
		if name != "" {
			stuck = append(stuck, name)
		}
	}
	if len(stuck) > 0 {
		// Naming them is the whole point: "shutdown timed out" tells an owner
		// nothing, and the next person to read the log needs to know which loop.
		log.Printf("Shutdown proceeded after %s without these finishing: %v", drainBudget, stuck)
	}
}
