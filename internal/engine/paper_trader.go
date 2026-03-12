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
	Direction  string                  `json:"direction"`
	Time       time.Time               `json:"time"`
	Exits      map[int]decimal.Decimal `json:"exits"`
	Status     string                  `json:"status"`
	ExitPrice  decimal.Decimal         `json:"exit_price"`
}

type PaperTrader struct {
	sync.Mutex
	ActiveTrades map[string][]*Trade `json:"active_trades"`
	Completed    []Trade             `json:"completed"`
	TotalWins    int                 `json:"total_wins"`
	TotalLosses  int                 `json:"total_losses"`
	TP           decimal.Decimal     `json:"tp"`
	SL           decimal.Decimal     `json:"sl"`
}

func NewPaperTrader() *PaperTrader {
	return &PaperTrader{
		ActiveTrades: make(map[string][]*Trade),
		Completed:    make([]Trade, 0),
		TP:           decimal.NewFromFloat(0.005),
		SL:           decimal.NewFromFloat(0.003),
	}
}

func (p *PaperTrader) OnSignal(sig models.Signal) {
	p.Lock()
	defer p.Unlock()

	trade := &Trade{
		Symbol:     sig.Symbol,
		EntryPrice: sig.Price,
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
			t.Status = "LOSS"
			if isWin {
				t.Status = "WIN"
				p.TotalWins++
			} else {
				p.TotalLosses++
			}
			t.ExitPrice = currentPrice
			
			log.Info().
				Str("symbol", t.Symbol).
				Str("direction", t.Direction).
				Str("entry", t.EntryPrice.String()).
				Str("exit", currentPrice.String()).
				Str("result", t.Status).
				Msg("Target hit")
			
			p.Completed = append(p.Completed, *t)
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

			t.Status = "LOSS"
			if isWinAt10 {
				t.Status = "WIN"
				p.TotalWins++
			} else {
				p.TotalLosses++
			}
			t.ExitPrice = currentPrice
			
			log.Info().
				Str("symbol", t.Symbol).
				Str("direction", t.Direction).
				Str("entry", t.EntryPrice.String()).
				Str("exit", currentPrice.String()).
				Str("result", t.Status).
				Msg("Time exit")

			p.Completed = append(p.Completed, *t)
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
