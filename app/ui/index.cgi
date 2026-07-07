#!/usr/bin/env python3
import os, urllib.parse, subprocess, re, sys, json

qs = os.environ.get('QUERY_STRING', '')
params = urllib.parse.parse_qs(qs)
file_path = params.get('path', [None])[0]
action = params.get('action', [''])[0]
path_info = os.environ.get('PATH_INFO', '')
request_uri = os.environ.get('REQUEST_URI', '')

user_id   = os.environ.get('HTTP_X_TRIM_USERID', '')
user_name = os.environ.get('HTTP_X_TRIM_USERNAME', '')
user_dir  = f'/vol1/{user_id}' if user_id else '/vol1/1000'

referer = os.environ.get('HTTP_REFERER', '')
fnos_host = '127.0.0.1'
m = re.search(r'https?://([^/:]+)', referer)
if m: fnos_host = m.group(1)
connector_base = f'http://127.0.0.1:10088'

# ---- API 代理：/api/create ----
if '/api/create' in request_uri:
    doc_type = params.get('type', ['docx'])[0]
    dir_param = params.get('dir', [user_dir])[0]
    result = subprocess.run(
        ['curl', '-s', '-X', 'POST', f'{connector_base}/api/create?type={doc_type}&dir={urllib.parse.quote(dir_param)}'],
        capture_output=True, text=True, timeout=10
    )
    print('Content-Type: application/json')
    print()
    print(result.stdout.strip())
    sys.exit(0)

# ---- API 代理：/api/history ----
if '/api/history' in request_uri:
    result = subprocess.run(
        ['curl', '-s', f'{connector_base}/api/history'],
        capture_output=True, text=True, timeout=10
    )
    print('Content-Type: application/json')
    print()
    print(result.stdout.strip())
    sys.exit(0)

# ---- 首页创建文档（action=create）----
if action == 'create':
    doc_type = params.get('type', ['docx'])[0]
    result = subprocess.run(
        ['curl', '-s', '-X', 'POST', f'{connector_base}/api/create?type={doc_type}&dir={urllib.parse.quote(user_dir)}'],
        capture_output=True, text=True, timeout=10
    )
    try:
        data = json.loads(result.stdout.strip())
        new_path = data.get('path', result.stdout.strip())
    except:
        new_path = result.stdout.strip()
    print(f'Location: /cgi/ThirdParty/OfficeEditor/index.cgi?path={urllib.parse.quote(new_path)}')
    print('Status: 302')
    print()
    sys.exit(0)

# ---- 打开文件编辑 ----
if file_path:
    encoded = urllib.parse.quote(file_path)
    editor_url = f'{connector_base}/editor?path={encoded}&host={fnos_host}'
    if user_id: editor_url += f'&user_id={urllib.parse.quote(user_id)}'
    if user_name: editor_url += f'&user_name={urllib.parse.quote(user_name)}'
    result = subprocess.run(['curl', '-s', editor_url], capture_output=True, text=True, timeout=10)
    html = result.stdout
    if result.returncode != 0 or not html.strip():
        print('Content-Type: text/html; charset=utf-8')
        print()
        print('<html><body><h1>错误</h1><p>无法连接到编辑器服务</p></body></html>')
    else:
        html = html.replace('127.0.0.1:9080', f'{fnos_host}:9080')
        html = html.replace('localhost:10088', f'{fnos_host}:10088')
        print('Content-Type: text/html; charset=utf-8')
        print()
        print(html)
    sys.exit(0)

# ---- 首页 ----
result = subprocess.run(
    ['curl', '-s', f'{connector_base}/?dir={urllib.parse.quote(user_dir)}&user_name={urllib.parse.quote(user_name)}'],
    capture_output=True, text=True, timeout=10
)
html = result.stdout
print('Content-Type: text/html; charset=utf-8')
print()
print(html)
