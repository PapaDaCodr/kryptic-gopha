package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/papadacodr/kryptic-gopha/internal/engine"
	"github.com/papadacodr/kryptic-gopha/internal/ingester"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func main() {
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339})

	symbol := flag.String("symbol", "BTCUSDT", "Symbol to backtest")
	interval := flag.String("interval", "1m", "Kline interval (1m, 5m, 1h, etc.)")
	limit := flag.Int("limit", 500, "Number of candles to fetch")
	flag.Parse()

	log.Info().
		Str("symbol", *symbol).
		Str("interval", *interval).
		Int("limit", *limit).
		Msg("Fetching historical data")

	ticks, err := ingester.FetchHistoricalKlines(*symbol, *interval, *limit)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to fetch klines")
	}

	// Declare as Trader interface so the report uses only the public contract.
	var trader engine.Trader = engine.NewPaperTrader(10000.0)
	strategy := engine.NewEfficientStrategy(12, 26, 14)
	mgr := engine.NewEngineManager([]string{*symbol}, 1000, strategy, trader)

	log.Info().Int("ticks", len(ticks)).Msg("Starting backtest")
	start := time.Now()

	for _, tick := range ticks {
		if err := mgr.UpdatePrice(tick); err != nil {
			log.Error().Err(err).Msg("Tick processing error")
		}
	}

	stats := trader.GetStats()
	elapsed := time.Since(start)

	fmt.Printf("\n========================================\n")
	fmt.Printf("           BACKTEST REPORT\n")
	fmt.Printf("========================================\n")
	fmt.Printf("Symbol:        %s\n", *symbol)
	fmt.Printf("Interval:      %s\n", *interval)
	fmt.Printf("Total Ticks:   %d\n", len(ticks))
	fmt.Printf("Duration:      %v\n", elapsed)
	fmt.Printf("\nPERFORMANCE:\n")
	fmt.Printf("----------------------------------------\n")
	fmt.Printf("Total Trades:  %d\n", stats.TotalSignals)
	fmt.Printf("Wins:          %d\n", stats.TotalWins)
	fmt.Printf("Losses:        %d\n", stats.TotalLosses)
	fmt.Printf("Win Rate:      %.2f%%\n", stats.WinRate)
	fmt.Printf("========================================\n\n")
}
