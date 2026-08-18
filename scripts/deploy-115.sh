#!/bin/bash
# Deploy NAS panel to [REDACTED]
set -euo pipefail

BINARY="/home/jacky/soft/nas/web/nas-panel"
TARGET="fm@[REDACTED]"

echo "=== Step 1: Upload binary ==="
scp -o ConnectTimeout=10 "$BINARY" "$TARGET:/tmp/nas-panel"
echo "OK"

echo "=== Step 2: Install deps + configure ==="
ssh -o ConnectTimeout=10 "$TARGET" bash << 'ENDSSH'
set -euo pipefail
sudo mv /tmp/nas-panel /usr/local/bin/nas-panel
sudo chmod +x /usr/local/bin/nas-panel
sudo apt-get install -y -qq parted smartmontools lvm2 mdadm 2>&1 | tail -1
sudo mkdir -p /opt/nas
TOKEN=$(openssl rand -hex 32)
cat | sudo tee /opt/nas/.env > /dev/null << ENVEOF
NAS_USER=fm
NAS_PASS=[REDACTED]
NAS_TOKEN=$TOKEN
ENVEOF
echo "NAS_TOKEN=$TOKEN"
ENDSSH
echo "OK"

echo "=== Step 3: Systemd service ==="
ssh -o ConnectTimeout=10 "$TARGET" bash << 'ENDSSH'
set -euo pipefail
sudo tee /etc/systemd/system/nas-panel.service > /dev/null << 'UNIT'
[Unit]
Description=NAS Web Management Panel
After=network.target

[Service]
Type=simple
EnvironmentFile=/opt/nas/.env
Environment=NAS_USER=fm
ExecStart=/usr/local/bin/nas-panel
Restart=on-failure
RestartSec=10

[Install]
WantedBy=multi-user.target
UNIT
sudo systemctl daemon-reload
sudo systemctl enable nas-panel
sudo systemctl start nas-panel
sleep 2
systemctl is-active nas-panel
ENDSSH
echo "OK"

echo "=== Step 4: Verify ==="
ssh -o ConnectTimeout=10 "$TARGET" 'curl -s -o /dev/null -w "%{http_code}" http://localhost:8090/'
echo " (HTTP status)"

echo ""
echo "=== DEPLOY COMPLETE ==="
echo "Panel: http://[REDACTED]:8090"
echo "Login: fm / [REDACTED]"