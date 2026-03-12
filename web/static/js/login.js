// Klever Node Hub - Login Page Logic
let recoveryCodes = [];

async function init() {
    show('loading');

    if (!Passkey.isSupported()) {
        hide('loading');
        show('no-webauthn');
        return;
    }

    const status = await API.getJSON('/api/setup/status');
    hide('loading');

    if (!status) {
        showError('Failed to check setup status');
        return;
    }

    if (status.setup_complete) {
        show('login-section');
    } else {
        show('setup-name-section');
    }
}

async function setupPasskey() {
    const name = document.getElementById('passkey-name').value || 'default';
    const btn = document.getElementById('btn-setup');
    btn.disabled = true;
    btn.textContent = 'Registering...';
    clearAlerts();

    try {
        const result = await Passkey.register(name);
        if (result.recovery_codes) {
            recoveryCodes = result.recovery_codes;
            displayRecoveryCodes(recoveryCodes);
            hide('setup-section');
            show('recovery-section');
        } else {
            window.location.href = '/overview';
        }
    } catch (err) {
        showError(err.message);
        btn.disabled = false;
        btn.textContent = 'Register Passkey';
    }
}

async function loginPasskey() {
    const btn = document.getElementById('btn-passkey-login');
    btn.disabled = true;
    btn.textContent = 'Authenticating...';
    clearAlerts();

    try {
        await Passkey.login();
        window.location.href = '/overview';
    } catch (err) {
        showError(err.message);
        btn.disabled = false;
        btn.textContent = 'Sign in with Passkey';
    }
}

async function loginRecovery() {
    const code = document.getElementById('recovery-code').value.trim();
    if (!code) {
        showError('Please enter a recovery code');
        return;
    }
    clearAlerts();

    const result = await API.postJSON('/api/auth/recovery', { code });
    if (result && result.ok) {
        API.setToken(result.data.access_token);
        if (result.data.remaining <= 2) {
            showSuccess('Login successful. Warning: only ' + result.data.remaining + ' recovery codes remaining!');
            setTimeout(() => { window.location.href = '/overview'; }, 2000);
        } else {
            window.location.href = '/overview';
        }
    } else {
        showError(result?.data?.error || 'Invalid recovery code');
    }
}

function displayRecoveryCodes(codes) {
    const container = document.getElementById('recovery-codes');
    container.textContent = '';
    codes.forEach(c => {
        const span = document.createElement('span');
        span.textContent = c;
        container.appendChild(span);
    });
}

function copyRecoveryCodes() {
    navigator.clipboard.writeText(recoveryCodes.join('\n'))
        .then(() => showSuccess('Copied to clipboard'))
        .catch(() => showError('Copy failed'));
}

function downloadRecoveryCodes() {
    const text = 'Klever Node Hub - Recovery Codes\n' +
                 'Generated: ' + new Date().toISOString() + '\n\n' +
                 recoveryCodes.join('\n') + '\n\n' +
                 'Keep these codes safe. Each can only be used once.';
    const blob = new Blob([text], { type: 'text/plain' });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = 'klever-node-hub-recovery-codes.txt';
    a.click();
    URL.revokeObjectURL(url);
}

function showError(msg) {
    const el = document.getElementById('error-alert');
    el.textContent = msg;
    el.classList.remove('hidden');
}

function showSuccess(msg) {
    const el = document.getElementById('success-alert');
    el.textContent = msg;
    el.classList.remove('hidden');
}

function clearAlerts() {
    document.getElementById('error-alert').classList.add('hidden');
    document.getElementById('success-alert').classList.add('hidden');
}

function show(id) { document.getElementById(id).classList.remove('hidden'); }
function hide(id) { document.getElementById(id).classList.add('hidden'); }

async function setupDashboardName() {
    const name = document.getElementById('setup-dashboard-name').value.trim() || 'Klever Node Hub';
    clearAlerts();
    try {
        await fetch('/api/settings', {
            method: 'PUT',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ dashboard_name: name })
        });
    } catch (_) {
        // Settings save is best-effort during setup (no JWT yet)
    }
    hide('setup-name-section');
    show('setup-section');
}

function showSetupNotify() {
    hide('recovery-section');
    show('setup-notify-section');
}

function toggleSetupNotifyFields() {
    const type = document.getElementById('setup-notify-type').value;
    document.getElementById('setup-notify-telegram').classList.toggle('hidden', type !== 'telegram');
    document.getElementById('setup-notify-webhook').classList.toggle('hidden', type !== 'webhook');
}

async function finishSetup() {
    const type = document.getElementById('setup-notify-type').value;
    if (type === 'telegram') {
        const token = document.getElementById('setup-tg-token').value.trim();
        const chatID = document.getElementById('setup-tg-chatid').value.trim();
        if (token && chatID) {
            await API.post('/api/notifications/channels', {
                type: 'telegram', bot_token: token, chat_id: chatID
            });
        }
    } else if (type === 'webhook') {
        const url = document.getElementById('setup-wh-url').value.trim();
        if (url) {
            await API.post('/api/notifications/channels', {
                type: 'webhook', url: url
            });
        }
    }
    window.location.href = '/overview';
}

init();
