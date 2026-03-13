package ingester

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/papadacodr/kryptic-gopha/internal/engine"
	"github.com/papadacodr/kryptic-gopha/internal/models"
	"github.com/rs/zerolog/log"
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

// klineCloseIdx is the index of the close price in a Binance kline array.
// klineCloseTimeIdx is the index of the candle close time (Unix ms).
const (
	klineCloseIdx     = 4
	klineCloseTimeIdx = 6
	klineMinLen       = 7
)

func FetchHistoricalKlines(symbol, interval string, limit int) ([]models.MarketTick, error) {
	url := fmt.Sprintf("%s/api/v3/klines?symbol=%s&interval=%s&limit=%d", binanceRestURL, symbol, interval, limit)

	resp, err := http.Get(url) //nolint:noctx // historical fetch; no streaming context needed
	if err != nil {
		return nil, fmt.Errorf("binance klines request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("binance api error: %s", resp.Status)
	}

	var raw [][]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("binance klines decode failed: %w", err)
	}

	ticks := make([]models.MarketTick, 0, len(raw))
	for i, kline := range raw {
		if len(kline) < klineMinLen {
			return nil, fmt.Errorf("kline[%d]: expected at least %d fields, got %d", i, klineMinLen, len(kline))
		}

		closePrice, ok := kline[klineCloseIdx].(string)
		if !ok {
			return nil, fmt.Errorf("kline[%d]: close price is not a string (got %T)", i, kline[klineCloseIdx])
		}

		closeTime, ok := kline[klineCloseTimeIdx].(float64)
		if !ok {
			return nil, fmt.Errorf("kline[%d]: close time is not a number (got %T)", i, kline[klineCloseTimeIdx])
		}

		ticks = append(ticks, models.MarketTick{
			Symbol:    symbol,
			Price:     closePrice,
			Timestamp: int64(closeTime),
		})
	}

	return ticks, nil
}

func StartBinanceStream(ctx context.Context, symbols []string, mgr *engine.EngineManager) {
	normalizedSymbols := normalizeSymbols(symbols)
	url := binanceStreamURL + strings.Join(normalizedSymbols, "/")

	retryDelay := initialRetryDelay

	for {
		select {
		case <-ctx.Done():
			log.Info().Msg("Binance stream shutting down")
			return
		default:
			conn, err := connectToBinance(url)
			if err != nil {
				log.Error().Err(err).Dur("retry_in", retryDelay).Msg("Binance connection failed")
				time.Sleep(retryDelay)
				retryDelay = increaseRetryDelay(retryDelay)
				continue
			}

			log.Info().Int("symbols", len(symbols)).Msg("Connected to Binance WebSocket")
			retryDelay = initialRetryDelay 

			connCtx, cancel := context.WithCancel(ctx)
			
			go monitorConnection(connCtx, conn)
			handleConnectionClosed(conn, mgr)
			
			cancel()
			conn.Close()
			log.Warn().Msg("Binance connection closed, reconnecting...")
		}
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

func monitorConnection(ctx context.Context, conn *websocket.Conn) {
	ticker := time.NewTicker(pingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			conn.SetWriteDeadline(time.Now().Add(writeTimeout))
			if err := conn.WriteMessage(websocket.PingMessage, []byte{}); err != nil {
				log.Warn().Err(err).Msg("Ping failed")
				return
			}
		case <-ctx.Done():
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
		_, message, err := conn.ReadMessage()
		if err != nil {
			log.Error().Err(err).Msg("Read error")
			return
		}

		conn.SetReadDeadline(time.Now().Add(readTimeout))
		processMarketData(message, mgr)
	}
}

func processMarketData(message []byte, mgr *engine.EngineManager) {
	var payload struct {
		Data models.MarketTick `json:"data"`
	}

	if err := json.Unmarshal(message, &payload); err != nil {
		var multiPayload struct {
			Stream string            `json:"stream"`
			Data   models.MarketTick `json:"data"`
		}
		if err2 := json.Unmarshal(message, &multiPayload); err2 == nil {
			payload.Data = multiPayload.Data
		} else {
			log.Error().Err(err).Msg("Market data unmarshal failed")
			return
		}
	}

	if payload.Data.Symbol == "" {
		return
	}

	if err := mgr.UpdatePrice(payload.Data); err != nil {
		log.Error().Err(err).Str("symbol", payload.Data.Symbol).Msg("Price update failed")
	}
}
