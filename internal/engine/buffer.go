package engine

import (
	"sync"
	"github.com/shopspring/decimal"
)

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

func (b *PriceBuffer) Add(price decimal.Decimal) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.prices[b.cursor] = price
	b.cursor = (b.cursor + 1) % b.size
	if b.cursor == 0 {
		b.isFull = true
	}
}

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