package models

import (
	"time"
	"github.com/shopspring/decimal"
)

type MarketTick struct {
	Symbol    string `json:"s"`
	Price     string `json:"p"`
	Timestamp int64  `json:"T"`
}

type PricePoint struct {
	Price     decimal.Decimal `json:"price"`
	Timestamp time.Time       `json:"timestamp"`
}

type Signal struct {
	Symbol     string          `json:"symbol"`
	Price      decimal.Decimal `json:"price"`
	Direction  string          `json:"direction"`
	Reason     string          `json:"reason"`
	Confidence float64         `json:"confidence"`
	Timestamp  time.Time       `json:"timestamp"`
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

func (mt MarketTick) ToPricePoint() (PricePoint, error) {
	p, err := decimal.NewFromString(mt.Price)
	if err != nil {
		return PricePoint{}, err
	}
	
	ts := time.UnixMilli(mt.Timestamp)
	
	return PricePoint{
		Price:     p,
		Timestamp: ts,
	}, nil
}

