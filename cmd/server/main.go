package main

import (
	"github.com/papadacodr/kryptic-gopha/internal/engine"
	"github.com/papadacodr/kryptic-gopha/internal/ingester"
)

func main() {
	watchlist := []string{"BTCUSDT", "ETHUSDT", "BNBUSDT", "SOLUSDT", "DOGEUSDT", "FLOWUSDT"}
	
	// Initialize engine with buffer size 100 per symbol
	mgr := engine.NewEngineManager(watchlist, 100)
	
	ingester.StartBinanceStream(watchlist, mgr)
}
