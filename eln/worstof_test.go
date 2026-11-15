package eln

import (
	"errors"
	"math"
	"testing"

	"pricer/daycount"
	"pricer/discount"
	"pricer/option"
	"pricer/pricing"
)

func TestWorstOfELNMatchesVanilla(t *testing.T) {
	asOf := date(2026, 11, 16)
	expiry := date(2027, 2, 16)
	curve := discount.Flat{Rate: 0.04, DayCount: daycount.Actual365Fixed}
	vanilla := Vanilla{
		Notional: 100, Spot: 100, Strike: 95, Expiry: expiry,
		Volatility: 0.2, DividendYield: 0.02, DayCount: daycount.Actual365Fixed,
		VolBump: 0.01, Markup: 0.005,
	}
	want, err := vanilla.Price(asOf, curve)
	if err != nil {
		t.Fatal(err)
	}
	got, err := (WorstOf{
		Notional: 100,
		Underliers: []option.Underlier{{
			Spot: 100, Volatility: 0.2, DividendYield: 0.02,
		}},
		Strike: 0.95, Expiry: expiry, DayCount: daycount.Actual365Fixed,
		VolBump: 0.01, Markup: 0.005, Paths: 300_000, Seed: 3,
	}).Price(asOf, curve)
	if err != nil {
		t.Fatal(err)
	}
	if diff := math.Abs(got.TheoreticalValue - want.TheoreticalValue); diff > 4*got.TheoreticalStdErr {
		t.Fatalf("theoretical = %.4f ± %.4f, want %.4f", got.TheoreticalValue, got.TheoreticalStdErr, want.TheoreticalValue)
	}
	if diff := math.Abs(got.IssuePrice - want.IssuePrice); diff > 4*got.IssueStdErr {
		t.Fatalf("issue = %.4f ± %.4f, want %.4f", got.IssuePrice, got.IssueStdErr, want.IssuePrice)
	}
	bumpErr := math.Hypot(got.PutStdErr, got.BumpedPutStdErr)
	if diff := math.Abs(got.CostOfBump - want.CostOfBump); diff > 4*bumpErr {
		t.Fatalf("cost of bump = %.4f ± %.4f, want %.4f", got.CostOfBump, bumpErr, want.CostOfBump)
	}
	if diff := math.Abs(got.InvestorDeltas[0] - want.InvestorDelta); diff > 0.03 {
		t.Fatalf("investor delta = %.4f, want %.4f", got.InvestorDeltas[0], want.InvestorDelta)
	}
	if math.Abs(got.PutNotional-100/0.95) > 1e-12 {
		t.Fatalf("put notional = %v", got.PutNotional)
	}
}

func TestWorstOfELNReplication(t *testing.T) {
	asOf := date(2026, 11, 16)
	expiry := date(2027, 2, 16)
	curve := discount.Flat{Rate: 0.04, DayCount: daycount.Actual365Fixed}
	note := WorstOf{
		Notional: 1_000_000,
		Underliers: []option.Underlier{
			{Spot: 100, Volatility: 0.2, DividendYield: 0.01},
			{Spot: 50, Volatility: 0.3},
		},
		Correlation: [][]float64{{1, 0.4}, {0.4, 1}},
		Strike:      0.95, Expiry: expiry, DayCount: daycount.Actual365Fixed,
		VolBump: 0.01, Markup: 0.005, Paths: 40_000, Seed: 9,
	}
	got, err := note.Price(asOf, curve)
	if err != nil {
		t.Fatal(err)
	}
	haircut := note.embeddedPut(note.VolBump)
	down, err := haircut.Price(asOf, curve)
	if err != nil {
		t.Fatal(err)
	}
	wantIssue := down.DiscountFactor*note.Notional - down.Premium + note.Markup*note.Notional
	if math.Abs(got.IssuePrice-wantIssue) > 1e-6 {
		t.Fatalf("issue %.6f != haircut value + markup %.6f", got.IssuePrice, wantIssue)
	}
	if math.Abs(got.Day1Margin-(got.CostOfBump+got.Markup)) > 1e-9 {
		t.Fatalf("day-1 margin = %.6f", got.Day1Margin)
	}
	if got.CostOfBump <= 0 {
		t.Fatal("a vol haircut should reduce the put credited to the investor")
	}
	var inst pricing.Instrument = note
	res, err := inst.TheoreticalPrice(asOf, curve)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(res.Dirty-got.TheoreticalValue) > 1e-8 {
		t.Fatalf("instrument value = %.6f", res.Dirty)
	}
}

func TestWorstOfELNRisesWithCorrelation(t *testing.T) {
	asOf := date(2026, 11, 16)
	prev := 0.0
	for _, rho := range []float64{-0.3, 0.8} {
		got, err := (WorstOf{
			Notional: 100,
			Underliers: []option.Underlier{
				{Spot: 100, Volatility: 0.25},
				{Spot: 100, Volatility: 0.25},
				{Spot: 100, Volatility: 0.25},
			},
			Correlation: equicorr(3, rho),
			Strike:      0.95, Expiry: date(2027, 2, 16), DayCount: daycount.Actual365Fixed,
			Paths: 200_000, Seed: 11,
		}).Price(asOf, discount.Flat{Rate: 0.04, DayCount: daycount.Actual365Fixed})
		if err != nil {
			t.Fatal(err)
		}
		if got.IssuePrice <= prev {
			t.Fatalf("rho %.1f: issue %.4f did not rise above %.4f", rho, got.IssuePrice, prev)
		}
		prev = got.IssuePrice
	}
}

func TestWorstOfELNExpiryAndZeroVol(t *testing.T) {
	curve := discount.Flat{Rate: 0.04, DayCount: daycount.Actual365Fixed}
	expiry := date(2027, 2, 16)
	note := WorstOf{
		Notional: 100,
		Underliers: []option.Underlier{
			{Spot: 100, Initial: 100, Volatility: 0.2},
			{Spot: 80, Initial: 100, Volatility: 0.3},
		},
		Strike: 0.95, Expiry: expiry, DayCount: daycount.Actual365Fixed,
		VolBump: 0.01, Markup: 0.005, Paths: 1_000, Seed: 1,
	}
	got, err := note.Price(expiry, curve)
	if err != nil {
		t.Fatal(err)
	}
	payoff := 100 * 0.8 / 0.95
	if math.Abs(got.TheoreticalValue-payoff) > 1e-9 {
		t.Fatalf("expiry value = %v, want %v", got.TheoreticalValue, payoff)
	}
	if math.Abs(got.CostOfBump) > 1e-9 {
		t.Fatalf("expiry bump cost = %v", got.CostOfBump)
	}
	if math.Abs(got.IssuePrice-(payoff+0.5)) > 1e-9 {
		t.Fatalf("expiry issue = %v", got.IssuePrice)
	}

	note = WorstOf{
		Notional: 100,
		Underliers: []option.Underlier{
			{Spot: 100, Volatility: 0},
			{Spot: 40, Initial: 40, Volatility: 0},
		},
		Strike: 0.95, Expiry: expiry, DayCount: daycount.Actual365Fixed,
		Markup: 0.005,
	}
	got, err = note.Price(date(2026, 11, 16), curve)
	if err != nil {
		t.Fatal(err)
	}
	want := got.DiscountFactor*100 + 0.5
	if math.Abs(got.IssuePrice-want) > 1e-9 || got.PutPremium != 0 {
		t.Fatalf("zero-vol issue = %v, put = %v, want %v", got.IssuePrice, got.PutPremium, want)
	}
}

func TestWorstOfELNIsReproducible(t *testing.T) {
	note := WorstOf{
		Notional: 100,
		Underliers: []option.Underlier{
			{Spot: 100, Volatility: 0.2},
			{Spot: 100, Volatility: 0.3},
		},
		Correlation: [][]float64{{1, 0.5}, {0.5, 1}},
		Strike:      1, Expiry: date(2027, 11, 16), DayCount: daycount.Actual365Fixed,
		VolBump: 0.01, Paths: 20_000, Seed: 5,
	}
	curve := discount.Flat{Rate: 0.03, DayCount: daycount.Actual365Fixed}
	a, err := note.Price(date(2026, 11, 16), curve)
	if err != nil {
		t.Fatal(err)
	}
	b, err := note.Price(date(2026, 11, 16), curve)
	if err != nil {
		t.Fatal(err)
	}
	if a.IssuePrice != b.IssuePrice || a.TheoreticalValue != b.TheoreticalValue {
		t.Fatalf("%v != %v", a.IssuePrice, b.IssuePrice)
	}
}

func TestWorstOfELNValidation(t *testing.T) {
	asOf := date(2026, 11, 16)
	curve := discount.Flat{Rate: 0.04}
	base := func() WorstOf {
		return WorstOf{
			Notional:   100,
			Underliers: []option.Underlier{{Spot: 100, Volatility: 0.2}},
			Strike:     0.95, Expiry: date(2027, 2, 16),
		}
	}
	cases := []struct {
		name   string
		mutate func(*WorstOf)
		want   error
	}{
		{"notional", func(n *WorstOf) { n.Notional = 0 }, ErrInvalidNotional},
		{"markup", func(n *WorstOf) { n.Markup = math.NaN() }, ErrInvalidMarkup},
		{"strike", func(n *WorstOf) { n.Strike = 0 }, option.ErrInvalidStrike},
		{"basket", func(n *WorstOf) { n.Underliers = nil }, option.ErrInvalidBasket},
		{"bump", func(n *WorstOf) { n.VolBump = 0.25 }, ErrInvalidBump},
		{"negative bump", func(n *WorstOf) { n.VolBump = -0.01 }, ErrInvalidBump},
		{"correlation", func(n *WorstOf) {
			n.Underliers = append(n.Underliers, option.Underlier{Spot: 100, Volatility: 0.2})
			n.Correlation = [][]float64{{1, 1.2}, {1.2, 1}}
		}, option.ErrInvalidCorrelation},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			n := base()
			tc.mutate(&n)
			if _, err := n.Price(asOf, curve); !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func equicorr(n int, rho float64) [][]float64 {
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
