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
	Price     decimal.Decimal
	Timestamp time.Time
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
	Symbol string
	Open   decimal.Decimal
	High   decimal.Decimal
	Low    decimal.Decimal
	Close  decimal.Decimal
	Volume decimal.Decimal
	Time   time.Time
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

