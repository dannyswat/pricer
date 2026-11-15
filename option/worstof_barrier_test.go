package option

import (
	"errors"
	"math"
	"testing"

	"pricer/daycount"
	"pricer/discount"
	"pricer/pricing"
)

func downInBasket(n int, rho, barrier float64) WorstOfDownInPut {
	us := make([]Underlier, n)
	for i := range us {
		us[i] = Underlier{Spot: 100, Volatility: 0.25}
	}
	return WorstOfDownInPut{
		Underliers: us, Correlation: equicorrelation(n, rho), Strike: 1, Barrier: barrier,
		Expiry: date(2027, 11, 16), Notional: 100, DayCount: daycount.Actual365Fixed,
		Paths: 40_000, Seed: 5,
	}
}

func equicorrelation(n int, rho float64) [][]float64 {
	m := make([][]float64, n)
	for i := range m {
		m[i] = make([]float64, n)
		for j := range m[i] {
			m[i][j] = rho
		}
		m[i][i] = 1
	}
	return m
}

func TestWorstOfDownInSingleMatchesClosedForm(t *testing.T) {
	asOf := date(2026, 11, 16)
	note := downInBasket(1, 1, 0.8)
	note.Underliers[0].DividendYield = 0.02
	got, err := note.Price(asOf, discount.Flat{Rate: 0.04, DayCount: daycount.Actual365Fixed})
	if err != nil {
		t.Fatal(err)
	}
	want, err := DownInPut{
		Spot: 100, Strike: 100, Barrier: 80, Expiry: note.Expiry,
		Volatility: 0.25, DividendYield: 0.02, Quantity: 1, DayCount: note.DayCount,
	}.Price(asOf, discount.Flat{Rate: 0.04, DayCount: daycount.Actual365Fixed})
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(got.Premium-want.Premium) > 1e-9 || math.Abs(got.Deltas[0]-want.Delta) > 1e-8 {
		t.Fatalf("single = %+v, closed form premium %.6f delta %.6f", got.Premium, want.Premium, want.Delta)
	}
	if got.KnockInProb <= 0 || got.KnockInProb >= 1 {
		t.Fatalf("knock-in probability = %v", got.KnockInProb)
	}
}

func TestWorstOfDownInBridgeMatchesClosedForm(t *testing.T) {
	asOf := date(2026, 11, 16)
	curve := discount.Flat{Rate: 0.05, DayCount: daycount.Actual365Fixed}
	note := downInBasket(1, 1, 0.9)
	note.Paths = 120_000
	note.Underliers[0].DividendYield = 0.01
	yearFrac, df, err := note.resolve(asOf, curve)
	if err != nil {
		t.Fatal(err)
	}
	got := note.simulate(yearFrac, df)
	want, err := note.Price(asOf, curve)
	if err != nil {
		t.Fatal(err)
	}
	if diff := math.Abs(got.Premium - want.Premium); diff > 5*got.StdErr {
		t.Fatalf("bridge premium = %.4f ± %.4f, closed form %.4f", got.Premium, got.StdErr, want.Premium)
	}
	if diff := math.Abs(got.KnockInProb - want.KnockInProb); diff > 0.01 {
		t.Fatalf("knock-in = %.4f, analytic %.4f", got.KnockInProb, want.KnockInProb)
	}
}

func TestWorstOfDownInAboveEachSingleAndBelowVanilla(t *testing.T) {
	asOf := date(2026, 11, 16)
	curve := discount.Flat{Rate: 0.04, DayCount: daycount.Actual365Fixed}
	note := downInBasket(2, 0.3, 0.75)
	note.Underliers[1].Volatility = 0.35
	note.Paths = 30_000
	got, err := note.Price(asOf, curve)
	if err != nil {
		t.Fatal(err)
	}
	for i, u := range note.Underliers {
		one := WorstOfDownInPut{
			Underliers: []Underlier{u}, Strike: note.Strike, Barrier: note.Barrier,
			Expiry: note.Expiry, Notional: note.Notional, DayCount: note.DayCount,
		}
		v, err := one.Price(asOf, curve)
		if err != nil {
			t.Fatal(err)
		}
		if got.Premium+4*got.StdErr < v.Premium {
			t.Fatalf("basket %.4f ± %.4f below single %d %.4f", got.Premium, got.StdErr, i+1, v.Premium)
		}
	}
	vanilla, err := note.asPut().Price(asOf, curve)
	if err != nil {
		t.Fatal(err)
	}
	if got.Premium-4*got.StdErr > vanilla.Premium+4*vanilla.StdErr {
		t.Fatalf("down-in %.4f exceeds worst-of put %.4f", got.Premium, vanilla.Premium)
	}
	if got.KnockInProb <= 0 || got.KnockInProb > 1 {
		t.Fatalf("knock-in = %v", got.KnockInProb)
	}
}

func TestWorstOfDownInBarrierAboveStrikeIsWorstOf(t *testing.T) {
	asOf := date(2026, 11, 16)
	curve := discount.Flat{Rate: 0.04, DayCount: daycount.Actual365Fixed}
	note := downInBasket(2, 0.4, 1.05)
	note.Strike = 1
	note.Paths = 2_000
	got, err := note.Price(asOf, curve)
	if err != nil {
		t.Fatal(err)
	}
	want, err := note.asPut().Price(asOf, curve)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(got.Premium-want.Premium) > 1e-9 || math.Abs(got.Deltas[0]-want.Deltas[0]) > 1e-9 {
		t.Fatalf("barrier above strike = %.6f, worst-of = %.6f", got.Premium, want.Premium)
	}
}

func TestWorstOfDownInAlreadyKnockedIn(t *testing.T) {
	asOf := date(2026, 11, 16)
	curve := discount.Flat{Rate: 0.03, DayCount: daycount.Actual365Fixed}
	note := downInBasket(2, 0.2, 0.8)
	note.Underliers[0].Initial = 100
	note.Underliers[0].Spot = 70 // performance 0.70, already through 0.80
	note.Paths = 1_000
	got, err := note.Price(asOf, curve)
	if err != nil {
		t.Fatal(err)
	}
	want, err := note.asPut().Price(asOf, curve)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(got.Premium-want.Premium) > 1e-9 || got.KnockInProb != 1 {
		t.Fatalf("knocked-in premium %.6f vs %.6f, prob %v", got.Premium, want.Premium, got.KnockInProb)
	}
}

func TestWorstOfDownInRisesWithBarrier(t *testing.T) {
	asOf := date(2026, 11, 16)
	curve := discount.Flat{Rate: 0.04, DayCount: daycount.Actual365Fixed}
	low := downInBasket(2, 0.5, 0.6)
	low.Paths = 25_000
	high := low
	high.Barrier = 0.85
	a, err := low.Price(asOf, curve)
	if err != nil {
		t.Fatal(err)
	}
	b, err := high.Price(asOf, curve)
	if err != nil {
		t.Fatal(err)
	}
	if b.Premium-a.Premium < 4*math.Hypot(a.StdErr, b.StdErr) {
		t.Fatalf("barrier 0.85 premium %.4f did not clear barrier 0.60 premium %.4f", b.Premium, a.Premium)
	}
}

func TestWorstOfDownInZeroVolAndExpiry(t *testing.T) {
	curve := discount.Flat{Rate: 0.04, DayCount: daycount.Actual365Fixed}
	note := downInBasket(2, 0.5, 0.9)
	note.Underliers[0].Volatility, note.Underliers[1].Volatility = 0, 0
	got, err := note.Price(date(2026, 11, 16), curve)
	if err != nil {
		t.Fatal(err)
	}
	if got.Premium != 0 || got.KnockInProb != 0 {
		t.Fatalf("upward zero-vol quote premium %v prob %v", got.Premium, got.KnockInProb)
	}

	note.Underliers[0].DividendYield = 0.3
	got, err = note.Price(date(2026, 11, 16), curve)
	if err != nil {
		t.Fatal(err)
	}
	want, err := note.asPut().Price(date(2026, 11, 16), curve)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(got.Premium-want.Premium) > 1e-9 || got.KnockInProb != 1 {
		t.Fatalf("hit zero-vol %.9f vs %.9f", got.Premium, want.Premium)
	}

	note = downInBasket(2, 0, 0.8)
	note.Underliers[0].Initial, note.Underliers[1].Initial = 100, 100
	note.Underliers[0].Spot = 85
	got, err = note.Price(note.Expiry, curve)
	if err != nil {
		t.Fatal(err)
	}
	if got.Premium != 0 {
		t.Fatalf("unhit expiry premium = %v", got.Premium)
	}
	note.Underliers[1].Spot = 70
	got, err = note.Price(note.Expiry, curve)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(got.Premium-30) > 1e-9 {
		t.Fatalf("knocked-in expiry premium = %v", got.Premium)
	}
}

func TestWorstOfDownInDeltaHoldsInitialFixing(t *testing.T) {
	asOf := date(2026, 11, 16)
	curve := discount.Flat{Rate: 0.04, DayCount: daycount.Actual365Fixed}
	floating := downInBasket(2, 0.45, 0.75)
	floating.Paths = 5_000
	pinned := floating
	pinned.Underliers = append([]Underlier(nil), floating.Underliers...)
	pinned.Underliers[0].Initial = floating.Underliers[0].Spot
	pinned.Underliers[1].Initial = floating.Underliers[1].Spot
	a, err := floating.Price(asOf, curve)
	if err != nil {
		t.Fatal(err)
	}
	b, err := pinned.Price(asOf, curve)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(a.Premium-b.Premium) > 1e-9 || math.Abs(a.Deltas[0]-b.Deltas[0]) > 1e-9 || math.Abs(a.Vegas[1]-b.Vegas[1]) > 1e-9 {
		t.Fatalf("omitted initial %+v, explicit initial %+v", a, b)
	}
	if math.Abs(a.Deltas[0]) < 1e-3 && math.Abs(a.Deltas[1]) < 1e-3 {
		t.Fatalf("spot deltas vanished: %v", a.Deltas)
	}
}

func TestWorstOfDownInIsReproducible(t *testing.T) {
	note := downInBasket(2, 0.4, 0.8)
	note.Paths = 8_000
	curve := discount.Flat{Rate: 0.04, DayCount: daycount.Actual365Fixed}
	a, err := note.Price(date(2026, 11, 16), curve)
	if err != nil {
		t.Fatal(err)
	}
	b, err := note.Price(date(2026, 11, 16), curve)
	if err != nil {
		t.Fatal(err)
	}
	if a.Premium != b.Premium || a.Deltas[1] != b.Deltas[1] {
		t.Fatalf("%v != %v", a.Premium, b.Premium)
	}
	var inst pricing.Instrument = note
	res, err := inst.TheoreticalPrice(date(2026, 11, 16), curve)
	if err != nil || math.Abs(res.Dirty-a.Premium) > 1e-12 {
		t.Fatalf("instrument %+v %v", res, err)
	}
}

func TestWorstOfDownInValidation(t *testing.T) {
	asOf := date(2026, 11, 16)
	curve := discount.Flat{Rate: 0.04}
	note := downInBasket(2, 0.2, 0)
	if _, err := note.Price(asOf, curve); !errors.Is(err, ErrInvalidBarrier) {
		t.Fatalf("err = %v", err)
	}
	note.Barrier = 0.8
	note.Underliers = nil
	note.Correlation = nil
	if _, err := note.Price(asOf, curve); !errors.Is(err, ErrInvalidBasket) {
		t.Fatalf("err = %v", err)
	}
}
