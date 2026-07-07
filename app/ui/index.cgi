#!/bin/bash
# ============================================================================
# OfficeEditor - CGI 入口
# 通过 CGI 代理到连接器，避免客户端跨端口访问问题
# ============================================================================

QUERY="$QUERY_STRING"

# 提取 path 参数
FILE_PATH=$(echo "$QUERY" | sed -n 's/.*path=\([^&]*\).*/\1/p' | python3 -c "
import sys, urllib.parse
print(urllib.parse.unquote(sys.stdin.read().strip()))
" 2>/dev/null)

if [ -z "$FILE_PATH" ]; then
    # 无文件参数 → 显示首页
    echo "Content-Type: text/html; charset=utf-8"
    echo ""
    cat /var/apps/OfficeEditor/target/www/index.html 2>/dev/null || \
      echo "<html><body><h1>FNos 办公编辑器</h1><p>服务运行中</p></body></html>"
else
    # 有文件路径 → 通过 curl 从连接器获取编辑器页面并直接返回给浏览器
    ENCODED=$(echo "$FILE_PATH" | python3 -c "import sys,urllib.parse; print(urllib.parse.quote(sys.stdin.read().strip()))")
    echo "Content-Type: text/html; charset=utf-8"
    echo ""
    curl -s "http://127.0.0.1:10088/editor?path=$ENCODED" 2>/dev/null || \
      echo "<html><body><h1>错误</h1><p>无法连接到 OnlyOffice 编辑器服务</p></body></html>"
fi
