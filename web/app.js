const state = {
    symbol: 'BTCUSDT',
    chart: null,
    candleSeries: null,
    tradeMarkers: [],
};

// Formatting helpers
const formatCurrency = (val) => new Intl.NumberFormat('en-US', { style: 'currency', currency: 'USD' }).format(val);
const formatPct = (val) => `${parseFloat(val).toFixed(2)}%`;

// Helper to align timestamps to minute boundary for charting/markers
const snapToMinute = (ts) => Math.floor(ts / 60) * 60;

async function init() {
    initChart();
    
    // Legend Toggle Logic
    const btnLegend = document.getElementById('btn-legend');
    const modalLegend = document.getElementById('legend-modal');
    const closeLegend = document.getElementById('close-legend');

    if (btnLegend && modalLegend) {
        btnLegend.onclick = () => {
            modalLegend.style.display = modalLegend.style.display === 'none' ? 'block' : 'none';
        };
    }
    if (closeLegend && modalLegend) {
        closeLegend.onclick = () => {
            modalLegend.style.display = 'none';
        };
    }

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
    if (!container) return;
    
    if (typeof LightweightCharts === 'undefined') {
        console.error('LightweightCharts library not loaded');
        return;
    }

    if (state.chart) return; 

    try {
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
    } catch (e) {
        console.error('Failed to initialize chart:', e);
    }
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

        // --- Build Multisymbol Summary Table ---
        const summaryBody = document.getElementById('summary-body');
        summaryBody.innerHTML = '';
        
        const allSymbols = ['BTCUSDT', 'ETHUSDT', 'SOLUSDT', 'BNBUSDT'];
        
        allSymbols.forEach(sym => {
            // Calculate stats for this symbol
            const completed = data.completed.filter(t => t.symbol === sym);
            const active = (data.active_trades[sym] || []).length;
            const wins = completed.filter(t => t.status === 'WIN').length;
            const losses = completed.filter(t => t.status === 'LOSS').length;
            const pnl = completed.reduce((acc, t) => acc + parseFloat(t.pnl), 0);
            const winRate = (wins + losses) > 0 ? (wins / (wins + losses) * 100).toFixed(1) : '0.0';

            const row = document.createElement('tr');
            row.innerHTML = `
                <td class="sym-col">${sym}</td>
                <td>${completed.length + active}</td>
                <td>${wins}</td>
                <td>${losses}</td>
                <td class="${winRate >= 50 ? 'positive' : 'negative'}">${winRate}%</td>
                <td class="${pnl >= 0 ? 'positive' : 'negative'}">${formatCurrency(pnl)}</td>
                <td><span class="status-active">${active > 0 ? 'TRADING' : 'IDLE'}</span></td>
            `;
            summaryBody.appendChild(row);
        });

        // Build symbol selector badges if first load
        const badgeContainer = document.getElementById('symbol-badges');
        if (badgeContainer.children.length === 0) {
            allSymbols.forEach(sym => {
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
        list.innerHTML = '';

        let allTrades = [];
        Object.values(data.active || {}).forEach(arr => allTrades.push(...arr));
        allTrades.push(...(data.completed || []));
        
        // Fix new Date() by ensuring we have a valid timestamp string or number
        allTrades.sort((a, b) => new Date(b.time).getTime() - new Date(a.time).getTime());

        // We'll collect all markers here
        state.tradeMarkers = [];

        // Add Recent Trades to sidebar
        allTrades.slice(0, 50).forEach(t => {
            const isWin = t.status === 'WIN';
            const isLoss = t.status === 'LOSS';
            const isActive = t.status === 'ACTIVE';

            const el = document.createElement('div');
            el.className = `trade-item ${isWin ? 'win' : isLoss ? 'loss' : 'active'}`;
            
            const pnlClass = isWin ? 'positive' : isLoss ? 'negative' : '';
            const pnlText = isActive ? 'Open' : formatCurrency(t.pnl);

            const tradeTime = new Date(t.time).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' });

            el.innerHTML = `
                <div class="trade-header">
                    <div>
                        <span class="trade-symbol">${t.symbol}</span>
                        <span class="trade-time">${tradeTime}</span>
                    </div>
                    <span class="trade-dir ${t.direction.toLowerCase()}">${t.direction}</span>
                </div>
                <div class="trade-details">
                    <span>${parseFloat(t.entry_price).toFixed(2)} &rarr; ${isActive ? '-' : parseFloat(t.exit_price).toFixed(2)}</span>
                    <span class="trade-pnl ${pnlClass}">${pnlText}</span>
                </div>
            `;
            list.appendChild(el);

            // Chart markers for trades only if they match current symbol
            if (t.symbol === state.symbol) {
                const markerTime = snapToMinute(new Date(t.time).getTime() / 1000);
                
                state.tradeMarkers.push({
                    time: markerTime,
                    position: t.direction === 'BUY' ? 'belowBar' : 'aboveBar',
                    color: t.direction === 'BUY' ? '#3b82f6' : '#ef4444',
                    shape: t.direction === 'BUY' ? 'arrowUp' : 'arrowDown',
                    text: `EXEC: ${t.direction}`
                });
            }
        });

    } catch (e) {
        console.error('Failed to update trades:', e);
    }
}

async function loadChartData(symbol) {
    try {
        // 1. Fetch Candles
        const candleRes = await fetch(`/api/candles?symbol=${symbol}`);
        const candleData = await candleRes.json();
        if (!candleData || candleData.length === 0) return;

        const chartData = candleData
            .filter(d => d.open !== "0")
            .map(d => ({
                time: snapToMinute(new Date(d.timestamp).getTime() / 1000),
                open: parseFloat(d.open),
                high: parseFloat(d.high),
                low: parseFloat(d.low),
                close: parseFloat(d.close),
            }))
            .sort((a, b) => a.time - b.time);

        if (chartData.length > 0) {
            // Set historical data once, then update the last candle
            state.candleSeries.setData(chartData.slice(0, -1));
            state.candleSeries.update(chartData[chartData.length - 1]);
        }

        // 2. Fetch Model Predictions (Signals)
        const signalRes = await fetch(`/api/signals?symbol=${symbol}`);
        const signals = await signalRes.json();

        // Create price lines for TP/SL targets
        if (state.priceLines) {
            state.priceLines.forEach(l => state.candleSeries.removePriceLine(l));
        }
        state.priceLines = [];

        // Add prediction markers & target lines
        const predictionMarkers = signals.map(s => {
            const time = snapToMinute(new Date(s.timestamp).getTime() / 1000);
            
            // Only add lines for recent/relevant signals to avoid clutter
            // For this demo, we add them for all visible signals
            if (s.tp && s.sl) {
                const tpLine = state.candleSeries.createPriceLine({
                    price: parseFloat(s.tp),
                    color: '#10b981',
                    lineWidth: 1,
                    lineStyle: LightweightCharts.LineStyle.Dashed,
                    axisLabelVisible: true,
                    title: `TP: ${s.direction}`,
                });
                const slLine = state.candleSeries.createPriceLine({
                    price: parseFloat(s.sl),
                    color: '#ef4444',
                    lineWidth: 1,
                    lineStyle: LightweightCharts.LineStyle.Dashed,
                    axisLabelVisible: true,
                    title: `SL: ${s.direction}`,
                });
                state.priceLines.push(tpLine, slLine);
            }

            return {
                time: time,
                position: 'inBar',
                color: 'rgba(255, 255, 255, 0.3)',
                shape: 'circle',
                text: `PREDICT: ${s.direction}`
            };
        });

        // Combine and set all markers
        const allMarkers = [...state.tradeMarkers, ...predictionMarkers]
            .sort((a, b) => a.time - b.time);

        state.candleSeries.setMarkers(allMarkers);

        const currentPrice = chartData[chartData.length - 1].close;
        document.getElementById('chart-legend').textContent = `${symbol} • ${formatCurrency(currentPrice)}`;
        document.getElementById('val-last-update').textContent = `Last Updated: ${new Date().toLocaleTimeString()}`;

    } catch (e) {
        console.error('Failed to load chart data:', e);
    }
}

document.addEventListener('DOMContentLoaded', init);
