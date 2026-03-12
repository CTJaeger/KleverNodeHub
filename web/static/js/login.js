// Klever Node Hub - Login Page Logic
let recoveryCodes = [];

async function init() {
    show('loading');

    const status = await API.getJSON('/api/setup/status');
    hide('loading');

    if (!status) {
        showError('Failed to check setup status');
        return;
    }

    if (status.setup_complete) {
        show('login-section');
        // Show passkey login button only if passkeys are registered and WebAuthn is available
        if (status.passkey_count > 0 && Passkey.isSupported()) {
            show('passkey-login-section');
        }
        // Show Klever login button only if Klever Extension is available and address is registered
        if (status.has_klever && KleverAuth.isAvailable()) {
            show('klever-login-section');
        }
    } else {
        show('setup-name-section');
    }
}

// Setup wizard step 1 → step 2 (password)
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
    show('setup-password-section');
}

// Setup wizard step 2: set initial password
async function setupPassword() {
    const password = document.getElementById('setup-password').value;
    const confirm = document.getElementById('setup-password-confirm').value;
    clearAlerts();

    if (!password || password.length < 8) {
        showError('Password must be at least 8 characters');
        return;
    }
    if (password !== confirm) {
        showError('Passwords do not match');
        return;
    }

    const result = await API.postJSON('/api/setup/password', { password });
    if (result && result.ok) {
        API.setToken(result.data.access_token);
        hide('setup-password-section');
        // Show passkey setup only if WebAuthn is available
        if (Passkey.isSupported()) {
            show('setup-section');
        } else {
            // Skip passkey, go straight to notifications
            show('setup-notify-section');
        }
    } else {
        showError(result?.data?.error || 'Failed to set password');
    }
}

// Setup wizard step 3: optional passkey registration
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
            hide('setup-section');
            show('setup-notify-section');
        }
    } catch (err) {
        showError(err.message);
        btn.disabled = false;
        btn.textContent = 'Register Passkey';
    }
}

// Skip passkey setup → notifications
function skipPasskeySetup() {
    hide('setup-section');
    show('setup-notify-section');
}

// After recovery codes → notifications
function showSetupNotify() {
    hide('recovery-section');
    show('setup-notify-section');
}

// Login with password
async function loginPassword() {
    const password = document.getElementById('login-password').value;
    if (!password) {
        showError('Please enter your password');
        return;
    }
    clearAlerts();

    const result = await API.postJSON('/api/auth/password', { password });
    if (result && result.ok) {
        API.setToken(result.data.access_token);
        window.location.href = '/overview';
    } else {
        showError(result?.data?.error || 'Login failed');
    }
}

// Login with passkey
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

// Login with Klever Extension
async function loginKlever() {
    const btn = document.getElementById('btn-klever-login');
    btn.disabled = true;
    btn.textContent = 'Connecting wallet...';
    clearAlerts();

    try {
        await KleverAuth.login();
        window.location.href = '/overview';
    } catch (err) {
        showError(err.message);
        btn.disabled = false;
        btn.textContent = 'Sign in with Klever Wallet';
    }
}

// Login with recovery code
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

// Toggle recovery code section
function toggleRecovery() {
    const el = document.getElementById('recovery-login');
    el.classList.toggle('hidden');
}

// Recovery codes display helpers
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

// Notification setup (final wizard step)
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

// UI helpers
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

init();
