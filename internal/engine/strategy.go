package engine

import (
	"fmt"
	"sync"
	"time"

	"github.com/papadacodr/kryptic-gopha/internal/models"
	"github.com/shopspring/decimal"
)

// Strategy is the interface every trading algorithm must satisfy.
// Analyze is called on every completed bar with the symbol's full price
// history. Return nil when no trade is warranted.
type Strategy interface {
	Analyze(symbol string, prices []decimal.Decimal) *models.Signal
}

// EMAStrategy is a simple EMA-crossover strategy kept for reference and
// backtesting comparisons. It is not recommended for live use: it lacks
// a macro trend filter and emits signals at constant confidence regardless
// of market context.
type EMAStrategy struct {
	ShortPeriod int
	LongPeriod  int
	Threshold   decimal.Decimal
}

func (s *EMAStrategy) Analyze(symbol string, prices []decimal.Decimal) *models.Signal {
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

// EfficientMultiFactorStrategy is the production strategy. It uses three
// complementary filters:
//
//  1. Macro trend filter: price must be on the correct side of the 200-period
//     EMA. This suppresses counter-trend entries and is the highest-value
//     single filter in the system.
//
//  2. Entry trigger: short EMA crosses long EMA in the direction of the macro
//     trend (equivalent to a MACD signal-line crossover with 12/26/9 defaults).
//
//  3. Momentum gate: RSI(14) must be below 70 for longs and above 30 for
//     shorts to avoid entering near exhaustion points.
//
// All indicator state is maintained incrementally (O(1) per bar) using
// Wilder's exponential smoothing for RSI and the standard EMA recurrence
// relation. A full recalculation is performed only on first contact with a
// new symbol.
//
// Concurrency: each symbol has its own mutex so BTCUSDT and ETHUSDT analysis
// can proceed in parallel. The top-level mu protects only the symMu map.
type EfficientMultiFactorStrategy struct {
	ShortPeriod int
	LongPeriod  int
	RSIPeriod   int
	MacroPeriod int

	mu          sync.Mutex
	symMu       map[string]*sync.Mutex
	lastEMA     map[string]map[int]decimal.Decimal
	lastAvgGain map[string]decimal.Decimal
	lastAvgLoss map[string]decimal.Decimal
	initialized map[string]bool
}

func NewEfficientStrategy(short, long, rsi int) *EfficientMultiFactorStrategy {
	return &EfficientMultiFactorStrategy{
		ShortPeriod: short,
		LongPeriod:  long,
		RSIPeriod:   rsi,
		MacroPeriod: 200,
		symMu:       make(map[string]*sync.Mutex),
		lastEMA:     make(map[string]map[int]decimal.Decimal),
		lastAvgGain: make(map[string]decimal.Decimal),
		lastAvgLoss: make(map[string]decimal.Decimal),
		initialized: make(map[string]bool),
	}
}

// symbolLock returns the per-symbol mutex, creating it on first access.
// The global mu protects only the symMu map entry creation.
func (s *EfficientMultiFactorStrategy) symbolLock(symbol string) *sync.Mutex {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.symMu[symbol] == nil {
		s.symMu[symbol] = &sync.Mutex{}
	}
	return s.symMu[symbol]
}

func (s *EfficientMultiFactorStrategy) Analyze(symbol string, prices []decimal.Decimal) *models.Signal {
	mu := s.symbolLock(symbol)
	mu.Lock()
	defer mu.Unlock()

	if len(prices) < s.MacroPeriod || len(prices) < s.RSIPeriod+1 {
		return nil
	}

	currentPrice := prices[len(prices)-1]

	if _, ok := s.lastEMA[symbol]; !ok {
		s.lastEMA[symbol] = make(map[int]decimal.Decimal)
		warmup := prices[:len(prices)-1]
		s.lastEMA[symbol][s.ShortPeriod] = calculateEMA(warmup, s.ShortPeriod)
		s.lastEMA[symbol][s.LongPeriod] = calculateEMA(warmup, s.LongPeriod)
		s.lastEMA[symbol][s.MacroPeriod] = calculateEMA(warmup, s.MacroPeriod)
		s.initRSI(symbol, warmup)
	}

	shortEMA := updateEMA(s.lastEMA[symbol][s.ShortPeriod], currentPrice, s.ShortPeriod)
	longEMA := updateEMA(s.lastEMA[symbol][s.LongPeriod], currentPrice, s.LongPeriod)
	macroEMA := updateEMA(s.lastEMA[symbol][s.MacroPeriod], currentPrice, s.MacroPeriod)
	s.lastEMA[symbol][s.ShortPeriod] = shortEMA
	s.lastEMA[symbol][s.LongPeriod] = longEMA
	s.lastEMA[symbol][s.MacroPeriod] = macroEMA

	rsi := s.calculateIncrementalRSI(symbol, prices)
	rsiFloat, _ := rsi.Float64()

	// Confidence is anchored at 0.5 and scaled by RSI distance from 50.
	// RSI near an extreme (0 or 100) adds up to 0.4 to confidence; RSI at
	// exactly 50 (no momentum) contributes nothing.
	rsiDeviation := rsiFloat - 50
	if rsiDeviation < 0 {
		rsiDeviation = -rsiDeviation
	}
	confidence := 0.5 + (0.4 * (rsiDeviation / 50))
	boosted := confidence * 1.2
	if boosted > 1.0 {
		boosted = 1.0
	}

	isBullMarket := currentPrice.GreaterThan(macroEMA)
	isBearMarket := currentPrice.LessThan(macroEMA)

	if isBullMarket && shortEMA.GreaterThan(longEMA) && rsiFloat < 70 {
		return &models.Signal{
			Symbol:     symbol,
			Price:      currentPrice,
			Direction:  "BUY",
			Reason:     fmt.Sprintf("Trend:UP (+200EMA) | RSI:%.2f", rsiFloat),
			Confidence: boosted,
			Timestamp:  time.Now(),
		}
	}

	if isBearMarket && shortEMA.LessThan(longEMA) && rsiFloat > 30 {
		return &models.Signal{
			Symbol:     symbol,
			Price:      currentPrice,
			Direction:  "SELL",
			Reason:     fmt.Sprintf("Trend:DOWN (-200EMA) | RSI:%.2f", rsiFloat),
			Confidence: boosted,
			Timestamp:  time.Now(),
		}
	}

	return nil
}

// updateEMA applies the standard EMA recurrence: EMA = prev + k*(price - prev)
// where k = 2/(period+1). O(1) per call.
func updateEMA(prevEMA, currentPrice decimal.Decimal, period int) decimal.Decimal {
	multiplier := decimal.NewFromFloat(2.0 / (float64(period) + 1.0))
	return currentPrice.Sub(prevEMA).Mul(multiplier).Add(prevEMA)
}

// calculateEMA seeds the EMA over a full price slice. Only called once per
// symbol at initialisation; all subsequent updates use updateEMA.
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

// initRSI seeds avgGain and avgLoss using Wilder's initial SMA, then applies
// Wilder's smoothing over all remaining prices to bring state current.
// Only called once per symbol.
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
// On the first call for a symbol it falls through to initRSI for full seeding.
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
