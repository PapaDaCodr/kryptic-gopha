package engine

import (
	"testing"
)

func TestEfficientStrategy(t *testing.T) {
	strategy := NewEfficientStrategy(2, 4, 2)
	symbol := "BTCUSDT"

	// Not enough data yet
	prices := []float64{100, 105}
	sig := strategy.Analyze(symbol, prices)
	if sig != nil {
		t.Errorf("Expected nil signal for insufficient data, got %v", sig)
	}

	// Trend up + RSI low (hypothetically)
	// We need at least LongPeriod (4) + RSIPeriod+1 (3) = 4 points for LongPeriod check
	prices = []float64{100, 105, 110, 115, 120}
	sig = strategy.Analyze(symbol, prices)
	
	// We don't necessarily expect a signal here without careful price crafting, 
	// but we check if it doesn't crash
	if sig != nil {
		if sig.Symbol != symbol {
			t.Errorf("Expected symbol %s, got %s", symbol, sig.Symbol)
		}
	}
}

func TestUpdateEMA(t *testing.T) {
	prevEMA := 100.0
	currentPrice := 110.0
	period := 9
	
	// Multiplier = 2 / (9 + 1) = 0.2
	// EMA = (110 - 100) * 0.2 + 100 = 102
	expected := 102.0
	got := updateEMA(prevEMA, currentPrice, period)
	
	if got != expected {
		t.Errorf("Expected %f, got %f", expected, got)
	}
}
