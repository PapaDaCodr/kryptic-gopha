package ingester

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// buildKline constructs a minimal Binance kline array with the required fields.
// Binance kline format: [openTime, open, high, low, close, volume, closeTime, ...]
func buildKline(closePrice string, closeTimeMs float64) []interface{} {
	kline := make([]interface{}, 12)
	kline[0] = float64(1700000000000) // openTime
	kline[1] = "41000.00"             // open
	kline[2] = "43000.00"             // high
	kline[3] = "40000.00"             // low
	kline[4] = closePrice             // close  (klineCloseIdx)
	kline[5] = "100.5"                // volume
	kline[6] = closeTimeMs            // closeTime (klineCloseTimeIdx)
	return kline
}

func mockKlineServer(t *testing.T, statusCode int, body interface{}) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(statusCode)
		if body != nil {
			json.NewEncoder(w).Encode(body)
		}
	}))
}

func TestFetchHistoricalKlines_Success(t *testing.T) {
	klines := [][]interface{}{
		buildKline("42000.50", 1700000059999),
		buildKline("43100.00", 1700000119999),
	}

	srv := mockKlineServer(t, http.StatusOK, klines)
	defer srv.Close()

	original := binanceRestURL
	binanceRestURL = srv.URL
	defer func() { binanceRestURL = original }()

	ticks, err := FetchHistoricalKlines("BTCUSDT", "1m", 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ticks) != 2 {
		t.Fatalf("expected 2 ticks, got %d", len(ticks))
	}
	if ticks[0].Price != "42000.50" {
		t.Errorf("tick[0] price: got %s, want 42000.50", ticks[0].Price)
	}
	if ticks[1].Price != "43100.00" {
		t.Errorf("tick[1] price: got %s, want 43100.00", ticks[1].Price)
	}
	if ticks[0].Symbol != "BTCUSDT" {
		t.Errorf("symbol: got %s, want BTCUSDT", ticks[0].Symbol)
	}
}

func TestFetchHistoricalKlines_NonOKStatus(t *testing.T) {
	srv := mockKlineServer(t, http.StatusTooManyRequests, nil)
	defer srv.Close()

	original := binanceRestURL
	binanceRestURL = srv.URL
	defer func() { binanceRestURL = original }()

	_, err := FetchHistoricalKlines("BTCUSDT", "1m", 10)
	if err == nil {
		t.Error("expected error for non-200 status, got nil")
	}
}

func TestFetchHistoricalKlines_MalformedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`not valid json`))
	}))
	defer srv.Close()

	original := binanceRestURL
	binanceRestURL = srv.URL
	defer func() { binanceRestURL = original }()

	_, err := FetchHistoricalKlines("BTCUSDT", "1m", 10)
	if err == nil {
		t.Error("expected error for malformed JSON, got nil")
	}
}

func TestFetchHistoricalKlines_ShortKlineArray(t *testing.T) {
	// Array with fewer than klineMinLen fields
	short := [][]interface{}{{"41000.00", "43000.00"}}

	srv := mockKlineServer(t, http.StatusOK, short)
	defer srv.Close()

	original := binanceRestURL
	binanceRestURL = srv.URL
	defer func() { binanceRestURL = original }()

	_, err := FetchHistoricalKlines("BTCUSDT", "1m", 1)
	if err == nil {
		t.Error("expected error for short kline array, got nil")
	}
}

func TestFetchHistoricalKlines_WrongCloseType(t *testing.T) {
	// Close price as a number instead of a string (unexpected Binance format change)
	kline := make([]interface{}, 12)
	kline[4] = float64(42000) // should be string
	kline[6] = float64(1700000059999)
	bad := [][]interface{}{kline}

	srv := mockKlineServer(t, http.StatusOK, bad)
	defer srv.Close()

	original := binanceRestURL
	binanceRestURL = srv.URL
	defer func() { binanceRestURL = original }()

	_, err := FetchHistoricalKlines("BTCUSDT", "1m", 1)
	if err == nil {
		t.Error("expected error for wrong close price type, got nil")
	}
}

func TestFetchHistoricalKlines_EmptyResponse(t *testing.T) {
	srv := mockKlineServer(t, http.StatusOK, [][]interface{}{})
	defer srv.Close()

	original := binanceRestURL
	binanceRestURL = srv.URL
	defer func() { binanceRestURL = original }()

	ticks, err := FetchHistoricalKlines("BTCUSDT", "1m", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ticks) != 0 {
		t.Errorf("expected 0 ticks, got %d", len(ticks))
	}
}
