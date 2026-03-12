package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/papadacodr/kryptic-gopha/internal/engine"
	"github.com/papadacodr/kryptic-gopha/internal/ingester"
	"github.com/papadacodr/kryptic-gopha/pkg/notifier"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/shopspring/decimal"
)

const stateFile = "trader_state.json"

func init() {
	// Configure zerolog for production (JSON) or development (Console)
	if os.Getenv("ENV") == "dev" {
		log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339})
	}
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix
}

func main() {
	// 1. Setup Context & Shutdown Handling
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// 2. Configuration from Environment Variables
	watchStr := os.Getenv("WATCHLIST")
	if watchStr == "" {
		watchStr = "BTCUSDT,ETHUSDT,BNBUSDT"
	}
	watchlist := strings.Split(watchStr, ",")
	
	strategy := engine.NewEfficientStrategy(
		getEnvInt("SHORT_PERIOD", 12),
		getEnvInt("LONG_PERIOD", 26),
		getEnvInt("RSI_PERIOD", 14),
	)

	trader := engine.NewPaperTrader(getEnvFloat("INITIAL_BALANCE", 10000.0))
	trader.TP = getEnvDecimal("TP", "0.005")
	trader.SL = getEnvDecimal("SL", "0.003")
	trader.RiskPerTrade = getEnvDecimal("RISK_PER_TRADE", "0.01")
	trader.DailyLossLimit = getEnvDecimal("DAILY_LOSS_LIMIT", "0.05")
	trader.MaxOpenTrades = getEnvInt("MAX_OPEN_TRADES", 5)

	// 2b. Telegram Notifications
	tgToken := os.Getenv("TELEGRAM_BOT_TOKEN")
	tgChatID := os.Getenv("TELEGRAM_CHAT_ID")
	if tgToken != "" && tgChatID != "" {
		trader.Notifier = notifier.NewTelegramNotifier(tgToken, tgChatID)
		log.Info().Msg("Telegram notifications enabled")
	} else {
		log.Warn().Msg("Telegram not configured. Set TELEGRAM_BOT_TOKEN and TELEGRAM_CHAT_ID to enable.")
	}

	// 3. Load previous state if exists
	if _, err := os.Stat(stateFile); err == nil {
		if err := trader.LoadState(stateFile); err != nil {
			log.Warn().Err(err).Msg("Failed to load state file")
		} else {
			log.Info().Msg("Previous state loaded successfully")
		}
	}

	mgr := engine.NewEngineManager(watchlist, 500, strategy, trader)
	
	// 4. Warm-up Phase: Load historical data
	log.Info().Int("count", len(watchlist)).Msg("Starting warm-up phase")
	for _, symbol := range watchlist {
		ticks, err := ingester.FetchHistoricalKlines(symbol, "1m", 100)
		if err != nil {
			log.Error().Err(err).Str("symbol", symbol).Msg("Warm-up fetch failed")
			continue
		}
		for _, tick := range ticks {
			if err := mgr.UpdatePrice(tick); err != nil {
				log.Error().Err(err).Str("symbol", symbol).Msg("Warm-up processing failed")
			}
		}
		log.Debug().Str("symbol", symbol).Int("ticks", len(ticks)).Msg("Warmed up symbol")
	}

	// 5. Signal Listener
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case signal := <-mgr.Signals:
				log.Info().
					Str("symbol", signal.Symbol).
					Str("direction", signal.Direction).
					Str("price", signal.Price.String()).
					Float64("confidence", signal.Confidence).
					Str("reason", signal.Reason).
					Msg("Trading signal received")
				trader.OnSignal(signal)
			}
		}
	}()

	// 6. Persistence & Report Ticker
	go func() {
		ticker := time.NewTicker(2 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := trader.SaveState(stateFile); err != nil {
					log.Error().Err(err).Msg("Failed to save state")
				}
				
				log.Info().
					Int("total_signals", trader.TotalWins+trader.TotalLosses).
					Float64("win_rate", trader.GetWinRate()).
					Msg("Periodic report")
			}
		}
	}()

	// 7. Health Check Server (JSON API)
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":        "OK",
			"watchlist":     watchlist,
			"total_signals": trader.TotalWins + trader.TotalLosses,
			"win_rate":      trader.GetWinRate(),
			"active_trades": len(trader.ActiveTrades),
		})
	})

	srv := &http.Server{
		Addr:    ":" + port,
		Handler: mux,
	}

	go func() {
		log.Info().Str("port", port).Msg("Starting health server")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal().Err(err).Msg("HTTP server failed")
		}
	}()

	// 8. Start Ingestion
	go ingester.StartBinanceStream(ctx, watchlist, mgr)

	// Wait for termination
	<-ctx.Done()
	log.Info().Msg("Shutting down gracefully...")

	// Save final state
	if err := trader.SaveState(stateFile); err != nil {
		log.Error().Err(err).Msg("Failed to save final state")
	}

	// Cleanup
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	srv.Shutdown(shutdownCtx)
	
	log.Info().Msg("Exit.")
}

func getEnvInt(key string, fallback int) int {
	if val := os.Getenv(key); val != "" {
		if i, err := strconv.Atoi(val); err == nil {
			return i
		}
	}
	return fallback
}

func getEnvDecimal(key string, fallback string) decimal.Decimal {
	if val := os.Getenv(key); val != "" {
		if d, err := decimal.NewFromString(val); err == nil {
			return d
		}
	}
	d, _ := decimal.NewFromString(fallback)
	return d
}

func getEnvFloat(key string, fallback float64) float64 {
	if val := os.Getenv(key); val != "" {
		if f, err := strconv.ParseFloat(val, 64); err == nil {
			return f
		}
	}
	return fallback
}
