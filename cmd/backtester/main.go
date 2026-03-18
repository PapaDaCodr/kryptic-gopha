package main

import (
	"encoding/csv"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/papadacodr/kryptic-gopha/internal/engine"
	"github.com/papadacodr/kryptic-gopha/internal/ingester"
	"github.com/papadacodr/kryptic-gopha/internal/models"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/shopspring/decimal"
)

func main() {
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339})

	symbols := flag.String("symbols", "BTCUSDT", "Comma-separated symbols to backtest (e.g. BTCUSDT,ETHUSDT)")
	interval := flag.String("interval", "1m", "Kline interval (1m, 5m, 1h, etc.)")
	limit := flag.Int("limit", 500, "Number of candles per symbol")
	output := flag.String("output", "", "CSV output file (optional)")
	shortPeriod := flag.Int("short", 12, "EMA short period")
	longPeriod := flag.Int("long", 26, "EMA long period")
	rsiPeriod := flag.Int("rsi", 14, "RSI period")
	flag.Parse()

	symbolList := strings.Split(*symbols, ",")
	for i := range symbolList {
		symbolList[i] = strings.TrimSpace(symbolList[i])
	}

	log.Info().
		Strs("symbols", symbolList).
		Str("interval", *interval).
		Int("limit", *limit).
		Msg("Starting multi-symbol backtest")

	// Fetch klines for all symbols
	type marketTick struct {
		data  any
		order int
	}
	var allTicks []marketTick
	tickOrder := 0

	for _, sym := range symbolList {
		ticks, err := ingester.FetchHistoricalKlines(sym, *interval, *limit)
		if err != nil {
			log.Error().Err(err).Str("symbol", sym).Msg("Failed to fetch klines")
			continue
		}
		log.Info().Str("symbol", sym).Int("ticks", len(ticks)).Msg("Fetched klines")

		for _, tick := range ticks {
			allTicks = append(allTicks, marketTick{tick, tickOrder})
			tickOrder++
		}
	}

	if len(allTicks) == 0 {
		log.Fatal().Msg("No klines fetched; cannot backtest")
	}

	// Run backtest
	trader := engine.NewPaperTrader(10000.0)
	strategy := engine.NewEfficientStrategy(*shortPeriod, *longPeriod, *rsiPeriod)
	mgr := engine.NewEngineManager(symbolList, 1000, strategy, trader)

	log.Info().Int("total_ticks", len(allTicks)).Msg("Starting backtest replay")
	start := time.Now()

	// Replay ticks in order
	for _, mt := range allTicks {
		tick := mt.data.(models.MarketTick)
		if err := mgr.UpdatePrice(tick); err != nil {
			log.Warn().Err(err).Msg("Tick processing error")
		}
	}

	elapsed := time.Since(start)
	stats := trader.GetStats()
	state := trader.GetState()

	// Calculate metrics
	totalPnL := state.Balance.Sub(state.InitialBalance)
	maxDrawdown := calculateMaxDrawdown(state.Completed, state.InitialBalance)
	avgWinSize := decimal.Zero
	avgLossSize := decimal.Zero

	if stats.TotalWins > 0 {
		var totalWinPnL decimal.Decimal
		for _, t := range state.Completed {
			if t.PnL.GreaterThan(decimal.Zero) {
				totalWinPnL = totalWinPnL.Add(t.PnL)
			}
		}
		avgWinSize = totalWinPnL.Div(decimal.NewFromInt(int64(stats.TotalWins)))
	}

	if stats.TotalLosses > 0 {
		var totalLossPnL decimal.Decimal
		for _, t := range state.Completed {
			if t.PnL.LessThan(decimal.Zero) {
				totalLossPnL = totalLossPnL.Add(t.PnL)
			}
		}
		avgLossSize = totalLossPnL.Div(decimal.NewFromInt(int64(stats.TotalLosses)))
	}

	// Print report
	fmt.Printf("\n========================================\n")
	fmt.Printf("      MULTI-SYMBOL BACKTEST REPORT\n")
	fmt.Printf("========================================\n")
	fmt.Printf("Symbols:       %s\n", strings.Join(symbolList, ", "))
	fmt.Printf("Interval:      %s\n", *interval)
	fmt.Printf("Total Ticks:   %d\n", len(allTicks))
	fmt.Printf("Duration:      %v\n", elapsed)
	fmt.Printf("\nPERFORMANCE:\n")
	fmt.Printf("----------------------------------------\n")
	fmt.Printf("Total Trades:  %d\n", stats.TotalSignals)
	fmt.Printf("Wins:          %d\n", stats.TotalWins)
	fmt.Printf("Losses:        %d\n", stats.TotalLosses)
	fmt.Printf("Win Rate:      %.2f%%\n", stats.WinRate)
	fmt.Printf("Avg Win:       $%s\n", avgWinSize.StringFixed(2))
	fmt.Printf("Avg Loss:      $%s\n", avgLossSize.StringFixed(2))
	fmt.Printf("\nCAPITAL:\n")
	fmt.Printf("----------------------------------------\n")
	fmt.Printf("Starting:      $%s\n", state.InitialBalance.StringFixed(2))
	fmt.Printf("Ending:        $%s\n", state.Balance.StringFixed(2))
	fmt.Printf("Total P&L:     $%s\n", totalPnL.StringFixed(2))
	fmt.Printf("Max Drawdown:  %.2f%%\n", maxDrawdown*100)
	fmt.Printf("========================================\n\n")

	// Print per-trade log
	if len(state.Completed) > 0 {
		fmt.Println("TRADES:")
		fmt.Printf("%-12s %-6s %-10s %-10s %-6s %-12s\n", "Symbol", "Dir", "Entry", "Exit", "P&L", "Reason")
		fmt.Println(strings.Repeat("-", 70))
		for _, t := range state.Completed {
			dir := t.Direction
			fmt.Printf("%-12s %-6s %-10s %-10s %-6s %-12s\n",
				t.Symbol,
				dir,
				t.EntryPrice.StringFixed(2),
				t.ExitPrice.StringFixed(2),
				t.PnL.StringFixed(2),
				t.ExitReason,
			)
		}
		fmt.Println()
	}

	// Write CSV if requested
	if *output != "" {
		if err := writeBacktestCSV(*output, symbolList, *interval, *limit, stats, state, maxDrawdown); err != nil {
			log.Error().Err(err).Str("file", *output).Msg("Failed to write CSV")
		} else {
			log.Info().Str("file", *output).Msg("Results written to CSV")
		}
	}
}

func calculateMaxDrawdown(trades []engine.Trade, initialBalance decimal.Decimal) float64 {
	if len(trades) == 0 {
		return 0.0
	}

	peak := initialBalance
	maxDD := decimal.Zero

	balance := initialBalance
	for _, t := range trades {
		balance = balance.Add(t.PnL)
		if balance.GreaterThan(peak) {
			peak = balance
		}
		drawdown := peak.Sub(balance)
		if drawdown.GreaterThan(maxDD) {
			maxDD = drawdown
		}
	}

	if peak.IsZero() {
		return 0.0
	}

	dd := maxDD.Div(peak).InexactFloat64()
	return dd
}

func writeBacktestCSV(filename string, symbols []string, interval string, limit int, stats engine.TraderStats, state engine.TraderState, maxDD float64) error {
	file, err := os.Create(filename)
	if err != nil {
		return fmt.Errorf("create file: %w", err)
	}
	defer file.Close()

	w := csv.NewWriter(file)
	defer w.Flush()

	// Header with metadata
	w.Write([]string{"Backtest Report"})
	w.Write([]string{"Symbols", strings.Join(symbols, ",")})
	w.Write([]string{"Interval", interval})
	w.Write([]string{"Limit", strconv.Itoa(limit)})
	w.Write([]string{"Total Trades", strconv.Itoa(stats.TotalSignals)})
	w.Write([]string{"Wins", strconv.Itoa(stats.TotalWins)})
	w.Write([]string{"Losses", strconv.Itoa(stats.TotalLosses)})
	w.Write([]string{"Win Rate", fmt.Sprintf("%.2f%%", stats.WinRate)})
	w.Write([]string{"Starting Balance", state.InitialBalance.StringFixed(2)})
	w.Write([]string{"Ending Balance", state.Balance.StringFixed(2)})
	w.Write([]string{"Total P&L", state.Balance.Sub(state.InitialBalance).StringFixed(2)})
	w.Write([]string{"Max Drawdown", fmt.Sprintf("%.2f%%", maxDD*100)})
	w.Write([]string{}) // blank line

	// Trades
	w.Write([]string{"Symbol", "Direction", "EntryPrice", "ExitPrice", "Quantity", "P&L", "Reason"})
	for _, t := range state.Completed {
		w.Write([]string{
			t.Symbol,
			t.Direction,
			t.EntryPrice.String(),
			t.ExitPrice.String(),
			t.Quantity.String(),
			t.PnL.String(),
			t.ExitReason,
		})
	}

	return nil
}
