# Kryptic Gopha: High-Frequency Cryptocurrency Trading Engine

Kryptic Gopha is a high-performance, concurrent trading system developed in Go. The platform is engineered to handle real-time market data streams from various exchanges, apply complex multi-factor indicator analysis, and execute paper trading simulations with institutional-grade risk management protocols.

## System Architecture

The engine is built on a modular architecture designed for horizontal scalability and low-latency processing:

### 1. Data Ingestion Layer
Utilizes high-speed WebSocket connections to Binance for real-time trade data. The layer includes robust reconnection logic and connection monitoring to ensure zero data loss during high-volatility events.

### 2. Processing Engine (EngineManager)
The core of the system uses a sharded state management approach. Each market pair is isolated with its own mutex and state, allowing the engine to process thousands of ticks per second across multiple symbols without lock contention.

### 3. Strategy Implementation
The system implements a recursive Multi-Factor Strategy. By utilizing incremental calculation logic for EMA (Exponential Moving Average) and RSI (Relative Strength Index), the computational complexity of signal generation is reduced from O(N) to O(1) per update.

### 4. Paper Trading & Risk Management
The integrated PaperTrader simulates live execution. It incorporates:
- Real-time Stop-Loss (SL) monitoring.
- Real-time Take-Profit (TP) monitoring.
- Event-based time tracking for accurate historical simulation.

## Technical Specifications

- Language: Go 1.22
- Concurrency Model: CSP (Communicating Sequential Processes) via Channels
- Memory Management: Zero-allocation-focused price buffering
- Deployment: Dockerized multi-stage builds

## Getting Started

### Prerequisites
- Go 1.22 or higher
- Docker (optional, for containerized deployment)

### Installation
1. Clone the repository:
   ```bash
   git clone https://github.com/papadacodr/kryptic-gopha.git
   cd kryptic-gopha
   ```

2. Initialize environment variables:
   ```bash
   cp .env.example .env
   ```

3. Download dependencies:
   ```bash
   go mod download
   ```

### Running the Live Bot
To run the real-time trading engine:
```bash
go build -o bot ./cmd/server/main.go
./bot
```

### Running the Backtester
The backtester pulls historical data directly from the Binance REST API to simulate past performance:
```bash
go run cmd/backtester/main.go --symbol BTCUSDT --limit 1000 --interval 1m
```

### Testing
The project includes a comprehensive test suite for verifying engine integrity and risk logic:
```bash
go test -v ./...
```

## Configuration

The system is configured via environment variables. Key parameters include:

| Variable | Description |
| :--- | :--- |
| WATCHLIST | Comma-separated list of symbols (e.g., BTCUSDT,ETHUSDT) |
| TP | Take-Profit threshold in decimal (0.01 = 1%) |
| SL | Stop-Loss threshold in decimal (0.005 = 0.5%) |
| SHORT_PERIOD | EMA short-term period |
| LONG_PERIOD | EMA long-term period |
| RSI_PERIOD | RSI calculation period |

## Deployment

The system is fully containerized. To build and run via Docker:
```bash
docker build -t kryptic-gopha .
docker run --env-file .env kryptic-gopha
```

## CI/CD Pipeline

The project utilizes GitHub Actions for continuous integration. Every push to the main branch triggers:
1. Workspace linting and code integrity checks.
2. Automated builds for the bot and backtester binaries.
3. Execution of the full unit and integration test suite.

## Disclaimer

This software is provided for educational and research purposes. Digital asset trading involves significant risk. The developers are not responsible for financial losses incurred through the use of this software.
