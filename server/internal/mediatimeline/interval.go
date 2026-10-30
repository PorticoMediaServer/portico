// Package mediatimeline maps explicitly selected source clocks without treating
// catalog or provider duration as measured playback evidence.
package mediatimeline

import (
	"errors"
	"math/big"
)

var ErrInterval = errors.New("source clock cannot define a bounded timeline")

// Rational is canonical: denominator positive and numerator/denominator reduced.
type Rational struct{ Numerator, Denominator int64 }

// Interval requires an observed start and duration in the selected stream's
// clock. Trim offsets, when present, are exact seconds relative to that start.
// Missing stream timing must not be supplied as an assumed zero.
type Interval struct {
	StartTicks, DurationTicks int64
	TimeBase                  Rational
	TrimStart, TrimEnd        *Rational
}

type Mapping struct {
	SourceStart, SourceEnd Rational
	DurationMicroseconds   int64
}

func exact(v Rational) (*big.Rat, bool) {
	if v.Denominator <= 0 {
		return nil, false
	}
	r := new(big.Rat).SetFrac(big.NewInt(v.Numerator), big.NewInt(v.Denominator))
	return r, r.Num().IsInt64() && r.Denom().IsInt64() && r.Num().Int64() == v.Numerator && r.Denom().Int64() == v.Denominator
}

func bounded(r *big.Rat) (Rational, bool) {
	if !r.Num().IsInt64() || !r.Denom().IsInt64() {
		return Rational{}, false
	}
	return Rational{r.Num().Int64(), r.Denom().Int64()}, true
}

// Map performs exact subtraction before rounding the positive duration down
// once to microseconds. It preserves source offsets (including negative starts)
// separately from the zero-based presentation duration. It grants no source,
// publication, playback, or schedule authority.
func Map(v Interval) (Mapping, error) {
	tb, ok := exact(v.TimeBase)
	if !ok || tb.Sign() <= 0 || v.DurationTicks <= 0 {
		return Mapping{}, ErrInterval
	}
	start := new(big.Rat).Mul(new(big.Rat).SetInt64(v.StartTicks), tb)
	duration := new(big.Rat).Mul(new(big.Rat).SetInt64(v.DurationTicks), tb)
	from, until := new(big.Rat), new(big.Rat).Set(duration)
	if v.TrimStart != nil {
		from, ok = exact(*v.TrimStart)
		if !ok {
			return Mapping{}, ErrInterval
		}
	}
	if v.TrimEnd != nil {
		until, ok = exact(*v.TrimEnd)
		if !ok {
			return Mapping{}, ErrInterval
		}
	}
	if from.Sign() < 0 || until.Cmp(from) <= 0 || until.Cmp(duration) > 0 {
		return Mapping{}, ErrInterval
	}
	span := new(big.Rat).Sub(until, from)
	micros := new(big.Rat).Mul(span, new(big.Rat).SetInt64(1_000_000))
	whole := new(big.Int).Quo(micros.Num(), micros.Denom())
	if !whole.IsInt64() || whole.Sign() <= 0 {
		return Mapping{}, ErrInterval
	}
	a, aOK := bounded(new(big.Rat).Add(start, from))
	b, bOK := bounded(new(big.Rat).Add(start, until))
	if !aOK || !bOK {
		return Mapping{}, ErrInterval
	}
	return Mapping{SourceStart: a, SourceEnd: b, DurationMicroseconds: whole.Int64()}, nil
}
