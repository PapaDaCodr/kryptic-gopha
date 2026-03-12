package engine

import (
	"testing"
	"time"
	"github.com/papadacodr/kryptic-gopha/internal/models"
)

func TestStrategy_RSIAccuracy(t *testing.T) {
	s := NewEfficientStrategy(2, 5, 3)
	symbol := "BTC"
	prices := []float64{100, 102, 101, 105, 108, 107, 110, 112, 111}

	// 1. Full calculation baseline
	targetRSI := calculateRSI(prices, 3)

	// 2. Incremental
	for i := 1; i <= len(prices); i++ {
		s.Analyze(symbol, prices[:i])
	}

	// Calculate RSI from internal state to verify accuracy
	rs := s.lastAvgGain[symbol] / s.lastAvgLoss[symbol]
	gotRSI := 100 - (100 / (1 + rs))

	if gotRSI < targetRSI-0.01 || gotRSI > targetRSI+0.01 {
		t.Errorf("Incremental RSI mismatch. Target: %f, Got: %f", targetRSI, gotRSI)
	}
}

func TestEngineManager_OHLCV(t *testing.T) {
	strategy := NewEfficientStrategy(2, 4, 2)
	mgr := NewEngineManager([]string{"BTC"}, 10, strategy, nil)
	now := time.Now().Truncate(time.Minute)

	// Tick sequence for 1 minute
	ticks := []models.MarketTick{
		{Symbol: "BTC", Price: "100.0", Timestamp: now.UnixMilli()},
		{Symbol: "BTC", Price: "105.0", Timestamp: now.Add(time.Second).UnixMilli()},
		{Symbol: "BTC", Price: "95.0", Timestamp: now.Add(2 * time.Second).UnixMilli()},
		{Symbol: "BTC", Price: "102.0", Timestamp: now.Add(59 * time.Second).UnixMilli()},
	}

	for _, tick := range ticks {
		mgr.UpdatePrice(tick)
	}

	candle := mgr.states["BTC"].currentCandle
	if candle.Open != 100.0 || candle.High != 105.0 || candle.Low != 95.0 || candle.Close != 102.0 {
		t.Errorf("OHLCV logic failed: %+v", candle)
	}
}

func TestEngineManager_Concurrency(t *testing.T) {
	symbols := []string{"BTC", "ETH", "SOL", "BNB"}
	mgr := NewEngineManager(symbols, 10, NewEfficientStrategy(2, 4, 2), nil)
	
	const iterations = 100
	done := make(chan bool)

	for _, s := range symbols {
		go func(sym string) {
			for i := 0; i < iterations; i++ {
				mgr.UpdatePrice(models.MarketTick{
					Symbol: sym,
					Price:  "100.0",
					Timestamp: time.Now().UnixMilli(),
				})
			}
			done <- true
		}(s)
	}

	for i := 0; i < len(symbols); i++ {
		<-done
	}
}

func TestPriceBuffer_Circular(t *testing.T) {
	size := 3
	b := NewPriceBuffer(size)

	b.Add(1.0)
	b.Add(2.0)
	b.Add(3.0)
	b.Add(4.0) // Wraps around, replaces 1.0

	history := b.GetHistory()
	if len(history) != 3 {
		t.Errorf("Expected length 3, got %d", len(history))
	}
	// History should be [2, 3, 4]
	if history[0] != 2.0 || history[2] != 4.0 {
		t.Errorf("Circular buffer order incorrect: %v", history)
	}
}

func TestStrategy_IncrementalAccuracy(t *testing.T) {
	s := NewEfficientStrategy(2, 5, 3)
	symbol := "BTC"
	prices := []float64{100, 102, 101, 105, 104, 108, 107}

	// 1. Full calculation for baseline
	targetEMA := calculateEMA(prices, 5)
	
	// 2. Incremental calculation
	for i := 1; i <= len(prices); i++ {
		s.Analyze(symbol, prices[:i])
	}

	gotEMA := s.lastEMA[symbol][5]
	
	// Floats can have tiny diffs, check within 0.0001
	if gotEMA < targetEMA-0.0001 || gotEMA > targetEMA+0.0001 {
		t.Errorf("Incremental EMA mismatch. Target: %f, Got: %f", targetEMA, gotEMA)
	}
}

func TestPaperTrader_TimeExit(t *testing.T) {
	trader := NewPaperTrader()
	trader.TP = 0.5 // High TP so it doesn't hit
	trader.SL = 0.5 // High SL so it doesn't hit
	symbol := "BTC"
	start := time.Now().Add(-15 * time.Minute)

	trader.OnSignal(models.Signal{
		Symbol:    symbol,
		Price:     100.0,
		Direction: "BUY",
		Timestamp: start,
	})

	// Update at 1m, 5m, 10m
	trader.UpdateMetrics(symbol, 101.0, start.Add(time.Minute))
	trader.UpdateMetrics(symbol, 102.0, start.Add(5 * time.Minute))
	
	if len(trader.ActiveTrades) != 1 {
		t.Errorf("Trade exited too early")
	}

	// Final update at 11m -> should trigger 10m time exit
	trader.UpdateMetrics(symbol, 103.0, start.Add(11 * time.Minute))
	
	if len(trader.ActiveTrades) != 0 {
		t.Errorf("Trade should have exited after 10m")
	}
	if trader.TotalWins != 1 {
		t.Errorf("Expected 1 win (price 103 > 100), got %d", trader.TotalWins)
	}
}

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
}

func TestPaperTrader_StatusTransitions(t *testing.T) {
	trader := NewPaperTrader()
	trader.TP = 0.01 // 1%
	trader.SL = 0.01 // 1%
	symbol := "ETHUSDT"
	now := time.Now()

	trader.OnSignal(models.Signal{
		Symbol:    symbol,
		Price:     1000.0,
		Direction: "BUY",
		Timestamp: now,
	})

	trader.UpdateMetrics(symbol, 1020.0, now.Add(time.Minute))
	if trader.TotalWins != 1 {
		t.Errorf("Expected 1 win, got %d", trader.TotalWins)
	}

	trader.OnSignal(models.Signal{
		Symbol:    symbol,
		Price:     1000.0,
		Direction: "SELL",
		Timestamp: now.Add(10 * time.Minute),
	})

	trader.UpdateMetrics(symbol, 1020.0, now.Add(11 * time.Minute))
	if trader.TotalLosses != 1 {
		t.Errorf("Expected 1 loss, got %d", trader.TotalLosses)
	}
}
