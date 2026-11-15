package option

import (
	"errors"
	"math"
	"testing"

	"pricer/daycount"
	"pricer/discount"
	"pricer/pricing"
)

func TestDigitalCallIsHalfWhenD2IsZero(t *testing.T) {
	const (
		rate  = 0.1
		vol   = 0.2
		tenor = 0.5
		cash  = 10.0
	)
	df := math.Exp(-rate * tenor)
	// F = K exp(½σ²T) makes d2 zero, so the cash call is half the discounted cash.
	forward := 100 * math.Exp(0.5*vol*vol*tenor)
	spot := forward * df
	got, err := priceDigital(true, cash, spot, 100, tenor, df, vol, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	want := 0.5 * df * cash
	if math.Abs(got.D2) > 1e-12 || math.Abs(got.Premium-want) > 1e-12 {
		t.Fatalf("d2 = %.12f, premium = %.12f, want %.12f", got.D2, got.Premium, want)
	}
}

func TestVanillaCallIsAssetMinusStrikeTimesDigital(t *testing.T) {
	const (
		spot  = 100.0
		k     = 95.0
		vol   = 0.22
		q     = 0.01
		tenor = 0.8
		rate  = 0.04
		qty   = 3.0
	)
	df := math.Exp(-rate * tenor)
	call, err := priceCall(spot, k, tenor, df, vol, q, qty)
	if err != nil {
		t.Fatal(err)
	}
	digital, err := priceDigital(true, 1, spot, k, tenor, df, vol, q, qty)
	if err != nil {
		t.Fatal(err)
	}
	asset := qty * spot * math.Exp(-q*tenor) * normCDF(call.D1)
	want := asset - k*digital.Premium
	if math.Abs(call.Premium-want) > 1e-10 {
		t.Fatalf("call %.12f != asset-or-nothing minus strike digital %.12f", call.Premium, want)
	}
}

func TestDigitalCallPutSumToDiscountedCash(t *testing.T) {
	asOf := date(2026, 11, 16)
	expiry := date(2027, 2, 16)
	curve := discount.Flat{Rate: 0.04, Frequency: 2, DayCount: daycount.Actual365Fixed}
	call := DigitalCall{
		Spot: 100, Strike: 95, Expiry: expiry, Volatility: 0.2,
		DividendYield: 0.015, Cash: 25, Quantity: 4, DayCount: daycount.Actual365Fixed,
	}
	put := DigitalPut{
		Spot: call.Spot, Strike: call.Strike, Expiry: expiry, Volatility: call.Volatility,
		DividendYield: call.DividendYield, Cash: call.Cash, Quantity: call.Quantity,
		DayCount: call.DayCount,
	}
	gotCall, err := call.Price(asOf, curve)
	if err != nil {
		t.Fatal(err)
	}
	gotPut, err := put.Price(asOf, curve)
	if err != nil {
		t.Fatal(err)
	}
	df, err := curve.DiscountFactor(asOf, expiry)
	if err != nil {
		t.Fatal(err)
	}
	want := call.Cash * call.Quantity * df
	if math.Abs(gotCall.Premium+gotPut.Premium-want) > 1e-9 {
		t.Fatalf("call + put = %.12f, want %.12f", gotCall.Premium+gotPut.Premium, want)
	}
	var inst pricing.Instrument = call
	res, err := inst.TheoreticalPrice(asOf, curve)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(res.Dirty-gotCall.Premium) > 1e-12 {
		t.Fatalf("instrument premium = %.12f", res.Dirty)
	}
}

func TestDigitalCallSpreadApproachesCashCall(t *testing.T) {
	const (
		spot  = 100.0
		k     = 90.0
		vol   = 0.2
		tenor = 1.0
		rate  = 0.03
		eps   = 1e-3
	)
	df := math.Exp(-rate * tenor)
	low, err := priceCall(spot, k, tenor, df, vol, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	high, err := priceCall(spot, k+eps, tenor, df, vol, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	digital, err := priceDigital(true, 1, spot, k, tenor, df, vol, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	spread := (low.Premium - high.Premium) / eps
	if math.Abs(spread-digital.Premium) > 5e-4 {
		t.Fatalf("call spread %.8f, digital %.8f", spread, digital.Premium)
	}
}

func TestDigitalGreeksMatchBump(t *testing.T) {
	const (
		spot  = 100.0
		k     = 95.0
		vol   = 0.25
		q     = 0.01
		tenor = 0.75
		rate  = 0.04
		cash  = 8.0
		qty   = 2.0
	)
	df := math.Exp(-rate * tenor)
	got, err := priceDigital(true, cash, spot, k, tenor, df, vol, q, qty)
	if err != nil {
		t.Fatal(err)
	}
	const ds = 1e-4
	up, err := priceDigital(true, cash, spot+ds, k, tenor, df, vol, q, qty)
	if err != nil {
		t.Fatal(err)
	}
	dn, err := priceDigital(true, cash, spot-ds, k, tenor, df, vol, q, qty)
	if err != nil {
		t.Fatal(err)
	}
	delta := (up.Premium - dn.Premium) / (2 * ds)
	gamma := (up.Premium - 2*got.Premium + dn.Premium) / (ds * ds)
	if math.Abs(delta-got.Delta) > 1e-6 {
		t.Fatalf("delta = %.8f, bump = %.8f", got.Delta, delta)
	}
	if math.Abs(gamma-got.Gamma) > 1e-4 {
		t.Fatalf("gamma = %.8f, bump = %.8f", got.Gamma, gamma)
	}
	const dv = 1e-5
	volUp, err := priceDigital(true, cash, spot, k, tenor, df, vol+dv, q, qty)
	if err != nil {
		t.Fatal(err)
	}
	volDn, err := priceDigital(true, cash, spot, k, tenor, df, vol-dv, q, qty)
	if err != nil {
		t.Fatal(err)
	}
	vega := (volUp.Premium - volDn.Premium) / (2 * dv)
	if math.Abs(vega-got.Vega) > 1e-4 {
		t.Fatalf("vega = %.8f, bump = %.8f", got.Vega, vega)
	}
	const dt = 1e-5
	later, err := priceDigital(true, cash, spot, k, tenor+dt, math.Exp(-rate*(tenor+dt)), vol, q, qty)
	if err != nil {
		t.Fatal(err)
	}
	earlier, err := priceDigital(true, cash, spot, k, tenor-dt, math.Exp(-rate*(tenor-dt)), vol, q, qty)
	if err != nil {
		t.Fatal(err)
	}
	theta := -(later.Premium - earlier.Premium) / (2 * dt)
	if math.Abs(theta-got.Theta) > 1e-4 {
		t.Fatalf("theta = %.8f, bump = %.8f", got.Theta, theta)
	}
	const dr = 1e-5
	rateUp, err := priceDigital(true, cash, spot, k, tenor, math.Exp(-(rate+dr)*tenor), vol, q, qty)
	if err != nil {
		t.Fatal(err)
	}
	rateDn, err := priceDigital(true, cash, spot, k, tenor, math.Exp(-(rate-dr)*tenor), vol, q, qty)
	if err != nil {
		t.Fatal(err)
	}
	rho := (rateUp.Premium - rateDn.Premium) / (2 * dr)
	if math.Abs(rho-got.Rho) > 1e-4 {
		t.Fatalf("rho = %.8f, bump = %.8f", got.Rho, rho)
	}

	put, err := priceDigital(false, cash, spot, k, tenor, df, vol, q, qty)
	if err != nil {
		t.Fatal(err)
	}
	putUp, err := priceDigital(false, cash, spot+ds, k, tenor, df, vol, q, qty)
	if err != nil {
		t.Fatal(err)
	}
	putDn, err := priceDigital(false, cash, spot-ds, k, tenor, df, vol, q, qty)
	if err != nil {
		t.Fatal(err)
	}
	putDelta := (putUp.Premium - putDn.Premium) / (2 * ds)
	if math.Abs(putDelta-put.Delta) > 1e-6 {
		t.Fatalf("put delta = %.8f, bump = %.8f", put.Delta, putDelta)
	}
}

func TestDigitalExpiryAndZeroVol(t *testing.T) {
	when := date(2027, 11, 16)
	call := DigitalCall{
		Spot: 101, Strike: 100, Expiry: when, Volatility: 0.2,
		Cash: 5, Quantity: 2, DayCount: daycount.Actual365Fixed,
	}
	got, err := call.Price(when, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Premium != 10 || got.Delta != 0 {
		t.Fatalf("expiry call = %+v", got)
	}
	atStrike := call
	atStrike.Spot = 100
	got, err = atStrike.Price(when, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Premium != 0 {
		t.Fatalf("at-strike expiry pays nothing, got %.6f", got.Premium)
	}
	put := DigitalPut{
		Spot: 99, Strike: 100, Expiry: when, Volatility: 0.2,
		Cash: 5, Quantity: 2, DayCount: daycount.Actual365Fixed,
	}
	got, err = put.Price(when, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Premium != 10 {
		t.Fatalf("expiry put = %+v", got)
	}

	zero, err := priceDigital(true, 5, 100, 90, 1, math.Exp(-0.04), 0, 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(zero.Premium-10*math.Exp(-0.04)) > 1e-12 || zero.Delta != 0 {
		t.Fatalf("zero-vol digital = %+v", zero)
	}
	otm, err := priceDigital(true, 5, 80, 90, 1, math.Exp(-0.04), 0, 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if otm.Premium != 0 {
		t.Fatalf("zero-vol otm premium = %.6f", otm.Premium)
	}
}

func TestRejectsBadDigital(t *testing.T) {
	_, err := priceDigital(true, 0, 100, 100, 1, 0.9, 0.2, 0, 1)
	if !errors.Is(err, ErrInvalidCash) {
		t.Fatalf("err = %v", err)
	}
	opt := DigitalCall{
		Spot: 100, Strike: 100, Expiry: date(2027, 11, 16),
		Volatility: 0.2, Cash: 1, Quantity: 1,
	}
	_, err = opt.Price(date(2027, 11, 17), discount.Flat{Rate: 0.01})
	if !errors.Is(err, ErrExpired) {
		t.Fatalf("err = %v", err)
	}
}
