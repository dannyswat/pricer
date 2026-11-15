package main

import (
	"flag"
	"fmt"
	"os"
	"text/tabwriter"

	"pricer/daycount"
	"pricer/option"
)

func runWorstOfDownInPut(args []string) error {
	fs := flag.NewFlagSet("pricer worst-of-down-in-put", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var unds underlierFlags
	fs.Var(&unds, "underlier", "repeatable SPOT:VOL[:DIVIDEND[:INITIAL]]; INITIAL defaults to SPOT (default two: 100:0.2 and 100:0.25)")
	correlation := fs.String("correlation", "0.5", "pairwise correlation (0.5) or matrix with ';' between rows (1,0.5;0.5,1)")
	strike := fs.Float64("strike", 1, "put strike as a fraction of each initial level (1 = 100%)")
	barrier := fs.Float64("barrier", 0.7, "down barrier as a fraction of each initial level; any touch knocks the put in")
	notional := fs.Float64("notional", 100, "notional; payoff is notional × max(strike − worst performance, 0) if knocked in")
	expiryFlag := fs.String("expiry", "2027-11-16", "expiry date (YYYY-MM-DD); European exercise")
	valuationFlag := fs.String("valuation", "2026-11-16", "valuation date (YYYY-MM-DD)")
	paths := fs.Int("paths", option.DefaultPaths, "Monte Carlo paths when there is no closed form (each paired with an antithetic path)")
	seed := fs.Uint64("seed", 1, "random seed; the same seed gives the same price")
	rate := fs.Float64("rate", 0.04, "flat risk-free rate as a decimal, used when no -pillar is set")
	compounding := fs.String("compounding", "continuous", "continuous, annual, or semiannual")
	dayCountName := fs.String("daycount", "actual/365", "variance and dividend year fraction: actual/365 or actual/actual")
	var pillars pillarFlags
	fs.Var(&pillars, "pillar", "repeatable continuous zero pillar DATE=RATE (overrides -rate)")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `Price a European worst-of down-and-in put.

The put knocks in if any underlier's performance touches the barrier. If it
has knocked in by expiry, it pays notional × max(strike − worst performance, 0).
The barrier is monitored continuously and there is no rebate. One underlier
uses the closed-form down-and-in put. A basket is priced by Monte Carlo, with
a Brownian bridge so crossings between the endpoints are not missed.

Usage:
  pricer worst-of-down-in-put [flags]

`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}
	if len(unds) == 0 {
		unds = underlierFlags{
			{Name: "#1", Spot: 100, Volatility: 0.2},
			{Name: "#2", Spot: 100, Volatility: 0.25},
		}
	}
	expiry, err := parseDate(*expiryFlag)
	if err != nil {
		return fmt.Errorf("expiry: %w", err)
	}
	valuation, err := parseDate(*valuationFlag)
	if err != nil {
		return fmt.Errorf("valuation: %w", err)
	}
	dc, err := daycount.Parse(*dayCountName)
	if err != nil {
		return err
	}
	curve, curveLabel, err := buildCurve(valuation, *rate, *compounding, dc, pillars)
	if err != nil {
		return err
	}
	corr, err := parseCorrelation(*correlation, len(unds))
	if err != nil {
		return err
	}
	opt := option.WorstOfDownInPut{
		Underliers: unds, Correlation: corr, Strike: *strike, Barrier: *barrier,
		Expiry: expiry, Notional: *notional, DayCount: dc, Paths: *paths, Seed: *seed,
	}
	quote, err := opt.Price(valuation, curve)
	if err != nil {
		return err
	}
	return writeWorstOfDownIn(opt, curveLabel, quote, *seed)
}

func writeWorstOfDownIn(opt option.WorstOfDownInPut, curveLabel string, quote option.WorstOfDownInQuote, seed uint64) error {
	in := false
	for _, u := range opt.Underliers {
		initial := u.Initial
		if initial == 0 {
			initial = u.Spot
		}
		if u.Spot/initial <= opt.Barrier {
			in = true
		}
	}
	status := "not knocked in"
	if in {
		status = "knocked in"
	}
	model := "Monte Carlo, correlated GBM, Brownian-bridge barrier"
	if len(opt.Underliers) == 1 {
		model = "Black-Scholes-Merton, continuous barrier"
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "Theoretical price of a European worst-of down-and-in put")
	fmt.Fprintln(w)
	fmt.Fprintf(w, "Model\t%s\n", model)
	fmt.Fprintf(w, "Rebate\tnone\n")
	if quote.Paths > 0 {
		fmt.Fprintf(w, "Paths\t%d (x2 antithetic), seed %d\n", quote.Paths, seed)
	}
	fmt.Fprintf(w, "Strike\t%.6f%% of initial\n", opt.Strike*100)
	fmt.Fprintf(w, "Barrier\t%.6f%% of initial\n", opt.Barrier*100)
	fmt.Fprintf(w, "Status\t%s\n", status)
	fmt.Fprintf(w, "Notional\t%s\n", formatMoney(opt.Notional))
	fmt.Fprintf(w, "Expiry\t%s\n", opt.Expiry.Format("2006-01-02"))
	fmt.Fprintf(w, "Year fraction\t%.8f (%s)\n", quote.YearFraction, opt.DayCount)
	fmt.Fprintf(w, "Discount curve\t%s\n", curveLabel)
	fmt.Fprintf(w, "Discount factor\t%.8f\n", quote.DiscountFactor)
	fmt.Fprintln(w)
	fmt.Fprintf(w, "Premium\t%s\n", formatMoney(quote.Premium))
	if quote.StdErr > 0 {
		fmt.Fprintf(w, "Std error\t%s\n", formatMoney(quote.StdErr))
	}
	fmt.Fprintf(w, "Knock-in probability\t%.6f\n", quote.KnockInProb)
	fmt.Fprintf(w, "Exercise value\t%s\n", formatMoney(quote.ExerciseValue))
	fmt.Fprintf(w, "Time value\t%s\n", formatMoney(quote.TimeValue))
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Underlier\tSpot\tInitial\tVol\tDividend\tP(worst)\tDelta\tVega (per 1% vol)")
	for i, u := range opt.Underliers {
		initial := u.Initial
		if initial == 0 {
			initial = u.Spot
		}
		name := u.Name
		if name == "" {
			name = fmt.Sprintf("#%d", i+1)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%.4f%%\t%.4f%%\t%.4f\t%s\t%s\n",
			name, formatMoney(u.Spot), formatMoney(initial), u.Volatility*100, u.DividendYield*100,
			quote.WorstProb[i], formatMoney(quote.Deltas[i]), formatMoney(quote.Vegas[i]/100))
	}
	return w.Flush()
}
