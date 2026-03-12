const state = {
    symbol: 'BTCUSDT',
    chart: null,
    candleSeries: null,
    tradeMarkers: [],
};

// Formatting helpers
const formatCurrency = (val) => new Intl.NumberFormat('en-US', { style: 'currency', currency: 'USD' }).format(val);
const formatPct = (val) => `${parseFloat(val).toFixed(2)}%`;

async function init() {
    initChart();
    await updateState();
    await updateTrades();
    await loadChartData(state.symbol);

    // Poll every 5 seconds
    setInterval(async () => {
        await updateState();
        await updateTrades();
        await loadChartData(state.symbol);
    }, 5000);
}

function initChart() {
    const container = document.getElementById('chart');
    state.chart = LightweightCharts.createChart(container, {
        layout: {
            background: { color: '#0f111a' },
            textColor: '#94a3b8',
        },
        grid: {
            vertLines: { color: 'rgba(45, 212, 191, 0.05)' },
            horzLines: { color: 'rgba(45, 212, 191, 0.05)' },
        },
        crosshair: {
            mode: LightweightCharts.CrosshairMode.Normal,
        },
        timeScale: {
            timeVisible: true,
            secondsVisible: false,
        },
    });

    state.candleSeries = state.chart.addCandlestickSeries({
        upColor: '#10b981',
        downColor: '#ef4444',
        borderVisible: false,
        wickUpColor: '#10b981',
        wickDownColor: '#ef4444',
    });

    window.addEventListener('resize', () => {
        state.chart.applyOptions({ width: container.clientWidth, height: container.clientHeight });
    });
}

async function updateState() {
    try {
        const res = await fetch('/api/state');
        const data = await res.json();
        
        document.getElementById('val-balance').textContent = formatCurrency(data.balance);
        document.getElementById('val-daily-pnl').textContent = `Daily PnL: ${formatCurrency(data.daily_pnl)}`;
        
        const winEl = document.getElementById('val-winrate');
        const total = data.total_wins + data.total_losses;
        const rate = total > 0 ? (data.total_wins / total) * 100 : 0;
        winEl.textContent = formatPct(rate);
        winEl.className = `metric-value ${rate >= 50 ? 'positive' : 'negative'}`;

        const activeCount = Object.values(data.active_trades).reduce((acc, curr) => acc + curr.length, 0);
        document.getElementById('val-active').textContent = activeCount;

        const statusEl = document.getElementById('val-status');
        if (data.trading_enabled) {
            statusEl.textContent = 'ACTIVE';
            statusEl.className = 'metric-value positive';
        } else {
            statusEl.textContent = 'SUSPENDED';
            statusEl.className = 'metric-value negative';
        }

        // Build symbol selector
        const symbols = Object.keys(data.active_trades).length > 0 ? 
            Object.keys(data.active_trades) : ['BTCUSDT', 'ETHUSDT', 'SOLUSDT', 'BNBUSDT']; // Fallback

        const badgeContainer = document.getElementById('symbol-badges');
        if (badgeContainer.children.length === 0) {
            symbols.forEach(sym => {
                const el = document.createElement('div');
                el.className = `symbol-badge ${sym === state.symbol ? 'active' : ''}`;
                el.textContent = sym;
                el.onclick = () => {
                    document.querySelectorAll('.symbol-badge').forEach(b => b.classList.remove('active'));
                    el.classList.add('active');
                    state.symbol = sym;
                    loadChartData(sym);
                };
                badgeContainer.appendChild(el);
            });
        }
    } catch (e) {
        console.error('Failed to update state:', e);
    }
}

async function updateTrades() {
    try {
        const res = await fetch('/api/trades');
        const data = await res.json();
        
        const list = document.getElementById('trades-list');
        list.innerHTML = ''; // Clear

        // Extract and flatten all trades, sort by Time desc
        let allTrades = [];
        Object.values(data.active || {}).forEach(arr => allTrades.push(...arr));
        allTrades.push(...(data.completed || []));
        
        allTrades.sort((a, b) => new Date(b.time) - new Date(a.time));

        // Create markers for the chart
        state.tradeMarkers = [];

        allTrades.slice(0, 50).forEach(t => {
            const isWin = t.status === 'WIN';
            const isLoss = t.status === 'LOSS';
            const isActive = t.status === 'ACTIVE';

            // DOM Element
            const el = document.createElement('div');
            el.className = `trade-item ${isWin ? 'win' : isLoss ? 'loss' : 'active'}`;
            
            const pnlClass = isWin ? 'positive' : isLoss ? 'negative' : '';
            const pnlText = isActive ? 'Open' : formatCurrency(t.pnl);

            el.innerHTML = `
                <div class="trade-header">
                    <span class="trade-symbol">${t.symbol}</span>
                    <span class="trade-dir ${t.direction.toLowerCase()}">${t.direction}</span>
                </div>
                <div class="trade-details">
                    <span>${parseFloat(t.entry_price).toFixed(2)} &rarr; ${isActive ? '-' : parseFloat(t.exit_price).toFixed(2)}</span>
                    <span class="trade-pnl ${pnlClass}">${pnlText}</span>
                </div>
            `;
            list.appendChild(el);

            // Chart Marker
            if (t.symbol === state.symbol) {
                // Entry Marker
                state.tradeMarkers.push({
                    time: new Date(t.time).getTime() / 1000,
                    position: t.direction === 'BUY' ? 'belowBar' : 'aboveBar',
                    color: t.direction === 'BUY' ? '#3b82f6' : '#ef4444',
                    shape: t.direction === 'BUY' ? 'arrowUp' : 'arrowDown',
                    text: `${t.direction} @ ${parseFloat(t.entry_price).toFixed(2)}`
                });

                // Exit Marker (if closed)
                if (!isActive) {
                    state.tradeMarkers.push({
                        time: new Date(t.time).getTime() / 1000 + 60, // approximate exit time for viz
                        position: 'inBar',
                        color: isWin ? '#10b981' : '#ef4444',
                        shape: 'circle',
                        text: `${t.status} (${formatCurrency(t.pnl)})`
                    });
                }
            }
        });

    } catch (e) {
        console.error('Failed to update trades:', e);
    }
}

async function loadChartData(symbol) {
    try {
        const res = await fetch(`/api/candles?symbol=${symbol}`);
        const data = await res.json();
        
        if (!data || data.length === 0) return;

        // Process for Lightweight Charts
        const chartData = data
            .filter(d => d.open !== "0") // Filter out empty buffer slots
            .map(d => ({
                time: d.timestamp / 1000,
                open: parseFloat(d.open),
                high: parseFloat(d.high),
                low: parseFloat(d.low),
                close: parseFloat(d.close),
            }))
            .sort((a, b) => a.time - b.time);

        state.candleSeries.setData(chartData);

        // Sort markers by time
        if (state.tradeMarkers.length > 0) {
            state.tradeMarkers.sort((a, b) => a.time - b.time);
            state.candleSeries.setMarkers(state.tradeMarkers);
        }

        const currentPrice = chartData[chartData.length - 1].close;
        document.getElementById('chart-legend').textContent = `${symbol} • ${formatCurrency(currentPrice)}`;

    } catch (e) {
        console.error('Failed to load chart data:', e);
    }
}

document.addEventListener('DOMContentLoaded', init);
