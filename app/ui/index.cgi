#!/usr/bin/env python3
import os, urllib.parse, subprocess, sys, re

qs = os.environ.get('QUERY_STRING', '')
params = urllib.parse.parse_qs(qs)
file_path = params.get('path', [None])[0]

# 从 HTTP_REFERER 提取 FNos 服务器实际地址
referer = os.environ.get('HTTP_REFERER', '')
fnos_host = '127.0.0.1'
m = re.search(r'https?://([^/:]+)', referer)
if m:
    fnos_host = m.group(1)

if not file_path:
    print('Content-Type: text/html; charset=utf-8')
    print()
    print('<html><body><h1>FNos 办公编辑器</h1><p>服务运行中</p></body></html>')
else:
    encoded = urllib.parse.quote(file_path)
    editor_url = f'http://127.0.0.1:10088/editor?path={encoded}&host={fnos_host}'
    
    result = subprocess.run(
        ['curl', '-s', editor_url],
        capture_output=True, text=True, timeout=10
    )
    
    html = result.stdout
    if result.returncode != 0 or not html.strip():
        print('Content-Type: text/html; charset=utf-8')
        print()
        print('<html><body><h1>错误</h1><p>无法连接到编辑器服务</p></body></html>')
    else:
        # 替换页面中的 localhost/127.0.0.1 为实际 FNos 地址
        html = html.replace('127.0.0.1:9080', f'{fnos_host}:9080')
        html = html.replace('localhost:10088', f'{fnos_host}:10088')
        print('Content-Type: text/html; charset=utf-8')
        print()
        print(html)
