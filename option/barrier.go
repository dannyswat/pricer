package option

import (
	"fmt"
	"math"
	"time"

	"pricer/daycount"
	"pricer/discount"
	"pricer/pricing"
)

// DownInPut is a European down-and-in put. It pays max(Strike−spot, 0) at
// Expiry only if the spot has touched Barrier from above. The barrier is
// monitored continuously and there is no rebate.
//
// If Spot is already at or below Barrier, the put has knocked in and is a
// vanilla put. If Barrier is at or above Strike and the spot has not knocked
// in, every path that finishes below the strike has crossed the barrier, so
// the price is again the vanilla put. Otherwise the price is the
// Reiner–Rubinstein formula (Haug). Volatility and the dividend yield are
// constant, and the dividend yield is continuous.
type DownInPut struct {
	Spot          float64
	Strike        float64
	Barrier       float64
	Expiry        time.Time
	Volatility    float64
	DividendYield float64
	Quantity      float64
	DayCount      daycount.Convention
}

func (p DownInPut) terms() contract {
	return contract{p.Spot, p.Strike, p.Expiry, p.Volatility, p.DividendYield, p.Quantity, p.DayCount}
}

// TheoreticalPrice reports the premium in Result.Dirty.
func (p DownInPut) TheoreticalPrice(asOf time.Time, curve discount.Curve) (pricing.Result, error) {
	q, err := p.Price(asOf, curve)
	if err != nil {
		return pricing.Result{}, err
	}
	return pricing.Result{Dirty: q.Premium, Clean: q.Premium}, nil
}

// Price returns the premium of the down-and-in put. Greeks are for the
// position. Where the contract is a vanilla put they are the Black-Scholes
// Greeks; otherwise they are central differences of the closed form that do
// not step across the barrier.
func (p DownInPut) Price(asOf time.Time, curve discount.Curve) (Quote, error) {
	if err := p.validateBarrier(); err != nil {
		return Quote{}, err
	}
	yearFrac, df, err := p.terms().resolve(asOf, curve)
	if err != nil {
		return Quote{}, err
	}
	return priceDownInPut(p.Spot, p.Strike, p.Barrier, yearFrac, df, p.Volatility, p.DividendYield, p.Quantity)
}

func (p DownInPut) validateBarrier() error {
	if !(p.Barrier > 0) || math.IsNaN(p.Barrier) || math.IsInf(p.Barrier, 0) {
		return ErrInvalidBarrier
	}
	return nil
}

func priceDownInPut(spot, strike, barrier, yearFrac, df, vol, dividend, quantity float64) (Quote, error) {
	if math.IsNaN(df) || math.IsInf(df, 0) || df <= 0 {
		return Quote{}, fmt.Errorf("discount factor must be positive")
	}
	if yearFrac < 0 || math.IsNaN(yearFrac) || math.IsInf(yearFrac, 0) {
		return Quote{}, fmt.Errorf("year fraction must be non-negative")
	}
	knockedIn := spot <= barrier
	// A finish below a strike at or below the barrier has already crossed it.
	if knockedIn || strike <= barrier {
		return pricePut(spot, strike, yearFrac, df, vol, dividend, quantity)
	}
	growth := math.Exp(-dividend * yearFrac)
	forward := spot
	rate := 0.0
	if yearFrac > 0 {
		forward = spot * growth / df
		rate = -math.Log(df) / yearFrac
	}
	if yearFrac == 0 || vol*math.Sqrt(yearFrac) < 1e-14 {
		return zeroVolDownIn(spot, strike, barrier, yearFrac, df, forward, rate, dividend, growth, quantity), nil
	}
	unit := downInPutValue(spot, strike, barrier, yearFrac, df, vol, dividend)
	delta, gamma, vega, theta, rho := downInPutGreeks(spot, strike, barrier, yearFrac, df, vol, dividend, rate, unit)
	volSqrtT := vol * math.Sqrt(yearFrac)
	d1 := (math.Log(forward/strike) + 0.5*vol*vol*yearFrac) / volSqrtT
	premium := unit * quantity
	return Quote{
		Premium:        premium,
		Forward:        forward,
		DiscountFactor: df,
		YearFraction:   yearFrac,
		Rate:           rate,
		D1:             d1,
		D2:             d1 - volSqrtT,
		Delta:          delta * quantity,
		Gamma:          gamma * quantity,
		Vega:           vega * quantity,
		Theta:          theta * quantity,
		Rho:            rho * quantity,
		TimeValue:      premium,
	}, nil
}

// downInPutValue is the Reiner–Rubinstein down-and-in put with no rebate.
// Spot is above the barrier and the strike is above the barrier.
func downInPutValue(spot, strike, barrier, yearFrac, df, vol, dividend float64) float64 {
	r := -math.Log(df) / yearFrac
	sigma := vol * math.Sqrt(yearFrac)
	mu := (r-dividend)/(vol*vol) - 0.5
	// B(-1) - C(1,-1) + D(1,-1)
	return barrierB(spot, strike, barrier, yearFrac, df, dividend, sigma, mu, -1) -
		barrierC(spot, strike, barrier, yearFrac, df, dividend, sigma, mu, 1, -1) +
		barrierD(spot, strike, barrier, yearFrac, df, dividend, sigma, mu, 1, -1)
}

func barrierB(spot, strike, barrier, yearFrac, df, dividend, sigma, mu, phi float64) float64 {
	x2 := math.Log(spot/barrier)/sigma + (1+mu)*sigma
	growth := math.Exp(-dividend * yearFrac)
	return phi * (spot*growth*normCDF(phi*x2) - strike*df*normCDF(phi*(x2-sigma)))
}

func barrierC(spot, strike, barrier, yearFrac, df, dividend, sigma, mu, eta, phi float64) float64 {
	y1 := math.Log(barrier*barrier/(spot*strike))/sigma + (1+mu)*sigma
	return phi * reflectedTerms(spot, strike, barrier, yearFrac, df, dividend, sigma, mu, eta, y1)
}

func barrierD(spot, strike, barrier, yearFrac, df, dividend, sigma, mu, eta, phi float64) float64 {
	y2 := math.Log(barrier/spot)/sigma + (1+mu)*sigma
	return phi * reflectedTerms(spot, strike, barrier, yearFrac, df, dividend, sigma, mu, eta, y2)
}

func reflectedTerms(spot, strike, barrier, yearFrac, df, dividend, sigma, mu, eta, y float64) float64 {
	logHS := math.Log(barrier / spot)
	logPow0 := 2 * mu * logHS
	logPow1 := logPow0 + 2*logHS
	growth := math.Exp(-dividend * yearFrac)
	asset := spot * growth * powTimesCDF(logPow1, eta*y)
	cash := strike * df * powTimesCDF(logPow0, eta*(y-sigma))
	return asset - cash
}

// powTimesCDF returns exp(logPower) * N(y), formed in log space when either
// factor underflows or overflows.
func powTimesCDF(logPower, y float64) float64 {
	n := normCDF(y)
	if n > 0 && logPower < 700 && logPower > -700 {
		return math.Exp(logPower) * n
	}
	if y >= -8 && n > 0 {
		return math.Exp(logPower) * n
	}
	return math.Exp(logPower + logNormCDF(y))
}

func logNormCDF(y float64) float64 {
	if y >= 8 {
		return 0
	}
	n := normCDF(y)
	if n > 0 && y > -20 {
		return math.Log(n)
	}
	// Abramowitz and Stegun 26.2.12 for the left tail.
	ysqr := y * y
	sum := 1.0
	g := 1.0
	for i := 1.0; i < 40; i++ {
		x := (4*i - 3) / ysqr
		z := x * ((4*i - 1) / ysqr)
		a := g * (x - z)
		sum -= a
		g *= z
		if math.Abs(a) <= 1e-15*math.Abs(sum) {
			break
		}
	}
	if sum <= 0 {
		sum = 1e-300
	}
	return -0.5*ysqr - 0.5*math.Log(2*math.Pi) - math.Log(-y) + math.Log(sum)
}

func zeroVolDownIn(spot, strike, barrier, yearFrac, df, forward, rate, dividend, growth, quantity float64) Quote {
	hit := spot <= barrier || forward <= barrier
	if !hit {
		return Quote{
			Forward:        forward,
			DiscountFactor: df,
			YearFraction:   yearFrac,
			Rate:           rate,
		}
	}
	return zeroVolPut(spot, strike, yearFrac, df, forward, rate, dividend, growth, quantity, math.Max(strike-spot, 0)*quantity)
}

func downInPutGreeks(spot, strike, barrier, yearFrac, df, vol, dividend, rate, unit float64) (delta, gamma, vega, theta, rho float64) {
	hS := math.Max(spot*1e-4, 1e-6)
	if spot-hS <= barrier {
		up := downInPutValue(spot+hS, strike, barrier, yearFrac, df, vol, dividend)
		up2 := downInPutValue(spot+2*hS, strike, barrier, yearFrac, df, vol, dividend)
		delta = (up - unit) / hS
		gamma = (up2 - 2*up + unit) / (hS * hS)
	} else {
		up := downInPutValue(spot+hS, strike, barrier, yearFrac, df, vol, dividend)
		dn := downInPutValue(spot-hS, strike, barrier, yearFrac, df, vol, dividend)
		delta = (up - dn) / (2 * hS)
		gamma = (up - 2*unit + dn) / (hS * hS)
	}

	hV := 1e-4
	if vol <= hV {
		if vol > 0 {
			up := downInPutValue(spot, strike, barrier, yearFrac, df, vol+hV, dividend)
			vega = (up - unit) / hV
		}
	} else {
		up := downInPutValue(spot, strike, barrier, yearFrac, df, vol+hV, dividend)
		dn := downInPutValue(spot, strike, barrier, yearFrac, df, vol-hV, dividend)
		vega = (up - dn) / (2 * hV)
	}

	hT := math.Max(yearFrac*1e-4, 1e-6)
	upT := downInPutValue(spot, strike, barrier, yearFrac+hT, math.Exp(-rate*(yearFrac+hT)), vol, dividend)
	if yearFrac > 2*hT {
		dnT := downInPutValue(spot, strike, barrier, yearFrac-hT, math.Exp(-rate*(yearFrac-hT)), vol, dividend)
		theta = -(upT - dnT) / (2 * hT)
	} else {
		theta = -(upT - unit) / hT
	}

	hR := 1e-5
	upR := downInPutValue(spot, strike, barrier, yearFrac, math.Exp(-(rate+hR)*yearFrac), vol, dividend)
	dnR := downInPutValue(spot, strike, barrier, yearFrac, math.Exp(-(rate-hR)*yearFrac), vol, dividend)
	rho = (upR - dnR) / (2 * hR)
	return delta, gamma, vega, theta, rho
}
