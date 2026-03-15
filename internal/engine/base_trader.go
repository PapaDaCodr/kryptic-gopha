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

// atrSLMultiplier controls how many ATR units the stop-loss is placed away from
// entry. 1.5× is a common convention that balances noise tolerance against
// capital efficiency.
const atrSLMultiplier = 1.5

// Trade is a single position record shared by PaperTrader and LiveTrader.
// DynamicSLPrice is the ATR-derived stop computed as entry ± (atrSLMultiplier × ATR)
// at signal time; zero means the ATR was unavailable and the fixed SL % is used instead.
type Trade struct {
	Symbol         string                  `json:"symbol"`
	EntryPrice     decimal.Decimal         `json:"entry_price"`
	Quantity       decimal.Decimal         `json:"quantity"`
	Direction      string                  `json:"direction"`
	Time           time.Time               `json:"time"`
	Exits          map[int]decimal.Decimal `json:"exits"`
	Status         string                  `json:"status"`
	ExitPrice      decimal.Decimal         `json:"exit_price"`
	PnL            decimal.Decimal         `json:"pnl"`
	HighWaterMark  decimal.Decimal         `json:"high_water_mark"`
	ExitReason     string                  `json:"exit_reason"`
	DynamicSLPrice decimal.Decimal         `json:"dynamic_sl_price,omitempty"`
	TPOrderID      int64                   `json:"tp_order_id,omitempty"`
	SLOrderID      int64                   `json:"sl_order_id,omitempty"`
}

// BaseTrader holds all shared risk state. Embed it in a concrete trader and
// implement OnSignal/UpdateMetrics.
type BaseTrader struct {
	sync.Mutex

	ActiveTrades   map[string][]*Trade `json:"active_trades"`
	Completed      []Trade             `json:"completed"`
	TotalWins      int                 `json:"total_wins"`
	TotalLosses    int                 `json:"total_losses"`

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
	mode     string            // "paper" or "live" — controls notification labels
}

func newBaseTrader(balance float64, mode string) BaseTrader {
	bal := decimal.NewFromFloat(balance)
	return BaseTrader{
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
		mode:           mode,
	}
}

// activeCount must be called with b.Lock held.
func (b *BaseTrader) activeCount() int {
	n := 0
	for _, trades := range b.ActiveTrades {
		n += len(trades)
	}
	return n
}

// checkDailyReset resets daily PnL and re-enables trading at calendar day
// boundaries. Must be called with b.Lock held.
func (b *BaseTrader) checkDailyReset() {
	now := time.Now()
	if now.YearDay() != b.LastDailyReset.YearDay() || now.Year() != b.LastDailyReset.Year() {
		log.Info().Str("prev_daily_pnl", b.DailyPnL.StringFixed(2)).Msg("Daily risk reset")
		b.DailyPnL = decimal.Zero
		b.TradingEnabled = true
		b.InitialBalance = b.Balance
		b.LastDailyReset = now
	}
}

// updateHWM must be called with b.Lock held.
func (b *BaseTrader) updateHWM(t *Trade, price decimal.Decimal) {
	if t.Direction == "BUY" && price.GreaterThan(t.HighWaterMark) {
		t.HighWaterMark = price
	} else if t.Direction == "SELL" && price.LessThan(t.HighWaterMark) {
		t.HighWaterMark = price
	}
}

// slLevel returns the ATR-derived stop price when set at entry, or the
// configured fixed-percentage level otherwise.
func (b *BaseTrader) slLevel(t *Trade) decimal.Decimal {
	one := decimal.NewFromInt(1)
	if !t.DynamicSLPrice.IsZero() {
		return t.DynamicSLPrice
	}
	if t.Direction == "BUY" {
		return t.EntryPrice.Mul(one.Sub(b.SL))
	}
	return t.EntryPrice.Mul(one.Add(b.SL))
}

// evaluateExits checks TP, stop-loss (trailing or ATR/fixed), and a 10-minute
// time exit. Returns ("", false, false) when the position should stay open.
// Must be called with b.Lock held.
func (b *BaseTrader) evaluateExits(t *Trade, price decimal.Decimal, now time.Time) (isWin, isLoss bool, reason string) {
	one := decimal.NewFromInt(1)

	// ── Take-profit ──────────────────────────────────────────────────────────
	if t.Direction == "BUY" {
		if price.GreaterThanOrEqual(t.EntryPrice.Mul(one.Add(b.TP))) {
			return true, false, "TP_HIT"
		}
	} else {
		if price.LessThanOrEqual(t.EntryPrice.Mul(one.Sub(b.TP))) {
			return true, false, "TP_HIT"
		}
	}

	// ── Stop-loss (trailing or ATR/fixed) ────────────────────────────────────
	if b.TrailingSL {
		if t.Direction == "BUY" {
			trailStop := t.HighWaterMark.Mul(one.Sub(b.TrailingSLPct))
			if price.LessThanOrEqual(trailStop) {
				w := price.GreaterThan(t.EntryPrice)
				return w, !w, "TRAILING_SL"
			}
		} else {
			trailStop := t.HighWaterMark.Mul(one.Add(b.TrailingSLPct))
			if price.GreaterThanOrEqual(trailStop) {
				w := price.LessThan(t.EntryPrice)
				return w, !w, "TRAILING_SL"
			}
		}
	} else {
		sl := b.slLevel(t)
		if t.Direction == "BUY" && price.LessThanOrEqual(sl) {
			r := "FIXED_SL"
			if !t.DynamicSLPrice.IsZero() {
				r = "ATR_SL"
			}
			return false, true, r
		}
		if t.Direction == "SELL" && price.GreaterThanOrEqual(sl) {
			r := "FIXED_SL"
			if !t.DynamicSLPrice.IsZero() {
				r = "ATR_SL"
			}
			return false, true, r
		}
	}

	// ── Time exit (10-minute maximum hold) ───────────────────────────────────
	if now.Sub(t.Time) >= 10*time.Minute {
		w := (t.Direction == "BUY" && price.GreaterThan(t.EntryPrice)) ||
			(t.Direction == "SELL" && price.LessThan(t.EntryPrice))
		return w, !w, "TIME_EXIT"
	}

	return false, false, ""
}

// recordClose finalises a position and triggers the circuit breaker when the
// daily loss limit is breached. Must be called with b.Lock held.
func (b *BaseTrader) recordClose(t *Trade, price decimal.Decimal, isWin bool) {
	if isWin {
		t.Status = "WIN"
		b.TotalWins++
	} else {
		t.Status = "LOSS"
		b.TotalLosses++
	}
	t.ExitPrice = price

	var pnl decimal.Decimal
	if t.Direction == "BUY" {
		pnl = price.Sub(t.EntryPrice).Mul(t.Quantity)
	} else {
		pnl = t.EntryPrice.Sub(price).Mul(t.Quantity)
	}
	t.PnL = pnl
	b.Balance = b.Balance.Add(pnl)
	b.DailyPnL = b.DailyPnL.Add(pnl)

	modeUpper := "PAPER"
	if b.mode == "live" {
		modeUpper = "LIVE"
	}

	log.Info().
		Str("mode", b.mode).
		Str("symbol", t.Symbol).
		Str("direction", t.Direction).
		Str("entry", t.EntryPrice.String()).
		Str("exit", price.String()).
		Str("hwm", t.HighWaterMark.String()).
		Str("pnl", pnl.StringFixed(2)).
		Str("reason", t.ExitReason).
		Str("result", t.Status).
		Msg("Trade closed")

	if b.Notifier != nil {
		emoji := "❌"
		if isWin {
			emoji = "✅"
		}
		b.Notifier.Notify(fmt.Sprintf(
			"%s *%s TRADE CLOSED*\nSymbol: `%s`\nDirection: %s\nEntry: %s → Exit: %s\nHigh: %s\nPnL: `%s`\nReason: %s\nResult: *%s*",
			emoji, modeUpper, t.Symbol, t.Direction,
			t.EntryPrice.String(), price.String(),
			t.HighWaterMark.String(), pnl.StringFixed(2), t.ExitReason, t.Status))
	}

	lossLimit := b.InitialBalance.Mul(b.DailyLossLimit).Neg()
	if b.DailyPnL.LessThanOrEqual(lossLimit) {
		b.TradingEnabled = false
		log.Error().
			Str("daily_pnl", b.DailyPnL.StringFixed(2)).
			Str("limit", lossLimit.StringFixed(2)).
			Msg("Circuit breaker triggered: daily loss limit exceeded, trading suspended")
		if b.Notifier != nil {
			b.Notifier.Notify(fmt.Sprintf("🛑 *CIRCUIT BREAKER*\nDaily PnL: `%s`\nLimit: `%s`\nTrading *SUSPENDED*",
				b.DailyPnL.StringFixed(2), lossLimit.StringFixed(2)))
		}
	}

	b.Completed = append(b.Completed, *t)
}

// ── Trader interface methods ──────────────────────────────────────────────────

func (b *BaseTrader) GetWinRate() float64 {
	b.Lock()
	defer b.Unlock()
	total := b.TotalWins + b.TotalLosses
	if total == 0 {
		return 0
	}
	return float64(b.TotalWins) / float64(total) * 100
}

func (b *BaseTrader) GetStats() TraderStats {
	b.Lock()
	defer b.Unlock()
	total := b.TotalWins + b.TotalLosses
	winRate := 0.0
	if total > 0 {
		winRate = float64(b.TotalWins) / float64(total) * 100
	}
	return TraderStats{
		TotalSignals: total,
		WinRate:      winRate,
		ActiveTrades: b.activeCount(),
		TotalWins:    b.TotalWins,
		TotalLosses:  b.TotalLosses,
	}
}

func (b *BaseTrader) GetState() TraderState {
	b.Lock()
	defer b.Unlock()
	return TraderState{
		Balance:        b.Balance,
		InitialBalance: b.InitialBalance,
		DailyPnL:       b.DailyPnL,
		TotalWins:      b.TotalWins,
		TotalLosses:    b.TotalLosses,
		TradingEnabled: b.TradingEnabled,
		ActiveTrades:   deepCopyActiveTrades(b.ActiveTrades),
		Completed:      append([]Trade(nil), b.Completed...),
	}
}

func (b *BaseTrader) GetTrades() TradesSnapshot {
	b.Lock()
	defer b.Unlock()
	return TradesSnapshot{
		Active:    deepCopyActiveTrades(b.ActiveTrades),
		Completed: append([]Trade(nil), b.Completed...),
	}
}

func (b *BaseTrader) SetTradingEnabled(enabled bool) {
	b.Lock()
	defer b.Unlock()
	b.TradingEnabled = enabled
}

func (b *BaseTrader) SetBalance(bal decimal.Decimal) {
	b.Lock()
	defer b.Unlock()
	b.Balance = bal
	b.InitialBalance = bal
}

// saveState snapshots v while holding b's lock, then writes the JSON to disk
// after releasing the lock so disk I/O never blocks trading.
func (b *BaseTrader) saveState(v interface{}, filename string) error {
	b.Lock()
	data, err := json.MarshalIndent(v, "", "  ")
	b.Unlock()
	if err != nil {
		return err
	}
	return os.WriteFile(filename, data, 0600)
}

// loadState reads filename from disk and unmarshals it into v under b's lock.
func (b *BaseTrader) loadState(v interface{}, filename string) error {
	data, err := os.ReadFile(filename)
	if err != nil {
		return err
	}
	b.Lock()
	defer b.Unlock()
	return json.Unmarshal(data, v)
}

// deepCopyActiveTrades returns a map of independent Trade copies, preventing
// races between the JSON encoder and concurrent UpdateMetrics calls.
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

// maxPositionFraction caps the notional value of any single trade as a fraction
// of the account balance. This prevents ATR-based sizing from producing enormous
// positions on short timeframes where ATR is small relative to price.
// At 0.2, a $5,000 account can hold at most $1,000 notional per trade.
const maxPositionFraction = 0.20

// computeEntrySize returns order quantity and ATR-derived SL price. Falls back
// to fixed-percentage sizing when ATR is zero. Notional is capped at
// maxPositionFraction × balance to guard against oversized 1-minute ATR positions.
// Must be called with the trader lock held.
func (b *BaseTrader) computeEntrySize(sig models.Signal) (qty, dynamicSLPrice decimal.Decimal) {
	riskAmount := b.Balance.Mul(b.RiskPerTrade)
	if !sig.ATR.IsZero() {
		slDistance := sig.ATR.Mul(decimal.NewFromFloat(atrSLMultiplier))
		qty = riskAmount.Div(slDistance)
		if sig.Direction == "BUY" {
			dynamicSLPrice = sig.Price.Sub(slDistance)
		} else {
			dynamicSLPrice = sig.Price.Add(slDistance)
		}
	} else {
		qty = riskAmount.Div(sig.Price.Mul(b.SL))
	}

	// Cap notional: qty × price ≤ balance × maxPositionFraction
	if !sig.Price.IsZero() {
		maxNotional := b.Balance.Mul(decimal.NewFromFloat(maxPositionFraction))
		maxQty := maxNotional.Div(sig.Price)
		if qty.GreaterThan(maxQty) {
			qty = maxQty
		}
	}

	return qty, dynamicSLPrice
}
