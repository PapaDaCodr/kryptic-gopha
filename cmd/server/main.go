package server

import (
	"github.com/papadacodr/kryptic-gopha/internal/ingester"
)

func main(){
	watchlist := []string{"BTCUSDT", "ETHUSDT", "BNBUSDT, SOLUSDT, DOGEUSDT, FLOWUSDT, MATICUSDT, ADAUSDT, XRPUSDT, AVAXUSDT, DOTUSDT"}
	ingester.startBinanceStream(watchlist)
}