package exchange

import (
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/shopspring/decimal"
)

type balanceEntry struct {
	Asset              string `json:"asset"`
	AvailableBalance   string `json:"availableBalance"`
	CrossWalletBalance string `json:"crossWalletBalance"`
}

func (c *Client) GetUSDTBalance() (decimal.Decimal, error) {
	body, err := c.get("/fapi/v2/balance", url.Values{})
	if err != nil {
		return decimal.Zero, fmt.Errorf("get balance: %w", err)
	}

	var entries []balanceEntry
	if err := json.Unmarshal(body, &entries); err != nil {
		return decimal.Zero, fmt.Errorf("parse balance: %w", err)
	}

	for _, e := range entries {
		if e.Asset == "USDT" {
			bal, err := decimal.NewFromString(e.AvailableBalance)
			if err != nil {
				return decimal.Zero, fmt.Errorf("parse USDT balance: %w", err)
			}
			return bal, nil
		}
	}
	return decimal.Zero, fmt.Errorf("USDT balance not found in response")
}

type positionRiskEntry struct {
	Symbol           string `json:"symbol"`
	PositionAmt      string `json:"positionAmt"`
	EntryPrice       string `json:"entryPrice"`
	UnrealizedProfit string `json:"unRealizedProfit"`
	PositionSide     string `json:"positionSide"`
}

type OpenPosition struct {
	Symbol           string
	Quantity         decimal.Decimal
	EntryPrice       decimal.Decimal
	UnrealizedProfit decimal.Decimal
	Side             string // "LONG" or "SHORT"
}

func (c *Client) GetOpenPositions() ([]OpenPosition, error) {
	body, err := c.get("/fapi/v2/positionRisk", url.Values{})
	if err != nil {
		return nil, fmt.Errorf("get positions: %w", err)
	}

	var entries []positionRiskEntry
	if err := json.Unmarshal(body, &entries); err != nil {
		return nil, fmt.Errorf("parse positions: %w", err)
	}

	var positions []OpenPosition
	for _, e := range entries {
		qty, _ := decimal.NewFromString(e.PositionAmt)
		if qty.IsZero() {
			continue
		}

		entry, _ := decimal.NewFromString(e.EntryPrice)
		pnl, _ := decimal.NewFromString(e.UnrealizedProfit)

		side := "LONG"
		if qty.IsNegative() {
			side = "SHORT"
			qty = qty.Abs()
		}

		positions = append(positions, OpenPosition{
			Symbol:           e.Symbol,
			Quantity:         qty,
			EntryPrice:       entry,
			UnrealizedProfit: pnl,
			Side:             side,
		})
	}
	return positions, nil
}
