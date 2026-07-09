#!/usr/bin/env python3
import os, urllib.parse, subprocess, sys

qs = os.environ.get('QUERY_STRING', '')
params = urllib.parse.parse_qs(qs)
file_path = params.get('path', [None])[0]
action = params.get('action', [''])[0]

user_id   = os.environ.get('HTTP_X_TRIM_USERID', 'anonymous')
user_name = os.environ.get('HTTP_X_TRIM_USERNAME', '')
user_dir  = f'/vol1/{user_id}' if user_id and user_id != 'anonymous' else '/vol1/1000'
is_admin = os.environ.get('HTTP_X_TRIM_ISADMIN', 'false')

connector_base = 'http://127.0.0.1:10088'
cgi_self = '/cgi/ThirdParty/OfficeEditor/index.cgi'
request_host = os.environ.get('HTTP_HOST', '127.0.0.1').split(':')[0]

# ---- OnlyOffice JS/CSS 代理（通过 CGI URL 加载）----
if action == 'officeds':
    raw_path = params.get('path', [''])[0]
    if '/officeds/' in raw_path:
        backend_path = raw_path.split('/officeds/', 1)[1]
    else:
        backend_path = raw_path.lstrip('/')
    result = subprocess.run(['curl', '-s', f'http://127.0.0.1:9080/{backend_path}'], capture_output=True, timeout=30)
    ct = 'application/octet-stream'
    if backend_path.endswith('.js'):    ct = 'application/javascript'
    elif backend_path.endswith('.css'): ct = 'text/css'
    elif backend_path.endswith('.wasm'): ct = 'application/wasm'
    print(f'Content-Type: {ct}')
    print()
    sys.stdout.buffer.write(result.stdout)
    sys.exit(0)

# ---- 通用 API 代理 ----
if action == 'api':
    api_path = params.get('path', [''])[0]
    if not (api_path.startswith('/api/') or api_path.startswith('/sponsor/')):
        print('Content-Type: application/json')
        print('Status: 400\n')
        print('{"error":"invalid path"}')
        sys.exit(0)
    method = os.environ.get('REQUEST_METHOD', 'GET').upper()
    body_bytes = b''
    cl = os.environ.get('CONTENT_LENGTH', '0')
    if cl and cl.isdigit() and int(cl) > 0:
        body_bytes = sys.stdin.buffer.read(int(cl))
    orig_qs = os.environ.get('QUERY_STRING', '')
    new_qs = [p for p in orig_qs.split('&') if not p.startswith('action=') and not p.startswith('path=')]
    qs_sfx = ('?' + '&'.join(new_qs)) if new_qs else ''
    url = f'{connector_base}{api_path}{qs_sfx}'
    result = subprocess.run(['curl', '-s', '-X', method, '--data-binary', '@-', url],
                          input=body_bytes, capture_output=True, timeout=30)
    print('Content-Type: application/json')
    print()
    sys.stdout.buffer.write(result.stdout if result.stdout else b'null')
    sys.exit(0)

# ---- 新建文档 ----
if action == 'create':
    doc_type = params.get('type', ['docx'])[0]
    r = subprocess.run(['curl','-s','-X','POST',f'{connector_base}/api/create?type={doc_type}&dir={urllib.parse.quote(user_dir)}'], capture_output=True, text=True, timeout=10)
    try: d = json.loads(r.stdout.strip()); np = d.get('path', r.stdout.strip())
    except: np = r.stdout.strip()
    print(f'Location: /cgi/ThirdParty/OfficeEditor/index.cgi?path={urllib.parse.quote(np)}')
    print('Status: 302\n')
    sys.exit(0)

# ---- 编辑器页面 ----
if file_path:
    encoded = urllib.parse.quote(file_path)
    editor_url = f'{connector_base}/editor?path={encoded}&cgi_base={urllib.parse.quote(cgi_self, safe="")}'
    if user_id: editor_url += f'&user_id={urllib.parse.quote(user_id)}'
    if user_name: editor_url += f'&user_name={urllib.parse.quote(user_name)}'
    result = subprocess.run(['curl','-s',editor_url], capture_output=True, text=True, timeout=10)
    if result.returncode != 0 or not result.stdout.strip():
        print('Content-Type: text/html; charset=utf-8\n')
        print('<html><body><h1>错误</h1><p>无法连接到编辑器服务</p></body></html>')
    else:
        # 将 api.js URL 替换为 CGI 同源代理路径（避免跨端口加载问题）
        # 浏览器通过 CGI 加载 /cgi/.../?action=officeds&path=/officeds/...
        # CGI 内部转发到 OnlyOffice Document Server (9080)
        html = result.stdout
        # 处理两种可能: 旧版 /officeds/ 或新版 http://IP:10088/officeds/
        cgi_api_url = f'{cgi_self}?action=officeds&path='
        old_patterns = [
            'src="/officeds/',
            f'src="http://{request_host}:10088/officeds/',
        ]
        for pattern in old_patterns:
            if pattern in html:
                html = html.replace(pattern, f'src="{cgi_api_url}')
                break
        print('Content-Type: text/html; charset=utf-8\n')
        print(html)
    sys.exit(0)

# ---- 首页 ----
api_base = f'{cgi_self}?action=api&path='
result = subprocess.run(['curl','-s',f'{connector_base}/?api_base={urllib.parse.quote(api_base, safe="")}&dir={urllib.parse.quote(user_dir)}&user_name={urllib.parse.quote(user_name)}&user_id={user_id}&is_admin={is_admin}'], capture_output=True, text=True, timeout=10)
print('Content-Type: text/html; charset=utf-8\n')
print(result.stdout)
