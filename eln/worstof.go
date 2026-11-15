package eln

import (
	"math"
	"time"

	"pricer/daycount"
	"pricer/discount"
	"pricer/option"
	"pricer/pricing"
)

// WorstOf is an equity-linked note on the worst performer of a basket.
// At expiry the issuer pays Notional if every underlier finishes at or above
// Strike × its initial level, and Notional × min_i(S_i/Initial_i) / Strike
// otherwise. Strike is a fraction of each initial level (0.95 is 95%).
//
// That payoff is a discounted notional minus a worst-of put with notional
// Notional/Strike. The put is priced by Monte Carlo; Paths and Seed are
// forwarded, and a zero Paths uses option.DefaultPaths. VolBump is subtracted
// from every underlier's volatility when the put is credited to the investor.
// Markup is a fraction of notional added to the price the investor pays.
type WorstOf struct {
	Notional    float64
	Underliers  []option.Underlier
	Correlation [][]float64
	Strike      float64
	Expiry      time.Time
	DayCount    daycount.Convention
	VolBump     float64
	Markup      float64
	Paths       int
	Seed        uint64
}

// WorstOfQuote is the issuer's price of a worst-of ELN.
// TheoreticalValue is the mid-market value of the investor's payoff, and
// TheoreticalStdErr is the Monte Carlo standard error of that value.
// IssuePrice is theoretical value plus the cost of the vol bump plus markup.
// Because that equals the haircut-vol note plus markup, IssueStdErr is the
// standard error of the haircut put. InvestorDeltas and HedgeShares are per
// underlier, in basket order. HedgeShares is the number of shares the issuer
// buys to flatten the note.
type WorstOfQuote struct {
	TheoreticalValue  float64
	TheoreticalStdErr float64
	PutPremium        float64
	PutStdErr         float64
	BumpedPutPremium  float64
	BumpedPutStdErr   float64
	PutNotional       float64
	DiscountFactor    float64
	YearFraction      float64
	CostOfBump        float64
	Markup            float64
	IssuePrice        float64
	IssueStdErr       float64
	Day1Margin        float64
	MaxYield          float64
	Paths             int
	InvestorDeltas    []float64
	HedgeShares       []float64
	WorstProb         []float64
}

// TheoreticalPrice is the mid-market value of the investor payoff, with no
// vol bump and no markup. Result.Dirty is that value.
func (n WorstOf) TheoreticalPrice(asOf time.Time, curve discount.Curve) (pricing.Result, error) {
	q, err := n.Price(asOf, curve)
	if err != nil {
		return pricing.Result{}, err
	}
	return pricing.Result{Dirty: q.TheoreticalValue, Clean: q.TheoreticalValue}, nil
}

// Price returns the issuer issue price and its bump and markup components.
func (n WorstOf) Price(asOf time.Time, curve discount.Curve) (WorstOfQuote, error) {
	if err := n.validate(); err != nil {
		return WorstOfQuote{}, err
	}
	mid, err := n.embeddedPut(0).Price(asOf, curve)
	if err != nil {
		return WorstOfQuote{}, err
	}
	charged := mid
	if n.VolBump != 0 {
		charged, err = n.embeddedPut(n.VolBump).Price(asOf, curve)
		if err != nil {
			return WorstOfQuote{}, err
		}
	}
	theoretical := mid.DiscountFactor*n.Notional - mid.Premium
	costOfBump := mid.Premium - charged.Premium
	markup := n.Markup * n.Notional
	issue := theoretical + costOfBump + markup
	days := daycount.Days(asOf, n.Expiry)
	maxYield := 0.0
	if issue > 0 && days > 0 {
		maxYield = (n.Notional/issue - 1) * 365 / float64(days)
	}
	deltas := make([]float64, len(mid.Deltas))
	hedge := make([]float64, len(mid.Deltas))
	for i, d := range mid.Deltas {
		deltas[i] = -d
		hedge[i] = -d
	}
	return WorstOfQuote{
		TheoreticalValue:  theoretical,
		TheoreticalStdErr: mid.StdErr,
		PutPremium:        mid.Premium,
		PutStdErr:         mid.StdErr,
		BumpedPutPremium:  charged.Premium,
		BumpedPutStdErr:   charged.StdErr,
		PutNotional:       n.Notional / n.Strike,
		DiscountFactor:    mid.DiscountFactor,
		YearFraction:      mid.YearFraction,
		CostOfBump:        costOfBump,
		Markup:            markup,
		IssuePrice:        issue,
		IssueStdErr:       charged.StdErr,
		Day1Margin:        costOfBump + markup,
		MaxYield:          maxYield,
		Paths:             mid.Paths,
		InvestorDeltas:    deltas,
		HedgeShares:       hedge,
		WorstProb:         mid.WorstProb,
	}, nil
}

func (n WorstOf) embeddedPut(bump float64) option.WorstOfPut {
	us := make([]option.Underlier, len(n.Underliers))
	for i, u := range n.Underliers {
		u.Volatility -= bump
		us[i] = u
	}
	return option.WorstOfPut{
		Underliers:  us,
		Correlation: n.Correlation,
		Strike:      n.Strike,
		Expiry:      n.Expiry,
		Notional:    n.Notional / n.Strike,
		DayCount:    n.DayCount,
		Paths:       n.Paths,
		Seed:        n.Seed,
	}
}

func (n WorstOf) validate() error {
	if !(n.Notional > 0) || math.IsNaN(n.Notional) || math.IsInf(n.Notional, 0) {
		return ErrInvalidNotional
	}
	if math.IsNaN(n.Markup) || math.IsInf(n.Markup, 0) {
		return ErrInvalidMarkup
	}
	if !(n.Strike > 0) || math.IsNaN(n.Strike) || math.IsInf(n.Strike, 0) {
		return option.ErrInvalidStrike
	}
	if len(n.Underliers) == 0 {
		return option.ErrInvalidBasket
	}
	if math.IsNaN(n.VolBump) || math.IsInf(n.VolBump, 0) || n.VolBump < 0 {
		return ErrInvalidBump
	}
	for _, u := range n.Underliers {
		if u.Volatility-n.VolBump < 0 || math.IsNaN(u.Volatility) {
			return ErrInvalidBump
		}
	}
	return nil
}
