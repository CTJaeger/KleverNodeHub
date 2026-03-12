// Klever Node Hub - Klever Extension Wallet Authentication
// Challenge-response login via window.kleverWeb.signMessage()

const KleverAuth = {
    // Check if Klever Extension is available in the browser
    isAvailable() {
        return typeof window.kleverWeb !== 'undefined';
    },

    // Get the wallet address from the Klever Extension
    async getAddress() {
        if (!this.isAvailable()) {
            throw new Error('Klever Extension not detected');
        }
        await window.kleverWeb.initialize();
        const address = await window.kleverWeb.getWalletAddress();
        if (!address) {
            throw new Error('No wallet address found');
        }
        return address;
    },

    // Full challenge-response login flow
    async login() {
        const address = await this.getAddress();

        // Step 1: Get challenge from server
        const resp = await fetch('/api/auth/klever/challenge?address=' + encodeURIComponent(address));
        if (!resp.ok) {
            const data = await resp.json();
            throw new Error(data.error || 'Failed to get challenge');
        }
        const { challenge } = await resp.json();

        // Step 2: Sign challenge with Klever Extension
        const signature = await window.kleverWeb.signMessage(challenge);
        if (!signature) {
            throw new Error('Signing cancelled or failed');
        }

        // Step 3: Verify signature on server
        const verifyResp = await API.postJSON('/api/auth/klever/verify', {
            address: address,
            challenge: challenge,
            signature: signature
        });

        if (!verifyResp || !verifyResp.ok) {
            throw new Error(verifyResp?.data?.error || 'Verification failed');
        }

        API.setToken(verifyResp.data.access_token);
        return verifyResp.data;
    }
};
