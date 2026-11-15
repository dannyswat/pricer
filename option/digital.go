package option

import (
	"fmt"
	"math"
	"time"

	"pricer/daycount"
	"pricer/discount"
	"pricer/pricing"
)

// DigitalCall is a European cash-or-nothing call. It pays Cash at Expiry if
// the spot finishes strictly above Strike, and nothing otherwise.
type DigitalCall struct {
	Spot          float64
	Strike        float64
	Expiry        time.Time
	Volatility    float64
	DividendYield float64
	Cash          float64
	Quantity      float64
	DayCount      daycount.Convention
}

func (c DigitalCall) terms() contract {
	return contract{c.Spot, c.Strike, c.Expiry, c.Volatility, c.DividendYield, c.Quantity, c.DayCount}
}

// TheoreticalPrice reports the digital premium in Result.Dirty.
func (c DigitalCall) TheoreticalPrice(asOf time.Time, curve discount.Curve) (pricing.Result, error) {
	q, err := c.Price(asOf, curve)
	if err != nil {
		return pricing.Result{}, err
	}
	return pricing.Result{Dirty: q.Premium, Clean: q.Premium}, nil
}

// Price returns the Black-Scholes-Merton premium of the cash-or-nothing call.
func (c DigitalCall) Price(asOf time.Time, curve discount.Curve) (Quote, error) {
	yearFrac, df, err := c.terms().resolve(asOf, curve)
	if err != nil {
		return Quote{}, err
	}
	return priceDigital(true, c.Cash, c.Spot, c.Strike, yearFrac, df, c.Volatility, c.DividendYield, c.Quantity)
}

// DigitalPut is a European cash-or-nothing put. It pays Cash at Expiry if
// the spot finishes strictly below Strike, and nothing otherwise.
type DigitalPut struct {
	Spot          float64
	Strike        float64
	Expiry        time.Time
	Volatility    float64
	DividendYield float64
	Cash          float64
	Quantity      float64
	DayCount      daycount.Convention
}

func (p DigitalPut) terms() contract {
	return contract{p.Spot, p.Strike, p.Expiry, p.Volatility, p.DividendYield, p.Quantity, p.DayCount}
}

// TheoreticalPrice reports the digital premium in Result.Dirty.
func (p DigitalPut) TheoreticalPrice(asOf time.Time, curve discount.Curve) (pricing.Result, error) {
	q, err := p.Price(asOf, curve)
	if err != nil {
		return pricing.Result{}, err
	}
	return pricing.Result{Dirty: q.Premium, Clean: q.Premium}, nil
}

// Price returns the Black-Scholes-Merton premium of the cash-or-nothing put.
func (p DigitalPut) Price(asOf time.Time, curve discount.Curve) (Quote, error) {
	yearFrac, df, err := p.terms().resolve(asOf, curve)
	if err != nil {
		return Quote{}, err
	}
	return priceDigital(false, p.Cash, p.Spot, p.Strike, yearFrac, df, p.Volatility, p.DividendYield, p.Quantity)
}

func priceDigital(isCall bool, cash, spot, strike, yearFrac, df, vol, dividend, quantity float64) (Quote, error) {
	if !(cash > 0) || math.IsNaN(cash) || math.IsInf(cash, 0) {
		return Quote{}, ErrInvalidCash
	}
	if math.IsNaN(df) || math.IsInf(df, 0) || df <= 0 {
		return Quote{}, fmt.Errorf("discount factor must be positive")
	}
	if yearFrac < 0 || math.IsNaN(yearFrac) || math.IsInf(yearFrac, 0) {
		return Quote{}, fmt.Errorf("year fraction must be non-negative")
	}
	inTheMoney := spot > strike
	if !isCall {
		inTheMoney = spot < strike
	}
	exercise := 0.0
	if inTheMoney {
		exercise = cash * quantity
	}
	if yearFrac == 0 {
		return Quote{
			Premium:        exercise,
			Forward:        spot,
			DiscountFactor: df,
			ExerciseValue:  exercise,
		}, nil
	}
	growth := math.Exp(-dividend * yearFrac)
	forward := spot * growth / df
	rate := -math.Log(df) / yearFrac
	volSqrtT := vol * math.Sqrt(yearFrac)
	if volSqrtT < 1e-14 {
		return zeroVolDigital(isCall, cash, yearFrac, df, forward, strike, quantity, exercise), nil
	}
	d1 := (math.Log(forward/strike) + 0.5*vol*vol*yearFrac) / volSqrtT
	d2 := d1 - volSqrtT
	sign := 1.0
	if !isCall {
		sign = -1
	}
	prob := normCDF(sign * d2)
	pdf := normPDF(d2)
	unit := df * cash * prob
	// ∂d2/∂T at constant r, q, and σ. Theta is ∂V/∂t = −∂V/∂T.
	d2dT := (rate-dividend)/volSqrtT - d1/(2*yearFrac)
	delta := sign * df * cash * pdf / (spot * volSqrtT)
	gamma := -sign * df * cash * pdf * d1 / (spot * spot * volSqrtT * volSqrtT)
	vega := -sign * df * cash * pdf * d1 / vol
	theta := df * cash * (rate*prob - sign*pdf*d2dT)
	rho := df * cash * (-yearFrac*prob + sign*pdf*math.Sqrt(yearFrac)/vol)
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

func zeroVolDigital(isCall bool, cash, yearFrac, df, forward, strike, quantity, exercise float64) Quote {
	pays := forward > strike
	if !isCall {
		pays = forward < strike
	}
	premium := 0.0
	if pays {
		premium = df * cash * quantity
	}
	return Quote{
		Premium:        premium,
		Forward:        forward,
		DiscountFactor: df,
		YearFraction:   yearFrac,
		Rate:           -math.Log(df) / yearFrac,
		ExerciseValue:  exercise,
		TimeValue:      premium - exercise,
	}
}
