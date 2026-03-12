package engine

import (
	"fmt"
	"sync"
	"time"

	"github.com/papadacodr/kryptic-gopha/internal/models"
)

type EngineManager struct {
	sync.Mutex
	Buffers         map[string]*PriceBuffer
	Strategy        Strategy
	Signals         chan models.Signal
	lastSignalType  map[string]string         // symbol -> "BUY"/"SELL"
	lastSignalTime  map[string]time.Time      // symbol -> timestamp
	currentCandles  map[string]*models.Candle // current open candle
	Trader          *PaperTrader              // Benchmarking tool
}

func NewEngineManager(symbols []string, bufferSize int, strategy Strategy, trader *PaperTrader) *EngineManager {
	mgr := &EngineManager{
		Buffers:         make(map[string]*PriceBuffer),
		Strategy:        strategy,
		Signals:         make(chan models.Signal, 100),
		lastSignalType:  make(map[string]string),
		lastSignalTime:  make(map[string]time.Time),
		currentCandles:  make(map[string]*models.Candle),
		Trader:          trader,
	}
	
	for _, s := range symbols {
		mgr.Buffers[s] = NewPriceBuffer(bufferSize)
	}
	return mgr
}

func (m *EngineManager) UpdatePrice(tick models.MarketTick) error {
	point, err := tick.ToPricePoint()
	if err != nil {
		return fmt.Errorf("conversion error for %s: %w", tick.Symbol, err)
	}

	m.Lock()
	defer m.Unlock()

	// Update PaperTrader with every tick for precise benchmarking
	if m.Trader != nil {
		m.Trader.UpdateMetrics(tick.Symbol, point.Price)
	}

	buffer, exists := m.Buffers[tick.Symbol]
	if !exists {
		return fmt.Errorf("received data for untracked symbol: %s", tick.Symbol)
	}

	// OHLCV Smoothing (1-minute candles)
	curr := m.currentCandles[tick.Symbol]
	tickTime := point.Timestamp.Truncate(time.Minute)

	if curr == nil || tickTime.After(curr.Time) {
		// New minute started: Close old candle and process it
		if curr != nil {
			buffer.Add(curr.Close)
			m.analyzeInternal(tick.Symbol, buffer)
		}

		// Initialize new candle
		m.currentCandles[tick.Symbol] = &models.Candle{
			Symbol: tick.Symbol,
			Open:   point.Price,
			High:   point.Price,
			Low:    point.Price,
			Close:  point.Price,
			Time:   tickTime,
		}
	} else {
		// Update current candle
		if point.Price > curr.High {
			curr.High = point.Price
		}
		if point.Price < curr.Low {
			curr.Low = point.Price
		}
		curr.Close = point.Price
	}

	return nil
}

func (m *EngineManager) analyzeInternal(symbol string, buffer *PriceBuffer) {
	history := buffer.GetHistory()
	if signal := m.Strategy.Analyze(symbol, history); signal != nil {
		lastType := m.lastSignalType[symbol]
		lastTime := m.lastSignalTime[symbol]

		if signal.Direction != lastType || time.Since(lastTime) > 5*time.Minute {
			m.lastSignalType[symbol] = signal.Direction
			m.lastSignalTime[symbol] = time.Now()
			m.Signals <- *signal
		}
	}
}