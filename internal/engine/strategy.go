package engine

import (
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/papadacodr/kryptic-gopha/internal/models"
	"github.com/rs/zerolog/log"
	"github.com/shopspring/decimal"
)

// Strategy is the interface every trading algorithm must satisfy.
// Analyze is called on every completed bar with the symbol's full OHLCV history.
// Return nil when no trade is warranted.
type Strategy interface {
	Analyze(symbol string, candles []models.Candle) *models.Signal
}

// EMAStrategy is a simple EMA-crossover reference implementation kept for
// backtesting comparisons. Not recommended for live use: it lacks a macro
// trend filter and emits signals at constant confidence regardless of regime.
type EMAStrategy struct {
	ShortPeriod int
	LongPeriod  int
	Threshold   decimal.Decimal
}

func (s *EMAStrategy) Analyze(symbol string, candles []models.Candle) *models.Signal {
	prices := candleCloses(candles)
	if len(prices) < s.LongPeriod {
		return nil
	}
	shortEMA := calculateEMA(prices, s.ShortPeriod)
	longEMA := calculateEMA(prices, s.LongPeriod)

	thresholdMul := decimal.NewFromInt(1).Add(s.Threshold)
	if shortEMA.GreaterThan(longEMA.Mul(thresholdMul)) {
		return &models.Signal{
			Symbol:     symbol,
			Price:      prices[len(prices)-1],
			Direction:  "BUY",
			Reason:     "EMA Bullish Crossover",
			Confidence: 0.75,
			Timestamp:  time.Now(),
		}
	}
	thresholdMulSell := decimal.NewFromInt(1).Sub(s.Threshold)
	if shortEMA.LessThan(longEMA.Mul(thresholdMulSell)) {
		return &models.Signal{
			Symbol:     symbol,
			Price:      prices[len(prices)-1],
			Direction:  "SELL",
			Reason:     "EMA Bearish Crossover",
			Confidence: 0.75,
			Timestamp:  time.Now(),
		}
	}
	return nil
}

// EfficientMultiFactorStrategy is the production strategy. It applies four
// complementary filters in sequence:
//
//  1. Macro trend filter: price must be on the correct side of the 200-period EMA.
//     This is the highest-value single filter — it suppresses the majority of
//     losing counter-trend entries.
//
//  2. ADX regime gate: ADX(14) must exceed ADXThreshold (default 25). Below
//     that the market is ranging and EMA crossovers are mostly noise.
//
//  3. Entry trigger: short EMA crosses long EMA in the direction of the macro
//     trend (MACD-equivalent with 12/26 defaults).
//
//  4. Momentum gate: RSI(14) must be below 70 for longs and above 30 for shorts
//     to avoid entering near exhaustion points.
//
//  5. Volume confirmation: the signal candle's volume must exceed 1.2x the
//     20-bar EMA of volume. Bypassed when volume data is unavailable (zero).
//
// All indicator state is maintained incrementally (O(1) per bar) using Wilder's
// exponential smoothing. State is seeded on first contact with each symbol using
// the full warmup history.
//
// Concurrency: each symbol has its own mutex so symbols can be analysed in
// parallel. The top-level mu guards only the symMu map itself.
type EfficientMultiFactorStrategy struct {
	ShortPeriod   int
	LongPeriod    int
	RSIPeriod     int
	MacroPeriod   int
	ADXPeriod     int
	ADXThreshold  float64
	VolMultiplier float64

	mu          sync.Mutex
	symMu       map[string]*sync.Mutex
	lastEMA     map[string]map[int]decimal.Decimal
	lastAvgGain map[string]decimal.Decimal
	lastAvgLoss map[string]decimal.Decimal
	initialized map[string]bool

	// ADX/ATR state — all float64 for performance; only the final ATR value
	// is converted to decimal.Decimal when placed on the outgoing Signal.
	smTR      map[string]float64
	smPlusDM  map[string]float64
	smMinusDM map[string]float64
	adxValue  map[string]float64
	adxReady  map[string]bool
	prevHigh  map[string]float64
	prevLow   map[string]float64
	prevClose map[string]float64

	// Volume EMA state
	volEMA    map[string]float64
	volPeriod int
}

func NewEfficientStrategy(short, long, rsi int) *EfficientMultiFactorStrategy {
	return &EfficientMultiFactorStrategy{
		ShortPeriod:   short,
		LongPeriod:    long,
		RSIPeriod:     rsi,
		MacroPeriod:   200,
		ADXPeriod:     14,
		ADXThreshold:  25.0,
		VolMultiplier: 1.2,
		volPeriod:     20,
		symMu:         make(map[string]*sync.Mutex),
		lastEMA:       make(map[string]map[int]decimal.Decimal),
		lastAvgGain:   make(map[string]decimal.Decimal),
		lastAvgLoss:   make(map[string]decimal.Decimal),
		initialized:   make(map[string]bool),
		smTR:          make(map[string]float64),
		smPlusDM:      make(map[string]float64),
		smMinusDM:     make(map[string]float64),
		adxValue:      make(map[string]float64),
		adxReady:      make(map[string]bool),
		prevHigh:      make(map[string]float64),
		prevLow:       make(map[string]float64),
		prevClose:     make(map[string]float64),
		volEMA:        make(map[string]float64),
	}
}

// symbolLock returns the per-symbol mutex, creating it on first access.
// The global mu guards only the symMu map entry creation.
func (s *EfficientMultiFactorStrategy) symbolLock(symbol string) *sync.Mutex {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.symMu[symbol] == nil {
		s.symMu[symbol] = &sync.Mutex{}
	}
	return s.symMu[symbol]
}

func (s *EfficientMultiFactorStrategy) Analyze(symbol string, candles []models.Candle) *models.Signal {
	mu := s.symbolLock(symbol)
	mu.Lock()
	defer mu.Unlock()

	if len(candles) < s.MacroPeriod || len(candles) < s.RSIPeriod+1 {
		return nil
	}

	prices := candleCloses(candles)
	currentPrice := prices[len(prices)-1]

	// Seed all indicator state on first contact with this symbol.
	if _, ok := s.lastEMA[symbol]; !ok {
		s.lastEMA[symbol] = make(map[int]decimal.Decimal)
		warmup := prices[:len(prices)-1]
		s.lastEMA[symbol][s.ShortPeriod] = calculateEMA(warmup, s.ShortPeriod)
		s.lastEMA[symbol][s.LongPeriod] = calculateEMA(warmup, s.LongPeriod)
		s.lastEMA[symbol][s.MacroPeriod] = calculateEMA(warmup, s.MacroPeriod)
		s.initRSI(symbol, warmup)
		s.initADX(symbol, candles[:len(candles)-1])
		s.initVolEMA(symbol, candles[:len(candles)-1])
	}

	shortEMA := updateEMA(s.lastEMA[symbol][s.ShortPeriod], currentPrice, s.ShortPeriod)
	longEMA := updateEMA(s.lastEMA[symbol][s.LongPeriod], currentPrice, s.LongPeriod)
	macroEMA := updateEMA(s.lastEMA[symbol][s.MacroPeriod], currentPrice, s.MacroPeriod)
	s.lastEMA[symbol][s.ShortPeriod] = shortEMA
	s.lastEMA[symbol][s.LongPeriod] = longEMA
	s.lastEMA[symbol][s.MacroPeriod] = macroEMA

	rsi := s.calculateIncrementalRSI(symbol, prices)
	rsiFloat, _ := rsi.Float64()

	curr := candles[len(candles)-1]
	atrValue := s.updateADX(symbol, curr)

	currVol, _ := curr.Volume.Float64()
	volEMA := s.updateVolEMA(symbol, currVol)

	// Confidence anchored at 0.5, scaled by RSI distance from 50.
	rsiDeviation := math.Abs(rsiFloat - 50)
	confidence := 0.5 + (0.4 * (rsiDeviation / 50))
	if boosted := confidence * 1.2; boosted < 1.0 {
		confidence = boosted
	} else {
		confidence = 1.0
	}

	isBullMarket := currentPrice.GreaterThan(macroEMA)
	isBearMarket := currentPrice.LessThan(macroEMA)

	// ADX regime gate: bypass only while ADX hasn't accumulated enough history.
	if s.adxReady[symbol] && s.adxValue[symbol] < s.ADXThreshold {
		return nil
	}

	// Volume confirmation: bypass when volume data is unavailable (zero feed).
	volOK := currVol == 0 || volEMA == 0 || currVol >= volEMA*s.VolMultiplier

	atrDecimal := decimal.NewFromFloat(atrValue)

	if isBullMarket && shortEMA.GreaterThan(longEMA) && rsiFloat < 70 && volOK {
		return &models.Signal{
			Symbol:     symbol,
			Price:      currentPrice,
			Direction:  "BUY",
			Reason:     fmt.Sprintf("Trend:UP (+200EMA) | RSI:%.2f | ADX:%.2f", rsiFloat, s.adxValue[symbol]),
			Confidence: confidence,
			Timestamp:  time.Now(),
			ATR:        atrDecimal,
		}
	}

	if isBearMarket && shortEMA.LessThan(longEMA) && rsiFloat > 30 && volOK {
		return &models.Signal{
			Symbol:     symbol,
			Price:      currentPrice,
			Direction:  "SELL",
			Reason:     fmt.Sprintf("Trend:DOWN (-200EMA) | RSI:%.2f | ADX:%.2f", rsiFloat, s.adxValue[symbol]),
			Confidence: confidence,
			Timestamp:  time.Now(),
			ATR:        atrDecimal,
		}
	}

	return nil
}

// candleCloses extracts the close price from each candle in chronological order.
func candleCloses(candles []models.Candle) []decimal.Decimal {
	out := make([]decimal.Decimal, len(candles))
	for i, c := range candles {
		out[i] = c.Close
	}
	return out
}

// computeDM returns the True Range, +DM, and -DM for a single bar transition.
// TR = max(H-L, |H-prevClose|, |L-prevClose|).
// +DM = H-prevH when that move exceeds the downward move; -DM is the mirror.
func computeDM(curr, prev models.Candle) (tr, plusDM, minusDM float64) {
	h, _ := curr.High.Float64()
	l, _ := curr.Low.Float64()
	ph, _ := prev.High.Float64()
	pl, _ := prev.Low.Float64()
	pc, _ := prev.Close.Float64()

	tr = math.Max(h-l, math.Max(math.Abs(h-pc), math.Abs(l-pc)))
	upMove := h - ph
	downMove := pl - l
	if upMove > downMove && upMove > 0 {
		plusDM = upMove
	}
	if downMove > upMove && downMove > 0 {
		minusDM = downMove
	}
	return
}

// computeDX returns the directional index given Wilder-smoothed TR/+DM/-DM.
func computeDX(smTR, smPlusDM, smMinusDM float64) float64 {
	if smTR == 0 {
		return 0
	}
	plusDI := 100 * smPlusDM / smTR
	minusDI := 100 * smMinusDM / smTR
	diSum := plusDI + minusDI
	if diSum == 0 {
		return 0
	}
	return 100 * math.Abs(plusDI-minusDI) / diSum
}

// initADX seeds the Wilder-smoothed ADX/ATR state from a candle history batch.
// Requires at least 2*ADXPeriod candles to produce a valid initial ADX value;
// with fewer candles adxReady stays false and the ADX gate is bypassed.
func (s *EfficientMultiFactorStrategy) initADX(symbol string, candles []models.Candle) {
	if len(candles) < 2 {
		return
	}
	n := float64(s.ADXPeriod)

	// Wilder's initial sums: plain sum over the first ADXPeriod bars.
	var sumTR, sumPlusDM, sumMinusDM float64
	limit := s.ADXPeriod
	if limit >= len(candles) {
		limit = len(candles) - 1
	}
	for i := 1; i <= limit; i++ {
		tr, pDM, mDM := computeDM(candles[i], candles[i-1])
		sumTR += tr
		sumPlusDM += pDM
		sumMinusDM += mDM
	}
	smTR := sumTR
	smPlusDM := sumPlusDM
	smMinusDM := sumMinusDM

	// Accumulate DX values until we have enough to seed ADX.
	var dxValues []float64
	if smTR > 0 {
		dxValues = append(dxValues, computeDX(smTR, smPlusDM, smMinusDM))
	}

	adxValue := 0.0
	adxSeeded := false
	for i := s.ADXPeriod + 1; i < len(candles); i++ {
		tr, pDM, mDM := computeDM(candles[i], candles[i-1])
		smTR = smTR - smTR/n + tr
		smPlusDM = smPlusDM - smPlusDM/n + pDM
		smMinusDM = smMinusDM - smMinusDM/n + mDM
		if smTR > 0 {
			dx := computeDX(smTR, smPlusDM, smMinusDM)
			if !adxSeeded {
				dxValues = append(dxValues, dx)
				if len(dxValues) >= s.ADXPeriod {
					var sum float64
					for _, v := range dxValues {
						sum += v
					}
					adxValue = sum / float64(len(dxValues))
					adxSeeded = true
				}
			} else {
				adxValue = (adxValue*(n-1) + dx) / n
			}
		}
	}

	s.smTR[symbol] = smTR
	s.smPlusDM[symbol] = smPlusDM
	s.smMinusDM[symbol] = smMinusDM
	s.adxValue[symbol] = adxValue
	s.adxReady[symbol] = adxSeeded
	if !adxSeeded {
		log.Warn().
			Str("symbol", symbol).
			Int("candles", len(candles)).
			Int("required", 2*s.ADXPeriod).
			Msg("ADX gate inactive: insufficient warmup history, ADX filter bypassed until more bars accumulate")
	}

	// prevHigh/Low/Close must always be set so updateADX has valid prior values.
	last := candles[len(candles)-1]
	h, _ := last.High.Float64()
	l, _ := last.Low.Float64()
	c, _ := last.Close.Float64()
	s.prevHigh[symbol] = h
	s.prevLow[symbol] = l
	s.prevClose[symbol] = c
}

// updateADX advances the ADX/ATR state by one bar using Wilder's smoothing and
// returns the current ATR expressed in price units (smTR / ADXPeriod).
func (s *EfficientMultiFactorStrategy) updateADX(symbol string, curr models.Candle) float64 {
	h, _ := curr.High.Float64()
	l, _ := curr.Low.Float64()
	c, _ := curr.Close.Float64()
	n := float64(s.ADXPeriod)

	tr := math.Max(h-l, math.Max(math.Abs(h-s.prevClose[symbol]), math.Abs(l-s.prevClose[symbol])))
	upMove := h - s.prevHigh[symbol]
	downMove := s.prevLow[symbol] - l
	var plusDM, minusDM float64
	if upMove > downMove && upMove > 0 {
		plusDM = upMove
	}
	if downMove > upMove && downMove > 0 {
		minusDM = downMove
	}

	smTR := s.smTR[symbol] - s.smTR[symbol]/n + tr
	smPlusDM := s.smPlusDM[symbol] - s.smPlusDM[symbol]/n + plusDM
	smMinusDM := s.smMinusDM[symbol] - s.smMinusDM[symbol]/n + minusDM
	s.smTR[symbol] = smTR
	s.smPlusDM[symbol] = smPlusDM
	s.smMinusDM[symbol] = smMinusDM

	if smTR > 0 {
		dx := computeDX(smTR, smPlusDM, smMinusDM)
		s.adxValue[symbol] = (s.adxValue[symbol]*(n-1) + dx) / n
	}

	s.prevHigh[symbol] = h
	s.prevLow[symbol] = l
	s.prevClose[symbol] = c

	if n > 0 {
		return smTR / n
	}
	return 0
}

// initVolEMA seeds the volume EMA from a candle history batch using standard
// exponential smoothing with k = 2/(volPeriod+1).
func (s *EfficientMultiFactorStrategy) initVolEMA(symbol string, candles []models.Candle) {
	if len(candles) == 0 {
		return
	}
	k := 2.0 / (float64(s.volPeriod) + 1.0)
	vol0, _ := candles[0].Volume.Float64()
	ema := vol0
	for _, c := range candles[1:] {
		v, _ := c.Volume.Float64()
		ema = ema + k*(v-ema)
	}
	s.volEMA[symbol] = ema
}

// updateVolEMA advances the volume EMA by one bar and returns the updated value.
func (s *EfficientMultiFactorStrategy) updateVolEMA(symbol string, vol float64) float64 {
	k := 2.0 / (float64(s.volPeriod) + 1.0)
	updated := s.volEMA[symbol] + k*(vol-s.volEMA[symbol])
	s.volEMA[symbol] = updated
	return updated
}

// updateEMA applies the standard EMA recurrence: EMA = prev + k*(price - prev)
// where k = 2/(period+1). O(1) per call.
func updateEMA(prevEMA, currentPrice decimal.Decimal, period int) decimal.Decimal {
	multiplier := decimal.NewFromFloat(2.0 / (float64(period) + 1.0))
	return currentPrice.Sub(prevEMA).Mul(multiplier).Add(prevEMA)
}

// calculateEMA seeds an EMA over a full price slice. Only called once per symbol
// at initialisation; all subsequent updates use updateEMA.
func calculateEMA(prices []decimal.Decimal, period int) decimal.Decimal {
	if len(prices) == 0 {
		return decimal.Zero
	}
	multiplier := decimal.NewFromFloat(2.0 / (float64(period) + 1.0))
	ema := prices[0]
	for i := 1; i < len(prices); i++ {
		ema = prices[i].Sub(ema).Mul(multiplier).Add(ema)
	}
	return ema
}

// initRSI seeds avgGain and avgLoss with Wilder's initial SMA, then rolls
// forward through all remaining prices to bring state current. Called once
// per symbol at initialisation.
func (s *EfficientMultiFactorStrategy) initRSI(symbol string, prices []decimal.Decimal) {
	if len(prices) < s.RSIPeriod+1 {
		return
	}
	totalGain := decimal.Zero
	totalLoss := decimal.Zero
	for i := 1; i <= s.RSIPeriod; i++ {
		change := prices[i].Sub(prices[i-1])
		if change.IsPositive() {
			totalGain = totalGain.Add(change)
		} else {
			totalLoss = totalLoss.Sub(change)
		}
	}
	n := decimal.NewFromInt(int64(s.RSIPeriod))
	nm1 := decimal.NewFromInt(int64(s.RSIPeriod - 1))
	s.lastAvgGain[symbol] = totalGain.Div(n)
	s.lastAvgLoss[symbol] = totalLoss.Div(n)

	for i := s.RSIPeriod + 1; i < len(prices); i++ {
		change := prices[i].Sub(prices[i-1])
		gain, loss := decimal.Zero, decimal.Zero
		if change.IsPositive() {
			gain = change
		} else {
			loss = change.Neg()
		}
		s.lastAvgGain[symbol] = s.lastAvgGain[symbol].Mul(nm1).Add(gain).Div(n)
		s.lastAvgLoss[symbol] = s.lastAvgLoss[symbol].Mul(nm1).Add(loss).Div(n)
	}
	s.initialized[symbol] = true
}

// calculateIncrementalRSI advances the Wilder-smoothed RSI by one bar.
func (s *EfficientMultiFactorStrategy) calculateIncrementalRSI(symbol string, prices []decimal.Decimal) decimal.Decimal {
	n := decimal.NewFromInt(int64(s.RSIPeriod))
	nm1 := decimal.NewFromInt(int64(s.RSIPeriod - 1))

	if !s.initialized[symbol] {
		s.initRSI(symbol, prices)
	} else {
		change := prices[len(prices)-1].Sub(prices[len(prices)-2])
		gain, loss := decimal.Zero, decimal.Zero
		if change.IsPositive() {
			gain = change
		} else {
			loss = change.Neg()
		}
		s.lastAvgGain[symbol] = s.lastAvgGain[symbol].Mul(nm1).Add(gain).Div(n)
		s.lastAvgLoss[symbol] = s.lastAvgLoss[symbol].Mul(nm1).Add(loss).Div(n)
	}

	if s.lastAvgLoss[symbol].IsZero() {
		return decimal.NewFromInt(100)
	}
	rs := s.lastAvgGain[symbol].Div(s.lastAvgLoss[symbol])
	return decimal.NewFromInt(100).Sub(decimal.NewFromInt(100).Div(decimal.NewFromInt(1).Add(rs)))
}
