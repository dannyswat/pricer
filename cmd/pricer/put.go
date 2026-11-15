package main

import (
	"flag"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"pricer/daycount"
	"pricer/option"
)

func runPut(args []string) error {
	return runVanilla("put", args)
}

func runCall(args []string) error {
	return runVanilla("call", args)
}

func runVanilla(right string, args []string) error {
	fs := flag.NewFlagSet("pricer "+right, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	spot := fs.Float64("spot", 100, "spot price of the underlier")
	strike := fs.Float64("strike", 100, right+" strike")
	expiryFlag := fs.String("expiry", "2027-11-16", "expiry date (YYYY-MM-DD); European exercise")
	valuationFlag := fs.String("valuation", "2026-11-16", "valuation date (YYYY-MM-DD)")
	vol := fs.Float64("vol", 0.2, "annualized volatility as a decimal (0.2 = 20%)")
	dividend := fs.Float64("dividend", 0, "continuous dividend yield as a decimal")
	quantity := fs.Float64("quantity", 1, "number of options; premium and Greeks scale by this")
	rate := fs.Float64("rate", 0.04, "flat risk-free rate as a decimal, used when no -pillar is set")
	compounding := fs.String("compounding", "continuous", "continuous, annual, or semiannual")
	dayCountName := fs.String("daycount", "actual/365", "variance and dividend year fraction: actual/365 or actual/actual")
	var pillars pillarFlags
	fs.Var(&pillars, "pillar", "repeatable continuous zero pillar DATE=RATE (overrides -rate)")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `Price a European vanilla %s.

The premium is the Black-Scholes-Merton price. The strike is discounted on
the risk-free curve. Volatility is constant and the dividend yield is
continuous. Exercise is only at expiry, so this is not an American price.

Defaults price a 1-year at-the-money %s on 100, valued 16 November 2026
and expiring 16 November 2027, at 20%% volatility and a 4%% continuous
risk-free rate.

Usage:
  pricer %s [flags]

`, right, right, right)
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
	var quote option.Quote
	if right == "call" {
		quote, err = (option.EuropeanCall{
			Spot: *spot, Strike: *strike, Expiry: expiry,
			Volatility: *vol, DividendYield: *dividend, Quantity: *quantity, DayCount: dc,
		}).Price(valuation, curve)
	} else {
		quote, err = (option.EuropeanPut{
			Spot: *spot, Strike: *strike, Expiry: expiry,
			Volatility: *vol, DividendYield: *dividend, Quantity: *quantity, DayCount: dc,
		}).Price(valuation, curve)
	}
	if err != nil {
		return err
	}
	return writeOption(right, *spot, *strike, expiry, *vol, *dividend, *quantity, dc, valuation, curveLabel, quote)
}

func writeOption(right string, spot, strike float64, expiry time.Time, vol, dividend, quantity float64, dc daycount.Convention, valuation time.Time, curveLabel string, quote option.Quote) error {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(w, "Theoretical price of a European vanilla %s\n", right)
	fmt.Fprintln(w)
	fmt.Fprintf(w, "Model\tBlack-Scholes-Merton\n")
	fmt.Fprintf(w, "Spot\t%s\n", formatMoney(spot))
	fmt.Fprintf(w, "Strike\t%s\n", formatMoney(strike))
	fmt.Fprintf(w, "Expiry\t%s\n", expiry.Format("2006-01-02"))
	fmt.Fprintf(w, "Valuation\t%s\n", valuation.Format("2006-01-02"))
	fmt.Fprintf(w, "Year fraction\t%.8f (%s)\n", quote.YearFraction, dc)
	fmt.Fprintf(w, "Volatility\t%.6f%%\n", vol*100)
	fmt.Fprintf(w, "Dividend yield\t%.6f%% continuous\n", dividend*100)
	fmt.Fprintf(w, "Discount curve\t%s\n", curveLabel)
	fmt.Fprintf(w, "Discount factor\t%.8f\n", quote.DiscountFactor)
	fmt.Fprintf(w, "Forward\t%s\n", formatMoney(quote.Forward))
	fmt.Fprintf(w, "Quantity\t%.6f\n", quantity)
	fmt.Fprintln(w)
	fmt.Fprintf(w, "Premium\t%s\n", formatMoney(quote.Premium))
	fmt.Fprintf(w, "Exercise value\t%s\n", formatMoney(quote.ExerciseValue))
	fmt.Fprintf(w, "Time value\t%s\n", formatMoney(quote.TimeValue))
	fmt.Fprintln(w)
	fmt.Fprintf(w, "d1\t%.6f\n", quote.D1)
	fmt.Fprintf(w, "d2\t%.6f\n", quote.D2)
	fmt.Fprintf(w, "Delta\t%.6f\n", quote.Delta)
	fmt.Fprintf(w, "Gamma\t%.6f\n", quote.Gamma)
	fmt.Fprintf(w, "Vega (per 1%% vol)\t%s\n", formatMoney(quote.Vega/100))
	fmt.Fprintf(w, "Theta (per year)\t%s\n", formatMoney(quote.Theta))
	fmt.Fprintf(w, "Theta (per day)\t%s\n", formatMoney(quote.Theta/365))
	fmt.Fprintf(w, "Rho (per 1%% rate)\t%s\n", formatMoney(quote.Rho/100))
	return w.Flush()
}
