package main

import (
	"fmt"
	"math"

	"pricer/eln"
	"pricer/option"
)

const (
	groupContract = "contract"
	groupMarket   = "market"
	groupModel    = "model"

	defaultValuation = "2026-11-16"
	defaultExpiry    = "2027-11-16"
)

// curvePaths is the Monte Carlo path count for each point of a sensitivity
// curve. The seed is fixed, so the curve uses common random numbers.
const curvePaths = 10_000

// field describes one input control. Kind is number, percent (shown x100),
// date, or int. Min and Max, in display units, add a slider.
type field struct {
	Key   string   `json:"key"`
	Label string   `json:"label"`
	Kind  string   `json:"kind"`
	Group string   `json:"group"`
	Step  float64  `json:"step,omitempty"`
	Min   *float64 `json:"min,omitempty"`
	Max   *float64 `json:"max,omitempty"`
	Help  string   `json:"help,omitempty"`
}

func (f field) slider(min, max float64) field {
	f.Min, f.Max = &min, &max
	return f
}

func (f field) help(s string) field {
	f.Help = s
	return f
}

func num(key, label, group string, step float64) field {
	return field{Key: key, Label: label, Kind: "number", Group: group, Step: step}
}

func pct(key, label, group string, step float64) field {
	return field{Key: key, Label: label, Kind: "percent", Group: group, Step: step}
}

func date(key, label, group string) field {
	return field{Key: key, Label: label, Kind: "date", Group: group}
}

type product struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Basket      bool    `json:"basket"`
	Fields      []field `json:"fields"`
	Defaults    request `json:"defaults"`

	price       func(env) (response, error)
	payoff      func(e env, spot float64) float64
	payoffLabel string
	markers     func(env) []marker
}

var commonMarket = []field{
	pct("vol", "Volatility", groupMarket, 0.5).slider(5, 80),
	pct("rate", "Risk-free rate", groupMarket, 0.25).slider(0, 10),
	pct("dividend", "Dividend yield", groupMarket, 0.25),
	date("valuation", "Valuation date", groupMarket),
}

var basketMarket = []field{
	num("correlation", "Correlation", groupMarket, 0.05).slider(-0.5, 1),
	pct("rate", "Risk-free rate", groupMarket, 0.25).slider(0, 10),
	date("valuation", "Valuation date", groupMarket),
	{Key: "paths", Label: "Monte Carlo paths", Kind: "int", Group: groupModel, Step: 10000,
		Help: "Each path is paired with an antithetic path. More paths are slower and less noisy."},
}

func fields(parts ...[]field) []field {
	var out []field
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func baseRequest(id string) request {
	return request{
		Product: id, Spot: 100, Strike: 100, Vol: 0.2, Rate: 0.04, Quantity: 1,
		Expiry: defaultExpiry, Valuation: defaultValuation, Seed: 1,
	}
}

func basketRequest(id string) request {
	r := baseRequest(id)
	r.Strike, r.Notional, r.Correlation, r.Paths = 1, 100, 0.5, option.DefaultPaths
	r.Underliers = []underlierReq{
		{Name: "#1", Spot: 100, Initial: 100, Vol: 0.20},
		{Name: "#2", Spot: 100, Initial: 100, Vol: 0.25},
	}
	return r
}

func products() []product {
	var out []product

	put := baseRequest("put")
	out = append(out, product{
		ID: "put", Name: "European put",
		Description: "Right to sell at the strike on expiry. Black-Scholes-Merton.",
		Fields: fields([]field{
			num("spot", "Spot price", groupContract, 1).slider(20, 200),
			num("strike", "Put strike", groupContract, 1).slider(20, 200),
			date("expiry", "Expiry date", groupContract),
			num("quantity", "Quantity", groupContract, 1),
		}, commonMarket),
		Defaults: put,
		price: vanilla("European put", "Black-Scholes-Merton", false, func(e env) (option.Quote, error) {
			r := e.req
			return option.EuropeanPut{Spot: r.Spot, Strike: r.Strike, Expiry: e.expiry,
				Volatility: r.Vol, DividendYield: r.Dividend, Quantity: r.Quantity, DayCount: e.dc}.Price(e.valuation, e.curve)
		}),
		payoff:      func(e env, s float64) float64 { return e.req.Quantity * math.Max(e.req.Strike-s, 0) },
		payoffLabel: "Payoff at expiry",
		markers:     strikeMarkers,
	})

	dip := baseRequest("down-in-put")
	dip.Barrier = 90
	out = append(out, product{
		ID: "down-in-put", Name: "Down-and-in put",
		Description: "A put that only exists if spot touches the barrier from above. Continuous barrier, no rebate.",
		Fields: fields([]field{
			num("spot", "Spot price", groupContract, 1).slider(20, 200),
			num("strike", "Put strike", groupContract, 1).slider(20, 200),
			num("barrier", "Barrier", groupContract, 1).slider(20, 200).help("Knock-in level; the put activates if spot touches it."),
			date("expiry", "Expiry date", groupContract),
			num("quantity", "Quantity", groupContract, 1),
		}, commonMarket),
		Defaults: dip,
		price: vanilla("European down-and-in put", "Black-Scholes-Merton, continuous barrier", true, func(e env) (option.Quote, error) {
			r := e.req
			return option.DownInPut{Spot: r.Spot, Strike: r.Strike, Barrier: r.Barrier, Expiry: e.expiry,
				Volatility: r.Vol, DividendYield: r.Dividend, Quantity: r.Quantity, DayCount: e.dc}.Price(e.valuation, e.curve)
		}),
		payoff:      func(e env, s float64) float64 { return e.req.Quantity * math.Max(e.req.Strike-s, 0) },
		payoffLabel: "Payoff if knocked in",
		markers:     strikeBarrierMarkers,
	})

	call := baseRequest("call")
	out = append(out, product{
		ID: "call", Name: "European call",
		Description: "Right to buy at the strike on expiry. Black-Scholes-Merton.",
		Fields: fields([]field{
			num("spot", "Spot price", groupContract, 1).slider(20, 200),
			num("strike", "Call strike", groupContract, 1).slider(20, 200),
			date("expiry", "Expiry date", groupContract),
			num("quantity", "Quantity", groupContract, 1),
		}, commonMarket),
		Defaults: call,
		price: vanilla("European call", "Black-Scholes-Merton", false, func(e env) (option.Quote, error) {
			r := e.req
			return option.EuropeanCall{Spot: r.Spot, Strike: r.Strike, Expiry: e.expiry,
				Volatility: r.Vol, DividendYield: r.Dividend, Quantity: r.Quantity, DayCount: e.dc}.Price(e.valuation, e.curve)
		}),
		payoff:      func(e env, s float64) float64 { return e.req.Quantity * math.Max(s-e.req.Strike, 0) },
		payoffLabel: "Payoff at expiry",
		markers:     strikeMarkers,
	})

	dput := baseRequest("digital-put")
	dput.Cash = 1
	out = append(out, product{
		ID: "digital-put", Name: "Digital put",
		Description: "Pays a fixed cash amount if spot finishes below the strike.",
		Fields: fields([]field{
			num("spot", "Spot price", groupContract, 1).slider(20, 200),
			num("strike", "Put strike", groupContract, 1).slider(20, 200),
			num("cash", "Cash payout", groupContract, 1),
			date("expiry", "Expiry date", groupContract),
			num("quantity", "Quantity", groupContract, 1),
		}, commonMarket),
		Defaults: dput,
		price: vanilla("European cash-or-nothing digital put", "Black-Scholes-Merton", false, func(e env) (option.Quote, error) {
			r := e.req
			return option.DigitalPut{Spot: r.Spot, Strike: r.Strike, Expiry: e.expiry,
				Volatility: r.Vol, DividendYield: r.Dividend, Cash: r.Cash, Quantity: r.Quantity, DayCount: e.dc}.Price(e.valuation, e.curve)
		}),
		payoff: func(e env, s float64) float64 {
			if s < e.req.Strike {
				return e.req.Quantity * e.req.Cash
			}
			return 0
		},
		payoffLabel: "Payoff at expiry",
		markers:     strikeMarkers,
	})

	dcall := baseRequest("digital-call")
	dcall.Cash = 1
	out = append(out, product{
		ID: "digital-call", Name: "Digital call",
		Description: "Pays a fixed cash amount if spot finishes above the strike.",
		Fields: fields([]field{
			num("spot", "Spot price", groupContract, 1).slider(20, 200),
			num("strike", "Call strike", groupContract, 1).slider(20, 200),
			num("cash", "Cash payout", groupContract, 1),
			date("expiry", "Expiry date", groupContract),
			num("quantity", "Quantity", groupContract, 1),
		}, commonMarket),
		Defaults: dcall,
		price: vanilla("European cash-or-nothing digital call", "Black-Scholes-Merton", false, func(e env) (option.Quote, error) {
			r := e.req
			return option.DigitalCall{Spot: r.Spot, Strike: r.Strike, Expiry: e.expiry,
				Volatility: r.Vol, DividendYield: r.Dividend, Cash: r.Cash, Quantity: r.Quantity, DayCount: e.dc}.Price(e.valuation, e.curve)
		}),
		payoff: func(e env, s float64) float64 {
			if s > e.req.Strike {
				return e.req.Quantity * e.req.Cash
			}
			return 0
		},
		payoffLabel: "Payoff at expiry",
		markers:     strikeMarkers,
	})

	en := baseRequest("eln")
	en.Strike, en.Notional, en.Expiry, en.VolBump, en.Markup = 95, 100, "2027-02-16", 0.01, 0.005
	out = append(out, product{
		ID: "eln", Name: "Equity-linked note",
		Description: "Issuer view of a note redeeming at notional if spot ends at or above the strike, otherwise notional x spot / strike.",
		Fields: fields([]field{
			num("notional", "Notional", groupContract, 10),
			num("spot", "Spot price", groupContract, 1).slider(20, 200),
			num("strike", "Strike", groupContract, 1).slider(20, 200),
			date("expiry", "Expiry date", groupContract),
			pct("volBump", "Vol bump", groupContract, 0.25).slider(0, 5).help("Vol points haircut from the put credited to the investor."),
			pct("markup", "Markup (of notional)", groupContract, 0.05).slider(0, 3),
		}, commonMarket),
		Defaults: en,
		price:    vanillaELN,
		payoff: func(e env, s float64) float64 {
			return e.req.Notional * math.Min(1, s/e.req.Strike)
		},
		payoffLabel: "Redemption at expiry",
		markers:     strikeMarkers,
	})

	wp := basketRequest("worst-of-put")
	out = append(out, product{
		ID: "worst-of-put", Name: "Worst-of put",
		Description: "Put on the worst performer of a basket, priced by Monte Carlo.",
		Basket:      true,
		Fields: fields([]field{
			pct("strike", "Put strike (% of initial)", groupContract, 1).slider(50, 150),
			num("notional", "Notional", groupContract, 10),
			date("expiry", "Expiry date", groupContract),
		}, basketMarket),
		Defaults: wp,
		price:    worstOfPut,
	})

	wdip := basketRequest("worst-of-down-in-put")
	wdip.Barrier, wdip.Paths = 0.7, 50_000
	out = append(out, product{
		ID: "worst-of-down-in-put", Name: "Worst-of down-and-in put",
		Description: "Worst-of put that activates if any underlier touches the barrier. Monte Carlo with a Brownian bridge.",
		Basket:      true,
		Fields: fields([]field{
			pct("strike", "Put strike (% of initial)", groupContract, 1).slider(50, 150),
			pct("barrier", "Barrier (% of initial)", groupContract, 1).slider(30, 120),
			num("notional", "Notional", groupContract, 10),
			date("expiry", "Expiry date", groupContract),
		}, basketMarket),
		Defaults: wdip,
		price:    worstOfDownInPut,
	})

	we := basketRequest("worst-of-eln")
	we.Strike, we.Expiry, we.VolBump, we.Markup = 0.95, "2027-02-16", 0.01, 0.005
	out = append(out, product{
		ID: "worst-of-eln", Name: "Worst-of ELN",
		Description: "Issuer view of a note on a basket: par unless the worst performer ends below the strike.",
		Basket:      true,
		Fields: fields([]field{
			pct("strike", "Strike (% of initial)", groupContract, 1).slider(50, 150),
			num("notional", "Notional", groupContract, 10),
			date("expiry", "Expiry date", groupContract),
			pct("volBump", "Vol bump", groupContract, 0.25).slider(0, 5),
			pct("markup", "Markup (of notional)", groupContract, 0.05).slider(0, 3),
		}, basketMarket),
		Defaults: we,
		price:    worstOfELN,
	})
	return out
}

func strikeMarkers(e env) []marker {
	return []marker{{Label: "Strike", X: e.req.Strike}}
}

func strikeBarrierMarkers(e env) []marker {
	return []marker{{Label: "Strike", X: e.req.Strike}, {Label: "Barrier", X: e.req.Barrier}}
}

// curve reprices over a range of spot levels.
func (p product) curve(e env) (*curve, error) {
	c := &curve{PriceLabel: "Price today", PayoffLabel: p.payoffLabel}
	steps, lo, hi, paths := 40, 0.5, 1.5, e.paths
	if p.Basket {
		steps, lo, hi = 16, 0.6, 1.4
		paths = min(e.paths, curvePaths)
		c.XLabel = "All underliers, % of today's level"
		c.Current = 100
	} else {
		c.XLabel = "Spot price"
		c.Current = e.req.Spot
	}
	for i := 0; i <= steps; i++ {
		m := lo + (hi-lo)*float64(i)/float64(steps)
		resp, err := p.price(e.scaled(m, paths))
		if err != nil {
			return nil, err
		}
		pt := point{Price: resp.Headline[0].Value}
		if p.Basket {
			pt.X = m * 100
		} else {
			pt.X = e.req.Spot * m
			if p.payoff != nil {
				v := p.payoff(e, pt.X)
				pt.Payoff = &v
			}
		}
		c.Points = append(c.Points, pt)
	}
	if p.markers != nil && !p.Basket {
		c.Markers = p.markers(e)
	}
	return c, nil
}

func vanilla(title, model string, barrier bool, quote func(env) (option.Quote, error)) func(env) (response, error) {
	return func(e env) (response, error) {
		q, err := quote(e)
		if err != nil {
			return response{}, err
		}
		r := e.req
		resp := response{
			Title: title, Model: model,
			Headline: []metric{
				money("Premium", q.Premium),
				money("Exercise value", q.ExerciseValue),
				money("Time value", q.TimeValue),
				number("Delta", q.Delta),
			},
			Details: []metric{
				money("Forward", q.Forward),
				number("Discount factor", q.DiscountFactor),
				number("Year fraction", q.YearFraction),
				number("d1", q.D1),
				number("d2", q.D2),
			},
			Greeks: []metric{
				number("Delta", q.Delta),
				number("Gamma", q.Gamma),
				money("Vega (per 1% vol)", q.Vega/100),
				money("Theta (per day)", q.Theta/365),
				money("Rho (per 1% rate)", q.Rho/100),
			},
		}
		if barrier {
			resp.Status = "Not knocked in"
			if r.Spot <= r.Barrier {
				resp.Status = "Knocked in"
			}
		}
		return resp, nil
	}
}

func vanillaELN(e env) (response, error) {
	r := e.req
	q, err := eln.Vanilla{
		Notional: r.Notional, Spot: r.Spot, Strike: r.Strike, Expiry: e.expiry,
		Volatility: r.Vol, DividendYield: r.Dividend, DayCount: e.dc, VolBump: r.VolBump, Markup: r.Markup,
	}.Price(e.valuation, e.curve)
	if err != nil {
		return response{}, err
	}
	return response{
		Title: "Issuer price of a vanilla equity-linked note",
		Model: "Discounted notional less Notional/Strike European puts",
		Headline: []metric{
			money("Issue price", q.IssuePrice).note(per100(q.IssuePrice, r.Notional)),
			money("Theoretical value (mid)", q.TheoreticalValue).note(per100(q.TheoreticalValue, r.Notional)),
			money("Day-1 margin", q.Day1Margin).note(per100(q.Day1Margin, r.Notional)),
			percent("Maximum yield", q.MaxYield),
		},
		Details: []metric{
			money("Embedded put (mid vol)", q.PutPremium),
			money("Embedded put (haircut vol)", q.BumpedPutPremium),
			money("Cost of vol bump", q.CostOfBump),
			money("Markup", q.Markup),
			number("Puts per note", q.PutQuantity),
			number("Discount factor", q.DiscountFactor),
			number("Year fraction", q.YearFraction),
		},
		Greeks: []metric{
			number("Investor delta (shares)", q.InvestorDelta),
			number("Issuer hedge (buy shares)", q.HedgeShares),
		},
		Notes: []string{
			"Issue price = mid-market value + cost of the vol bump + markup.",
			"Maximum yield assumes redemption at notional on an Actual/365 basis.",
		},
	}, nil
}

func per100(amount, notional float64) string {
	if notional == 0 {
		return ""
	}
	return fmt.Sprintf("%.4f per 100", amount/notional*100)
}

func underlierTable(unds []option.Underlier, extra []string, cols ...[]float64) *table {
	t := &table{Columns: append([]string{"Underlier", "Spot", "Initial", "Vol", "Dividend"}, extra...)}
	for i, u := range unds {
		initial := u.Initial
		if initial == 0 {
			initial = u.Spot
		}
		row := []string{u.Name, fmt.Sprintf("%.2f", u.Spot), fmt.Sprintf("%.2f", initial),
			fmt.Sprintf("%.2f%%", u.Volatility*100), fmt.Sprintf("%.2f%%", u.DividendYield*100)}
		for _, c := range cols {
			row = append(row, fmt.Sprintf("%.4f", c[i]))
		}
		t.Rows = append(t.Rows, row)
	}
	return t
}

func worstOfPut(e env) (response, error) {
	r := e.req
	unds := e.underliers()
	q, err := option.WorstOfPut{
		Underliers: unds, Correlation: e.correlation(), Strike: r.Strike, Expiry: e.expiry,
		Notional: r.Notional, DayCount: e.dc, Paths: e.paths, Seed: r.Seed,
	}.Price(e.valuation, e.curve)
	if err != nil {
		return response{}, err
	}
	return response{
		Title: "European worst-of put",
		Model: fmt.Sprintf("Monte Carlo, correlated GBM, %d paths (x2 antithetic)", q.Paths),
		Headline: []metric{
			money("Premium", q.Premium).note(per100(q.Premium, r.Notional)),
			money("Std error", q.StdErr),
			money("Exercise value", q.ExerciseValue),
			money("Time value", q.TimeValue),
		},
		Details: []metric{
			number("Discount factor", q.DiscountFactor),
			number("Year fraction", q.YearFraction),
		},
		Table: underlierTable(unds, []string{"P(worst)", "Delta", "Vega (1% vol)"},
			q.WorstProb, q.Deltas, scale(q.Vegas, 0.01)),
	}, nil
}

func worstOfDownInPut(e env) (response, error) {
	r := e.req
	unds := e.underliers()
	q, err := option.WorstOfDownInPut{
		Underliers: unds, Correlation: e.correlation(), Strike: r.Strike, Barrier: r.Barrier,
		Expiry: e.expiry, Notional: r.Notional, DayCount: e.dc, Paths: e.paths, Seed: r.Seed,
	}.Price(e.valuation, e.curve)
	if err != nil {
		return response{}, err
	}
	status := "Not knocked in"
	for _, u := range unds {
		initial := u.Initial
		if initial == 0 {
			initial = u.Spot
		}
		if u.Spot/initial <= r.Barrier {
			status = "Knocked in"
		}
	}
	model := fmt.Sprintf("Monte Carlo, correlated GBM, Brownian-bridge barrier, %d paths (x2 antithetic)", q.Paths)
	if len(unds) == 1 {
		model = "Black-Scholes-Merton, continuous barrier"
	}
	resp := response{
		Title: "European worst-of down-and-in put", Model: model, Status: status,
		Headline: []metric{
			money("Premium", q.Premium).note(per100(q.Premium, r.Notional)),
			percent("Knock-in probability", q.KnockInProb),
			money("Exercise value", q.ExerciseValue),
			money("Time value", q.TimeValue),
		},
		Details: []metric{
			number("Discount factor", q.DiscountFactor),
			number("Year fraction", q.YearFraction),
		},
		Table: underlierTable(unds, []string{"P(worst)", "Delta", "Vega (1% vol)"},
			q.WorstProb, q.Deltas, scale(q.Vegas, 0.01)),
	}
	if q.StdErr > 0 {
		resp.Details = append([]metric{money("Std error", q.StdErr)}, resp.Details...)
	}
	return resp, nil
}

func worstOfELN(e env) (response, error) {
	r := e.req
	unds := e.underliers()
	q, err := eln.WorstOf{
		Notional: r.Notional, Underliers: unds, Correlation: e.correlation(), Strike: r.Strike,
		Expiry: e.expiry, DayCount: e.dc, VolBump: r.VolBump, Markup: r.Markup, Paths: e.paths, Seed: r.Seed,
	}.Price(e.valuation, e.curve)
	if err != nil {
		return response{}, err
	}
	return response{
		Title: "Issuer price of a worst-of equity-linked note",
		Model: fmt.Sprintf("Discounted notional less a worst-of put, Monte Carlo, %d paths (x2 antithetic)", q.Paths),
		Headline: []metric{
			money("Issue price", q.IssuePrice).note(per100(q.IssuePrice, r.Notional)),
			money("Theoretical value (mid)", q.TheoreticalValue).note(per100(q.TheoreticalValue, r.Notional)),
			money("Day-1 margin", q.Day1Margin).note(per100(q.Day1Margin, r.Notional)),
			percent("Maximum yield", q.MaxYield),
		},
		Details: []metric{
			money("Issue price std error", q.IssueStdErr),
			money("Theoretical std error", q.TheoreticalStdErr),
			money("Embedded put (mid vol)", q.PutPremium),
			money("Embedded put (haircut vol)", q.BumpedPutPremium),
			money("Cost of vol bump", q.CostOfBump),
			money("Markup", q.Markup),
			money("Put notional", q.PutNotional),
			number("Discount factor", q.DiscountFactor),
			number("Year fraction", q.YearFraction),
		},
		Table: underlierTable(unds, []string{"P(worst)", "Investor delta", "Issuer hedge (buy)"},
			q.WorstProb, q.InvestorDeltas, q.HedgeShares),
		Notes: []string{
			"Issue price = mid-market value + cost of the vol bump + markup.",
			"Maximum yield assumes redemption at notional on an Actual/365 basis.",
		},
	}, nil
}

func scale(xs []float64, k float64) []float64 {
	out := make([]float64, len(xs))
	for i, x := range xs {
		out[i] = x * k
	}
	return out
}
