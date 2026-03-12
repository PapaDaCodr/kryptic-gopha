package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/papadacodr/kryptic-gopha/internal/engine"
	"github.com/papadacodr/kryptic-gopha/internal/ingester"
)

func main() {
	// Configuration from Environment Variables
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

	trader := engine.NewPaperTrader()
	trader.TP = getEnvFloat("TP", 0.005)
	trader.SL = getEnvFloat("SL", 0.003)

	mgr := engine.NewEngineManager(watchlist, 500, strategy, trader)
	
	// Signal Listener
	go func() {
		for signal := range mgr.Signals {
			log.Printf("\n--- [SIGNAL] %s ---\nAction: %s\nPrice: %f\nReason: %s\nConfidence: %.2f\n-------------------\n",
				signal.Symbol, signal.Direction, signal.Price, signal.Reason, signal.Confidence)
			
			trader.OnSignal(signal)
		}
	}()

	// Accuracy Reporter Ticker
	go func() {
		ticker := time.NewTicker(1 * time.Minute)
		for range ticker.C {
			log.Printf("\n==== ACCURACY REPORT ====\nTotal Signals: %d\nWin Rate: %.2f%%\nTP/SL: %.2f%%/%.2f%%\n=========================\n",
				trader.TotalWins+trader.TotalLosses, trader.GetWinRate(), trader.TP*100, trader.SL*100)
		}
	}()

	// Health Check Server
	go func() {
		port := os.Getenv("PORT")
		if port == "" {
			port = "8080"
		}

		http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintf(w, "Kryptic Gopha Bot is Running\nWatchlist: %v\nTotal Signals: %d\nWin Rate: %.2f%%", 
				watchlist, trader.TotalWins+trader.TotalLosses, trader.GetWinRate())
		})

		log.Printf("Starting health check server on port %s", port)
		if err := http.ListenAndServe(":"+port, nil); err != nil {
			log.Printf("HTTP server failed: %v", err)
		}
	}()

	ingester.StartBinanceStream(watchlist, mgr)
}

func getEnvInt(key string, fallback int) int {
	if val := os.Getenv(key); val != "" {
		if i, err := strconv.Atoi(val); err == nil {
			return i
		}
	}
	return fallback
}

func getEnvFloat(key string, fallback float64) float64 {
	if val := os.Getenv(key); val != "" {
		if f, err := strconv.ParseFloat(val, 64); err == nil {
			return f
		}
	}
	return fallback
}
