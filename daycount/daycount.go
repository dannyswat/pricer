// Package daycount converts a pair of calendar dates into a year fraction.
package daycount

import (
	"fmt"
	"time"
)

// Convention selects a year-fraction rule.
type Convention int

const (
	// ActualActualISDA splits the interval on year boundaries and divides
	// each piece by 365 or 366. It is the default for theoretical discounting.
	ActualActualISDA Convention = iota
	// Actual365Fixed divides the actual number of days by 365.
	Actual365Fixed
)

func (c Convention) String() string {
	switch c {
	case ActualActualISDA:
		return "Actual/Actual ISDA"
	case Actual365Fixed:
		return "Actual/365 Fixed"
	default:
		return fmt.Sprintf("daycount(%d)", int(c))
	}
}

// Parse accepts the CLI names for a convention.
func Parse(name string) (Convention, error) {
	switch name {
	case "actual/actual", "aa", "isda":
		return ActualActualISDA, nil
	case "actual/365", "a365":
		return Actual365Fixed, nil
	default:
		return 0, fmt.Errorf("unknown day count %q (use actual/actual or actual/365)", name)
	}
}

// Normalize returns the calendar date of t as UTC midnight, preserving the
// year-month-day in t's location. A zero time stays zero.
func Normalize(t time.Time) time.Time {
	if t.IsZero() {
		return t
	}
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// Days is the actual number of calendar days from start to end.
func Days(start, end time.Time) int {
	start = Normalize(start)
	end = Normalize(end)
	return int(end.Sub(start).Hours() / 24)
}

// YearFraction returns the year fraction from start to end under c.
// A non-positive interval has fraction 0.
func (c Convention) YearFraction(start, end time.Time) (float64, error) {
	start = Normalize(start)
	end = Normalize(end)
	if !end.After(start) {
		return 0, nil
	}
	switch c {
	case ActualActualISDA:
		return actualActualISDA(start, end), nil
	case Actual365Fixed:
		return float64(Days(start, end)) / 365, nil
	default:
		return 0, fmt.Errorf("unsupported day count %s", c)
	}
}

func actualActualISDA(start, end time.Time) float64 {
	if start.Year() == end.Year() {
		return float64(Days(start, end)) / float64(daysInYear(start.Year()))
	}
	yearEnd := time.Date(start.Year()+1, time.January, 1, 0, 0, 0, 0, time.UTC)
	frac := float64(Days(start, yearEnd)) / float64(daysInYear(start.Year()))
	for y := start.Year() + 1; y < end.Year(); y++ {
		frac += 1
	}
	yearStart := time.Date(end.Year(), time.January, 1, 0, 0, 0, 0, time.UTC)
	frac += float64(Days(yearStart, end)) / float64(daysInYear(end.Year()))
	return frac
}

func daysInYear(year int) int {
	if isLeap(year) {
		return 366
	}
	return 365
}

func isLeap(year int) bool {
	return year%4 == 0 && (year%100 != 0 || year%400 == 0)
}
