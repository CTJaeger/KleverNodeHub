// Klever Node Hub - WebAuthn / Passkey Helpers
// Handles registration and authentication ceremonies.

const Passkey = {
    // Check if WebAuthn is supported in the browser
    isSupported() {
        return window.PublicKeyCredential !== undefined;
    },

    // Register a new passkey
    async register(name) {
        if (!this.isSupported()) {
            throw new Error('WebAuthn is not supported in this browser');
        }

        // Begin registration — get challenge from server
        const beginResp = await API.postJSON('/api/auth/passkey/register/begin', { name });
        if (!beginResp || !beginResp.ok) {
            throw new Error(beginResp?.data?.error || 'Failed to begin registration');
        }

        const { options, session_id } = beginResp.data;

        // Convert base64url fields to ArrayBuffer
        const publicKey = options.publicKey;
        publicKey.challenge = base64urlToBuffer(publicKey.challenge);
        publicKey.user.id = base64urlToBuffer(publicKey.user.id);
        if (publicKey.excludeCredentials) {
            publicKey.excludeCredentials = publicKey.excludeCredentials.map(c => ({
                ...c,
                id: base64urlToBuffer(c.id),
            }));
        }

        // Prompt user to create credential
        const credential = await navigator.credentials.create({ publicKey });

        // Serialize response for server
        const attestation = {
            id: credential.id,
            rawId: bufferToBase64url(credential.rawId),
            type: credential.type,
            response: {
                attestationObject: bufferToBase64url(credential.response.attestationObject),
                clientDataJSON: bufferToBase64url(credential.response.clientDataJSON),
            },
        };

        // Finish registration — send attestation to server
        const finishResp = await API.postJSON(
            '/api/auth/passkey/register/finish?session_id=' + encodeURIComponent(session_id) + '&name=' + encodeURIComponent(name),
            attestation,
        );

        if (!finishResp || !finishResp.ok) {
            throw new Error(finishResp?.data?.error || 'Failed to finish registration');
        }

        return finishResp.data;
    },

    // Authenticate with an existing passkey
    async login() {
        if (!this.isSupported()) {
            throw new Error('WebAuthn is not supported in this browser');
        }

        // Begin login — get challenge from server
        const beginResp = await API.postJSON('/api/auth/passkey/login/begin', {});
        if (!beginResp || !beginResp.ok) {
            throw new Error(beginResp?.data?.error || 'Failed to begin login');
        }

        const { options, session_id } = beginResp.data;

        // Convert base64url fields to ArrayBuffer
        const publicKey = options.publicKey;
        publicKey.challenge = base64urlToBuffer(publicKey.challenge);
        if (publicKey.allowCredentials) {
            publicKey.allowCredentials = publicKey.allowCredentials.map(c => ({
                ...c,
                id: base64urlToBuffer(c.id),
            }));
        }

        // Prompt user to select credential
        const assertion = await navigator.credentials.get({ publicKey });

        // Serialize response for server
        const assertionData = {
            id: assertion.id,
            rawId: bufferToBase64url(assertion.rawId),
            type: assertion.type,
            response: {
                authenticatorData: bufferToBase64url(assertion.response.authenticatorData),
                clientDataJSON: bufferToBase64url(assertion.response.clientDataJSON),
                signature: bufferToBase64url(assertion.response.signature),
                userHandle: assertion.response.userHandle
                    ? bufferToBase64url(assertion.response.userHandle)
                    : null,
            },
        };

        // Finish login — send assertion to server
        const finishResp = await API.postJSON(
            '/api/auth/passkey/login/finish?session_id=' + encodeURIComponent(session_id),
            assertionData,
        );

        if (!finishResp || !finishResp.ok) {
            throw new Error(finishResp?.data?.error || 'Failed to finish login');
        }

        // Store token
        if (finishResp.data.access_token) {
            API.setToken(finishResp.data.access_token);
        }

        return finishResp.data;
    }
};

// --- Base64URL helpers ---

function base64urlToBuffer(base64url) {
    const base64 = base64url.replace(/-/g, '+').replace(/_/g, '/');
    const padding = '='.repeat((4 - base64.length % 4) % 4);
    const binary = atob(base64 + padding);
    const bytes = new Uint8Array(binary.length);
    for (let i = 0; i < binary.length; i++) {
        bytes[i] = binary.charCodeAt(i);
    }
    return bytes.buffer;
}

function bufferToBase64url(buffer) {
    const bytes = new Uint8Array(buffer);
    let binary = '';
    for (let i = 0; i < bytes.length; i++) {
        binary += String.fromCharCode(bytes[i]);
    }
    return btoa(binary).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
}
