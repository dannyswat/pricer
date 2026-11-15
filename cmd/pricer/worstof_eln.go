package main

import (
	"flag"
	"fmt"
	"os"
	"text/tabwriter"

	"pricer/daycount"
	"pricer/eln"
	"pricer/option"
)

func runWorstOfELN(args []string) error {
	fs := flag.NewFlagSet("pricer worst-of-eln", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var unds underlierFlags
	fs.Var(&unds, "underlier", "repeatable SPOT:VOL[:DIVIDEND[:INITIAL]]; INITIAL defaults to SPOT (default two: 100:0.2 and 100:0.25)")
	correlation := fs.String("correlation", "0.5", "pairwise correlation (0.5) or matrix with ';' between rows (1,0.5;0.5,1)")
	notional := fs.Float64("notional", 100, "note notional redeemed if every underlier finishes at or above the strike")
	strike := fs.Float64("strike", 0.95, "strike as a fraction of each initial level (0.95 = 95%)")
	expiryFlag := fs.String("expiry", "2027-02-16", "expiry date (YYYY-MM-DD)")
	valuationFlag := fs.String("valuation", "2026-11-16", "valuation date (YYYY-MM-DD)")
	volBump := fs.Float64("vol-bump", 0.01, "vol points haircut from every underlier when the put is credited to the investor")
	markup := fs.Float64("markup", 0.005, "issuer markup as a fraction of notional (0.005 = 50 bps)")
	paths := fs.Int("paths", option.DefaultPaths, "Monte Carlo paths (each paired with an antithetic path)")
	seed := fs.Uint64("seed", 1, "random seed; the same seed gives the same price")
	rate := fs.Float64("rate", 0.04, "flat risk-free rate as a decimal, used when no -pillar is set")
	compounding := fs.String("compounding", "continuous", "continuous, annual, or semiannual")
	dayCountName := fs.String("daycount", "actual/365", "variance and dividend year fraction: actual/365 or actual/actual")
	var pillars pillarFlags
	fs.Var(&pillars, "pillar", "repeatable continuous zero pillar DATE=RATE (overrides -rate)")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `Price a worst-of equity-linked note as the issuer.

The investor pays the issue price today. At expiry the issuer pays notional
if every underlier finishes at or above strike times its initial level, and
notional * worst performance / strike otherwise. That is a discounted
notional minus a worst-of put of notional/strike. The issuer is long the put.

The put is priced by Monte Carlo, so the note value has a standard error.
Issue price = mid-market note value + cost of the vol bump + markup.
The bump haircuts every underlier's vol when the put is credited to the
investor. Markup is sales margin.

Defaults are a 3-month note on two underliers, struck at 95% of initial,
with a 1 vol-point bump and 50 bps of markup.

Usage:
  pricer worst-of-eln [flags]

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
	note := eln.WorstOf{
		Notional:    *notional,
		Underliers:  unds,
		Correlation: corr,
		Strike:      *strike,
		Expiry:      expiry,
		DayCount:    dc,
		VolBump:     *volBump,
		Markup:      *markup,
		Paths:       *paths,
		Seed:        *seed,
	}
	quote, err := note.Price(valuation, curve)
	if err != nil {
		return err
	}
	return writeWorstOfELN(note, curveLabel, quote, *seed)
}

func writeWorstOfELN(note eln.WorstOf, curveLabel string, quote eln.WorstOfQuote, seed uint64) error {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "Issuer price of a worst-of equity-linked note")
	fmt.Fprintln(w)
	fmt.Fprintf(w, "Payoff\tif worst performance >= strike, notional; otherwise notional * worst / strike\n")
	fmt.Fprintf(w, "Replication\tdiscounted notional minus a worst-of put of notional %s\n", formatMoney(quote.PutNotional))
	fmt.Fprintf(w, "Issuer option\tlong the embedded worst-of put\n")
	fmt.Fprintf(w, "Model\tMonte Carlo, correlated GBM, antithetic variates\n")
	fmt.Fprintf(w, "Paths\t%d (x2 antithetic), seed %d\n", quote.Paths, seed)
	fmt.Fprintln(w)
	fmt.Fprintf(w, "Notional\t%s\n", formatMoney(note.Notional))
	fmt.Fprintf(w, "Strike\t%.6f%% of initial\n", note.Strike*100)
	fmt.Fprintf(w, "Expiry\t%s\n", note.Expiry.Format("2006-01-02"))
	fmt.Fprintf(w, "Year fraction\t%.8f (%s)\n", quote.YearFraction, note.DayCount)
	fmt.Fprintf(w, "Vol bump\t%.6f%% on every underlier\n", note.VolBump*100)
	fmt.Fprintf(w, "Markup\t%.6f%% of notional\n", note.Markup*100)
	fmt.Fprintf(w, "Discount curve\t%s\n", curveLabel)
	fmt.Fprintf(w, "Discount factor\t%.8f\n", quote.DiscountFactor)
	fmt.Fprintln(w)
	writeELNRow(w, "Theoretical value (mid vol)", quote.TheoreticalValue, note.Notional)
	fmt.Fprintf(w, "Theoretical std error\t%s\n", formatMoney(quote.TheoreticalStdErr))
	writeELNRow(w, "Embedded put (mid vol)", quote.PutPremium, note.Notional)
	writeELNRow(w, "Embedded put (haircut vol)", quote.BumpedPutPremium, note.Notional)
	writeELNRow(w, "Cost of bump", quote.CostOfBump, note.Notional)
	writeELNRow(w, "Markup", quote.Markup, note.Notional)
	fmt.Fprintln(w)
	writeELNRow(w, "Issue price", quote.IssuePrice, note.Notional)
	fmt.Fprintf(w, "Issue price std error\t%s\n", formatMoney(quote.IssueStdErr))
	writeELNRow(w, "Issuer day-1 margin", quote.Day1Margin, note.Notional)
	fmt.Fprintf(w, "Maximum yield\t%.6f%%\n", quote.MaxYield*100)
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Underlier\tSpot\tInitial\tVol\tDividend\tP(worst)\tInvestor delta\tIssuer hedge (buy)")
	for i, u := range note.Underliers {
		initial := u.Initial
		if initial == 0 {
			initial = u.Spot
		}
		name := u.Name
		if name == "" {
			name = fmt.Sprintf("#%d", i+1)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%.4f%%\t%.4f%%\t%.4f\t%.6f\t%.6f\n",
			name, formatMoney(u.Spot), formatMoney(initial), u.Volatility*100, u.DividendYield*100,
			quote.WorstProb[i], quote.InvestorDeltas[i], quote.HedgeShares[i])
	}
	fmt.Fprintln(w)
	fmt.Fprintf(w, "Day-1 margin is the issue price minus the mid-market value.\n")
	fmt.Fprintf(w, "Marked at the haircut vol, the reserve is the cost of bump and the booked margin is the markup.\n")
	fmt.Fprintf(w, "The issue-price standard error is the haircut put's Monte Carlo error.\n")
	fmt.Fprintf(w, "Maximum yield assumes redemption at notional and an Actual/365 money-market basis.\n")
	return w.Flush()
}
