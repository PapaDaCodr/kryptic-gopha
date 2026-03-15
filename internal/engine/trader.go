package engine

import (
	"time"

	"github.com/papadacodr/kryptic-gopha/internal/models"
	"github.com/shopspring/decimal"
)

// Trader is the interface satisfied by both PaperTrader and LiveTrader,
// allowing the server to switch modes without touching handler code.
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

type TraderStats struct {
	TotalSignals int
	TotalWins    int
	TotalLosses  int
	WinRate      float64
	ActiveTrades int
}

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

type TradesSnapshot struct {
	Active    map[string][]*Trade `json:"active"`
	Completed []Trade             `json:"completed"`
}
