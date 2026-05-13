// Klever Node Hub - Agent Update Check
// Polls /api/agent/version and /api/servers, shows a banner if any agent
// is running an older version than the dashboard.

(function() {
    var BANNER_ID = 'agent-update-banner';
    var dismissedKey = 'agent-update-dismissed';

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
        if (!pageHeader) return null;
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
            '<button class="update-banner-dismiss" id="' + BANNER_ID + '-dismiss" title="Dismiss for this session">&times;</button>';
        pageHeader.parentNode.insertBefore(banner, pageHeader.nextSibling);
        document.getElementById(BANNER_ID + '-btn').addEventListener('click', updateAllAgents);
        document.getElementById(BANNER_ID + '-dismiss').addEventListener('click', function() {
            sessionStorage.setItem(dismissedKey, '1');
            banner.classList.add('hidden');
        });
        return banner;
    }

    async function fetchJSON(path) {
        try {
            var data = await API.getJSON(path);
            return data;
        } catch (e) {
            return null;
        }
    }

    async function checkAgentVersions() {
        if (sessionStorage.getItem(dismissedKey)) return;

        // Compare each agent's version against the dashboard's own version.
        // The dashboard and agent are released together, so any agent older
        // than the running dashboard counts as outdated.
        var sysResp = await fetchJSON('/api/system/version');
        var srvResp = await fetchJSON('/api/servers');
        if (!sysResp || !sysResp.version || !srvResp || !srvResp.servers) return;

        var latest = sysResp.version;
        var outdated = srvResp.servers.filter(function(s) {
            return s.status === 'online' &&
                   s.agent_version &&
                   compareVersions(s.agent_version, latest) < 0;
        });

        var banner = ensureBanner();
        if (!banner) return;
        if (outdated.length === 0) {
            banner.classList.add('hidden');
            return;
        }

        var total = srvResp.servers.filter(function(s) { return s.status === 'online'; }).length;
        var text = outdated.length + ' of ' + total + ' agent' + (total === 1 ? '' : 's') +
                   ' running outdated version — latest is ' + esc(latest);
        document.getElementById(BANNER_ID + '-text').innerHTML = esc(text);
        banner.classList.remove('hidden');
    }

    async function updateAllAgents() {
        var btn = document.getElementById(BANNER_ID + '-btn');
        var setBtn = function(text, disabled) {
            if (!btn) return;
            btn.textContent = text;
            btn.disabled = !!disabled;
        };

        // Determine the target version from the dashboard's own version.
        var sysResp = await fetchJSON('/api/system/version');
        var target = sysResp && sysResp.version;
        if (!target) {
            setBtn('Retry', false);
            return;
        }

        setBtn('Downloading...', true);

        // Step 1: ensure the agent binary is in the hub's update store.
        // If a release-driven install never uploaded one, the hub pulls
        // the binary from the matching GitHub release automatically.
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

        // Step 2: trigger the bulk agent update.
        setBtn('Updating...', true);
        try {
            var resp = await API.post('/api/agent/update/all', { version: target });
            if (resp && resp.ok) {
                setBtn('Update sent', true);
                // Agents restart — give them time before re-checking versions.
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

    if (document.readyState === 'loading') {
        document.addEventListener('DOMContentLoaded', start);
    } else {
        start();
    }
})();
