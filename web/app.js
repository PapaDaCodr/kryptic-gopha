/**
 * Kryptic Gopha Dashboard
 * Single-symbol candlestick view with trade execution markers and signal overlays.
 */

const state = {
    symbol:       null,   // active symbol; set from first watchlist entry
    watchlist:    [],     // populated from /health on init
    chart:        null,
    candleSeries: null,
    priceLines:   [],     // active TP/SL price lines on the chart
    tradeMarkers: [],     // EXEC markers for the current symbol
};

// ─── Formatters ───────────────────────────────────────────────────────────────
const formatUSD    = v => new Intl.NumberFormat('en-US', { style: 'currency', currency: 'USD' }).format(v);
const formatPct    = v => `${parseFloat(v).toFixed(2)}%`;
const snapMinute   = ts => Math.floor(ts / 60) * 60;

// ─── Initialisation ───────────────────────────────────────────────────────────
async function init() {
    // 1. Fetch watchlist from backend before doing anything else.
    try {
        const health = await fetch('/health').then(r => r.json());
        state.watchlist = health.watchlist || ['BTCUSDT'];
    } catch {
        state.watchlist = ['BTCUSDT'];
    }

    state.symbol = state.watchlist[0];

    // 2. Build chart and symbol tabs.
    initChart();
    buildSymbolTabs();

    // 3. Legend toggle.
    document.getElementById('btn-legend').onclick = () => {
        const modal = document.getElementById('legend-modal');
        modal.style.display = modal.style.display === 'none' ? 'block' : 'none';
    };
    document.getElementById('close-legend').onclick = () => {
        document.getElementById('legend-modal').style.display = 'none';
    };

    // 4. First load.
    await refresh();

    // 5. Poll every 5 s.
    setInterval(refresh, 5000);
}

// ─── Chart setup ──────────────────────────────────────────────────────────────
function initChart() {
    const container = document.getElementById('chart');
    if (!container || typeof LightweightCharts === 'undefined' || state.chart) return;

    state.chart = LightweightCharts.createChart(container, {
        layout:    { background: { color: '#0f111a' }, textColor: '#94a3b8' },
        grid:      { vertLines: { color: 'rgba(45,212,191,0.04)' }, horzLines: { color: 'rgba(45,212,191,0.04)' } },
        crosshair: { mode: LightweightCharts.CrosshairMode.Normal },
        timeScale: { timeVisible: true, secondsVisible: false, borderColor: '#1e2235' },
        rightPriceScale: { borderColor: '#1e2235' },
    });

    state.candleSeries = state.chart.addCandlestickSeries({
        upColor: '#10b981', downColor: '#ef4444',
        borderVisible: false,
        wickUpColor: '#10b981', wickDownColor: '#ef4444',
    });

    // Responsive resize.
    const ro = new ResizeObserver(() => {
        state.chart.applyOptions({ width: container.clientWidth, height: container.clientHeight });
    });
    ro.observe(container);
}

// ─── Symbol tabs ──────────────────────────────────────────────────────────────
function buildSymbolTabs() {
    const container = document.getElementById('symbol-tabs');
    container.innerHTML = '';
    state.watchlist.forEach(sym => {
        const btn = document.createElement('button');
        btn.className = `tab-btn ${sym === state.symbol ? 'active' : ''}`;
        btn.textContent = sym.replace('USDT', '');
        btn.dataset.full = sym;
        btn.onclick = () => selectSymbol(sym);
        container.appendChild(btn);
    });
}

function selectSymbol(sym) {
    if (sym === state.symbol) return;
    state.symbol = sym;
    document.querySelectorAll('.tab-btn').forEach(b => {
        b.classList.toggle('active', b.dataset.full === sym);
    });
    // Clear stale price immediately while new data loads.
    document.getElementById('val-live-price').textContent = '—';
    refreshChart();
}

// ─── Full refresh (sidebar + chart) ───────────────────────────────────────────
async function refresh() {
    await Promise.all([refreshSidebar(), refreshChart()]);
}

// ─── Sidebar: metrics + trade list ────────────────────────────────────────────
async function refreshSidebar() {
    try {
        const [stateData, tradesData] = await Promise.all([
            fetch('/api/state').then(r => r.json()),
            fetch('/api/trades').then(r => r.json()),
        ]);

        // Metrics.
        document.getElementById('val-balance').textContent = formatUSD(stateData.balance);

        const dailyPnL = parseFloat(stateData.daily_pnl) || 0;
        const dailyEl = document.getElementById('val-daily-pnl');
        dailyEl.textContent = `Daily PnL: ${formatUSD(dailyPnL)}`;
        dailyEl.className = `metric-subtitle ${dailyPnL >= 0 ? 'positive' : 'negative'}`;

        const total = (stateData.total_wins || 0) + (stateData.total_losses || 0);
        const winRate = total > 0 ? (stateData.total_wins / total) * 100 : 0;
        const winEl = document.getElementById('val-winrate');
        winEl.textContent = formatPct(winRate);
        winEl.className = `metric-value ${winRate >= 50 ? 'positive' : 'negative'}`;

        const activeCount = Object.values(stateData.active_trades || {})
            .reduce((n, arr) => n + arr.length, 0);
        document.getElementById('val-active').textContent = activeCount;

        const statusEl = document.getElementById('val-status');
        const enabled = stateData.trading_enabled;
        statusEl.textContent = enabled ? 'ACTIVE' : 'SUSPENDED';
        statusEl.className = `metric-value ${enabled ? 'positive' : 'negative'}`;

        // Recent trades list.
        buildTradeList(tradesData);

        // Summary table.
        buildSummaryTable(stateData);

    } catch (e) {
        console.error('Sidebar refresh failed:', e);
    }
}

function buildTradeList(data) {
    const list = document.getElementById('trades-list');
    list.innerHTML = '';

    const active    = Object.values(data.active || {}).flat();
    const completed = data.completed || [];
    const all = [...active, ...completed]
        .sort((a, b) => new Date(b.time) - new Date(a.time))
        .slice(0, 40);

    // Rebuild trade markers for the active symbol while we're here.
    state.tradeMarkers = [];

    all.forEach(t => {
        const isWin    = t.status === 'WIN';
        const isLoss   = t.status === 'LOSS';
        const isActive = t.status === 'ACTIVE';

        // Sidebar item.
        const el = document.createElement('div');
        el.className = `trade-item ${isWin ? 'win' : isLoss ? 'loss' : 'active'}`;
        const pnlText = isActive ? 'Open' : formatUSD(t.pnl);
        const timeStr = new Date(t.time).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });

        el.innerHTML = `
            <div class="trade-header">
                <span class="trade-symbol">${t.symbol}</span>
                <span class="trade-dir ${t.direction.toLowerCase()}">${t.direction}</span>
            </div>
            <div class="trade-meta">
                <span class="trade-time">${timeStr}</span>
                <span class="trade-pnl ${isWin ? 'positive' : isLoss ? 'negative' : ''}">${pnlText}</span>
            </div>`;
        list.appendChild(el);

        // Collect EXEC markers for the current symbol.
        if (t.symbol === state.symbol) {
            state.tradeMarkers.push({
                time:     snapMinute(new Date(t.time).getTime() / 1000),
                position: t.direction === 'BUY' ? 'belowBar' : 'aboveBar',
                color:    t.direction === 'BUY' ? '#3b82f6' : '#f97316',
                shape:    t.direction === 'BUY' ? 'arrowUp' : 'arrowDown',
                text:     `EXEC ${t.direction}`,
            });
        }
    });
}

function buildSummaryTable(data) {
    const tbody = document.getElementById('summary-body');
    tbody.innerHTML = '';

    state.watchlist.forEach(sym => {
        const completed = (data.completed || []).filter(t => t.symbol === sym);
        const active    = (data.active_trades?.[sym] || []).length;
        const wins      = completed.filter(t => t.status === 'WIN').length;
        const losses    = completed.filter(t => t.status === 'LOSS').length;
        const pnl       = completed.reduce((s, t) => s + parseFloat(t.pnl || 0), 0);
        const wr        = (wins + losses) > 0 ? (wins / (wins + losses) * 100).toFixed(1) : '—';

        const tr = document.createElement('tr');
        tr.innerHTML = `
            <td class="sym-col">${sym}</td>
            <td>${completed.length + active}</td>
            <td class="positive">${wins}</td>
            <td class="negative">${losses}</td>
            <td class="${parseFloat(wr) >= 50 ? 'positive' : (wr === '—' ? '' : 'negative')}">${wr !== '—' ? wr + '%' : '—'}</td>
            <td class="${pnl >= 0 ? 'positive' : 'negative'}">${formatUSD(pnl)}</td>
            <td>${active > 0 ? '<span class="badge-trading">TRADING</span>' : '<span class="badge-idle">IDLE</span>'}</td>`;
        tbody.appendChild(tr);
    });
}

// ─── Chart: candles + markers ─────────────────────────────────────────────────
async function refreshChart() {
    if (!state.symbol || !state.candleSeries) return;
    try {
        const [candleData, signals] = await Promise.all([
            fetch(`/api/candles?symbol=${state.symbol}`).then(r => r.json()),
            fetch(`/api/signals?symbol=${state.symbol}`).then(r => r.json()),
        ]);

        if (!candleData?.length) return;

        // Build chart-ready candles.
        const chartData = candleData
            .filter(d => d.open !== '0' && d.open !== 0)
            .map(d => ({
                time:  snapMinute(new Date(d.timestamp).getTime() / 1000),
                open:  parseFloat(d.open),
                high:  parseFloat(d.high),
                low:   parseFloat(d.low),
                close: parseFloat(d.close),
            }))
            .sort((a, b) => a.time - b.time);

        if (chartData.length === 0) return;

        // Feed historical data then live-update the last (forming) candle.
        state.candleSeries.setData(chartData.slice(0, -1));
        state.candleSeries.update(chartData.at(-1));

        // ── TP/SL lines: only the most recent signal ─────────────────────────
        state.priceLines.forEach(l => { try { state.candleSeries.removePriceLine(l); } catch {} });
        state.priceLines = [];

        const latestSignal = signals?.at(-1);
        if (latestSignal?.tp && latestSignal?.sl) {
            const tp = parseFloat(latestSignal.tp);
            const sl = parseFloat(latestSignal.sl);
            if (tp > 0) state.priceLines.push(state.candleSeries.createPriceLine({
                price: tp, color: '#10b981', lineWidth: 1,
                lineStyle: LightweightCharts.LineStyle.Dashed,
                axisLabelVisible: true, title: `TP`,
            }));
            if (sl > 0) state.priceLines.push(state.candleSeries.createPriceLine({
                price: sl, color: '#ef4444', lineWidth: 1,
                lineStyle: LightweightCharts.LineStyle.Dashed,
                axisLabelVisible: true, title: `SL`,
            }));
        }

        // ── Prediction markers: last 20, subtle style ────────────────────────
        const recentSignals = (signals || []).slice(-20);
        const predMarkers = recentSignals.map(s => ({
            time:     snapMinute(new Date(s.timestamp).getTime() / 1000),
            position: s.direction === 'BUY' ? 'belowBar' : 'aboveBar',
            color:    'rgba(148, 163, 184, 0.25)',
            shape:    'circle',
            size:     0,
            text:     '',
        }));

        // Combine EXEC + prediction markers, dedup by time (EXEC takes priority).
        const execTimes = new Set(state.tradeMarkers.map(m => m.time));
        const filteredPred = predMarkers.filter(m => !execTimes.has(m.time));
        const allMarkers = [...filteredPred, ...state.tradeMarkers]
            .sort((a, b) => a.time - b.time);

        state.candleSeries.setMarkers(allMarkers);

        // ── Header info ───────────────────────────────────────────────────────
        const last = chartData.at(-1);
        const prev = chartData.at(-2);
        const change = prev ? ((last.close - prev.close) / prev.close) * 100 : 0;
        const sign   = change >= 0 ? '+' : '';

        document.getElementById('val-live-price').textContent = formatUSD(last.close);
        const chEl = document.getElementById('val-price-change');
        chEl.textContent = `${sign}${change.toFixed(2)}%`;
        chEl.className   = `price-change ${change >= 0 ? 'positive' : 'negative'}`;
        document.getElementById('val-last-update').textContent =
            `Updated ${new Date().toLocaleTimeString()}`;

    } catch (e) {
        console.error('Chart refresh failed:', e);
    }
}

document.addEventListener('DOMContentLoaded', init);
