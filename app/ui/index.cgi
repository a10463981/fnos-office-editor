#!/bin/bash
# CGI 入口 - 重定向到连接器
BASE_PATH="/var/apps/OfficeEditor/target/www"

# 输出 HTTP 头
echo "Content-Type: text/html; charset=utf-8"
echo ""

# 输出 index.html
if [ -f "$BASE_PATH/index.html" ]; then
    cat "$BASE_PATH/index.html"
else
    echo "<html><body><h1>FNos 办公编辑器</h1><p>请等待安装完成</p></body></html>"
fi
