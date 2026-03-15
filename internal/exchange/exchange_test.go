package exchange

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/shopspring/decimal"
)

// mockServer returns an httptest.Server that responds with statusCode and body
// (JSON-encoded) for every request.
func mockServer(t *testing.T, statusCode int, body any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(statusCode)
		if body != nil {
			json.NewEncoder(w).Encode(body)
		}
	}))
}

// resetInfoCache clears the package-level symbol-info cache between tests.
func resetInfoCache() {
	infoCacheMu.Lock()
	infoCache = map[string]SymbolInfo{}
	infoCacheMu.Unlock()
}

// ── PlaceMarketOrder ─────────────────────────────────────────────────────────

func TestPlaceMarketOrder_Success(t *testing.T) {
	srv := mockServer(t, http.StatusOK, map[string]any{
		"orderId":     int64(7777),
		"symbol":      "BTCUSDT",
		"side":        "BUY",
		"executedQty": "0.001",
		"avgPrice":    "42000.50",
		"status":      "FILLED",
	})
	defer srv.Close()

	c := newClientWithBase("key", "secret", srv.URL)
	result, err := c.PlaceMarketOrder("BTCUSDT", SideBuy, decimal.NewFromFloat(0.001))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.OrderID != 7777 {
		t.Errorf("OrderID: got %d, want 7777", result.OrderID)
	}
	wantQty, _ := decimal.NewFromString("0.001")
	if !result.ExecutedQty.Equal(wantQty) {
		t.Errorf("ExecutedQty: got %s, want 0.001", result.ExecutedQty)
	}
	wantPrice, _ := decimal.NewFromString("42000.50")
	if !result.AvgPrice.Equal(wantPrice) {
		t.Errorf("AvgPrice: got %s, want 42000.50", result.AvgPrice)
	}
}

func TestPlaceMarketOrder_APIError(t *testing.T) {
	srv := mockServer(t, http.StatusBadRequest, map[string]any{
		"code": -1100,
		"msg":  "Illegal characters found in parameter",
	})
	defer srv.Close()

	c := newClientWithBase("key", "secret", srv.URL)
	_, err := c.PlaceMarketOrder("BTCUSDT", SideBuy, decimal.NewFromFloat(0.001))
	if err == nil {
		t.Fatal("expected error for non-200 response, got nil")
	}
}

// ── parseOrderResult ─────────────────────────────────────────────────────────

func TestParseOrderResult_MalformedExecutedQty(t *testing.T) {
	body := []byte(`{"orderId":1,"symbol":"BTCUSDT","side":"BUY","executedQty":"not-a-number","avgPrice":"100.0","status":"FILLED"}`)
	_, err := parseOrderResult(body)
	if err == nil {
		t.Fatal("expected error for malformed ExecutedQty, got nil")
	}
}

func TestParseOrderResult_MalformedAvgPrice(t *testing.T) {
	body := []byte(`{"orderId":1,"symbol":"BTCUSDT","side":"BUY","executedQty":"0.001","avgPrice":"bad","status":"FILLED"}`)
	_, err := parseOrderResult(body)
	if err == nil {
		t.Fatal("expected error for malformed AvgPrice, got nil")
	}
}

func TestParseOrderResult_MalformedJSON(t *testing.T) {
	_, err := parseOrderResult([]byte(`not json`))
	if err == nil {
		t.Fatal("expected error for malformed JSON, got nil")
	}
}

// ── GetUSDTBalance ────────────────────────────────────────────────────────────

func TestGetUSDTBalance_Success(t *testing.T) {
	srv := mockServer(t, http.StatusOK, []map[string]string{
		{"asset": "BNB", "availableBalance": "1.5", "crossWalletBalance": "1.5"},
		{"asset": "USDT", "availableBalance": "5000.00", "crossWalletBalance": "5000.00"},
	})
	defer srv.Close()

	c := newClientWithBase("key", "secret", srv.URL)
	bal, err := c.GetUSDTBalance()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want, _ := decimal.NewFromString("5000.00")
	if !bal.Equal(want) {
		t.Errorf("balance: got %s, want 5000.00", bal)
	}
}

func TestGetUSDTBalance_NotFound(t *testing.T) {
	srv := mockServer(t, http.StatusOK, []map[string]string{
		{"asset": "BNB", "availableBalance": "1.0", "crossWalletBalance": "1.0"},
	})
	defer srv.Close()

	c := newClientWithBase("key", "secret", srv.URL)
	_, err := c.GetUSDTBalance()
	if err == nil {
		t.Fatal("expected error when USDT absent from response, got nil")
	}
}

func TestGetUSDTBalance_APIError(t *testing.T) {
	srv := mockServer(t, http.StatusUnauthorized, nil)
	defer srv.Close()

	c := newClientWithBase("key", "secret", srv.URL)
	_, err := c.GetUSDTBalance()
	if err == nil {
		t.Fatal("expected error for non-200 response, got nil")
	}
}

// ── GetOpenPositions ──────────────────────────────────────────────────────────

func TestGetOpenPositions_FiltersZero(t *testing.T) {
	srv := mockServer(t, http.StatusOK, []map[string]string{
		{"symbol": "BTCUSDT", "positionAmt": "0", "entryPrice": "0", "unRealizedProfit": "0", "positionSide": "BOTH"},
		{"symbol": "ETHUSDT", "positionAmt": "1.5", "entryPrice": "2000.0", "unRealizedProfit": "50.0", "positionSide": "BOTH"},
	})
	defer srv.Close()

	c := newClientWithBase("key", "secret", srv.URL)
	positions, err := c.GetOpenPositions()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(positions) != 1 {
		t.Fatalf("expected 1 non-zero position, got %d", len(positions))
	}
	if positions[0].Symbol != "ETHUSDT" {
		t.Errorf("symbol: got %s, want ETHUSDT", positions[0].Symbol)
	}
	if positions[0].Side != "LONG" {
		t.Errorf("side: got %s, want LONG", positions[0].Side)
	}
}

func TestGetOpenPositions_NegativeAmountIsShort(t *testing.T) {
	srv := mockServer(t, http.StatusOK, []map[string]string{
		{"symbol": "BTCUSDT", "positionAmt": "-0.5", "entryPrice": "30000.0", "unRealizedProfit": "-100.0", "positionSide": "BOTH"},
	})
	defer srv.Close()

	c := newClientWithBase("key", "secret", srv.URL)
	positions, err := c.GetOpenPositions()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(positions) != 1 {
		t.Fatalf("expected 1 position, got %d", len(positions))
	}
	if positions[0].Side != "SHORT" {
		t.Errorf("side: got %s, want SHORT", positions[0].Side)
	}
	if positions[0].Quantity.IsNegative() {
		t.Errorf("quantity should be positive absolute value, got %s", positions[0].Quantity)
	}
}

func TestGetOpenPositions_Empty(t *testing.T) {
	srv := mockServer(t, http.StatusOK, []map[string]string{})
	defer srv.Close()

	c := newClientWithBase("key", "secret", srv.URL)
	positions, err := c.GetOpenPositions()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(positions) != 0 {
		t.Errorf("expected 0 positions, got %d", len(positions))
	}
}

// ── GetSymbolInfo ─────────────────────────────────────────────────────────────

func TestGetSymbolInfo_ParsesFilters(t *testing.T) {
	resetInfoCache()
	srv := mockServer(t, http.StatusOK, map[string]any{
		"symbols": []map[string]any{
			{
				"symbol": "BTCUSDT",
				"filters": []map[string]string{
					{"filterType": "LOT_SIZE", "stepSize": "0.001", "minQty": "0.001", "maxQty": "1000"},
					{"filterType": "PRICE_FILTER", "tickSize": "0.01"},
					{"filterType": "MIN_NOTIONAL", "notional": "5.0"},
				},
			},
		},
	})
	defer srv.Close()

	c := newClientWithBase("key", "secret", srv.URL)
	info, err := c.GetSymbolInfo("BTCUSDT")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantStep, _ := decimal.NewFromString("0.001")
	if !info.StepSize.Equal(wantStep) {
		t.Errorf("StepSize: got %s, want 0.001", info.StepSize)
	}
	wantNotional, _ := decimal.NewFromString("5.0")
	if !info.MinNotional.Equal(wantNotional) {
		t.Errorf("MinNotional: got %s, want 5.0", info.MinNotional)
	}
}

func TestGetSymbolInfo_CachePreventsRedundantHTTP(t *testing.T) {
	resetInfoCache()
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		json.NewEncoder(w).Encode(map[string]any{
			"symbols": []map[string]any{
				{
					"symbol":  "BTCUSDT",
					"filters": []map[string]string{{"filterType": "LOT_SIZE", "stepSize": "0.001", "minQty": "0.001", "maxQty": "1000"}},
				},
			},
		})
	}))
	defer srv.Close()

	c := newClientWithBase("key", "secret", srv.URL)
	if _, err := c.GetSymbolInfo("BTCUSDT"); err != nil {
		t.Fatalf("first call: %v", err)
	}
	if _, err := c.GetSymbolInfo("BTCUSDT"); err != nil {
		t.Fatalf("second call: %v", err)
	}
	if calls != 1 {
		t.Errorf("expected 1 HTTP call (second should be cached), got %d", calls)
	}
}

func TestGetSymbolInfo_SymbolNotFound(t *testing.T) {
	resetInfoCache()
	srv := mockServer(t, http.StatusOK, map[string]any{"symbols": []any{}})
	defer srv.Close()

	c := newClientWithBase("key", "secret", srv.URL)
	_, err := c.GetSymbolInfo("UNKNOWN")
	if err == nil {
		t.Fatal("expected error for unknown symbol, got nil")
	}
}

// ── RoundToStepSize ───────────────────────────────────────────────────────────

func TestRoundToStepSize(t *testing.T) {
	cases := []struct{ qty, step, want string }{
		{"1.2345", "0.001", "1.234"},  // truncates, does not round up
		{"1.000", "0.001", "1.000"},   // already aligned
		{"0.0019", "0.001", "0.001"},  // partial step kept
		{"1.5", "0", "1.5"},           // zero step → pass-through
	}
	for _, tc := range cases {
		qty, _ := decimal.NewFromString(tc.qty)
		step, _ := decimal.NewFromString(tc.step)
		got := RoundToStepSize(qty, step)
		want, _ := decimal.NewFromString(tc.want)
		if !got.Equal(want) {
			t.Errorf("RoundToStepSize(%s, %s) = %s, want %s", tc.qty, tc.step, got, tc.want)
		}
	}
}

// ── CancelOrder ───────────────────────────────────────────────────────────────

func TestCancelOrder_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"orderId": 42, "status": "CANCELED"})
	}))
	defer srv.Close()

	c := newClientWithBase("key", "secret", srv.URL)
	if err := c.CancelOrder("BTCUSDT", 42); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCancelOrder_APIError(t *testing.T) {
	srv := mockServer(t, http.StatusBadRequest, map[string]any{
		"code": -2011,
		"msg":  "Unknown order sent.",
	})
	defer srv.Close()

	c := newClientWithBase("key", "secret", srv.URL)
	if err := c.CancelOrder("BTCUSDT", 99); err == nil {
		t.Fatal("expected error for API error, got nil")
	}
}
