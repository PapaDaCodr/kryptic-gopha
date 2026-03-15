package engine

import (
	"fmt"
	"time"

	"github.com/papadacodr/kryptic-gopha/internal/exchange"
	"github.com/papadacodr/kryptic-gopha/internal/models"
	"github.com/rs/zerolog/log"
	"github.com/shopspring/decimal"
)

// LiveTrader executes real orders on Binance USDT-M Futures. All shared risk
// state and position-management logic lives in BaseTrader; only the execution
// path (placing real orders) differs here.
//
// Position lifecycle:
//  1. OnSignal: entry MARKET order only. All TP/SL/trailing tracked locally.
//  2. UpdateMetrics: evaluates TP/SL/trailing via BaseTrader.evaluateExits.
//     When an exit triggers, a MARKET close order is placed immediately.
//
// No exchange brackets (STOP_MARKET / TAKE_PROFIT_MARKET) are used because
// Binance Futures rejects these order types at /fapi/v1/order with -4120 in
// some environments. All exit logic is handled locally via market orders.
type LiveTrader struct {
	BaseTrader
	client *exchange.Client
}

// NewLiveTrader constructs a LiveTrader. Call SyncBalance after construction
// so internal accounting reflects the real Binance balance.
func NewLiveTrader(client *exchange.Client, fallbackBalance float64) *LiveTrader {
	base := newBaseTrader(fallbackBalance, "live")
	base.TP = decimal.NewFromFloat(0.005)
	base.SL = decimal.NewFromFloat(0.003)
	base.TrailingSL = true
	base.TrailingSLPct = decimal.NewFromFloat(0.003)
	base.RiskPerTrade = decimal.NewFromFloat(0.01)
	return &LiveTrader{BaseTrader: base, client: client}
}

// SyncBalance should be called once after construction to align internal
// accounting with the real Binance balance.
func (lt *LiveTrader) SyncBalance() error {
	bal, err := lt.client.GetUSDTBalance()
	if err != nil {
		return fmt.Errorf("sync balance: %w", err)
	}
	lt.Lock()
	lt.Balance = bal
	lt.InitialBalance = bal
	lt.Unlock()
	log.Info().Str("balance", bal.StringFixed(2)).Msg("Live balance synced from Binance")
	return nil
}

func (lt *LiveTrader) OnSignal(sig models.Signal) {
	lt.Lock()
	defer lt.Unlock()

	if !lt.TradingEnabled {
		log.Warn().Str("symbol", sig.Symbol).Msg("Live trade ignored: circuit breaker active")
		return
	}
	lt.checkDailyReset()

	if lt.activeCount() >= lt.MaxOpenTrades {
		log.Warn().Int("limit", lt.MaxOpenTrades).Msg("Live trade ignored: max open trades reached")
		return
	}

	entrySide := exchange.SideBuy
	if sig.Direction == "SELL" {
		entrySide = exchange.SideSell
	}

	info, err := lt.client.GetSymbolInfo(sig.Symbol)
	if err != nil {
		log.Error().Err(err).Str("symbol", sig.Symbol).Msg("Failed to get symbol info")
		return
	}

	rawQty, dynamicSLPrice := lt.computeEntrySize(sig)

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
	if executedQty.IsZero() {
		// Testnet sometimes returns status="NEW" with executedQty=0 for MARKET orders.
		executedQty = qty
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
	}
	lt.ActiveTrades[sig.Symbol] = append(lt.ActiveTrades[sig.Symbol], trade)

	log.Info().
		Str("symbol", sig.Symbol).
		Str("direction", sig.Direction).
		Str("price", entryPrice.String()).
		Str("qty", executedQty.StringFixed(4)).
		Int64("order_id", entryResult.OrderID).
		Msg("Live trade opened")

	if lt.Notifier != nil {
		lt.Notifier.Notify(fmt.Sprintf(
			"🚀 *LIVE TRADE OPENED*\nSymbol: `%s`\nDirection: %s\nPrice: %s\nQty: %s\nOrderID: %d",
			sig.Symbol, sig.Direction, entryPrice.String(), executedQty.StringFixed(4), entryResult.OrderID))
	}
}

func (lt *LiveTrader) UpdateMetrics(symbol string, currentPrice decimal.Decimal, now time.Time) {
	lt.Lock()
	defer lt.Unlock()

	trades, ok := lt.ActiveTrades[symbol]
	if !ok {
		return
	}

	remaining := make([]*Trade, 0, len(trades))

	for _, t := range trades {
		lt.updateHWM(t, currentPrice)

		isWin, isLoss, reason := lt.evaluateExits(t, currentPrice, now)
		if !isWin && !isLoss {
			remaining = append(remaining, t)
			continue
		}

		t.ExitReason = reason

		closeSide := exchange.SideSell
		if t.Direction == "SELL" {
			closeSide = exchange.SideBuy
		}
		if _, err := lt.client.PlaceMarketOrder(symbol, closeSide, t.Quantity); err != nil {
			log.Error().Err(err).Str("symbol", symbol).Str("reason", reason).
				Msg("CRITICAL: market-close failed — manual intervention may be required")
			if lt.Notifier != nil {
				lt.Notifier.Notify(fmt.Sprintf(
					"🚨 *CLOSE FAILED*\nCould not close `%s` [%s]. Check Binance.", symbol, reason))
			}
		} else {
			log.Info().Str("symbol", symbol).Str("reason", reason).
				Str("price", currentPrice.String()).Msg("Live trade closed")
			if lt.Notifier != nil {
				emoji := "✅"
				if isLoss {
					emoji = "🔴"
				}
				lt.Notifier.Notify(fmt.Sprintf(
					"%s *TRADE CLOSED*\nSymbol: `%s`\nReason: %s\nPrice: %s",
					emoji, symbol, reason, currentPrice.String()))
			}
		}

		lt.recordClose(t, currentPrice, isWin)
	}

	if len(remaining) == 0 {
		delete(lt.ActiveTrades, symbol)
	} else {
		lt.ActiveTrades[symbol] = remaining
	}
}

// emergencyClose unwinds an unprotected entry at market. Logs critical if the
// close itself fails.
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

func (lt *LiveTrader) SaveState(filename string) error {
	return lt.BaseTrader.saveState(lt, filename)
}

func (lt *LiveTrader) LoadState(filename string) error {
	return lt.BaseTrader.loadState(lt, filename)
}
