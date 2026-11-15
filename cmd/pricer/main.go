// Command pricer prints the theoretical price of a risk-free Treasury bond.
package main

import (
	"flag"
	"fmt"
	"math"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"pricer/bond"
	"pricer/daycount"
	"pricer/discount"
	"pricer/pricing"
)

const rootUsage = `Usage:
  pricer treasury [flags]   price a risk-free Treasury bond
  pricer put [flags]        price a European vanilla put
  pricer down-in-put [flags]   price a European down-and-in barrier put
  pricer call [flags]       price a European vanilla call
  pricer digital-call [flags]  price a European cash-or-nothing call
  pricer digital-put [flags]   price a European cash-or-nothing put
  pricer worst-of-put [flags]  price a European worst-of put (Monte Carlo)
  pricer worst-of-down-in-put [flags]  price a worst-of down-and-in put
  pricer eln [flags]        price a vanilla ELN as issuer
  pricer worst-of-eln [flags]  price a worst-of ELN as issuer (Monte Carlo)

With no command, the sample Treasury is priced.
`

func main() {
	args := os.Args[1:]
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		switch args[0] {
		case "put":
			if err := runPut(args[1:]); err != nil {
				fmt.Fprintf(os.Stderr, "pricer: %v\n", err)
				os.Exit(1)
			}
			return
		case "down-in-put":
			if err := runDownInPut(args[1:]); err != nil {
				fmt.Fprintf(os.Stderr, "pricer: %v\n", err)
				os.Exit(1)
			}
			return
		case "call":
			if err := runCall(args[1:]); err != nil {
				fmt.Fprintf(os.Stderr, "pricer: %v\n", err)
				os.Exit(1)
			}
			return
		case "digital-call":
			if err := runDigitalCall(args[1:]); err != nil {
				fmt.Fprintf(os.Stderr, "pricer: %v\n", err)
				os.Exit(1)
			}
			return
		case "digital-put":
			if err := runDigitalPut(args[1:]); err != nil {
				fmt.Fprintf(os.Stderr, "pricer: %v\n", err)
				os.Exit(1)
			}
			return
		case "worst-of-put":
			if err := runWorstOfPut(args[1:]); err != nil {
				fmt.Fprintf(os.Stderr, "pricer: %v\n", err)
				os.Exit(1)
			}
			return
		case "worst-of-down-in-put":
			if err := runWorstOfDownInPut(args[1:]); err != nil {
				fmt.Fprintf(os.Stderr, "pricer: %v\n", err)
				os.Exit(1)
			}
			return
		case "eln":
			if err := runELN(args[1:]); err != nil {
				fmt.Fprintf(os.Stderr, "pricer: %v\n", err)
				os.Exit(1)
			}
			return
		case "worst-of-eln":
			if err := runWorstOfELN(args[1:]); err != nil {
				fmt.Fprintf(os.Stderr, "pricer: %v\n", err)
				os.Exit(1)
			}
			return
		case "treasury", "bond":
			args = args[1:]
		case "help":
			fmt.Fprint(os.Stderr, rootUsage)
			return
		default:
			fmt.Fprintf(os.Stderr, "pricer: unknown command %q\n\n%s", args[0], rootUsage)
			os.Exit(2)
		}
	}
	if err := runTreasury(args); err != nil {
		fmt.Fprintf(os.Stderr, "pricer: %v\n", err)
		os.Exit(1)
	}
}

func runTreasury(args []string) error {
	fs := flag.NewFlagSet("pricer treasury", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	maturityFlag := fs.String("maturity", "2036-11-15", "maturity date (YYYY-MM-DD)")
	settlementFlag := fs.String("settlement", "2026-11-16", "valuation / settlement date (YYYY-MM-DD)")
	coupon := fs.Float64("coupon", 0.0425, "annual coupon rate as a decimal (0.0425 = 4.25%)")
	face := fs.Float64("face", 100, "face amount in USD")
	frequency := fs.Int("frequency", 2, "coupons per year; US notes and bonds use 2")
	rate := fs.Float64("rate", 0.04, "flat risk-free rate as a decimal, used when no -pillar is set")
	compounding := fs.String("compounding", "continuous", "continuous, annual, or semiannual")
	dayCountName := fs.String("daycount", "actual/actual", "discount year fraction: actual/actual or actual/365")
	ytm := fs.Float64("yield", 0.04, "bond-equivalent yield for the street-convention quote")
	var pillars pillarFlags
	fs.Var(&pillars, "pillar", "repeatable continuous zero pillar DATE=RATE (overrides -rate)")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `Price a risk-free Treasury bond from its maturity date.

The theoretical invoice price is the present value of remaining coupons and
par redemption. The bond is assumed risk-free, so cash flows are discounted
on the risk-free curve with no credit spread. Coupon dates are rolled back
from maturity and are not shifted for weekends or holidays.

Defaults price the 4.25%% of 15 November 2036 on 16 November 2026.
Other instruments: pricer put, pricer call, pricer eln.

Usage:
  pricer treasury [flags]

`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}
	return run(*maturityFlag, *settlementFlag, *coupon, *face, *frequency, *rate, *compounding, *dayCountName, *ytm, pillars)
}

func run(maturityS, settlementS string, coupon, face float64, frequency int, rate float64, compounding, dayCountName string, ytm float64, pillars []discount.Pillar) error {
	maturity, err := parseDate(maturityS)
	if err != nil {
		return fmt.Errorf("maturity: %w", err)
	}
	settlement, err := parseDate(settlementS)
	if err != nil {
		return fmt.Errorf("settlement: %w", err)
	}
	dc, err := daycount.Parse(dayCountName)
	if err != nil {
		return err
	}
	note := bond.Treasury{
		Face:       face,
		CouponRate: coupon,
		Maturity:   maturity,
		Frequency:  frequency,
	}
	curve, curveLabel, err := buildCurve(settlement, rate, compounding, dc, pillars)
	if err != nil {
		return err
	}
	theoretical, err := note.TheoreticalPrice(settlement, curve)
	if err != nil {
		return err
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "Theoretical price of a risk-free Treasury bond")
	fmt.Fprintln(w)
	fmt.Fprintf(w, "Face\t%s USD\n", formatMoney(face))
	if coupon == 0 {
		fmt.Fprintf(w, "Coupon\tzero\n")
	} else {
		fmt.Fprintf(w, "Coupon\t%.6f%% %s\n", coupon*100, frequencyLabel(frequency))
	}
	fmt.Fprintf(w, "Maturity\t%s\n", maturity.Format("2006-01-02"))
	fmt.Fprintf(w, "Settlement\t%s\n", settlement.Format("2006-01-02"))
	fmt.Fprintf(w, "Redemption\tpar\n")
	fmt.Fprintf(w, "Credit\trisk-free (spread 0)\n")
	fmt.Fprintf(w, "Discount curve\t%s\n", curveLabel)
	fmt.Fprintf(w, "Accrual\tActual/Actual ICMA, regular schedule back from maturity\n")
	fmt.Fprintln(w)
	writePrice(w, "Theoretical invoice (dirty)", theoretical, face)
	writePrice(w, "Accrued interest", pricing.Result{Dirty: theoretical.Accrued}, face)
	writePrice(w, "Clean quote", pricing.Result{Dirty: theoretical.Clean}, face)
	fmt.Fprintf(w, "Nearest 1/32 tick\t%s per 100 (display only)\n", format32(per100(theoretical.Clean, face)))
	if err := w.Flush(); err != nil {
		return err
	}

	implied, err := note.YieldFromDirty(settlement, theoretical.Dirty)
	if err != nil {
		fmt.Fprintf(os.Stdout, "\nBond-equivalent yield of the theoretical invoice price: unavailable (%v)\n", err)
	} else {
		fmt.Fprintf(os.Stdout, "\nBond-equivalent yield of the theoretical invoice price: %.6f%%\n", implied*100)
	}

	street, err := note.PriceAtYield(settlement, ytm)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "\nStreet convention at bond-equivalent yield %.6f%%\n", ytm*100)
	w = tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	writePrice(w, "Invoice (dirty)", street, face)
	writePrice(w, "Accrued interest", pricing.Result{Dirty: street.Accrued}, face)
	writePrice(w, "Clean quote", pricing.Result{Dirty: street.Clean}, face)
	if err := w.Flush(); err != nil {
		return err
	}
	fmt.Println("\nStreet discounting counts coupon periods. It differs from the theoretical curve price when a coupon period is not exactly 1/frequency years.")

	fmt.Println("\nCash flows discounted on the risk-free curve")
	w = tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "Date\tKind\tAmount\tDiscount\tPresent value")
	for _, cf := range theoretical.Flows {
		fmt.Fprintf(w, "%s\t%s\t%s\t%.8f\t%s\n",
			cf.Date.Format("2006-01-02"), cf.Kind, formatMoney(cf.Amount), cf.DiscountFactor, formatMoney(cf.PresentValue))
	}
	fmt.Fprintf(w, "\t\t\t\t%s\n", formatMoney(theoretical.Dirty))
	return w.Flush()
}

func writePrice(w *tabwriter.Writer, label string, res pricing.Result, face float64) {
	fmt.Fprintf(w, "%s\t%s USD\t%.6f per 100\n", label, formatMoney(res.Dirty), per100(res.Dirty, face))
}

func per100(amount, face float64) float64 {
	return amount / face * 100
}

func buildCurve(settlement time.Time, rate float64, compounding string, dc daycount.Convention, pillars []discount.Pillar) (discount.Curve, string, error) {
	if len(pillars) > 0 {
		curve := discount.ZeroCurve{AsOf: settlement, Pillars: pillars, DayCount: dc}
		return curve, curve.Describe(), nil
	}
	freq, err := compoundingFrequency(compounding)
	if err != nil {
		return nil, "", err
	}
	curve := discount.Flat{Rate: rate, Frequency: freq, DayCount: dc}
	return curve, curve.Describe(), nil
}

func compoundingFrequency(name string) (int, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "continuous", "cont":
		return 0, nil
	case "annual", "annually":
		return 1, nil
	case "semiannual", "semi-annual", "semi":
		return 2, nil
	default:
		return 0, fmt.Errorf("unknown compounding %q (use continuous, annual, or semiannual)", name)
	}
}

func frequencyLabel(frequency int) string {
	switch frequency {
	case 1:
		return "annual"
	case 2:
		return "semi-annual"
	case 4:
		return "quarterly"
	case 12:
		return "monthly"
	default:
		return fmt.Sprintf("%d times/year", frequency)
	}
}

func parseDate(s string) (time.Time, error) {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return time.Time{}, fmt.Errorf("want YYYY-MM-DD, got %q", s)
	}
	return daycount.Normalize(t), nil
}

type pillarFlags []discount.Pillar

func (p *pillarFlags) String() string { return "" }

func (p *pillarFlags) Set(s string) error {
	datePart, ratePart, ok := strings.Cut(s, "=")
	if !ok {
		return fmt.Errorf("pillar %q must be DATE=RATE", s)
	}
	d, err := parseDate(datePart)
	if err != nil {
		return fmt.Errorf("pillar: %w", err)
	}
	var rate float64
	if _, err := fmt.Sscan(ratePart, &rate); err != nil {
		return fmt.Errorf("pillar rate %q: %w", ratePart, err)
	}
	*p = append(*p, discount.Pillar{Date: d, ContinuousZero: rate})
	return nil
}

func formatMoney(v float64) string {
	sign := ""
	if v < 0 {
		sign = "-"
		v = -v
	}
	raw := fmt.Sprintf("%.6f", v)
	intPart, frac, _ := strings.Cut(raw, ".")
	var b strings.Builder
	for i, r := range intPart {
		if i > 0 && (len(intPart)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	return sign + b.String() + "." + frac
}

func format32(per100 float64) string {
	ticks := math.Round(per100 * 32)
	whole := math.Floor(ticks / 32)
	frac := int(math.Abs(ticks-whole*32)) % 32
	return fmt.Sprintf("%d-%02d", int(whole), frac)
}
