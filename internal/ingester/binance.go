package ingester

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/papadacodr/kryptic-gopha/internal/engine"
	"github.com/papadacodr/kryptic-gopha/internal/models"
)

const (
	binanceStreamURL       = "wss://stream.binance.com:9443/stream?streams="
	binanceRestURL         = "https://api.binance.com"
	streamSuffix           = "@trade"
	initialRetryDelay      = time.Second
	maxRetryDelay          = 30 * time.Second
	retryBackoffMultiplier = 1.5
	pingInterval           = 30 * time.Second
	readTimeout            = 90 * time.Second
	writeTimeout           = 10 * time.Second
)

func FetchHistoricalKlines(symbol, interval string, limit int) ([]models.MarketTick, error) {
	url := fmt.Sprintf("%s/api/v3/klines?symbol=%s&interval=%s&limit=%d", binanceRestURL, symbol, interval, limit)
	
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var raw [][]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}

	ticks := make([]models.MarketTick, 0, len(raw))
	for _, kline := range raw {
		// Kline format: [Open time, Open, High, Low, Close, Volume, Close time, ...]
		ticks = append(ticks, models.MarketTick{
			Symbol:    symbol,
			Price:     kline[4].(string), // Close price
			Timestamp: int64(kline[6].(float64)), // Close time
		})
	}

	return ticks, nil
}




func StartBinanceStream(symbols []string, mgr *engine.EngineManager) {
	normalizedSymbols := normalizeSymbols(symbols)
	url := binanceStreamURL + strings.Join(normalizedSymbols, "/")

	retryDelay := initialRetryDelay

	for {
		conn, err := connectToBinance(url)
		if err != nil {
			log.Printf("Failed to connect to Binance: %v. Retrying in %v", err, retryDelay)
			time.Sleep(retryDelay)
			retryDelay = increaseRetryDelay(retryDelay)
			continue
		}

		log.Printf("Successfully connected to Binance WebSocket. Streaming %d symbols", len(symbols))
		retryDelay = initialRetryDelay 

		go monitorConnection(conn)
		handleConnectionClosed(conn, mgr)
		conn.Close()
	}
}


func normalizeSymbols(symbols []string) []string {
	normalized := make([]string, len(symbols))
	for i, symbol := range symbols {
		normalized[i] = strings.ToLower(symbol) + streamSuffix
	}
	return normalized
}

func connectToBinance(url string) (*websocket.Conn, error) {
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		return nil, fmt.Errorf("websocket dial failed: %w", err)
	}
	return conn, nil
}

func increaseRetryDelay(currentDelay time.Duration) time.Duration {
	newDelay := time.Duration(float64(currentDelay) * retryBackoffMultiplier)
	if newDelay > maxRetryDelay {
		return maxRetryDelay
	}
	return newDelay
}

func monitorConnection(conn *websocket.Conn) {
	ticker := time.NewTicker(pingInterval)
	defer ticker.Stop()

	for range ticker.C {
		conn.SetWriteDeadline(time.Now().Add(writeTimeout))
		if err := conn.WriteMessage(websocket.PingMessage, []byte{}); err != nil {
			log.Printf("Failed to send ping: %v", err)
			return
		}
	}
}

func handleConnectionClosed(conn *websocket.Conn, mgr *engine.EngineManager) {
	conn.SetReadDeadline(time.Now().Add(readTimeout))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(readTimeout))
		return nil
	})

	for {
		conn.SetReadDeadline(time.Now().Add(readTimeout))
		_, message, err := conn.ReadMessage()
		if err != nil {
			log.Printf("Connection closed: %v", err)
			return
		}

		processMarketData(message, mgr)
	}
}

func processMarketData(message []byte, mgr *engine.EngineManager) {
	var payload struct {
		Data models.MarketTick `json:"data"`
	}

	if err := json.Unmarshal(message, &payload); err != nil {
		log.Printf("Failed to unmarshal market data: %v", err)
		return
	}

	if err := mgr.UpdatePrice(payload.Data); err != nil {
		log.Printf("Failed to update price for %s: %v", payload.Data.Symbol, err)
	}
}

