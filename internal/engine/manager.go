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
	currentCandle  *models.Candle
	lastSignalType string
	lastSignalTime time.Time
}

type EngineManager struct {
	states   map[string]*symbolState
	Strategy Strategy
	Signals  chan models.Signal
	Trader   *PaperTrader
}

func NewEngineManager(symbols []string, bufferSize int, strategy Strategy, trader *PaperTrader) *EngineManager {
	mgr := &EngineManager{
		states:   make(map[string]*symbolState),
		Strategy: strategy,
		Signals:  make(chan models.Signal, 1000),
		Trader:   trader,
	}
	
	for _, s := range symbols {
		mgr.states[s] = &symbolState{
			buffer: NewPriceBuffer(bufferSize),
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
	tickTime := point.Timestamp.Truncate(time.Minute)

	if curr == nil || tickTime.After(curr.Time) {
		if curr != nil {
			state.buffer.Add(curr.Close)
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

func (m *EngineManager) analyzeInternal(symbol string, state *symbolState) {
	history := state.buffer.GetHistory()
	if signal := m.Strategy.Analyze(symbol, history); signal != nil {
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