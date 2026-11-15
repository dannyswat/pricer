package option

import (
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"time"

	"pricer/daycount"
	"pricer/discount"
	"pricer/pricing"
)

var (
	ErrInvalidBasket      = errors.New("worst-of put needs at least one underlier")
	ErrInvalidCorrelation = errors.New("correlation must be a symmetric positive semi-definite matrix with unit diagonal")
	ErrInvalidNotional    = errors.New("notional must be positive")
	ErrInvalidPaths       = errors.New("paths must be positive")
)

// DefaultPaths is the number of simulated paths when WorstOfPut.Paths is zero.
// Each path is paired with its antithetic mirror.
const DefaultPaths = 200_000

// Underlier is one asset in a worst-of basket. Initial is the fixing level
// that performance is measured against; zero means Spot, i.e. the option is
// struck at inception. Volatility and DividendYield are annual decimals and
// DividendYield is continuous.
type Underlier struct {
	Name          string
	Spot          float64
	Initial       float64
	Volatility    float64
	DividendYield float64
}

// WorstOfPut is a European put on the worst performer of a basket. At Expiry
// it pays Notional × max(Strike − min_i(S_i/Initial_i), 0), so Strike is a
// fraction of the initial level (1.0 is at the money).
//
// There is no closed form for more than one underlier, so the premium is a
// Monte Carlo estimate. Each underlier follows geometric Brownian motion with
// constant volatility under the risk-neutral measure, with correlated
// terminal shocks. Correlation is an N×N matrix in Underliers order; nil means
// the identity (independent underliers). With a single underlier the price
// converges to the Black-Scholes put. Seed makes results reproducible.
type WorstOfPut struct {
	Underliers  []Underlier
	Correlation [][]float64
	Strike      float64
	Expiry      time.Time
	Notional    float64
	DayCount    daycount.Convention
	Paths       int
	Seed        uint64
}

// WorstOfQuote is a Monte Carlo price. StdErr is the standard error of
// Premium. Deltas, Vegas and ForwardPerf are per underlier in basket order.
// Delta is ∂V/∂S_i from the pathwise estimator and Vega is ∂V/∂σ_i from the
// pathwise estimator, both for the position (scaled by Notional). ForwardPerf
// is the risk-neutral expected performance S_i(T)/Initial_i.
type WorstOfQuote struct {
	Premium        float64
	StdErr         float64
	DiscountFactor float64
	YearFraction   float64
	Rate           float64
	Paths          int
	Deltas         []float64
	Vegas          []float64
	ForwardPerf    []float64
	WorstProb      []float64 // probability each underlier is the worst performer
	ExerciseValue  float64
	TimeValue      float64
}

// TheoreticalPrice reports the premium in Result.Dirty.
func (p WorstOfPut) TheoreticalPrice(asOf time.Time, curve discount.Curve) (pricing.Result, error) {
	q, err := p.Price(asOf, curve)
	if err != nil {
		return pricing.Result{}, err
	}
	return pricing.Result{Dirty: q.Premium, Clean: q.Premium}, nil
}

// Price returns the Monte Carlo premium of the worst-of put.
func (p WorstOfPut) Price(asOf time.Time, curve discount.Curve) (WorstOfQuote, error) {
	if err := p.validate(); err != nil {
		return WorstOfQuote{}, err
	}
	// The shared resolver validates the scalar fields, so feed it the first underlier.
	first := p.Underliers[0]
	yearFrac, df, err := contract{
		Spot: first.Spot, Strike: p.Strike, Expiry: p.Expiry, Volatility: first.Volatility,
		DividendYield: first.DividendYield, Quantity: p.Notional, DayCount: p.DayCount,
	}.resolve(asOf, curve)
	if err != nil {
		return WorstOfQuote{}, err
	}
	if math.IsNaN(df) || math.IsInf(df, 0) || df <= 0 {
		return WorstOfQuote{}, fmt.Errorf("discount factor must be positive")
	}
	chol, err := cholesky(p.Correlation, len(p.Underliers))
	if err != nil {
		return WorstOfQuote{}, err
	}
	paths := p.Paths
	if paths == 0 {
		paths = DefaultPaths
	}
	return p.simulate(yearFrac, df, chol, paths), nil
}

func (p WorstOfPut) validate() error {
	n := len(p.Underliers)
	if n == 0 {
		return ErrInvalidBasket
	}
	for i, u := range p.Underliers {
		if !(u.Spot > 0) || math.IsInf(u.Spot, 0) {
			return fmt.Errorf("underlier %d: %w", i+1, ErrInvalidSpot)
		}
		if u.Initial < 0 || math.IsNaN(u.Initial) || math.IsInf(u.Initial, 0) {
			return fmt.Errorf("underlier %d: initial level must be positive", i+1)
		}
		if u.Volatility < 0 || math.IsNaN(u.Volatility) || math.IsInf(u.Volatility, 0) {
			return fmt.Errorf("underlier %d: %w", i+1, ErrInvalidVol)
		}
		if math.IsNaN(u.DividendYield) || math.IsInf(u.DividendYield, 0) {
			return fmt.Errorf("underlier %d: dividend yield must be finite", i+1)
		}
	}
	if !(p.Strike > 0) || math.IsInf(p.Strike, 0) {
		return ErrInvalidStrike
	}
	if !(p.Notional > 0) || math.IsInf(p.Notional, 0) {
		return ErrInvalidNotional
	}
	if p.Paths < 0 {
		return ErrInvalidPaths
	}
	return nil
}

func (p WorstOfPut) simulate(yearFrac, df float64, chol [][]float64, paths int) WorstOfQuote {
	n := len(p.Underliers)
	sqrtT := math.Sqrt(yearFrac)
	rate := 0.0
	if yearFrac > 0 {
		rate = -math.Log(df) / yearFrac
	}
	// perf0 is the performance at inception; fwd is E[S_T]/Initial.
	perf0 := make([]float64, n)
	fwd := make([]float64, n)
	for i, u := range p.Underliers {
		initial := u.Initial
		if initial == 0 {
			initial = u.Spot
		}
		perf0[i] = u.Spot / initial
		fwd[i] = perf0[i] * math.Exp(-u.DividendYield*yearFrac) / df
	}
	exercise := p.Notional * math.Max(p.Strike-minOf(perf0), 0)

	rng := rand.New(rand.NewPCG(p.Seed, 0x9e3779b97f4a7c15))
	z := make([]float64, n)
	corr := make([]float64, n)
	perf := make([]float64, n)
	deltaSum := make([]float64, n)
	vegaSum := make([]float64, n)
	worstCount := make([]float64, n)
	forwardSum := make([]float64, n)
	var sum, sumSq float64

	// pathPayoff evaluates one mirrored sample; sign is +1 or -1 for the antithetic leg.
	pathPayoff := func(sign float64) float64 {
		worst := 0
		for i, u := range p.Underliers {
			x := sign * corr[i]
			perf[i] = fwd[i] * math.Exp(-0.5*u.Volatility*u.Volatility*yearFrac+u.Volatility*sqrtT*x)
			if perf[i] < perf[worst] {
				worst = i
			}
			forwardSum[i] += perf[i]
		}
		worstCount[worst]++
		payoff := math.Max(p.Strike-perf[worst], 0)
		if payoff > 0 {
			u := p.Underliers[worst]
			// ∂payoff/∂S0 = −S_T/S0 and ∂S_T/∂σ = S_T(−σT + √T·x) for the worst name.
			x := sign * corr[worst]
			deltaSum[worst] -= perf[worst] / u.Spot
			vegaSum[worst] -= perf[worst] * (-u.Volatility*yearFrac + sqrtT*x)
		}
		return payoff
	}

	for k := 0; k < paths; k++ {
		for i := range z {
			z[i] = rng.NormFloat64()
		}
		for i := range corr {
			s := 0.0
			for j := 0; j <= i; j++ {
				s += chol[i][j] * z[j]
			}
			corr[i] = s
		}
		pair := 0.5 * (pathPayoff(1) + pathPayoff(-1))
		sum += pair
		sumSq += pair * pair
	}

	m := float64(paths)
	mean := sum / m
	variance := 0.0
	if paths > 1 {
		variance = math.Max(sumSq/m-mean*mean, 0) * m / (m - 1)
	}
	scale := df * p.Notional
	premium := scale * mean
	q := WorstOfQuote{
		Premium:        premium,
		StdErr:         scale * math.Sqrt(variance/m),
		DiscountFactor: df,
		YearFraction:   yearFrac,
		Rate:           rate,
		Paths:          paths,
		Deltas:         make([]float64, n),
		Vegas:          make([]float64, n),
		ForwardPerf:    make([]float64, n),
		WorstProb:      make([]float64, n),
		ExerciseValue:  exercise,
		TimeValue:      premium - exercise,
	}
	samples := 2 * m
	for i := range q.Deltas {
		q.Deltas[i] = scale * deltaSum[i] / samples
		q.Vegas[i] = scale * vegaSum[i] / samples
		q.ForwardPerf[i] = forwardSum[i] / samples
		q.WorstProb[i] = worstCount[i] / samples
	}
	return q
}

func minOf(xs []float64) float64 {
	m := xs[0]
	for _, x := range xs[1:] {
		m = math.Min(m, x)
	}
	return m
}

// cholesky factors a correlation matrix. A nil matrix is the identity.
// Singular matrices (e.g. correlation 1) are accepted.
func cholesky(corr [][]float64, n int) ([][]float64, error) {
	l := make([][]float64, n)
	for i := range l {
		l[i] = make([]float64, n)
	}
	if corr == nil {
		for i := range l {
			l[i][i] = 1
		}
		return l, nil
	}
	if len(corr) != n {
		return nil, fmt.Errorf("%w: want %d×%d", ErrInvalidCorrelation, n, n)
	}
	const tol = 1e-9
	for i := 0; i < n; i++ {
		if len(corr[i]) != n {
			return nil, fmt.Errorf("%w: want %d×%d", ErrInvalidCorrelation, n, n)
		}
		if math.Abs(corr[i][i]-1) > tol {
			return nil, ErrInvalidCorrelation
		}
		for j := 0; j < i; j++ {
			if math.IsNaN(corr[i][j]) || math.Abs(corr[i][j]) > 1+tol || math.Abs(corr[i][j]-corr[j][i]) > tol {
				return nil, ErrInvalidCorrelation
			}
		}
	}
	for j := 0; j < n; j++ {
		d := corr[j][j]
		for k := 0; k < j; k++ {
			d -= l[j][k] * l[j][k]
		}
		if d < -tol {
			return nil, ErrInvalidCorrelation
		}
		if d <= tol {
			d = 0
		}
		l[j][j] = math.Sqrt(d)
		for i := j + 1; i < n; i++ {
			s := corr[i][j]
			for k := 0; k < j; k++ {
				s -= l[i][k] * l[j][k]
			}
			if l[j][j] == 0 {
				if math.Abs(s) > 1e-6 {
					return nil, ErrInvalidCorrelation
				}
				continue
			}
			l[i][j] = s / l[j][j]
		}
	}
	return l, nil
}
