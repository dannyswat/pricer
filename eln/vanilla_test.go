package eln

import (
	"errors"
	"math"
	"testing"
	"time"

	"pricer/daycount"
	"pricer/discount"
	"pricer/option"
	"pricer/pricing"
)

func date(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func TestIssuerPriceAddsBumpAndMarkup(t *testing.T) {
	asOf := date(2026, 11, 16)
	expiry := date(2027, 2, 16)
	note := Vanilla{
		Notional: 1_000_000, Spot: 100, Strike: 95, Expiry: expiry,
		Volatility: 0.2, DividendYield: 0.01, DayCount: daycount.Actual365Fixed,
		VolBump: 0.01, Markup: 0.005,
	}
	curve := discount.Flat{Rate: 0.04, Frequency: 0, DayCount: daycount.Actual365Fixed}
	got, err := note.Price(asOf, curve)
	if err != nil {
		t.Fatal(err)
	}

	put := option.EuropeanPut{
		Spot: 100, Strike: 95, Expiry: expiry, Volatility: 0.2,
		DividendYield: 0.01, Quantity: note.Notional / note.Strike,
		DayCount: daycount.Actual365Fixed,
	}
	mid, err := put.Price(asOf, curve)
	if err != nil {
		t.Fatal(err)
	}
	wantTheo := mid.DiscountFactor*note.Notional - mid.Premium
	if math.Abs(got.TheoreticalValue-wantTheo) > 1e-6 {
		t.Fatalf("theoretical = %.6f, want %.6f", got.TheoreticalValue, wantTheo)
	}
	if math.Abs(got.InvestorDelta-(-mid.Delta)) > 1e-8 {
		t.Fatalf("investor delta = %.8f, put delta = %.8f", got.InvestorDelta, mid.Delta)
	}

	haircut := put
	haircut.Volatility = 0.19
	down, err := haircut.Price(asOf, curve)
	if err != nil {
		t.Fatal(err)
	}
	wantBump := mid.Premium - down.Premium
	if wantBump <= 0 {
		t.Fatal("a vol haircut should reduce the put credited to the investor")
	}
	if math.Abs(got.CostOfBump-wantBump) > 1e-6 {
		t.Fatalf("cost of bump = %.6f, want %.6f", got.CostOfBump, wantBump)
	}
	wantMarkup := 0.005 * note.Notional
	wantIssue := wantTheo + wantBump + wantMarkup
	if math.Abs(got.IssuePrice-wantIssue) > 1e-6 {
		t.Fatalf("issue price = %.6f, want %.6f", got.IssuePrice, wantIssue)
	}
	if math.Abs(got.Day1Margin-(wantBump+wantMarkup)) > 1e-6 {
		t.Fatalf("day-1 margin = %.6f", got.Day1Margin)
	}
	// Crediting the put at the haircut vol, then adding markup, is the same price.
	haircutValue := down.DiscountFactor*note.Notional - down.Premium
	if math.Abs(got.IssuePrice-(haircutValue+wantMarkup)) > 1e-6 {
		t.Fatalf("issue price %.6f != haircut value + markup %.6f", got.IssuePrice, haircutValue+wantMarkup)
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

func TestNoBumpNoMarkupIsTheoretical(t *testing.T) {
	asOf := date(2026, 11, 16)
	note := Vanilla{
		Notional: 100, Spot: 100, Strike: 95, Expiry: date(2027, 2, 16),
		Volatility: 0.2, DayCount: daycount.Actual365Fixed,
	}
	curve := discount.Flat{Rate: 0.04, DayCount: daycount.Actual365Fixed}
	got, err := note.Price(asOf, curve)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(got.IssuePrice-got.TheoreticalValue) > 1e-12 || got.CostOfBump != 0 || got.Markup != 0 {
		t.Fatalf("quote = %+v", got)
	}
}

func TestRejectsBadELN(t *testing.T) {
	note := Vanilla{Notional: 0, Spot: 100, Strike: 95, Expiry: date(2027, 2, 16), Volatility: 0.2}
	_, err := note.Price(date(2026, 11, 16), discount.Flat{Rate: 0.01})
	if !errors.Is(err, ErrInvalidNotional) {
		t.Fatalf("err = %v", err)
	}
	note.Notional = 100
	note.VolBump = -1
	_, err = note.Price(date(2026, 11, 16), discount.Flat{Rate: 0.01})
	if !errors.Is(err, ErrInvalidBump) {
		t.Fatalf("err = %v", err)
	}
}
