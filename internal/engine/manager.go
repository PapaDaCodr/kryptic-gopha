package engine

import (
	"fmt"
	"sync"
	"time"

	"github.com/papadacodr/kryptic-gopha/internal/models"
	"github.com/rs/zerolog/log"
)

type symbolState struct {
	sync.Mutex
	buffer         *PriceBuffer
	history        []models.Candle // Stores completed candles for the dashboard
	currentCandle  *models.Candle
	lastSignalType string
	lastSignalTime time.Time
	signalHistory  []models.Signal // Stores recent predictions/signals
}

type EngineManager struct {
	states      map[string]*symbolState
	Strategy    Strategy
	Signals     chan models.Signal
	Trader      Trader
	BarInterval time.Duration // Candle aggregation interval (default: 1 minute)
}

func NewEngineManager(symbols []string, bufferSize int, strategy Strategy, trader Trader) *EngineManager {
	mgr := &EngineManager{
		states:      make(map[string]*symbolState),
		Strategy:    strategy,
		Signals:     make(chan models.Signal, 1000),
		Trader:      trader,
		BarInterval: time.Minute,
	}
	
	for _, s := range symbols {
		mgr.states[s] = &symbolState{
			buffer:  NewPriceBuffer(bufferSize),
			history: make([]models.Candle, 0, bufferSize),
		}
	}
	return mgr
}

func (m *EngineManager) UpdatePrice(tick models.MarketTick) error {
	point, err := tick.ToPricePoint()
	if err != nil {
		return fmt.Errorf("conversion error for %s: %w", tick.Symbol, err)
	}

	state, exists := m.states[tick.Symbol]
	if !exists {
		return fmt.Errorf("received data for untracked symbol: %s", tick.Symbol)
	}

	if m.Trader != nil {
		m.Trader.UpdateMetrics(tick.Symbol, point.Price, point.Timestamp)
	}

	state.Lock()
	defer state.Unlock()

	curr := state.currentCandle
	tickTime := point.Timestamp.Truncate(m.BarInterval)

	if curr == nil || tickTime.After(curr.Time) {
		if curr != nil {
			state.buffer.Add(curr.Close)
			// Store the completed candle in history
			state.history = append(state.history, *curr)
			if len(state.history) > state.buffer.size {
				state.history = state.history[1:]
			}
			m.analyzeInternal(tick.Symbol, state)
		}

		state.currentCandle = &models.Candle{
			Symbol: tick.Symbol,
			Open:   point.Price,
			High:   point.Price,
			Low:    point.Price,
			Close:  point.Price,
			Time:   tickTime,
		}
		log.Debug().Str("symbol", tick.Symbol).Time("time", tickTime).Msg("New candle started")
	} else {
		if point.Price.GreaterThan(curr.High) {
			curr.High = point.Price
		}
		if point.Price.LessThan(curr.Low) {
			curr.Low = point.Price
		}
		curr.Close = point.Price
	}

	return nil
}

// analyzeInternal runs the strategy and records signals
func (m *EngineManager) analyzeInternal(symbol string, state *symbolState) {
	history := state.buffer.GetHistory()
	if signal := m.Strategy.Analyze(symbol, history); signal != nil {
		// Store in history for dashboard
		state.signalHistory = append(state.signalHistory, *signal)
		if len(state.signalHistory) > 100 {
			state.signalHistory = state.signalHistory[1:]
		}

		if signal.Direction != state.lastSignalType || time.Since(state.lastSignalTime) > 5*time.Minute {
			state.lastSignalType = signal.Direction
			state.lastSignalTime = time.Now()
			
			select {
			case m.Signals <- *signal:
			default:
				log.Warn().Str("symbol", symbol).Msg("Signal channel full. Dropping signal")
			}
		}
	}
}

// GetCandles returns the recent OHLCV history for a symbol
func (m *EngineManager) GetCandles(symbol string) []models.Candle {
	state, exists := m.states[symbol]
	if !exists {
		return nil
	}
	state.Lock()
	defer state.Unlock()

	// Return a copy of history + the current forming candle
	total := len(state.history)
	if state.currentCandle != nil {
		total++
	}
	res := make([]models.Candle, total)
	copy(res, state.history)
	if state.currentCandle != nil {
		res[len(state.history)] = *state.currentCandle
	}
	return res
}
// GetSignals returns the recent signal history for a symbol
func (m *EngineManager) GetSignals(symbol string) []models.Signal {
	state, exists := m.states[symbol]
	if !exists {
		return nil
	}
	state.Lock()
	defer state.Unlock()

	res := make([]models.Signal, len(state.signalHistory))
	copy(res, state.signalHistory)
	return res
}
