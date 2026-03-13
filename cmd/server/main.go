package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/joho/godotenv"
	"github.com/papadacodr/kryptic-gopha/internal/engine"
	"github.com/papadacodr/kryptic-gopha/internal/exchange"
	"github.com/papadacodr/kryptic-gopha/internal/ingester"
	"github.com/papadacodr/kryptic-gopha/pkg/notifier"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/shopspring/decimal"
)

const stateFile = "trader_state.json"

func init() {
	// Load .env file (ignore error if not found, e.g. in Docker)
	_ = godotenv.Load()

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

	shortPeriod := getEnvInt("SHORT_PERIOD", 12)
	longPeriod := getEnvInt("LONG_PERIOD", 26)
	rsiPeriod := getEnvInt("RSI_PERIOD", 14)

	// Validate strategy parameters at startup so misconfiguration is caught early.
	if shortPeriod <= 0 {
		log.Fatal().Int("SHORT_PERIOD", shortPeriod).Msg("SHORT_PERIOD must be > 0")
	}
	if longPeriod <= shortPeriod {
		log.Fatal().Int("SHORT_PERIOD", shortPeriod).Int("LONG_PERIOD", longPeriod).
			Msg("LONG_PERIOD must be > SHORT_PERIOD")
	}
	if rsiPeriod <= 1 {
		log.Fatal().Int("RSI_PERIOD", rsiPeriod).Msg("RSI_PERIOD must be > 1")
	}
	if len(watchlist) == 0 || watchlist[0] == "" {
		log.Fatal().Msg("WATCHLIST must contain at least one symbol")
	}

	strategy := engine.NewEfficientStrategy(shortPeriod, longPeriod, rsiPeriod)

	// 2b. Select paper or live trading mode.
	var trader engine.Trader
	tradingMode := os.Getenv("TRADING_MODE") // "paper" (default) or "live"

	if tradingMode == "live" {
		apiKey := os.Getenv("BINANCE_API_KEY")
		apiSecret := os.Getenv("BINANCE_API_SECRET")
		if apiKey == "" || apiSecret == "" {
			log.Fatal().Msg("TRADING_MODE=live requires BINANCE_API_KEY and BINANCE_API_SECRET")
		}
		exClient := exchange.NewClient(apiKey, apiSecret)
		lt := engine.NewLiveTrader(exClient, getEnvFloat("INITIAL_BALANCE", 1000.0))
		lt.TP = getEnvDecimal("TP", "0.005")
		lt.SL = getEnvDecimal("SL", "0.003")
		lt.RiskPerTrade = getEnvDecimal("RISK_PER_TRADE", "0.01")
		lt.DailyLossLimit = getEnvDecimal("DAILY_LOSS_LIMIT", "0.05")
		lt.MaxOpenTrades = getEnvInt("MAX_OPEN_TRADES", 3)
		lt.TrailingSLPct = getEnvDecimal("TRAILING_SL_PCT", "0.003")
		if err := lt.SyncBalance(); err != nil {
			log.Warn().Err(err).Msg("Could not sync live balance; using fallback")
		}
		trader = lt
		log.Info().Msg("Trading mode: LIVE (Binance USDT-M Futures)")
	} else {
		pt := engine.NewPaperTrader(getEnvFloat("INITIAL_BALANCE", 10000.0))
		pt.TP = getEnvDecimal("TP", "0.05")
		pt.SL = getEnvDecimal("SL", "0.02")
		pt.RiskPerTrade = getEnvDecimal("RISK_PER_TRADE", "0.02")
		pt.DailyLossLimit = getEnvDecimal("DAILY_LOSS_LIMIT", "0.06")
		pt.MaxOpenTrades = getEnvInt("MAX_OPEN_TRADES", 5)
		pt.TrailingSL = os.Getenv("TRAILING_SL") != "false"
		pt.TrailingSLPct = getEnvDecimal("TRAILING_SL_PCT", "0.015")
		trader = pt
		log.Info().Msg("Trading mode: PAPER (simulation)")
	}

	// 2c. Telegram Notifications
	tgToken := os.Getenv("TELEGRAM_BOT_TOKEN")
	tgChatID := os.Getenv("TELEGRAM_CHAT_ID")
	if tgToken != "" && tgChatID != "" {
		tgNotifier := notifier.NewTelegramNotifier(tgToken, tgChatID)
		// Wire notifier to the concrete trader type.
		switch t := trader.(type) {
		case *engine.PaperTrader:
			t.Notifier = tgNotifier
		case *engine.LiveTrader:
			t.Notifier = tgNotifier
		}
		log.Info().Msg("Telegram notifications enabled")
		
		// Start listening for commands; ctx cancellation stops the goroutine.
		tgNotifier.StartListening(ctx, func(command string, args []string) string {
			switch command {
			case "/status":
				st := trader.GetState()
				status := "ACTIVE 🟢"
				if !st.TradingEnabled {
					status = "SUSPENDED 🛑"
				}
				active := 0
				for _, list := range st.ActiveTrades {
					active += len(list)
				}
				return fmt.Sprintf("🤖 *Bot Status*\n\nStatus: %s\nBalance: $%s\nDaily PnL: $%s\nActive Trades: %d",
					status, st.Balance.StringFixed(2), st.DailyPnL.StringFixed(2), active)

			case "/help", "/start":
				return "🤖 *Kryptic Gopha Commands*\n\n" +
					"• `/status` - Show bot health and PnL\n" +
					"• `/setbalance <amt>` - Update trading capital\n" +
					"• `/stop` - Emergency trading suspension\n" +
					"• `/help` - Show this list"

			case "/stop":
				trader.SetTradingEnabled(false)
				return "🛑 Trading has been manually *SUSPENDED*."

			case "/setbalance":
				if len(args) == 0 {
					return "Please provide an amount. Example: `/setbalance 50000`"
				}
				amount, err := decimal.NewFromString(args[0])
				if err != nil {
					return "Invalid amount format."
				}
				trader.SetBalance(amount)
				return fmt.Sprintf("✅ Balance successfully updated to *$%s*.", amount.StringFixed(2))

			default:
				return "Unknown command. Available:\n/status\n/stop\n/setbalance <amount>"
			}
		})
	} else {
		log.Warn().Msg("Telegram not configured. Set TELEGRAM_BOT_TOKEN and TELEGRAM_CHAT_ID to enable.")
	}

	// 3. Load previous state if exists; re-apply env config so .env always wins.
	if _, err := os.Stat(stateFile); err == nil {
		if err := trader.LoadState(stateFile); err != nil {
			log.Warn().Err(err).Msg("Failed to load state file")
		} else {
			log.Info().Msg("Previous state loaded successfully")
			// Re-apply risk config after state restore (LoadState overwrites fields).
			switch t := trader.(type) {
			case *engine.PaperTrader:
				t.TP = getEnvDecimal("TP", "0.05")
				t.SL = getEnvDecimal("SL", "0.02")
				t.TrailingSLPct = getEnvDecimal("TRAILING_SL_PCT", "0.015")
				t.RiskPerTrade = getEnvDecimal("RISK_PER_TRADE", "0.02")
				t.MaxOpenTrades = getEnvInt("MAX_OPEN_TRADES", 5)
			case *engine.LiveTrader:
				t.TP = getEnvDecimal("TP", "0.005")
				t.SL = getEnvDecimal("SL", "0.003")
				t.TrailingSLPct = getEnvDecimal("TRAILING_SL_PCT", "0.003")
				t.RiskPerTrade = getEnvDecimal("RISK_PER_TRADE", "0.01")
				t.MaxOpenTrades = getEnvInt("MAX_OPEN_TRADES", 3)
			}
		}
	}

	mgr := engine.NewEngineManager(watchlist, 500, strategy, trader)
	barSeconds := getEnvInt("BAR_INTERVAL_SECONDS", 60)
	if barSeconds < 1 {
		log.Fatal().Int("BAR_INTERVAL_SECONDS", barSeconds).Msg("BAR_INTERVAL_SECONDS must be >= 1")
	}
	mgr.BarInterval = time.Duration(barSeconds) * time.Second
	log.Info().Int("bar_interval_seconds", barSeconds).Msg("Bar interval configured")
	
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

				stats := trader.GetStats()
				log.Info().
					Int("total_signals", stats.TotalSignals).
					Float64("win_rate", stats.WinRate).
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
		stats := trader.GetStats()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"status":        "OK",
			"watchlist":     watchlist,
			"total_signals": stats.TotalSignals,
			"win_rate":      stats.WinRate,
			"active_trades": stats.ActiveTrades,
		})
	})

	// Dashboard API Endpoints
	mux.HandleFunc("/api/state", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(trader.GetState())
	})

	mux.HandleFunc("/api/trades", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(trader.GetTrades())
	})

	mux.HandleFunc("/api/signals", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		symbol := r.URL.Query().Get("symbol")
		if symbol == "" {
			http.Error(w, `{"error":"symbol required"}`, http.StatusBadRequest)
			return
		}
		json.NewEncoder(w).Encode(mgr.GetSignals(symbol))
	})

	mux.HandleFunc("/api/candles", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		symbol := r.URL.Query().Get("symbol")
		if symbol == "" {
			http.Error(w, `{"error":"symbol required"}`, http.StatusBadRequest)
			return
		}
		
		candles := mgr.GetCandles(symbol)
		json.NewEncoder(w).Encode(candles)
	})

	// Serve the dashboard statically
	fs := http.FileServer(http.Dir("./web"))
	mux.Handle("/", fs)

	srv := &http.Server{
		Addr:         ":" + port,
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
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
