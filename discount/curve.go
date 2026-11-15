// Package discount is the risk-free discount curve used to present-value cash flows.
package discount

import (
	"fmt"
	"math"
	"sort"
	"time"

	"pricer/daycount"
)

// Curve is a deterministic risk-free discount curve.
// DiscountFactor(from, to) is the value on from of 1 unit paid on to.
type Curve interface {
	DiscountFactor(from, to time.Time) (float64, error)
	Describe() string
}

// Flat is a constant risk-free rate.
// Frequency 0 means continuous compounding. Otherwise the rate is a nominal
// rate compounded Frequency times per year (1 = annual, 2 = semi-annual).
type Flat struct {
	Rate      float64
	Frequency int
	DayCount  daycount.Convention
}

func (f Flat) Describe() string {
	if f.Frequency == 0 {
		return fmt.Sprintf("flat %.6f%% continuous, %s", f.Rate*100, f.DayCount)
	}
	return fmt.Sprintf("flat %.6f%% compounded %d times/year, %s", f.Rate*100, f.Frequency, f.DayCount)
}

func (f Flat) DiscountFactor(from, to time.Time) (float64, error) {
	t, err := f.DayCount.YearFraction(from, to)
	if err != nil {
		return 0, err
	}
	if t == 0 {
		return 1, nil
	}
	if f.Frequency == 0 {
		df := math.Exp(-f.Rate * t)
		if !isDF(df) {
			return 0, fmt.Errorf("flat curve produced a non-finite discount factor")
		}
		return df, nil
	}
	if f.Frequency < 1 {
		return 0, fmt.Errorf("compounding frequency must be 0 (continuous) or positive")
	}
	base := 1 + f.Rate/float64(f.Frequency)
	if base <= 0 {
		return 0, fmt.Errorf("flat rate %v is not valid for frequency %d", f.Rate, f.Frequency)
	}
	df := math.Pow(base, -float64(f.Frequency)*t)
	if !isDF(df) {
		return 0, fmt.Errorf("flat curve produced a non-finite discount factor")
	}
	return df, nil
}

// Pillar is a continuously compounded zero rate from the curve anchor to Date.
type Pillar struct {
	Date           time.Time
	ContinuousZero float64
}

// ZeroCurve is a risk-free curve of continuously compounded zero rates.
// Zeros are interpolated linearly in year-fraction time. Before the first
// pillar the first zero is used. Past the last pillar the last segment's
// continuous forward is held flat (the zero itself, if there is one pillar).
type ZeroCurve struct {
	AsOf     time.Time
	Pillars  []Pillar
	DayCount daycount.Convention
}

func (z ZeroCurve) Describe() string {
	return fmt.Sprintf("%d zero pillars, continuous, %s, log-linear discount factors", len(z.Pillars), z.DayCount)
}

func (z ZeroCurve) DiscountFactor(from, to time.Time) (float64, error) {
	dfTo, err := z.dfFromAsOf(to)
	if err != nil {
		return 0, err
	}
	dfFrom, err := z.dfFromAsOf(from)
	if err != nil {
		return 0, err
	}
	if dfFrom == 0 {
		return 0, fmt.Errorf("discount factor to the curve start date is zero")
	}
	df := dfTo / dfFrom
	if !isDF(df) {
		return 0, fmt.Errorf("zero curve produced a non-finite discount factor")
	}
	return df, nil
}

func (z ZeroCurve) dfFromAsOf(date time.Time) (float64, error) {
	if len(z.Pillars) == 0 {
		return 0, fmt.Errorf("zero curve has no pillars")
	}
	pillars := append([]Pillar(nil), z.Pillars...)
	sort.Slice(pillars, func(i, j int) bool {
		return pillars[i].Date.Before(pillars[j].Date)
	})
	asOf := daycount.Normalize(z.AsOf)
	t, err := z.DayCount.YearFraction(asOf, date)
	if err != nil {
		return 0, err
	}
	if t <= 0 {
		return 1, nil
	}
	nodes := make([]zeroNode, 0, len(pillars))
	for _, p := range pillars {
		pt, err := z.DayCount.YearFraction(asOf, p.Date)
		if err != nil {
			return 0, err
		}
		if pt <= 0 {
			return 0, fmt.Errorf("pillar %s is not after the curve as-of date", p.Date.Format("2006-01-02"))
		}
		nodes = append(nodes, zeroNode{t: pt, z: p.ContinuousZero})
	}
	zero, err := interpolatedZero(nodes, t)
	if err != nil {
		return 0, err
	}
	return math.Exp(-zero * t), nil
}

type zeroNode struct {
	t float64
	z float64
}

func interpolatedZero(nodes []zeroNode, t float64) (float64, error) {
	if len(nodes) == 0 {
		return 0, fmt.Errorf("zero curve has no pillars")
	}
	if t <= nodes[0].t {
		return nodes[0].z, nil
	}
	last := nodes[len(nodes)-1]
	if t >= last.t {
		forward := last.z
		if len(nodes) >= 2 {
			prev := nodes[len(nodes)-2]
			span := last.t - prev.t
			if span <= 0 {
				return 0, fmt.Errorf("zero pillars are not strictly increasing in time")
			}
			forward = (last.z*last.t - prev.z*prev.t) / span
		}
		return (last.z*last.t + forward*(t-last.t)) / t, nil
	}
	for i := 1; i < len(nodes); i++ {
		if t <= nodes[i].t {
			left, right := nodes[i-1], nodes[i]
			span := right.t - left.t
			if span <= 0 {
				return 0, fmt.Errorf("zero pillars are not strictly increasing in time")
			}
			w := (t - left.t) / span
			return left.z + w*(right.z-left.z), nil
		}
	}
	return 0, fmt.Errorf("failed to interpolate zero rate")
}

func isDF(df float64) bool {
	return !math.IsNaN(df) && !math.IsInf(df, 0) && df > 0
}
