// Package exchange provides a thin authenticated client for the Binance
// USDT-M Futures REST API (fapi.binance.com).
package exchange

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

const (
	defaultBaseURL    = "https://fapi.binance.com"
	defaultRecvWindow = 5000 // milliseconds; generous window for clock skew on VPS
)

// Client is an authenticated Binance USDT-M Futures REST client.
// It is safe for concurrent use across goroutines; the underlying http.Client
// manages connection pooling internally.
type Client struct {
	apiKey    string
	secretKey string
	baseURL   string
	http      *http.Client
}

// NewClient constructs an authenticated Binance Futures client using
// HMAC-SHA256 request signing.
func NewClient(apiKey, secretKey string) *Client {
	return &Client{
		apiKey:    apiKey,
		secretKey: secretKey,
		baseURL:   defaultBaseURL,
		http:      &http.Client{Timeout: 10 * time.Second},
	}
}

// newClientWithBase overrides the base URL. Used in tests to redirect
// requests to an httptest.Server without modifying the public API surface.
func newClientWithBase(apiKey, secretKey, baseURL string) *Client {
	c := NewClient(apiKey, secretKey)
	c.baseURL = baseURL
	return c
}

// sign mutates params in-place, appending the timestamp, recvWindow, and
// HMAC-SHA256 signature required by every authenticated Binance endpoint.
// Callers must encode params *after* this call.
func (c *Client) sign(params url.Values) {
	params.Set("timestamp", strconv.FormatInt(time.Now().UnixMilli(), 10))
	params.Set("recvWindow", strconv.Itoa(defaultRecvWindow))
	mac := hmac.New(sha256.New, []byte(c.secretKey))
	mac.Write([]byte(params.Encode()))
	params.Set("signature", hex.EncodeToString(mac.Sum(nil)))
}

// publicGet issues an unauthenticated GET (e.g. /fapi/v1/exchangeInfo).
func (c *Client) publicGet(path string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet,
		fmt.Sprintf("%s%s", c.baseURL, path), nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	return c.doRequest(req, path)
}

// get issues a signed GET request with params in the query string.
func (c *Client) get(path string, params url.Values) ([]byte, error) {
	if params == nil {
		params = url.Values{}
	}
	c.sign(params)
	req, err := http.NewRequest(http.MethodGet,
		fmt.Sprintf("%s%s?%s", c.baseURL, path, params.Encode()), nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("X-MBX-APIKEY", c.apiKey)
	return c.doRequest(req, path)
}

// post issues a signed POST request with params in the query string.
// Binance Futures order placement uses POST with query-string params, not a JSON body.
func (c *Client) post(path string, params url.Values) ([]byte, error) {
	if params == nil {
		params = url.Values{}
	}
	c.sign(params)
	req, err := http.NewRequest(http.MethodPost,
		fmt.Sprintf("%s%s", c.baseURL, path), nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.URL.RawQuery = params.Encode()
	req.Header.Set("X-MBX-APIKEY", c.apiKey)
	return c.doRequest(req, path)
}

// delete issues a signed DELETE request. Binance Futures uses DELETE for
// all order-cancellation endpoints.
func (c *Client) delete(path string, params url.Values) ([]byte, error) {
	if params == nil {
		params = url.Values{}
	}
	c.sign(params)
	req, err := http.NewRequest(http.MethodDelete,
		fmt.Sprintf("%s%s?%s", c.baseURL, path, params.Encode()), nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("X-MBX-APIKEY", c.apiKey)
	return c.doRequest(req, path)
}

func (c *Client) doRequest(req *http.Request, path string) ([]byte, error) {
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", req.Method, path, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s %s: HTTP %d: %s", req.Method, path, resp.StatusCode, body)
	}
	return body, nil
}
