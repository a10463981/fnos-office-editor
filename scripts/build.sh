#!/bin/bash
# ============================================================================
# FNos 办公编辑器 - 构建脚本
# ============================================================================
set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_DIR="$SCRIPT_DIR"

echo "=========================================="
echo "  FNos 办公编辑器 - 构建"
echo "=========================================="

# 1. 构建 Go 连接器
echo ""
echo "[1/3] 构建 Go 连接器..."
cd "$PROJECT_DIR/connector"

if command -v go &>/dev/null; then
    go mod tidy
    go build -o "$PROJECT_DIR/dist/officeeditor-connector" ./cmd/server/
    echo "  ✓ 连接器已构建: dist/officeeditor-connector"
else
    echo "  ⚠ Go 未安装，跳过连接器构建"
fi

# 2. 检查 FPK 目录结构
echo ""
echo "[2/3] 检查 FPK 目录结构..."
cd "$PROJECT_DIR"

required_dirs=(
    "app/www"
    "app/ui/images"
    "app/officeeditor-docker"
    "cmd"
    "config"
    "wizard/install"
    "wizard/upgrade"
    "wizard/uninstall"
    "wizard/config"
)

for dir in "${required_dirs[@]}"; do
    if [ -d "$dir" ]; then
        echo "  ✓ $dir"
    else
        echo "  ✗ $dir - 缺失"
    fi
done

required_files=(
    "manifest"
    "config/privilege"
    "config/resource"
    "cmd/main"
    "cmd/install_init"
    "cmd/install_callback"
    "ICON.PNG"
    "ICON_256.PNG"
)

for file in "${required_files[@]}"; do
    if [ -f "$file" ]; then
        echo "  ✓ $file"
    else
        echo "  ✗ $file - 缺失"
    fi
done

# 3. 打包 FPK
echo ""
echo "[3/3] 打包 FPK..."
cd "$PROJECT_DIR"

if command -v fnpack &>/dev/null; then
    mkdir -p dist
    fnpack build --directory . --output dist/
    echo "  ✓ FPK 已生成: dist/OfficeEditor.fpk"
else
    echo "  ⚠ fnpack 未安装，跳过 FPK 打包"
    echo "  下载: https://developer.fnnas.com/docs/cli/fnpack/"
fi

echo ""
echo "=========================================="
echo "  构建完成!"
echo "=========================================="
