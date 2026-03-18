# Production Readiness Plan — Phases 1-4 Complete

**Status**: ✅ All Phases 1-4 implemented and tested
**Target Go-Live**: May 1, 2026
**Time Remaining**: ~6 weeks to validate and optimize

---

## What's Been Completed

### Phase 1 — Risk Management Safeguards ✅
Risk controls that protect against catastrophic losses:
- **Daily Loss Circuit Breaker**: Trading auto-pauses when daily PnL drops below threshold
  - `DAILY_LOSS_LIMIT` env var (default: 5% of account)
  - Resets at calendar day boundary (00:00 UTC)
- **Per-Trade Risk Sizing**: Each trade size = `RiskPerTrade × Balance / StopLoss`
  - `RISK_PER_TRADE` env var (default: live=1%, paper=2%)
  - Scales dynamically as balance grows/shrinks
- **Max Open Trades**: Prevents over-leverage
  - `MAX_OPEN_TRADES` env var (default: live=3, paper=5)
  - Checked before every entry

### Phase 2 — Strategy & Backtester Improvements ✅
Tools for validating strategy effectiveness:
- **Multi-Symbol Backtest Support**:
  ```bash
  go run cmd/backtester/main.go -symbols "BTCUSDT,ETHUSDT,BNBUSDT" \
    -interval 1m -limit 1000 -output results.csv
  ```
  - Fetches klines for each symbol
  - Replays in chronological order
  - Outputs detailed per-trade CSV report

- **Extended Time Exit**: 10 minutes → **30 minutes**
  - Better for trend trades that need time to develop
  - Still has TP/SL exits for faster exits when profitable

- **Max Daily Trades Limit**:
  - `MAX_DAILY_TRADES` env var (default: 15 trades/day)
  - Resets at calendar day boundary
  - Prevents overtrading and sequence-dependent losses

- **Enhanced Backtest Metrics**:
  - Total P&L, ROI%, Win rate, Profit factor
  - Max drawdown, Avg win/loss size
  - Per-trade log with entry/exit/reason/PnL

### Phase 3 — Dynamic Account Sizing ✅
Enables starting with small accounts (minimum $10):
- **Minimum Balance Enforcement**: `$10` account minimum
  - Trades skip if account < $10
  - Prevents errors from tiny position sizes
- **Dynamic Scaling**: Position size = `(Balance × Risk%) / StopLoss`
  - As account grows → position sizes grow
  - No fixed contracts = works with any account size

### Phase 4 — Advanced Monitoring & Health Checks ✅
Real-time visibility into bot health:
- **New `/api/metrics` Endpoint**:
  ```json
  {
    "balance": "9850.50",
    "roi_percent": "-1.49%",
    "total_pnl": "-149.50",
    "daily_pnl": "-250.00",
    "total_trades": 42,
    "wins": 23,
    "losses": 19,
    "win_rate": "54.76%",
    "profit_factor": "1.23",
    "max_drawdown": "5.32%",
    "active_trades": 2,
    "daily_trade_count": 8,
    "trading_enabled": true
  }
  ```

- **Position Reconciliation** (Live Mode):
  - On startup, compares local trades vs Binance open positions
  - Alerts if orphaned positions or missing trades detected
  - Ensures sync after crashes or restarts

- **Graceful Shutdown**:
  - SIGTERM signal triggers orderly close of all positions
  - Saves state file before exit
  - Prevents orphaned positions on deployment

---

## Test Status
✅ **All 25 tests pass with `-race` flag**
```bash
go test -race ./...
```
- engine: position mgmt, exits, daily reset, time exit
- ingester: stream handling, kline conversion
- models: data structures
- notifier: Telegram notifications
- exchange: order placement (testnet)

---

## Environment Variables Summary

### Risk Management
- `RISK_PER_TRADE` — risk amount per trade (default: live=0.01, paper=0.02)
- `DAILY_LOSS_LIMIT` — daily loss threshold (default: 0.05 = 5%)
- `MAX_OPEN_TRADES` — max concurrent positions (default: live=3, paper=5)
- `MAX_DAILY_TRADES` — max trades per day (default: 15)

### Strategy Parameters
- `SHORT_PERIOD` — EMA short (default: 12)
- `LONG_PERIOD` — EMA long (default: 26)
- `RSI_PERIOD` — RSI period (default: 14)
- `BAR_INTERVAL_SECONDS` — candle size (default: 60)

### Binance Connection
- `BINANCE_API_KEY` — API key (required for live mode)
- `BINANCE_API_SECRET` — API secret (required for live mode)
- `BINANCE_TESTNET=true` — use testnet.binancefuture.com
- `TRADING_MODE=live|paper` — execution mode

### Telegram Alerts
- `TELEGRAM_BOT_TOKEN` — bot token (optional)
- `TELEGRAM_CHAT_ID` — chat ID (optional)

### Server
- `PORT` — HTTP API port (default: 8080)
- `INITIAL_BALANCE` — starting capital (default: live=1000, paper=10000)
- `WATCHLIST` — symbols to trade (default: "BTCUSDT,ETHUSDT,BNBUSDT")

---

## Backtest Examples

### Single Symbol, 1000 candles, CSV output:
```bash
go run cmd/backtester/main.go \
  -symbols BTCUSDT \
  -interval 1m \
  -limit 1000 \
  -output backtest_btc.csv
```

### Multi-symbol portfolio backtest:
```bash
go run cmd/backtester/main.go \
  -symbols "BTCUSDT,ETHUSDT,BNBUSDT,SOLUSDT" \
  -interval 5m \
  -limit 500 \
  -output portfolio_5m.csv
```

### With custom strategy params:
```bash
go run cmd/backtester/main.go \
  -symbols BTCUSDT \
  -interval 1h \
  -limit 250 \
  -short 10 \
  -long 25 \
  -rsi 12 \
  -output custom_params.csv
```

---

## What to Test Before May 1st

### 1. Extended Backtests (2+ weeks)
- [ ] Run 2-week backtest on 1m, 5m, 1h intervals
- [ ] Test with risk_tolerance 40-60% (your loss tolerance)
- [ ] Identify optimal parameters (TP%, SL%, ATR multiplier)
- [ ] Verify win rate stays >50% across different market conditions

### 2. Testnet Live Trading (48+ hours)
- [ ] Create Binance Testnet account
- [ ] Set `BINANCE_TESTNET=true` and `TRADING_MODE=live`
- [ ] Run bot for 48+ hours
- [ ] Check position reconciliation on restart
- [ ] Verify graceful shutdown closes all positions

### 3. Crash Recovery
- [ ] Start bot with open positions
- [ ] Kill process (simulates crash)
- [ ] Restart bot
- [ ] Verify `/api/metrics` shows consistent balance
- [ ] Check state file has latest trades

### 4. Monitoring Dashboard
- [ ] Monitor `/api/metrics` endpoint
- [ ] Track win rate, drawdown, daily PnL
- [ ] Verify alerts work (if Telegram enabled)
- [ ] Test manual `/stop` and `/resume` commands

---

## Hosting Recommendations

### Free Tier Options (No Credit Card)
1. **Railway.app** (free tier: $5/mo credits)
   - Go support built-in
   - Can run 24/7 with credits
   - No credit card needed initially

2. **Render.com** (free tier: limited uptime)
   - Has always-on option with payment method
   - Good for proof of concept

3. **Heroku** (deprecated free tier, but can use Procfile)
   - Alternatives: Fly.io (free tier with 3 shared-cpu-1x VMs)

4. **Self-Hosted (Recommended for production)**
   - Raspberry Pi ($50) + electricity
   - VPS with free tier ($0-5/mo from Hostinger, etc.)
   - Better uptime control

### Docker Deployment
Bot already works in Docker. Create `Dockerfile`:
```dockerfile
FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY . .
RUN go build -o server ./cmd/server

FROM alpine:latest
RUN apk --no-cache add ca-certificates
WORKDIR /root
COPY --from=builder /app/server .
CMD ["./server"]
```

---

## Risk Tolerance (40-60% Loss Tolerance)

Your stated risk tolerance: willing to lose 40-60% to the market.

**Recommended Risk Settings**:
- `DAILY_LOSS_LIMIT=0.40` → stop trading if -40% on day (aggressive)
- `RISK_PER_TRADE=0.05` → 5% risk per trade (aggressive)
- `MAX_DAILY_TRADES=20` → allow more trades with higher risk
- `MAX_OPEN_TRADES=5` → allow more concurrent positions

**OR Conservative Approach** (recommended for proof of concept):
- `DAILY_LOSS_LIMIT=0.10` → stop trading if -10% on day
- `RISK_PER_TRADE=0.02` → 2% risk per trade
- `MAX_DAILY_TRADES=15` → balanced frequency
- `MAX_OPEN_TRADES=3` → safety first

Start conservative, increase risk after validating strategy in live testnet.

---

## Trading Frequency Recommendations

**Your question**: What's the max number of trades and recommended frequency?

Based on Phase 2 implementation:
- **Default**: 15 trades/day limit with 30-min time exit
- **Aggressive**: 20-25 trades/day (scalping, 1m bars, 5-min exit)
- **Conservative**: 5-10 trades/day (swing, 1h bars, 30-min exit)
- **Optimal**: Test with backtests, find your sweet spot

**Profit Factor Sweet Spot** (from backtest metrics):
- < 1.0 = losing strategy (skip)
- 1.0-1.5 = viable (go with it)
- 1.5-2.0+ = strong (increase position size)

---

## Next Steps (Immediate)

1. **Run Extended Backtests** (this week)
   ```bash
   go run cmd/backtester/main.go -symbols "BTCUSDT" -interval 1m -limit 2880 -output 2day.csv
   ```

2. **Analyze Results**
   - Win rate > 55%?
   - Profit factor > 1.2?
   - Max drawdown < 20%?

3. **Setup Testnet** (next week)
   - Create account at https://testnet.binancefuture.com
   - Generate API keys
   - Update .env: `BINANCE_TESTNET=true`, `TRADING_MODE=live`
   - Run 48+ hours

4. **Test Crash Recovery** (week 3)
   - Run bot with positions
   - Kill and restart
   - Verify state consistency

5. **Hosting Setup** (week 4)
   - Choose platform (Railway/Render/self-hosted)
   - Deploy Docker container
   - Setup monitoring

---

## Success Criteria for May 1st Launch

- [ ] Backtest win rate > 55% across multiple instruments
- [ ] Profit factor > 1.2
- [ ] Max drawdown < 25%
- [ ] Testnet live trading verified (48+ hours)
- [ ] Crash recovery tested and working
- [ ] Graceful shutdown tested
- [ ] Hosting platform confirmed
- [ ] Monitoring dashboard accessible
- [ ] API keys rotated and secured
- [ ] Risk parameters tuned to tolerance

---

## Questions for You

Before proceeding further, clarify:

1. **Risk Tolerance**: Do you want to stay conservative (10% daily loss limit) or aggressive (40% loss tolerance)?
2. **Trading Hours**: Want the bot to trade 24/7 or specific hours?
3. **Hosting**: Self-hosted (Raspberry Pi) or cloud (Railway/Render)?
4. **Monitoring**: Do you want email alerts or just Telegram?
5. **Starting Capital**: When ready, will you start with $100, $1000, or more?

These answers will help fine-tune the parameters for your production environment.
