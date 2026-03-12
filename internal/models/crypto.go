package models

import (
	"strconv"
	"time"
)

type MarketTick struct {
	Symbol    string `json:"s"`
	Price     string `json:"p"`
	Timestamp int64  `json:"T"`
}

type PricePoint struct {
	Price     float64
	Timestamp time.Time
}

type Signal struct {
	Symbol     string    `json:"symbol"`
	Price      float64   `json:"price"`
	Direction  string    `json:"direction"`
	Reason     string    `json:"reason"`
	Confidence float64   `json:"confidence"`
	Timestamp  time.Time `json:"timestamp"`
}

type Candle struct {
	Symbol string
	Open   float64
	High   float64
	Low    float64
	Close  float64
	Volume float64
	Time   time.Time
}

func (mt MarketTick) ToPricePoint() (PricePoint, error) {
	p, err := strconv.ParseFloat(mt.Price, 64)
	if err != nil {
		return PricePoint{}, err
	}
	
	ts := time.UnixMilli(mt.Timestamp)
	
	return PricePoint{
		Price:     p,
		Timestamp: ts,
	}, nil
}
