package live

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
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
	// Away is a viewer outside the home networks; Name is the device's name; StartedAt is
	// when it tuned in. For the owner's Now playing.
	Away      bool
	Name      string
	StartedAt time.Time
	// CountdownTo, when set, is when this stream stops so a recording can take its tuner, and
	// CountdownReason says so in a sentence.
	CountdownTo     time.Time
	CountdownReason string
	// Play is how the device plays the channel when it cannot take the source's own stream:
	// nil for the source's MPEG-TS as it is.
	Play *Play
}

// Play is HLS made for one device from a channel's shared input, for a device that cannot
// play the source's MPEG-TS as it is (package mediaengine makes it).
type Play struct {
	// Route is repackaged (the source's picture and sound, in HLS) or converted.
	Route string
	// Engine is the device's player that plays it; Container is mpegts_hls or fmp4_hls.
	Engine, Container string
}

// Conversion is a device starting to watch a channel, for the media engine to decide how it
// plays it: the source's MPEG-TS as it is, or HLS made for the device (a Play).
type Conversion struct {
	// SessionID is the viewing the HLS belongs to.
	SessionID string
	Input     *Input
	// Capabilities is the document the device last published; nil plans for a device that
	// plays only the most widely supported formats.
	Capabilities json.RawMessage
	// Preferences are the profile's and the device's settings, key to JSON value.
	Preferences map[string]string
	// Network is local, remote or cellular; Limits is what the account is held to away from
	// home.
	Network string
	Limits  db.PlayLimits
}

// Viewings holds every live viewing.
type Viewings struct {
	mu      sync.Mutex
	byID    map[string]*Viewing
	byGrant map[string]*Viewing
	now     func() time.Time
	// Ended is told each viewing that ends, however it ends, so what was made for it stops
	// too. Set it before the viewings are used.
	Ended func(id string)
	// stopped are the viewings the owner or an administrator stopped, kept a while so their
	// device is told why when it next reports.
	stopped map[string]stoppedViewing
	// held are places taken for viewings still tuning: they count as streams until the
	// viewing starts or the tune fails.
	held map[string]heldPlace
}

// Lease is how long a viewing lives without a report from its device; it reports every
// ten seconds.
const (
	Lease       = time.Minute
	ReportEvery = 10 * time.Second
)

// NewViewings makes the registry.
func NewViewings(now func() time.Time) *Viewings {
	return &Viewings{byID: map[string]*Viewing{}, byGrant: map[string]*Viewing{}, now: now, stopped: map[string]stoppedViewing{}, held: map[string]heldPlace{}}
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

// Start records a viewing of an input the caller has already tuned with use, in the place
// held (Hold) for it, if any.
func (v *Viewings) Start(userID, profileID, signInID int64, channelID string, in *Input, use *Use, held string) *Viewing {
	v.mu.Lock()
	delete(v.held, held)
	w := &Viewing{ID: "lvs_" + token(10), UserID: userID, ProfileID: profileID, SignInID: signInID, ChannelID: channelID, Grant: token(24),
		Input: in, use: use, Lease: v.now().Add(Lease), Away: use.Away, Name: use.Name, StartedAt: v.now()}
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
func (v *Viewings) ByGrant(grant string) (Viewing, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	w := v.byGrant[grant]
	if w == nil {
		return Viewing{}, false
	}
	return *w, true
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
	if v.Ended != nil {
		v.Ended(id)
	}
	w.Input.Leave(w.use)
}

// SetPlay records how a viewing's device plays its channel, and says whether the viewing is
// still there: one that ended meanwhile (the device started again) is not.
func (v *Viewings) SetPlay(id string, play *Play) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	w := v.byID[id]
	if w != nil {
		w.Play = play
	}
	return w != nil
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
		for id, st := range v.stopped {
			if v.now().Sub(st.at) > Lease {
				delete(v.stopped, id)
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
