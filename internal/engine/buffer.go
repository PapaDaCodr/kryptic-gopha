package engine

import (
	"sync"

	"github.com/shopspring/decimal"
)

// PriceBuffer is a fixed-capacity circular buffer of decimal prices.
// Add and GetHistory are both O(1) in time and O(N) in space where N is the
// capacity. The buffer is used to feed the strategy without allocating on
// every bar close.
type PriceBuffer struct {
	mu     sync.Mutex
	prices []decimal.Decimal
	size   int
	cursor int
	isFull bool
}

func NewPriceBuffer(size int) *PriceBuffer {
	return &PriceBuffer{
		prices: make([]decimal.Decimal, size),
		size:   size,
	}
}

// Add inserts price at the current cursor position and advances the cursor,
// wrapping around when the end of the backing slice is reached.
func (b *PriceBuffer) Add(price decimal.Decimal) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.prices[b.cursor] = price
	b.cursor = (b.cursor + 1) % b.size
	if b.cursor == 0 {
		b.isFull = true
	}
}

// GetHistory returns prices in chronological order (oldest first). Before the
// buffer is full it returns only the written portion; after that it reassembles
// the two segments around the cursor in a single allocation.
func (b *PriceBuffer) GetHistory() []decimal.Decimal {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.isFull {
		return b.prices[:b.cursor]
	}
	out := make([]decimal.Decimal, b.size)
	copy(out, b.prices[b.cursor:])
	copy(out[b.size-b.cursor:], b.prices[:b.cursor])
	return out
}
