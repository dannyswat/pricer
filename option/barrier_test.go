package option

import (
	"errors"
	"math"
	"math/rand/v2"
	"testing"

	"pricer/daycount"
	"pricer/discount"
	"pricer/pricing"
)

func TestDownInPutMatchesBrownianBridge(t *testing.T) {
	// S=100, K=100, H=90, r=5%, q=2%, T=1, σ=25%. The bridge weight is the
	// exact continuous hitting probability given the terminal spot.
	const (
		spot, strike, barrier = 100.0, 100.0, 90.0
		rate, div, vol, T     = 0.05, 0.02, 0.25, 1.0
	)
	df := math.Exp(-rate * T)
	got, err := priceDownInPut(spot, strike, barrier, T, df, vol, div, 1)
	if err != nil {
		t.Fatal(err)
	}
	mean, stderr := bridgeDownInPut(spot, strike, barrier, T, df, vol, div, 400_000, 11)
	if diff := math.Abs(got.Premium - mean); diff > 5*stderr {
		t.Fatalf("premium = %.6f, bridge = %.6f ± %.6f", got.Premium, mean, stderr)
	}
	vanilla, err := pricePut(spot, strike, T, df, vol, div, 1)
	if err != nil {
		t.Fatal(err)
	}
	if got.Premium >= vanilla.Premium || got.Premium <= 0 {
		t.Fatalf("down-in %.6f should sit strictly between 0 and vanilla %.6f", got.Premium, vanilla.Premium)
	}
	if got.Vega <= 0 {
		t.Fatalf("vega = %v", got.Vega)
	}
}

func TestDownInPutIsVanillaWhenBarrierCoversStrike(t *testing.T) {
	df := math.Exp(-0.04 * 0.5)
	got, err := priceDownInPut(100, 90, 95, 0.5, df, 0.2, 0.01, 2)
	if err != nil {
		t.Fatal(err)
	}
	want, err := pricePut(100, 90, 0.5, df, 0.2, 0.01, 2)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(got.Premium-want.Premium) > 1e-12 || math.Abs(got.Delta-want.Delta) > 1e-12 || math.Abs(got.Vega-want.Vega) > 1e-12 {
		t.Fatalf("down-in %+v, vanilla %+v", got, want)
	}
}

func TestDownInPutAlreadyKnockedIn(t *testing.T) {
	df := math.Exp(-0.03 * 0.75)
	got, err := priceDownInPut(80, 100, 90, 0.75, df, 0.3, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	want, err := pricePut(80, 100, 0.75, df, 0.3, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(got.Premium-want.Premium) > 1e-12 || math.Abs(got.Gamma-want.Gamma) > 1e-12 {
		t.Fatalf("knocked-in %.12f, vanilla %.12f", got.Premium, want.Premium)
	}
}

func TestDownInPutRisesWithBarrier(t *testing.T) {
	df := math.Exp(-0.04)
	prev := 0.0
	for _, h := range []float64{70, 80, 90, 95} {
		got, err := priceDownInPut(100, 100, h, 1, df, 0.2, 0, 1)
		if err != nil {
			t.Fatal(err)
		}
		if got.Premium <= prev {
			t.Fatalf("barrier %.0f: premium %.6f did not rise above %.6f", h, got.Premium, prev)
		}
		prev = got.Premium
	}
}

func TestDownInPutZeroVolAndExpiry(t *testing.T) {
	// Drift carries the spot up, so a down barrier is never hit.
	df := math.Exp(-0.05)
	got, err := priceDownInPut(100, 100, 90, 1, df, 0, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if got.Premium != 0 || got.Delta != 0 {
		t.Fatalf("unhit zero-vol quote = %+v", got)
	}

	// Dividend pulls the forward through the barrier.
	df = math.Exp(-0.01)
	got, err = priceDownInPut(100, 100, 90, 1, df, 0, 0.2, 3)
	if err != nil {
		t.Fatal(err)
	}
	forward := 100 * math.Exp(-0.2) / df
	want := 3 * df * (100 - forward)
	if math.Abs(got.Premium-want) > 1e-9 {
		t.Fatalf("hit zero-vol premium = %.9f, want %.9f", got.Premium, want)
	}

	got, err = priceDownInPut(95, 100, 90, 0, 1, 0.2, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if got.Premium != 0 {
		t.Fatalf("unhit expiry premium = %v", got.Premium)
	}
	got, err = priceDownInPut(85, 100, 90, 0, 1, 0.2, 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(got.Premium-30) > 1e-12 {
		t.Fatalf("knocked-in expiry premium = %v", got.Premium)
	}
}

func TestDownInPutGreeksMatchBump(t *testing.T) {
	const (
		spot, strike, barrier = 100.0, 100.0, 80.0
		rate, div, vol, T     = 0.04, 0.01, 0.22, 0.8
	)
	df := math.Exp(-rate * T)
	got, err := priceDownInPut(spot, strike, barrier, T, df, vol, div, 4)
	if err != nil {
		t.Fatal(err)
	}
	up, _ := priceDownInPut(spot+0.2, strike, barrier, T, df, vol, div, 4)
	dn, _ := priceDownInPut(spot-0.2, strike, barrier, T, df, vol, div, 4)
	fdDelta := (up.Premium - dn.Premium) / 0.4
	if math.Abs(got.Delta-fdDelta) > 5e-4 {
		t.Fatalf("delta = %.6f, bump = %.6f", got.Delta, fdDelta)
	}
	upV, _ := priceDownInPut(spot, strike, barrier, T, df, vol+0.002, div, 4)
	dnV, _ := priceDownInPut(spot, strike, barrier, T, df, vol-0.002, div, 4)
	fdVega := (upV.Premium - dnV.Premium) / 0.004
	if math.Abs(got.Vega-fdVega) > 0.05 {
		t.Fatalf("vega = %.6f, bump = %.6f", got.Vega, fdVega)
	}
	upR, _ := priceDownInPut(spot, strike, barrier, T, math.Exp(-(rate+0.001)*T), vol, div, 4)
	dnR, _ := priceDownInPut(spot, strike, barrier, T, math.Exp(-(rate-0.001)*T), vol, div, 4)
	fdRho := (upR.Premium - dnR.Premium) / 0.002
	if math.Abs(got.Rho-fdRho) > 0.02 {
		t.Fatalf("rho = %.6f, bump = %.6f", got.Rho, fdRho)
	}
}

func TestDownInPutUsesCurve(t *testing.T) {
	asOf := date(2026, 11, 16)
	expiry := date(2027, 11, 16)
	opt := DownInPut{
		Spot: 100, Strike: 100, Barrier: 85, Expiry: expiry,
		Volatility: 0.2, DividendYield: 0.01, Quantity: 1,
		DayCount: daycount.Actual365Fixed,
	}
	curve := discount.Flat{Rate: 0.04, Frequency: 2, DayCount: daycount.Actual365Fixed}
	got, err := opt.Price(asOf, curve)
	if err != nil {
		t.Fatal(err)
	}
	df, err := curve.DiscountFactor(asOf, expiry)
	if err != nil {
		t.Fatal(err)
	}
	want, err := priceDownInPut(100, 100, 85, 1, df, 0.2, 0.01, 1)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(got.Premium-want.Premium) > 1e-12 {
		t.Fatalf("premium = %.12f, want %.12f", got.Premium, want.Premium)
	}
	var inst pricing.Instrument = opt
	res, err := inst.TheoreticalPrice(asOf, curve)
	if err != nil || math.Abs(res.Dirty-got.Premium) > 1e-12 {
		t.Fatalf("instrument = %+v, err %v", res, err)
	}
}

func TestDownInPutValidation(t *testing.T) {
	asOf := date(2026, 11, 16)
	opt := DownInPut{
		Spot: 100, Strike: 100, Barrier: 0, Expiry: date(2027, 11, 16),
		Volatility: 0.2, Quantity: 1,
	}
	_, err := opt.Price(asOf, discount.Flat{Rate: 0.04})
	if !errors.Is(err, ErrInvalidBarrier) {
		t.Fatalf("err = %v", err)
	}
	opt.Barrier = 90
	opt.Expiry = date(2026, 1, 1)
	_, err = opt.Price(asOf, discount.Flat{Rate: 0.04})
	if !errors.Is(err, ErrExpired) {
		t.Fatalf("err = %v", err)
	}
}

// bridgeDownInPut prices the continuous barrier by sampling the terminal spot
// and weighting by the Brownian-bridge hitting probability.
func bridgeDownInPut(spot, strike, barrier, yearFrac, df, vol, dividend float64, paths int, seed uint64) (mean, stderr float64) {
	rng := rand.New(rand.NewPCG(seed, 0x9e3779b97f4a7c15))
	var sum, sumSq float64
	variance := vol * vol * yearFrac
	for range paths {
		z := rng.NormFloat64()
		pay := 0.5 * (bridgePayoff(z, spot, strike, barrier, yearFrac, df, vol, dividend, variance) +
			bridgePayoff(-z, spot, strike, barrier, yearFrac, df, vol, dividend, variance))
		sum += pay
		sumSq += pay * pay
	}
	n := float64(paths)
	mean = sum / n
	stderr = math.Sqrt(math.Max(sumSq/n-mean*mean, 0) / (n - 1))
	return mean, stderr
}

func bridgePayoff(z, spot, strike, barrier, yearFrac, df, vol, dividend, variance float64) float64 {
	st := spot * math.Exp(((-math.Log(df)/yearFrac)-dividend-0.5*vol*vol)*yearFrac+vol*math.Sqrt(yearFrac)*z)
	hit := 1.0
	if st > barrier {
		hit = math.Exp(-2 * math.Log(spot/barrier) * math.Log(st/barrier) / variance)
	}
	return df * math.Max(strike-st, 0) * hit
}
