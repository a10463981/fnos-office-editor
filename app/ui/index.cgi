#!/usr/bin/env python3
"""
FNOS OfficeEditor CGI - 身份透传 + 同源代理
==============================================
关键设计：
- FNOS Gateway / APP 通过 HTTP_X_TRIM_USERID / HTTP_X_TRIM_USERNAME 等头部透传身份
- CGI 内部向本地 Connector (port 10088) 调用时必须把 X-Trim-* 头一并发过去，
  Connector 才会读到真实用户，不会变成 guest_xxx
- API proxy 必须用绝对 URL + 头转发，不能仅靠 query string
"""
import os, urllib.parse, subprocess, sys, json, re

# OnlyOffice 文件根目录（Docker 容器内路径）
OFFICEDS_ROOT = '/var/www/onlyoffice/documentserver/web-apps'

# 解析 query
qs = os.environ.get('QUERY_STRING', '')
params = urllib.parse.parse_qs(qs)
file_path = params.get('path', [None])[0]
action    = params.get('action', [''])[0]

# 身份读取（来自 FNOS Gateway / APP 透传）
user_id   = os.environ.get('HTTP_X_TRIM_USERID', 'anonymous')
user_name = os.environ.get('HTTP_X_TRIM_USERNAME', '') or 'FNos 用户'
is_admin  = os.environ.get('HTTP_X_TRIM_ISADMIN', 'false')

# user_dir 仍然给 fallback（可能 user_id == 'anonymous'）
user_dir  = f'/vol1/{user_id}' if user_id and user_id not in ('anonymous', 'guest') else '/vol1/1000'

connector_base = 'http://127.0.0.1:10088'
cgi_self       = '/cgi/ThirdParty/OfficeEditor/index.cgi'
request_host   = os.environ.get('HTTP_HOST', '127.0.0.1').split(':')[0]

# 所有 curl 子进程都要带的身份头
trim_headers = [
    '-H', f'X-Trim-UserID: {user_id}',
    '-H', f'X-Trim-Username: {user_name}',
    '-H', f'X-Trim-IsAdmin: {is_admin}',
    '-H', 'X-FNOS-UserID: '+user_id,
    '-H', 'X-FNOS-Username: '+user_name,
]

def cgi_print(content_type, body=b'', status='200 OK'):
    """统一 CGI 输出：显式 Status 头 + Content-Type + 空行 + body"""
    sys.stdout.buffer.write(f'Status: {status}\r\n'.encode())
    sys.stdout.buffer.write(f'Content-Type: {content_type}\r\n\r\n'.encode())
    if isinstance(body, str):
        body = body.encode('utf-8', errors='replace')
    sys.stdout.buffer.write(body)
    sys.stdout.buffer.flush()

def cgi_error(status, msg):
    cgi_print('text/plain; charset=utf-8', msg.encode(), status)
    sys.exit(0)

# ---- OnlyOffice JS/CSS 同源代理 ----
if action == 'officeds':
    raw_path = params.get('path', [''])[0]
    if not raw_path:
        cgi_error('404 Not Found', 'missing path')
    # 拆掉 query 部分（path 可能含 ?_dc=...&lang=zh）
    if '?' in raw_path:
        backend_path, backend_qs = raw_path.split('?', 1)
    else:
        backend_path, backend_qs = raw_path, ''
    # 清理 fragment
    if '#' in backend_path:
        backend_path = backend_path.split('#', 1)[0]
    # 兼容 /officeds/web-apps/... 与 /web-apps/...
    if '/officeds/' in raw_path:
        backend_path = raw_path.split('/officeds/', 1)[1]
        # 如果 split 后的部分还有 ?，已经在上面对 raw_path 拆过，但这里可能残留
        if '?' in backend_path:
            backend_path = backend_path.split('?', 1)[0]
    else:
        backend_path = backend_path.lstrip('/')
    # ---- Path Resolver: normalize 路径，防止 ../ 逃逸并处理 RequireJS 动态模块路径 ----
    # 1) normpath 把 main/../common/ 或 main/../../../vendor/ 规范化为干净路径
    # 2) 安全校验：normpath 后不能以 ../ 开头（路径穿越保护）
    # 3) 映射到容器内真实文件路径做存在性校验
    backend_normed = os.path.normpath(backend_path)
    # 相对路径 normpath 后不会以 / 开头（如 "a/b/c"），不需要加前缀
    # 安全校验
    if backend_normed.startswith('..'):
        cgi_error('403 Forbidden', 'path traversal denied')
    # 可选：限制最大目录深度（防止超长路径 DoS），这里不做，交给 curl 超时
    # ---- 尝试容器内实际文件路径 ----
    real_file = os.path.join(OFFICEDS_ROOT, backend_normed)
    # 如果文件不存在且不是 .gz 结尾，尝试 .gz 版本
    if not os.path.isfile(real_file) and not real_file.endswith('.gz'):
        real_gz = real_file + '.gz'
        if os.path.isfile(real_gz):
            real_file = real_gz
            backend_normed = backend_normed + '.gz'  # 告诉下游按 .gz 处理
    # 仍然不存在 → 回退到原始路径（curl --path-as-is 让 OnlyOffice nginx 处理）
    if not os.path.isfile(real_file):
        # 回退：用 normpath 后的路径（去掉 ..）但保留文件名
        # 这比原始路径更干净，OnlyOffice nginx 可能处理更好
        pass  # 保持 backend_normed 不变，curl 会用这个路径
    # 注意：实际不需要把 query 传给 onlyoffice，因为 onlyoffice 用 ?lang= 等
    # 我们把 query 一起转发，让 onlyoffice 自己解析
    try:
        # 用 normpath 后的干净路径请求 OnlyOffice
        target_url = f'http://127.0.0.1:9080/{backend_normed}'
        if backend_qs:
            target_url += '?' + backend_qs
        result = subprocess.run(
            ['curl', '-s', '--max-time', '30', '-H', 'Accept-Encoding: identity', target_url],
            capture_output=True, timeout=30
        )
    except subprocess.TimeoutExpired:
        cgi_error('504 Gateway Timeout', 'onlyoffice timeout')
    if result.returncode != 0:
        cgi_error('502 Bad Gateway', 'onlyoffice error')
    # 如果是 .gz 文件，解压
    if real_file.endswith('.gz'):
        try:
            import gzip
            result.stdout = gzip.decompress(result.stdout)
        except Exception:
            pass
    ct = 'application/octet-stream'
    if backend_normed.endswith('.js'):         ct = 'application/javascript'
    elif backend_normed.endswith('.css'):     ct = 'text/css'
    elif backend_normed.endswith('.wasm'):    ct = 'application/wasm'
    elif backend_normed.endswith('.json'):    ct = 'application/json'
    elif backend_normed.endswith('.svg'):     ct = 'image/svg+xml'
    elif backend_normed.endswith('.png'):     ct = 'image/png'
    elif backend_normed.endswith('.jpg') or backend_normed.endswith('.jpeg'): ct = 'image/jpeg'
    elif backend_normed.endswith('.gif'):     ct = 'image/gif'
    elif backend_normed.endswith('.ico'):     ct = 'image/x-icon'
    elif backend_normed.endswith('.html') or backend_normed.endswith('.htm'): ct = 'text/html; charset=utf-8'
    elif backend_normed.endswith('.xml'):     ct = 'application/xml'
    elif backend_normed.endswith('.pdf'):     ct = 'application/pdf'
    elif backend_normed.endswith('.txt'):     ct = 'text/plain; charset=utf-8'
    elif backend_normed.endswith('.woff'):    ct = 'font/woff'
    elif backend_normed.endswith('.woff2'):   ct = 'font/woff2'
    elif backend_normed.endswith('.ttf'):     ct = 'font/ttf'
    elif backend_normed.endswith('.eot'):     ct = 'application/vnd.ms-fontobject'
    elif backend_normed.endswith('.mp4'):     ct = 'video/mp4'
    elif backend_normed.endswith('.webm'):    ct = 'video/webm'
    elif backend_normed.endswith('.mp3'):     ct = 'audio/mpeg'
    # /sponsor/ 通常是真实 PNG（没有扩展名），用 magic bytes 嗅探
    if ct == 'application/octet-stream' and result.stdout[:8]:
    	# PNG magic: 89 50 4E 47 0D 0A 1A 0A
    	if result.stdout[:8] == b'\x89PNG\r\n\x1a\n':
    		ct = 'image/png'
    	elif result.stdout[:3] == b'GIF':
    		ct = 'image/gif'
    	elif result.stdout[:2] == b'\xff\xd8':
    		ct = 'image/jpeg'
    	elif result.stdout[:5] == b'<!DOC' or result.stdout[:5] == b'<html' or result.stdout[:6] == b'<?xml ':
    		ct = 'text/html; charset=utf-8'
    	elif result.stdout[:1] == b'{' or result.stdout[:1] == b'[':
    		ct = 'application/json'

    # 如果是 HTML 响应，CGI 注入 base + 改写资源路径
    # ----------------------------------------------------------------------------
    # OnlyOffice HTML 内是相对路径 (../../../apps/.../app.css)，基于它假设的
    # baseUrl = web-apps/。但浏览器看到的当前 URL 是 CGI 同源代理，所以相对路径会算错。
    #
    # 解决策略：
    # 1) base_dir = this html 文件所在的 onlyoffice 目录（去掉 index.html）
    # 2) 把所有 relative src/href 重写为经过 CGI 的绝对 URL
    # 3) require.js 没有 data-main → baseUrl 默认 require.js 所在目录，
    #    找不到 'app' 模块。注入 require.config({ baseUrl }) 块解决。
    # ----------------------------------------------------------------------------
    if ct.startswith('text/html') and result.stdout:
        body = result.stdout.decode('utf-8', errors='replace')
        idx = backend_normed.rfind('/')
        base_dir = '/officeds/' + backend_normed[:idx+1] if '/' in backend_normed else '/officeds/'
        base_cgi = f'/cgi/ThirdParty/OfficeEditor/index.cgi?action=officeds&path={urllib.parse.quote(base_dir, safe="")}'

        # 1) 改写 <script src=".."> <link href="..">
        #    任何相对 URL 都替换为通过 CGI 的绝对 URL
        #    rewrite_url 内部不做 normpath（CGI 端 path resolver 会处理）
        def rewrite_url(m):
            attr = m.group(1)        # 包括 src=, href=, action=
            url_val = m.group(3)     # 去引号的 URL 内容
            quote = m.group(2)
            if not url_val or url_val.startswith(('http://','https://','data:','mailto:','#','/cgi/')):
                return m.group(0)
            abs_target = base_dir + url_val
            # 用 os.path.normpath 做路径规范化（替代手写的 seg 循环）
            abs_norm = os.path.normpath(abs_target.lstrip('/'))
            if abs_norm.startswith('..'):
                # 路径穿越 → 给原始值让 OnlyOffice nginx 处理
                clean = abs_target
            else:
                clean = '/' + abs_norm
            new_url = f'/cgi/ThirdParty/OfficeEditor/index.cgi?action=officeds&path={urllib.parse.quote(clean, safe="")}'
            return f'{attr}{quote}{new_url}{quote}'

        # 2) 处理 require.js —— 构造正确的 CGI URL
        #    注意：OnlyOffice 9.4 自己有 require.config()，data-main 只影响首次加载
        #    用 normpath 后的干净路径
        if '/documenteditor/' in base_dir or '/documenteditor/' in backend_normed:
            editor_base = f'/officeds/{backend_normed.split("/", 1)[0]}/web-apps/apps/documenteditor/'
        elif '/spreadsheeteditor/' in base_dir or '/spreadsheeteditor/' in backend_normed:
            editor_base = f'/officeds/{backend_normed.split("/", 1)[0]}/web-apps/apps/spreadsheeteditor/'
        elif '/presentationeditor/' in base_dir or '/presentationeditor/' in backend_normed:
            editor_base = f'/officeds/{backend_normed.split("/", 1)[0]}/web-apps/apps/presentationeditor/'
        else:
            editor_base = ''
        # 构造完整的 require.js 和 app.js CGI URL（路径都放在同一个 quote 中）
        require_path = editor_base + 'main/vendor/requirejs/require.js' if editor_base else ''
        app_path = editor_base + 'main/app.js' if editor_base else ''
        require_cgi = f'/cgi/ThirdParty/OfficeEditor/index.cgi?action=officeds&path={urllib.parse.quote(require_path, safe="/")}' if require_path else ''
        app_cgi = f'/cgi/ThirdParty/OfficeEditor/index.cgi?action=officeds&path={urllib.parse.quote(app_path, safe="/")}' if app_path else ''
        # 把 require.js script 改为完整 CGI 路径
        old_require = '<script src="../../../vendor/requirejs/require.js"></script>'
        new_require = f'<script src="{require_cgi}"></script>'
        body = body.replace(old_require, new_require, 1)

        # 3) 现在做 URL 重写
        body = re.sub(r'((?:src|href|action)=)(["\'])([^"\']+)\2', rewrite_url, body)

        # 4) 注入 <base href> —— 让其它相对 URL 用 base 解析（HTML 标签属性）
        base_tag = f'<base href="{base_cgi}">'
        if re.search(r'<head[^>]*>', body, re.IGNORECASE):
            body = re.sub(r'(<head[^>]*>)', r'\1' + base_tag, body, count=1, flags=re.IGNORECASE)
        elif re.search(r'<html', body, re.IGNORECASE):
            body = re.sub(r'(<html[^>]*>)', r'\1<head>' + base_tag + '</head>', body, count=1, flags=re.IGNORECASE)
        else:
            body = base_tag + body

        cgi_print(ct, body.encode('utf-8'))
    else:
        cgi_print(ct, result.stdout)
    sys.exit(0)

# ---- API 代理（统一转发到 Connector）----
if action == 'api':
    api_path_raw = params.get('path', [''])[0]
    # 兼容性：前端把参数误放在 path 值里，形如 '/api/create?type=docx&dir=...'
    # 拆出真正的 path 和附加 query
    if '?' in api_path_raw:
        api_path, nested_qs = api_path_raw.split('?', 1)
        nested_qs = '&' + nested_qs.lstrip('&')
    else:
        api_path = api_path_raw
        nested_qs = ''
    if '#' in api_path:
        api_path = api_path.split('#', 1)[0]
    api_path = api_path.strip()
    if not (api_path.startswith('/api/') or api_path.startswith('/sponsor/')):
        cgi_error('400 Bad Request', '{"error":"invalid path"}')
    method  = os.environ.get('REQUEST_METHOD', 'GET').upper()
    # body
    body_bytes = b''
    cl = os.environ.get('CONTENT_LENGTH', '0')
    if cl and cl.isdigit() and int(cl) > 0:
        try:
            body_bytes = sys.stdin.buffer.read(int(cl))
        except Exception:
            pass
    # 保留其他 query 参数（如 user_id/user_name 兼容旧版调用）
    orig_qs = os.environ.get('QUERY_STRING', '')
    extra_qs = '&'.join(
        p for p in orig_qs.split('&')
        if not (p.startswith('action=') or p.startswith('path='))
    )
    target = f'{connector_base}{api_path}'
    qs_combined = nested_qs + ('&' + extra_qs if extra_qs else '')
    qs_combined = qs_combined.lstrip('&')
    if qs_combined:
        target += '?' + qs_combined
    # 转发身份头 + body + method，并通过 -D - 把 connector 的 response header 给我们，
    # 让我们能取 Content-Type 再决定 CGI 输出什么
    cmd = ['curl', '-s', '--max-time', '30', '-X', method,
           '-D', '/tmp/_cgi_hdr.{}'.format(os.getpid())]
    cmd += trim_headers
    if body_bytes:
        cmd += ['--data-binary', '@-']
    cmd.append(target)
    try:
        result = subprocess.run(
            cmd, input=body_bytes, capture_output=True, timeout=30
        )
    except subprocess.TimeoutExpired:
        cgi_error('504 Gateway Timeout', 'connector timeout')
    if result.returncode != 0:
        cgi_error('502 Bad Gateway', f'connector error rc={result.returncode}')
    out = result.stdout if result.stdout else b'null'
    # 读 connector 返回的 header，提取 Content-Type 与 Status
    hdr_path = '/tmp/_cgi_hdr.{}'.format(os.getpid())
    ct_final = None
    status_final = '200 OK'
    if os.path.exists(hdr_path):
        try:
            with open(hdr_path, 'rb') as f:
                hdr_blob = f.read()
            for line in hdr_blob.split(b'\r\n'):
                ll = line.decode('latin-1').strip()
                if ll.lower().startswith('content-type:'):
                    ct_final = ll.split(':', 1)[1].strip()
                elif ll.lower().startswith('http/'):
                    # HTTP/1.1 200 OK
                    parts = ll.split(' ', 2)
                    if len(parts) >= 2 and parts[1].isdigit():
                        status_final = f'{parts[1]} {parts[2] if len(parts)>2 else ""}'.strip()
            os.remove(hdr_path)
        except Exception:
            pass
    if ct_final is None:
        # 嗅探 body
        if out[:8] == b'\x89PNG\r\n\x1a\n':
            ct_final = 'image/png'
        elif out[:3] == b'GIF':
            ct_final = 'image/gif'
        elif out[:2] == b'\xff\xd8':
            ct_final = 'image/jpeg'
        elif out and out[0:1] in (b'{', b'['):
            ct_final = 'application/json'
        else:
            ct_final = 'application/octet-stream'
    sys.stdout.buffer.write(f'Status: {status_final}\r\n'.encode())
    sys.stdout.buffer.write(f'Content-Type: {ct_final}\r\n\r\n'.encode())
    sys.stdout.buffer.write(out)
    sys.stdout.buffer.flush()
    sys.exit(0)

# ---- 新建文档 ----
if action == 'create':
    doc_type = params.get('type', ['docx'])[0]
    try:
        r = subprocess.run(
            ['curl', '-s', '-X', 'POST', '--max-time', '10'] + trim_headers +
            [f'{connector_base}/api/create?type={doc_type}&dir={urllib.parse.quote(user_dir)}'],
            capture_output=True, timeout=10
        )
    except subprocess.TimeoutExpired:
        cgi_error('504 Gateway Timeout', 'connector timeout')
    try:
        d = json.loads(r.stdout.strip())
        np = d.get('path', r.stdout.strip().decode('utf-8', 'replace'))
    except Exception:
        np = r.stdout.strip().decode('utf-8', 'replace')
    sys.stdout.buffer.write(f'Status: 302 Found\r\n'.encode())
    sys.stdout.buffer.write(f'Location: {cgi_self}?path={urllib.parse.quote(np)}\r\n\r\n'.encode())
    sys.exit(0)

# ---- 编辑器页面 ----
if file_path:
    encoded = urllib.parse.quote(file_path, safe='')
    qs_parts = [f'path={encoded}']
    qs_parts.append(f'cgi_base={urllib.parse.quote(cgi_self, safe="")}')
    # user_id/user_name 也作为 query 兜底（CGI 链接被外部工具直接打开时仍能工作）
    if user_id and user_id != 'anonymous':
        qs_parts.append(f'user_id={urllib.parse.quote(user_id)}')
    if user_name:
        qs_parts.append(f'user_name={urllib.parse.quote(user_name)}')
    editor_url = f'{connector_base}/editor?{"&".join(qs_parts)}'
    cmd = ['curl', '-s', '--max-time', '10'] + trim_headers + [editor_url]
    try:
        result = subprocess.run(cmd, capture_output=True, text=True, timeout=10)
    except subprocess.TimeoutExpired:
        cgi_error('504 Gateway Timeout', 'connector timeout')
    if result.returncode != 0 or not result.stdout or not result.stdout.strip():
        sys.stdout.buffer.write(b'Status: 502 Bad Gateway\r\n')
        sys.stdout.buffer.write(b'Content-Type: text/html; charset=utf-8\r\n\r\n')
        sys.stdout.buffer.write(b'<html><body><h1>502</h1><p>Cannot reach connector</p></body></html>')
        sys.exit(0)
    # 将 api.js URL 替换为 CGI 同源代理路径
    html = result.stdout
    cgi_api_url = f'{cgi_self}?action=officeds&path='
    replaced = False
    for pattern in [
        'src="/officeds/',
        f'src="http://{request_host}:10088/officeds/',
        f'src="http://{request_host}:9080/officeds/',
    ]:
        if pattern in html:
            html = html.replace(pattern, f'src="{cgi_api_url}', 1)
            replaced = True
            break
    sys.stdout.buffer.write(b'Status: 200 OK\r\n')
    sys.stdout.buffer.write(b'Content-Type: text/html; charset=utf-8\r\n\r\n')
    sys.stdout.buffer.write(html.encode('utf-8', errors='replace'))
    sys.exit(0)

# ---- 首页 ----
api_base_q  = urllib.parse.quote(f'{cgi_self}?action=api&path=', safe='')
dir_q       = urllib.parse.quote(user_dir, safe='')
user_name_q = urllib.parse.quote(user_name, safe='')
home_url = (
    f'{connector_base}/?api_base={api_base_q}'
    f'&dir={dir_q}&user_name={user_name_q}'
    f'&user_id={urllib.parse.quote(user_id)}&is_admin={is_admin}'
)
cmd = ['curl', '-s', '--max-time', '10'] + trim_headers + [home_url]
try:
    result = subprocess.run(cmd, capture_output=True, text=True, timeout=10)
except subprocess.TimeoutExpired:
    cgi_error('504 Gateway Timeout', 'connector timeout')
sys.stdout.buffer.write(b'Status: 200 OK\r\n')
sys.stdout.buffer.write(b'Content-Type: text/html; charset=utf-8\r\n\r\n')
if result.stdout:
    sys.stdout.buffer.write(result.stdout.encode('utf-8', errors='replace'))
sys.exit(0)
