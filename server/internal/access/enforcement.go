package access

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/streamlimits"
)

// Admission failures. Each one is a policy answer, not a fault: the message is
// written for the person who hit it, because a client shows it verbatim.
var (
	ErrStreamLimit   = errors.New("This account already has as many streams playing as its limit allows.")
	ErrSchedule      = errors.New("This account is outside the hours it is allowed to watch.")
	ErrContentRating = errors.New("This title's rating is above the limit set for this account.")
	ErrChannelDenied = errors.New("This channel is not allowed for this account.")
)

// Enforcer applies per-member limits at admission. It reads the limits envelope
// inside the caller's transaction so a limit saved a moment ago is already in
// force on the next operation, with no cache to invalidate.
//
// An administrator is not exempt by accident: limits are stored per account and
// an owner or admin simply has none by default. A stored envelope on an
// administrative account is applied like any other.
type Enforcer struct {
	Store *Store
}

// Admission is what the caller asks about.
type Admission struct {
	// Remote is true when the request arrived over a non-LAN transport; the
	// remote bitrate cap applies only then.
	Remote bool
	// ReplanSessionID is the v1 session this admission continues or replaces
	// (a quality/version/audio/part change, a queue transition, a replacing
	// start). It already holds its stream, so it is not counted again against
	// maxStreams. ReplanMediaID is a prepared next entry's media session that
	// exists before its v1 session does; it is not a second stream either.
	ReplanSessionID string
	ReplanMediaID   string
}

// Decision carries the clamps a permitted request still has to respect.
type Decision struct {
	UploadLimited bool `json:"-"`
	// MaxVideoBitrateBPS is 0 when the account has no remote bitrate cap, or the
	// request is not remote.
	MaxVideoBitrateBPS int
}

func (e *Enforcer) limits(ctx context.Context, tx *sql.Tx, account string) (Limits, error) {
	if e == nil || e.Store == nil {
		return DefaultLimits(), nil
	}
	doc, err := readLimits(tx, account)
	return doc.Limits, err
}

// AdmitSession is the sign-in side: the access schedule. There is no session
// count: how many devices a member has signed in never blocks anyone.
func (e *Enforcer) AdmitSession(ctx context.Context, tx *sql.Tx, p identity.Principal) error {
	return e.AdmitAccount(ctx, tx, p.AccountID)
}

// AdmitAccount is AdmitSession for a caller that knows only the account: the
// identity service applies it to every session it creates or renews.
func (e *Enforcer) AdmitAccount(ctx context.Context, tx *sql.Tx, account string) error {
	limits, err := e.limits(ctx, tx, account)
	if err != nil {
		return err
	}
	return withinSchedule(limits.Schedule, e.now())
}

// AdmitPlayback is the playback side: concurrent stream count, the schedule
// and the remote bitrate cap. The device-bound session and current account
// authority are checked independently on every request. It runs before a playback lease
// exists, so the count it reads excludes the request being admitted. Whether
// the member may see the title at all (rating, unrated, denied labels) is not
// decided here: that is the one catalogue predicate, which the caller applies
// to the same item before asking (contentaccess.VisibleItemTx).
func (e *Enforcer) AdmitPlayback(ctx context.Context, tx *sql.Tx, p identity.Principal, q Admission) (Decision, error) {
	var out Decision
	limits, err := e.limits(ctx, tx, p.AccountID)
	if err != nil {
		return out, err
	}
	now := e.now()
	if err = withinSchedule(limits.Schedule, now); err != nil {
		return out, err
	}
	streams, err := streamlimits.ReadTx(ctx, tx, p.AccountID)
	if err != nil {
		return out, err
	}
	if streams.EffectiveLimit != nil {
		n, e := streamlimits.CountTx(ctx, tx, p.Authority, p.AccountID, true, now, q.ReplanSessionID, q.ReplanMediaID)
		if e != nil {
			return out, e
		}
		if n >= int64(*streams.EffectiveLimit) {
			return out, ErrStreamLimit
		}
	}

	if q.Remote && limits.RemoteBitrateKbps > 0 {
		out.MaxVideoBitrateBPS = limits.RemoteBitrateKbps * 1000
	}
	return out, nil
}

// RemoteBitrateCap is the member's remote bitrate cap in bits per second (zero
// when none), read without admitting anything: a plan preview must match what a
// start would get, and must not count as a stream or observe the device.
func (e *Enforcer) RemoteBitrateCap(ctx context.Context, tx *sql.Tx, p identity.Principal) (int, error) {
	limits, err := e.limits(ctx, tx, p.AccountID)
	if err != nil {
		return 0, err
	}
	return limits.RemoteBitrateKbps * 1000, nil
}

// AdmitChannel applies the access schedule and the live channel allow/deny
// policy. Both the stored lists and the channel under test are canonical v1
// channel ids ("live:<source>:<channel>" or "library:<channel>"); anything
// else never matches, so a non-canonical list entry denies nothing (deny) or
// everything (allow) until it is cleaned.
func (e *Enforcer) AdmitChannel(ctx context.Context, tx *sql.Tx, p identity.Principal, channel string) error {
	limits, err := e.limits(ctx, tx, p.AccountID)
	if err != nil {
		return err
	}
	if err = withinSchedule(limits.Schedule, e.now()); err != nil {
		return err
	}
	return channelAllowed(limits.Channels, channel)
}

// AdmitProgramme applies the account's rating limit to a live guide slot, which
// has no catalogue item to judge. It is the rule the catalogue applies to a
// title, on the same age scale: a programme rated above the limit is refused,
// and one with no rating, or a rating the table does not know, is refused when
// the account may not watch unrated titles. An absent guide slot is unrated.
func (e *Enforcer) AdmitProgramme(ctx context.Context, tx *sql.Tx, p identity.Principal, rating string) error {
	limits, err := e.limits(ctx, tx, p.AccountID)
	if err != nil {
		return err
	}
	age, rated := identity.RatingAge(rating)
	if !rated {
		if !limits.AllowUnrated {
			return ErrContentRating
		}
		return nil
	}
	if ceiling := limits.Ceiling(); ceiling >= 0 && age > ceiling {
		return ErrContentRating
	}
	return nil
}

func channelAllowed(policy ChannelPolicy, channel string) error {
	if policy.Mode == "" || policy.Mode == "all" || channel == "" {
		return nil
	}
	listed := false
	for _, id := range policy.Channels {
		if id == channel {
			listed = true
			break
		}
	}
	if policy.Mode == "allow" && !listed || policy.Mode == "deny" && listed {
		return ErrChannelDenied
	}
	return nil
}

func (e *Enforcer) now() time.Time {
	if e != nil && e.Store != nil && e.Store.Now != nil {
		return e.Store.Now()
	}
	return time.Now()
}

// withinSchedule evaluates the access schedule in its own time zone, or in the
// server's when it names none. An
// empty window list means no restriction. A window whose end minute is not after
// its start wraps past local midnight and is evaluated as two bands.
func withinSchedule(schedule AccessSchedule, now time.Time) error {
	if len(schedule.Windows) == 0 {
		return nil
	}
	// A schedule with no time zone named runs on the server's own clock,
	// which is what the People screen calls "server time".
	location := time.Local
	if schedule.Timezone != "" {
		if loaded, err := time.LoadLocation(schedule.Timezone); err == nil {
			location = loaded
		}
	}
	local := now.In(location)
	day, minute := int(local.Weekday()), local.Hour()*60+local.Minute()
	for _, w := range schedule.Windows {
		if w.EndMinute > w.StartMinute {
			if containsDay(w.Days, day) && minute >= w.StartMinute && minute < w.EndMinute {
				return nil
			}
			continue
		}
		// Wrapping window: the tail of the listed day, or the head of the next.
		if containsDay(w.Days, day) && minute >= w.StartMinute {
			return nil
		}
		if containsDay(w.Days, (day+6)%7) && minute < w.EndMinute {
			return nil
		}
	}
	return ErrSchedule
}

func containsDay(days []int, day int) bool {
	for _, d := range days {
		if d == day {
			return true
		}
	}
	return false
}
