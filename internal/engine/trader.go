package engine

import (
	"time"

	"github.com/papadacodr/kryptic-gopha/internal/models"
	"github.com/shopspring/decimal"
)

// Trader is the minimal interface the engine and HTTP layer depend on.
// Both PaperTrader and LiveTrader satisfy it, allowing the server to switch
// modes without touching any routing or handler code.
type Trader interface {
	OnSignal(sig models.Signal)
	UpdateMetrics(symbol string, currentPrice decimal.Decimal, now time.Time)
	GetStats() TraderStats
	GetState() TraderState
	GetTrades() TradesSnapshot
	SetTradingEnabled(enabled bool)
	SetBalance(bal decimal.Decimal)
	SaveState(filename string) error
	LoadState(filename string) error
}

// TraderStats is a lightweight snapshot of headline metrics suitable for
// periodic logging, health checks, and the backtester report.
type TraderStats struct {
	TotalSignals int
	TotalWins    int
	TotalLosses  int
	WinRate      float64
	ActiveTrades int
}

// TraderState is the full state snapshot returned by /api/state.
type TraderState struct {
	Balance        decimal.Decimal     `json:"balance"`
	InitialBalance decimal.Decimal     `json:"initial_balance"`
	DailyPnL       decimal.Decimal     `json:"daily_pnl"`
	TotalWins      int                 `json:"total_wins"`
	TotalLosses    int                 `json:"total_losses"`
	TradingEnabled bool                `json:"trading_enabled"`
	ActiveTrades   map[string][]*Trade `json:"active_trades"`
	Completed      []Trade             `json:"completed"`
}

// TradesSnapshot is returned by /api/trades.
type TradesSnapshot struct {
	Active    map[string][]*Trade `json:"active"`
	Completed []Trade             `json:"completed"`
}
