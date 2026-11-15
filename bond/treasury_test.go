package bond

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

func TestParBondOnCouponDate(t *testing.T) {
	note := Treasury{
		Face:       100,
		CouponRate: 0.04,
		Maturity:   date(2031, 11, 15),
		Frequency:  2,
	}
	settlement := date(2026, 11, 15)
	got, err := note.PriceAtYield(settlement, 0.04)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(got.Accrued) > 1e-12 {
		t.Fatalf("accrued = %v, want 0", got.Accrued)
	}
	if math.Abs(got.Dirty-100) > 1e-9 {
		t.Fatalf("dirty = %.12f, want 100", got.Dirty)
	}
	if math.Abs(got.Clean-100) > 1e-9 {
		t.Fatalf("clean = %.12f, want 100", got.Clean)
	}
	if !got.Flows[0].Date.Equal(date(2027, 5, 15)) {
		t.Fatalf("first cash flow %s, want the next coupon, not settlement", got.Flows[0].Date.Format("2006-01-02"))
	}
}

func TestAccruedAndParIdentityBetweenCoupons(t *testing.T) {
	note := Treasury{
		Face:       100,
		CouponRate: 0.04,
		Maturity:   date(2031, 11, 15),
		Frequency:  2,
	}
	// 15 Nov 2026 to 15 May 2027 is 181 days. 15 Nov to 15 Feb is 92 days.
	settlement := date(2027, 2, 15)
	got, err := note.PriceAtYield(settlement, 0.04)
	if err != nil {
		t.Fatal(err)
	}
	const periodDays = 181
	const accruedDays = 92
	wantAccrued := 2.0 * accruedDays / periodDays
	if math.Abs(got.Accrued-wantAccrued) > 1e-12 {
		t.Fatalf("accrued = %.12f, want %.12f", got.Accrued, wantAccrued)
	}
	w := float64(periodDays-accruedDays) / periodDays
	wantDirty := 100 * math.Pow(1.02, 1-w)
	if math.Abs(got.Dirty-wantDirty) > 1e-9 {
		t.Fatalf("dirty = %.12f, want %.12f", got.Dirty, wantDirty)
	}
	if math.Abs(got.Clean-(wantDirty-wantAccrued)) > 1e-9 {
		t.Fatalf("clean = %.12f", got.Clean)
	}
}

func TestMonthEndSchedule(t *testing.T) {
	note := Treasury{
		Face:       100,
		CouponRate: 0.03,
		Maturity:   date(2032, 1, 31),
		Frequency:  2,
	}
	got, err := note.PriceAtYield(date(2031, 1, 31), 0.03)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Flows[0].Date.Equal(date(2031, 7, 31)) {
		t.Fatalf("first coupon = %s, want 2031-07-31", got.Flows[0].Date.Format("2006-01-02"))
	}
}

func TestZeroCouponTheoreticalPrice(t *testing.T) {
	strip := Treasury{
		Face:       100,
		CouponRate: 0,
		Maturity:   date(2027, 11, 15),
		Frequency:  2,
	}
	settlement := date(2026, 11, 15)
	curve := discount.Flat{Rate: 0.05, Frequency: 0, DayCount: daycount.ActualActualISDA}
	got, err := strip.TheoreticalPrice(settlement, curve)
	if err != nil {
		t.Fatal(err)
	}
	want := 100 * math.Exp(-0.05)
	if math.Abs(got.Dirty-want) > 1e-12 {
		t.Fatalf("dirty = %.12f, want %.12f", got.Dirty, want)
	}
	if got.Accrued != 0 || len(got.Flows) != 1 || got.Flows[0].Kind != pricing.Principal {
		t.Fatalf("unexpected zero-coupon result: %+v", got)
	}
}

func TestTheoreticalPriceSumsFlows(t *testing.T) {
	note := Treasury{
		Face:       10000,
		CouponRate: 0.0425,
		Maturity:   date(2036, 11, 15),
		Frequency:  2,
	}
	settlement := date(2026, 11, 16)
	curve := discount.Flat{Rate: 0.04, Frequency: 0, DayCount: daycount.ActualActualISDA}
	got, err := note.TheoreticalPrice(settlement, curve)
	if err != nil {
		t.Fatal(err)
	}
	var sum float64
	for _, cf := range got.Flows {
		sum += cf.PresentValue
		if cf.Date.Equal(note.Maturity) && cf.Kind == pricing.Principal && cf.Amount != note.Face {
			t.Fatalf("principal amount = %v", cf.Amount)
		}
	}
	if math.Abs(sum-got.Dirty) > 1e-9 {
		t.Fatalf("sum of PVs %.8f != dirty %.8f", sum, got.Dirty)
	}
	if got.Dirty <= note.Face {
		t.Fatalf("4.25%% bond discounted at 4%% continuous should be above par, got %.4f", got.Dirty)
	}
}

func TestYieldRoundTrip(t *testing.T) {
	note := Treasury{
		Face:       100,
		CouponRate: 0.0425,
		Maturity:   date(2036, 11, 15),
		Frequency:  2,
	}
	settlement := date(2026, 11, 16)
	const want = 0.045
	priced, err := note.PriceAtYield(settlement, want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := note.YieldFromDirty(settlement, priced.Dirty)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(got-want) > 1e-10 {
		t.Fatalf("yield = %.12f, want %.12f", got, want)
	}
}

func TestRejectsMaturedAndBadFace(t *testing.T) {
	note := Treasury{Face: 100, CouponRate: 0.04, Maturity: date(2026, 11, 15), Frequency: 2}
	_, err := note.TheoreticalPrice(date(2026, 11, 15), discount.Flat{Rate: 0.01})
	if !errors.Is(err, ErrMatured) {
		t.Fatalf("err = %v, want matured", err)
	}
	note.Face = 0
	note.Maturity = date(2030, 11, 15)
	_, err = note.PriceAtYield(date(2026, 11, 16), 0.04)
	if !errors.Is(err, ErrInvalidFace) {
		t.Fatalf("err = %v, want invalid face", err)
	}
}
