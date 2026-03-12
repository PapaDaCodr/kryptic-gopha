package engine

import (
	"fmt"
	"time"
	"github.com/papadacodr/kryptic-gopha/internal/models"
	"github.com/shopspring/decimal"
)

// Strategy defines the interface for trading algorithms
type Strategy interface {
	Analyze(symbol string, prices []decimal.Decimal) *models.Signal
}

// EMAStrategy is a basic but effective trend-following strategy
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

	// Bullish Crossover: shortEMA > longEMA*(1+s.Threshold)
	thresholdMultiplier := decimal.NewFromInt(1).Add(s.Threshold)
	if shortEMA.GreaterThan(longEMA.Mul(thresholdMultiplier)) {
		price := prices[len(prices)-1]
		return &models.Signal{
			Symbol:     symbol,
			Price:      price,
			Direction:  "BUY",
			Reason:     "EMA Bullish Crossover",
			Confidence: 0.75,
			Timestamp:  time.Now(),
			TP:         price.Mul(decimal.NewFromFloat(1.05)),
			SL:         price.Mul(decimal.NewFromFloat(0.98)),
		}
	}

	// Bearish Crossover: shortEMA < longEMA*(1-s.Threshold)
	thresholdMultiplierSell := decimal.NewFromInt(1).Sub(s.Threshold)
	if shortEMA.LessThan(longEMA.Mul(thresholdMultiplierSell)) {
		price := prices[len(prices)-1]
		return &models.Signal{
			Symbol:     symbol,
			Price:      price,
			Direction:  "SELL",
			Reason:     "EMA Bearish Crossover",
			Confidence: 0.75,
			Timestamp:  time.Now(),
			TP:         price.Mul(decimal.NewFromFloat(0.95)),
			SL:         price.Mul(decimal.NewFromFloat(1.02)),
		}
	}

	return nil
}

// EfficientMultiFactorStrategy uses caching to avoid redundant calculations
type EfficientMultiFactorStrategy struct {
	ShortPeriod int
	LongPeriod  int
	RSIPeriod   int
	MacroPeriod int // e.g. 200 EMA for Macro Trend Filter
	
	// State per symbol
	lastEMA     map[string]map[int]decimal.Decimal // symbol -> period -> value
	lastAvgGain map[string]decimal.Decimal
	lastAvgLoss map[string]decimal.Decimal
	initialized map[string]bool
}

func NewEfficientStrategy(short, long, rsi int) *EfficientMultiFactorStrategy {
	return &EfficientMultiFactorStrategy{
		ShortPeriod: short,
		LongPeriod:  long,
		RSIPeriod:   rsi,
		MacroPeriod: 200, // Hardcoded standard for macro trend
		lastEMA:     make(map[string]map[int]decimal.Decimal),
		lastAvgGain: make(map[string]decimal.Decimal),
		lastAvgLoss: make(map[string]decimal.Decimal),
		initialized: make(map[string]bool),
	}
}

func (s *EfficientMultiFactorStrategy) Analyze(symbol string, prices []decimal.Decimal) *models.Signal {
	// We need enough data for the Macro Period (200)
	if len(prices) < s.MacroPeriod || len(prices) < s.RSIPeriod+1 {
		return nil
	}

	currentPrice := prices[len(prices)-1]

	// Handle Initialization
	if _, ok := s.lastEMA[symbol]; !ok {
		s.lastEMA[symbol] = make(map[int]decimal.Decimal)
		s.lastEMA[symbol][s.ShortPeriod] = calculateEMA(prices[:len(prices)-1], s.ShortPeriod)
		s.lastEMA[symbol][s.LongPeriod] = calculateEMA(prices[:len(prices)-1], s.LongPeriod)
		s.lastEMA[symbol][s.MacroPeriod] = calculateEMA(prices[:len(prices)-1], s.MacroPeriod)
		s.initRSI(symbol, prices[:len(prices)-1])
	}

	shortEMA := updateEMA(s.lastEMA[symbol][s.ShortPeriod], currentPrice, s.ShortPeriod)
	longEMA := updateEMA(s.lastEMA[symbol][s.LongPeriod], currentPrice, s.LongPeriod)
	macroEMA := updateEMA(s.lastEMA[symbol][s.MacroPeriod], currentPrice, s.MacroPeriod)
	
	s.lastEMA[symbol][s.ShortPeriod] = shortEMA
	s.lastEMA[symbol][s.LongPeriod] = longEMA
	s.lastEMA[symbol][s.MacroPeriod] = macroEMA

	rsi := s.calculateIncrementalRSI(symbol, prices)
	rsiFloat, _ := rsi.Float64()
	
	confidence := 0.5 + (0.4 * (func(v float64) float64 {
		if v > 50 { return (v - 50) / 50 }
		return (50 - v) / 50
	}(rsiFloat)))

	// Strategic Optimization:
	// 1. MACRO TREND FILTER: Price MUST be on the right side of the 200 EMA.
	// 2. ENTRY TRIGGER: Short EMA crosses Long EMA in the direction of the Macro Trend.
	// 3. MOMENTUM FILTER: RSI prevents buying at the absolute top or selling at the bottom.

	isBullMarket := currentPrice.GreaterThan(macroEMA)
	isBearMarket := currentPrice.LessThan(macroEMA)

	// BUY Condition: Bull Market + Bullish Cross + RSI Not Overbought (< 70)
	if isBullMarket && shortEMA.GreaterThan(longEMA) && rsiFloat < 70 {
		return &models.Signal{
			Symbol:     symbol,
			Price:      currentPrice,
			Direction:  "BUY",
			Reason:     fmt.Sprintf("Trend:UP (+200EMA) | RSI:%.2f", rsiFloat),
			Confidence: confidence * 1.2, // Boost confidence due to macro alignment
			Timestamp:  time.Now(),
			TP:         currentPrice.Mul(decimal.NewFromFloat(1.05)),
			SL:         currentPrice.Mul(decimal.NewFromFloat(0.98)),
		}
	}

	// SELL Condition: Bear Market + Bearish Cross + RSI Not Oversold (> 30)
	if isBearMarket && shortEMA.LessThan(longEMA) && rsiFloat > 30 {
		return &models.Signal{
			Symbol:     symbol,
			Price:      currentPrice,
			Direction:  "SELL",
			Reason:     fmt.Sprintf("Trend:DOWN (-200EMA) | RSI:%.2f", rsiFloat),
			Confidence: confidence * 1.2,
			Timestamp:  time.Now(),
			TP:         currentPrice.Mul(decimal.NewFromFloat(0.95)),
			SL:         currentPrice.Mul(decimal.NewFromFloat(1.02)),
		}
	}

	return nil
}

func updateEMA(prevEMA, currentPrice decimal.Decimal, period int) decimal.Decimal {
	multiplier := decimal.NewFromFloat(2.0 / (float64(period) + 1.0))
	return currentPrice.Sub(prevEMA).Mul(multiplier).Add(prevEMA)
}

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

	s.lastAvgGain[symbol] = totalGain.Div(decimal.NewFromInt(int64(s.RSIPeriod)))
	s.lastAvgLoss[symbol] = totalLoss.Div(decimal.NewFromInt(int64(s.RSIPeriod)))

	// Process remaining prices to catch up state
	rsiPeriodDec := decimal.NewFromInt(int64(s.RSIPeriod))
	rsiMinusOneDec := decimal.NewFromInt(int64(s.RSIPeriod - 1))

	for i := s.RSIPeriod + 1; i < len(prices); i++ {
		change := prices[i].Sub(prices[i-1])
		gain, loss := decimal.Zero, decimal.Zero
		if change.IsPositive() {
			gain = change
		} else {
			loss = change.Neg()
		}
		s.lastAvgGain[symbol] = s.lastAvgGain[symbol].Mul(rsiMinusOneDec).Add(gain).Div(rsiPeriodDec)
		s.lastAvgLoss[symbol] = s.lastAvgLoss[symbol].Mul(rsiMinusOneDec).Add(loss).Div(rsiPeriodDec)
	}
	s.initialized[symbol] = true
}

func (s *EfficientMultiFactorStrategy) calculateIncrementalRSI(symbol string, prices []decimal.Decimal) decimal.Decimal {
	rsiPeriodDec := decimal.NewFromInt(int64(s.RSIPeriod))
	rsiMinusOneDec := decimal.NewFromInt(int64(s.RSIPeriod - 1))

	if !s.initialized[symbol] {
		s.initRSI(symbol, prices)
	} else {
		currIdx := len(prices) - 1
		prevIdx := len(prices) - 2
		change := prices[currIdx].Sub(prices[prevIdx])
		
		gain, loss := decimal.Zero, decimal.Zero
		if change.IsPositive() {
			gain = change
		} else {
			loss = change.Neg()
		}

		s.lastAvgGain[symbol] = s.lastAvgGain[symbol].Mul(rsiMinusOneDec).Add(gain).Div(rsiPeriodDec)
		s.lastAvgLoss[symbol] = s.lastAvgLoss[symbol].Mul(rsiMinusOneDec).Add(loss).Div(rsiPeriodDec)
	}

	if s.lastAvgLoss[symbol].IsZero() {
		return decimal.NewFromInt(100)
	}

	rs := s.lastAvgGain[symbol].Div(s.lastAvgLoss[symbol])
	// 100 - (100 / (1 + rs))
	return decimal.NewFromInt(100).Sub(decimal.NewFromInt(100).Div(decimal.NewFromInt(1).Add(rs)))
}

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

func calculateRSI(prices []decimal.Decimal, period int) decimal.Decimal {
	if len(prices) <= period {
		return decimal.NewFromInt(50)
	}

	totalGain := decimal.Zero
	totalLoss := decimal.Zero
	for i := 1; i <= period; i++ {
		change := prices[i].Sub(prices[i-1])
		if change.IsPositive() {
			totalGain = totalGain.Add(change)
		} else {
			totalLoss = totalLoss.Sub(change)
		}
	}

	avgGain := totalGain.Div(decimal.NewFromInt(int64(period)))
	avgLoss := totalLoss.Div(decimal.NewFromInt(int64(period)))

	periodDec := decimal.NewFromInt(int64(period))
	periodMinusOneDec := decimal.NewFromInt(int64(period - 1))

	for i := period + 1; i < len(prices); i++ {
		change := prices[i].Sub(prices[i-1])
		gain, loss := decimal.Zero, decimal.Zero
		if change.IsPositive() {
			gain = change
		} else {
			loss = change.Neg()
		}
		// Wilder's Smoothing: (PrevAvg * (N-1) + CurrentValue) / N
		avgGain = avgGain.Mul(periodMinusOneDec).Add(gain).Div(periodDec)
		avgLoss = avgLoss.Mul(periodMinusOneDec).Add(loss).Div(periodDec)
	}

	if avgLoss.IsZero() {
		if avgGain.IsZero() {
			return decimal.NewFromInt(50)
		}
		return decimal.NewFromInt(100)
	}

	rs := avgGain.Div(avgLoss)
	return decimal.NewFromInt(100).Sub(decimal.NewFromInt(100).Div(decimal.NewFromInt(1).Add(rs)))
}
