package main

import (
	"github.com/papadacodr/kryptic-gopha/internal/ingester"
)

func main() {
	watchlist := []string{"BTCUSDT", "ETHUSDT", "BNBUSDT", "SOLUSDT", "DOGEUSDT", "FLOWUSDT"}
	ingester.StartBinanceStream(watchlist)
}
