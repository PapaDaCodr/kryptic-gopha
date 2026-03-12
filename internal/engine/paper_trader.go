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
}

type PaperTrader struct {
	sync.Mutex
	ActiveTrades []Trade
	Completed    []Trade
	TotalWins    int
	TotalLosses  int
}

func NewPaperTrader() *PaperTrader {
	return &PaperTrader{
		ActiveTrades: make([]Trade, 0),
		Completed:    make([]Trade, 0),
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
	}
	p.ActiveTrades = append(p.ActiveTrades, trade)
}

func (p *PaperTrader) UpdateMetrics(symbol string, currentPrice float64) {
	p.Lock()
	defer p.Unlock()

	for i := 0; i < len(p.ActiveTrades); i++ {
		t := &p.ActiveTrades[i]
		if t.Symbol != symbol {
			continue
		}

		duration := time.Since(t.Time).Minutes()
		
		checkIntervals := []int{1, 5, 10}
		for _, interval := range checkIntervals {
			if duration >= float64(interval) && t.Exits[interval] == 0 {
				t.Exits[interval] = currentPrice
				
				// Calculate win/loss specifically for the 5-minute benchmark
				if interval == 5 {
					isWin := false
					if t.Direction == "BUY" && currentPrice > t.EntryPrice {
						isWin = true
					} else if t.Direction == "SELL" && currentPrice < t.EntryPrice {
						isWin = true
					}

					if isWin {
						p.TotalWins++
					} else {
						p.TotalLosses++
					}
					
					fmt.Printf("\n[BENCHMARK] %s %s at %f | 5m Price: %f | Result: %s\n", 
						t.Symbol, t.Direction, t.EntryPrice, currentPrice, map[bool]string{true: "WIN", false: "LOSS"}[isWin])
				}
			}
		}

		// Move to completed if all intervals checked
		if t.Exits[10] != 0 {
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
