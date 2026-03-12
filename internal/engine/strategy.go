package engine

import (
	"fmt"
	"time"
	"github.com/papadacodr/kryptic-gopha/internal/models"
)

// Strategy defines the interface for trading algorithms
type Strategy interface {
	Analyze(symbol string, prices []float64) *models.Signal
}

// EMAStrategy is a basic but effective trend-following strategy
type EMAStrategy struct {
	ShortPeriod int
	LongPeriod  int
	Threshold   float64
}

func (s *EMAStrategy) Analyze(symbol string, prices []float64) *models.Signal {
	if len(prices) < s.LongPeriod {
		return nil
	}

	shortEMA := calculateEMA(prices, s.ShortPeriod)
	longEMA := calculateEMA(prices, s.LongPeriod)

	// Bullish Crossover
	if shortEMA > longEMA*(1+s.Threshold) {
		return &models.Signal{
			Symbol:     symbol,
			Price:      prices[len(prices)-1],
			Direction:  "BUY",
			Reason:     "EMA Bullish Crossover",
			Confidence: 0.75, // Simplified
			Timestamp:  time.Now(),
		}
	}

	// Bearish Crossover
	if shortEMA < longEMA*(1-s.Threshold) {
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

// EfficientMultiFactorStrategy uses caching to avoid redundant calculations
type EfficientMultiFactorStrategy struct {
	ShortPeriod int
	LongPeriod  int
	RSIPeriod   int
	
	// State per symbol
	lastEMA     map[string]map[int]float64 // symbol -> period -> value
	lastAvgGain map[string]float64
	lastAvgLoss map[string]float64
	initialized map[string]bool
}

func NewEfficientStrategy(short, long, rsi int) *EfficientMultiFactorStrategy {
	return &EfficientMultiFactorStrategy{
		ShortPeriod: short,
		LongPeriod:  long,
		RSIPeriod:   rsi,
		lastEMA:     make(map[string]map[int]float64),
		lastAvgGain: make(map[string]float64),
		lastAvgLoss: make(map[string]float64),
		initialized: make(map[string]bool),
	}
}

func (s *EfficientMultiFactorStrategy) Analyze(symbol string, prices []float64) *models.Signal {
	if len(prices) < s.LongPeriod || len(prices) < s.RSIPeriod+1 {
		return nil
	}

	currentPrice := prices[len(prices)-1]

	if _, ok := s.lastEMA[symbol]; !ok {
		s.lastEMA[symbol] = make(map[int]float64)
		s.lastEMA[symbol][s.ShortPeriod] = calculateEMA(prices[:len(prices)-1], s.ShortPeriod)
		s.lastEMA[symbol][s.LongPeriod] = calculateEMA(prices[:len(prices)-1], s.LongPeriod)
	}

	shortEMA := updateEMA(s.lastEMA[symbol][s.ShortPeriod], currentPrice, s.ShortPeriod)
	longEMA := updateEMA(s.lastEMA[symbol][s.LongPeriod], currentPrice, s.LongPeriod)
	
	s.lastEMA[symbol][s.ShortPeriod] = shortEMA
	s.lastEMA[symbol][s.LongPeriod] = longEMA

	rsi := s.calculateIncrementalRSI(symbol, prices)
	
	confidence := 0.5 + (0.4 * (func(v float64) float64 {
		if v > 50 { return (v - 50) / 50 }
		return (50 - v) / 50
	}(rsi)))

	if shortEMA > longEMA && rsi < 40 {
		return &models.Signal{
			Symbol:     symbol,
			Price:      currentPrice,
			Direction:  "BUY",
			Reason:     fmt.Sprintf("Trend:UP | RSI:%.2f", rsi),
			Confidence: confidence,
			Timestamp:  time.Now(),
		}
	}

	if shortEMA < longEMA && rsi > 60 {
		return &models.Signal{
			Symbol:     symbol,
			Price:      currentPrice,
			Direction:  "SELL",
			Reason:     fmt.Sprintf("Trend:DOWN | RSI:%.2f", rsi),
			Confidence: confidence,
			Timestamp:  time.Now(),
		}
	}

	return nil
}

func updateEMA(prevEMA, currentPrice float64, period int) float64 {
	multiplier := 2.0 / (float64(period) + 1.0)
	return (currentPrice-prevEMA)*multiplier + prevEMA
}

func (s *EfficientMultiFactorStrategy) calculateIncrementalRSI(symbol string, prices []float64) float64 {
	currIdx := len(prices) - 1
	prevIdx := len(prices) - 2
	change := prices[currIdx] - prices[prevIdx]
	
	gain := 0.0
	loss := 0.0
	if change > 0 {
		gain = change
	} else {
		loss = -change
	}

	if !s.initialized[symbol] {
		var initialGain, initialLoss float64
		for i := 1; i <= s.RSIPeriod; i++ {
			c := prices[i] - prices[i-1]
			if c > 0 {
				initialGain += c
			} else {
				initialLoss -= c
			}
		}
		s.lastAvgGain[symbol] = initialGain / float64(s.RSIPeriod)
		s.lastAvgLoss[symbol] = initialLoss / float64(s.RSIPeriod)
		s.initialized[symbol] = true
	} else {
		s.lastAvgGain[symbol] = (s.lastAvgGain[symbol]*float64(s.RSIPeriod-1) + gain) / float64(s.RSIPeriod)
		s.lastAvgLoss[symbol] = (s.lastAvgLoss[symbol]*float64(s.RSIPeriod-1) + loss) / float64(s.RSIPeriod)
	}

	if s.lastAvgLoss[symbol] == 0 {
		return 100
	}

	rs := s.lastAvgGain[symbol] / s.lastAvgLoss[symbol]
	return 100 - (100 / (1 + rs))
}


func calculateEMA(prices []float64, period int) float64 {
	length := len(prices)
	if length == 0 {
		return 0
	}
	if length > period {
		prices = prices[length-period:]
	}
	
	multiplier := 2.0 / (float64(period) + 1.0)
	ema := prices[0]
	
	for i := 1; i < len(prices); i++ {
		ema = (prices[i]-ema)*multiplier + ema
	}
	return ema
}

func calculateRSI(prices []float64, period int) float64 {
	if len(prices) <= period {
		return 50 // Neutral
	}

	var gains, losses float64
	for i := 1; i <= period; i++ {
		change := prices[len(prices)-i] - prices[len(prices)-i-1]
		if change > 0 {
			gains += change
		} else {
			losses -= change
		}
	}

	avgGain := gains / float64(period)
	avgLoss := losses / float64(period)

	if avgLoss == 0 {
		return 100
	}

	rs := avgGain / avgLoss
	return 100 - (100 / (1 + rs))
}

