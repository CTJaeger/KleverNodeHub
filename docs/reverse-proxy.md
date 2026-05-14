# Reverse Proxy Setup (HTTPS with Let's Encrypt)

The dashboard uses a self-signed TLS certificate (from its internal CA for mTLS with agents). While this encrypts traffic, browsers will show a certificate warning and **PWA installation will be blocked** because service workers require a trusted certificate.

To get a trusted HTTPS connection, place a reverse proxy in front of the dashboard that terminates TLS with a Let's Encrypt certificate.

## Prerequisites

- A domain or subdomain pointing to your server (A record or CNAME)
- Port 80 and 443 open on your firewall
- [Certbot](https://certbot.eff.org/) installed with the plugin for your web server:
  - Apache: `sudo apt install certbot python3-certbot-apache`
  - Nginx: `sudo apt install certbot python3-certbot-nginx`

## Agents connect directly to port 9443

Agents authenticate to the dashboard with **mTLS client certificates** on the `/ws/agent` endpoint. A reverse proxy terminates TLS and strips those client certificates, so **agents must connect directly to port 9443 — not through the proxy**.

```
Browsers ──HTTPS:443─▶ Reverse Proxy ──HTTPS:9443─▶ Dashboard
Agents   ──mTLS:9443────────────────────────────────▶ Dashboard
```

This has two consequences for how you expose the dashboard:

- **Port 9443 must be reachable by every agent.** If any agent runs on a different host than the dashboard, that agent needs network reach to port 9443.
- **The `127.0.0.1:9443` Docker binding shown below only works if every agent runs on the same host as the dashboard.** For remote agents, bind 9443 to a LAN address (e.g. `-p 10.0.0.5:9443:9443`) or the public interface — restricted to agent IPs via firewall — instead of `127.0.0.1`.

## Docker: Bind to localhost only (all agents local)

If the dashboard and **all** agents run on the same host, bind 9443 to `127.0.0.1` so it is only reachable through the proxy from the outside:

```bash
docker run -d \
  -p 127.0.0.1:9443:9443 \
  -v klever-data:/root/.klever-node-hub \
  --name klever-node-hub \
  ctjaeger/klever-node-hub:latest \
  --domain your-domain.example.com
```

> **Important:** The `--domain` flag must match the domain you use in the reverse proxy config. It sets the WebAuthn Relying Party ID for Passkey authentication.

If running the dashboard as a binary (not Docker), pass `--addr 127.0.0.1:9443` so it only listens on localhost behind the proxy.

If you have **remote agents**, replace `127.0.0.1` with a LAN or public address that those agents can reach (and firewall-restrict it to agent source IPs). Browsers still go through the proxy on 443.

---

## Option 1: Apache

### 1. Enable required modules

```bash
sudo a2enmod proxy proxy_http proxy_wstunnel ssl rewrite headers
sudo systemctl restart apache2
```

### 2. Obtain a certificate

```bash
sudo certbot certonly --apache -d your-domain.example.com
```

### 3. Create the virtual host

Create `/etc/apache2/sites-available/klever-node-hub.conf`:

```apache
<VirtualHost *:80>
    ServerName your-domain.example.com
    RewriteEngine On
    RewriteRule ^(.*)$ https://%{HTTP_HOST}$1 [R=301,L]
</VirtualHost>

<VirtualHost *:443>
    ServerName your-domain.example.com

    SSLEngine on
    SSLProtocol -all +TLSv1.2 +TLSv1.3
    SSLCertificateFile    /etc/letsencrypt/live/your-domain.example.com/fullchain.pem
    SSLCertificateKeyFile /etc/letsencrypt/live/your-domain.example.com/privkey.pem

    # Proxy to dashboard (self-signed backend cert)
    ProxyPreserveHost On
    SSLProxyEngine On
    SSLProxyVerify none
    SSLProxyCheckPeerCN off
    SSLProxyCheckPeerName off

    # Forward client protocol. mod_proxy_http adds X-Forwarded-For
    # automatically, which the dashboard uses for rate limiting and lockout.
    RequestHeader set X-Forwarded-Proto "https"

    # WebSocket support (required for live metrics and log streaming)
    # mod_proxy_wstunnel handles the upgrade automatically
    RewriteEngine On
    RewriteCond %{HTTP:Upgrade} websocket [NC]
    RewriteRule ^/(.*)$ https://127.0.0.1:9443/$1 [P,L]

    # All other requests
    ProxyPass / https://127.0.0.1:9443/
    ProxyPassReverse / https://127.0.0.1:9443/
</VirtualHost>
```

### 4. Enable and activate

```bash
sudo a2ensite klever-node-hub.conf
sudo apache2ctl configtest
sudo systemctl reload apache2
```

### 5. Certificate renewal

Certbot sets up automatic renewal by default. Verify with:

```bash
sudo certbot renew --dry-run
```

---

## Option 2: Nginx

### 1. Obtain a certificate

```bash
sudo certbot certonly --nginx -d your-domain.example.com
```

### 2. Create the server block

Create `/etc/nginx/sites-available/klever-node-hub`:

```nginx
# Redirect HTTP to HTTPS
server {
    listen 80;
    server_name your-domain.example.com;
    return 301 https://$host$request_uri;
}

server {
    listen 443 ssl http2;
    server_name your-domain.example.com;

    ssl_certificate     /etc/letsencrypt/live/your-domain.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/your-domain.example.com/privkey.pem;
    ssl_protocols       TLSv1.2 TLSv1.3;

    location / {
        proxy_pass https://127.0.0.1:9443;
        proxy_ssl_verify off;  # dashboard uses self-signed cert

        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;

        # WebSocket support (required for live metrics and log streaming)
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";

        # Keep WebSocket connections alive (default 60s kills idle connections)
        proxy_read_timeout 86400s;
        proxy_send_timeout 86400s;
    }
}
```

### 3. Enable and activate

```bash
sudo ln -s /etc/nginx/sites-available/klever-node-hub /etc/nginx/sites-enabled/
sudo nginx -t
sudo systemctl reload nginx
```

### 4. Certificate renewal

Certbot sets up automatic renewal by default. Verify with:

```bash
sudo certbot renew --dry-run
```

---

## Option 3: Caddy (automatic Let's Encrypt)

[Caddy](https://caddyserver.com/) automatically obtains and renews Let's Encrypt certificates — no certbot or manual renewal needed.

### Using Docker Compose

```yaml
services:
  klever-node-hub:
    image: ctjaeger/klever-node-hub:latest
    command: --domain your-domain.example.com
    volumes:
      - klever-data:/root/.klever-node-hub

  caddy:
    image: caddy:latest
    ports:
      - "80:80"
      - "443:443"
    volumes:
      - caddy-data:/data
      - ./Caddyfile:/etc/caddy/Caddyfile
    depends_on:
      - klever-node-hub

volumes:
  klever-data:
  caddy-data:
```

Create a `Caddyfile`:

```
your-domain.example.com {
    reverse_proxy klever-node-hub:9443 {
        transport http {
            tls_insecure_skip_verify
        }
    }
}
```

Start with:

```bash
docker compose up -d
```

---

## Verifying the setup

1. Open `https://your-domain.example.com` in a browser
2. Confirm the padlock icon shows a valid certificate (not self-signed)
3. On mobile, you should now see the **"Add to Home Screen"** or install prompt for the PWA
4. Check that live metrics update in real-time (confirms WebSocket proxying works)
