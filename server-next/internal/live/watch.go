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

// StopDevice ends what one sign-in is watching live, before it tunes something else: changing
// channel gives its tuner back first, so a device can change channel on a source whose tuners
// are all in use, one of them by the device itself.
func (v *Viewings) StopDevice(userID, signInID int64) {
	v.mu.Lock()
	var mine []*Viewing
	for _, other := range v.byID {
		if other.UserID == userID && other.SignInID == signInID {
			mine = append(mine, other)
		}
	}
	v.mu.Unlock()
	for _, w := range mine {
		v.Stop(w.ID, userID)
	}
}

// Start records a viewing of an input the caller has already tuned with use.
func (v *Viewings) Start(userID, profileID, signInID int64, channelID string, in *Input, use *Use) *Viewing {
	v.mu.Lock()
	w := &Viewing{ID: "lvs_" + token(10), UserID: userID, ProfileID: profileID, SignInID: signInID, ChannelID: channelID, Grant: token(24),
		Input: in, use: use, Lease: v.now().Add(Lease)}
	v.byID[w.ID], v.byGrant[w.Grant] = w, w
	v.mu.Unlock()
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

// Count is how many live viewings an account has on other sign-ins, and the server
// altogether, for stream limits: the asking device's own is about to be replaced.
func (v *Viewings) Count(userID, signInID int64) (account, total int) {
	v.mu.Lock()
	defer v.mu.Unlock()
	for _, w := range v.byID {
		total++
		if w.UserID == userID && w.SignInID != signInID {
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
