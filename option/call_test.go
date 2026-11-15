package option

import (
	"errors"
	"math"
	"testing"

	"pricer/daycount"
	"pricer/discount"
	"pricer/pricing"
)

func TestHullEuropeanCall(t *testing.T) {
	// Hull, European call: S=42, K=40, r=10%, σ=20%, T=0.5, no dividend.
	const (
		spot   = 42.0
		k      = 40.0
		rate   = 0.1
		vol    = 0.2
		expiry = 0.5
	)
	df := math.Exp(-rate * expiry)
	got, err := priceCall(spot, k, expiry, df, vol, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	const want = 4.759422392871528
	if math.Abs(got.Premium-want) > 1e-12 {
		t.Fatalf("premium = %.12f, want %.12f", got.Premium, want)
	}
	put, err := pricePut(spot, k, expiry, df, vol, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	parity := spot - k*df
	if math.Abs(got.Premium-put.Premium-parity) > 1e-12 {
		t.Fatalf("call %.12f - put %.12f != %.12f", got.Premium, put.Premium, parity)
	}
}

func TestPutCallParityOnCurve(t *testing.T) {
	asOf := date(2026, 11, 16)
	expiry := date(2027, 11, 16)
	callOpt := EuropeanCall{
		Spot: 100, Strike: 95, Expiry: expiry,
		Volatility: 0.22, DividendYield: 0.01, Quantity: 2,
		DayCount: daycount.Actual365Fixed,
	}
	putOpt := EuropeanPut{
		Spot: callOpt.Spot, Strike: callOpt.Strike, Expiry: expiry,
		Volatility: callOpt.Volatility, DividendYield: callOpt.DividendYield,
		Quantity: callOpt.Quantity, DayCount: callOpt.DayCount,
	}
	curve := discount.Flat{Rate: 0.04, Frequency: 2, DayCount: daycount.Actual365Fixed}
	call, err := callOpt.Price(asOf, curve)
	if err != nil {
		t.Fatal(err)
	}
	put, err := putOpt.Price(asOf, curve)
	if err != nil {
		t.Fatal(err)
	}
	df, err := curve.DiscountFactor(asOf, expiry)
	if err != nil {
		t.Fatal(err)
	}
	growth := math.Exp(-callOpt.DividendYield * 1)
	want := callOpt.Quantity * (callOpt.Spot*growth - callOpt.Strike*df)
	if math.Abs(call.Premium-put.Premium-want) > 1e-9 {
		t.Fatalf("parity residual %.12f", call.Premium-put.Premium-want)
	}
	var inst pricing.Instrument = callOpt
	res, err := inst.TheoreticalPrice(asOf, curve)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(res.Dirty-call.Premium) > 1e-12 {
		t.Fatalf("instrument premium = %.12f", res.Dirty)
	}
}

func TestZeroVolatilityCall(t *testing.T) {
	df := math.Exp(-0.04)
	growth := math.Exp(-0.02)
	forward := 100 * growth / df
	got, err := priceCall(100, 90, 1, df, 0, 0.02, 2)
	if err != nil {
		t.Fatal(err)
	}
	wantUnit := df * (forward - 90)
	if math.Abs(got.Premium-2*wantUnit) > 1e-10 {
		t.Fatalf("itm premium = %.12f, want %.12f", got.Premium, 2*wantUnit)
	}
	if math.Abs(got.Delta-2*growth) > 1e-12 {
		t.Fatalf("itm delta = %.12f", got.Delta)
	}
	otm, err := priceCall(100, 120, 1, df, 0, 0.02, 1)
	if err != nil {
		t.Fatal(err)
	}
	if otm.Premium != 0 || otm.Delta != 0 {
		t.Fatalf("otm zero-vol call = %+v", otm)
	}
}

func TestCallExpiryIsExerciseValue(t *testing.T) {
	when := date(2027, 11, 16)
	call := EuropeanCall{
		Spot: 110, Strike: 100, Expiry: when,
		Volatility: 0.2, Quantity: 3,
		DayCount: daycount.Actual365Fixed,
	}
	got, err := call.Price(when, nil)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(got.Premium-30) > 1e-12 || math.Abs(got.Delta-3) > 1e-12 {
		t.Fatalf("expiry quote = %+v", got)
	}
	otm := call
	otm.Spot = 90
	got, err = otm.Price(when, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Premium != 0 || got.Delta != 0 {
		t.Fatalf("otm expiry quote = %+v", got)
	}
}

func TestCallGreeksMatchBump(t *testing.T) {
	asOf := date(2026, 11, 16)
	call := EuropeanCall{
		Spot: 100, Strike: 105, Expiry: date(2027, 11, 16),
		Volatility: 0.25, DividendYield: 0.01, Quantity: 2,
		DayCount: daycount.Actual365Fixed,
	}
	curve := discount.Flat{Rate: 0.04, Frequency: 0, DayCount: daycount.Actual365Fixed}
	got, err := call.Price(asOf, curve)
	if err != nil {
		t.Fatal(err)
	}
	const ds = 1e-4
	up, dn := call, call
	up.Spot += ds
	dn.Spot -= ds
	cu, err := up.Price(asOf, curve)
	if err != nil {
		t.Fatal(err)
	}
	cd, err := dn.Price(asOf, curve)
	if err != nil {
		t.Fatal(err)
	}
	delta := (cu.Premium - cd.Premium) / (2 * ds)
	gamma := (cu.Premium - 2*got.Premium + cd.Premium) / (ds * ds)
	if math.Abs(delta-got.Delta) > 1e-6 {
		t.Fatalf("delta = %.8f, bump = %.8f", got.Delta, delta)
	}
	if math.Abs(gamma-got.Gamma) > 1e-5 {
		t.Fatalf("gamma = %.8f, bump = %.8f", got.Gamma, gamma)
	}
}

func TestCallRejectsBadInputs(t *testing.T) {
	call := EuropeanCall{
		Spot: 100, Strike: 100, Expiry: date(2027, 11, 16),
		Volatility: 0.2, Quantity: 1, DayCount: daycount.Actual365Fixed,
	}
	_, err := call.Price(date(2028, 11, 16), discount.Flat{Rate: 0.01})
	if !errors.Is(err, ErrExpired) {
		t.Fatalf("err = %v, want expired", err)
	}
}
