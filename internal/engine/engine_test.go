package engine

import (
	"testing"
	"time"
	"github.com/papadacodr/kryptic-gopha/internal/models"
	"github.com/shopspring/decimal"
)

func TestStrategy_RSIAccuracy(t *testing.T) {
	s := NewEfficientStrategy(2, 5, 3)
	s.MacroPeriod = 5 // Override default 200 for this small test
	symbol := "BTC"
	priceVals := []string{"100", "102", "101", "105", "108", "107", "110", "112", "111"}
	prices := make([]decimal.Decimal, len(priceVals))
	for i, v := range priceVals {
		prices[i], _ = decimal.NewFromString(v)
	}

	// 1. Full calculation baseline
	targetRSI := calculateRSI(prices, 3)

	// 2. Incremental
	for i := 1; i <= len(prices); i++ {
		s.Analyze(symbol, prices[:i])
	}

	// Calculate RSI from internal state to verify accuracy
	rs := s.lastAvgGain[symbol].Div(s.lastAvgLoss[symbol])
	gotRSI := decimal.NewFromInt(100).Sub(decimal.NewFromInt(100).Div(decimal.NewFromInt(1).Add(rs)))

	if !gotRSI.Sub(targetRSI).Abs().LessThan(decimal.NewFromFloat(0.01)) {
		t.Errorf("Incremental RSI mismatch. Target: %s, Got: %s", targetRSI.String(), gotRSI.String())
	}
}

func TestEngineManager_OHLCV(t *testing.T) {
	strategy := NewEfficientStrategy(2, 4, 2)
	strategy.MacroPeriod = 4 // Override for test
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
	val100, _ := decimal.NewFromString("100.0")
	val105, _ := decimal.NewFromString("105.0")
	val95, _ := decimal.NewFromString("95.0")
	val102, _ := decimal.NewFromString("102.0")

	if !candle.Open.Equal(val100) || !candle.High.Equal(val105) || !candle.Low.Equal(val95) || !candle.Close.Equal(val102) {
		t.Errorf("OHLCV logic failed: %+v", candle)
	}
}

func TestEngineManager_Concurrency(t *testing.T) {
	symbols := []string{"BTC", "ETH", "SOL", "BNB"}
	strategy := NewEfficientStrategy(2, 4, 2)
	strategy.MacroPeriod = 4
	mgr := NewEngineManager(symbols, 10, strategy, nil)
	
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

	b.Add(decimal.NewFromInt(1))
	b.Add(decimal.NewFromInt(2))
	b.Add(decimal.NewFromInt(3))
	b.Add(decimal.NewFromInt(4)) // Wraps around, replaces 1.0

	history := b.GetHistory()
	if len(history) != 3 {
		t.Errorf("Expected length 3, got %d", len(history))
	}
	// History should be [2, 3, 4]
	if !history[0].Equal(decimal.NewFromInt(2)) || !history[2].Equal(decimal.NewFromInt(4)) {
		t.Errorf("Circular buffer order incorrect: %v", history)
	}
}

func TestStrategy_IncrementalAccuracy(t *testing.T) {
	s := NewEfficientStrategy(2, 4, 3)
	s.MacroPeriod = 4 // Override for small test
	symbol := "ETH"
	priceVals := []string{"100", "102", "101", "105", "104", "108", "107"}
	prices := make([]decimal.Decimal, len(priceVals))
	for i, v := range priceVals {
		prices[i], _ = decimal.NewFromString(v)
	}

	// 1. Full calculation for baseline
	targetEMA := calculateEMA(prices, 5)
	
	// 2. Incremental calculation
	for i := 1; i <= len(prices); i++ {
		s.Analyze(symbol, prices[:i])
	}

	gotEMA := s.lastEMA[symbol][5]
	
	if !gotEMA.Equal(targetEMA) {
		t.Errorf("Incremental EMA mismatch. Target: %s, Got: %s", targetEMA.String(), gotEMA.String())
	}
}

func TestPaperTrader_TimeExit(t *testing.T) {
	trader := NewPaperTrader(10000.0)
	trader.TrailingSL = false // Disable trailing for this test
	trader.TP = decimal.NewFromFloat(0.5) // High TP so it doesn't hit
	trader.SL = decimal.NewFromFloat(0.5) // High SL so it doesn't hit
	symbol := "BTC"
	start := time.Now().Add(-15 * time.Minute)

	trader.OnSignal(models.Signal{
		Symbol:    symbol,
		Price:     decimal.NewFromInt(100),
		Direction: "BUY",
		Timestamp: start,
	})

	// Update at 1m, 5m, 10m
	trader.UpdateMetrics(symbol, decimal.NewFromInt(101), start.Add(time.Minute))
	trader.UpdateMetrics(symbol, decimal.NewFromInt(102), start.Add(5 * time.Minute))
	
	if len(trader.ActiveTrades[symbol]) != 1 {
		t.Errorf("Trade exited too early")
	}

	// Final update at 11m -> should trigger 10m time exit
	trader.UpdateMetrics(symbol, decimal.NewFromInt(103), start.Add(11 * time.Minute))
	
	if len(trader.ActiveTrades[symbol]) != 0 {
		t.Errorf("Trade should have exited after 10m")
	}
	if trader.TotalWins != 1 {
		t.Errorf("Expected 1 win (price 103 > 100), got %d", trader.TotalWins)
	}
}

func TestEngineManager_UpdatePrice(t *testing.T) {
	strategy := NewEfficientStrategy(2, 4, 2)
	strategy.MacroPeriod = 4
	trader := NewPaperTrader(10000.0)
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
	trader := NewPaperTrader(10000.0)
	trader.TrailingSL = false // Disable trailing for this test
	trader.TP = decimal.NewFromFloat(0.01) // 1%
	trader.SL = decimal.NewFromFloat(0.01) // 1%
	symbol := "ETHUSDT"
	now := time.Now()

	trader.OnSignal(models.Signal{
		Symbol:    symbol,
		Price:     decimal.NewFromInt(1000),
		Direction: "BUY",
		Timestamp: now,
	})

	trader.UpdateMetrics(symbol, decimal.NewFromInt(1020), now.Add(time.Minute))
	if trader.TotalWins != 1 {
		t.Errorf("Expected 1 win, got %d", trader.TotalWins)
	}

	trader.OnSignal(models.Signal{
		Symbol:    symbol,
		Price:     decimal.NewFromInt(1000),
		Direction: "SELL",
		Timestamp: now.Add(10 * time.Minute),
	})

	trader.UpdateMetrics(symbol, decimal.NewFromInt(1020), now.Add(11 * time.Minute))
	if trader.TotalLosses != 1 {
		t.Errorf("Expected 1 loss, got %d", trader.TotalLosses)
	}
}

func TestPaperTrader_TrailingSL(t *testing.T) {
	trader := NewPaperTrader(10000.0)
	trader.TP = decimal.NewFromFloat(0.10)  // 10% TP (won't hit in this test)
	trader.TrailingSL = true
	trader.TrailingSLPct = decimal.NewFromFloat(0.01) // 1% trailing distance
	symbol := "BTC"
	now := time.Now()

	// Open a BUY at 1000
	trader.OnSignal(models.Signal{
		Symbol:    symbol,
		Price:     decimal.NewFromInt(1000),
		Direction: "BUY",
		Timestamp: now,
	})

	// Price rises to 1050 (HWM updates to 1050)
	trader.UpdateMetrics(symbol, decimal.NewFromInt(1050), now.Add(time.Minute))
	if len(trader.ActiveTrades[symbol]) != 1 {
		t.Fatal("Trade should still be active at 1050")
	}

	// Price drops to 1040 (1% trailing from 1050 = 1039.5, so 1040 is above -> still active)
	trader.UpdateMetrics(symbol, decimal.NewFromInt(1040), now.Add(2*time.Minute))
	if len(trader.ActiveTrades[symbol]) != 1 {
		t.Fatal("Trade should still be active at 1040 (trail stop is 1039.5)")
	}

	// Price drops to 1039 (below 1039.5 trailing stop) -> should close as WIN (1039 > 1000 entry)
	trader.UpdateMetrics(symbol, decimal.NewFromInt(1039), now.Add(3*time.Minute))
	if len(trader.ActiveTrades[symbol]) != 0 {
		t.Fatal("Trade should have been closed by trailing SL at 1039")
	}
	if trader.TotalWins != 1 {
		t.Errorf("Expected 1 win (trailing SL above entry), got wins=%d losses=%d", trader.TotalWins, trader.TotalLosses)
	}
}
