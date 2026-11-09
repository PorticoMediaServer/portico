package mediasource

import (
	"errors"
	"math/big"
	"sort"
)

var (
	ErrTime         = errors.New("invalid rational time")
	ErrTimeOverflow = errors.New("rational time exceeds signed 64-bit representation")
	ErrUnmapped     = errors.New("time is outside mapped source extent")
	ErrTimeline     = errors.New("invalid or ambiguous timeline mapping")
)

// Time is exact seconds. The zero value is zero seconds. Arithmetic normalizes
// fractions and checks overflow after reduction, not before cross multiplication.
type Time struct{ n, d int64 }

func NewTime(numerator, denominator int64) (Time, error) {
	if denominator <= 0 {
		return Time{}, ErrTime
	}
	return fromRat(new(big.Rat).SetFrac(big.NewInt(numerator), big.NewInt(denominator)))
}

func FromTicks(ticks, baseNumerator, baseDenominator int64) (Time, error) {
	if baseNumerator <= 0 || baseDenominator <= 0 {
		return Time{}, ErrTime
	}
	n := new(big.Int).Mul(big.NewInt(ticks), big.NewInt(baseNumerator))
	return fromRat(new(big.Rat).SetFrac(n, big.NewInt(baseDenominator)))
}

func fromRat(r *big.Rat) (Time, error) {
	if !r.Num().IsInt64() || !r.Denom().IsInt64() {
		return Time{}, ErrTimeOverflow
	}
	return Time{r.Num().Int64(), r.Denom().Int64()}, nil
}

func (t Time) rat() *big.Rat {
	d := t.d
	if d == 0 {
		d = 1
	}
	return new(big.Rat).SetFrac(big.NewInt(t.n), big.NewInt(d))
}
func (t Time) Fraction() (int64, int64) {
	if t.d == 0 {
		return 0, 1
	}
	return t.n, t.d
}
func (t Time) Compare(u Time) int       { return t.rat().Cmp(u.rat()) }
func (t Time) Add(u Time) (Time, error) { return fromRat(new(big.Rat).Add(t.rat(), u.rat())) }
func (t Time) Sub(u Time) (Time, error) { return fromRat(new(big.Rat).Sub(t.rat(), u.rat())) }

type Rounding uint8

const (
	Exact Rounding = iota
	Floor
	Ceil
)

// Microseconds is an explicit lossy boundary unless Exact succeeds. Floor/Ceil
// retain their mathematical meaning for negative source origins. No float is used.
func (t Time) Microseconds(mode Rounding) (int64, error) {
	if mode > Ceil {
		return 0, ErrTime
	}
	r := new(big.Rat).Mul(t.rat(), big.NewRat(1000000, 1))
	q, rem := new(big.Int), new(big.Int)
	q.QuoRem(r.Num(), r.Denom(), rem)
	if rem.Sign() != 0 {
		if mode == Exact {
			return 0, ErrTime
		}
		if mode == Floor && rem.Sign() < 0 {
			q.Sub(q, big.NewInt(1))
		}
		if mode == Ceil && rem.Sign() > 0 {
			q.Add(q, big.NewInt(1))
		}
	}
	if !q.IsInt64() {
		return 0, ErrTimeOverflow
	}
	return q.Int64(), nil
}

// Span maps a source extent at unit media rate; playback speed is separate user
// intent. Trims/priming are represented by SourceStart. Omitted source material
// and presentation gaps remain unmapped; this primitive never repairs them.
// Part and Version disambiguate repeated timestamps in multipart media.
type Span struct {
	Part, Version                             string
	SourceStart, SourceEnd, PresentationStart Time
}
type Timeline struct {
	spans []Span
	ends  []Time
}

func NewTimeline(spans []Span) (Timeline, error) {
	if len(spans) == 0 || len(spans) > 4096 {
		return Timeline{}, ErrTimeline
	}
	out := append([]Span(nil), spans...)
	sort.Slice(out, func(i, j int) bool { return out[i].PresentationStart.Compare(out[j].PresentationStart) < 0 })
	ends := make([]Time, len(out))
	for i, s := range out {
		if !validIdentityText(s.Part) || !validIdentityText(s.Version) || s.SourceStart.Compare(s.SourceEnd) >= 0 || s.PresentationStart.Compare(Time{}) < 0 {
			return Timeline{}, ErrTimeline
		}
		duration, err := s.SourceEnd.Sub(s.SourceStart)
		if err != nil {
			return Timeline{}, err
		}
		end, err := s.PresentationStart.Add(duration)
		if err != nil {
			return Timeline{}, err
		}
		ends[i] = end
		if i > 0 && s.PresentationStart.Compare(ends[i-1]) < 0 {
			return Timeline{}, ErrTimeline
		}
		for j := 0; j < i; j++ {
			p := out[j]
			if p.Part == s.Part && p.Version == s.Version && s.SourceStart.Compare(p.SourceEnd) < 0 && p.SourceStart.Compare(s.SourceEnd) < 0 {
				return Timeline{}, ErrTimeline
			}
		}
	}
	return Timeline{spans: out, ends: ends}, nil
}

// MapSource uses half-open intervals. Exact end-of-item completion is a separate
// observation, not a fictitious sample at the end of a source span.
func (t Timeline) MapSource(part, version string, source Time) (Time, error) {
	for _, s := range t.spans {
		if s.Part == part && s.Version == version && source.Compare(s.SourceStart) >= 0 && source.Compare(s.SourceEnd) < 0 {
			delta, err := source.Sub(s.SourceStart)
			if err != nil {
				return Time{}, err
			}
			return s.PresentationStart.Add(delta)
		}
	}
	return Time{}, ErrUnmapped
}

func (t Timeline) MapPresentation(position Time) (part, version string, source Time, err error) {
	for i, s := range t.spans {
		if position.Compare(s.PresentationStart) < 0 || position.Compare(t.ends[i]) >= 0 {
			continue
		}
		delta, e := position.Sub(s.PresentationStart)
		if e != nil {
			return "", "", Time{}, e
		}
		candidate, e := s.SourceStart.Add(delta)
		if e != nil {
			return "", "", Time{}, e
		}
		return s.Part, s.Version, candidate, nil
	}
	return "", "", Time{}, ErrUnmapped
}
