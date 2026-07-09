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

connector_base = f'http://127.0.0.1:10088'

# ---- 通用 API 代理：所有 /api/* 请求通过 CGI 内部转发 ----
# 前端 JS 调用 fetch(apiBase + "/api/history?...")
# 实际发起的请求是 fetch("/cgi/.../index.cgi?action=api&path=/api/history&...")
if action == 'api':
    api_path = params.get('path', [''])[0]
    if not api_path.startswith('/api/'):
        print('Status: 400')
        print('Content-Type: application/json\n')
        print('{"error":"invalid path"}')
        sys.exit(0)

    # 转发 HTTP 方法（保留 POST body）
    method = os.environ.get('REQUEST_METHOD', 'GET').upper()
    body_bytes = b''
    content_length = os.environ.get('CONTENT_LENGTH', '0')
    if content_length and content_length.isdigit() and int(content_length) > 0:
        body_bytes = sys.stdin.buffer.read(int(content_length))

    # 重建 query string（去掉 action 和 path 参数，保留其余参数）
    orig_qs = os.environ.get('QUERY_STRING', '')
    new_qs = []
    for part in orig_qs.split('&'):
        if part.startswith('action=') or part.startswith('path='):
            continue
        new_qs.append(part)
    qs_suffix = ('?' + '&'.join(new_qs)) if new_qs else ''

    url = f'{connector_base}{api_path}{qs_suffix}'

    # 根据方法使用 curl 或 urllib
    result = subprocess.run(['curl', '-s', '-X', method, '--data-binary', '@-', url],
                          input=body_bytes, capture_output=True, timeout=30)

    ct = result.stdout
    if not ct:
        ct = b'null'
    print('Content-Type: application/json')
    print()
    sys.stdout.buffer.write(ct)
    sys.exit(0)

# ---- 代理 OnlyOffice JS/CSS ----
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

# ---- 新建文档 ----
if action == 'create':
    doc_type = params.get('type', ['docx'])[0]
    result = subprocess.run(['curl','-s','-X','POST',f'{connector_base}/api/create?type={doc_type}&dir={urllib.parse.quote(user_dir)}'], capture_output=True, text=True, timeout=10)
    try: data = json.loads(result.stdout.strip()); new_path = data.get('path', result.stdout.strip())
    except: new_path = result.stdout.strip()
    print(f'Location: /cgi/ThirdParty/OfficeEditor/index.cgi?path={urllib.parse.quote(new_path)}')
    print('Status: 302\n')
    sys.exit(0)

# ---- 编辑器页面 ----
if file_path:
    encoded = urllib.parse.quote(file_path)
    editor_url = f'{connector_base}/editor?path={encoded}'
    if user_id: editor_url += f'&user_id={urllib.parse.quote(user_id)}'
    if user_name: editor_url += f'&user_name={urllib.parse.quote(user_name)}'
    result = subprocess.run(['curl','-s',editor_url], capture_output=True, text=True, timeout=10)
    html = result.stdout
    if result.returncode != 0 or not html.strip():
        print('Content-Type: text/html; charset=utf-8\n')
        print('<html><body><h1>错误</h1><p>无法连接到编辑器服务</p></body></html>')
    else:
        print('Content-Type: text/html; charset=utf-8\n')
        print(html)
    sys.exit(0)

# ---- 首页（通过 action=api 代理方式）----
cgi_self = '/cgi/ThirdParty/OfficeEditor/index.cgi'
api_base = f'{cgi_self}?action=api&path='

result = subprocess.run(['curl','-s',f'{connector_base}/?api_base={urllib.parse.quote(api_base, safe="")}&dir={urllib.parse.quote(user_dir)}&user_name={urllib.parse.quote(user_name)}&user_id={user_id}&is_admin={is_admin}'], capture_output=True, text=True, timeout=10)
print('Content-Type: text/html; charset=utf-8\n')
print(result.stdout)
