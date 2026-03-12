# Kryptic Gopha: High-Frequency Cryptocurrency Trading Engine

Kryptic Gopha is a high-performance, concurrent trading system developed in Go. The platform is engineered to handle real-time market data streams from various exchanges, apply complex multi-factor indicator analysis, and execute paper trading simulations with institutional-grade risk management protocols.

## System Architecture

The engine is built on a modular architecture designed for horizontal scalability and low-latency processing:

### 1. Data Ingestion Layer
Utilizes high-speed WebSocket connections to Binance for real-time trade data. Built with `context.Context` for robust lifecycle management, preventing goroutine leaks and ensuring clean reconnections.

### 2. Processing Engine (EngineManager)
The core of the system uses a sharded state management approach. Each market pair is isolated with its own mutex and state. 
- **Precision**: Uses `shopspring/decimal` for 100% financial calculation accuracy.
- **Concurrency**: Implements non-blocking signal broadcasting to ensure no symbol stalls the entire engine.

### 3. Strategy Implementation
The system implements a recursive Multi-Factor Strategy. 
- **Warm-up**: Automatically fetches historical data on startup to seed indicators.
- **Complexity**: Reducing computational complexity from O(N) to O(1) via incremental EMA/RSI calculations.

### 4. Paper Trading & Risk Management
The institutional-grade PaperTrader simulates live execution with:
- **Dynamic Position Sizing**: Automatically calculates order quantity based on 1% balance risk.
- **Circuit Breaker**: Auto-suspends trading if the daily loss limit (default 5%) is triggered.
- **State Persistence**: Saves all trades and metrics to `trader_state.json` to survive restarts.

### 5. Observability
- **Structured Logging**: All logs are JSON-formatted via `zerolog` for enterprise monitoring.
- **JSON API**: Health and performance metrics exposed via `/health` endpoint.

## Getting Started

### Installation
1. Clone the repository:
   ```bash
   git clone https://github.com/papadacodr/kryptic-gopha.git
   cd kryptic-gopha
   ```

2. Initialize environment variables in `.env`.

3. Run the Backtester or Live Bot:
   ```bash
   # Backtest
   go run cmd/backtester/main.go --symbol ETHUSDT --limit 1000
   
   # Live Bot (Paper Mode)
   go run cmd/server/main.go
   ```

## Configuration

| Variable | Description |
| :--- | :--- |
| WATCHLIST | Comma-separated symbols (e.g., BTCUSDT,ETHUSDT) |
| TP / SL | Take-Profit (0.005) and Stop-Loss (0.003) |
| INITIAL_BALANCE | Wallet starting amount (e.g., 10000.0) |
| RISK_PER_TRADE | % balance to risk per signal (0.01 = 1%) |
| DAILY_LOSS_LIMIT| Drawdown limit before circuit breaker (0.05 = 5%) |

## Disclaimer

This software is provided for educational purposes. Digital asset trading involves significant risk. The developers are not responsible for financial losses.
