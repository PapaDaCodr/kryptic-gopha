// Package exchange provides a thin authenticated client for the Binance
// USDT-M Futures REST API (fapi.binance.com).
//
// Supported operations:
//   - Account balance query
//   - Exchange info / LOT_SIZE filter lookup
//   - Market and limit order placement
//   - Open position query
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
	defaultRecvWindow = 5000 // ms
)

// Client is an authenticated Binance Futures REST client.
type Client struct {
	apiKey    string
	secretKey string
	baseURL   string
	http      *http.Client
}

// NewClient creates a new authenticated Binance Futures client.
func NewClient(apiKey, secretKey string) *Client {
	return &Client{
		apiKey:    apiKey,
		secretKey: secretKey,
		baseURL:   defaultBaseURL,
		http:      &http.Client{Timeout: 10 * time.Second},
	}
}

// newClientWithBase is used in tests to inject a custom base URL.
func newClientWithBase(apiKey, secretKey, baseURL string) *Client {
	c := NewClient(apiKey, secretKey)
	c.baseURL = baseURL
	return c
}

// sign appends timestamp + recvWindow + HMAC-SHA256 signature to params.
func (c *Client) sign(params url.Values) {
	params.Set("timestamp", strconv.FormatInt(time.Now().UnixMilli(), 10))
	params.Set("recvWindow", strconv.Itoa(defaultRecvWindow))

	mac := hmac.New(sha256.New, []byte(c.secretKey))
	mac.Write([]byte(params.Encode()))
	params.Set("signature", hex.EncodeToString(mac.Sum(nil)))
}

// publicGet performs an unauthenticated GET (e.g. /fapi/v1/exchangeInfo).
func (c *Client) publicGet(path string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet,
		fmt.Sprintf("%s%s", c.baseURL, path), nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	return c.doRequest(req, path)
}

// get performs an authenticated GET request.
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

// post performs an authenticated POST request.
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
