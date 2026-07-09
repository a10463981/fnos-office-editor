#!/usr/bin/env python3
import os, urllib.parse, subprocess, re, sys, json

qs = os.environ.get('QUERY_STRING', '')
params = urllib.parse.parse_qs(qs)
file_path = params.get('path', [None])[0]
action = params.get('action', [''])[0]

user_id   = os.environ.get('HTTP_X_TRIM_USERID', '')
user_name = os.environ.get('HTTP_X_TRIM_USERNAME', '')
user_dir  = f'/vol1/{user_id}' if user_id else '/vol1/1000'

referer = os.environ.get('HTTP_REFERER', '')
fnos_host = '127.0.0.1'

# ---- 代理 OnlyOffice JS/CSS (支持 FN Connect 远程) ----
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
m = re.search(r'https?://([^/:]+)', referer)
if m: fnos_host = m.group(1)
connector_base = f'http://127.0.0.1:10088'

# ---- 赞助图片 ----
if '/sponsor/' in os.environ.get('REQUEST_URI', ''):
    img_name = 'donate-wechat.png' if 'wechat' in os.environ.get('REQUEST_URI', '') else 'donate-alipay.png'
    img_path = f'{os.environ.get("TRIM_APPDEST", "/var/apps/OfficeEditor/target")}/ui/images/{img_name}'
    if os.path.exists(img_path):
        print(f'Content-Type: image/png')
        print()
        with open(img_path, 'rb') as f:
            sys.stdout.buffer.write(f.read())
    else:
        print('Status: 404')
        print()
    sys.exit(0)
api_base = f'http://{fnos_host}:10088'

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
    editor_url = f'{connector_base}/editor?path={encoded}&host={fnos_host}'
    if user_id: editor_url += f'&user_id={urllib.parse.quote(user_id)}'
    if user_name: editor_url += f'&user_name={urllib.parse.quote(user_name)}'
    result = subprocess.run(['curl','-s',editor_url], capture_output=True, text=True, timeout=10)
    html = result.stdout
    if result.returncode != 0 or not html.strip():
        print('Content-Type: text/html; charset=utf-8\n')
        print('<html><body><h1>错误</h1><p>无法连接到编辑器服务</p></body></html>')
    else:
        html = html.replace('127.0.0.1:9080', f'{fnos_host}:9080')
        html = html.replace('localhost:10088', f'{fnos_host}:10088')
        print('Content-Type: text/html; charset=utf-8\n')
        print(html)
    sys.exit(0)

is_admin = os.environ.get('HTTP_X_TRIM_ISADMIN', 'false')
result = subprocess.run(['curl','-s',f'{connector_base}/?api_base={api_base}&dir={urllib.parse.quote(user_dir)}&user_name={urllib.parse.quote(user_name)}&is_admin={is_admin}'], capture_output=True, text=True, timeout=10)
print('Content-Type: text/html; charset=utf-8\n')
print(result.stdout)
