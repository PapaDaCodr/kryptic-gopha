package exchange

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"

	"github.com/shopspring/decimal"
)

// OrderSide is BUY or SELL.
type OrderSide string

const (
	SideBuy  OrderSide = "BUY"
	SideSell OrderSide = "SELL"
)

// OrderResult is returned by order placement calls.
type OrderResult struct {
	OrderID     int64           `json:"orderId"`
	Symbol      string          `json:"symbol"`
	Side        string          `json:"side"`
	ExecutedQty decimal.Decimal `json:"executedQty"`
	AvgPrice    decimal.Decimal `json:"avgPrice"`
	Status      string          `json:"status"`
}

// rawOrderResult is the wire format for order responses.
// Binance encodes numeric fields as quoted strings.
type rawOrderResult struct {
	OrderID     int64  `json:"orderId"`
	Symbol      string `json:"symbol"`
	Side        string `json:"side"`
	ExecutedQty string `json:"executedQty"`
	AvgPrice    string `json:"avgPrice"`
	Status      string `json:"status"`
}

// PlaceMarketOrder submits a MARKET order. qty must already be rounded to the
// symbol's LOT_SIZE step via RoundToStepSize.
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
	return parseOrderResult(body)
}

// PlaceStopMarketOrder submits a STOP_MARKET order used for stop-loss placement.
// stopPrice is the trigger; the position is closed at market when hit.
func (c *Client) PlaceStopMarketOrder(symbol string, side OrderSide, qty, stopPrice decimal.Decimal) (OrderResult, error) {
	params := url.Values{}
	params.Set("symbol", symbol)
	params.Set("side", string(side))
	params.Set("type", "STOP_MARKET")
	params.Set("quantity", qty.String())
	params.Set("stopPrice", stopPrice.StringFixed(8))
	params.Set("reduceOnly", "true")

	body, err := c.post("/fapi/v1/order", params)
	if err != nil {
		return OrderResult{}, fmt.Errorf("place stop market order %s %s: %w", side, symbol, err)
	}
	return parseOrderResult(body)
}

// PlaceTakeProfitMarketOrder submits a TAKE_PROFIT_MARKET order.
func (c *Client) PlaceTakeProfitMarketOrder(symbol string, side OrderSide, qty, stopPrice decimal.Decimal) (OrderResult, error) {
	params := url.Values{}
	params.Set("symbol", symbol)
	params.Set("side", string(side))
	params.Set("type", "TAKE_PROFIT_MARKET")
	params.Set("quantity", qty.String())
	params.Set("stopPrice", stopPrice.StringFixed(8))
	params.Set("reduceOnly", "true")

	body, err := c.post("/fapi/v1/order", params)
	if err != nil {
		return OrderResult{}, fmt.Errorf("place take-profit order %s %s: %w", side, symbol, err)
	}
	return parseOrderResult(body)
}

// CancelOrder cancels a single order by ID. Use this instead of
// CancelAllOpenOrders when multiple positions exist on the same symbol —
// cancelling all orders would remove brackets belonging to other positions.
func (c *Client) CancelOrder(symbol string, orderID int64) error {
	params := url.Values{}
	params.Set("symbol", symbol)
	params.Set("orderId", strconv.FormatInt(orderID, 10))
	_, err := c.delete("/fapi/v1/order", params)
	if err != nil {
		return fmt.Errorf("cancel order %d %s: %w", orderID, symbol, err)
	}
	return nil
}

// CancelAllOpenOrders cancels every open order for a symbol in a single
// round-trip. Only safe when no other positions on this symbol carry active
// bracket orders.
func (c *Client) CancelAllOpenOrders(symbol string) error {
	params := url.Values{}
	params.Set("symbol", symbol)
	_, err := c.delete("/fapi/v1/allOpenOrders", params)
	if err != nil {
		return fmt.Errorf("cancel all orders %s: %w", symbol, err)
	}
	return nil
}

func parseOrderResult(body []byte) (OrderResult, error) {
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
