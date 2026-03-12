package ingester
import (
	"encoding/json"
	"log"
	"strings"

	"github.com/gorilla/websocket"
)

type marketData struct {
	Symbol string `json:"s"`
	Price  string `json:"p"`
	Time   int64  `json:"T"`
}

func startBinanceStream(symbols []string) {

	for i, s:= range symbols {
		symbols[i] = strings.ToLower(s) + "@trade"
	}

	url := "wss://stream.binance.com:9443/stream?streams=" + strings.Join(symbols, "/")

	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		log.Fatal("Error connecting to Binance WebSocket:", err)
	}
	defer conn.Close()
	log.Println("Connected to Binance WebSocket for symbols:", symbols)	
	
	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			log.Println("Error reading message:", err)
			return
		}

		var payload struct {
			Data marketData `json:"data"`
		}
		if err := json.Unmarshal(message, &payload); err == nil {
			log.Printf("[%s] Current Price: $%s", payload.Data.Symbol, payload.Data.Price)
		}
	}
}