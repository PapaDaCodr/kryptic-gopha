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

// MultiFactorStrategy combines multiple indicators for high accuracy
type MultiFactorStrategy struct {
	ShortPeriod int
	LongPeriod  int
	RSIPeriod   int
}

func (s *MultiFactorStrategy) Analyze(symbol string, prices []float64) *models.Signal {
	if len(prices) < s.LongPeriod || len(prices) < s.RSIPeriod+1 {
		return nil
	}

	shortEMA := calculateEMA(prices, s.ShortPeriod)
	longEMA := calculateEMA(prices, s.LongPeriod)
	rsi := calculateRSI(prices, s.RSIPeriod)

	currentPrice := prices[len(prices)-1]

	// Dynamic Confidence Calculation
	// Higher RSI deviation from 50 = higher confidence
	confidence := 0.5 + (0.4 * (func(v float64) float64 {
		if v > 50 { return (v - 50) / 50 }
		return (50 - v) / 50
	}(rsi)))

	// High-accuracy BUY: EMA Bullish + RSI < 40
	if shortEMA > longEMA && rsi < 40 {
		return &models.Signal{
			Symbol:     symbol,
			Price:      currentPrice,
			Direction:  "BUY",
			Reason:     fmt.Sprintf("Trend:UP | RSI:%.2f (Oversold)", rsi),
			Confidence: confidence,
			Timestamp:  time.Now(),
		}
	}

	// High-accuracy SELL: EMA Bearish + RSI > 60
	if shortEMA < longEMA && rsi > 60 {
		return &models.Signal{
			Symbol:     symbol,
			Price:      currentPrice,
			Direction:  "SELL",
			Reason:     fmt.Sprintf("Trend:DOWN | RSI:%.2f (Overbought)", rsi),
			Confidence: confidence,
			Timestamp:  time.Now(),
		}
	}

	return nil
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

