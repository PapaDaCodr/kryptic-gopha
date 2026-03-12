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
	// Configure console logging for the backtester
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339})
	
	symbol := flag.String("symbol", "BTCUSDT", "Symbol to test")
	interval := flag.String("interval", "1m", "Candle interval (1m, 5m, 1h, etc)")
	limit := flag.Int("limit", 500, "Number of candles to fetch")
	flag.Parse()

	// 1. Fetch Historical Data from Binance
	log.Info().Str("symbol", *symbol).Str("interval", *interval).Int("limit", *limit).Msg("Fetching historical data")
	ticks, err := ingester.FetchHistoricalKlines(*symbol, *interval, *limit)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to fetch data")
	}

	// 2. Initialize Strategy & Trader
	strategy := engine.NewEfficientStrategy(12, 26, 14)
	trader := engine.NewPaperTrader()
	
	mgr := engine.NewEngineManager([]string{*symbol}, 1000, strategy, trader)

	log.Info().Int("points", len(ticks)).Msg("Starting backtest session")
	startTime := time.Now()

	// 3. Process Ticks
	for _, tick := range ticks {
		if err := mgr.UpdatePrice(tick); err != nil {
			log.Error().Err(err).Msg("Engine processing error")
		}
	}

	// 4. Final Report
	duration := time.Since(startTime)
	fmt.Printf("\n" + `========================================
            BACKTEST REPORT
========================================
Symbol:        %s
Interval:      %s
Total Ticks:   %d
Duration:      %v

PERFORMANCE:
----------------------------------------
Total Signals: %d
Wins:          %d
Losses:        %d
Win Rate:      %.2f%%
========================================` + "\n", 
		*symbol, *interval, len(ticks), duration,
		trader.TotalWins+trader.TotalLosses, trader.TotalWins, trader.TotalLosses, trader.GetWinRate())
}
