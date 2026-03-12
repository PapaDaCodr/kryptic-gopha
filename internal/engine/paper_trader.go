package engine

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/papadacodr/kryptic-gopha/internal/models"
	"github.com/papadacodr/kryptic-gopha/pkg/notifier"
	"github.com/rs/zerolog/log"
	"github.com/shopspring/decimal"
)

type Trade struct {
	Symbol        string                  `json:"symbol"`
	EntryPrice    decimal.Decimal         `json:"entry_price"`
	Quantity      decimal.Decimal         `json:"quantity"`
	Direction     string                  `json:"direction"`
	Time          time.Time               `json:"time"`
	Exits         map[int]decimal.Decimal `json:"exits"`
	Status        string                  `json:"status"`
	ExitPrice     decimal.Decimal         `json:"exit_price"`
	PnL           decimal.Decimal         `json:"pnl"`
	HighWaterMark decimal.Decimal         `json:"high_water_mark"` // Best price seen for trailing SL
	ExitReason    string                  `json:"exit_reason"`
}

type PaperTrader struct {
	sync.Mutex
	ActiveTrades map[string][]*Trade `json:"active_trades"`
	Completed    []Trade             `json:"completed"`
	TotalWins    int                 `json:"total_wins"`
	TotalLosses  int                 `json:"total_losses"`
	
	// Risk Management Settings
	TP               decimal.Decimal     `json:"tp"`
	SL               decimal.Decimal     `json:"sl"`
	TrailingSL       bool                `json:"trailing_sl"`         // Enable trailing stop-loss
	TrailingSLPct    decimal.Decimal     `json:"trailing_sl_pct"`     // Trailing distance (e.g., 0.003 = 0.3%)
	Balance          decimal.Decimal     `json:"balance"`
	InitialBalance   decimal.Decimal     `json:"initial_balance"`
	RiskPerTrade     decimal.Decimal     `json:"risk_per_trade"`
	MaxOpenTrades    int                 `json:"max_open_trades"`
	DailyLossLimit   decimal.Decimal     `json:"daily_loss_limit"`
	
	// Circuit Breaker State
	TradingEnabled bool              `json:"trading_enabled"`
	LastDailyReset time.Time         `json:"last_daily_reset"`
	DailyPnL       decimal.Decimal   `json:"daily_pnl"`

	// External services
	Notifier notifier.Notifier `json:"-"`
}

func NewPaperTrader(balance float64) *PaperTrader {
	bal := decimal.NewFromFloat(balance)
	return &PaperTrader{
		ActiveTrades:   make(map[string][]*Trade),
		Completed:      make([]Trade, 0),
		TP:             decimal.NewFromFloat(0.005),
		SL:             decimal.NewFromFloat(0.003),
		TrailingSL:     true,
		TrailingSLPct:  decimal.NewFromFloat(0.003), // 0.3% trailing distance
		Balance:        bal,
		InitialBalance: bal,
		RiskPerTrade:   decimal.NewFromFloat(0.01),
		MaxOpenTrades:  5,
		DailyLossLimit: decimal.NewFromFloat(0.05),
		TradingEnabled: true,
		LastDailyReset: time.Now(),
		DailyPnL:       decimal.Zero,
	}
}

func (p *PaperTrader) OnSignal(sig models.Signal) {
	p.Lock()
	defer p.Unlock()

	if !p.TradingEnabled {
		log.Warn().Str("symbol", sig.Symbol).Msg("Trade ignored: Circuit breaker active")
		return
	}

	p.checkDailyReset()

	// Check Max Concurrent Trades
	activeCount := 0
	for _, trades := range p.ActiveTrades {
		activeCount += len(trades)
	}
	if activeCount >= p.MaxOpenTrades {
		log.Warn().Int("limit", p.MaxOpenTrades).Msg("Trade ignored: Max open trades reached")
		return
	}

	// Dynamic Position Sizing
	// Size = (Balance * RiskPerTrade) / SL_Amount
	// If SL is 0.3%, we risk 1% of balance.
	riskAmount := p.Balance.Mul(p.RiskPerTrade)
	quantity := riskAmount.Div(sig.Price.Mul(p.SL))

	trade := &Trade{
		Symbol:        sig.Symbol,
		EntryPrice:    sig.Price,
		Quantity:      quantity,
		Direction:     sig.Direction,
		Time:          sig.Timestamp,
		Exits:         make(map[int]decimal.Decimal),
		Status:        "ACTIVE",
		HighWaterMark: sig.Price, // Initialize HWM at entry
	}
	p.ActiveTrades[sig.Symbol] = append(p.ActiveTrades[sig.Symbol], trade)
	
	log.Info().
		Str("symbol", sig.Symbol).
		Str("direction", sig.Direction).
		Str("price", sig.Price.String()).
		Str("quantity", quantity.StringFixed(4)).
		Msg("New trade opened")

	if p.Notifier != nil {
		p.Notifier.Notify(fmt.Sprintf("🚀 *NEW TRADE*\nSymbol: `%s`\nDirection: %s\nPrice: %s\nQty: %s",
			sig.Symbol, sig.Direction, sig.Price.String(), quantity.StringFixed(4)))
	}
}

func (p *PaperTrader) UpdateMetrics(symbol string, currentPrice decimal.Decimal, now time.Time) {
	p.Lock()
	defer p.Unlock()

	trades, ok := p.ActiveTrades[symbol]
	if !ok {
		return
	}

	remainingTrades := make([]*Trade, 0, len(trades))
	one := decimal.NewFromInt(1)

	for _, t := range trades {
		// Update High Water Mark for trailing SL
		if t.Direction == "BUY" && currentPrice.GreaterThan(t.HighWaterMark) {
			t.HighWaterMark = currentPrice
		} else if t.Direction == "SELL" && currentPrice.LessThan(t.HighWaterMark) {
			t.HighWaterMark = currentPrice
		}

		isWin := false
		isLoss := false
		exitReason := ""

		// Check fixed TP
		if t.Direction == "BUY" {
			if currentPrice.GreaterThanOrEqual(t.EntryPrice.Mul(one.Add(p.TP))) {
				isWin = true
				exitReason = "TP_HIT"
			}
		} else {
			if currentPrice.LessThanOrEqual(t.EntryPrice.Mul(one.Sub(p.TP))) {
				isWin = true
				exitReason = "TP_HIT"
			}
		}

		// Check Trailing SL (if enabled) or fixed SL
		if !isWin {
			if p.TrailingSL {
				// Trailing SL: stop is relative to HWM, not entry
				if t.Direction == "BUY" {
					trailStop := t.HighWaterMark.Mul(one.Sub(p.TrailingSLPct))
					if currentPrice.LessThanOrEqual(trailStop) {
						// It's a win if we're still above entry
						if currentPrice.GreaterThan(t.EntryPrice) {
							isWin = true
						} else {
							isLoss = true
						}
						exitReason = "TRAILING_SL"
					}
				} else {
					trailStop := t.HighWaterMark.Mul(one.Add(p.TrailingSLPct))
					if currentPrice.GreaterThanOrEqual(trailStop) {
						if currentPrice.LessThan(t.EntryPrice) {
							isWin = true
						} else {
							isLoss = true
						}
						exitReason = "TRAILING_SL"
					}
				}
			} else {
				// Fixed SL
				if t.Direction == "BUY" {
					if currentPrice.LessThanOrEqual(t.EntryPrice.Mul(one.Sub(p.SL))) {
						isLoss = true
						exitReason = "FIXED_SL"
					}
				} else {
					if currentPrice.GreaterThanOrEqual(t.EntryPrice.Mul(one.Add(p.SL))) {
						isLoss = true
						exitReason = "FIXED_SL"
					}
				}
			}
		}

		if isWin || isLoss {
			t.ExitReason = exitReason
			p.closeTrade(t, currentPrice, isWin)
			continue
		}

		// Time-based exit checks
		duration := now.Sub(t.Time).Minutes()
		checkIntervals := []int{1, 5, 10}
		for _, interval := range checkIntervals {
			if duration >= float64(interval) && (t.Exits[interval].IsZero()) {
				t.Exits[interval] = currentPrice
			}
		}

		if !t.Exits[10].IsZero() {
			isWinAt10 := false
			if t.Direction == "BUY" && currentPrice.GreaterThan(t.EntryPrice) {
				isWinAt10 = true
			} else if t.Direction == "SELL" && currentPrice.LessThan(t.EntryPrice) {
				isWinAt10 = true
			}
			t.ExitReason = "TIME_EXIT"
			p.closeTrade(t, currentPrice, isWinAt10)
			continue
		}

		remainingTrades = append(remainingTrades, t)
	}

	if len(remainingTrades) == 0 {
		delete(p.ActiveTrades, symbol)
	} else {
		p.ActiveTrades[symbol] = remainingTrades
	}
}

func (p *PaperTrader) closeTrade(t *Trade, currentPrice decimal.Decimal, isWin bool) {
	t.Status = "LOSS"
	if isWin {
		t.Status = "WIN"
		p.TotalWins++
	} else {
		p.TotalLosses++
	}
	t.ExitPrice = currentPrice

	// Calculate PnL: (Exit - Entry) * Qty for BUY, (Entry - Exit) * Qty for SELL
	var pnl decimal.Decimal
	if t.Direction == "BUY" {
		pnl = t.ExitPrice.Sub(t.EntryPrice).Mul(t.Quantity)
	} else {
		pnl = t.EntryPrice.Sub(t.ExitPrice).Mul(t.Quantity)
	}
	
	t.PnL = pnl
	p.Balance = p.Balance.Add(pnl)
	p.DailyPnL = p.DailyPnL.Add(pnl)

	log.Info().
		Str("symbol", t.Symbol).
		Str("direction", t.Direction).
		Str("entry", t.EntryPrice.String()).
		Str("exit", currentPrice.String()).
		Str("hwm", t.HighWaterMark.String()).
		Str("pnl", pnl.StringFixed(2)).
		Str("reason", t.ExitReason).
		Str("result", t.Status).
		Msg("Trade closed")

	if p.Notifier != nil {
		emoji := "❌"
		if t.Status == "WIN" {
			emoji = "✅"
		}
		p.Notifier.Notify(fmt.Sprintf("%s *TRADE CLOSED*\nSymbol: `%s`\nDirection: %s\nEntry: %s → Exit: %s\nHigh: %s\nPnL: `%s`\nReason: %s\nResult: *%s*",
			emoji, t.Symbol, t.Direction, t.EntryPrice.String(), currentPrice.String(), t.HighWaterMark.String(), pnl.StringFixed(2), t.ExitReason, t.Status))
	}

	// Check Circuit Breaker
	lossLimit := p.InitialBalance.Mul(p.DailyLossLimit).Neg()
	if p.DailyPnL.LessThanOrEqual(lossLimit) {
		p.TradingEnabled = false
		log.Error().
			Str("daily_pnl", p.DailyPnL.StringFixed(2)).
			Str("limit", lossLimit.StringFixed(2)).
			Msg("CIRCUIT BREAKER TRIGGERED: Daily loss limit hit. Trading suspended.")

		if p.Notifier != nil {
			p.Notifier.Notify(fmt.Sprintf("🛑 *CIRCUIT BREAKER TRIGGERED*\nDaily PnL: `%s`\nLimit: `%s`\nTrading has been *SUSPENDED*",
				p.DailyPnL.StringFixed(2), lossLimit.StringFixed(2)))
		}
	}
	
	p.Completed = append(p.Completed, *t)
}

func (p *PaperTrader) checkDailyReset() {
	now := time.Now()
	if now.YearDay() != p.LastDailyReset.YearDay() || now.Year() != p.LastDailyReset.Year() {
		log.Info().
			Str("prev_daily_pnl", p.DailyPnL.StringFixed(2)).
			Msg("Daily risk reset. Trading re-enabled if it was suspended.")
		p.DailyPnL = decimal.Zero
		p.TradingEnabled = true
		p.InitialBalance = p.Balance // Set new baseline for today
		p.LastDailyReset = now
	}
}

func (p *PaperTrader) GetWinRate() float64 {
	p.Lock()
	defer p.Unlock()
	
	total := p.TotalWins + p.TotalLosses
	if total == 0 {
		return 0
	}
	return float64(p.TotalWins) / float64(total) * 100
}

func (p *PaperTrader) SaveState(filename string) error {
	p.Lock()
	defer p.Unlock()

	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filename, data, 0644)
}

func (p *PaperTrader) LoadState(filename string) error {
	p.Lock()
	defer p.Unlock()

	data, err := os.ReadFile(filename)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, p)
}
