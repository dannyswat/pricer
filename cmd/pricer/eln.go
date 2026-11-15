package main

import (
	"flag"
	"fmt"
	"os"
	"text/tabwriter"

	"pricer/daycount"
	"pricer/eln"
)

func runELN(args []string) error {
	fs := flag.NewFlagSet("pricer eln", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	notional := fs.Float64("notional", 100, "note notional redeemed if spot finishes at or above strike")
	spot := fs.Float64("spot", 100, "spot price of the underlier")
	strike := fs.Float64("strike", 95, "strike; below this the issuer pays notional * spot / strike")
	expiryFlag := fs.String("expiry", "2027-02-16", "expiry date (YYYY-MM-DD)")
	valuationFlag := fs.String("valuation", "2026-11-16", "valuation date (YYYY-MM-DD)")
	vol := fs.Float64("vol", 0.2, "mid-market annualized volatility as a decimal")
	volBump := fs.Float64("vol-bump", 0.01, "vol points haircut from the put credited to the investor (0.01 = 1 vol point)")
	markup := fs.Float64("markup", 0.005, "issuer markup as a fraction of notional (0.005 = 50 bps)")
	dividend := fs.Float64("dividend", 0, "continuous dividend yield as a decimal")
	rate := fs.Float64("rate", 0.04, "flat risk-free rate as a decimal, used when no -pillar is set")
	compounding := fs.String("compounding", "continuous", "continuous, annual, or semiannual")
	dayCountName := fs.String("daycount", "actual/365", "variance year fraction: actual/365 or actual/actual")
	var pillars pillarFlags
	fs.Var(&pillars, "pillar", "repeatable continuous zero pillar DATE=RATE (overrides -rate)")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `Price a vanilla equity-linked note as the issuer.

The investor pays the issue price today. At expiry the issuer pays notional
if spot >= strike, and notional * spot / strike otherwise. That is a
discounted notional minus Notional/Strike European puts. The issuer is long
the put.

Issue price = mid-market note value + cost of the vol bump + markup.
The bump haircuts the vol of the put credited to the investor. Markup is sales margin.

Defaults are a 3-month note on 100, struck at 95, with a 1 vol-point bump
and 50 bps of markup.

Usage:
  pricer eln [flags]

`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
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
	note := eln.Vanilla{
		Notional:      *notional,
		Spot:          *spot,
		Strike:        *strike,
		Expiry:        expiry,
		Volatility:    *vol,
		DividendYield: *dividend,
		DayCount:      dc,
		VolBump:       *volBump,
		Markup:        *markup,
	}
	quote, err := note.Price(valuation, curve)
	if err != nil {
		return err
	}
	return writeELN(note, curveLabel, quote)
}

func writeELN(note eln.Vanilla, curveLabel string, quote eln.Quote) error {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "Issuer price of a vanilla equity-linked note")
	fmt.Fprintln(w)
	fmt.Fprintf(w, "Payoff\tif spot >= strike, notional; otherwise notional * spot / strike\n")
	fmt.Fprintf(w, "Replication\tdiscounted notional minus %.6f European puts\n", quote.PutQuantity)
	fmt.Fprintf(w, "Issuer option\tlong the embedded put\n")
	fmt.Fprintln(w)
	fmt.Fprintf(w, "Notional\t%s\n", formatMoney(note.Notional))
	fmt.Fprintf(w, "Spot\t%s\n", formatMoney(note.Spot))
	fmt.Fprintf(w, "Strike\t%s\n", formatMoney(note.Strike))
	fmt.Fprintf(w, "Expiry\t%s\n", note.Expiry.Format("2006-01-02"))
	fmt.Fprintf(w, "Year fraction\t%.8f (%s)\n", quote.YearFraction, note.DayCount)
	fmt.Fprintf(w, "Volatility\t%.6f%% mid\n", note.Volatility*100)
	fmt.Fprintf(w, "Vol bump\t%.6f%%\n", note.VolBump*100)
	fmt.Fprintf(w, "Markup\t%.6f%% of notional\n", note.Markup*100)
	fmt.Fprintf(w, "Dividend yield\t%.6f%% continuous\n", note.DividendYield*100)
	fmt.Fprintf(w, "Discount curve\t%s\n", curveLabel)
	fmt.Fprintf(w, "Discount factor\t%.8f\n", quote.DiscountFactor)
	fmt.Fprintln(w)
	writeELNRow(w, "Theoretical value (mid vol)", quote.TheoreticalValue, note.Notional)
	writeELNRow(w, "Embedded put (mid vol)", quote.PutPremium, note.Notional)
	writeELNRow(w, "Embedded put (haircut vol)", quote.BumpedPutPremium, note.Notional)
	writeELNRow(w, "Cost of bump", quote.CostOfBump, note.Notional)
	writeELNRow(w, "Markup", quote.Markup, note.Notional)
	fmt.Fprintln(w)
	writeELNRow(w, "Issue price", quote.IssuePrice, note.Notional)
	writeELNRow(w, "Issuer day-1 margin", quote.Day1Margin, note.Notional)
	fmt.Fprintf(w, "Maximum yield\t%.6f%%\n", quote.MaxYield*100)
	fmt.Fprintf(w, "Investor delta\t%.6f shares\n", quote.InvestorDelta)
	fmt.Fprintf(w, "Issuer hedge\tbuy %.6f shares\n", quote.HedgeShares)
	fmt.Fprintln(w)
	fmt.Fprintf(w, "Day-1 margin is the issue price minus the mid-market value.\n")
	fmt.Fprintf(w, "Marked at the haircut vol, the reserve is the cost of bump and the booked margin is the markup.\n")
	fmt.Fprintf(w, "Maximum yield assumes redemption at notional and an Actual/365 money-market basis.\n")
	return w.Flush()
}

func writeELNRow(w *tabwriter.Writer, label string, amount, notional float64) {
	fmt.Fprintf(w, "%s\t%s\t%.6f per 100\n", label, formatMoney(amount), amount/notional*100)
}
