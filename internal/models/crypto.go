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

type PricePoint struct {
	Price     decimal.Decimal `json:"price"`
	Timestamp time.Time       `json:"timestamp"`
}

// Signal is the output of a Strategy.Analyze call. Confidence is in [0, 1].
//
// ATR holds the 14-period Average True Range at signal time, expressed in
// price units. Traders use it to compute ATR-based stop-loss distances
// (typically 1.5x ATR). Zero means the strategy has not accumulated enough
// history to produce a valid ATR and the trader should fall back to its
// configured fixed-percentage SL.
type Signal struct {
	Symbol     string          `json:"symbol"`
	Price      decimal.Decimal `json:"price"`
	Direction  string          `json:"direction"`
	Reason     string          `json:"reason"`
	Confidence float64         `json:"confidence"`
	Timestamp  time.Time       `json:"timestamp"`
	ATR        decimal.Decimal `json:"atr,omitempty"`
}

type Candle struct {
	Symbol string          `json:"symbol"`
	Open   decimal.Decimal `json:"open"`
	High   decimal.Decimal `json:"high"`
	Low    decimal.Decimal `json:"low"`
	Close  decimal.Decimal `json:"close"`
	Volume decimal.Decimal `json:"volume"`
	Time   time.Time       `json:"timestamp"`
}

// ToPricePoint converts the raw WebSocket event into a typed PricePoint.
// Price is string-encoded in the Binance stream to preserve decimal precision.
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
