#!/bin/bash

set -e

# Post-installation script for kube-tunnel
# This script runs after package installation on Linux systems

BINARY_PATH="/usr/bin/kube-tunnel"
SERVICE_PATH="/etc/systemd/system/kube-tunnel.service"
CONFIG_DIR="/etc/kube-tunnel"
LOG_DIR="/var/log/kube-tunnel"
USER="kube-tunnel"
GROUP="kube-tunnel"

echo "🚀 Setting up kube-tunnel..."

# Create kube-tunnel user and group if they don't exist
if ! id "$USER" &>/dev/null; then
    echo "Creating user: $USER"
    useradd --system --no-create-home --shell /bin/false "$USER"
fi

# Create configuration directory
if [ ! -d "$CONFIG_DIR" ]; then
    echo "Creating configuration directory: $CONFIG_DIR"
    mkdir -p "$CONFIG_DIR"
    chown "$USER:$GROUP" "$CONFIG_DIR"
    chmod 755 "$CONFIG_DIR"
fi

# Create log directory
if [ ! -d "$LOG_DIR" ]; then
    echo "Creating log directory: $LOG_DIR"
    mkdir -p "$LOG_DIR"
    chown "$USER:$GROUP" "$LOG_DIR"
    chmod 755 "$LOG_DIR"
fi

# Create systemd service file
if [ ! -f "$SERVICE_PATH" ]; then
    echo "Creating systemd service: $SERVICE_PATH"
    cat > "$SERVICE_PATH" << 'EOF'
[Unit]
Description=kube-tunnel - Kubernetes service proxy
Documentation=https://github.com/snakeice/kube-tunnel
After=network.target
Wants=network.target

[Service]
Type=simple
User=kube-tunnel
Group=kube-tunnel
ExecStart=/usr/bin/kube-tunnel -port=80
Restart=always
RestartSec=5
StandardOutput=journal
StandardError=journal
SyslogIdentifier=kube-tunnel

# Security settings
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/var/log/kube-tunnel
CapabilityBoundingSet=CAP_NET_BIND_SERVICE
AmbientCapabilities=CAP_NET_BIND_SERVICE

# Environment
Environment=LOG_LEVEL=info
Environment=HEALTH_MONITOR_ENABLED=true

[Install]
WantedBy=multi-user.target
EOF
fi

# Set proper permissions
chmod 644 "$SERVICE_PATH"
chmod +x "$BINARY_PATH"

# Reload systemd
if command -v systemctl >/dev/null 2>&1; then
    echo "Reloading systemd daemon..."
    systemctl daemon-reload
    systemctl enable kube-tunnel.service
fi

# Create default configuration
if [ ! -f "$CONFIG_DIR/config.env" ]; then
    echo "Creating default configuration: $CONFIG_DIR/config.env"
    cat > "$CONFIG_DIR/config.env" << 'EOF'
# kube-tunnel configuration
# Uncomment and modify as needed

# Proxy settings
# PROXY_PORT=80
# PROXY_MAX_RETRIES=2
# PROXY_RETRY_DELAY_MS=100

# Health monitoring
# HEALTH_MONITOR_ENABLED=true
# HEALTH_CHECK_INTERVAL=30s
# HEALTH_CHECK_TIMEOUT=2s
# HEALTH_MAX_FAILURES=3

# Performance tuning
# MAX_IDLE_CONNS=300
# MAX_IDLE_CONNS_PER_HOST=100
# FORCE_HTTP2=true

# Logging
# LOG_LEVEL=info

# DNS settings
# SKIP_HEALTH_CHECK=false
EOF
    chown "$USER:$GROUP" "$CONFIG_DIR/config.env"
    chmod 644 "$CONFIG_DIR/config.env"
fi

# Add kube-tunnel to PATH if not already there
if ! command -v kube-tunnel >/dev/null 2>&1; then
    echo "Adding kube-tunnel to PATH..."
    echo 'export PATH=$PATH:/usr/bin' >> /etc/environment
fi

echo ""
echo "✅ kube-tunnel installation completed!"
echo ""
echo "📋 Next steps:"
echo "  1. Configure kubeconfig: export KUBECONFIG=~/.kube/config"
echo "  2. Start the service: sudo systemctl start kube-tunnel"
echo "  3. Check status: sudo systemctl status kube-tunnel"
echo "  4. View logs: sudo journalctl -u kube-tunnel -f"
echo ""
echo "🌐 Test installation:"
echo "  curl http://my-service.default.svc.cluster.local/health"
echo ""
echo "📚 Configuration file: $CONFIG_DIR/config.env"
echo "📊 Performance testing: /usr/share/kube-tunnel/scripts/perf-test.sh"
echo "🔍 Health demo: /usr/share/kube-tunnel/scripts/health-demo.sh"
echo ""
echo "For more information, visit: https://github.com/snakeice/kube-tunnel"
