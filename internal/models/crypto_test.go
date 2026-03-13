package models

import (
	"testing"
	"time"
)

func TestMarketTick_ToPricePoint_Valid(t *testing.T) {
	tick := MarketTick{
		Symbol:    "BTCUSDT",
		Price:     "42000.50",
		Timestamp: 1700000000000, // Unix ms
	}

	pp, err := tick.ToPricePoint()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// StringFixed preserves explicit precision; Decimal.String() drops trailing
	// zeros (42000.50 → "42000.5").
	if pp.Price.StringFixed(2) != "42000.50" {
		t.Errorf("price: got %s, want 42000.50", pp.Price.StringFixed(2))
	}

	wantTime := time.UnixMilli(1700000000000)
	if !pp.Timestamp.Equal(wantTime) {
		t.Errorf("timestamp: got %v, want %v", pp.Timestamp, wantTime)
	}
}

func TestMarketTick_ToPricePoint_InvalidPrice(t *testing.T) {
	tick := MarketTick{
		Symbol:    "BTCUSDT",
		Price:     "not-a-number",
		Timestamp: 1700000000000,
	}

	_, err := tick.ToPricePoint()
	if err == nil {
		t.Error("expected error for invalid price string, got nil")
	}
}

func TestMarketTick_ToPricePoint_ZeroTimestamp(t *testing.T) {
	tick := MarketTick{
		Symbol:    "ETHUSDT",
		Price:     "2000.00",
		Timestamp: 0,
	}

	pp, err := tick.ToPricePoint()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !pp.Timestamp.Equal(time.UnixMilli(0)) {
		t.Errorf("expected Unix epoch, got %v", pp.Timestamp)
	}
}

func TestMarketTick_ToPricePoint_HighPrecision(t *testing.T) {
	tick := MarketTick{
		Symbol:    "BTCUSDT",
		Price:     "0.00000001", // Satoshi-level precision
		Timestamp: 1700000000000,
	}

	pp, err := tick.ToPricePoint()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if pp.Price.String() != "0.00000001" {
		t.Errorf("precision lost: got %s", pp.Price.String())
	}
}
