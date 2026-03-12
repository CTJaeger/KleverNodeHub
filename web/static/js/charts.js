/**
 * Klever Node Hub — Lightweight Charts Module
 * SVG gauges + Canvas time-series charts, no external dependencies.
 */

const Charts = {
    /**
     * Render a ring gauge (SVG) inside a container.
     * @param {HTMLElement} el — container element
     * @param {number} percent — 0..100
     * @param {object} opts — { label, size, strokeWidth, color }
     */
    gauge(el, percent, opts = {}) {
        const size = opts.size || 80;
        const stroke = opts.strokeWidth || 6;
        const r = (size - stroke) / 2;
        const circ = 2 * Math.PI * r;
        const pct = Math.max(0, Math.min(100, percent || 0));
        const offset = circ - (pct / 100) * circ;
        const color = opts.color || Charts._gaugeColor(pct);
        const label = opts.label || '';

        el.innerHTML = `
            <svg width="${size}" height="${size}" viewBox="0 0 ${size} ${size}" class="gauge-svg">
                <circle cx="${size/2}" cy="${size/2}" r="${r}"
                    fill="none" stroke="rgba(255,255,255,0.08)" stroke-width="${stroke}" />
                <circle cx="${size/2}" cy="${size/2}" r="${r}"
                    fill="none" stroke="${color}" stroke-width="${stroke}"
                    stroke-dasharray="${circ}" stroke-dashoffset="${offset}"
                    stroke-linecap="round"
                    transform="rotate(-90 ${size/2} ${size/2})"
                    style="transition:stroke-dashoffset 0.5s ease" />
                <text x="${size/2}" y="${size/2}" text-anchor="middle" dy="0.35em"
                    fill="var(--text-primary)" font-size="${size * 0.22}px" font-weight="700">
                    ${pct.toFixed(0)}%
                </text>
            </svg>
            ${label ? `<div class="gauge-label">${label}</div>` : ''}`;
    },

    _gaugeColor(pct) {
        if (pct < 60) return 'var(--accent)';
        if (pct < 85) return 'var(--warning)';
        return 'var(--error)';
    },

    /**
     * Render a sparkline (small inline chart) in a canvas.
     * @param {HTMLCanvasElement} canvas
     * @param {number[]} values
     * @param {object} opts — { color, fillColor }
     */
    sparkline(canvas, values, opts = {}) {
        if (!values || values.length < 2) return;
        const ctx = canvas.getContext('2d');
        const dpr = window.devicePixelRatio || 1;
        const w = canvas.clientWidth;
        const h = canvas.clientHeight;
        canvas.width = w * dpr;
        canvas.height = h * dpr;
        ctx.scale(dpr, dpr);

        const color = opts.color || '#e8a737';
        const min = Math.min(...values);
        const max = Math.max(...values);
        const range = max - min || 1;
        const step = w / (values.length - 1);

        ctx.clearRect(0, 0, w, h);
        ctx.beginPath();
        values.forEach((v, i) => {
            const x = i * step;
            const y = h - ((v - min) / range) * (h - 4) - 2;
            if (i === 0) ctx.moveTo(x, y);
            else ctx.lineTo(x, y);
        });
        ctx.strokeStyle = color;
        ctx.lineWidth = 1.5;
        ctx.stroke();

        // Fill
        if (opts.fill !== false) {
            ctx.lineTo(w, h);
            ctx.lineTo(0, h);
            ctx.closePath();
            ctx.fillStyle = color.replace(')', ',0.1)').replace('rgb', 'rgba');
            ctx.fill();
        }
    },

    /**
     * Render a time-series line chart in a canvas.
     * @param {HTMLCanvasElement} canvas
     * @param {object} data — { labels: number[], datasets: [{ values, label, color }] }
     * @param {object} opts — { yLabel, yFormat, xFormat }
     */
    timeSeries(canvas, data, opts = {}) {
        if (!data || !data.datasets || data.datasets.length === 0) return;

        const ctx = canvas.getContext('2d');
        const dpr = window.devicePixelRatio || 1;
        const w = canvas.clientWidth;
        const h = canvas.clientHeight;
        canvas.width = w * dpr;
        canvas.height = h * dpr;
        ctx.scale(dpr, dpr);

        const pad = { top: 10, right: 16, bottom: 30, left: 55 };
        const chartW = w - pad.left - pad.right;
        const chartH = h - pad.top - pad.bottom;

        ctx.clearRect(0, 0, w, h);

        // Calculate Y range across all datasets
        let yMin = Infinity, yMax = -Infinity;
        data.datasets.forEach(ds => {
            ds.values.forEach(v => {
                if (v < yMin) yMin = v;
                if (v > yMax) yMax = v;
            });
        });
        if (yMin === yMax) { yMin -= 1; yMax += 1; }
        const yRange = yMax - yMin;
        yMin -= yRange * 0.05;
        yMax += yRange * 0.05;

        // X range
        const labels = data.labels;
        const xMin = labels[0];
        const xMax = labels[labels.length - 1];
        const xRange = xMax - xMin || 1;

        // Grid lines + Y labels
        ctx.strokeStyle = 'rgba(255,255,255,0.06)';
        ctx.lineWidth = 1;
        ctx.fillStyle = 'var(--text-secondary)';
        ctx.font = '11px monospace';
        ctx.textAlign = 'right';

        const yTicks = 5;
        for (let i = 0; i <= yTicks; i++) {
            const y = pad.top + chartH - (i / yTicks) * chartH;
            const val = yMin + (i / yTicks) * (yMax - yMin);
            ctx.beginPath();
            ctx.moveTo(pad.left, y);
            ctx.lineTo(w - pad.right, y);
            ctx.stroke();
            const formatted = opts.yFormat ? opts.yFormat(val) : Charts._formatNumber(val);
            ctx.fillText(formatted, pad.left - 6, y + 4);
        }

        // X labels
        ctx.textAlign = 'center';
        const xTicks = Math.min(6, labels.length);
        const xStep = Math.floor(labels.length / xTicks);
        for (let i = 0; i < labels.length; i += xStep) {
            const x = pad.left + ((labels[i] - xMin) / xRange) * chartW;
            const y = h - pad.bottom + 16;
            const formatted = opts.xFormat ? opts.xFormat(labels[i]) : Charts._formatTime(labels[i]);
            ctx.fillText(formatted, x, y);
        }

        // Draw datasets
        data.datasets.forEach(ds => {
            ctx.beginPath();
            ctx.strokeStyle = ds.color || '#e8a737';
            ctx.lineWidth = 2;

            labels.forEach((t, i) => {
                const x = pad.left + ((t - xMin) / xRange) * chartW;
                const y = pad.top + chartH - ((ds.values[i] - yMin) / (yMax - yMin)) * chartH;
                if (i === 0) ctx.moveTo(x, y);
                else ctx.lineTo(x, y);
            });
            ctx.stroke();

            // Area fill
            const lastX = pad.left + ((labels[labels.length-1] - xMin) / xRange) * chartW;
            const firstX = pad.left;
            ctx.lineTo(lastX, pad.top + chartH);
            ctx.lineTo(firstX, pad.top + chartH);
            ctx.closePath();
            ctx.fillStyle = (ds.color || '#e8a737').replace(')', ',0.08)').replace('rgb', 'rgba');
            if (ds.color && ds.color.startsWith('#')) {
                ctx.fillStyle = ds.color + '14';
            }
            ctx.fill();
        });

        // Y axis label
        if (opts.yLabel) {
            ctx.save();
            ctx.translate(12, pad.top + chartH / 2);
            ctx.rotate(-Math.PI / 2);
            ctx.fillStyle = 'var(--text-secondary)';
            ctx.font = '11px sans-serif';
            ctx.textAlign = 'center';
            ctx.fillText(opts.yLabel, 0, 0);
            ctx.restore();
        }

        // Legend
        if (data.datasets.length > 1) {
            let lx = pad.left;
            const ly = h - 4;
            ctx.font = '11px sans-serif';
            data.datasets.forEach(ds => {
                ctx.fillStyle = ds.color || '#e8a737';
                ctx.fillRect(lx, ly - 8, 12, 3);
                ctx.fillStyle = 'var(--text-secondary)';
                ctx.textAlign = 'left';
                ctx.fillText(ds.label || '', lx + 16, ly - 3);
                lx += ctx.measureText(ds.label || '').width + 32;
            });
        }
    },

    _formatNumber(v) {
        if (Math.abs(v) >= 1e9) return (v / 1e9).toFixed(1) + 'G';
        if (Math.abs(v) >= 1e6) return (v / 1e6).toFixed(1) + 'M';
        if (Math.abs(v) >= 1e3) return (v / 1e3).toFixed(1) + 'K';
        return v.toFixed(v % 1 === 0 ? 0 : 1);
    },

    _formatTime(ts) {
        const d = new Date(ts * 1000);
        return d.getHours().toString().padStart(2, '0') + ':' + d.getMinutes().toString().padStart(2, '0');
    },

    _formatDateTime(ts) {
        const d = new Date(ts * 1000);
        return d.toLocaleDateString(undefined, {month:'short', day:'numeric'}) + ' ' +
            d.getHours().toString().padStart(2, '0') + ':' + d.getMinutes().toString().padStart(2, '0');
    },

    /**
     * Format bytes to human-readable.
     */
    formatBytes(b) {
        if (b >= 1e12) return (b / 1e12).toFixed(1) + ' TB';
        if (b >= 1e9) return (b / 1e9).toFixed(1) + ' GB';
        if (b >= 1e6) return (b / 1e6).toFixed(1) + ' MB';
        if (b >= 1e3) return (b / 1e3).toFixed(1) + ' KB';
        return b + ' B';
    },

    formatBps(bps) {
        if (bps >= 1e6) return (bps / 1e6).toFixed(1) + ' Mbps';
        if (bps >= 1e3) return (bps / 1e3).toFixed(1) + ' Kbps';
        return bps + ' bps';
    }
};
