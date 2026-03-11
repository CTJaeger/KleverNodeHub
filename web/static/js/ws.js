// Klever Node Hub - WebSocket Client
// Receives real-time updates from the dashboard.

const WS = {
    socket: null,
    listeners: {},
    reconnectDelay: 1000,
    maxReconnectDelay: 30000,

    connect() {
        const protocol = location.protocol === 'https:' ? 'wss:' : 'ws:';
        const url = protocol + '//' + location.host + '/ws';

        this.socket = new WebSocket(url);

        this.socket.onopen = () => {
            console.log('WebSocket connected');
            this.reconnectDelay = 1000;
            this.emit('connected');
        };

        this.socket.onmessage = (event) => {
            try {
                const msg = JSON.parse(event.data);
                this.emit(msg.action, msg.payload);
                this.emit('message', msg);
            } catch (e) {
                console.error('WebSocket parse error:', e);
            }
        };

        this.socket.onclose = () => {
            console.log('WebSocket disconnected, reconnecting in', this.reconnectDelay + 'ms');
            this.emit('disconnected');
            setTimeout(() => this.connect(), this.reconnectDelay);
            this.reconnectDelay = Math.min(this.reconnectDelay * 2, this.maxReconnectDelay);
        };

        this.socket.onerror = (err) => {
            console.error('WebSocket error:', err);
            this.socket.close();
        };
    },

    on(event, callback) {
        if (!this.listeners[event]) {
            this.listeners[event] = [];
        }
        this.listeners[event].push(callback);
    },

    off(event, callback) {
        if (!this.listeners[event]) return;
        this.listeners[event] = this.listeners[event].filter(cb => cb !== callback);
    },

    emit(event, data) {
        if (!this.listeners[event]) return;
        this.listeners[event].forEach(cb => cb(data));
    },

    send(msg) {
        if (this.socket && this.socket.readyState === WebSocket.OPEN) {
            this.socket.send(JSON.stringify(msg));
        }
    }
};
