package engine

import (
	"fmt"
	"time"

	"github.com/papadacodr/kryptic-gopha/internal/models"
	"github.com/rs/zerolog/log"
	"github.com/shopspring/decimal"
)

// PaperTrader simulates execution against live data. All risk logic lives in BaseTrader.
type PaperTrader struct {
	BaseTrader
}

func NewPaperTrader(balance float64) *PaperTrader {
	base := newBaseTrader(balance, "paper")
	base.TP = decimal.NewFromFloat(0.005)
	base.SL = decimal.NewFromFloat(0.003)
	base.TrailingSL = true
	base.TrailingSLPct = decimal.NewFromFloat(0.003)
	base.RiskPerTrade = decimal.NewFromFloat(0.01)
	return &PaperTrader{BaseTrader: base}
}

func (p *PaperTrader) OnSignal(sig models.Signal) {
	p.Lock()
	defer p.Unlock()

	if !p.TradingEnabled {
		log.Warn().Str("symbol", sig.Symbol).Msg("Paper trade ignored: circuit breaker active")
		return
	}
	p.checkDailyReset()

	if p.activeCount() >= p.MaxOpenTrades {
		log.Warn().Int("limit", p.MaxOpenTrades).Msg("Paper trade ignored: max open trades reached")
		return
	}

	qty, dynamicSLPrice := p.computeEntrySize(sig)

	trade := &Trade{
		Symbol:         sig.Symbol,
		EntryPrice:     sig.Price,
		Quantity:       qty,
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
		Str("quantity", qty.StringFixed(4)).
		Str("sl_price", dynamicSLPrice.String()).
		Msg("Paper trade opened")

	if p.Notifier != nil {
		p.Notifier.Notify(fmt.Sprintf("🚀 *NEW PAPER TRADE*\nSymbol: `%s`\nDirection: %s\nPrice: %s\nQty: %s",
			sig.Symbol, sig.Direction, sig.Price.String(), qty.StringFixed(4)))
	}
}

func (p *PaperTrader) UpdateMetrics(symbol string, currentPrice decimal.Decimal, now time.Time) {
	p.Lock()
	defer p.Unlock()

	trades, ok := p.ActiveTrades[symbol]
	if !ok {
		return
	}

	remaining := make([]*Trade, 0, len(trades))

	for _, t := range trades {
		p.updateHWM(t, currentPrice)

		isWin, isLoss, reason := p.evaluateExits(t, currentPrice, now)

		if isWin || isLoss {
			t.ExitReason = reason
			p.recordClose(t, currentPrice, isWin)
			continue
		}

		// Snapshot price at fixed intervals for attribution.
		duration := now.Sub(t.Time).Minutes()
		for _, interval := range []int{1, 5, 10} {
			if duration >= float64(interval) && t.Exits[interval].IsZero() {
				t.Exits[interval] = currentPrice
			}
		}

		remaining = append(remaining, t)
	}

	if len(remaining) == 0 {
		delete(p.ActiveTrades, symbol)
	} else {
		p.ActiveTrades[symbol] = remaining
	}
}

func (p *PaperTrader) SaveState(filename string) error {
	return p.BaseTrader.saveState(p, filename)
}

func (p *PaperTrader) LoadState(filename string) error {
	return p.BaseTrader.loadState(p, filename)
}
