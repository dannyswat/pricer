package discount

import (
	"math"
	"testing"
	"time"

	"pricer/daycount"
)

func date(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func TestFlatContinuous(t *testing.T) {
	curve := Flat{Rate: 0.05, Frequency: 0, DayCount: daycount.Actual365Fixed}
	df, err := curve.DiscountFactor(date(2026, 11, 16), date(2027, 11, 16))
	if err != nil {
		t.Fatal(err)
	}
	yearFrac := 365.0 / 365.0
	want := math.Exp(-0.05 * yearFrac)
	if math.Abs(df-want) > 1e-12 {
		t.Fatalf("df = %.12f, want %.12f", df, want)
	}
}

func TestZeroCurveMatchesFlatZeros(t *testing.T) {
	asOf := date(2026, 11, 16)
	const rate = 0.03
	curve := ZeroCurve{
		AsOf:     asOf,
		DayCount: daycount.Actual365Fixed,
		Pillars: []Pillar{
			{Date: date(2027, 11, 16), ContinuousZero: rate},
			{Date: date(2029, 11, 16), ContinuousZero: rate},
		},
	}
	mid := date(2028, 5, 16)
	got, err := curve.DiscountFactor(asOf, mid)
	if err != nil {
		t.Fatal(err)
	}
	flat := Flat{Rate: rate, Frequency: 0, DayCount: daycount.Actual365Fixed}
	want, err := flat.DiscountFactor(asOf, mid)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(got-want) > 1e-12 {
		t.Fatalf("interpolated df = %.12f, flat df = %.12f", got, want)
	}
}
