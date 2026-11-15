// Package pricing holds the shared result of a theoretical price.
package pricing

import (
	"time"

	"pricer/discount"
)

// Kind identifies why a cash flow is paid.
type Kind string

const (
	Coupon    Kind = "coupon"
	Principal Kind = "principal"
)

// Flow is one contractual payment and its present value.
type Flow struct {
	Date           time.Time
	Amount         float64
	Kind           Kind
	DiscountFactor float64
	PresentValue   float64
}

// Result is a theoretical price. Dirty is the invoice price: the present
// value of contractual cash flows after the valuation date. Clean is dirty
// minus accrued coupon, so it can be compared with a market quote.
type Result struct {
	Dirty   float64
	Clean   float64
	Accrued float64
	Flows   []Flow
}

// Instrument is a claim whose theoretical price is the risk-free present
// value of its contractual cash flows. Credit spreads are not applied here;
// a Treasury is priced directly on the risk-free curve.
type Instrument interface {
	TheoreticalPrice(asOf time.Time, curve discount.Curve) (Result, error)
}
