package models

import (
	"time"

	"github.com/shopspring/decimal"
)

// MarketTick is the raw trade event from the Binance WebSocket stream.
// Fields match the Binance stream JSON payload exactly.
type MarketTick struct {
	Symbol    string `json:"s"`
	Price     string `json:"p"`
	Timestamp int64  `json:"T"`
}

// PricePoint is a parsed, typed representation of a single market tick.
type PricePoint struct {
	Price     decimal.Decimal `json:"price"`
	Timestamp time.Time       `json:"timestamp"`
}

// Signal is the output of a Strategy.Analyze call. Confidence is in [0, 1]
// where higher values indicate stronger alignment across indicator conditions.
// TP and SL prices are not included here: both traders compute their own
// bracket levels from trader-level percentage settings, ensuring consistent
// risk management regardless of which strategy generated the signal.
type Signal struct {
	Symbol     string          `json:"symbol"`
	Price      decimal.Decimal `json:"price"`
	Direction  string          `json:"direction"`
	Reason     string          `json:"reason"`
	Confidence float64         `json:"confidence"`
	Timestamp  time.Time       `json:"timestamp"`
}

// Candle is a completed OHLCV bar.
type Candle struct {
	Symbol string          `json:"symbol"`
	Open   decimal.Decimal `json:"open"`
	High   decimal.Decimal `json:"high"`
	Low    decimal.Decimal `json:"low"`
	Close  decimal.Decimal `json:"close"`
	Volume decimal.Decimal `json:"volume"`
	Time   time.Time       `json:"timestamp"`
}

// ToPricePoint parses the string-encoded price and millisecond timestamp
// from the raw WebSocket event into a typed PricePoint.
func (mt MarketTick) ToPricePoint() (PricePoint, error) {
	p, err := decimal.NewFromString(mt.Price)
	if err != nil {
		return PricePoint{}, err
	}
	return PricePoint{
		Price:     p,
		Timestamp: time.UnixMilli(mt.Timestamp),
	}, nil
}
