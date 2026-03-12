package engine

import (
	"testing"
	"time"
	"github.com/papadacodr/kryptic-gopha/internal/models"
)

func TestPaperTrader_TP_SL(t *testing.T) {
	trader := NewPaperTrader()
	trader.TP = 0.01 // 1%
	trader.SL = 0.01 // 1%
	symbol := "BTCUSDT"
	now := time.Now()

	// 1. Test Buy Win (TP hit)
	trader.OnSignal(models.Signal{
		Symbol:    symbol,
		Price:     100.0,
		Direction: "BUY",
		Timestamp: now,
	})

	trader.UpdateMetrics(symbol, 101.1, now.Add(time.Minute))
	if trader.TotalWins != 1 {
		t.Errorf("Expected 1 win, got %d", trader.TotalWins)
	}
	if len(trader.ActiveTrades) != 0 {
		t.Errorf("Expected 0 active trades, got %d", len(trader.ActiveTrades))
	}

	// 2. Test Sell Loss (SL hit)
	trader.OnSignal(models.Signal{
		Symbol:    symbol,
		Price:     100.0,
		Direction: "SELL",
		Timestamp: now,
	})

	trader.UpdateMetrics(symbol, 101.5, now.Add(time.Minute))
	if trader.TotalLosses != 1 {
		t.Errorf("Expected 1 loss, got %d", trader.TotalLosses)
	}
}
