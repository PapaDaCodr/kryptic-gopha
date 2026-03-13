package exchange

import (
	"encoding/json"
	"fmt"
	"sync"

	"github.com/shopspring/decimal"
)

// SymbolInfo holds the trading rules we need for order sizing.
type SymbolInfo struct {
	StepSize    decimal.Decimal // LOT_SIZE minimum quantity increment
	MinQty      decimal.Decimal // Minimum order quantity
	MaxQty      decimal.Decimal // Maximum order quantity
	TickSize    decimal.Decimal // PRICE_FILTER minimum price increment
	MinNotional decimal.Decimal // MIN_NOTIONAL minimum order value in USDT
}

type exchangeInfoResponse struct {
	Symbols []struct {
		Symbol  string `json:"symbol"`
		Filters []struct {
			FilterType string `json:"filterType"`
			StepSize   string `json:"stepSize,omitempty"`
			MinQty     string `json:"minQty,omitempty"`
			MaxQty     string `json:"maxQty,omitempty"`
			TickSize   string `json:"tickSize,omitempty"`
			Notional   string `json:"notional,omitempty"`
		} `json:"filters"`
	} `json:"symbols"`
}

var (
	infoCache   = map[string]SymbolInfo{}
	infoCacheMu sync.RWMutex
)

// GetSymbolInfo fetches and caches exchange rules for a symbol.
// Results are cached for the lifetime of the process since rules rarely change.
func (c *Client) GetSymbolInfo(symbol string) (SymbolInfo, error) {
	infoCacheMu.RLock()
	if info, ok := infoCache[symbol]; ok {
		infoCacheMu.RUnlock()
		return info, nil
	}
	infoCacheMu.RUnlock()

	body, err := c.publicGet("/fapi/v1/exchangeInfo")
	if err != nil {
		return SymbolInfo{}, fmt.Errorf("exchange info: %w", err)
	}

	var resp exchangeInfoResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return SymbolInfo{}, fmt.Errorf("parse exchange info: %w", err)
	}

	infoCacheMu.Lock()
	defer infoCacheMu.Unlock()

	for _, sym := range resp.Symbols {
		info := SymbolInfo{}
		for _, f := range sym.Filters {
			switch f.FilterType {
			case "LOT_SIZE":
				info.StepSize, _ = decimal.NewFromString(f.StepSize)
				info.MinQty, _ = decimal.NewFromString(f.MinQty)
				info.MaxQty, _ = decimal.NewFromString(f.MaxQty)
			case "PRICE_FILTER":
				info.TickSize, _ = decimal.NewFromString(f.TickSize)
			case "MIN_NOTIONAL":
				info.MinNotional, _ = decimal.NewFromString(f.Notional)
			}
		}
		infoCache[sym.Symbol] = info
	}

	info, ok := infoCache[symbol]
	if !ok {
		return SymbolInfo{}, fmt.Errorf("symbol %s not found in exchange info", symbol)
	}
	return info, nil
}

// RoundToStepSize rounds qty down to the nearest valid LOT_SIZE increment.
func RoundToStepSize(qty, stepSize decimal.Decimal) decimal.Decimal {
	if stepSize.IsZero() {
		return qty
	}
	steps := qty.Div(stepSize).Floor()
	return steps.Mul(stepSize)
}
