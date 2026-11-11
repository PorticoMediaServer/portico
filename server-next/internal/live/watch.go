package live

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"strings"
	"sync"
	"time"
)

// Viewing is one device watching a source's channel. It is running state: a restart ends it
// and the app joins the channel again at live.
type Viewing struct {
	ID        string
	UserID    int64
	ProfileID int64
	SignInID  int64
	ChannelID string
	Grant     string // the stream's address is named by it, and it is the permission to read it
	Input     *Input
	use       *Use
	Paused    bool
	Lease     time.Time
	// CountdownTo, when set, is when this stream stops so a recording can take its tuner, and
	// CountdownReason says so in a sentence.
	CountdownTo     time.Time
	CountdownReason string
}

// Viewings holds every live viewing.
type Viewings struct {
	mu      sync.Mutex
	byID    map[string]*Viewing
	byGrant map[string]*Viewing
	now     func() time.Time
}

// Lease is how long a viewing lives without a report from its device; it reports every
// ten seconds.
const (
	Lease       = time.Minute
	ReportEvery = 10 * time.Second
)

// NewViewings makes the registry.
func NewViewings(now func() time.Time) *Viewings {
	return &Viewings{byID: map[string]*Viewing{}, byGrant: map[string]*Viewing{}, now: now}
}

func token(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// IsLive says whether a playback session id names a live viewing.
func IsLive(id string) bool { return strings.HasPrefix(id, "lvs_") }

// Start records a viewing of an input the caller has already tuned with use. A device
// watching something else live stops watching it.
func (v *Viewings) Start(userID, profileID, signInID int64, channelID string, in *Input, use *Use) *Viewing {
	v.mu.Lock()
	var replaced []*Viewing
	for _, other := range v.byID {
		if signInID != 0 && other.SignInID == signInID {
			replaced = append(replaced, other)
		}
	}
	w := &Viewing{ID: "lvs_" + token(10), UserID: userID, ProfileID: profileID, SignInID: signInID, ChannelID: channelID, Grant: token(24),
		Input: in, use: use, Lease: v.now().Add(Lease)}
	v.byID[w.ID], v.byGrant[w.Grant] = w, w
	v.mu.Unlock()
	for _, old := range replaced {
		v.Stop(old.ID, old.UserID)
	}
	return w
}

// Get answers a viewing of the account, or false.
func (v *Viewings) Get(id string, userID int64) (Viewing, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	w := v.byID[id]
	if w == nil || w.UserID != userID {
		return Viewing{}, false
	}
	return *w, true
}

// Report keeps a viewing alive and records whether it is paused.
func (v *Viewings) Report(id string, userID int64, paused bool) (Viewing, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	w := v.byID[id]
	if w == nil || w.UserID != userID {
		return Viewing{}, false
	}
	w.Lease, w.Paused = v.now().Add(Lease), paused
	return *w, true
}

// ByGrant finds the viewing a stream address belongs to.
func (v *Viewings) ByGrant(grant string) (*Input, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	w := v.byGrant[grant]
	if w == nil {
		return nil, false
	}
	return w.Input, true
}

// Stop ends a viewing, letting go of its input. Stopping one that is gone succeeds.
func (v *Viewings) Stop(id string, userID int64) {
	v.mu.Lock()
	w := v.byID[id]
	if w == nil || w.UserID != userID {
		v.mu.Unlock()
		return
	}
	delete(v.byID, id)
	delete(v.byGrant, w.Grant)
	v.mu.Unlock()
	w.Input.Leave(w.use)
}

// Count is how many live viewings an account has, and the server altogether, for stream limits.
func (v *Viewings) Count(userID int64) (account, total int) {
	v.mu.Lock()
	defer v.mu.Unlock()
	for _, w := range v.byID {
		total++
		if w.UserID == userID {
			account++
		}
	}
	return account, total
}

// Run ends viewings whose device stopped reporting, until ctx ends.
func (v *Viewings) Run(ctx context.Context) {
	tick := time.NewTicker(ReportEvery)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		v.mu.Lock()
		var gone []*Viewing
		for _, w := range v.byID {
			if v.now().After(w.Lease) {
				gone = append(gone, w)
			}
		}
		v.mu.Unlock()
		for _, w := range gone {
			v.Stop(w.ID, w.UserID)
		}
	}
}

// OnSource lists the viewings watching a source's channels other than one, for a recording
// that needs one of their tuners. The oldest come first.
func (v *Viewings) OnSource(sourceID, exceptChannel int64) []string {
	v.mu.Lock()
	defer v.mu.Unlock()
	var out []string
	for id, w := range v.byID {
		if w.Input.SourceID == sourceID && w.Input.ChannelID != exceptChannel {
			out = append(out, id)
		}
	}
	return out
}

// Warn tells a viewing, through its session, that its stream stops at until and why.
func (v *Viewings) Warn(id string, until time.Time, reason string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if w := v.byID[id]; w != nil {
		w.CountdownTo, w.CountdownReason = until, reason
	}
}

// End stops a viewing whatever account it belongs to: the recorder taking its tuner.
func (v *Viewings) End(id string) {
	v.mu.Lock()
	w := v.byID[id]
	v.mu.Unlock()
	if w != nil {
		v.Stop(id, w.UserID)
	}
}
