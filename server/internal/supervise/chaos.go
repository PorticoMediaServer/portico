package supervise

import (
	"math/rand"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

// Panic containment is the one hardening change that is completely invisible
// until the day it matters, which makes it the one most likely to have been
// wired up wrong and never noticed. So there is a seam for making it matter on
// purpose.
//
//	PORTICO_CHAOS_PANIC=catalog.search.group:0.01,playback.hls.produce:0.1
//
// names goroutines and the fraction of their starts that panic before running a
// line of their own work. A name matches by prefix, so `playback.` covers every
// goroutine in that package. The soak tier sets it, drives real traffic, and
// asserts the process survives and the diagnostics counter moves.
//
// It is read once, from the environment, and does nothing at all when unset:
// there is no build tag, because a seam that only exists in a test build is a
// seam that has never been exercised in the binary that ships.

var (
	chaosOnce  sync.Once
	chaosRules atomic.Pointer[[]chaosRule]
)

type chaosRule struct {
	prefix   string
	fraction float64
}

// chaosPanic is what an injected failure panics with, so a reader of the log can
// tell an injected panic from a real one at a glance.
type chaosPanic struct{ name string }

func (c chaosPanic) Error() string  { return "injected chaos panic in " + c.name }
func (c chaosPanic) String() string { return c.Error() }

// Chaos panics if this name is named in PORTICO_CHAOS_PANIC and the dice say so.
//
// It is called by the code that wants to be injectable, not by Go: a panic
// raised before the goroutine's own body has set up its defers skips them, and a
// caller whose WaitGroup.Done lives in that body would wait forever. Injecting
// where the work happens is also the more honest test — it is a panic in the
// middle of real work, with the real cleanup in place.
func Chaos(name string) {
	chaosOnce.Do(loadChaos)
	rules := chaosRules.Load()
	if rules == nil || len(*rules) == 0 {
		return
	}
	for _, rule := range *rules {
		if strings.HasPrefix(name, rule.prefix) && rand.Float64() < rule.fraction {
			panic(chaosPanic{name: name})
		}
	}
}

func loadChaos() { storeChaos(parseChaos(os.Getenv("PORTICO_CHAOS_PANIC"))) }

func storeChaos(rules []chaosRule) { chaosRules.Store(&rules) }

func parseChaos(raw string) []chaosRule {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var rules []chaosRule
	for _, entry := range strings.Split(raw, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		name, share, found := strings.Cut(entry, ":")
		fraction := 1.0
		if found {
			value, err := strconv.ParseFloat(strings.TrimSpace(share), 64)
			if err != nil || value <= 0 {
				continue
			}
			fraction = value
		}
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		rules = append(rules, chaosRule{prefix: name, fraction: fraction})
	}
	return rules
}

// SetChaosForTest installs a chaos specification directly, for tests that want
// the seam without an environment variable.
func SetChaosForTest(spec string) func() {
	chaosOnce.Do(func() {})
	previous := chaosRules.Load()
	storeChaos(parseChaos(spec))
	return func() { chaosRules.Store(previous) }
}
