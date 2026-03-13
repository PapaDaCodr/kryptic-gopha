package exchange

import (
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/shopspring/decimal"
)

// OrderSide is BUY or SELL.
type OrderSide string

const (
	SideBuy  OrderSide = "BUY"
	SideSell OrderSide = "SELL"
)

// OrderResult is returned by PlaceMarketOrder.
type OrderResult struct {
	OrderID       int64           `json:"orderId"`
	Symbol        string          `json:"symbol"`
	Side          string          `json:"side"`
	ExecutedQty   decimal.Decimal `json:"executedQty"`
	AvgPrice      decimal.Decimal `json:"avgPrice"`
	Status        string          `json:"status"`
}

type rawOrderResult struct {
	OrderID     int64  `json:"orderId"`
	Symbol      string `json:"symbol"`
	Side        string `json:"side"`
	ExecutedQty string `json:"executedQty"`
	AvgPrice    string `json:"avgPrice"`
	Status      string `json:"status"`
}

// PlaceMarketOrder submits a MARKET order for the given symbol and quantity.
// side must be SideBuy or SideSell.
// qty must already be rounded to the symbol's LOT_SIZE step.
func (c *Client) PlaceMarketOrder(symbol string, side OrderSide, qty decimal.Decimal) (OrderResult, error) {
	params := url.Values{}
	params.Set("symbol", symbol)
	params.Set("side", string(side))
	params.Set("type", "MARKET")
	params.Set("quantity", qty.String())

	body, err := c.post("/fapi/v1/order", params)
	if err != nil {
		return OrderResult{}, fmt.Errorf("place market order %s %s: %w", side, symbol, err)
	}

	var raw rawOrderResult
	if err := json.Unmarshal(body, &raw); err != nil {
		return OrderResult{}, fmt.Errorf("parse order result: %w", err)
	}

	execQty, _ := decimal.NewFromString(raw.ExecutedQty)
	avgPrice, _ := decimal.NewFromString(raw.AvgPrice)

	return OrderResult{
		OrderID:     raw.OrderID,
		Symbol:      raw.Symbol,
		Side:        raw.Side,
		ExecutedQty: execQty,
		AvgPrice:    avgPrice,
		Status:      raw.Status,
	}, nil
}

// PlaceStopMarketOrder submits a STOP_MARKET order (used for SL placement).
// stopPrice is the trigger price; the order closes the position at market when hit.
func (c *Client) PlaceStopMarketOrder(symbol string, side OrderSide, qty, stopPrice decimal.Decimal) (OrderResult, error) {
	params := url.Values{}
	params.Set("symbol", symbol)
	params.Set("side", string(side))
	params.Set("type", "STOP_MARKET")
	params.Set("quantity", qty.String())
	params.Set("stopPrice", stopPrice.StringFixed(8))
	params.Set("closePosition", "false")

	body, err := c.post("/fapi/v1/order", params)
	if err != nil {
		return OrderResult{}, fmt.Errorf("place stop market order %s %s: %w", side, symbol, err)
	}

	var raw rawOrderResult
	if err := json.Unmarshal(body, &raw); err != nil {
		return OrderResult{}, fmt.Errorf("parse stop order result: %w", err)
	}

	execQty, _ := decimal.NewFromString(raw.ExecutedQty)
	avgPrice, _ := decimal.NewFromString(raw.AvgPrice)

	return OrderResult{
		OrderID:     raw.OrderID,
		Symbol:      raw.Symbol,
		Side:        raw.Side,
		ExecutedQty: execQty,
		AvgPrice:    avgPrice,
		Status:      raw.Status,
	}, nil
}

// PlaceTakeProfitMarketOrder submits a TAKE_PROFIT_MARKET order.
func (c *Client) PlaceTakeProfitMarketOrder(symbol string, side OrderSide, qty, stopPrice decimal.Decimal) (OrderResult, error) {
	params := url.Values{}
	params.Set("symbol", symbol)
	params.Set("side", string(side))
	params.Set("type", "TAKE_PROFIT_MARKET")
	params.Set("quantity", qty.String())
	params.Set("stopPrice", stopPrice.StringFixed(8))
	params.Set("closePosition", "false")

	body, err := c.post("/fapi/v1/order", params)
	if err != nil {
		return OrderResult{}, fmt.Errorf("place take-profit order %s %s: %w", side, symbol, err)
	}

	var raw rawOrderResult
	if err := json.Unmarshal(body, &raw); err != nil {
		return OrderResult{}, fmt.Errorf("parse take-profit result: %w", err)
	}

	execQty, _ := decimal.NewFromString(raw.ExecutedQty)
	avgPrice, _ := decimal.NewFromString(raw.AvgPrice)

	return OrderResult{
		OrderID:     raw.OrderID,
		Symbol:      raw.Symbol,
		Side:        raw.Side,
		ExecutedQty: execQty,
		AvgPrice:    avgPrice,
		Status:      raw.Status,
	}, nil
}

// CancelAllOpenOrders cancels all open orders for a symbol (called on position exit).
func (c *Client) CancelAllOpenOrders(symbol string) error {
	params := url.Values{}
	params.Set("symbol", symbol)
	_, err := c.post("/fapi/v1/allOpenOrders", params)
	if err != nil {
		return fmt.Errorf("cancel all orders %s: %w", symbol, err)
	}
	return nil
}
