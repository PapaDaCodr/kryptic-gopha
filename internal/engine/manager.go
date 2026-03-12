package engine

import (
	"fmt"
	"sync"

	"github.com/papadacodr/kryptic-gopha/internal/models"
)

type EngineManager struct {
	sync.Mutex
	Buffers map[string]*PriceBuffer
}

func NewEngineManager(symbols []string, bufferSize int) *EngineManager {
	mgr := &EngineManager{
		Buffers: make(map[string]*PriceBuffer),
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
	buffer, exists := m.Buffers[tick.Symbol]
	m.Unlock()

	if !exists {
		return fmt.Errorf("received data for untracked symbol: %s", tick.Symbol)
	}

	buffer.Add(point.Price)
	return nil
}