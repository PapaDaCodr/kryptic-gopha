# Kryptic Gopha

A concurrent algorithmic trading engine written in Go, targeting Binance USDT-M Perpetual Futures. The system supports both paper trading (simulation against live market data) and live execution, controlled via a web dashboard and Telegram bot.

---

## Architecture

```
cmd/
  server/       — Main server process (paper + live modes)
  backtester/   — CLI tool for historical simulation

internal/
  engine/       — Core trading loop: strategy, candle aggregation, position management
  exchange/     — Authenticated Binance Futures REST client
  ingester/     — Binance WebSocket stream consumer + historical klines fetcher
  models/       — Domain types: MarketTick, Signal, Candle

pkg/
  notifier/     — Telegram bot: async notifications + command listener

web/            — Dashboard frontend (HTML/JS/CSS)
research/       — Strategy research and analysis documents
```

### Data Flow

```
Binance WS Stream
      │
      ▼
   Ingester  ──── tick ────►  EngineManager
                                    │
                          ┌─────────┼──────────┐
                          ▼         ▼           ▼
                      Candle    Trader        Strategy
                    Aggregation UpdateMetrics  Analyze
                          │                     │
                          │                   Signal
                          │                 (with ATR)
                          └─────────────────────►
                                                 │
                                            Trader.OnSignal
                                                 │
                                      ATR-sized position
                                      Exchange Orders (live)
                                      Simulated P&L (paper)
```

---

## Strategy

The production strategy (`EfficientMultiFactorStrategy`) applies five sequential filters. All conditions must pass for an entry to be warranted.

**Long entry conditions (short mirrors each with reversed comparisons):**

| # | Filter | Condition | Purpose |
|---|---|---|---|
| 1 | Macro trend | `close > EMA(200)` | Align with dominant directional bias |
| 2 | ADX regime gate | `ADX(14) > 25` | Confirm a trending market; suppress EMA noise in ranging conditions |
| 3 | Entry trigger | `EMA(12) > EMA(26)` | MACD-equivalent crossover in trend direction |
| 4 | Momentum gate | `RSI(14) < 70` | Avoid entering near exhaustion |
| 5 | Volume confirmation | `bar_volume > 1.2 × EMA20(volume)` | Require institutional participation behind the move |

The 200-period EMA macro filter and the ADX gate together address the two primary failure modes of EMA crossover systems: counter-trend entries and false crossovers during ranging markets.

**Signal output** includes `ATR(14)` in price units, which the trader uses to size positions dynamically (see Risk Management below).

**Indicator computation** — all five indicators are maintained incrementally using Wilder's exponential smoothing, reducing each bar update to O(1). State is seeded on first contact with each symbol by processing 100 historical 1-minute klines fetched from the Binance REST API.

See [research/hft_analysis.md](research/hft_analysis.md) for a detailed quantitative assessment of the strategy's performance characteristics and remaining improvement roadmap.

---

## Risk Management

| Parameter | Paper Default | Live Default | Environment Variable |
|---|---|---|---|
| Take-profit | 5% | 0.5% | `TP` |
| Stop-loss (fixed fallback) | 2% | 0.3% | `SL` |
| Dynamic stop-loss | 1.5 × ATR(14) | 1.5 × ATR(14) | — (automatic when ATR available) |
| Trailing SL distance | 1.5% | 0.3% | `TRAILING_SL_PCT` |
| Risk per trade | 2% of balance | 1% of balance | `RISK_PER_TRADE` |
| Daily loss limit | 6% | 5% | `DAILY_LOSS_LIMIT` |
| Max open trades | 5 | 3 | `MAX_OPEN_TRADES` |

**Position sizing** — when `Signal.ATR > 0`, position size is computed as `(balance × risk%) / (1.5 × ATR)`, bounding the dollar loss on any single trade to the configured risk percentage regardless of current volatility. When ATR is unavailable (insufficient history), the system falls back to `(balance × risk%) / (entry_price × SL%)`.

**Dynamic stop-loss** — the ATR-based stop (`entry ± 1.5 × ATR`) is stored per-trade and used throughout the life of the position. This produces tighter stops in low-volatility conditions (faster capital recovery) and wider stops in volatile conditions (fewer premature exits on valid trends).

**Circuit breaker** — suspends all new entries when daily PnL falls below `DAILY_LOSS_LIMIT`. Resets automatically at calendar day rollover or on the `/resume` Telegram command.

---

## Configuration

All configuration is read from environment variables at startup. Copy `.env.example` to `.env` and set values before running.

```env
# Trading mode
TRADING_MODE=paper          # paper | live

# Strategy periods
SHORT_PERIOD=12
LONG_PERIOD=26
RSI_PERIOD=14
BAR_INTERVAL_SECONDS=60     # Candle aggregation interval in seconds

# Watchlist (comma-separated Binance USDT-M futures symbols)
WATCHLIST=BTCUSDT,ETHUSDT,BNBUSDT

# Capital and risk
INITIAL_BALANCE=10000
TP=0.05
SL=0.02
TRAILING_SL=true
TRAILING_SL_PCT=0.015
RISK_PER_TRADE=0.02
DAILY_LOSS_LIMIT=0.06
MAX_OPEN_TRADES=5

# Live mode only
BINANCE_API_KEY=
BINANCE_API_SECRET=

# Optional: Telegram control panel
TELEGRAM_BOT_TOKEN=
TELEGRAM_CHAT_ID=

# Server
PORT=8080
ENV=dev                     # dev = human-readable logs; omit for JSON logs
```

---

## Running

**Paper trading (default):**
```bash
go run cmd/server/main.go
```

**Live trading:**
```bash
TRADING_MODE=live go run cmd/server/main.go
```

**Backtester:**
```bash
go run cmd/backtester/main.go -symbol BTCUSDT -interval 1m -limit 500
```

**Docker:**
```bash
make run
# or
docker build -t kryptic-gopha .
docker run --env-file .env -p 8080:8080 kryptic-gopha
```

---

## API Endpoints

| Method | Path | Description |
|---|---|---|
| GET | `/health` | Bot status, win rate, active trade count |
| GET | `/api/state` | Full trader state snapshot |
| GET | `/api/trades` | Active and completed trades |
| GET | `/api/signals?symbol=BTCUSDT` | Recent signal history for a symbol |
| GET | `/api/candles?symbol=BTCUSDT` | OHLCV history + current forming bar |
| GET | `/` | Web dashboard |

---

## Telegram Commands

| Command | Description |
|---|---|
| `/status` | Balance, daily PnL, active trade count |
| `/stop` | Immediately suspend all new entries |
| `/resume` | Re-enable trading (clears circuit breaker) |
| `/setbalance <amount>` | Update trading capital |
| `/help` | Command list |

---

## Development

```bash
# Run all tests
go test ./...

# Run with race detector
go test -race ./...

# Build binary
go build -o bin/kryptic-gopha ./cmd/server
```

---

## Disclaimer

This software is a research and educational tool. Cryptocurrency trading involves substantial risk of loss. Nothing in this codebase constitutes financial advice. Use in live markets entirely at your own risk.
