package main

import (
	"flag"
	"fmt"
	"log"
	"time"

	"github.com/papadacodr/kryptic-gopha/internal/engine"
	"github.com/papadacodr/kryptic-gopha/internal/ingester"
)

func main() {
	symbol := flag.String("symbol", "BTCUSDT", "Symbol to test")
	interval := flag.String("interval", "1m", "Candle interval (1m, 5m, 1h, etc)")
	limit := flag.Int("limit", 500, "Number of candles to fetch")
	flag.Parse()

	// 1. Fetch Historical Data from Binance
	fmt.Printf("Fetching %d historical klines for %s (%s)...\n", *limit, *symbol, *interval)
	ticks, err := ingester.FetchHistoricalKlines(*symbol, *interval, *limit)
	if err != nil {
		log.Fatalf("Failed to fetch data: %v", err)
	}

	// 2. Initialize Strategy & Trader
	strategy := engine.NewEfficientStrategy(12, 26, 14)
	trader := engine.NewPaperTrader()
	
	mgr := engine.NewEngineManager([]string{*symbol}, 1000, strategy, trader)

	fmt.Printf("Starting backtest on %d data points...\n", len(ticks))
	startTime := time.Now()

	// 3. Process Ticks
	for _, tick := range ticks {
		if err := mgr.UpdatePrice(tick); err != nil {
			log.Printf("Engine error: %v", err)
		}
	}

	// 4. Final Report
	duration := time.Since(startTime)
	fmt.Printf("\n--- BACKTEST COMPLETE ---\n")
	fmt.Printf("Processed %d points in %v\n", len(ticks), duration)
	fmt.Printf("-------------------------\n")
	fmt.Printf("Total Signals: %d\n", trader.TotalWins+trader.TotalLosses)
	fmt.Printf("Wins:          %d\n", trader.TotalWins)
	fmt.Printf("Losses:        %d\n", trader.TotalLosses)
	fmt.Printf("Win Rate:      %.2f%%\n", trader.GetWinRate())
	fmt.Printf("-------------------------\n")
}
