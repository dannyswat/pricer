package option

import (
	"math"
	"math/rand/v2"
	"time"

	"pricer/daycount"
	"pricer/discount"
	"pricer/pricing"
)

// WorstOfDownInPut is a European put on the worst performer that knocks in
// only if some underlier's performance touches Barrier. Barrier and Strike
// are fractions of each initial level. At Expiry, if the barrier has been
// touched, the put pays Notional × max(Strike − min_i(S_i/Initial_i), 0).
// The barrier is monitored continuously and there is no rebate.
//
// A single underlier is the closed-form down-and-in put. If the basket has
// already knocked in, or Barrier is at or above Strike, every finishing put
// payoff has crossed the barrier, so the price is the worst-of put. Otherwise
// the premium is a Monte Carlo estimate: exact terminal spots, with a
// Brownian-bridge bisection for crossings between the endpoints.
type WorstOfDownInPut struct {
	Underliers  []Underlier
	Correlation [][]float64
	Strike      float64
	Barrier     float64
	Expiry      time.Time
	Notional    float64
	DayCount    daycount.Convention
	Paths       int
	Seed        uint64
}

// WorstOfDownInQuote is the price of a worst-of down-and-in put.
// KnockInProb is the probability that the barrier is touched. For a Monte
// Carlo price, Deltas and Vegas are common-random-number bumps.
type WorstOfDownInQuote struct {
	WorstOfQuote
	KnockInProb float64
}

// TheoreticalPrice reports the premium in Result.Dirty.
func (p WorstOfDownInPut) TheoreticalPrice(asOf time.Time, curve discount.Curve) (pricing.Result, error) {
	q, err := p.Price(asOf, curve)
	if err != nil {
		return pricing.Result{}, err
	}
	return pricing.Result{Dirty: q.Premium, Clean: q.Premium}, nil
}

// Price returns the premium of the worst-of down-and-in put.
func (p WorstOfDownInPut) Price(asOf time.Time, curve discount.Curve) (WorstOfDownInQuote, error) {
	if err := p.validate(); err != nil {
		return WorstOfDownInQuote{}, err
	}
	yearFrac, df, err := p.resolve(asOf, curve)
	if err != nil {
		return WorstOfDownInQuote{}, err
	}
	if len(p.Underliers) == 1 {
		return p.priceOne(yearFrac, df)
	}
	perf := currentPerf(p.Underliers)
	in := touched(perf, p.Barrier)
	if yearFrac == 0 {
		if !in {
			return p.expiredOut(df), nil
		}
		q, err := p.asPut().Price(asOf, curve)
		if err != nil {
			return WorstOfDownInQuote{}, err
		}
		return WorstOfDownInQuote{WorstOfQuote: q, KnockInProb: 1}, nil
	}
	if p.allZeroVol() {
		return p.priceZeroVol(asOf, curve, yearFrac, df, perf)
	}
	if in || p.Barrier >= p.Strike {
		q, err := p.asPut().Price(asOf, curve)
		if err != nil {
			return WorstOfDownInQuote{}, err
		}
		knock := 1.0
		if !in {
			knock = p.simulate(yearFrac, df).KnockInProb
		}
		return WorstOfDownInQuote{WorstOfQuote: q, KnockInProb: knock}, nil
	}
	quote := p.simulate(yearFrac, df)
	p.fillGreeks(yearFrac, df, &quote)
	return quote, nil
}

func (p WorstOfDownInPut) validate() error {
	if err := p.asPut().validate(); err != nil {
		return err
	}
	if !(p.Barrier > 0) || math.IsNaN(p.Barrier) || math.IsInf(p.Barrier, 0) {
		return ErrInvalidBarrier
	}
	return nil
}

func (p WorstOfDownInPut) resolve(asOf time.Time, curve discount.Curve) (float64, float64, error) {
	first := p.Underliers[0]
	return contract{
		Spot: first.Spot, Strike: p.Strike, Expiry: p.Expiry, Volatility: first.Volatility,
		DividendYield: first.DividendYield, Quantity: p.Notional, DayCount: p.DayCount,
	}.resolve(asOf, curve)
}

func (p WorstOfDownInPut) asPut() WorstOfPut {
	return WorstOfPut{
		Underliers: p.Underliers, Correlation: p.Correlation, Strike: p.Strike,
		Expiry: p.Expiry, Notional: p.Notional, DayCount: p.DayCount,
		Paths: p.Paths, Seed: p.Seed,
	}
}

func (p WorstOfDownInPut) priceOne(yearFrac, df float64) (WorstOfDownInQuote, error) {
	u := p.Underliers[0]
	initial := initialLevel(u)
	q, err := priceDownInPut(u.Spot, p.Strike*initial, p.Barrier*initial, yearFrac, df, u.Volatility, u.DividendYield, p.Notional/initial)
	if err != nil {
		return WorstOfDownInQuote{}, err
	}
	perf := u.Spot / initial
	fwd := perf
	if yearFrac > 0 {
		fwd = perf * math.Exp(-u.DividendYield*yearFrac) / df
	}
	return WorstOfDownInQuote{
		WorstOfQuote: WorstOfQuote{
			Premium: q.Premium, DiscountFactor: df, YearFraction: yearFrac, Rate: q.Rate,
			Deltas: []float64{q.Delta}, Vegas: []float64{q.Vega},
			ForwardPerf: []float64{fwd}, WorstProb: []float64{1},
			ExerciseValue: q.ExerciseValue, TimeValue: q.TimeValue,
		},
		KnockInProb: downHitProb(perf, p.Barrier, yearFrac, df, u.Volatility, u.DividendYield),
	}, nil
}

func (p WorstOfDownInPut) expiredOut(df float64) WorstOfDownInQuote {
	n := len(p.Underliers)
	return WorstOfDownInQuote{
		WorstOfQuote: WorstOfQuote{
			DiscountFactor: df,
			Deltas:         make([]float64, n),
			Vegas:          make([]float64, n),
			ForwardPerf:    currentPerf(p.Underliers),
			WorstProb:      make([]float64, n),
		},
	}
}

func (p WorstOfDownInPut) priceZeroVol(asOf time.Time, curve discount.Curve, yearFrac, df float64, perf []float64) (WorstOfDownInQuote, error) {
	hit := touched(perf, p.Barrier)
	if !hit {
		for i, u := range p.Underliers {
			perfT := perf[i] * math.Exp(-u.DividendYield*yearFrac) / df
			if math.Min(perf[i], perfT) <= p.Barrier {
				hit = true
				break
			}
		}
	}
	if !hit {
		q := p.expiredOut(df)
		q.YearFraction = yearFrac
		if yearFrac > 0 {
			q.Rate = -math.Log(df) / yearFrac
		}
		for i, u := range p.Underliers {
			q.ForwardPerf[i] = perf[i] * math.Exp(-u.DividendYield*yearFrac) / df
		}
		return q, nil
	}
	wq, err := p.asPut().Price(asOf, curve)
	if err != nil {
		return WorstOfDownInQuote{}, err
	}
	return WorstOfDownInQuote{WorstOfQuote: wq, KnockInProb: 1}, nil
}

func (p WorstOfDownInPut) allZeroVol() bool {
	for _, u := range p.Underliers {
		if u.Volatility > 0 {
			return false
		}
	}
	return true
}

func (p WorstOfDownInPut) pathCount() int {
	if p.Paths == 0 {
		return DefaultPaths
	}
	return p.Paths
}

func (p WorstOfDownInPut) simulate(yearFrac, df float64) WorstOfDownInQuote {
	n := len(p.Underliers)
	paths := p.pathCount()
	chol, err := cholesky(p.Correlation, n)
	if err != nil {
		panic(err)
	}
	sqrtT := math.Sqrt(yearFrac)
	rate := -math.Log(df) / yearFrac
	perf0 := currentPerf(p.Underliers)
	fwd := make([]float64, n)
	vol := make([]float64, n)
	for i, u := range p.Underliers {
		fwd[i] = perf0[i] * math.Exp(-u.DividendYield*yearFrac) / df
		vol[i] = u.Volatility
	}
	eng := newBridge(vol, chol, perf0, p.Barrier)
	z := make([]float64, n)
	corr := make([]float64, n)
	perf := make([]float64, n)
	var sum, sumSq, hits float64
	worstCount := make([]float64, n)
	forwardSum := make([]float64, n)

	for k := 0; k < paths; k++ {
		rng := rand.NewPCG(p.Seed, uint64(k)+0x9e3779b97f4a7c15)
		for i := range z {
			z[i] = nextNormal(rng)
		}
		applyChol(chol, z, corr)
		saved := *rng
		pay1, hit1, worst1 := eng.sample(corr, 1, fwd, sqrtT, yearFrac, p.Strike, perf, rng)
		*rng = saved
		pay2, hit2, worst2 := eng.sample(corr, -1, fwd, sqrtT, yearFrac, p.Strike, perf, rng)
		pair := 0.5 * (pay1 + pay2)
		sum += pair
		sumSq += pair * pair
		if hit1 {
			hits++
		}
		if hit2 {
			hits++
		}
		worstCount[worst1]++
		worstCount[worst2]++
		for i := range forwardSum {
			forwardSum[i] += fwd[i] * math.Exp(-0.5*vol[i]*vol[i]*yearFrac+vol[i]*sqrtT*corr[i])
			forwardSum[i] += fwd[i] * math.Exp(-0.5*vol[i]*vol[i]*yearFrac+vol[i]*sqrtT*(-corr[i]))
		}
	}
	m := float64(paths)
	mean := sum / m
	variance := 0.0
	if paths > 1 {
		variance = math.Max(sumSq/m-mean*mean, 0) * m / (m - 1)
	}
	scale := df * p.Notional
	premium := scale * mean
	q := WorstOfDownInQuote{
		WorstOfQuote: WorstOfQuote{
			Premium: premium, StdErr: scale * math.Sqrt(variance/m),
			DiscountFactor: df, YearFraction: yearFrac, Rate: rate, Paths: paths,
			Deltas: make([]float64, n), Vegas: make([]float64, n),
			ForwardPerf: make([]float64, n), WorstProb: make([]float64, n),
			TimeValue: premium,
		},
		KnockInProb: hits / (2 * m),
	}
	samples := 2 * m
	for i := range q.ForwardPerf {
		q.ForwardPerf[i] = forwardSum[i] / samples
		q.WorstProb[i] = worstCount[i] / samples
	}
	return q
}

func (p WorstOfDownInPut) fillGreeks(yearFrac, df float64, q *WorstOfDownInQuote) {
	const rel = 1e-4
	for i, u := range p.Underliers {
		hS := math.Max(u.Spot*rel, 1e-6)
		upSpot, dnSpot := u.Spot+hS, u.Spot-hS
		if dnSpot <= 0 || dnSpot/initialLevel(u) <= p.Barrier {
			up := p.withSpot(i, upSpot).simulate(yearFrac, df).Premium
			q.Deltas[i] = (up - q.Premium) / hS
		} else {
			up := p.withSpot(i, upSpot).simulate(yearFrac, df).Premium
			dn := p.withSpot(i, dnSpot).simulate(yearFrac, df).Premium
			q.Deltas[i] = (up - dn) / (2 * hS)
		}
		hV := 1e-4
		if u.Volatility <= hV {
			up := p.withVol(i, u.Volatility+hV).simulate(yearFrac, df).Premium
			q.Vegas[i] = (up - q.Premium) / hV
			continue
		}
		up := p.withVol(i, u.Volatility+hV).simulate(yearFrac, df).Premium
		dn := p.withVol(i, u.Volatility-hV).simulate(yearFrac, df).Premium
		q.Vegas[i] = (up - dn) / (2 * hV)
	}
}

func (p WorstOfDownInPut) withSpot(i int, spot float64) WorstOfDownInPut {
	us := append([]Underlier(nil), p.Underliers...)
	// A zero initial means "struck at the current spot". Hold that fixing
	// constant so a spot bump moves performance, matching the worst-of put.
	if us[i].Initial == 0 {
		us[i].Initial = us[i].Spot
	}
	us[i].Spot = spot
	p.Underliers = us
	return p
}

func (p WorstOfDownInPut) withVol(i int, vol float64) WorstOfDownInPut {
	us := append([]Underlier(nil), p.Underliers...)
	us[i].Volatility = vol
	p.Underliers = us
	return p
}

func applyChol(chol [][]float64, z, corr []float64) {
	for i := range corr {
		s := 0.0
		for j := 0; j <= i; j++ {
			s += chol[i][j] * z[j]
		}
		corr[i] = s
	}
}

type bridge struct {
	vol      []float64
	chol     [][]float64
	barrier  float64
	start    []float64
	end      []float64
	mid      [][]float64
	ps       []float64
	z        []float64
	corr     []float64
	maxDepth int
}

func newBridge(vol []float64, chol [][]float64, perf0 []float64, barrier float64) *bridge {
	n := len(vol)
	const maxDepth = 8
	b := &bridge{
		vol: vol, chol: chol, barrier: barrier,
		start: make([]float64, n), end: make([]float64, n),
		mid: make([][]float64, maxDepth), ps: make([]float64, n),
		z: make([]float64, n), corr: make([]float64, n),
		maxDepth: maxDepth,
	}
	for i, perf := range perf0 {
		b.start[i] = math.Log(perf / barrier)
	}
	for d := range b.mid {
		b.mid[d] = make([]float64, n)
	}
	return b
}

func (b *bridge) sample(shock []float64, sign float64, fwd []float64, sqrtT, yearFrac, strike float64, perf []float64, rng *rand.PCG) (pay float64, hit bool, worst int) {
	for i := range perf {
		perf[i] = fwd[i] * math.Exp(-0.5*b.vol[i]*b.vol[i]*yearFrac+b.vol[i]*sqrtT*sign*shock[i])
		b.end[i] = math.Log(perf[i] / b.barrier)
		if i == 0 || perf[i] < perf[worst] {
			worst = i
		}
	}
	hit = b.crossed(b.start, b.end, yearFrac, 0, rng)
	if hit {
		pay = math.Max(strike-perf[worst], 0)
	}
	return pay, hit, worst
}

// crossed reports whether any log-performance, given its endpoints, touches
// the barrier on an interval of length dt. The coordinates are
// log(performance/barrier), so the barrier is zero.
func (b *bridge) crossed(left, right []float64, dt float64, depth int, rng *rand.PCG) bool {
	n := len(left)
	for i := 0; i < n; i++ {
		if left[i] <= 0 || right[i] <= 0 {
			return true
		}
	}
	active := -1
	var maxP float64
	for i := 0; i < n; i++ {
		sig2 := b.vol[i] * b.vol[i] * dt
		if sig2 == 0 {
			b.ps[i] = 0
			continue
		}
		b.ps[i] = math.Exp(-2 * left[i] * right[i] / sig2)
		if b.ps[i] > maxP {
			maxP = b.ps[i]
		}
		if b.ps[i] > 1e-12 {
			if active >= 0 {
				active = -2
			} else if active == -1 {
				active = i
			}
		}
	}
	if maxP < 1e-14 {
		return false
	}
	if active >= 0 {
		return nextUniform(rng) < b.ps[active]
	}
	if depth >= b.maxDepth {
		survive := 1.0
		for i := 0; i < n; i++ {
			survive *= 1 - b.ps[i]
		}
		return nextUniform(rng) > survive
	}
	scale := math.Sqrt(dt) / 2
	for i := range b.z {
		b.z[i] = nextNormal(rng)
	}
	applyChol(b.chol, b.z, b.corr)
	mid := b.mid[depth]
	for i := 0; i < n; i++ {
		mid[i] = 0.5*(left[i]+right[i]) + b.vol[i]*scale*b.corr[i]
		if mid[i] <= 0 {
			return true
		}
	}
	if b.crossed(left, mid, dt/2, depth+1, rng) {
		return true
	}
	return b.crossed(mid, right, dt/2, depth+1, rng)
}

func currentPerf(us []Underlier) []float64 {
	out := make([]float64, len(us))
	for i, u := range us {
		out[i] = u.Spot / initialLevel(u)
	}
	return out
}

func initialLevel(u Underlier) float64 {
	if u.Initial == 0 {
		return u.Spot
	}
	return u.Initial
}

func touched(perf []float64, barrier float64) bool {
	for _, x := range perf {
		if x <= barrier {
			return true
		}
	}
	return false
}

// downHitProb is the probability that geometric Brownian motion touches a
// down barrier. Performance and barrier are in the same units.
func downHitProb(perf, barrier, yearFrac, df, vol, dividend float64) float64 {
	if perf <= barrier {
		return 1
	}
	if yearFrac == 0 || vol == 0 {
		fwd := perf
		if yearFrac > 0 {
			fwd = perf * math.Exp(-dividend*yearFrac) / df
		}
		if math.Min(perf, fwd) <= barrier {
			return 1
		}
		return 0
	}
	nu := -math.Log(df)/yearFrac - dividend - 0.5*vol*vol
	dist := math.Log(perf / barrier)
	s := vol * math.Sqrt(yearFrac)
	return normCDF((-dist+nu*yearFrac)/s) + math.Exp(-2*nu*dist/(vol*vol))*normCDF((-dist-nu*yearFrac)/s)
}

func nextUniform(rng *rand.PCG) float64 {
	return (float64(rng.Uint64()>>11) + 0.5) / (1 << 53)
}

func nextNormal(rng *rand.PCG) float64 {
	return math.Sqrt(-2*math.Log(nextUniform(rng))) * math.Cos(2*math.Pi*nextUniform(rng))
}
