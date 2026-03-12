// DataTable — Client-side pagination and filtering for data lists.
// Works with any data array and render function. No third-party dependencies.

class DataTable {
    constructor(options) {
        this.containerId = options.containerId;
        this.data = [];
        this.filteredData = [];
        this.page = 1;
        this.pageSize = parseInt(localStorage.getItem('dt_pageSize_' + this.containerId) || options.pageSize || 10, 10);
        this.renderItem = options.renderItem;       // (item, index) => HTML string
        this.renderHeader = options.renderHeader;   // optional () => HTML string for table header
        this.renderFooter = options.renderFooter;   // optional () => HTML string for table footer (e.g. closing tags)
        this.columns = options.columns || [];       // [{key, label, filterable}] for dropdown filters
        this.searchFields = options.searchFields || []; // keys to search across
        this.onRender = options.onRender || null;   // callback after DOM update
        this.emptyMessage = options.emptyMessage || 'No data.';
        this.searchQuery = '';
        this.columnFilters = {};
        this.debounceTimer = null;
    }

    setData(data) {
        this.data = data || [];
        this.applyFilters();
    }

    applyFilters() {
        let result = this.data;

        // Global text search
        if (this.searchQuery) {
            const q = this.searchQuery.toLowerCase();
            result = result.filter(item => {
                if (this.searchFields.length > 0) {
                    return this.searchFields.some(key => {
                        const val = this._getNestedValue(item, key);
                        return val != null && String(val).toLowerCase().includes(q);
                    });
                }
                // Fallback: search all string values
                return Object.values(item).some(v => v != null && String(v).toLowerCase().includes(q));
            });
        }

        // Column-specific dropdown filters
        for (const [key, val] of Object.entries(this.columnFilters)) {
            if (val) {
                result = result.filter(item => String(this._getNestedValue(item, key)) === val);
            }
        }

        this.filteredData = result;

        // Adjust page if out of bounds
        const maxPage = Math.max(1, Math.ceil(this.filteredData.length / this.pageSize));
        if (this.page > maxPage) this.page = maxPage;

        this.render();
    }

    render() {
        const container = document.getElementById(this.containerId);
        if (!container) return;

        const total = this.filteredData.length;
        const maxPage = Math.max(1, Math.ceil(total / this.pageSize));
        const start = (this.page - 1) * this.pageSize;
        const end = Math.min(start + this.pageSize, total);
        const pageData = this.filteredData.slice(start, end);

        let html = '';

        // Controls bar
        html += '<div class="data-table-controls">';
        html += '<div class="data-table-controls-filters">';

        // Search input
        html += '<div class="data-table-control-field data-table-control-search">';
        html += '<input type="text" class="form-input data-table-search" placeholder="Search..." ' +
            'value="' + this._esc(this.searchQuery) + '" ' +
            'oninput="window._dt[\'' + this.containerId + '\'].onSearch(this.value)">';
        html += '</div>';

        // Column dropdown filters
        for (const col of this.columns) {
            if (!col.filterable) continue;
            const uniqueVals = [...new Set(this.data.map(item => String(this._getNestedValue(item, col.key) || '')))].filter(v => v).sort();
            const currentVal = this.columnFilters[col.key] || '';
            html += '<div class="data-table-control-field">';
            html += '<select class="form-input data-table-filter" ' +
                'onchange="window._dt[\'' + this.containerId + '\'].onColumnFilter(\'' + col.key + '\',this.value)">' +
                '<option value="">All ' + this._esc(col.label) + '</option>';
            for (const v of uniqueVals) {
                html += '<option value="' + this._esc(v) + '"' + (v === currentVal ? ' selected' : '') + '>' + this._esc(v) + '</option>';
            }
            html += '</select>';
            html += '</div>';
        }

        // Clear filters
        if (this.searchQuery || Object.values(this.columnFilters).some(v => v)) {
            html += '<button class="btn btn-secondary btn-sm data-table-clear" onclick="window._dt[\'' + this.containerId + '\'].clearFilters()">Clear</button>';
        }
        html += '</div>';

        // Page size selector (right side)
        html += '<div class="data-table-controls-meta">';
        html += '<label class="data-table-page-size">';
        html += '<span>Show</span><select class="form-input data-table-page-size-select" ' +
            'onchange="window._dt[\'' + this.containerId + '\'].onPageSize(this.value)">';
        for (const ps of [10, 25, 50, 100]) {
            html += '<option value="' + ps + '"' + (ps === this.pageSize ? ' selected' : '') + '>' + ps + '</option>';
        }
        html += '</select></label>';
        html += '</div>';
        html += '</div>';

        // Content
        if (total === 0) {
            html += '<div style="text-align:center;padding:24px;color:var(--text-muted);">' + this._esc(this.emptyMessage) + '</div>';
        } else {
            if (this.renderHeader) {
                html += this.renderHeader();
            }
            html += pageData.map((item, i) => this.renderItem(item, start + i)).join('');
            if (this.renderFooter) {
                html += this.renderFooter();
            }
        }

        // Pagination footer
        if (total > this.pageSize) {
            html += '<div class="data-table-pagination">';
            html += '<span class="data-table-pagination-info">Showing ' + (start + 1) + '-' + end + ' of ' + total + '</span>';
            html += '<div class="data-table-pagination-actions">';
            html += '<button class="btn btn-secondary btn-sm"' + (this.page <= 1 ? ' disabled' : '') +
                ' onclick="window._dt[\'' + this.containerId + '\'].goPage(' + (this.page - 1) + ')">Prev</button>';

            // Page numbers (show max 5)
            const pageStart = Math.max(1, this.page - 2);
            const pageEnd = Math.min(maxPage, pageStart + 4);
            for (let p = pageStart; p <= pageEnd; p++) {
                const activeClass = p === this.page ? ' data-table-page-active' : '';
                html += '<button class="btn btn-secondary btn-sm data-table-page-button' + activeClass + '" ' +
                    'onclick="window._dt[\'' + this.containerId + '\'].goPage(' + p + ')">' + p + '</button>';
            }

            html += '<button class="btn btn-secondary btn-sm"' + (this.page >= maxPage ? ' disabled' : '') +
                ' onclick="window._dt[\'' + this.containerId + '\'].goPage(' + (this.page + 1) + ')">Next</button>';
            html += '</div></div>';
        } else if (total > 0) {
            html += '<div class="data-table-count">Showing ' + total + ' entries</div>';
        }

        container.innerHTML = html;

        if (this.onRender) {
            this.onRender(pageData);
        }
    }

    onSearch(query) {
        clearTimeout(this.debounceTimer);
        this.debounceTimer = setTimeout(() => {
            this.searchQuery = query;
            this.page = 1;
            this.applyFilters();
        }, 200);
    }

    onColumnFilter(key, value) {
        this.columnFilters[key] = value;
        this.page = 1;
        this.applyFilters();
    }

    onPageSize(size) {
        this.pageSize = parseInt(size, 10);
        localStorage.setItem('dt_pageSize_' + this.containerId, this.pageSize);
        this.page = 1;
        this.applyFilters();
    }

    goPage(page) {
        const maxPage = Math.max(1, Math.ceil(this.filteredData.length / this.pageSize));
        this.page = Math.max(1, Math.min(page, maxPage));
        this.render();
    }

    clearFilters() {
        this.searchQuery = '';
        this.columnFilters = {};
        this.page = 1;
        this.applyFilters();
    }

    _getNestedValue(obj, key) {
        return key.split('.').reduce((o, k) => o && o[k], obj);
    }

    _esc(s) {
        const d = document.createElement('div');
        d.textContent = s || '';
        return d.innerHTML;
    }
}

// Global registry for event handlers (onclick in innerHTML)
if (!window._dt) window._dt = {};
