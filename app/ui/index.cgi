#!/usr/bin/env python3
"""FNos OfficeEditor CGI Reverse Proxy

完整反向代理架构:
  FNOS Gateway → CGI (身份桥) → Connector :10088 → OnlyOffice :9080

所有浏览器请求都通过此脚本，确保 FNOS 用户身份始终注入。
"""
import os, urllib.parse, subprocess, sys

# ---- FNOS 用户身份（CGI 环境变量，由 FNOS Gateway 注入） ----
user_id   = os.environ.get('HTTP_X_TRIM_USERID', '')
user_name = os.environ.get('HTTP_X_TRIM_USERNAME', '')
is_admin  = os.environ.get('HTTP_X_TRIM_ISADMIN', 'false')

connector_base = 'http://127.0.0.1:10088'
cgi_script = '/cgi/ThirdParty/OfficeEditor/index.cgi'

# ---- 解析请求 ----
qs = os.environ.get('QUERY_STRING', '')
params = urllib.parse.parse_qs(qs)
file_path = params.get('path', [None])[0]
action = params.get('action', [''])[0]

# ---- 辅助函数：请求体读取 ----
def read_body():
    cl = os.environ.get('CONTENT_LENGTH', '0')
    if cl and cl.isdigit() and int(cl) > 0:
        return sys.stdin.buffer.read(int(cl))
    return b''

# ---- 辅助函数：反向代理请求到 Connector ----
def proxy_to_connector(method, path, query='', body=b'', ct='application/json', add_user=True):
    """代理 HTTP 请求到 Connector，返回 (status, headers_dict, body_bytes)"""
    url = f'{connector_base}{path}'
    if query:
        url += '?' + query

    curl_cmd = ['curl', '-s', '-w', '%{http_code}', '-o', '-', '-X', method]
    # 透传 Content-Type
    if ct:
        curl_cmd.extend(['-H', f'Content-Type: {ct}'])
    # 注入 FNOS 用户身份
    if add_user:
        if user_id:
            curl_cmd.extend(['-H', f'X-FNOS-UserID: {user_id}', '-H', f'X-Trim-UserID: {user_id}'])
        if user_name:
            curl_cmd.extend(['-H', f'X-FNOS-Username: {user_name}', '-H', f'X-Trim-Username: {user_name}'])
    # POST body
    if body:
        curl_cmd.extend(['--data-binary', '@-'])
    curl_cmd.append(url)

    # 使用管道模式获取返回码
    result = subprocess.run(curl_cmd, input=body, capture_output=True, timeout=60)
    stdout = result.stdout

    # 从响应体分离 HTTP 状态码（最后 3 位）
    if len(stdout) >= 3:
        try:
            status = int(stdout[-3:].decode())
            resp_body = stdout[:-3]
        except (ValueError, UnicodeDecodeError):
            status = 200
            resp_body = stdout
    else:
        status = 200
        resp_body = stdout

    return status, resp_body


# ============================================================
# 路由分发
# ============================================================

method = os.environ.get('REQUEST_METHOD', 'GET').upper()
body = read_body()
ct = os.environ.get('CONTENT_TYPE', '')

# ---- 1. API 代理 ----
if action == 'api':
    api_path = params.get('path', [''])[0]
    if not api_path.startswith('api/') and not api_path.startswith('/api/'):
        print('Content-Type: application/json')
        print('Status: 400\n')
        print('{"error":"invalid path"}')
        sys.exit(0)
    api_path = api_path.lstrip('/')
    # 分离 path 自身携带的 query 参数
    if '?' in api_path:
        path_part, extra_qs = api_path.split('?', 1)
    else:
        path_part, extra_qs = api_path, ''
    # 收集非 action/path 的原始 query 参数
    remaining = [p for p in qs.split('&') if not p.startswith('action=') and not p.startswith('path=')]
    all_qs_parts = []
    if extra_qs:
        all_qs_parts.append(extra_qs)
    all_qs_parts.extend(remaining)
    final_qs = '&'.join(all_qs_parts)

    status, resp_body = proxy_to_connector(method, '/' + path_part, final_qs, body, ct)
    print(f'Status: {status}')
    print('Content-Type: application/json')
    print()
    sys.stdout.buffer.write(resp_body)
    sys.exit(0)

# ---- 2. ooffice ds 代理（OnlyOffice JS/CSS/WASM） ----
if action == 'officeds':
    raw_path = params.get('path', [''])[0]
    if '/officeds/' in raw_path:
        backend_path = raw_path.split('/officeds/', 1)[1]
    else:
        backend_path = raw_path.lstrip('/')
    status, resp_body = proxy_to_connector('GET', '/' + backend_path, '', b'', '')
    # 确定 Content-Type
    if backend_path.endswith('.js'):
        ct = 'application/javascript'
    elif backend_path.endswith('.css'):
        ct = 'text/css'
    elif backend_path.endswith('.wasm'):
        ct = 'application/wasm'
    elif backend_path.endswith('.png'):
        ct = 'image/png'
    elif backend_path.endswith('.svg'):
        ct = 'image/svg+xml'
    elif backend_path.endswith('.html'):
        ct = 'text/html; charset=utf-8'
    else:
        ct = 'application/octet-stream'
    print(f'Status: {status}')
    print(f'Content-Type: {ct}')
    print()
    sys.stdout.buffer.write(resp_body)
    sys.exit(0)

# ---- 3. 新建文档（CGI 快捷操作） ----
if action == 'create':
    doc_type = params.get('type', ['docx'])[0]
    user_dir = f'/vol1/{user_id}' if user_id else '/vol1/1000'
    api_qs = f'type={doc_type}&dir={urllib.parse.quote(user_dir)}'
    if user_id:
        api_qs += f'&user_id={urllib.parse.quote(user_id)}'
    status, resp_body = proxy_to_connector('POST', '/api/create', api_qs, b'', 'application/json')
    if status == 200:
        import json
        try:
            d = json.loads(resp_body.decode())
            np = d.get('path', resp_body.decode())
        except:
            np = resp_body.decode()
        redirect_url = f'{cgi_script}?path={urllib.parse.quote(np)}'
        if user_id:
            redirect_url += f'&user_id={urllib.parse.quote(user_id)}'
        if user_name:
            redirect_url += f'&user_name={urllib.parse.quote(user_name)}'
        print(f'Location: {redirect_url}')
        print('Status: 302')
        print()
    else:
        print(f'Status: {status}')
        print('Content-Type: application/json')
        print()
        sys.stdout.buffer.write(resp_body)
    sys.exit(0)

# ---- 4. 编辑器页面 (path=/vol1/xxx) ----
if file_path:
    editor_qs = f'path={urllib.parse.quote(file_path)}'
    if user_id:
        editor_qs += f'&user_id={urllib.parse.quote(user_id)}'
    if user_name:
        editor_qs += f'&user_name={urllib.parse.quote(user_name)}'
    status, resp_body = proxy_to_connector('GET', '/editor', editor_qs, b'', 'text/html')

    if status == 200 and resp_body:
        html = resp_body.decode('utf-8', errors='replace')
        # 将相对路径 officeds/... 改为 CGI 代理路径
        # 使浏览器通过 CGI 加载 OnlyOffice 资源
        html = html.replace(
            'src="officeds/',
            f'src="{cgi_script}?action=officeds&path=officeds/'
        )
        html = html.replace(
            "src='officeds/",
            f"src='{cgi_script}?action=officeds&path=officeds/"
        )
        print('Content-Type: text/html; charset=utf-8')
        print(f'Status: {status}')
        print()
        print(html)
    else:
        print(f'Status: {status}')
        print('Content-Type: text/html; charset=utf-8')
        print()
        sys.stdout.buffer.write(resp_body)
    sys.exit(0)

# ---- 5. 静态资源透传 (.js, .css, 等) ----
ext = os.environ.get('PATH_INFO', '')
if not ext:
    ext = file_path or ''
_, ext_name = os.path.splitext(ext)
if ext_name in ('.js', '.css', '.wasm', '.png', '.svg', '.ico', '.woff', '.woff2', '.ttf'):
    status, resp_body = proxy_to_connector('GET', ext, '', b'', '')
    mime_map = {
        '.js': 'application/javascript',
        '.css': 'text/css',
        '.wasm': 'application/wasm',
        '.png': 'image/png',
        '.svg': 'image/svg+xml',
        '.ico': 'image/x-icon',
        '.woff': 'font/woff',
        '.woff2': 'font/woff2',
        '.ttf': 'font/ttf',
    }
    ct = mime_map.get(ext_name, 'application/octet-stream')
    print(f'Status: {status}')
    print(f'Content-Type: {ct}')
    print()
    sys.stdout.buffer.write(resp_body)
    sys.exit(0)

# ---- 6. 首页（默认行为） ----
home_qs = ''
if user_id:
    home_qs += f'user_id={urllib.parse.quote(user_id)}&'
if user_name:
    home_qs += f'user_name={urllib.parse.quote(user_name)}&'
if is_admin:
    home_qs += f'is_admin={is_admin}&'
# apiBase: 前端 JS 通过此 base 调用 CGI API 代理
api_base_url = f'{cgi_script}?action=api&path='
home_qs += f'api_base={urllib.parse.quote(api_base_url, safe="")}'

status, resp_body = proxy_to_connector('GET', '/', home_qs, b'', 'text/html')
print('Content-Type: text/html; charset=utf-8')
print(f'Status: {status}')
print()
sys.stdout.buffer.write(resp_body)
