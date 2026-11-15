package option

import (
	"fmt"
	"math"
	"time"

	"pricer/daycount"
	"pricer/discount"
	"pricer/pricing"
)

// EuropeanCall is a vanilla call exercisable only at Expiry.
type EuropeanCall struct {
	Spot          float64
	Strike        float64
	Expiry        time.Time
	Volatility    float64
	DividendYield float64
	Quantity      float64
	DayCount      daycount.Convention
}

func (c EuropeanCall) terms() contract {
	return contract{c.Spot, c.Strike, c.Expiry, c.Volatility, c.DividendYield, c.Quantity, c.DayCount}
}

// TheoreticalPrice reports the call premium in Result.Dirty. Clean equals Dirty
// because an option has no accrued coupon.
func (c EuropeanCall) TheoreticalPrice(asOf time.Time, curve discount.Curve) (pricing.Result, error) {
	q, err := c.Price(asOf, curve)
	if err != nil {
		return pricing.Result{}, err
	}
	return pricing.Result{Dirty: q.Premium, Clean: q.Premium}, nil
}

// Price returns the Black-Scholes-Merton premium of the call.
func (c EuropeanCall) Price(asOf time.Time, curve discount.Curve) (Quote, error) {
	yearFrac, df, err := c.terms().resolve(asOf, curve)
	if err != nil {
		return Quote{}, err
	}
	return priceCall(c.Spot, c.Strike, yearFrac, df, c.Volatility, c.DividendYield, c.Quantity)
}

func priceCall(spot, strike, yearFrac, df, vol, dividend, quantity float64) (Quote, error) {
	if math.IsNaN(df) || math.IsInf(df, 0) || df <= 0 {
		return Quote{}, fmt.Errorf("discount factor must be positive")
	}
	if yearFrac < 0 || math.IsNaN(yearFrac) || math.IsInf(yearFrac, 0) {
		return Quote{}, fmt.Errorf("year fraction must be non-negative")
	}
	exercise := math.Max(spot-strike, 0) * quantity
	if yearFrac == 0 {
		delta := 0.0
		if spot > strike {
			delta = quantity
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
		return zeroVolCall(spot, strike, yearFrac, df, forward, rate, dividend, growth, quantity, exercise), nil
	}
	d1 := (math.Log(forward/strike) + 0.5*vol*vol*yearFrac) / volSqrtT
	d2 := d1 - volSqrtT
	n1 := normCDF(d1)
	n2 := normCDF(d2)
	unit := df * (forward*n1 - strike*n2)
	pdf := normPDF(d1)
	delta := growth * n1
	gamma := growth * pdf / (spot * volSqrtT)
	vega := spot * growth * pdf * math.Sqrt(yearFrac)
	theta := -spot*growth*pdf*vol/(2*math.Sqrt(yearFrac)) + dividend*spot*growth*n1 - rate*strike*df*n2
	rho := strike * yearFrac * df * n2
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

func zeroVolCall(spot, strike, yearFrac, df, forward, rate, dividend, growth, quantity, exercise float64) Quote {
	var unit, delta, theta, rho float64
	if forward > strike {
		unit = df * (forward - strike)
		delta = growth
		theta = dividend*spot*growth - rate*strike*df
		rho = strike * yearFrac * df
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
