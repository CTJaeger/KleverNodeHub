// Klever Node Hub — Service Worker (PWA)
const CACHE_NAME = 'knh-v1';
const STATIC_ASSETS = [
    '/static/css/style.css',
    '/static/js/api.js',
    '/static/js/ws.js',
    '/static/js/datatable.js',
    '/static/js/charts.js',
    '/static/manifest.json',
];

// Install: pre-cache static assets
self.addEventListener('install', event => {
    event.waitUntil(
        caches.open(CACHE_NAME).then(cache => cache.addAll(STATIC_ASSETS))
    );
    self.skipWaiting();
});

// Activate: clean old caches
self.addEventListener('activate', event => {
    event.waitUntil(
        caches.keys().then(keys =>
            Promise.all(keys.filter(k => k !== CACHE_NAME).map(k => caches.delete(k)))
        )
    );
    self.clients.claim();
});

// Fetch: network-first for API/pages, cache-first for static assets
self.addEventListener('fetch', event => {
    const url = new URL(event.request.url);

    // Skip non-GET and API requests
    if (event.request.method !== 'GET' || url.pathname.startsWith('/api/')) {
        return;
    }

    // Static assets: cache-first
    if (url.pathname.startsWith('/static/')) {
        event.respondWith(
            caches.match(event.request).then(cached => cached || fetch(event.request))
        );
        return;
    }

    // Pages: network-first (always get fresh HTML)
    event.respondWith(
        fetch(event.request).catch(() => caches.match(event.request))
    );
});

// Push: show notification when receiving a push message
self.addEventListener('push', event => {
    if (!event.data) return;

    let data;
    try {
        data = event.data.json();
    } catch {
        data = { title: 'Klever Node Hub', body: event.data.text() };
    }

    const options = {
        body: data.body || '',
        icon: '/static/img/icon-192.png',
        badge: '/static/img/icon-192.png',
        tag: data.tag || 'knh-alert',
        renotify: true,
        data: { url: data.url || '/overview' },
    };

    event.waitUntil(
        self.registration.showNotification(data.title || 'Klever Node Hub', options)
    );
});

// Notification click: open/focus the app
self.addEventListener('notificationclick', event => {
    event.notification.close();
    const url = event.notification.data?.url || '/overview';

    event.waitUntil(
        clients.matchAll({ type: 'window', includeUncontrolled: true }).then(windowClients => {
            for (const client of windowClients) {
                if (client.url.includes(self.location.origin) && 'focus' in client) {
                    client.navigate(url);
                    return client.focus();
                }
            }
            return clients.openWindow(url);
        })
    );
});
