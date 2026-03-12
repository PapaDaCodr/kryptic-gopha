package engine

import (
	"encoding/json"
	"os"
	"sync"
	"time"

	"github.com/papadacodr/kryptic-gopha/internal/models"
	"github.com/rs/zerolog/log"
	"github.com/shopspring/decimal"
)

type Trade struct {
	Symbol     string                  `json:"symbol"`
	EntryPrice decimal.Decimal         `json:"entry_price"`
	Quantity   decimal.Decimal         `json:"quantity"`
	Direction  string                  `json:"direction"`
	Time       time.Time               `json:"time"`
	Exits      map[int]decimal.Decimal `json:"exits"`
	Status     string                  `json:"status"`
	ExitPrice  decimal.Decimal         `json:"exit_price"`
	PnL        decimal.Decimal         `json:"pnl"`
}

type PaperTrader struct {
	sync.Mutex
	ActiveTrades map[string][]*Trade `json:"active_trades"`
	Completed    []Trade             `json:"completed"`
	TotalWins    int                 `json:"total_wins"`
	TotalLosses  int                 `json:"total_losses"`
	
	// Risk Management Settings
	TP           decimal.Decimal     `json:"tp"`
	SL           decimal.Decimal     `json:"sl"`
	Balance      decimal.Decimal     `json:"balance"`       // Current wallet balance
	InitialBalance decimal.Decimal   `json:"initial_balance"`
	RiskPerTrade decimal.Decimal     `json:"risk_per_trade"` // % of balance to risk per trade (e.g., 0.01 for 1%)
	MaxOpenTrades int                `json:"max_open_trades"`
	DailyLossLimit decimal.Decimal   `json:"daily_loss_limit"` // % of initial balance (e.g., 0.05 for 5%)
	
	// Circuit Breaker State
	TradingEnabled bool              `json:"trading_enabled"`
	LastDailyReset time.Time         `json:"last_daily_reset"`
	DailyPnL       decimal.Decimal   `json:"daily_pnl"`
}

func NewPaperTrader(balance float64) *PaperTrader {
	bal := decimal.NewFromFloat(balance)
	return &PaperTrader{
		ActiveTrades:   make(map[string][]*Trade),
		Completed:      make([]Trade, 0),
		TP:             decimal.NewFromFloat(0.005),
		SL:             decimal.NewFromFloat(0.003),
		Balance:        bal,
		InitialBalance: bal,
		RiskPerTrade:   decimal.NewFromFloat(0.01), // 1% default risk
		MaxOpenTrades:  5,
		DailyLossLimit: decimal.NewFromFloat(0.05), // 5% daily limit
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
		Symbol:     sig.Symbol,
		EntryPrice: sig.Price,
		Quantity:   quantity,
		Direction:  sig.Direction,
		Time:       sig.Timestamp,
		Exits:      make(map[int]decimal.Decimal),
		Status:     "ACTIVE",
	}
	p.ActiveTrades[sig.Symbol] = append(p.ActiveTrades[sig.Symbol], trade)
	
	log.Info().
		Str("symbol", sig.Symbol).
		Str("direction", sig.Direction).
		Str("price", sig.Price.String()).
		Str("quantity", quantity.StringFixed(4)).
		Msg("New trade opened")
}

func (p *PaperTrader) UpdateMetrics(symbol string, currentPrice decimal.Decimal, now time.Time) {
	p.Lock()
	defer p.Unlock()

	trades, ok := p.ActiveTrades[symbol]
	if !ok {
		return
	}

	remainingTrades := make([]*Trade, 0, len(trades))

	for _, t := range trades {
		isWin := false
		isLoss := false

		if t.Direction == "BUY" {
			if currentPrice.GreaterThanOrEqual(t.EntryPrice.Mul(decimal.NewFromInt(1).Add(p.TP))) {
				isWin = true
			} else if currentPrice.LessThanOrEqual(t.EntryPrice.Mul(decimal.NewFromInt(1).Sub(p.SL))) {
				isLoss = true
			}
		} else {
			if currentPrice.LessThanOrEqual(t.EntryPrice.Mul(decimal.NewFromInt(1).Sub(p.TP))) {
				isWin = true
			} else if currentPrice.GreaterThanOrEqual(t.EntryPrice.Mul(decimal.NewFromInt(1).Add(p.SL))) {
				isLoss = true
			}
		}

		if isWin || isLoss {
			p.closeTrade(t, currentPrice, isWin)
			continue
		}

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
		Str("pnl", pnl.StringFixed(2)).
		Str("result", t.Status).
		Msg("Trade closed")

	// Check Circuit Breaker
	lossLimit := p.InitialBalance.Mul(p.DailyLossLimit).Neg()
	if p.DailyPnL.LessThanOrEqual(lossLimit) {
		p.TradingEnabled = false
		log.Error().
			Str("daily_pnl", p.DailyPnL.StringFixed(2)).
			Str("limit", lossLimit.StringFixed(2)).
			Msg("CIRCUIT BREAKER TRIGGERED: Daily loss limit hit. Trading suspended.")
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
