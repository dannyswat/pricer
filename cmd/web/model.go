package main

import (
	"fmt"
	"time"

	"pricer/daycount"
	"pricer/discount"
	"pricer/option"
)

// maxPaths caps Monte Carlo work per request.
const maxPaths = 1_000_000

type underlierReq struct {
	Name     string  `json:"name"`
	Spot     float64 `json:"spot"`
	Initial  float64 `json:"initial"`
	Vol      float64 `json:"vol"`
	Dividend float64 `json:"dividend"`
}

// request holds every input any product can use. Unused fields are ignored.
// Rates, volatilities, dividends, markup and basket strikes/barriers are
// decimals (0.2 = 20%).
type request struct {
	Product     string         `json:"product"`
	Spot        float64        `json:"spot"`
	Strike      float64        `json:"strike"`
	Barrier     float64        `json:"barrier"`
	Vol         float64        `json:"vol"`
	Dividend    float64        `json:"dividend"`
	Rate        float64        `json:"rate"`
	Quantity    float64        `json:"quantity"`
	Cash        float64        `json:"cash"`
	Notional    float64        `json:"notional"`
	VolBump     float64        `json:"volBump"`
	Markup      float64        `json:"markup"`
	Correlation float64        `json:"correlation"`
	Expiry      string         `json:"expiry"`
	Valuation   string         `json:"valuation"`
	Paths       int            `json:"paths"`
	Seed        uint64         `json:"seed"`
	Underliers  []underlierReq `json:"underliers"`
}

func (r request) clone() request {
	r.Underliers = append([]underlierReq(nil), r.Underliers...)
	return r
}

// env is a validated request ready to price.
type env struct {
	req       request
	valuation time.Time
	expiry    time.Time
	curve     discount.Curve
	curveText string
	dc        daycount.Convention
	paths     int
}

func newEnv(r request) (env, error) {
	valuation, err := parseDate(r.Valuation)
	if err != nil {
		return env{}, fmt.Errorf("valuation: %w", err)
	}
	expiry, err := parseDate(r.Expiry)
	if err != nil {
		return env{}, fmt.Errorf("expiry: %w", err)
	}
	if r.Paths > maxPaths {
		return env{}, fmt.Errorf("paths must be at most %d", maxPaths)
	}
	paths := r.Paths
	if paths <= 0 {
		paths = option.DefaultPaths
	}
	dc := daycount.Actual365Fixed
	curve := discount.Flat{Rate: r.Rate, DayCount: dc}
	return env{
		req: r, valuation: valuation, expiry: expiry,
		curve: curve, curveText: curve.Describe(), dc: dc, paths: paths,
	}, nil
}

// scaled returns the environment with every spot multiplied by m. Basket
// initial levels are pinned first so strikes and barriers stay put.
func (e env) scaled(m float64, paths int) env {
	r := e.req.clone()
	r.Spot *= m
	for i := range r.Underliers {
		u := &r.Underliers[i]
		if u.Initial == 0 {
			u.Initial = u.Spot
		}
		u.Spot *= m
	}
	e.req = r
	e.paths = paths
	return e
}

func (e env) underliers() []option.Underlier {
	out := make([]option.Underlier, len(e.req.Underliers))
	for i, u := range e.req.Underliers {
		name := u.Name
		if name == "" {
			name = fmt.Sprintf("#%d", i+1)
		}
		out[i] = option.Underlier{Name: name, Spot: u.Spot, Initial: u.Initial, Volatility: u.Vol, DividendYield: u.Dividend}
	}
	return out
}

func (e env) correlation() [][]float64 {
	n := len(e.req.Underliers)
	m := make([][]float64, n)
	for i := range m {
		m[i] = make([]float64, n)
		for j := range m[i] {
			if i == j {
				m[i][j] = 1
			} else {
				m[i][j] = e.req.Correlation
			}
		}
	}
	return m
}

func parseDate(s string) (time.Time, error) {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return time.Time{}, fmt.Errorf("want YYYY-MM-DD, got %q", s)
	}
	return daycount.Normalize(t), nil
}

// metric is one labelled number. Format is money, percent (value is a
// fraction), or number.
type metric struct {
	Label  string  `json:"label"`
	Value  float64 `json:"value"`
	Format string  `json:"format"`
	Note   string  `json:"note,omitempty"`
}

func money(label string, v float64) metric { return metric{Label: label, Value: v, Format: "money"} }
func percent(label string, v float64) metric {
	return metric{Label: label, Value: v, Format: "percent"}
}
func number(label string, v float64) metric { return metric{Label: label, Value: v, Format: "number"} }

func (m metric) note(s string) metric {
	m.Note = s
	return m
}

type table struct {
	Columns []string   `json:"columns"`
	Rows    [][]string `json:"rows"`
}

type marker struct {
	Label string  `json:"label"`
	X     float64 `json:"x"`
}

type point struct {
	X      float64  `json:"x"`
	Price  float64  `json:"price"`
	Payoff *float64 `json:"payoff,omitempty"`
}

type curve struct {
	XLabel      string   `json:"xLabel"`
	PriceLabel  string   `json:"priceLabel"`
	PayoffLabel string   `json:"payoffLabel,omitempty"`
	Current     float64  `json:"current"`
	Points      []point  `json:"points"`
	Markers     []marker `json:"markers,omitempty"`
}

// response is the priced product. Headline[0] is the price that the
// sensitivity curve plots.
type response struct {
	Product  string   `json:"product"`
	Title    string   `json:"title"`
	Model    string   `json:"model"`
	Status   string   `json:"status,omitempty"`
	Headline []metric `json:"headline"`
	Details  []metric `json:"details"`
	Greeks   []metric `json:"greeks,omitempty"`
	Table    *table   `json:"table,omitempty"`
	Notes    []string `json:"notes,omitempty"`
	Curve    *curve   `json:"curve,omitempty"`
}
