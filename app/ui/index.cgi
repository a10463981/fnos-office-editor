#!/usr/bin/env python3
"""FNos OfficeEditor CGI Reverse Proxy

二进制透传反向代理，完整保留 Connector 响应头和内容。
"""
import os, sys, subprocess, urllib.parse, json

# ---- FNOS 用户身份（CGI 环境变量，FNOS Gateway 注入） ----
FNOS_USER_ID   = os.environ.get('HTTP_X_TRIM_USERID', '')
FNOS_USER_NAME = os.environ.get('HTTP_X_TRIM_USERNAME', '')
FNOS_IS_ADMIN  = os.environ.get('HTTP_X_TRIM_ISADMIN', 'false')

CONNECTOR = 'http://127.0.0.1:10088'
CGI_SELF   = '/cgi/ThirdParty/OfficeEditor/index.cgi'

# ---- 解析请求 ----
qs   = os.environ.get('QUERY_STRING', '')
par  = urllib.parse.parse_qs(qs)
path = par.get('path', [None])[0]
act  = par.get('action', [''])[0]

METHOD = os.environ.get('REQUEST_METHOD', 'GET').upper()
CL     = os.environ.get('CONTENT_LENGTH', '0')
CT     = os.environ.get('CONTENT_TYPE', '')


def read_body():
    if CL and CL.isdigit() and int(CL) > 0:
        return sys.stdin.buffer.read(int(CL))
    return b''


def proxy(method, path_part, query_str=b'', body=b'', extra_headers=None):
    """二进制透传到 Connector，返回 (status_code, headers_dict, body_bytes)"""
    url = CONNECTOR + path_part
    if query_str:
        url += '?' + (query_str.decode() if isinstance(query_str, bytes) else query_str)

    # 构建 curl args
    curl = ['curl', '-s', '-i', '-X', method, '--max-time', '30']

    # 透传 Content-Type
    ctype = os.environ.get('CONTENT_TYPE', '')
    if ctype:
        curl.extend(['-H', f'Content-Type: {ctype}'])

    # 注入用户身份
    if FNOS_USER_ID:
        curl.extend(['-H', f'X-FNOS-UserID: {FNOS_USER_ID}'])
        curl.extend(['-H', f'X-Trim-UserID: {FNOS_USER_ID}'])
    if FNOS_USER_NAME:
        curl.extend(['-H', f'X-FNOS-Username: {FNOS_USER_NAME}'])
        curl.extend(['-H', f'X-Trim-Username: {FNOS_USER_NAME}'])

    # 额外头
    if extra_headers:
        for k, v in extra_headers.items():
            curl.extend(['-H', f'{k}: {v}'])

    # POST body
    if body:
        curl.extend(['--data-binary', '@-'])

    curl.append(url)

    # 执行
    result = subprocess.run(curl, input=body, capture_output=True, timeout=60)
    raw = result.stdout

    # 分离 HTTP 响应头和体
    # 格式: HTTP/1.1 200 OK\r\nHeaders\r\n\r\nBody
    header_end = raw.find(b'\r\n\r\n')
    if header_end == -1:
        return 502, {'Content-Type': 'text/plain'}, b'upstream error'

    header_block = raw[:header_end].decode('utf-8', errors='replace')
    resp_body = raw[header_end + 4:]

    # 解析状态码
    status_line = header_block.split('\r\n')[0]  # e.g. "HTTP/1.1 200 OK"
    try:
        status = int(status_line.split(' ')[1])
    except (IndexError, ValueError):
        status = 502

    # 解析响应头
    headers = {}
    for line in header_block.split('\r\n')[1:]:
        if ':' in line:
            k, v = line.split(':', 1)
            headers[k.strip().lower()] = v.strip()

    return status, headers, resp_body


def emit(status, headers, body):
    """输出 CGI 响应"""
    print(f'Status: {status}')
    # 透传关键头
    for hdr in ('content-type', 'content-length', 'cache-control', 'location',
                'access-control-allow-origin', 'access-control-allow-headers',
                'access-control-allow-methods', 'x-robots-tag'):
        if hdr in headers:
            val = headers[hdr]
            # 标准化 Content-Type 大小写
            if hdr == 'content-type':
                hdr = 'Content-Type'
            elif hdr == 'content-length':
                hdr = 'Content-Length'
            elif hdr == 'cache-control':
                hdr = 'Cache-Control'
            elif hdr == 'location':
                hdr = 'Location'
            print(f'{hdr}: {val}')
    print()
    sys.stdout.buffer.write(body)


# ========== 读取请求体 ==========
BODY = read_body()

# ========== 路由分发 ==========

# 1. API 代理
if act == 'api':
    api_path = par.get('path', [''])[0]
    if not api_path.startswith('api/') and not api_path.startswith('/api/'):
        emit(400, {'Content-Type': 'application/json'}, b'{"error":"invalid path"}')
        sys.exit(0)
    api_path = api_path.lstrip('/')
    # path 值可能自带 query 参数
    if '?' in api_path:
        path_part, extra_qs = api_path.split('?', 1)
    else:
        path_part, extra_qs = api_path, ''
    remaining = [p for p in qs.split('&') if not p.startswith('action=') and not p.startswith('path=')]
    all_qs = '&'.join(filter(None, [extra_qs] + remaining))
    status, hdrs, body = proxy(METHOD, '/' + path_part, all_qs.encode(), BODY)
    emit(status, hdrs, body)
    sys.exit(0)

# 2. Officeds 资源代理（OnlyOffice JS/CSS/WASM）
if act == 'officeds':
    raw_path = par.get('path', [''])[0]
    # 提取 /officeds/ 后的路径
    if '/officeds/' in raw_path:
        backend = '/' + raw_path.split('/officeds/', 1)[1]
    else:
        # 如果 path=officeds/xxx 格式
        backend = '/' + raw_path
        if backend.startswith('/officeds/'):
            backend = backend[len('/officeds'):]  # 保留后面的路径
    status, hdrs, body = proxy('GET', backend, b'', b'')
    emit(status, hdrs, body)
    sys.exit(0)

# 3. 新建文档
if act == 'create':
    doc_type = par.get('type', ['docx'])[0]
    user_dir = f'/vol1/{FNOS_USER_ID}' if FNOS_USER_ID else '/vol1/1000'
    api_qs = f'type={doc_type}&dir={urllib.parse.quote(user_dir)}'
    if FNOS_USER_ID:
        api_qs += f'&user_id={urllib.parse.quote(FNOS_USER_ID)}'
    status, hdrs, body = proxy('POST', '/api/create', api_qs.encode(), BODY)
    if status == 200 and body:
        try:
            d = json.loads(body.decode())
            np = d.get('path', body.decode())
        except:
            np = body.decode()
        loc = f'{CGI_SELF}?path={urllib.parse.quote(np)}'
        if FNOS_USER_ID:
            loc += f'&user_id={urllib.parse.quote(FNOS_USER_ID)}'
        if FNOS_USER_NAME:
            loc += f'&user_name={urllib.parse.quote(FNOS_USER_NAME)}'
        emit(302, {'Location': loc}, b'')
    else:
        emit(status, hdrs, body)
    sys.exit(0)

# 4. 编辑器页面 (path=/vol1/xxx)
if path:
    ed_qs = f'path={urllib.parse.quote(path)}'
    if FNOS_USER_ID:
        ed_qs += f'&user_id={urllib.parse.quote(FNOS_USER_ID)}'
    if FNOS_USER_NAME:
        ed_qs += f'&user_name={urllib.parse.quote(FNOS_USER_NAME)}'
    status, hdrs, body = proxy('GET', '/editor', ed_qs.encode(), b'')

    if status == 200 and body:
        # 将 officeds 相对路径改为 CGI 代理路径
        html = body.decode('utf-8', errors='replace')
        old = 'src="officeds/'
        new = f'src="{CGI_SELF}?action=officeds&path=officeds/'
        if old in html:
            html = html.replace(old, new)
        # 同样处理单引号
        old2 = "src='officeds/"
        new2 = f"src='{CGI_SELF}?action=officeds&path=officeds/"
        if old2 in html:
            html = html.replace(old2, new2)
        body = html.encode('utf-8')
        hdrs['content-type'] = 'text/html; charset=utf-8'

    emit(status, hdrs, body)
    sys.exit(0)

# 5. 首页（默认路由）
home_qs_parts = []
if FNOS_USER_ID:
    home_qs_parts.append(f'user_id={urllib.parse.quote(FNOS_USER_ID)}')
if FNOS_USER_NAME:
    home_qs_parts.append(f'user_name={urllib.parse.quote(FNOS_USER_NAME)}')
if FNOS_IS_ADMIN:
    home_qs_parts.append(f'is_admin={FNOS_IS_ADMIN}')
api_base = f'{CGI_SELF}?action=api&path='
home_qs_parts.append(f'api_base={urllib.parse.quote(api_base, safe="")}')
home_qs = '&'.join(home_qs_parts)

status, hdrs, body = proxy('GET', '/', home_qs.encode(), b'')
emit(status, hdrs, body)
