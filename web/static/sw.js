// Klever Node Hub — Service Worker (PWA)
//
// Strategy: the dashboard is a live monitoring tool, so freshness beats
// offline-availability. CSS/JS/pages are network-first (always serve the
// latest when online; the cache is only an offline fallback). Only rarely
// changing binary assets (icons/fonts) are cache-first.
//
// The previous version cached CSS/JS cache-first under a fixed cache name,
// so a tab left open kept serving stale styles after a deploy — new HTML
// (network-first) collided with an old style.css, breaking layout until a
// hard refresh. Network-first for code assets removes that whole class of bug.
const CACHE_NAME = 'knh-v2';

// Binary assets that change rarely and are safe to serve cache-first.
const PRECACHE_ASSETS = [
    '/static/manifest.json',
];

self.addEventListener('install', event => {
    event.waitUntil(
        caches.open(CACHE_NAME).then(cache => cache.addAll(PRECACHE_ASSETS)).catch(() => {})
    );
    self.skipWaiting();
});

// Activate: drop every cache that isn't the current one, so old cached CSS/JS
// from a previous build can't linger.
self.addEventListener('activate', event => {
    event.waitUntil(
        caches.keys().then(keys =>
            Promise.all(keys.filter(k => k !== CACHE_NAME).map(k => caches.delete(k)))
        ).then(() => self.clients.claim())
    );
});

function isCacheFirstAsset(pathname) {
    return /\.(png|jpg|jpeg|gif|svg|ico|webp|woff2?|ttf|eot)$/i.test(pathname) ||
        pathname === '/static/manifest.json';
}

// networkFirst: try the network, fall back to cache (and refresh the cache on
// success so the fallback stays as current as possible).
async function networkFirst(request) {
    try {
        const fresh = await fetch(request);
        if (fresh && fresh.ok && request.method === 'GET') {
            const copy = fresh.clone();
            caches.open(CACHE_NAME).then(c => c.put(request, copy)).catch(() => {});
        }
        return fresh;
    } catch (err) {
        const cached = await caches.match(request);
        if (cached) return cached;
        throw err;
    }
}

// cacheFirst: serve from cache, fall back to network and cache the result.
async function cacheFirst(request) {
    const cached = await caches.match(request);
    if (cached) return cached;
    const fresh = await fetch(request);
    if (fresh && fresh.ok) {
        const copy = fresh.clone();
        caches.open(CACHE_NAME).then(c => c.put(request, copy)).catch(() => {});
    }
    return fresh;
}

self.addEventListener('fetch', event => {
    const url = new URL(event.request.url);

    // Never touch non-GET or API requests.
    if (event.request.method !== 'GET' || url.pathname.startsWith('/api/')) {
        return;
    }

    // Rarely-changing binary assets: cache-first.
    if (url.pathname.startsWith('/static/') && isCacheFirstAsset(url.pathname)) {
        event.respondWith(cacheFirst(event.request));
        return;
    }

    // Everything else (CSS, JS, HTML pages): network-first so a deploy is
    // picked up immediately without a hard refresh.
    event.respondWith(networkFirst(event.request));
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
