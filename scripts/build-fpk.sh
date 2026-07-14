#!/bin/bash
# Manually build FPK without fnpack
set -e

PROJECT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
cd "$PROJECT_DIR"

# 1. Verify connector binary exists
if [ ! -f "app/connector/officeeditor-connector" ]; then
    echo "ERROR: connector binary not found. Run go build first."
    exit 1
fi

BUILD_DIR="/tmp/fpk-build-$$"
mkdir -p "$BUILD_DIR/app"

# 2. Build app.tgz (tar.gz of app/ contents, preserving directory structure inside)
echo "Building app.tgz..."
cd app
tar czf "$BUILD_DIR/app.tgz" \
    connector/officeeditor-connector \
    docker/docker-compose.yaml \
    templates/new.pptx \
    ui/config \
    ui/images/icon_256.png \
    ui/images/icon_64.png \
    ui/images/donate.png \
    ui/images/donate-alipay.png \
    ui/images/donate-wechat.png \
    ui/index.cgi \
    www/index.html
cd "$PROJECT_DIR"

# 3. Build FPK (tar.gz containing all top-level files + app.tgz)
echo "Building FPK..."
cd "$PROJECT_DIR"
tar czf "$BUILD_DIR/OfficeEditor.fpk" \
    --transform 's,^\./,,' \
    manifest \
    LICENSE \
    ICON.PNG \
    ICON_256.PNG \
    config/privilege \
    config/resource \
    cmd/main \
    cmd/install_init \
    cmd/install_callback \
    cmd/uninstall_init \
    cmd/uninstall_callback \
    cmd/upgrade_init \
    cmd/upgrade_callback \
    cmd/config_init \
    cmd/config_callback \
    "$BUILD_DIR/app.tgz"

# Replace app.tgz in the archive with just the name (not the full path)
# Actually, the tar command above includes the full BUILD_DIR path
# Let me re-do it properly

rm -f "$BUILD_DIR/OfficeEditor.fpk"
# Move app.tgz to same context
cp "$BUILD_DIR/app.tgz" "$PROJECT_DIR/app.tgz"

tar czf "$BUILD_DIR/OfficeEditor.fpk" \
    manifest \
    LICENSE \
    ICON.PNG \
    ICON_256.PNG \
    config/privilege \
    config/resource \
    cmd/main \
    cmd/install_init \
    cmd/install_callback \
    cmd/uninstall_init \
    cmd/uninstall_callback \
    cmd/upgrade_init \
    cmd/upgrade_callback \
    cmd/config_init \
    cmd/config_callback \
    app.tgz

# Remove temporary app.tgz from project dir
rm -f "$PROJECT_DIR/app.tgz"

# Copy result
cp "$BUILD_DIR/OfficeEditor.fpk" releases/OfficeEditor.fpk

# Cleanup
rm -rf "$BUILD_DIR"

echo "FPK built: releases/OfficeEditor.fpk"
ls -lh releases/OfficeEditor.fpk
