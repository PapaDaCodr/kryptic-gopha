package engine

import (
	"fmt"
	"sync"
	"time"

	"github.com/papadacodr/kryptic-gopha/internal/models"
	"github.com/rs/zerolog/log"
)

// symbolState holds per-symbol mutable state. The embedded mutex ensures that
// concurrent WebSocket ticks for the same symbol are serialised, while ticks
// for different symbols run in parallel.
type symbolState struct {
	sync.Mutex
	history        []models.Candle
	currentCandle  *models.Candle
	lastSignalType string
	lastSignalTime time.Time
	signalHistory  []models.Signal
}

// EngineManager routes price ticks to per-symbol state, aggregates them into
// OHLCV candles, and dispatches signals to the Signals channel on bar close.
// It is safe for concurrent use.
type EngineManager struct {
	states      map[string]*symbolState
	Strategy    Strategy
	Signals     chan models.Signal
	Trader      Trader
	BarInterval time.Duration
	bufferSize  int
}

func NewEngineManager(symbols []string, bufferSize int, strategy Strategy, trader Trader) *EngineManager {
	mgr := &EngineManager{
		states:      make(map[string]*symbolState),
		Strategy:    strategy,
		Signals:     make(chan models.Signal, 1000),
		Trader:      trader,
		BarInterval: time.Minute,
		bufferSize:  bufferSize,
	}
	for _, s := range symbols {
		mgr.states[s] = &symbolState{
			history: make([]models.Candle, 0, bufferSize),
		}
	}
	return mgr
}

// UpdatePrice processes a single market tick: updates open trades via the
// trader, aggregates into the current candle, and on bar close runs the
// strategy and dispatches any resulting signal.
func (m *EngineManager) UpdatePrice(tick models.MarketTick) error {
	point, err := tick.ToPricePoint()
	if err != nil {
		return fmt.Errorf("conversion error for %s: %w", tick.Symbol, err)
	}

	state, exists := m.states[tick.Symbol]
	if !exists {
		return fmt.Errorf("received data for untracked symbol: %s", tick.Symbol)
	}

	// UpdateMetrics is called outside the symbol lock because it acquires its
	// own internal lock. Calling it inside would create a lock ordering hazard
	// if the trader ever calls back into the engine.
	if m.Trader != nil {
		m.Trader.UpdateMetrics(tick.Symbol, point.Price, point.Timestamp)
	}

	state.Lock()
	defer state.Unlock()

	tickTime := point.Timestamp.Truncate(m.BarInterval)
	curr := state.currentCandle

	if curr == nil || tickTime.After(curr.Time) {
		if curr != nil {
			state.history = append(state.history, *curr)
			if len(state.history) > m.bufferSize {
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
		log.Debug().Str("symbol", tick.Symbol).Time("time", tickTime).Msg("New candle opened")
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

// analyzeInternal runs the strategy on the completed bar's price history and
// forwards any new signal to the Signals channel. Signal deduplication prevents
// flooding during sustained trends: the same direction is suppressed for 5
// minutes unless the direction changes.
//
// Must be called with state.Lock held.
func (m *EngineManager) analyzeInternal(symbol string, state *symbolState) {
	signal := m.Strategy.Analyze(symbol, state.history)
	if signal == nil {
		return
	}

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
			log.Warn().Str("symbol", symbol).Msg("Signal channel full; signal dropped")
		}
	}
}

// GetCandles returns completed candle history plus the current forming candle.
// The current candle is appended as the last element so callers always see
// the live bar without a separate API call.
func (m *EngineManager) GetCandles(symbol string) []models.Candle {
	state, exists := m.states[symbol]
	if !exists {
		return nil
	}
	state.Lock()
	defer state.Unlock()

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

// GetSignals returns the most recent signal history for a symbol (up to 100 entries).
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
