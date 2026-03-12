package engine

import (
	"fmt"
	"sync"
	"time"

	"github.com/papadacodr/kryptic-gopha/internal/models"
)

type Trade struct {
	Symbol     string
	EntryPrice float64
	Direction  string
	Time       time.Time
	Exits      map[int]float64 // minute -> price
	Status     string          // "ACTIVE", "WIN", "LOSS"
	ExitPrice  float64
}

type PaperTrader struct {
	sync.Mutex
	ActiveTrades []Trade
	Completed    []Trade
	TotalWins    int
	TotalLosses  int
	TP           float64 // Take Profit Percentage (e.g. 0.01 for 1%)
	SL           float64 // Stop Loss Percentage (e.g. 0.005 for 0.5%)
}

func NewPaperTrader() *PaperTrader {
	return &PaperTrader{
		ActiveTrades: make([]Trade, 0),
		Completed:    make([]Trade, 0),
		TP:           0.005, // 0.5% default take profit
		SL:           0.003, // 0.3% default stop loss
	}
}

func (p *PaperTrader) OnSignal(sig models.Signal) {
	p.Lock()
	defer p.Unlock()

	trade := Trade{
		Symbol:     sig.Symbol,
		EntryPrice: sig.Price,
		Direction:  sig.Direction,
		Time:       sig.Timestamp,
		Exits:      make(map[int]float64),
		Status:     "ACTIVE",
	}
	p.ActiveTrades = append(p.ActiveTrades, trade)
}

func (p *PaperTrader) UpdateMetrics(symbol string, currentPrice float64, now time.Time) {
	p.Lock()
	defer p.Unlock()

	for i := 0; i < len(p.ActiveTrades); i++ {
		t := &p.ActiveTrades[i]
		if t.Symbol != symbol {
			continue
		}

		isWin := false
		isLoss := false

		if t.Direction == "BUY" {
			if currentPrice >= t.EntryPrice*(1+p.TP) {
				isWin = true
			} else if currentPrice <= t.EntryPrice*(1-p.SL) {
				isLoss = true
			}
		} else {
			if currentPrice <= t.EntryPrice*(1-p.TP) {
				isWin = true
			} else if currentPrice >= t.EntryPrice*(1+p.SL) {
				isLoss = true
			}
		}

		if isWin || isLoss {
			t.Status = map[bool]string{true: "WIN", false: "LOSS"}[isWin]
			t.ExitPrice = currentPrice
			if isWin {
				p.TotalWins++
			} else {
				p.TotalLosses++
			}
			fmt.Printf("\n[TARGET HIT] %s %s | Entry: %.2f | Exit: %.2f | Result: %s\n", 
				t.Symbol, t.Direction, t.EntryPrice, currentPrice, t.Status)
			
			p.Completed = append(p.Completed, *t)
			p.ActiveTrades = append(p.ActiveTrades[:i], p.ActiveTrades[i+1:]...)
			i--
			continue
		}

		duration := now.Sub(t.Time).Minutes()
		
		checkIntervals := []int{1, 5, 10}
		for _, interval := range checkIntervals {
			if duration >= float64(interval) && t.Exits[interval] == 0 {
				t.Exits[interval] = currentPrice
			}
		}

		if t.Exits[10] != 0 {
			isWinAt10 := false
			if t.Direction == "BUY" && currentPrice > t.EntryPrice {
				isWinAt10 = true
			} else if t.Direction == "SELL" && currentPrice < t.EntryPrice {
				isWinAt10 = true
			}

			t.Status = map[bool]string{true: "WIN", false: "LOSS"}[isWinAt10]
			t.ExitPrice = currentPrice
			if isWinAt10 {
				p.TotalWins++
			} else {
				p.TotalLosses++
			}
			
			fmt.Printf("\n[TIME EXIT] %s %s | Entry: %.2f | Exit: %.2f | Result: %s\n", 
				t.Symbol, t.Direction, t.EntryPrice, currentPrice, t.Status)

			p.Completed = append(p.Completed, *t)
			p.ActiveTrades = append(p.ActiveTrades[:i], p.ActiveTrades[i+1:]...)
			i--
		}
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
