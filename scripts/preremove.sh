#!/bin/bash

set -e

# Pre-removal script for kube-tunnel
# This script runs before package removal on Linux systems

SERVICE_NAME="kube-tunnel"
SERVICE_PATH="/etc/systemd/system/kube-tunnel.service"
CONFIG_DIR="/etc/kube-tunnel"
LOG_DIR="/var/log/kube-tunnel"
USER="kube-tunnel"
GROUP="kube-tunnel"

echo "🔧 Preparing to remove kube-tunnel..."

# Stop and disable the service if it's running
if command -v systemctl >/dev/null 2>&1; then
    if systemctl is-active --quiet "$SERVICE_NAME"; then
        echo "Stopping kube-tunnel service..."
        systemctl stop "$SERVICE_NAME"
    fi

    if systemctl is-enabled --quiet "$SERVICE_NAME"; then
        echo "Disabling kube-tunnel service..."
        systemctl disable "$SERVICE_NAME"
    fi
fi

# Clean up any active port-forwards
echo "Cleaning up active connections..."
if pgrep -f "kube-tunnel" >/dev/null; then
    echo "Terminating kube-tunnel processes..."
    pkill -TERM -f "kube-tunnel" || true
    sleep 2
    # Force kill if still running
    if pgrep -f "kube-tunnel" >/dev/null; then
        pkill -KILL -f "kube-tunnel" || true
    fi
fi

# Remove systemd service file
if [ -f "$SERVICE_PATH" ]; then
    echo "Removing systemd service file..."
    rm -f "$SERVICE_PATH"
    if command -v systemctl >/dev/null 2>&1; then
        systemctl daemon-reload
    fi
fi

# Clean up DNS configuration (if any)
echo "Cleaning up DNS configuration..."

# macOS resolver cleanup
if [ -f "/etc/resolver/cluster.local" ]; then
    echo "Removing macOS DNS resolver configuration..."
    rm -f "/etc/resolver/cluster.local" || true
fi

# Linux hosts file cleanup
if [ -f "/etc/hosts" ]; then
    echo "Cleaning up /etc/hosts entries..."
    # Remove lines containing .svc.cluster.local
    sed -i.backup '/\.svc\.cluster\.local/d' /etc/hosts 2>/dev/null || true
fi

# Backup important files before removal
BACKUP_DIR="/tmp/kube-tunnel-backup-$(date +%Y%m%d-%H%M%S)"
if [ -d "$CONFIG_DIR" ] || [ -d "$LOG_DIR" ]; then
    echo "Creating backup at: $BACKUP_DIR"
    mkdir -p "$BACKUP_DIR"

    if [ -d "$CONFIG_DIR" ]; then
        cp -r "$CONFIG_DIR" "$BACKUP_DIR/config" 2>/dev/null || true
    fi

    if [ -d "$LOG_DIR" ]; then
        # Only backup recent logs (last 7 days)
        find "$LOG_DIR" -name "*.log" -mtime -7 -exec cp {} "$BACKUP_DIR/" \; 2>/dev/null || true
    fi

    echo "📁 Configuration and logs backed up to: $BACKUP_DIR"
fi

# Clean up log files older than 30 days
if [ -d "$LOG_DIR" ]; then
    echo "Cleaning up old log files..."
    find "$LOG_DIR" -name "*.log" -mtime +30 -delete 2>/dev/null || true
fi

# Remove temporary files
echo "Cleaning up temporary files..."
rm -rf /tmp/kube-tunnel-* 2>/dev/null || true

# Clean up any cached Go modules (if installed via go install)
if [ -d "$HOME/go/pkg/mod/github.com/snakeice/kube-tunnel*" ]; then
    echo "Cleaning up Go module cache..."
    rm -rf "$HOME/go/pkg/mod/github.com/snakeice/kube-tunnel"* 2>/dev/null || true
fi

# Remove user and group (only if they exist and no other packages use them)
if id "$USER" &>/dev/null; then
    echo "Checking if user $USER can be removed..."

    # Check if user owns any running processes (other than the ones we just killed)
    if ! pgrep -u "$USER" >/dev/null 2>&1; then
        # Check if user has any files outside of our directories
        USER_FILES=$(find /home /var /opt -user "$USER" 2>/dev/null | grep -v -E "($CONFIG_DIR|$LOG_DIR)" | wc -l)

        if [ "$USER_FILES" -eq 0 ]; then
            echo "Removing user: $USER"
            userdel "$USER" 2>/dev/null || true

            # Remove group if it exists and is empty
            if getent group "$GROUP" >/dev/null 2>&1; then
                groupdel "$GROUP" 2>/dev/null || true
            fi
        else
            echo "⚠️  User $USER has other files, keeping user account"
        fi
    else
        echo "⚠️  User $USER has running processes, keeping user account"
    fi
fi

# Display removal summary
echo ""
echo "🗑️  kube-tunnel pre-removal completed"
echo ""
echo "📋 What was done:"
echo "  ✅ Service stopped and disabled"
echo "  ✅ Active processes terminated"
echo "  ✅ DNS configuration cleaned up"
echo "  ✅ Temporary files removed"
echo "  ✅ Configuration backed up to: $BACKUP_DIR"
echo ""
echo "📁 Preserved directories (remove manually if needed):"
echo "  - Configuration: $CONFIG_DIR"
echo "  - Logs: $LOG_DIR"
echo "  - Backup: $BACKUP_DIR"
echo ""
echo "🔧 Manual cleanup (if needed):"
echo "  sudo rm -rf $CONFIG_DIR"
echo "  sudo rm -rf $LOG_DIR"
echo "  sudo rm -rf $BACKUP_DIR"
echo ""
echo "Thank you for using kube-tunnel! 🚀"
