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

// atrSLMultiplier controls how many ATR units the dynamic stop-loss is placed
// away from entry. 1.5x is a common convention that balances noise tolerance
// against capital efficiency.
const atrSLMultiplier = 1.5

// Trade is a single position record shared by PaperTrader and LiveTrader.
//
// DynamicSLPrice: when non-zero, this is the absolute stop-loss price
// computed as entry ± (atrSLMultiplier × ATR) at signal time. UpdateMetrics
// uses this level instead of the configured SL percentage, making the stop
// adaptive to the market's current volatility. When zero (ATR unavailable),
// the configured SL percentage is used as a fallback.
//
// TPOrderID / SLOrderID: Binance order IDs for the bracket orders placed at
// entry. Used by LiveTrader to cancel only the specific bracket belonging to
// this trade when the position closes, avoiding cancellation of other open
// positions on the same symbol.
type Trade struct {
	Symbol          string                  `json:"symbol"`
	EntryPrice      decimal.Decimal         `json:"entry_price"`
	Quantity        decimal.Decimal         `json:"quantity"`
	Direction       string                  `json:"direction"`
	Time            time.Time               `json:"time"`
	Exits           map[int]decimal.Decimal `json:"exits"`
	Status          string                  `json:"status"`
	ExitPrice       decimal.Decimal         `json:"exit_price"`
	PnL             decimal.Decimal         `json:"pnl"`
	HighWaterMark   decimal.Decimal         `json:"high_water_mark"`
	ExitReason      string                  `json:"exit_reason"`
	DynamicSLPrice  decimal.Decimal         `json:"dynamic_sl_price,omitempty"`
	TPOrderID       int64                   `json:"tp_order_id,omitempty"`
	SLOrderID       int64                   `json:"sl_order_id,omitempty"`
}

// PaperTrader simulates trade execution against live market data without
// placing real orders. It is the default mode and serves as the backtester backend.
type PaperTrader struct {
	sync.Mutex
	ActiveTrades map[string][]*Trade `json:"active_trades"`
	Completed    []Trade             `json:"completed"`
	TotalWins    int                 `json:"total_wins"`
	TotalLosses  int                 `json:"total_losses"`

	TP             decimal.Decimal `json:"tp"`
	SL             decimal.Decimal `json:"sl"`
	TrailingSL     bool            `json:"trailing_sl"`
	TrailingSLPct  decimal.Decimal `json:"trailing_sl_pct"`
	Balance        decimal.Decimal `json:"balance"`
	InitialBalance decimal.Decimal `json:"initial_balance"`
	RiskPerTrade   decimal.Decimal `json:"risk_per_trade"`
	MaxOpenTrades  int             `json:"max_open_trades"`
	DailyLossLimit decimal.Decimal `json:"daily_loss_limit"`

	TradingEnabled bool            `json:"trading_enabled"`
	LastDailyReset time.Time       `json:"last_daily_reset"`
	DailyPnL       decimal.Decimal `json:"daily_pnl"`

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
		TrailingSLPct:  decimal.NewFromFloat(0.003),
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
		log.Warn().Str("symbol", sig.Symbol).Msg("Trade ignored: circuit breaker active")
		return
	}
	p.checkDailyReset()

	activeCount := 0
	for _, trades := range p.ActiveTrades {
		activeCount += len(trades)
	}
	if activeCount >= p.MaxOpenTrades {
		log.Warn().Int("limit", p.MaxOpenTrades).Msg("Trade ignored: max open trades reached")
		return
	}

	riskAmount := p.Balance.Mul(p.RiskPerTrade)

	var quantity decimal.Decimal
	var dynamicSLPrice decimal.Decimal

	if !sig.ATR.IsZero() {
		// ATR-based sizing: risk amount / (atrSLMultiplier × ATR).
		// Quantity is independent of entry price, which correctly accounts
		// for the volatility of the current regime.
		slDistance := sig.ATR.Mul(decimal.NewFromFloat(atrSLMultiplier))
		quantity = riskAmount.Div(slDistance)

		// Store absolute SL level so UpdateMetrics does not need the ATR again.
		one := decimal.NewFromInt(1)
		if sig.Direction == "BUY" {
			dynamicSLPrice = sig.Price.Sub(slDistance)
		} else {
			_ = one
			dynamicSLPrice = sig.Price.Add(slDistance)
		}
	} else {
		// Fallback to fixed-percentage sizing when ATR is unavailable (e.g.
		// insufficient history during warm-up).
		quantity = riskAmount.Div(sig.Price.Mul(p.SL))
	}

	trade := &Trade{
		Symbol:         sig.Symbol,
		EntryPrice:     sig.Price,
		Quantity:       quantity,
		Direction:      sig.Direction,
		Time:           sig.Timestamp,
		Exits:          make(map[int]decimal.Decimal),
		Status:         "ACTIVE",
		HighWaterMark:  sig.Price,
		DynamicSLPrice: dynamicSLPrice,
	}
	p.ActiveTrades[sig.Symbol] = append(p.ActiveTrades[sig.Symbol], trade)

	log.Info().
		Str("symbol", sig.Symbol).
		Str("direction", sig.Direction).
		Str("price", sig.Price.String()).
		Str("quantity", quantity.StringFixed(4)).
		Str("sl_price", dynamicSLPrice.String()).
		Msg("Paper trade opened")

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
		if t.Direction == "BUY" && currentPrice.GreaterThan(t.HighWaterMark) {
			t.HighWaterMark = currentPrice
		} else if t.Direction == "SELL" && currentPrice.LessThan(t.HighWaterMark) {
			t.HighWaterMark = currentPrice
		}

		isWin := false
		isLoss := false
		exitReason := ""

		// TP check (always percentage-based).
		if t.Direction == "BUY" {
			if currentPrice.GreaterThanOrEqual(t.EntryPrice.Mul(one.Add(p.TP))) {
				isWin, exitReason = true, "TP_HIT"
			}
		} else {
			if currentPrice.LessThanOrEqual(t.EntryPrice.Mul(one.Sub(p.TP))) {
				isWin, exitReason = true, "TP_HIT"
			}
		}

		if !isWin {
			if p.TrailingSL {
				// Trailing stop trails the high-water mark. An exit above entry
				// is counted as a win even if the trailing stop triggered.
				if t.Direction == "BUY" {
					trailStop := t.HighWaterMark.Mul(one.Sub(p.TrailingSLPct))
					if currentPrice.LessThanOrEqual(trailStop) {
						isWin = currentPrice.GreaterThan(t.EntryPrice)
						isLoss = !isWin
						exitReason = "TRAILING_SL"
					}
				} else {
					trailStop := t.HighWaterMark.Mul(one.Add(p.TrailingSLPct))
					if currentPrice.GreaterThanOrEqual(trailStop) {
						isWin = currentPrice.LessThan(t.EntryPrice)
						isLoss = !isWin
						exitReason = "TRAILING_SL"
					}
				}
			} else {
				// Use ATR-derived absolute SL level when available, otherwise
				// fall back to the configured fixed percentage.
				if !t.DynamicSLPrice.IsZero() {
					if t.Direction == "BUY" && currentPrice.LessThanOrEqual(t.DynamicSLPrice) {
						isLoss, exitReason = true, "ATR_SL"
					} else if t.Direction == "SELL" && currentPrice.GreaterThanOrEqual(t.DynamicSLPrice) {
						isLoss, exitReason = true, "ATR_SL"
					}
				} else {
					if t.Direction == "BUY" {
						if currentPrice.LessThanOrEqual(t.EntryPrice.Mul(one.Sub(p.SL))) {
							isLoss, exitReason = true, "FIXED_SL"
						}
					} else {
						if currentPrice.GreaterThanOrEqual(t.EntryPrice.Mul(one.Add(p.SL))) {
							isLoss, exitReason = true, "FIXED_SL"
						}
					}
				}
			}
		}

		if isWin || isLoss {
			t.ExitReason = exitReason
			p.closeTrade(t, currentPrice, isWin)
			continue
		}

		// Snapshot price at fixed intervals for attribution. The 10-minute
		// snapshot doubles as the time-based exit trigger.
		duration := now.Sub(t.Time).Minutes()
		for _, interval := range []int{1, 5, 10} {
			if duration >= float64(interval) && t.Exits[interval].IsZero() {
				t.Exits[interval] = currentPrice
			}
		}
		if !t.Exits[10].IsZero() {
			isWinAt10 := (t.Direction == "BUY" && currentPrice.GreaterThan(t.EntryPrice)) ||
				(t.Direction == "SELL" && currentPrice.LessThan(t.EntryPrice))
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

// closeTrade finalises a position and fires the circuit breaker when the daily
// loss limit is breached. Must be called with p.Lock held.
func (p *PaperTrader) closeTrade(t *Trade, currentPrice decimal.Decimal, isWin bool) {
	if isWin {
		t.Status = "WIN"
		p.TotalWins++
	} else {
		t.Status = "LOSS"
		p.TotalLosses++
	}
	t.ExitPrice = currentPrice

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
		Msg("Paper trade closed")

	if p.Notifier != nil {
		emoji := "❌"
		if t.Status == "WIN" {
			emoji = "✅"
		}
		p.Notifier.Notify(fmt.Sprintf(
			"%s *TRADE CLOSED*\nSymbol: `%s`\nDirection: %s\nEntry: %s → Exit: %s\nHigh: %s\nPnL: `%s`\nReason: %s\nResult: *%s*",
			emoji, t.Symbol, t.Direction, t.EntryPrice.String(), currentPrice.String(),
			t.HighWaterMark.String(), pnl.StringFixed(2), t.ExitReason, t.Status))
	}

	lossLimit := p.InitialBalance.Mul(p.DailyLossLimit).Neg()
	if p.DailyPnL.LessThanOrEqual(lossLimit) {
		p.TradingEnabled = false
		log.Error().
			Str("daily_pnl", p.DailyPnL.StringFixed(2)).
			Str("limit", lossLimit.StringFixed(2)).
			Msg("Circuit breaker triggered: daily loss limit exceeded, trading suspended")
		if p.Notifier != nil {
			p.Notifier.Notify(fmt.Sprintf("🛑 *CIRCUIT BREAKER*\nDaily PnL: `%s`\nLimit: `%s`\nTrading *SUSPENDED*",
				p.DailyPnL.StringFixed(2), lossLimit.StringFixed(2)))
		}
	}

	p.Completed = append(p.Completed, *t)
}

func (p *PaperTrader) checkDailyReset() {
	now := time.Now()
	if now.YearDay() != p.LastDailyReset.YearDay() || now.Year() != p.LastDailyReset.Year() {
		log.Info().Str("prev_daily_pnl", p.DailyPnL.StringFixed(2)).Msg("Daily risk reset")
		p.DailyPnL = decimal.Zero
		p.TradingEnabled = true
		p.InitialBalance = p.Balance
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

func (p *PaperTrader) GetStats() TraderStats {
	p.Lock()
	defer p.Unlock()
	total := p.TotalWins + p.TotalLosses
	winRate := 0.0
	if total > 0 {
		winRate = float64(p.TotalWins) / float64(total) * 100
	}
	active := 0
	for _, trades := range p.ActiveTrades {
		active += len(trades)
	}
	return TraderStats{
		TotalSignals: total,
		WinRate:      winRate,
		ActiveTrades: active,
		TotalWins:    p.TotalWins,
		TotalLosses:  p.TotalLosses,
	}
}

func (p *PaperTrader) GetState() TraderState {
	p.Lock()
	defer p.Unlock()
	return TraderState{
		Balance:        p.Balance,
		InitialBalance: p.InitialBalance,
		DailyPnL:       p.DailyPnL,
		TotalWins:      p.TotalWins,
		TotalLosses:    p.TotalLosses,
		TradingEnabled: p.TradingEnabled,
		ActiveTrades:   deepCopyActiveTrades(p.ActiveTrades),
		Completed:      append([]Trade(nil), p.Completed...),
	}
}

func (p *PaperTrader) GetTrades() TradesSnapshot {
	p.Lock()
	defer p.Unlock()
	return TradesSnapshot{
		Active:    deepCopyActiveTrades(p.ActiveTrades),
		Completed: append([]Trade(nil), p.Completed...),
	}
}

func (p *PaperTrader) SetTradingEnabled(enabled bool) {
	p.Lock()
	defer p.Unlock()
	p.TradingEnabled = enabled
}

func (p *PaperTrader) SetBalance(bal decimal.Decimal) {
	p.Lock()
	defer p.Unlock()
	p.Balance = bal
	p.InitialBalance = bal
}

func (p *PaperTrader) SaveState(filename string) error {
	p.Lock()
	data, err := json.MarshalIndent(p, "", "  ")
	p.Unlock()
	if err != nil {
		return err
	}
	return os.WriteFile(filename, data, 0644)
}

func (p *PaperTrader) LoadState(filename string) error {
	data, err := os.ReadFile(filename)
	if err != nil {
		return err
	}
	p.Lock()
	defer p.Unlock()
	return json.Unmarshal(data, p)
}

// deepCopyActiveTrades returns a map of independent Trade copies. Struct values
// are copied (not pointers) to prevent races between the encoder and UpdateMetrics.
func deepCopyActiveTrades(src map[string][]*Trade) map[string][]*Trade {
	dst := make(map[string][]*Trade, len(src))
	for sym, trades := range src {
		cp := make([]*Trade, len(trades))
		for i, t := range trades {
			tcopy := *t
			cp[i] = &tcopy
		}
		dst[sym] = cp
	}
	return dst
}
