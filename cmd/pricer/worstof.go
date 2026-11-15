package main

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"

	"pricer/daycount"
	"pricer/option"
)

type underlierFlags []option.Underlier

func (u *underlierFlags) String() string { return "" }

// Set parses SPOT:VOL[:DIVIDEND[:INITIAL]].
func (u *underlierFlags) Set(s string) error {
	parts := strings.Split(s, ":")
	if len(parts) < 2 || len(parts) > 4 {
		return fmt.Errorf("underlier %q must be SPOT:VOL[:DIVIDEND[:INITIAL]]", s)
	}
	vals := make([]float64, len(parts))
	for i, part := range parts {
		v, err := strconv.ParseFloat(strings.TrimSpace(part), 64)
		if err != nil {
			return fmt.Errorf("underlier %q: %w", s, err)
		}
		vals[i] = v
	}
	und := option.Underlier{Name: fmt.Sprintf("#%d", len(*u)+1), Spot: vals[0], Volatility: vals[1]}
	if len(vals) > 2 {
		und.DividendYield = vals[2]
	}
	if len(vals) > 3 {
		und.Initial = vals[3]
	}
	*u = append(*u, und)
	return nil
}

// parseCorrelation accepts a single pairwise value ("0.5") or a full matrix
// with rows separated by ";" and entries by "," ("1,0.5;0.5,1").
func parseCorrelation(s string, n int) ([][]float64, error) {
	s = strings.TrimSpace(s)
	matrix := make([][]float64, n)
	for i := range matrix {
		matrix[i] = make([]float64, n)
	}
	if !strings.ContainsAny(s, ",;") {
		rho, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return nil, fmt.Errorf("correlation %q: %w", s, err)
		}
		for i := range matrix {
			for j := range matrix[i] {
				matrix[i][j] = rho
			}
			matrix[i][i] = 1
		}
		return matrix, nil
	}
	rows := strings.Split(s, ";")
	if len(rows) != n {
		return nil, fmt.Errorf("correlation matrix has %d rows, want %d", len(rows), n)
	}
	for i, row := range rows {
		cells := strings.Split(row, ",")
		if len(cells) != n {
			return nil, fmt.Errorf("correlation row %d has %d entries, want %d", i+1, len(cells), n)
		}
		for j, cell := range cells {
			v, err := strconv.ParseFloat(strings.TrimSpace(cell), 64)
			if err != nil {
				return nil, fmt.Errorf("correlation %q: %w", cell, err)
			}
			matrix[i][j] = v
		}
	}
	return matrix, nil
}

func runWorstOfPut(args []string) error {
	fs := flag.NewFlagSet("pricer worst-of-put", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var unds underlierFlags
	fs.Var(&unds, "underlier", "repeatable SPOT:VOL[:DIVIDEND[:INITIAL]]; INITIAL defaults to SPOT (default two: 100:0.2 and 100:0.25)")
	correlation := fs.String("correlation", "0.5", "pairwise correlation (0.5) or matrix with ';' between rows (1,0.5;0.5,1)")
	strike := fs.Float64("strike", 1, "strike as a fraction of each initial level (1 = 100%)")
	notional := fs.Float64("notional", 100, "notional; payoff is notional × max(strike − worst performance, 0)")
	expiryFlag := fs.String("expiry", "2027-11-16", "expiry date (YYYY-MM-DD); European exercise")
	valuationFlag := fs.String("valuation", "2026-11-16", "valuation date (YYYY-MM-DD)")
	paths := fs.Int("paths", option.DefaultPaths, "Monte Carlo paths (each paired with an antithetic path)")
	seed := fs.Uint64("seed", 1, "random seed; the same seed gives the same price")
	rate := fs.Float64("rate", 0.04, "flat risk-free rate as a decimal, used when no -pillar is set")
	compounding := fs.String("compounding", "continuous", "continuous, annual, or semiannual")
	dayCountName := fs.String("daycount", "actual/365", "variance and dividend year fraction: actual/365 or actual/actual")
	var pillars pillarFlags
	fs.Var(&pillars, "pillar", "repeatable continuous zero pillar DATE=RATE (overrides -rate)")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `Price a European worst-of put by Monte Carlo.

At expiry the put pays notional × max(strike − min_i(S_i/initial_i), 0), the
put on the worst performer of the basket. Each underlier follows geometric
Brownian motion with constant volatility and a continuous dividend yield, with
correlated terminal shocks. There is no closed form, so the premium has a
Monte Carlo standard error. Greeks are pathwise estimates per underlier.

Usage:
  pricer worst-of-put [flags]

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
	opt := option.WorstOfPut{
		Underliers: unds, Correlation: corr, Strike: *strike, Expiry: expiry,
		Notional: *notional, DayCount: dc, Paths: *paths, Seed: *seed,
	}
	quote, err := opt.Price(valuation, curve)
	if err != nil {
		return err
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "Theoretical price of a European worst-of put")
	fmt.Fprintln(w)
	fmt.Fprintf(w, "Model\tMonte Carlo, correlated GBM, antithetic variates\n")
	fmt.Fprintf(w, "Paths\t%d (x2 antithetic), seed %d\n", quote.Paths, *seed)
	fmt.Fprintf(w, "Strike\t%.6f%% of initial\n", *strike*100)
	fmt.Fprintf(w, "Notional\t%s\n", formatMoney(*notional))
	fmt.Fprintf(w, "Expiry\t%s\n", expiry.Format("2006-01-02"))
	fmt.Fprintf(w, "Valuation\t%s\n", valuation.Format("2006-01-02"))
	fmt.Fprintf(w, "Year fraction\t%.8f (%s)\n", quote.YearFraction, dc)
	fmt.Fprintf(w, "Discount curve\t%s\n", curveLabel)
	fmt.Fprintf(w, "Discount factor\t%.8f\n", quote.DiscountFactor)
	fmt.Fprintln(w)
	fmt.Fprintf(w, "Premium\t%s\n", formatMoney(quote.Premium))
	fmt.Fprintf(w, "Std error\t%s\n", formatMoney(quote.StdErr))
	fmt.Fprintf(w, "Premium (%% notional)\t%.6f%%\n", quote.Premium / *notional * 100)
	fmt.Fprintf(w, "Exercise value\t%s\n", formatMoney(quote.ExerciseValue))
	fmt.Fprintf(w, "Time value\t%s\n", formatMoney(quote.TimeValue))
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Underlier\tSpot\tInitial\tVol\tDividend\tP(worst)\tDelta\tVega (per 1% vol)")
	for i, u := range unds {
		initial := u.Initial
		if initial == 0 {
			initial = u.Spot
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%.4f%%\t%.4f%%\t%.4f\t%s\t%s\n",
			u.Name, formatMoney(u.Spot), formatMoney(initial), u.Volatility*100, u.DividendYield*100,
			quote.WorstProb[i], formatMoney(quote.Deltas[i]), formatMoney(quote.Vegas[i]/100))
	}
	return w.Flush()
}
