package engine

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/papadacodr/kryptic-gopha/internal/exchange"
	"github.com/papadacodr/kryptic-gopha/internal/models"
	"github.com/papadacodr/kryptic-gopha/pkg/notifier"
	"github.com/rs/zerolog/log"
	"github.com/shopspring/decimal"
)

// LiveTrader implements the Trader interface and executes real orders on
// Binance USDT-M Futures.  It mirrors the risk management logic of
// PaperTrader so behaviour is identical; only the order execution differs.
type LiveTrader struct {
	mu sync.Mutex

	client   *exchange.Client
	Notifier notifier.Notifier

	// Risk settings — match PaperTrader fields so main.go config is the same.
	TP            decimal.Decimal
	SL            decimal.Decimal
	TrailingSLPct decimal.Decimal
	RiskPerTrade  decimal.Decimal
	MaxOpenTrades int
	DailyLossLimit decimal.Decimal

	// Accounting state (mirrors PaperTrader for the dashboard and /api/state).
	Balance        decimal.Decimal
	InitialBalance decimal.Decimal
	DailyPnL       decimal.Decimal
	LastDailyReset time.Time
	TradingEnabled bool
	TotalWins      int
	TotalLosses    int
	ActiveTrades   map[string][]*Trade
	Completed      []Trade
}

// NewLiveTrader creates a LiveTrader.  The starting balance is fetched live
// from Binance at startup; the value passed here is only used as a fallback.
func NewLiveTrader(client *exchange.Client, fallbackBalance float64) *LiveTrader {
	bal := decimal.NewFromFloat(fallbackBalance)
	return &LiveTrader{
		client:         client,
		TP:             decimal.NewFromFloat(0.005),
		SL:             decimal.NewFromFloat(0.003),
		TrailingSLPct:  decimal.NewFromFloat(0.003),
		RiskPerTrade:   decimal.NewFromFloat(0.01),
		MaxOpenTrades:  5,
		DailyLossLimit: decimal.NewFromFloat(0.05),
		Balance:        bal,
		InitialBalance: bal,
		DailyPnL:       decimal.Zero,
		TradingEnabled: true,
		LastDailyReset: time.Now(),
		ActiveTrades:   make(map[string][]*Trade),
		Completed:      make([]Trade, 0),
	}
}

// SyncBalance fetches the live USDT balance from Binance and updates the
// internal accounting.  Call once at startup after creating the trader.
func (lt *LiveTrader) SyncBalance() error {
	bal, err := lt.client.GetUSDTBalance()
	if err != nil {
		return fmt.Errorf("sync balance: %w", err)
	}
	lt.mu.Lock()
	lt.Balance = bal
	lt.InitialBalance = bal
	lt.mu.Unlock()
	log.Info().Str("balance", bal.StringFixed(2)).Msg("Live balance synced from Binance")
	return nil
}

// ── Trader interface ──────────────────────────────────────────────────────────

func (lt *LiveTrader) OnSignal(sig models.Signal) {
	lt.mu.Lock()
	defer lt.mu.Unlock()

	if !lt.TradingEnabled {
		log.Warn().Str("symbol", sig.Symbol).Msg("Live trade ignored: circuit breaker active")
		return
	}
	lt.checkDailyReset()

	activeCount := 0
	for _, trades := range lt.ActiveTrades {
		activeCount += len(trades)
	}
	if activeCount >= lt.MaxOpenTrades {
		log.Warn().Int("limit", lt.MaxOpenTrades).Msg("Live trade ignored: max open trades reached")
		return
	}

	// Determine order sides.
	entrySide := exchange.SideBuy
	exitSide := exchange.SideSell
	if sig.Direction == "SELL" {
		entrySide = exchange.SideSell
		exitSide = exchange.SideBuy
	}

	// Get LOT_SIZE rules and compute quantity.
	info, err := lt.client.GetSymbolInfo(sig.Symbol)
	if err != nil {
		log.Error().Err(err).Str("symbol", sig.Symbol).Msg("Failed to get symbol info")
		return
	}

	// Check minimum notional.
	riskAmount := lt.Balance.Mul(lt.RiskPerTrade)
	rawQty := riskAmount.Div(sig.Price.Mul(lt.SL))
	qty := exchange.RoundToStepSize(rawQty, info.StepSize)

	if qty.LessThan(info.MinQty) {
		log.Warn().
			Str("symbol", sig.Symbol).
			Str("qty", qty.String()).
			Str("min_qty", info.MinQty.String()).
			Msg("Order quantity below minimum; skipping trade")
		return
	}
	if !info.MinNotional.IsZero() && qty.Mul(sig.Price).LessThan(info.MinNotional) {
		log.Warn().
			Str("symbol", sig.Symbol).
			Str("notional", qty.Mul(sig.Price).StringFixed(2)).
			Str("min_notional", info.MinNotional.StringFixed(2)).
			Msg("Order notional below minimum; skipping trade")
		return
	}

	// Place entry market order.
	result, err := lt.client.PlaceMarketOrder(sig.Symbol, entrySide, qty)
	if err != nil {
		log.Error().Err(err).Str("symbol", sig.Symbol).Msg("Failed to place entry order")
		if lt.Notifier != nil {
			lt.Notifier.Notify(fmt.Sprintf("❌ *ORDER FAILED*\n%s %s: %v", sig.Direction, sig.Symbol, err))
		}
		return
	}

	entryPrice := result.AvgPrice
	if entryPrice.IsZero() {
		entryPrice = sig.Price // fallback if avg not yet filled
	}

	one := decimal.NewFromInt(1)

	// Place TP and SL bracket orders (best-effort; log errors but don't abort).
	if sig.Direction == "BUY" {
		tpPrice := entryPrice.Mul(one.Add(lt.TP))
		slPrice := entryPrice.Mul(one.Sub(lt.SL))
		if _, err := lt.client.PlaceTakeProfitMarketOrder(sig.Symbol, exitSide, qty, tpPrice); err != nil {
			log.Error().Err(err).Str("symbol", sig.Symbol).Msg("Failed to place TP order")
		}
		if _, err := lt.client.PlaceStopMarketOrder(sig.Symbol, exitSide, qty, slPrice); err != nil {
			log.Error().Err(err).Str("symbol", sig.Symbol).Msg("Failed to place SL order")
		}
	} else {
		tpPrice := entryPrice.Mul(one.Sub(lt.TP))
		slPrice := entryPrice.Mul(one.Add(lt.SL))
		if _, err := lt.client.PlaceTakeProfitMarketOrder(sig.Symbol, exitSide, qty, tpPrice); err != nil {
			log.Error().Err(err).Str("symbol", sig.Symbol).Msg("Failed to place TP order")
		}
		if _, err := lt.client.PlaceStopMarketOrder(sig.Symbol, exitSide, qty, slPrice); err != nil {
			log.Error().Err(err).Str("symbol", sig.Symbol).Msg("Failed to place SL order")
		}
	}

	trade := &Trade{
		Symbol:        sig.Symbol,
		EntryPrice:    entryPrice,
		Quantity:      result.ExecutedQty,
		Direction:     sig.Direction,
		Time:          sig.Timestamp,
		Exits:         make(map[int]decimal.Decimal),
		Status:        "ACTIVE",
		HighWaterMark: entryPrice,
	}
	lt.ActiveTrades[sig.Symbol] = append(lt.ActiveTrades[sig.Symbol], trade)

	log.Info().
		Str("symbol", sig.Symbol).
		Str("direction", sig.Direction).
		Str("price", entryPrice.String()).
		Str("qty", result.ExecutedQty.StringFixed(4)).
		Int64("order_id", result.OrderID).
		Msg("Live trade opened")

	if lt.Notifier != nil {
		lt.Notifier.Notify(fmt.Sprintf("🚀 *LIVE TRADE OPENED*\nSymbol: `%s`\nDirection: %s\nPrice: %s\nQty: %s\nOrderID: %d",
			sig.Symbol, sig.Direction, entryPrice.String(), result.ExecutedQty.StringFixed(4), result.OrderID))
	}
}

// UpdateMetrics mirrors PaperTrader.UpdateMetrics but does NOT close orders
// here — Binance bracket orders handle TP/SL on-exchange.  We only update
// the local accounting mirror so the dashboard stays accurate.
func (lt *LiveTrader) UpdateMetrics(symbol string, currentPrice decimal.Decimal, now time.Time) {
	lt.mu.Lock()
	defer lt.mu.Unlock()

	trades, ok := lt.ActiveTrades[symbol]
	if !ok {
		return
	}

	one := decimal.NewFromInt(1)
	remaining := make([]*Trade, 0, len(trades))

	for _, t := range trades {
		// Update HWM for local display.
		if t.Direction == "BUY" && currentPrice.GreaterThan(t.HighWaterMark) {
			t.HighWaterMark = currentPrice
		} else if t.Direction == "SELL" && currentPrice.LessThan(t.HighWaterMark) {
			t.HighWaterMark = currentPrice
		}

		isWin := false
		isLoss := false
		exitReason := ""

		// Mirror the same TP/SL checks as PaperTrader so the local dashboard
		// updates when Binance would have triggered the bracket order.
		if t.Direction == "BUY" {
			if currentPrice.GreaterThanOrEqual(t.EntryPrice.Mul(one.Add(lt.TP))) {
				isWin, exitReason = true, "TP_HIT"
			} else if currentPrice.LessThanOrEqual(t.EntryPrice.Mul(one.Sub(lt.SL))) {
				isLoss, exitReason = true, "SL_HIT"
			}
		} else {
			if currentPrice.LessThanOrEqual(t.EntryPrice.Mul(one.Sub(lt.TP))) {
				isWin, exitReason = true, "TP_HIT"
			} else if currentPrice.GreaterThanOrEqual(t.EntryPrice.Mul(one.Add(lt.SL))) {
				isLoss, exitReason = true, "SL_HIT"
			}
		}

		if !isWin && !isLoss {
			// 10-minute time-based accounting close (bracket stays live on exchange).
			if now.Sub(t.Time) >= 10*time.Minute {
				if t.Direction == "BUY" {
					isWin = currentPrice.GreaterThan(t.EntryPrice)
				} else {
					isWin = currentPrice.LessThan(t.EntryPrice)
				}
				isLoss = !isWin
				exitReason = "TIME_EXIT"
			}
		}

		if isWin || isLoss {
			t.ExitReason = exitReason
			lt.closeLocal(t, currentPrice, isWin)
			// Cancel any remaining bracket orders for this position.
			if err := lt.client.CancelAllOpenOrders(symbol); err != nil {
				log.Warn().Err(err).Str("symbol", symbol).Msg("Cancel bracket orders failed")
			}
			continue
		}

		remaining = append(remaining, t)
	}

	if len(remaining) == 0 {
		delete(lt.ActiveTrades, symbol)
	} else {
		lt.ActiveTrades[symbol] = remaining
	}
}

func (lt *LiveTrader) closeLocal(t *Trade, currentPrice decimal.Decimal, isWin bool) {
	t.Status = "LOSS"
	if isWin {
		t.Status = "WIN"
		lt.TotalWins++
	} else {
		lt.TotalLosses++
	}
	t.ExitPrice = currentPrice

	var pnl decimal.Decimal
	if t.Direction == "BUY" {
		pnl = t.ExitPrice.Sub(t.EntryPrice).Mul(t.Quantity)
	} else {
		pnl = t.EntryPrice.Sub(t.ExitPrice).Mul(t.Quantity)
	}
	t.PnL = pnl
	lt.Balance = lt.Balance.Add(pnl)
	lt.DailyPnL = lt.DailyPnL.Add(pnl)

	log.Info().
		Str("symbol", t.Symbol).
		Str("pnl", pnl.StringFixed(2)).
		Str("reason", t.ExitReason).
		Str("result", t.Status).
		Msg("Live trade accounting closed")

	if lt.Notifier != nil {
		emoji := "❌"
		if t.Status == "WIN" {
			emoji = "✅"
		}
		lt.Notifier.Notify(fmt.Sprintf("%s *LIVE TRADE CLOSED*\nSymbol: `%s`\nDirection: %s\nEntry: %s → Exit: %s\nPnL: `%s`\nReason: %s\nResult: *%s*",
			emoji, t.Symbol, t.Direction, t.EntryPrice.String(), currentPrice.String(),
			pnl.StringFixed(2), t.ExitReason, t.Status))
	}

	lossLimit := lt.InitialBalance.Mul(lt.DailyLossLimit).Neg()
	if lt.DailyPnL.LessThanOrEqual(lossLimit) {
		lt.TradingEnabled = false
		log.Error().
			Str("daily_pnl", lt.DailyPnL.StringFixed(2)).
			Msg("CIRCUIT BREAKER: daily loss limit hit. Live trading suspended.")
		if lt.Notifier != nil {
			lt.Notifier.Notify(fmt.Sprintf("🛑 *CIRCUIT BREAKER*\nDaily PnL: `%s`\nLive trading *SUSPENDED*",
				lt.DailyPnL.StringFixed(2)))
		}
	}

	lt.Completed = append(lt.Completed, *t)
}

func (lt *LiveTrader) checkDailyReset() {
	now := time.Now()
	if now.YearDay() != lt.LastDailyReset.YearDay() || now.Year() != lt.LastDailyReset.Year() {
		lt.DailyPnL = decimal.Zero
		lt.TradingEnabled = true
		lt.InitialBalance = lt.Balance
		lt.LastDailyReset = now
		log.Info().Msg("Live trader daily risk reset.")
	}
}

func (lt *LiveTrader) GetStats() TraderStats {
	lt.mu.Lock()
	defer lt.mu.Unlock()
	total := lt.TotalWins + lt.TotalLosses
	wr := 0.0
	if total > 0 {
		wr = float64(lt.TotalWins) / float64(total) * 100
	}
	active := 0
	for _, trades := range lt.ActiveTrades {
		active += len(trades)
	}
	return TraderStats{TotalSignals: total, WinRate: wr, ActiveTrades: active}
}

func (lt *LiveTrader) GetState() TraderState {
	lt.mu.Lock()
	defer lt.mu.Unlock()

	activeCopy := make(map[string][]*Trade, len(lt.ActiveTrades))
	for sym, trades := range lt.ActiveTrades {
		cp := make([]*Trade, len(trades))
		copy(cp, trades)
		activeCopy[sym] = cp
	}
	completedCopy := make([]Trade, len(lt.Completed))
	copy(completedCopy, lt.Completed)

	return TraderState{
		Balance:        lt.Balance,
		InitialBalance: lt.InitialBalance,
		DailyPnL:       lt.DailyPnL,
		TotalWins:      lt.TotalWins,
		TotalLosses:    lt.TotalLosses,
		TradingEnabled: lt.TradingEnabled,
		ActiveTrades:   activeCopy,
		Completed:      completedCopy,
	}
}

func (lt *LiveTrader) GetTrades() TradesSnapshot {
	lt.mu.Lock()
	defer lt.mu.Unlock()

	activeCopy := make(map[string][]*Trade, len(lt.ActiveTrades))
	for sym, trades := range lt.ActiveTrades {
		cp := make([]*Trade, len(trades))
		copy(cp, trades)
		activeCopy[sym] = cp
	}
	completedCopy := make([]Trade, len(lt.Completed))
	copy(completedCopy, lt.Completed)

	return TradesSnapshot{Active: activeCopy, Completed: completedCopy}
}

func (lt *LiveTrader) SetTradingEnabled(enabled bool) {
	lt.mu.Lock()
	defer lt.mu.Unlock()
	lt.TradingEnabled = enabled
}

func (lt *LiveTrader) SetBalance(bal decimal.Decimal) {
	lt.mu.Lock()
	defer lt.mu.Unlock()
	lt.Balance = bal
	lt.InitialBalance = bal
}

// SaveState persists a JSON snapshot of the live trader (for restart recovery).
func (lt *LiveTrader) SaveState(filename string) error {
	lt.mu.Lock()
	data, err := json.MarshalIndent(lt, "", "  ")
	lt.mu.Unlock()
	if err != nil {
		return err
	}
	return os.WriteFile(filename, data, 0644)
}

// LoadState restores trader state from disk.
func (lt *LiveTrader) LoadState(filename string) error {
	data, err := os.ReadFile(filename)
	if err != nil {
		return err
	}
	lt.mu.Lock()
	defer lt.mu.Unlock()
	return json.Unmarshal(data, lt)
}
