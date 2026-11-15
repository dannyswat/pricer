// Package eln prices cash-settled equity-linked notes from the issuer's side.
//
// The investor pays the issue price at inception. At expiry a vanilla note
// pays notional if the spot is at or above the strike, and notional × spot /
// strike otherwise. A worst-of note uses the worst performance in the basket
// in place of spot / strike. Either payoff is a discounted notional minus a
// put the investor is short, so the issuer is long that put.
//
// The issuer price adds two charges to the mid-market value of that payoff:
// the cost of a volatility haircut on the embedded put, and a markup.
package eln

import (
	"errors"
	"math"
	"time"

	"pricer/daycount"
	"pricer/discount"
	"pricer/option"
	"pricer/pricing"
)

var (
	ErrInvalidNotional = errors.New("notional must be positive")
	ErrInvalidMarkup   = errors.New("markup must be finite")
	ErrInvalidBump     = errors.New("vol bump must be between zero and volatility")
)

// Vanilla is a fixed-strike equity-linked note with no barrier.
// VolBump is subtracted from Volatility when the embedded put is credited to
// the investor. The issuer is long that put, so the haircut is the cost of
// selling the hedge below mid vol. Markup is a fraction of notional (0.005 is
// 50 bps) added to the price the investor pays.
type Vanilla struct {
	Notional      float64
	Spot          float64
	Strike        float64
	Expiry        time.Time
	Volatility    float64
	DividendYield float64
	DayCount      daycount.Convention
	VolBump       float64
	Markup        float64
}

// Quote is the issuer's price of a vanilla ELN.
// TheoreticalValue is the mid-market value of the investor's payoff.
// IssuePrice is what the investor pays: theoretical value, plus the cost of
// the vol bump, plus markup. Day1Margin is issue price minus theoretical value.
type Quote struct {
	TheoreticalValue float64
	PutPremium       float64
	BumpedPutPremium float64
	PutQuantity      float64
	DiscountFactor   float64
	YearFraction     float64
	CostOfBump       float64
	Markup           float64
	IssuePrice       float64
	Day1Margin       float64
	MaxYield         float64
	InvestorDelta    float64
	// HedgeShares is the number of shares the issuer buys to flatten the note.
	HedgeShares float64
}

// TheoreticalPrice is the mid-market value of the investor payoff, with no
// vol bump and no markup. Result.Dirty is that value.
func (n Vanilla) TheoreticalPrice(asOf time.Time, curve discount.Curve) (pricing.Result, error) {
	q, err := n.Price(asOf, curve)
	if err != nil {
		return pricing.Result{}, err
	}
	return pricing.Result{Dirty: q.TheoreticalValue, Clean: q.TheoreticalValue}, nil
}

// Price returns the issuer issue price and its bump and markup components.
func (n Vanilla) Price(asOf time.Time, curve discount.Curve) (Quote, error) {
	if err := n.validate(); err != nil {
		return Quote{}, err
	}
	mid, err := n.embeddedPut(n.Volatility).Price(asOf, curve)
	if err != nil {
		return Quote{}, err
	}
	chargedVol := n.Volatility - n.VolBump
	charged := mid
	if n.VolBump != 0 {
		charged, err = n.embeddedPut(chargedVol).Price(asOf, curve)
		if err != nil {
			return Quote{}, err
		}
	}
	quantity := n.Notional / n.Strike
	theoretical := mid.DiscountFactor*n.Notional - mid.Premium
	costOfBump := mid.Premium - charged.Premium
	markup := n.Markup * n.Notional
	issue := theoretical + costOfBump + markup
	days := daycount.Days(asOf, n.Expiry)
	maxYield := 0.0
	if issue > 0 && days > 0 {
		maxYield = (n.Notional/issue - 1) * 365 / float64(days)
	}
	return Quote{
		TheoreticalValue: theoretical,
		PutPremium:       mid.Premium,
		BumpedPutPremium: charged.Premium,
		PutQuantity:      quantity,
		DiscountFactor:   mid.DiscountFactor,
		YearFraction:     mid.YearFraction,
		CostOfBump:       costOfBump,
		Markup:           markup,
		IssuePrice:       issue,
		Day1Margin:       costOfBump + markup,
		MaxYield:         maxYield,
		InvestorDelta:    -mid.Delta,
		HedgeShares:      -mid.Delta,
	}, nil
}

func (n Vanilla) embeddedPut(vol float64) option.EuropeanPut {
	return option.EuropeanPut{
		Spot:          n.Spot,
		Strike:        n.Strike,
		Expiry:        n.Expiry,
		Volatility:    vol,
		DividendYield: n.DividendYield,
		Quantity:      n.Notional / n.Strike,
		DayCount:      n.DayCount,
	}
}

func (n Vanilla) validate() error {
	if !(n.Notional > 0) || math.IsNaN(n.Notional) || math.IsInf(n.Notional, 0) {
		return ErrInvalidNotional
	}
	if math.IsNaN(n.Markup) || math.IsInf(n.Markup, 0) {
		return ErrInvalidMarkup
	}
	if math.IsNaN(n.VolBump) || math.IsInf(n.VolBump, 0) || n.VolBump < 0 || n.Volatility-n.VolBump < 0 {
		return ErrInvalidBump
	}
	if !(n.Strike > 0) {
		return option.ErrInvalidStrike
	}
	return nil
}
