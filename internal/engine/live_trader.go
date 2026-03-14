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

// LiveTrader executes real orders on Binance USDT-M Futures. Its risk
// management logic mirrors PaperTrader exactly; only the execution path differs.
//
// Position lifecycle:
//  1. OnSignal: entry MARKET → TP bracket → SL bracket. If either bracket
//     fails, the entry is closed at market immediately (emergencyClose). A
//     live position is never left unprotected.
//  2. UpdateMetrics: mirrors TP/SL locally for dashboard accuracy. When a
//     local check triggers it cancels only the bracket orders for that specific
//     trade (by stored order ID) — not all orders for the symbol.
//  3. TIME_EXIT: places a closing MARKET order to actually close the on-exchange
//     position, then cancels any remaining brackets.
type LiveTrader struct {
	mu sync.Mutex

	client   *exchange.Client
	Notifier notifier.Notifier

	TP             decimal.Decimal
	SL             decimal.Decimal
	TrailingSLPct  decimal.Decimal
	RiskPerTrade   decimal.Decimal
	MaxOpenTrades  int
	DailyLossLimit decimal.Decimal

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

// NewLiveTrader constructs a LiveTrader. The starting balance is fetched from
// Binance via SyncBalance; fallbackBalance is used only if that call fails.
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

// SyncBalance fetches the live USDT balance from Binance. Call once after
// construction so the internal accounting reflects the real account state.
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

	entrySide := exchange.SideBuy
	exitSide := exchange.SideSell
	if sig.Direction == "SELL" {
		entrySide = exchange.SideSell
		exitSide = exchange.SideBuy
	}

	info, err := lt.client.GetSymbolInfo(sig.Symbol)
	if err != nil {
		log.Error().Err(err).Str("symbol", sig.Symbol).Msg("Failed to get symbol info")
		return
	}

	// Position sizing: ATR-based when available, otherwise fixed-percentage.
	riskAmount := lt.Balance.Mul(lt.RiskPerTrade)
	var rawQty decimal.Decimal
	var dynamicSLPrice decimal.Decimal

	if !sig.ATR.IsZero() {
		slDistance := sig.ATR.Mul(decimal.NewFromFloat(atrSLMultiplier))
		rawQty = riskAmount.Div(slDistance)
		if sig.Direction == "BUY" {
			dynamicSLPrice = sig.Price.Sub(slDistance)
		} else {
			dynamicSLPrice = sig.Price.Add(slDistance)
		}
	} else {
		rawQty = riskAmount.Div(sig.Price.Mul(lt.SL))
	}

	qty := exchange.RoundToStepSize(rawQty, info.StepSize)
	if qty.LessThan(info.MinQty) {
		log.Warn().Str("symbol", sig.Symbol).Str("qty", qty.String()).Msg("Qty below minimum; skipping trade")
		return
	}
	if !info.MinNotional.IsZero() && qty.Mul(sig.Price).LessThan(info.MinNotional) {
		log.Warn().Str("symbol", sig.Symbol).Msg("Notional below minimum; skipping trade")
		return
	}

	entryResult, err := lt.client.PlaceMarketOrder(sig.Symbol, entrySide, qty)
	if err != nil {
		log.Error().Err(err).Str("symbol", sig.Symbol).Msg("Entry order failed")
		if lt.Notifier != nil {
			lt.Notifier.Notify(fmt.Sprintf("❌ *ORDER FAILED*\n%s %s: %v", sig.Direction, sig.Symbol, err))
		}
		return
	}

	entryPrice := entryResult.AvgPrice
	if entryPrice.IsZero() {
		entryPrice = sig.Price
	}
	executedQty := entryResult.ExecutedQty
	one := decimal.NewFromInt(1)

	// Determine bracket prices. Prefer the ATR-derived SL level when available
	// since it adapts to current volatility; fall back to the configured fixed %.
	var slPrice decimal.Decimal
	if !dynamicSLPrice.IsZero() {
		slPrice = dynamicSLPrice
	} else if sig.Direction == "BUY" {
		slPrice = entryPrice.Mul(one.Sub(lt.SL))
	} else {
		slPrice = entryPrice.Mul(one.Add(lt.SL))
	}

	var tpPrice decimal.Decimal
	if sig.Direction == "BUY" {
		tpPrice = entryPrice.Mul(one.Add(lt.TP))
	} else {
		tpPrice = entryPrice.Mul(one.Sub(lt.TP))
	}

	// Place TP bracket. On failure, immediately unwind the entry.
	tpResult, err := lt.client.PlaceTakeProfitMarketOrder(sig.Symbol, exitSide, executedQty, tpPrice)
	if err != nil {
		log.Error().Err(err).Str("symbol", sig.Symbol).Msg("TP order failed — closing entry at market")
		lt.emergencyClose(sig.Symbol, exitSide, executedQty, "TP placement failed")
		return
	}

	// Place SL bracket. On failure, cancel TP and unwind the entry.
	slResult, err := lt.client.PlaceStopMarketOrder(sig.Symbol, exitSide, executedQty, slPrice)
	if err != nil {
		log.Error().Err(err).Str("symbol", sig.Symbol).Msg("SL order failed — cancelling TP and closing entry at market")
		if cancelErr := lt.client.CancelOrder(sig.Symbol, tpResult.OrderID); cancelErr != nil {
			log.Error().Err(cancelErr).Str("symbol", sig.Symbol).Msg("Failed to cancel TP during SL-failure rollback")
		}
		lt.emergencyClose(sig.Symbol, exitSide, executedQty, "SL placement failed")
		return
	}

	trade := &Trade{
		Symbol:         sig.Symbol,
		EntryPrice:     entryPrice,
		Quantity:       executedQty,
		Direction:      sig.Direction,
		Time:           sig.Timestamp,
		Exits:          make(map[int]decimal.Decimal),
		Status:         "ACTIVE",
		HighWaterMark:  entryPrice,
		DynamicSLPrice: dynamicSLPrice,
		TPOrderID:      tpResult.OrderID,
		SLOrderID:      slResult.OrderID,
	}
	lt.ActiveTrades[sig.Symbol] = append(lt.ActiveTrades[sig.Symbol], trade)

	log.Info().
		Str("symbol", sig.Symbol).
		Str("direction", sig.Direction).
		Str("price", entryPrice.String()).
		Str("qty", executedQty.StringFixed(4)).
		Str("sl_price", slPrice.String()).
		Int64("order_id", entryResult.OrderID).
		Msg("Live trade opened")

	if lt.Notifier != nil {
		lt.Notifier.Notify(fmt.Sprintf(
			"🚀 *LIVE TRADE OPENED*\nSymbol: `%s`\nDirection: %s\nPrice: %s\nQty: %s\nOrderID: %d",
			sig.Symbol, sig.Direction, entryPrice.String(), executedQty.StringFixed(4), entryResult.OrderID))
	}
}

// UpdateMetrics mirrors TP/SL checks locally for dashboard accuracy. When a
// trade closes it cancels only the bracket orders for that trade by stored order
// IDs, preserving brackets for any concurrent positions on the same symbol.
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
		if t.Direction == "BUY" && currentPrice.GreaterThan(t.HighWaterMark) {
			t.HighWaterMark = currentPrice
		} else if t.Direction == "SELL" && currentPrice.LessThan(t.HighWaterMark) {
			t.HighWaterMark = currentPrice
		}

		isWin := false
		isLoss := false
		exitReason := ""

		if t.Direction == "BUY" {
			if currentPrice.GreaterThanOrEqual(t.EntryPrice.Mul(one.Add(lt.TP))) {
				isWin, exitReason = true, "TP_HIT"
			} else {
				slLevel := lt.slLevel(t, one)
				if currentPrice.LessThanOrEqual(slLevel) {
					isLoss, exitReason = true, "SL_HIT"
				}
			}
		} else {
			if currentPrice.LessThanOrEqual(t.EntryPrice.Mul(one.Sub(lt.TP))) {
				isWin, exitReason = true, "TP_HIT"
			} else {
				slLevel := lt.slLevel(t, one)
				if currentPrice.GreaterThanOrEqual(slLevel) {
					isLoss, exitReason = true, "SL_HIT"
				}
			}
		}

		if !isWin && !isLoss && now.Sub(t.Time) >= 10*time.Minute {
			isWin = (t.Direction == "BUY" && currentPrice.GreaterThan(t.EntryPrice)) ||
				(t.Direction == "SELL" && currentPrice.LessThan(t.EntryPrice))
			isLoss = !isWin
			exitReason = "TIME_EXIT"
		}

		if isWin || isLoss {
			t.ExitReason = exitReason

			if exitReason == "TIME_EXIT" {
				// TIME_EXIT: position still open on exchange — close at market,
				// then cancel both bracket orders.
				closeSide := exchange.SideSell
				if t.Direction == "SELL" {
					closeSide = exchange.SideBuy
				}
				if _, err := lt.client.PlaceMarketOrder(symbol, closeSide, t.Quantity); err != nil {
					log.Error().Err(err).Str("symbol", symbol).
						Msg("CRITICAL: TIME_EXIT market-close failed — manual intervention may be required")
					if lt.Notifier != nil {
						lt.Notifier.Notify(fmt.Sprintf(
							"🚨 *TIME-EXIT FAILED*\nCould not close `%s` position. Check Binance.", symbol))
					}
				}
				lt.cancelTradeBrackets(symbol, t)
			} else if exitReason == "TP_HIT" {
				// TP bracket executed on exchange; cancel the orphaned SL bracket.
				if t.SLOrderID != 0 {
					if err := lt.client.CancelOrder(symbol, t.SLOrderID); err != nil {
						log.Warn().Err(err).Int64("order_id", t.SLOrderID).Msg("Failed to cancel SL after TP hit")
					}
				}
			} else {
				// SL_HIT: cancel the orphaned TP bracket.
				if t.TPOrderID != 0 {
					if err := lt.client.CancelOrder(symbol, t.TPOrderID); err != nil {
						log.Warn().Err(err).Int64("order_id", t.TPOrderID).Msg("Failed to cancel TP after SL hit")
					}
				}
			}

			lt.closeLocal(t, currentPrice, isWin)
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

// slLevel returns the effective stop-loss price for a trade: ATR-derived when
// available, fixed-percentage otherwise.
func (lt *LiveTrader) slLevel(t *Trade, one decimal.Decimal) decimal.Decimal {
	if !t.DynamicSLPrice.IsZero() {
		return t.DynamicSLPrice
	}
	if t.Direction == "BUY" {
		return t.EntryPrice.Mul(one.Sub(lt.SL))
	}
	return t.EntryPrice.Mul(one.Add(lt.SL))
}

// cancelTradeBrackets cancels both the TP and SL orders for a single trade.
// Errors are warnings because the bracket may have already been filled or
// cancelled on the exchange before our cancel arrives.
func (lt *LiveTrader) cancelTradeBrackets(symbol string, t *Trade) {
	if t.TPOrderID != 0 {
		if err := lt.client.CancelOrder(symbol, t.TPOrderID); err != nil {
			log.Warn().Err(err).Int64("order_id", t.TPOrderID).Str("symbol", symbol).Msg("Failed to cancel TP bracket")
		}
	}
	if t.SLOrderID != 0 {
		if err := lt.client.CancelOrder(symbol, t.SLOrderID); err != nil {
			log.Warn().Err(err).Int64("order_id", t.SLOrderID).Str("symbol", symbol).Msg("Failed to cancel SL bracket")
		}
	}
}

// emergencyClose places a market order to unwind an unprotected entry. If the
// close itself fails, a critical alert is fired because the position requires
// manual handling.
func (lt *LiveTrader) emergencyClose(symbol string, side exchange.OrderSide, qty decimal.Decimal, reason string) {
	if _, err := lt.client.PlaceMarketOrder(symbol, side, qty); err != nil {
		log.Error().Err(err).Str("symbol", symbol).Str("reason", reason).
			Msg("CRITICAL: emergency close failed — position is unprotected, manual intervention required")
		if lt.Notifier != nil {
			lt.Notifier.Notify(fmt.Sprintf(
				"🚨 *CRITICAL: MANUAL CLOSE REQUIRED*\n`%s` has an unprotected position.\nReason: %s", symbol, reason))
		}
		return
	}
	log.Warn().Str("symbol", symbol).Str("reason", reason).Msg("Entry unwound via emergency close")
	if lt.Notifier != nil {
		lt.Notifier.Notify(fmt.Sprintf("⚠️ *ENTRY UNWOUND*\n`%s` entry closed at market.\nReason: %s", symbol, reason))
	}
}

func (lt *LiveTrader) closeLocal(t *Trade, currentPrice decimal.Decimal, isWin bool) {
	if isWin {
		t.Status = "WIN"
		lt.TotalWins++
	} else {
		t.Status = "LOSS"
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
		lt.Notifier.Notify(fmt.Sprintf(
			"%s *LIVE TRADE CLOSED*\nSymbol: `%s`\nDirection: %s\nEntry: %s → Exit: %s\nPnL: `%s`\nReason: %s\nResult: *%s*",
			emoji, t.Symbol, t.Direction, t.EntryPrice.String(), currentPrice.String(),
			pnl.StringFixed(2), t.ExitReason, t.Status))
	}

	lossLimit := lt.InitialBalance.Mul(lt.DailyLossLimit).Neg()
	if lt.DailyPnL.LessThanOrEqual(lossLimit) {
		lt.TradingEnabled = false
		log.Error().Str("daily_pnl", lt.DailyPnL.StringFixed(2)).
			Msg("Circuit breaker: daily loss limit hit, live trading suspended")
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
		log.Info().Msg("Live trader daily risk reset")
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
	return TraderStats{TotalSignals: total, WinRate: wr, ActiveTrades: active, TotalWins: lt.TotalWins, TotalLosses: lt.TotalLosses}
}

func (lt *LiveTrader) GetState() TraderState {
	lt.mu.Lock()
	defer lt.mu.Unlock()
	return TraderState{
		Balance: lt.Balance, InitialBalance: lt.InitialBalance, DailyPnL: lt.DailyPnL,
		TotalWins: lt.TotalWins, TotalLosses: lt.TotalLosses, TradingEnabled: lt.TradingEnabled,
		ActiveTrades: deepCopyActiveTrades(lt.ActiveTrades),
		Completed:    append([]Trade(nil), lt.Completed...),
	}
}

func (lt *LiveTrader) GetTrades() TradesSnapshot {
	lt.mu.Lock()
	defer lt.mu.Unlock()
	return TradesSnapshot{Active: deepCopyActiveTrades(lt.ActiveTrades), Completed: append([]Trade(nil), lt.Completed...)}
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

func (lt *LiveTrader) SaveState(filename string) error {
	lt.mu.Lock()
	data, err := json.MarshalIndent(lt, "", "  ")
	lt.mu.Unlock()
	if err != nil {
		return err
	}
	return os.WriteFile(filename, data, 0644)
}

func (lt *LiveTrader) LoadState(filename string) error {
	data, err := os.ReadFile(filename)
	if err != nil {
		return err
	}
	lt.mu.Lock()
	defer lt.mu.Unlock()
	return json.Unmarshal(data, lt)
}
