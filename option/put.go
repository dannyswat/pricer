// Package option prices European vanilla, barrier, and cash-or-nothing digital options.
//
// Calls and puts are priced with the Black-Scholes-Merton formula. The strike
// is discounted on the risk-free curve, so the premium is the discounted
// risk-neutral expected payoff. There is no early-exercise premium. Volatility
// and the dividend yield are constant, and the dividend yield is continuous.
package option

import (
	"errors"
	"fmt"
	"math"
	"time"

	"pricer/daycount"
	"pricer/discount"
	"pricer/pricing"
)

var (
	ErrInvalidSpot     = errors.New("spot must be positive")
	ErrInvalidStrike   = errors.New("strike must be positive")
	ErrInvalidVol      = errors.New("volatility must be non-negative")
	ErrInvalidQuantity = errors.New("quantity must be positive")
	ErrInvalidCash     = errors.New("cash amount must be positive")
	ErrInvalidBarrier  = errors.New("barrier must be positive")
	ErrExpired         = errors.New("valuation must be on or before expiry")
)

// contract is the shared description of a European vanilla option.
// Volatility and DividendYield are annual decimals. DividendYield is a
// continuous yield (zero for a non-dividend underlier). Quantity scales the
// premium and the Greeks. DayCount measures variance time and the dividend
// year fraction; the zero value is Actual/Actual ISDA.
type contract struct {
	Spot          float64
	Strike        float64
	Expiry        time.Time
	Volatility    float64
	DividendYield float64
	Quantity      float64
	DayCount      daycount.Convention
}

// EuropeanPut is a vanilla put exercisable only at Expiry.
type EuropeanPut struct {
	Spot          float64
	Strike        float64
	Expiry        time.Time
	Volatility    float64
	DividendYield float64
	Quantity      float64
	DayCount      daycount.Convention
}

func (p EuropeanPut) terms() contract {
	return contract{p.Spot, p.Strike, p.Expiry, p.Volatility, p.DividendYield, p.Quantity, p.DayCount}
}

// Quote is the theoretical price of a European vanilla option and its Black-Scholes Greeks.
// Greeks are for the position (already scaled by Quantity). Theta is per year
// of calendar time, ∂V/∂t. Vega is ∂V/∂σ and Rho is ∂V/∂r, where r is the
// continuous zero implied by the discount factor over the variance year fraction.
type Quote struct {
	Premium        float64
	Forward        float64
	DiscountFactor float64
	YearFraction   float64
	Rate           float64
	D1             float64
	D2             float64
	Delta          float64
	Gamma          float64
	Vega           float64
	Theta          float64
	Rho            float64
	ExerciseValue  float64
	TimeValue      float64
}

// TheoreticalPrice reports the put premium in Result.Dirty. Clean equals Dirty
// because an option has no accrued coupon.
func (p EuropeanPut) TheoreticalPrice(asOf time.Time, curve discount.Curve) (pricing.Result, error) {
	q, err := p.Price(asOf, curve)
	if err != nil {
		return pricing.Result{}, err
	}
	return pricing.Result{Dirty: q.Premium, Clean: q.Premium}, nil
}

// Price returns the Black-Scholes-Merton premium of the put.
func (p EuropeanPut) Price(asOf time.Time, curve discount.Curve) (Quote, error) {
	yearFrac, df, err := p.terms().resolve(asOf, curve)
	if err != nil {
		return Quote{}, err
	}
	return pricePut(p.Spot, p.Strike, yearFrac, df, p.Volatility, p.DividendYield, p.Quantity)
}

func (c contract) resolve(asOf time.Time, curve discount.Curve) (yearFrac, df float64, err error) {
	if err = c.validate(); err != nil {
		return 0, 0, err
	}
	asOf = daycount.Normalize(asOf)
	expiry := daycount.Normalize(c.Expiry)
	if asOf.IsZero() || expiry.IsZero() {
		return 0, 0, fmt.Errorf("valuation and expiry dates are required")
	}
	if expiry.Before(asOf) {
		return 0, 0, ErrExpired
	}
	yearFrac, err = c.DayCount.YearFraction(asOf, expiry)
	if err != nil {
		return 0, 0, err
	}
	df = 1
	if expiry.After(asOf) {
		if curve == nil {
			return 0, 0, fmt.Errorf("discount curve is required")
		}
		df, err = curve.DiscountFactor(asOf, expiry)
		if err != nil {
			return 0, 0, err
		}
	}
	return yearFrac, df, nil
}

func (c contract) validate() error {
	if !(c.Spot > 0) || math.IsNaN(c.Spot) || math.IsInf(c.Spot, 0) {
		return ErrInvalidSpot
	}
	if !(c.Strike > 0) || math.IsNaN(c.Strike) || math.IsInf(c.Strike, 0) {
		return ErrInvalidStrike
	}
	if c.Volatility < 0 || math.IsNaN(c.Volatility) || math.IsInf(c.Volatility, 0) {
		return ErrInvalidVol
	}
	if !(c.Quantity > 0) || math.IsNaN(c.Quantity) || math.IsInf(c.Quantity, 0) {
		return ErrInvalidQuantity
	}
	if math.IsNaN(c.DividendYield) || math.IsInf(c.DividendYield, 0) {
		return fmt.Errorf("dividend yield must be finite")
	}
	return nil
}

func pricePut(spot, strike, yearFrac, df, vol, dividend, quantity float64) (Quote, error) {
	if math.IsNaN(df) || math.IsInf(df, 0) || df <= 0 {
		return Quote{}, fmt.Errorf("discount factor must be positive")
	}
	if yearFrac < 0 || math.IsNaN(yearFrac) || math.IsInf(yearFrac, 0) {
		return Quote{}, fmt.Errorf("year fraction must be non-negative")
	}
	exercise := math.Max(strike-spot, 0) * quantity
	if yearFrac == 0 {
		delta := 0.0
		if spot < strike {
			delta = -quantity
		}
		return Quote{
			Premium:        exercise,
			Forward:        spot,
			DiscountFactor: df,
			Delta:          delta,
			ExerciseValue:  exercise,
		}, nil
	}
	growth := math.Exp(-dividend * yearFrac)
	forward := spot * growth / df
	rate := -math.Log(df) / yearFrac
	volSqrtT := vol * math.Sqrt(yearFrac)
	if volSqrtT < 1e-14 {
		return zeroVolPut(spot, strike, yearFrac, df, forward, rate, dividend, growth, quantity, exercise), nil
	}
	d1 := (math.Log(forward/strike) + 0.5*vol*vol*yearFrac) / volSqrtT
	d2 := d1 - volSqrtT
	nd1 := normCDF(-d1)
	nd2 := normCDF(-d2)
	unit := df * (strike*nd2 - forward*nd1)
	pdf := normPDF(d1)
	delta := -growth * nd1
	gamma := growth * pdf / (spot * volSqrtT)
	vega := spot * growth * pdf * math.Sqrt(yearFrac)
	theta := -spot*growth*pdf*vol/(2*math.Sqrt(yearFrac)) + dividend*spot*growth*nd1 - rate*strike*df*nd2
	rho := -strike * yearFrac * df * nd2
	premium := unit * quantity
	return Quote{
		Premium:        premium,
		Forward:        forward,
		DiscountFactor: df,
		YearFraction:   yearFrac,
		Rate:           rate,
		D1:             d1,
		D2:             d2,
		Delta:          delta * quantity,
		Gamma:          gamma * quantity,
		Vega:           vega * quantity,
		Theta:          theta * quantity,
		Rho:            rho * quantity,
		ExerciseValue:  exercise,
		TimeValue:      premium - exercise,
	}, nil
}

func zeroVolPut(spot, strike, yearFrac, df, forward, rate, dividend, growth, quantity, exercise float64) Quote {
	var unit, delta, theta, rho float64
	if forward < strike {
		unit = df * (strike - forward)
		delta = -growth
		theta = rate*strike*df - dividend*spot*growth
		rho = -strike * yearFrac * df
	}
	premium := unit * quantity
	return Quote{
		Premium:        premium,
		Forward:        forward,
		DiscountFactor: df,
		YearFraction:   yearFrac,
		Rate:           rate,
		Delta:          delta * quantity,
		Theta:          theta * quantity,
		Rho:            rho * quantity,
		ExerciseValue:  exercise,
		TimeValue:      premium - exercise,
	}
}

func normCDF(x float64) float64 {
	return 0.5 * math.Erfc(-x/math.Sqrt2)
}

func normPDF(x float64) float64 {
	return math.Exp(-0.5*x*x) / math.Sqrt(2*math.Pi)
}
