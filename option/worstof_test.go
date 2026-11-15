package option

import (
	"errors"
	"math"
	"testing"

	"pricer/daycount"
	"pricer/discount"
)

func worstOfFixture(n int, rho float64) WorstOfPut {
	us := make([]Underlier, n)
	corr := make([][]float64, n)
	for i := range us {
		us[i] = Underlier{Spot: 100, Volatility: 0.2}
		corr[i] = make([]float64, n)
		for j := range corr[i] {
			corr[i][j] = rho
		}
		corr[i][i] = 1
	}
	return WorstOfPut{
		Underliers: us, Correlation: corr, Strike: 1, Expiry: date(2027, 11, 16),
		Notional: 100, DayCount: daycount.Actual365Fixed, Paths: 400_000, Seed: 7,
	}
}

var worstOfCurve = discount.Flat{Rate: 0.04, DayCount: daycount.Actual365Fixed}

func TestWorstOfSingleUnderlierMatchesBlackScholes(t *testing.T) {
	asOf := date(2026, 11, 16)
	w := worstOfFixture(1, 1)
	w.Underliers[0].DividendYield = 0.02
	got, err := w.Price(asOf, worstOfCurve)
	if err != nil {
		t.Fatal(err)
	}
	want, err := EuropeanPut{
		Spot: 100, Strike: 100, Expiry: w.Expiry, Volatility: 0.2, DividendYield: 0.02,
		Quantity: 1, DayCount: w.DayCount,
	}.Price(asOf, worstOfCurve)
	if err != nil {
		t.Fatal(err)
	}
	if diff := math.Abs(got.Premium - want.Premium); diff > 4*got.StdErr {
		t.Fatalf("premium = %.4f ± %.4f, want %.4f", got.Premium, got.StdErr, want.Premium)
	}
	if diff := math.Abs(got.Deltas[0] - want.Delta); diff > 0.02 {
		t.Fatalf("delta = %.4f, want %.4f", got.Deltas[0], want.Delta)
	}
	if diff := math.Abs(got.Vegas[0] - want.Vega); diff > 0.5 {
		t.Fatalf("vega = %.4f, want %.4f", got.Vegas[0], want.Vega)
	}
}

func TestWorstOfPerfectCorrelationEqualsSingle(t *testing.T) {
	asOf := date(2026, 11, 16)
	one, err := worstOfFixture(1, 1).Price(asOf, worstOfCurve)
	if err != nil {
		t.Fatal(err)
	}
	three, err := worstOfFixture(3, 1).Price(asOf, worstOfCurve)
	if err != nil {
		t.Fatal(err)
	}
	if diff := math.Abs(one.Premium - three.Premium); diff > 4*math.Hypot(one.StdErr, three.StdErr) {
		t.Fatalf("perfectly correlated basket = %.4f, single = %.4f", three.Premium, one.Premium)
	}
}

func TestWorstOfFallsAsCorrelationRises(t *testing.T) {
	asOf := date(2026, 11, 16)
	prev := math.Inf(1)
	for _, rho := range []float64{-0.4, 0, 0.5, 0.9} {
		q, err := worstOfFixture(3, rho).Price(asOf, worstOfCurve)
		if err != nil {
			t.Fatal(err)
		}
		if q.Premium >= prev {
			t.Fatalf("rho %.1f: premium %.4f did not fall below %.4f", rho, q.Premium, prev)
		}
		prev = q.Premium
	}
}

func TestWorstOfAtLeastEachVanillaPut(t *testing.T) {
	asOf := date(2026, 11, 16)
	w := worstOfFixture(2, 0.3)
	w.Underliers[1].Volatility = 0.35
	got, err := w.Price(asOf, worstOfCurve)
	if err != nil {
		t.Fatal(err)
	}
	for i, u := range w.Underliers {
		v, err := EuropeanPut{
			Spot: u.Spot, Strike: 100, Expiry: w.Expiry, Volatility: u.Volatility,
			Quantity: 1, DayCount: w.DayCount,
		}.Price(asOf, worstOfCurve)
		if err != nil {
			t.Fatal(err)
		}
		if got.Premium < v.Premium-4*got.StdErr {
			t.Fatalf("worst-of %.4f below vanilla put %d %.4f", got.Premium, i+1, v.Premium)
		}
	}
	probs := 0.0
	for _, p := range got.WorstProb {
		probs += p
	}
	if math.Abs(probs-1) > 1e-12 {
		t.Fatalf("worst probabilities sum to %v", probs)
	}
}

func TestWorstOfUsesInitialLevelAndIsReproducible(t *testing.T) {
	asOf := date(2026, 11, 16)
	w := worstOfFixture(2, 0.5)
	w.Underliers[0].Initial = 110 // spot is already below its fixing
	a, err := w.Price(asOf, worstOfCurve)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := w.Price(asOf, worstOfCurve)
	if a.Premium != b.Premium {
		t.Fatalf("same seed gave %v and %v", a.Premium, b.Premium)
	}
	base, _ := worstOfFixture(2, 0.5).Price(asOf, worstOfCurve)
	if a.Premium <= base.Premium {
		t.Fatalf("lower performance should raise the put: %v <= %v", a.Premium, base.Premium)
	}
}

func TestWorstOfExpiryAndZeroVol(t *testing.T) {
	w := worstOfFixture(2, 0.5)
	w.Underliers[0].Initial = 125
	got, err := w.Price(w.Expiry, worstOfCurve)
	if err != nil {
		t.Fatal(err)
	}
	if want := 100 * (1 - 0.8); math.Abs(got.Premium-want) > 1e-9 {
		t.Fatalf("expiry premium = %v, want %v", got.Premium, want)
	}

	w = worstOfFixture(2, 0.5)
	w.Underliers[0].Volatility, w.Underliers[1].Volatility = 0, 0
	w.Strike = 1.1
	got, err = w.Price(date(2026, 11, 16), worstOfCurve)
	if err != nil {
		t.Fatal(err)
	}
	df := got.DiscountFactor
	if want := 100 * df * (1.1 - 1/df); math.Abs(got.Premium-want) > 1e-9 {
		t.Fatalf("zero-vol premium = %v, want %v", got.Premium, want)
	}
}

func TestWorstOfValidation(t *testing.T) {
	asOf := date(2026, 11, 16)
	cases := map[string]struct {
		mutate func(*WorstOfPut)
		want   error
	}{
		"empty":        {func(w *WorstOfPut) { w.Underliers, w.Correlation = nil, nil }, ErrInvalidBasket},
		"strike":       {func(w *WorstOfPut) { w.Strike = 0 }, ErrInvalidStrike},
		"notional":     {func(w *WorstOfPut) { w.Notional = 0 }, ErrInvalidNotional},
		"paths":        {func(w *WorstOfPut) { w.Paths = -1 }, ErrInvalidPaths},
		"spot":         {func(w *WorstOfPut) { w.Underliers[1].Spot = -1 }, ErrInvalidSpot},
		"vol":          {func(w *WorstOfPut) { w.Underliers[0].Volatility = -0.1 }, ErrInvalidVol},
		"asymmetric":   {func(w *WorstOfPut) { w.Correlation[0][1] = 0.9 }, ErrInvalidCorrelation},
		"out of range": {func(w *WorstOfPut) { w.Correlation[0][1], w.Correlation[1][0] = 1.5, 1.5 }, ErrInvalidCorrelation},
		"shape":        {func(w *WorstOfPut) { w.Correlation = [][]float64{{1}} }, ErrInvalidCorrelation},
		"expired":      {func(w *WorstOfPut) { w.Expiry = date(2026, 1, 1) }, ErrExpired},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			w := worstOfFixture(2, 0.5)
			tc.mutate(&w)
			if _, err := w.Price(asOf, worstOfCurve); !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}

	w := worstOfFixture(3, 0)
	w.Correlation = [][]float64{{1, 0.9, -0.9}, {0.9, 1, 0.9}, {-0.9, 0.9, 1}}
	if _, err := w.Price(asOf, worstOfCurve); !errors.Is(err, ErrInvalidCorrelation) {
		t.Fatalf("non-PSD matrix: err = %v", err)
	}
}
