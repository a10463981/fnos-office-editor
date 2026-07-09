#!/usr/bin/env python3
import os, urllib.parse, subprocess, re, sys, json

qs = os.environ.get('QUERY_STRING', '')
params = urllib.parse.parse_qs(qs)
file_path = params.get('path', [None])[0]
action = params.get('action', [''])[0]

user_id   = os.environ.get('HTTP_X_TRIM_USERID', 'anonymous')
user_name = os.environ.get('HTTP_X_TRIM_USERNAME', '')
user_dir  = f'/vol1/{user_id}' if user_id and user_id != 'anonymous' else '/vol1/1000'
is_admin = os.environ.get('HTTP_X_TRIM_ISADMIN', 'false')

# ---- 前端 API 使用 nginx 代理路径（同源访问，适用于 App / 浏览器 / fnconnect 等所有场景）----
# nginx 已将 /officeeditor-api/ → 127.0.0.1:10088
# 前端 JS 通过这个路径调用连接器的所有 /api/* 接口
connector_base = f'http://127.0.0.1:10088'
api_base = f'/officeeditor-api'

# 以下变量仅用于 CGI 内部调用，不暴露给前端
request_host = os.environ.get('HTTP_HOST', '127.0.0.1').split(':')[0]

# ---- 代理 OnlyOffice JS/CSS（所有场景使用本地回源）----
if '/officeds/' in os.environ.get('REQUEST_URI', ''):
    target = os.environ.get('REQUEST_URI', '')
    idx = target.find('/officeds/')
    backend_path = target[idx + len('/officeds/'):]
    result = subprocess.run(['curl', '-s', f'http://127.0.0.1:9080/{backend_path}'], capture_output=True)
    if result.returncode == 0:
        ct = 'application/javascript' if backend_path.endswith('.js') else 'text/css' if backend_path.endswith('.css') else 'application/octet-stream'
        print(f'Content-Type: {ct}')
        print()
        sys.stdout.buffer.write(result.stdout)
    else:
        print('Status: 404')
        print()
    sys.exit(0)

if action == 'create':
    doc_type = params.get('type', ['docx'])[0]
    result = subprocess.run(['curl','-s','-X','POST',f'{connector_base}/api/create?type={doc_type}&dir={urllib.parse.quote(user_dir)}'], capture_output=True, text=True, timeout=10)
    try: data = json.loads(result.stdout.strip()); new_path = data.get('path', result.stdout.strip())
    except: new_path = result.stdout.strip()
    print(f'Location: /cgi/ThirdParty/OfficeEditor/index.cgi?path={urllib.parse.quote(new_path)}')
    print('Status: 302\n')
    sys.exit(0)

if file_path:
    encoded = urllib.parse.quote(file_path)
    # 不再传 host 参数（handleEditorPage 已不使用它）
    editor_url = f'{connector_base}/editor?path={encoded}'
    if user_id: editor_url += f'&user_id={urllib.parse.quote(user_id)}'
    if user_name: editor_url += f'&user_name={urllib.parse.quote(user_name)}'
    result = subprocess.run(['curl','-s',editor_url], capture_output=True, text=True, timeout=10)
    html = result.stdout
    if result.returncode != 0 or not html.strip():
        print('Content-Type: text/html; charset=utf-8\n')
        print('<html><body><h1>错误</h1><p>无法连接到编辑器服务</p></body></html>')
    else:
        # api.js 已使用相对路径 /officeds/，不需要 URL 替换
        # download/callback URL 使用 cfg.BaseURL（NAS 内网 IP），由 Document Server 服务器端调用
        print('Content-Type: text/html; charset=utf-8\n')
        print(html)
    sys.exit(0)

result = subprocess.run(['curl','-s',f'{connector_base}/?api_base={api_base}&dir={urllib.parse.quote(user_dir)}&user_name={urllib.parse.quote(user_name)}&user_id={user_id}&is_admin={is_admin}'], capture_output=True, text=True, timeout=10)
print('Content-Type: text/html; charset=utf-8\n')
print(result.stdout)
