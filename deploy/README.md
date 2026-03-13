# Deployment

## systemd Service Files

### Dashboard

```bash
# Copy binary
sudo cp klever-node-hub-linux-amd64 /usr/local/bin/klever-node-hub
sudo chmod +x /usr/local/bin/klever-node-hub

# Create service user
sudo useradd -r -s /bin/false klever

# Install service
sudo cp deploy/klever-node-hub.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now klever-node-hub

# View logs
journalctl -u klever-node-hub -f
```

Edit `/etc/systemd/system/klever-node-hub.service` to set your `--domain` and `--addr` flags.

### Agent

```bash
# Copy binary
sudo cp klever-agent-linux-amd64 /usr/local/bin/klever-agent
sudo chmod +x /usr/local/bin/klever-agent

# Install service (edit DASHBOARD_HOST first!)
sudo cp deploy/klever-agent.service /etc/systemd/system/
sudo nano /etc/systemd/system/klever-agent.service
sudo systemctl daemon-reload
sudo systemctl enable --now klever-agent

# View logs
journalctl -u klever-agent -f
```

The agent runs as root because it needs access to the Docker socket.

## Self-Update

When running as a systemd service with `Restart=always`, the dashboard self-update will:

1. Download and verify the new binary
2. Replace the current binary
3. Exit the process
4. systemd automatically restarts with the new version
