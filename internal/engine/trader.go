package engine

import (
	"time"

	"github.com/papadacodr/kryptic-gopha/internal/models"
	"github.com/shopspring/decimal"
)

// Trader is the minimal interface that the engine and HTTP layer require.
// Both PaperTrader and LiveTrader must satisfy this interface.
type Trader interface {
	// OnSignal is called when the strategy emits a new trading signal.
	OnSignal(sig models.Signal)

	// UpdateMetrics is called on every price tick so open trades can be
	// evaluated for TP/SL/time exits.
	UpdateMetrics(symbol string, currentPrice decimal.Decimal, now time.Time)

	// GetStats returns a race-free snapshot of headline metrics.
	GetStats() TraderStats

	// GetState returns a complete snapshot of trader state suitable for
	// JSON serialisation (used by /api/state).
	GetState() TraderState

	// GetTrades returns a snapshot of active and completed trades (used by /api/trades).
	GetTrades() TradesSnapshot

	// SetTradingEnabled suspends or resumes trading (e.g. /stop command).
	SetTradingEnabled(enabled bool)

	// SetBalance replaces the current and initial balance (e.g. /setbalance command).
	SetBalance(bal decimal.Decimal)

	// SaveState persists the trader state to disk.
	SaveState(filename string) error

	// LoadState restores trader state from disk.
	LoadState(filename string) error
}

// TraderState is the full state snapshot returned by /api/state.
// Fields are kept compatible with the JSON the frontend already consumes.
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
