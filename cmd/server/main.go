package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/papadacodr/kryptic-gopha/internal/engine"
	"github.com/papadacodr/kryptic-gopha/internal/ingester"
)

func main() {
	watchlist := []string{"BTCUSDT", "ETHUSDT", "BNBUSDT", "SOLUSDT", "DOGEUSDT", "FLOWUSDT"}
	
	strategy := &engine.MultiFactorStrategy{
		ShortPeriod: 12,
		LongPeriod:  26,
		RSIPeriod:   14,
	}

	trader := engine.NewPaperTrader()
	mgr := engine.NewEngineManager(watchlist, 500, strategy, trader)
	
	// Signal Listener
	go func() {
		for signal := range mgr.Signals {
			log.Printf("\n--- [SIGNAL] %s ---\nAction: %s\nPrice: %f\nReason: %s\nConfidence: %.2f\n-------------------\n",
				signal.Symbol, signal.Direction, signal.Price, signal.Reason, signal.Confidence)
			
			// Notify trader to track this virtual trade
			trader.OnSignal(signal)
		}
	}()

	// Accuracy Reporter Ticker
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		for range ticker.C {
			log.Printf("\n==== ACCURACY REPORT ====\nTotal Signals: %d\nWin Rate: %.2f%%\n=========================\n",
				trader.TotalWins+trader.TotalLosses, trader.GetWinRate())
		}
	}()

	// HTTP Server for Deployment (Health Check)
	go func() {
		port := os.Getenv("PORT")
		if port == "" {
			port = "8080"
		}

		http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintf(w, "Kryptic Gopha Bot is Running\nTotal Signals: %d\nWin Rate: %.2f%%", 
				trader.TotalWins+trader.TotalLosses, trader.GetWinRate())
		})

		log.Printf("Starting health check server on port %s", port)
		if err := http.ListenAndServe(":"+port, nil); err != nil {
			log.Printf("HTTP server failed: %v", err)
		}
	}()

	ingester.StartBinanceStream(watchlist, mgr)
}
