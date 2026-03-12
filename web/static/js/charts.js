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
        const size = opts.size || 102;
        const stroke = opts.strokeWidth || 12;
        const r = (size - stroke) / 2;
        const circ = 2 * Math.PI * r;
        const pct = Math.max(0, Math.min(100, percent || 0));
        const offset = circ - (pct / 100) * circ;
        const color = opts.color || Charts._gaugeColor(pct);
        const label = opts.label || '';

        el.innerHTML = `
            <svg width="${size}" height="${size}" viewBox="0 0 ${size} ${size}" class="gauge-svg">
                <defs>
                    <filter id="gaugeGlow" x="-50%" y="-50%" width="200%" height="200%">
                        <feGaussianBlur stdDeviation="2.4" result="blur"></feGaussianBlur>
                        <feMerge>
                            <feMergeNode in="blur"></feMergeNode>
                            <feMergeNode in="SourceGraphic"></feMergeNode>
                        </feMerge>
                    </filter>
                </defs>
                <circle cx="${size/2}" cy="${size/2}" r="${r}"
                    fill="none" stroke="rgba(255,255,255,0.07)" stroke-width="${stroke}" />
                <circle cx="${size/2}" cy="${size/2}" r="${r - stroke * 0.52}"
                    fill="rgba(5,10,19,0.9)" stroke="rgba(255,255,255,0.03)" stroke-width="1" />
                <circle cx="${size/2}" cy="${size/2}" r="${r}"
                    fill="none" stroke="${color}" stroke-width="${stroke}"
                    stroke-dasharray="${circ}" stroke-dashoffset="${offset}"
                    stroke-linecap="round"
                    transform="rotate(-90 ${size/2} ${size/2})"
                    filter="url(#gaugeGlow)"
                    style="transition:stroke-dashoffset 0.5s ease" />
                <text x="${size/2}" y="${size/2}" text-anchor="middle" dy="0.15em"
                    fill="var(--text-heading)" font-size="${size * 0.2}px" font-weight="800">
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

        const colors = {
            grid: Charts._cssVar('--border', 'rgba(123,146,191,0.18)'),
            gridSoft: Charts._cssVar('--border-soft', 'rgba(123,146,191,0.08)'),
            text: Charts._cssVar('--text-secondary', '#8d98b1'),
            textStrong: Charts._cssVar('--text-heading', '#eef3ff'),
            plot: Charts._cssVar('--bg-input', 'rgba(8,17,31,0.84)')
        };

        const pad = { top: 14, right: 12, bottom: 30, left: 44 };
        const chartW = w - pad.left - pad.right;
        const chartH = h - pad.top - pad.bottom;

        ctx.clearRect(0, 0, w, h);
        ctx.fillStyle = colors.plot;
        ctx.fillRect(pad.left, pad.top, chartW, chartH);

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
        ctx.strokeStyle = colors.grid;
        ctx.lineWidth = 1;
        ctx.fillStyle = colors.text;
        ctx.font = '11px "JetBrains Mono", monospace';
        ctx.textAlign = 'right';

        const yTicks = 4;
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

        // X labels + vertical grid
        ctx.textAlign = 'center';
        const xTicks = Math.min(6, labels.length);
        const tickIndexes = [];
        for (let i = 0; i < xTicks; i++) {
            tickIndexes.push(Math.round((i / Math.max(1, xTicks - 1)) * (labels.length - 1)));
        }
        tickIndexes.forEach((idx, pos) => {
            const x = pad.left + ((labels[idx] - xMin) / xRange) * chartW;
            if (pos > 0 && pos < tickIndexes.length - 1) {
                ctx.beginPath();
                ctx.strokeStyle = colors.gridSoft;
                ctx.moveTo(x, pad.top);
                ctx.lineTo(x, pad.top + chartH);
                ctx.stroke();
                ctx.strokeStyle = colors.grid;
            }
            const y = h - pad.bottom + 16;
            const formatted = opts.xFormat ? opts.xFormat(labels[idx]) : Charts._formatTime(labels[idx]);
            ctx.fillText(formatted, x, y);
        });

        // Plot border
        ctx.strokeStyle = colors.gridSoft;
        ctx.strokeRect(pad.left, pad.top, chartW, chartH);

        // Draw datasets
        data.datasets.forEach(ds => {
            const points = labels.map((t, i) => ({
                x: pad.left + ((t - xMin) / xRange) * chartW,
                y: pad.top + chartH - ((ds.values[i] - yMin) / (yMax - yMin)) * chartH
            }));

            if (points.length < 2) return;

            ctx.beginPath();
            ctx.lineCap = 'round';
            ctx.lineJoin = 'round';
            ctx.strokeStyle = ds.color || '#e8a737';
            ctx.lineWidth = 2;

            points.forEach((point, i) => {
                if (i === 0) ctx.moveTo(point.x, point.y);
                else ctx.lineTo(point.x, point.y);
            });
            ctx.stroke();

            const fill = ctx.createLinearGradient(0, pad.top, 0, pad.top + chartH);
            fill.addColorStop(0, Charts._withAlpha(ds.color || '#e8a737', 0.24));
            fill.addColorStop(1, Charts._withAlpha(ds.color || '#e8a737', 0.02));
            ctx.lineTo(points[points.length - 1].x, pad.top + chartH);
            ctx.lineTo(points[0].x, pad.top + chartH);
            ctx.closePath();
            ctx.fillStyle = fill;
            ctx.fill();

            ctx.beginPath();
            ctx.strokeStyle = ds.color || '#e8a737';
            ctx.lineWidth = 2;
            points.forEach((point, i) => {
                if (i === 0) ctx.moveTo(point.x, point.y);
                else ctx.lineTo(point.x, point.y);
            });
            ctx.stroke();

            const last = points[points.length - 1];
            ctx.beginPath();
            ctx.fillStyle = colors.textStrong;
            ctx.arc(last.x, last.y, 2.6, 0, Math.PI * 2);
            ctx.fill();

            ctx.beginPath();
            ctx.fillStyle = Charts._withAlpha(ds.color || '#e8a737', 0.22);
            ctx.arc(last.x, last.y, 6.5, 0, Math.PI * 2);
            ctx.fill();
        });

        // Y axis label
        if (opts.yLabel) {
            ctx.save();
            ctx.translate(14, pad.top + chartH / 2);
            ctx.rotate(-Math.PI / 2);
            ctx.fillStyle = colors.text;
            ctx.font = '11px Manrope, sans-serif';
            ctx.textAlign = 'center';
            ctx.fillText(opts.yLabel, 0, 0);
            ctx.restore();
        }

        // Legend
        if (data.datasets.length > 1) {
            let lx = pad.left;
            const ly = h - 4;
            ctx.font = '11px Manrope, sans-serif';
            data.datasets.forEach(ds => {
                ctx.fillStyle = ds.color || '#e8a737';
                ctx.fillRect(lx, ly - 8, 12, 3);
                ctx.fillStyle = colors.text;
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

    _cssVar(name, fallback) {
        const value = getComputedStyle(document.documentElement).getPropertyValue(name).trim();
        return value || fallback;
    },

    _withAlpha(color, alpha) {
        if (!color) return `rgba(255,255,255,${alpha})`;
        if (color.startsWith('#')) {
            let hex = color.slice(1);
            if (hex.length === 3) hex = hex.split('').map((ch) => ch + ch).join('');
            const int = parseInt(hex, 16);
            const r = (int >> 16) & 255;
            const g = (int >> 8) & 255;
            const b = int & 255;
            return `rgba(${r}, ${g}, ${b}, ${alpha})`;
        }
        if (color.startsWith('rgb(')) {
            return color.replace('rgb(', 'rgba(').replace(')', `, ${alpha})`);
        }
        if (color.startsWith('rgba(')) {
            const parts = color.slice(5, -1).split(',').map((part) => part.trim());
            if (parts.length >= 3) {
                return `rgba(${parts[0]}, ${parts[1]}, ${parts[2]}, ${alpha})`;
            }
        }
        return color;
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
