// Klever Node Hub - API Client
// Fetch wrapper with JWT token handling and auto-refresh on 401.

const API = {
    accessToken: null,

    async request(method, path, body) {
        const opts = {
            method,
            headers: { 'Content-Type': 'application/json' },
            credentials: 'same-origin',
        };

        if (this.accessToken) {
            opts.headers['Authorization'] = 'Bearer ' + this.accessToken;
        }

        if (body) {
            opts.body = JSON.stringify(body);
        }

        let resp = await fetch(path, opts);

        // Auto-refresh on 401
        if (resp.status === 401 && path !== '/api/auth/refresh') {
            const refreshed = await this.refresh();
            if (refreshed) {
                if (this.accessToken) {
                    opts.headers['Authorization'] = 'Bearer ' + this.accessToken;
                }
                resp = await fetch(path, opts);
            } else {
                window.location.href = '/login';
                return null;
            }
        }

        return resp;
    },

    async get(path) {
        return this.request('GET', path);
    },

    async post(path, body) {
        return this.request('POST', path, body);
    },

    async delete(path) {
        return this.request('DELETE', path);
    },

    async getJSON(path) {
        const resp = await this.get(path);
        if (!resp || !resp.ok) return null;
        return resp.json();
    },

    async postJSON(path, body) {
        const resp = await this.post(path, body);
        if (!resp) return null;
        return { ok: resp.ok, status: resp.status, data: await resp.json() };
    },

    async refresh() {
        try {
            const resp = await fetch('/api/auth/refresh', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                credentials: 'same-origin',
            });

            if (!resp.ok) return false;

            const data = await resp.json();
            this.accessToken = data.access_token;
            return true;
        } catch {
            return false;
        }
    },

    setToken(token) {
        this.accessToken = token;
    },

    async logout() {
        await this.post('/api/auth/logout');
        this.accessToken = null;
        window.location.href = '/login';
    }
};
