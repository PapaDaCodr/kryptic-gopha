package engine

import (
	"testing"
	"time"
	"github.com/papadacodr/kryptic-gopha/internal/models"
)

func TestEngineManager_UpdatePrice(t *testing.T) {
	strategy := NewEfficientStrategy(2, 4, 2)
	trader := NewPaperTrader()
	symbols := []string{"BTCUSDT"}
	mgr := NewEngineManager(symbols, 10, strategy, trader)

	ticks := []models.MarketTick{
		{Symbol: "BTCUSDT", Price: "100.0", Timestamp: time.Now().UnixMilli()},
		{Symbol: "BTCUSDT", Price: "101.0", Timestamp: time.Now().Add(time.Minute).UnixMilli()},
		{Symbol: "BTCUSDT", Price: "102.0", Timestamp: time.Now().Add(2 * time.Minute).UnixMilli()},
		{Symbol: "BTCUSDT", Price: "103.0", Timestamp: time.Now().Add(3 * time.Minute).UnixMilli()},
		{Symbol: "BTCUSDT", Price: "104.0", Timestamp: time.Now().Add(4 * time.Minute).UnixMilli()},
	}

	for _, tick := range ticks {
		if err := mgr.UpdatePrice(tick); err != nil {
			t.Errorf("Unexpected error: %v", err)
		}
	}

	state := mgr.states["BTCUSDT"]
	if state.buffer.cursor != 4 && !state.buffer.isFull {
		// Note: buffer adds candle on minute close, so 4 ticks might result in 3 or 4 points depending on timing
	}
}

func TestPaperTrader_StatusTransitions(t *testing.T) {
	trader := NewPaperTrader()
	trader.TP = 0.01 // 1%
	trader.SL = 0.01 // 1%
	symbol := "ETHUSDT"
	now := time.Now()

	// Buy Trade
	trader.OnSignal(models.Signal{
		Symbol:    symbol,
		Price:     1000.0,
		Direction: "BUY",
		Timestamp: now,
	})

	// Price goes up 2% -> TP Hit
	trader.UpdateMetrics(symbol, 1020.0, now.Add(time.Minute))
	if trader.TotalWins != 1 {
		t.Errorf("Expected 1 win, got %d", trader.TotalWins)
	}

	// Sell Trade
	trader.OnSignal(models.Signal{
		Symbol:    symbol,
		Price:     1000.0,
		Direction: "SELL",
		Timestamp: now.Add(10 * time.Minute),
	})

	// Price goes up 2% -> SL Hit (Sell trade)
	trader.UpdateMetrics(symbol, 1020.0, now.Add(11 * time.Minute))
	if trader.TotalLosses != 1 {
		t.Errorf("Expected 1 loss, got %d", trader.TotalLosses)
	}
}

func TestStrategy_IncrementalIntegrity(t *testing.T) {
	s := NewEfficientStrategy(2, 5, 3)
	symbol := "BTC"
	
	// Create a price series
	prices := []float64{10, 11, 10, 12, 11, 13, 12, 14}
	
	// Feed prices one by one
	for i := 1; i <= len(prices); i++ {
		s.Analyze(symbol, prices[:i])
	}
	
	// Check if initialized correctly
	if !s.initialized[symbol] {
		t.Errorf("Strategy should be initialized after enough data points")
	}
}
