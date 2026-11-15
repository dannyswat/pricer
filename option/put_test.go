package option

import (
	"errors"
	"math"
	"testing"
	"time"

	"pricer/daycount"
	"pricer/discount"
	"pricer/pricing"
)

func date(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func TestHullEuropeanPut(t *testing.T) {
	// Hull, European put: S=42, K=40, r=10%, σ=20%, T=0.5, no dividend.
	const (
		spot   = 42.0
		k      = 40.0
		rate   = 0.1
		vol    = 0.2
		expiry = 0.5
	)
	df := math.Exp(-rate * expiry)
	got, err := pricePut(spot, k, expiry, df, vol, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	const want = 0.8085993729000926
	if math.Abs(got.Premium-want) > 1e-12 {
		t.Fatalf("premium = %.12f, want %.12f", got.Premium, want)
	}
	if math.Abs(got.Delta-(-0.2208687090573309)) > 1e-12 {
		t.Fatalf("delta = %.12f", got.Delta)
	}
}

func TestPutUsesDiscountCurve(t *testing.T) {
	asOf := date(2026, 11, 16)
	expiry := date(2027, 11, 16)
	put := EuropeanPut{
		Spot: 100, Strike: 100, Expiry: expiry,
		Volatility: 0.2, Quantity: 1,
		DayCount: daycount.Actual365Fixed,
	}
	curve := discount.Flat{Rate: 0.04, Frequency: 2, DayCount: daycount.Actual365Fixed}
	got, err := put.Price(asOf, curve)
	if err != nil {
		t.Fatal(err)
	}
	df, err := curve.DiscountFactor(asOf, expiry)
	if err != nil {
		t.Fatal(err)
	}
	want, err := pricePut(100, 100, 1, df, 0.2, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(got.Premium-want.Premium) > 1e-12 {
		t.Fatalf("premium = %.12f, curve formula = %.12f", got.Premium, want.Premium)
	}
	if math.Abs(got.DiscountFactor-df) > 1e-12 {
		t.Fatalf("df = %.12f, want %.12f", got.DiscountFactor, df)
	}
	var inst pricing.Instrument = put
	res, err := inst.TheoreticalPrice(asOf, curve)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(res.Dirty-got.Premium) > 1e-12 {
		t.Fatalf("instrument premium = %.12f", res.Dirty)
	}
}

func TestZeroVolatilityPut(t *testing.T) {
	df := math.Exp(-0.04)
	growth := math.Exp(-0.02)
	forward := 100 * growth / df
	got, err := pricePut(100, 120, 1, df, 0, 0.02, 2)
	if err != nil {
		t.Fatal(err)
	}
	wantUnit := df * (120 - forward)
	if math.Abs(got.Premium-2*wantUnit) > 1e-10 {
		t.Fatalf("itm premium = %.12f, want %.12f", got.Premium, 2*wantUnit)
	}
	if math.Abs(got.Delta-(-2*growth)) > 1e-12 {
		t.Fatalf("itm delta = %.12f", got.Delta)
	}
	otm, err := pricePut(100, 90, 1, df, 0, 0.02, 1)
	if err != nil {
		t.Fatal(err)
	}
	if otm.Premium != 0 || otm.Delta != 0 {
		t.Fatalf("otm zero-vol put = %+v", otm)
	}
}

func TestExpiryIsExerciseValue(t *testing.T) {
	when := date(2027, 11, 16)
	put := EuropeanPut{
		Spot: 90, Strike: 100, Expiry: when,
		Volatility: 0.2, Quantity: 3,
		DayCount: daycount.Actual365Fixed,
	}
	got, err := put.Price(when, nil)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(got.Premium-30) > 1e-12 || math.Abs(got.Delta-(-3)) > 1e-12 {
		t.Fatalf("expiry quote = %+v", got)
	}
	otm := put
	otm.Spot = 110
	got, err = otm.Price(when, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Premium != 0 || got.Delta != 0 {
		t.Fatalf("otm expiry quote = %+v", got)
	}
}

func TestGreeksMatchBump(t *testing.T) {
	asOf := date(2026, 11, 16)
	put := EuropeanPut{
		Spot: 100, Strike: 95, Expiry: date(2027, 11, 16),
		Volatility: 0.25, DividendYield: 0.01, Quantity: 2,
		DayCount: daycount.Actual365Fixed,
	}
	curve := discount.Flat{Rate: 0.04, Frequency: 0, DayCount: daycount.Actual365Fixed}
	got, err := put.Price(asOf, curve)
	if err != nil {
		t.Fatal(err)
	}
	const ds = 1e-4
	up, dn := put, put
	up.Spot += ds
	dn.Spot -= ds
	pu, err := up.Price(asOf, curve)
	if err != nil {
		t.Fatal(err)
	}
	pd, err := dn.Price(asOf, curve)
	if err != nil {
		t.Fatal(err)
	}
	delta := (pu.Premium - pd.Premium) / (2 * ds)
	gamma := (pu.Premium - 2*got.Premium + pd.Premium) / (ds * ds)
	if math.Abs(delta-got.Delta) > 1e-6 {
		t.Fatalf("delta = %.8f, bump = %.8f", got.Delta, delta)
	}
	if math.Abs(gamma-got.Gamma) > 1e-5 {
		t.Fatalf("gamma = %.8f, bump = %.8f", got.Gamma, gamma)
	}
	const dv = 1e-5
	volUp := put
	volUp.Volatility += dv
	pv, err := volUp.Price(asOf, curve)
	if err != nil {
		t.Fatal(err)
	}
	volDn := put
	volDn.Volatility -= dv
	pdn, err := volDn.Price(asOf, curve)
	if err != nil {
		t.Fatal(err)
	}
	vega := (pv.Premium - pdn.Premium) / (2 * dv)
	if math.Abs(vega-got.Vega) > 1e-4 {
		t.Fatalf("vega = %.8f, bump = %.8f", got.Vega, vega)
	}
}

func TestRejectsBadInputs(t *testing.T) {
	put := EuropeanPut{
		Spot: 100, Strike: 100, Expiry: date(2027, 11, 16),
		Volatility: 0.2, Quantity: 1, DayCount: daycount.Actual365Fixed,
	}
	_, err := put.Price(date(2028, 11, 16), discount.Flat{Rate: 0.01})
	if !errors.Is(err, ErrExpired) {
		t.Fatalf("err = %v, want expired", err)
	}
	put.Spot = 0
	_, err = put.Price(date(2026, 11, 16), discount.Flat{Rate: 0.01})
	if !errors.Is(err, ErrInvalidSpot) {
		t.Fatalf("err = %v, want invalid spot", err)
	}
}
