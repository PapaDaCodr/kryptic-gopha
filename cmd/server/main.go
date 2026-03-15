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
	// .env is optional; Docker deployments inject vars directly.
	_ = godotenv.Load()
	if os.Getenv("ENV") == "dev" {
		log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339})
	}
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// ── Configuration ─────────────────────────────────────────────────────────

	watchStr := os.Getenv("WATCHLIST")
	if watchStr == "" {
		watchStr = "BTCUSDT,ETHUSDT,BNBUSDT"
	}
	watchlist := strings.Split(watchStr, ",")

	shortPeriod := getEnvInt("SHORT_PERIOD", 12)
	longPeriod := getEnvInt("LONG_PERIOD", 26)
	rsiPeriod := getEnvInt("RSI_PERIOD", 14)

	// Fail fast on invalid strategy parameters so misconfiguration is caught
	// before any network connections are established.
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

	// ── Trader Selection ──────────────────────────────────────────────────────

	testnet := os.Getenv("BINANCE_TESTNET") == "true"
	if testnet {
		log.Info().Msg("Binance Testnet mode enabled (testnet.binancefuture.com)")
	}

	var trader engine.Trader
	tradingMode := os.Getenv("TRADING_MODE")

	if tradingMode == "live" {
		apiKey := os.Getenv("BINANCE_API_KEY")
		// Support both BINANCE_API_SECRET (canonical) and BINANCE_SECRET_KEY (legacy alias)
		apiSecret := os.Getenv("BINANCE_API_SECRET")
		if apiSecret == "" {
			apiSecret = os.Getenv("BINANCE_SECRET_KEY")
		}
		if apiKey == "" || apiSecret == "" {
			log.Fatal().Msg("TRADING_MODE=live requires BINANCE_API_KEY and BINANCE_API_SECRET")
		}
		exClient := exchange.NewClient(apiKey, apiSecret, testnet)
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
		// Set leverage for all watched symbols. Default: 10x, configurable via LEVERAGE env var.
		leverage := getEnvInt("LEVERAGE", 10)
		for _, sym := range watchlist {
			if err := exClient.SetLeverage(sym, leverage); err != nil {
				log.Warn().Err(err).Str("symbol", sym).Int("leverage", leverage).Msg("SetLeverage failed")
			} else {
				log.Info().Str("symbol", sym).Int("leverage", leverage).Msg("Leverage set")
			}
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

	// ── Telegram ──────────────────────────────────────────────────────────────

	tgToken := os.Getenv("TELEGRAM_BOT_TOKEN")
	tgChatID := os.Getenv("TELEGRAM_CHAT_ID")
	if tgToken != "" && tgChatID != "" {
		tgNotifier := notifier.NewTelegramNotifier(tgToken, tgChatID)
		switch t := trader.(type) {
		case *engine.PaperTrader:
			t.Notifier = tgNotifier
		case *engine.LiveTrader:
			t.Notifier = tgNotifier
		}
		log.Info().Msg("Telegram notifications enabled")

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
					"• `/resume` - Resume after suspension or circuit breaker\n" +
					"• `/help` - Show this list"

			case "/stop":
				trader.SetTradingEnabled(false)
				return "🛑 Trading has been manually *SUSPENDED*."

			case "/resume":
				trader.SetTradingEnabled(true)
				return "✅ Trading has been *RESUMED*. Circuit breaker is cleared."

			case "/setbalance":
				if len(args) == 0 {
					return "Please provide an amount. Example: `/setbalance 50000`"
				}
				amount, err := decimal.NewFromString(args[0])
				if err != nil {
					return "Invalid amount format."
				}
				trader.SetBalance(amount)
				return fmt.Sprintf("✅ Balance updated to *$%s*.", amount.StringFixed(2))

			default:
				return "Unknown command. Use /help for available commands."
			}
		})
	} else {
		log.Warn().Msg("Telegram not configured (set TELEGRAM_BOT_TOKEN + TELEGRAM_CHAT_ID to enable)")
	}

	// ── State Restore ─────────────────────────────────────────────────────────

	// LoadState overwrites all fields including risk parameters, so re-apply
	// env config after restore to ensure .env always takes precedence.
	if _, err := os.Stat(stateFile); err == nil {
		if err := trader.LoadState(stateFile); err != nil {
			log.Warn().Err(err).Msg("Failed to load state file")
		} else {
			log.Info().Msg("Previous state loaded")
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

	// ── Engine Setup ──────────────────────────────────────────────────────────

	mgr := engine.NewEngineManager(watchlist, 500, strategy, trader)
	barSeconds := getEnvInt("BAR_INTERVAL_SECONDS", 60)
	if barSeconds < 1 {
		log.Fatal().Int("BAR_INTERVAL_SECONDS", barSeconds).Msg("BAR_INTERVAL_SECONDS must be >= 1")
	}
	mgr.BarInterval = time.Duration(barSeconds) * time.Second
	log.Info().Int("bar_interval_seconds", barSeconds).Msg("Bar interval configured")

	// Warm-up: seed price history from REST klines so the strategy can produce
	// valid signals on the first live bar. EMA(200) requires at least 200 bars to
	// stabilise, so we fetch 250 to give all indicators a comfortable margin.
	klineInterval := barSecondsToInterval(barSeconds)
	const warmupBars = 250
	log.Info().Int("symbols", len(watchlist)).Str("interval", klineInterval).Int("bars", warmupBars).
		Msg("Warm-up: fetching historical klines")
	for _, symbol := range watchlist {
		ticks, err := ingester.FetchHistoricalKlines(symbol, klineInterval, warmupBars)
		if err != nil {
			log.Error().Err(err).Str("symbol", symbol).Msg("Warm-up fetch failed; starting cold")
			continue
		}
		for _, tick := range ticks {
			if err := mgr.UpdatePrice(tick); err != nil {
				log.Error().Err(err).Str("symbol", symbol).Msg("Warm-up tick error")
			}
		}
		log.Debug().Str("symbol", symbol).Int("ticks", len(ticks)).Msg("Symbol warmed up")
	}

	// ── Signal Listener ───────────────────────────────────────────────────────

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case sig := <-mgr.Signals:
				log.Info().
					Str("symbol", sig.Symbol).
					Str("direction", sig.Direction).
					Str("price", sig.Price.String()).
					Float64("confidence", sig.Confidence).
					Str("reason", sig.Reason).
					Msg("Signal received")
				trader.OnSignal(sig)
			}
		}
	}()

	// ── Persistence Ticker ────────────────────────────────────────────────────

	go func() {
		ticker := time.NewTicker(2 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := trader.SaveState(stateFile); err != nil {
					log.Error().Err(err).Msg("Periodic state save failed")
				}
				stats := trader.GetStats()
				log.Info().
					Int("total_signals", stats.TotalSignals).
					Float64("win_rate", stats.WinRate).
					Msg("Periodic report")
			}
		}
	}()

	// ── HTTP API ──────────────────────────────────────────────────────────────

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	mux := http.NewServeMux()

	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		stats := trader.GetStats()
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{
			"status":        "OK",
			"watchlist":     watchlist,
			"total_signals": stats.TotalSignals,
			"win_rate":      stats.WinRate,
			"active_trades": stats.ActiveTrades,
		}); err != nil {
			log.Error().Err(err).Msg("/health encode error")
		}
	})

	mux.HandleFunc("/api/state", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(trader.GetState()); err != nil {
			log.Error().Err(err).Msg("/api/state encode error")
		}
	})

	mux.HandleFunc("/api/trades", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(trader.GetTrades()); err != nil {
			log.Error().Err(err).Msg("/api/trades encode error")
		}
	})

	mux.HandleFunc("/api/signals", func(w http.ResponseWriter, r *http.Request) {
		symbol := r.URL.Query().Get("symbol")
		if symbol == "" {
			http.Error(w, `{"error":"symbol required"}`, http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(mgr.GetSignals(symbol)); err != nil {
			log.Error().Err(err).Msg("/api/signals encode error")
		}
	})

	mux.HandleFunc("/api/candles", func(w http.ResponseWriter, r *http.Request) {
		symbol := r.URL.Query().Get("symbol")
		if symbol == "" {
			http.Error(w, `{"error":"symbol required"}`, http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(mgr.GetCandles(symbol)); err != nil {
			log.Error().Err(err).Msg("/api/candles encode error")
		}
	})

	mux.Handle("/", http.FileServer(http.Dir("./web")))

	srv := &http.Server{
		Addr:         ":" + port,
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	go func() {
		log.Info().Str("port", port).Msg("HTTP server started")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal().Err(err).Msg("HTTP server error")
		}
	}()

	// ── Ingestion ─────────────────────────────────────────────────────────────

	go ingester.StartBinanceStream(ctx, watchlist, mgr, testnet)

	<-ctx.Done()
	log.Info().Msg("Shutdown signal received")

	if err := trader.SaveState(stateFile); err != nil {
		log.Error().Err(err).Msg("Final state save failed")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error().Err(err).Msg("HTTP shutdown error")
	}

	log.Info().Msg("Shutdown complete")
}

// barSecondsToInterval converts a bar duration in seconds to the Binance kline
// interval string used by both the REST warm-up and WebSocket stream APIs.
func barSecondsToInterval(seconds int) string {
	switch seconds {
	case 15:
		return "15s"
	case 30:
		return "30s"
	case 60:
		return "1m"
	case 180:
		return "3m"
	case 300:
		return "5m"
	case 900:
		return "15m"
	case 1800:
		return "30m"
	case 3600:
		return "1h"
	default:
		return "1m"
	}
}

func getEnvInt(key string, fallback int) int {
	if val := os.Getenv(key); val != "" {
		if i, err := strconv.Atoi(val); err == nil {
			return i
		}
	}
	return fallback
}

func getEnvDecimal(key, fallback string) decimal.Decimal {
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
