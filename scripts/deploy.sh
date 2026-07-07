#!/bin/bash
# ============================================================================
# FNos 办公编辑器 - 部署到 FNos 测试设备
# ============================================================================
set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_DIR="$SCRIPT_DIR"

if [ $# -lt 1 ]; then
    echo "用法: $0 <fnos-ip> [ssh-user]"
    echo "示例: $0 192.168.1.100"
    exit 1
fi

FNOS_IP="$1"
SSH_USER="${2:-root}"

echo "部署到 $FNOS_IP..."

# 确保有 FPK
if [ ! -f "$PROJECT_DIR/dist/OfficeEditor.fpk" ]; then
    echo "先运行 build.sh 生成 FPK"
    exit 1
fi

# 上传 FPK
echo "上传 FPK..."
scp "$PROJECT_DIR/dist/OfficeEditor.fpk" "$SSH_USER@$FNOS_IP:/tmp/"

# 在 FNos 上安装
echo "安装到 FNos..."
ssh "$SSH_USER@$FNOS_IP" "appcenter-cli install-fpk /tmp/OfficeEditor.fpk"

echo "部署完成!"
echo "访问: http://$FNOS_IP:10088/health"
