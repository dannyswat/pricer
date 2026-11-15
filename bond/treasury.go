// Package bond prices fixed-coupon Treasury bonds and notes.
//
// A Treasury is assumed risk-free: its theoretical price is the present value
// of the remaining coupons and par redemption on a risk-free discount curve,
// with no credit spread. Coupon dates are the maturity date rolled backward
// by 12/frequency months and are not adjusted for weekends or holidays.
//
// PriceAtYield is the separate street convention: cash flows are discounted
// in semi-annual (or other coupon-frequency) periods, with a compound
// fractional period. That quote matches a bond-equivalent yield; it is not
// the same number as curve discounting unless every coupon period is exactly
// 1/frequency years.
package bond

import (
	"errors"
	"fmt"
	"math"
	"time"

	"pricer/daycount"
	"pricer/discount"
	"pricer/pricing"
)

var (
	ErrInvalidFace      = errors.New("face must be positive")
	ErrInvalidCoupon    = errors.New("coupon rate must be non-negative")
	ErrInvalidFrequency = errors.New("coupon frequency must be a positive divisor of 12")
	ErrMatured          = errors.New("settlement must be strictly before maturity")
	ErrInvalidYield     = errors.New("yield must be greater than -frequency")
	ErrNoBracket        = errors.New("could not bracket a bond-equivalent yield")
)

// Treasury is a fixed-coupon note or bond that redeems at par on Maturity.
// CouponRate is the annual rate as a decimal (0.0425 is 4.25%). Frequency is
// the number of coupons per year; US notes and bonds use 2. A zero coupon
// (a STRIP or bill) has CouponRate 0 and does not use Frequency.
type Treasury struct {
	Face       float64
	CouponRate float64
	Maturity   time.Time
	Frequency  int
}

func (t Treasury) validate(settlement time.Time) (time.Time, time.Time, error) {
	if !(t.Face > 0) || math.IsNaN(t.Face) || math.IsInf(t.Face, 0) {
		return time.Time{}, time.Time{}, ErrInvalidFace
	}
	if t.CouponRate < 0 || math.IsNaN(t.CouponRate) || math.IsInf(t.CouponRate, 0) {
		return time.Time{}, time.Time{}, ErrInvalidCoupon
	}
	maturity := daycount.Normalize(t.Maturity)
	settlement = daycount.Normalize(settlement)
	if maturity.IsZero() || settlement.IsZero() {
		return time.Time{}, time.Time{}, fmt.Errorf("settlement and maturity dates are required")
	}
	if !maturity.After(settlement) {
		return time.Time{}, time.Time{}, ErrMatured
	}
	if t.CouponRate > 0 {
		if _, err := monthsPerPeriod(t.Frequency); err != nil {
			return time.Time{}, time.Time{}, err
		}
	}
	return settlement, maturity, nil
}

func monthsPerPeriod(frequency int) (int, error) {
	if frequency <= 0 || 12%frequency != 0 {
		return 0, ErrInvalidFrequency
	}
	return 12 / frequency, nil
}

// TheoreticalPrice discounts remaining contractual cash flows on curve.
// Accrued interest uses the Treasury Actual/Actual ICMA fraction of the
// regular coupon period and is subtracted only to form the clean quote.
func (t Treasury) TheoreticalPrice(asOf time.Time, curve discount.Curve) (pricing.Result, error) {
	settlement, maturity, err := t.validate(asOf)
	if err != nil {
		return pricing.Result{}, err
	}
	if curve == nil {
		return pricing.Result{}, fmt.Errorf("discount curve is required")
	}
	flows, accrued, err := t.cashFlows(settlement, maturity)
	if err != nil {
		return pricing.Result{}, err
	}
	priced := make([]pricing.Flow, len(flows))
	var dirty float64
	for i, cf := range flows {
		df, err := curve.DiscountFactor(settlement, cf.Date)
		if err != nil {
			return pricing.Result{}, err
		}
		if math.IsNaN(df) || math.IsInf(df, 0) || df <= 0 {
			return pricing.Result{}, fmt.Errorf("discount factor to %s is not usable", cf.Date.Format("2006-01-02"))
		}
		pv := cf.Amount * df
		priced[i] = pricing.Flow{
			Date:           cf.Date,
			Amount:         cf.Amount,
			Kind:           cf.Kind,
			DiscountFactor: df,
			PresentValue:   pv,
		}
		dirty += pv
	}
	return pricing.Result{
		Dirty:   dirty,
		Clean:   dirty - accrued,
		Accrued: accrued,
		Flows:   priced,
	}, nil
}

// PriceAtYield is the street-convention invoice price at a bond-equivalent
// yield compounded once per coupon period. ytm is an annual decimal.
func (t Treasury) PriceAtYield(settlement time.Time, ytm float64) (pricing.Result, error) {
	settlement, maturity, err := t.validate(settlement)
	if err != nil {
		return pricing.Result{}, err
	}
	if t.CouponRate == 0 {
		return t.zeroPriceAtYield(settlement, maturity, ytm)
	}
	if math.IsNaN(ytm) || math.IsInf(ytm, 0) || ytm <= float64(-t.Frequency) {
		return pricing.Result{}, ErrInvalidYield
	}
	step, err := monthsPerPeriod(t.Frequency)
	if err != nil {
		return pricing.Result{}, err
	}
	next, prev, err := enclosingPeriod(maturity, step, settlement)
	if err != nil {
		return pricing.Result{}, err
	}
	periodDays := daycount.Days(prev, next)
	if periodDays <= 0 {
		return pricing.Result{}, fmt.Errorf("coupon period %s to %s has no days", prev.Format("2006-01-02"), next.Format("2006-01-02"))
	}
	w := float64(daycount.Days(settlement, next)) / float64(periodDays)
	accrued := t.periodicCoupon() * float64(daycount.Days(prev, settlement)) / float64(periodDays)

	dates, err := couponDatesAfter(maturity, step, settlement)
	if err != nil {
		return pricing.Result{}, err
	}
	coupon := t.periodicCoupon()
	g := ytm / float64(t.Frequency)
	priced := make([]pricing.Flow, 0, len(dates)+1)
	var dirty float64
	for k, d := range dates {
		df := periodDF(g, float64(k)+w)
		pv := coupon * df
		priced = append(priced, pricing.Flow{
			Date: d, Amount: coupon, Kind: pricing.Coupon,
			DiscountFactor: df, PresentValue: pv,
		})
		dirty += pv
		if d.Equal(maturity) {
			pv = t.Face * df
			priced = append(priced, pricing.Flow{
				Date: d, Amount: t.Face, Kind: pricing.Principal,
				DiscountFactor: df, PresentValue: pv,
			})
			dirty += pv
		}
	}
	return pricing.Result{Dirty: dirty, Clean: dirty - accrued, Accrued: accrued, Flows: priced}, nil
}

// zeroPriceAtYield discounts par redemption on a bond basis. Frequency 0,
// the pure-discount case, uses the semi-annual bond basis.
func (t Treasury) zeroPriceAtYield(settlement, maturity time.Time, ytm float64) (pricing.Result, error) {
	freq := t.Frequency
	if freq == 0 {
		freq = 2
	}
	if _, err := monthsPerPeriod(freq); err != nil {
		return pricing.Result{}, err
	}
	if math.IsNaN(ytm) || math.IsInf(ytm, 0) || ytm <= float64(-freq) {
		return pricing.Result{}, ErrInvalidYield
	}
	yearFrac, err := daycount.ActualActualISDA.YearFraction(settlement, maturity)
	if err != nil {
		return pricing.Result{}, err
	}
	g := ytm / float64(freq)
	df := periodDF(g, yearFrac*float64(freq))
	pv := t.Face * df
	return pricing.Result{
		Dirty:   pv,
		Clean:   pv,
		Accrued: 0,
		Flows: []pricing.Flow{{
			Date: maturity, Amount: t.Face, Kind: pricing.Principal,
			DiscountFactor: df, PresentValue: pv,
		}},
	}, nil
}

// YieldFromDirty solves the street-convention bond-equivalent yield whose
// invoice price equals dirty. The result is an annual decimal.
func (t Treasury) YieldFromDirty(settlement time.Time, dirty float64) (float64, error) {
	if !(dirty > 0) || math.IsNaN(dirty) || math.IsInf(dirty, 0) {
		return 0, fmt.Errorf("dirty price must be positive")
	}
	freq := t.Frequency
	if freq == 0 {
		freq = 2
	}
	lo := -0.5 * float64(freq)
	hi := 0.5
	plo, err := t.PriceAtYield(settlement, lo)
	if err != nil {
		return 0, err
	}
	phi, err := t.PriceAtYield(settlement, hi)
	if err != nil {
		return 0, err
	}
	for plo.Dirty < dirty && lo > float64(-freq)+1e-6 {
		lo = (lo + float64(-freq)) / 2
		plo, err = t.PriceAtYield(settlement, lo)
		if err != nil {
			return 0, err
		}
	}
	for phi.Dirty > dirty && hi < 100 {
		hi = hi*2 + 0.01
		phi, err = t.PriceAtYield(settlement, hi)
		if err != nil {
			return 0, err
		}
	}
	if plo.Dirty < dirty || phi.Dirty > dirty {
		return 0, ErrNoBracket
	}
	tol := 1e-12 * t.Face
	for i := 0; i < 80; i++ {
		mid := (lo + hi) / 2
		pm, err := t.PriceAtYield(settlement, mid)
		if err != nil {
			return 0, err
		}
		if math.Abs(pm.Dirty-dirty) <= tol {
			return mid, nil
		}
		if pm.Dirty > dirty {
			lo = mid
		} else {
			hi = mid
		}
	}
	return (lo + hi) / 2, nil
}

type rawFlow struct {
	Date   time.Time
	Amount float64
	Kind   pricing.Kind
}

func (t Treasury) cashFlows(settlement, maturity time.Time) ([]rawFlow, float64, error) {
	if t.CouponRate == 0 {
		return []rawFlow{{Date: maturity, Amount: t.Face, Kind: pricing.Principal}}, 0, nil
	}
	step, err := monthsPerPeriod(t.Frequency)
	if err != nil {
		return nil, 0, err
	}
	dates, err := couponDatesAfter(maturity, step, settlement)
	if err != nil {
		return nil, 0, err
	}
	coupon := t.periodicCoupon()
	flows := make([]rawFlow, 0, len(dates)+1)
	for _, d := range dates {
		flows = append(flows, rawFlow{Date: d, Amount: coupon, Kind: pricing.Coupon})
		if d.Equal(maturity) {
			flows = append(flows, rawFlow{Date: d, Amount: t.Face, Kind: pricing.Principal})
		}
	}
	accrued, err := t.accrued(settlement, maturity, step)
	if err != nil {
		return nil, 0, err
	}
	return flows, accrued, nil
}

func (t Treasury) periodicCoupon() float64 {
	return t.Face * t.CouponRate / float64(t.Frequency)
}

func (t Treasury) accrued(settlement, maturity time.Time, step int) (float64, error) {
	next, prev, err := enclosingPeriod(maturity, step, settlement)
	if err != nil {
		return 0, err
	}
	periodDays := daycount.Days(prev, next)
	if periodDays <= 0 {
		return 0, fmt.Errorf("coupon period has no days")
	}
	return t.periodicCoupon() * float64(daycount.Days(prev, settlement)) / float64(periodDays), nil
}

func couponDatesAfter(maturity time.Time, stepMonths int, settlement time.Time) ([]time.Time, error) {
	var dates []time.Time
	for k := 0; ; k++ {
		d := addMonths(maturity, -k*stepMonths)
		if !d.After(settlement) {
			break
		}
		dates = append(dates, d)
		if k > 10000 {
			return nil, fmt.Errorf("coupon schedule did not reach settlement")
		}
	}
	if len(dates) == 0 {
		return nil, ErrMatured
	}
	for i, j := 0, len(dates)-1; i < j; i, j = i+1, j-1 {
		dates[i], dates[j] = dates[j], dates[i]
	}
	return dates, nil
}

func enclosingPeriod(maturity time.Time, stepMonths int, settlement time.Time) (next, prev time.Time, err error) {
	for k := 0; ; k++ {
		d := addMonths(maturity, -k*stepMonths)
		if !d.After(settlement) {
			if k == 0 {
				return time.Time{}, time.Time{}, ErrMatured
			}
			return addMonths(maturity, -(k-1)*stepMonths), d, nil
		}
		if k > 10000 {
			return time.Time{}, time.Time{}, fmt.Errorf("coupon schedule did not reach settlement")
		}
	}
}

func addMonths(t time.Time, months int) time.Time {
	t = daycount.Normalize(t)
	first := time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, months, 0)
	day := t.Day()
	dim := daysInMonth(first.Year(), first.Month())
	if day > dim {
		day = dim
	}
	return time.Date(first.Year(), first.Month(), day, 0, 0, 0, 0, time.UTC)
}

func daysInMonth(year int, month time.Month) int {
	return time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC).Day()
}

func periodDF(periodYield, periods float64) float64 {
	if periodYield == 0 {
		return 1
	}
	return math.Pow(1+periodYield, -periods)
}
