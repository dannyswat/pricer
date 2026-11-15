package daycount

import (
	"math"
	"testing"
	"time"
)

func date(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func TestActualActualISDA(t *testing.T) {
	// 15 Nov 2024 to 15 Nov 2025 crosses the leap-year boundary.
	// 15 Nov 2024 -> 1 Jan 2025 is 47 days of a 366-day year.
	// 1 Jan 2025 -> 15 Nov 2025 is 318 days of a 365-day year.
	got, err := ActualActualISDA.YearFraction(date(2024, 11, 15), date(2025, 11, 15))
	if err != nil {
		t.Fatal(err)
	}
	want := 47.0/366.0 + 318.0/365.0
	if math.Abs(got-want) > 1e-12 {
		t.Fatalf("year fraction = %.12f, want %.12f", got, want)
	}

	got, err = ActualActualISDA.YearFraction(date(2027, 1, 15), date(2027, 3, 15))
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(got-59.0/365.0) > 1e-12 {
		t.Fatalf("same-year fraction = %.12f, want %.12f", got, 59.0/365.0)
	}
}

func TestActual365Fixed(t *testing.T) {
	got, err := Actual365Fixed.YearFraction(date(2024, 1, 1), date(2025, 1, 1))
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(got-366.0/365.0) > 1e-12 {
		t.Fatalf("got %.12f", got)
	}
}
