// Klever Node Hub - Agent Update Check
// Compares each online agent's version against the running dashboard's
// own version (/api/system/version) and renders a top-of-page banner if
// any agent is older. Exposes window.kleverAgentUpdateCheck() so other
// scripts (manualUpdateCheck) can trigger a re-check.

(function() {
    var BANNER_ID = 'agent-update-banner';
    // Dismiss state is in-memory only. Closing the X hides the banner for
    // the current page-session but never persists across reloads. Wipe any
    // legacy sessionStorage entry from older builds so previously-stuck
    // users recover after this update.
    try { sessionStorage.removeItem('agent-update-dismissed'); } catch (e) {}
    var dismissed = false;
    var DEBUG = false; // flip in DevTools: window.kleverAgentUpdateDebug = true

    function log() {
        if (!DEBUG && !window.kleverAgentUpdateDebug) return;
        try { console.log.apply(console, ['[agent-update]'].concat([].slice.call(arguments))); } catch (e) {}
    }

    function parseVersion(v) {
        if (!v) return null;
        v = String(v).replace(/^v/, '');
        var m = v.match(/^(\d+)\.(\d+)\.(\d+)/);
        if (!m) return null;
        return [parseInt(m[1], 10), parseInt(m[2], 10), parseInt(m[3], 10)];
    }

    function compareVersions(a, b) {
        var pa = parseVersion(a);
        var pb = parseVersion(b);
        if (!pa || !pb) return 0;
        for (var i = 0; i < 3; i++) {
            if (pa[i] !== pb[i]) return pa[i] - pb[i];
        }
        return 0;
    }

    function esc(s) {
        return String(s == null ? '' : s).replace(/[&<>"']/g, function(c) {
            return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c];
        });
    }

    function ensureBanner() {
        var banner = document.getElementById(BANNER_ID);
        if (banner) return banner;
        var pageHeader = document.querySelector('.page-header');
        if (!pageHeader) {
            log('no .page-header found — cannot mount banner');
            return null;
        }
        banner = document.createElement('div');
        banner.id = BANNER_ID;
        banner.className = 'agent-update-banner hidden';
        banner.innerHTML =
            '<svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="#f59e0b" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">' +
                '<path d="M10.29 3.86L1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z"/>' +
                '<line x1="12" y1="9" x2="12" y2="13"/><line x1="12" y1="17" x2="12.01" y2="17"/>' +
            '</svg>' +
            '<span id="' + BANNER_ID + '-text"></span>' +
            '<button class="agent-update-banner-btn" id="' + BANNER_ID + '-btn">Update all agents</button>' +
            '<button class="update-banner-dismiss" id="' + BANNER_ID + '-dismiss" title="Dismiss until next reload / login">&times;</button>';
        pageHeader.parentNode.insertBefore(banner, pageHeader.nextSibling);
        document.getElementById(BANNER_ID + '-btn').addEventListener('click', updateAllAgents);
        document.getElementById(BANNER_ID + '-dismiss').addEventListener('click', function() {
            dismissed = true;
            banner.classList.add('hidden');
        });
        return banner;
    }

    async function fetchJSON(path) {
        try {
            var data = await API.getJSON(path);
            return data;
        } catch (e) {
            log('fetch failed', path, e);
            return null;
        }
    }

    async function checkAgentVersions() {
        if (dismissed) {
            log('dismissed for this page session — skipping');
            return;
        }

        var sysResp = await fetchJSON('/api/system/version');
        var srvResp = await fetchJSON('/api/servers');
        if (!sysResp || !sysResp.version) {
            log('no dashboard version available', sysResp);
            return;
        }
        if (!srvResp || !srvResp.servers) {
            log('no servers list available', srvResp);
            return;
        }

        var latest = sysResp.version;
        var onlineServers = srvResp.servers.filter(function(s) { return s.status === 'online'; });
        var outdated = onlineServers.filter(function(s) {
            if (!s.agent_version) return false;
            return compareVersions(s.agent_version, latest) < 0;
        });

        log('dashboard=' + latest, 'online=' + onlineServers.length, 'outdated=' + outdated.length,
            'agents=' + onlineServers.map(function(s) { return (s.name || s.id) + ':' + (s.agent_version || '?'); }).join(','));

        var banner = ensureBanner();
        if (!banner) return;
        if (outdated.length === 0) {
            banner.classList.add('hidden');
            return;
        }

        var text = outdated.length + ' of ' + onlineServers.length + ' agent' +
                   (onlineServers.length === 1 ? '' : 's') +
                   ' running outdated version — latest is ' + latest;
        document.getElementById(BANNER_ID + '-text').textContent = text;
        // Reset button label/state in case a previous update attempt left it changed.
        var btn = document.getElementById(BANNER_ID + '-btn');
        if (btn) {
            btn.disabled = false;
            btn.textContent = 'Update all agents';
        }
        banner.classList.remove('hidden');
    }

    async function updateAllAgents() {
        var btn = document.getElementById(BANNER_ID + '-btn');
        var setBtn = function(text, disabled) {
            if (!btn) return;
            btn.textContent = text;
            btn.disabled = !!disabled;
        };

        var sysResp = await fetchJSON('/api/system/version');
        var target = sysResp && sysResp.version;
        if (!target) {
            setBtn('Retry', false);
            return;
        }

        setBtn('Downloading...', true);
        try {
            var dlResp = await API.post('/api/agent/download-release-auto', { tag: target });
            if (!dlResp || !dlResp.ok) {
                setBtn('Download failed', false);
                return;
            }
        } catch (e) {
            setBtn('Download failed', false);
            return;
        }

        setBtn('Updating...', true);
        try {
            var resp = await API.post('/api/agent/update/all', { version: target });
            if (resp && resp.ok) {
                setBtn('Update sent', true);
                setTimeout(checkAgentVersions, 15000);
            } else {
                setBtn('Retry', false);
            }
        } catch (e) {
            setBtn('Retry', false);
        }
    }

    function start() {
        checkAgentVersions();
        setInterval(checkAgentVersions, 60000);
    }

    // Expose for manual triggers (e.g. the "Check for updates" button)
    window.kleverAgentUpdateCheck = checkAgentVersions;

    if (document.readyState === 'loading') {
        document.addEventListener('DOMContentLoaded', start);
    } else {
        start();
    }
})();
