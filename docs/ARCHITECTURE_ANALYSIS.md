# FNos OfficeEditor 架构分析报告

> 分析日期: 2026-07-14
> 项目: https://github.com/a10463981/fnos-office-editor
> 当前版本: v1.0.29

---

## 1. 当前架构（CGI 时代遗留）

```
                                                    ┌─────────────────────┐
                                                    │   OnlyOffice        │
                                                    │   Document Server   │
                                                    │   Docker            │
                                                    │   127.0.0.1:9080    │
                                                    └────────┬────────────┘
                                                             │
                                         http://127.0.0.1:9080│
                                                             │
┌──────────┐    ┌──────────────┐    ┌────────────────────┐   │
│  Browser │───▶│  FNOS Nginx  │───▶│  Connector (Go)    │───┘
│          │    │  :5666       │    │  :10088            │
│ iframe   │    │              │    │                    │
│ SPA      │    │  CGI OR Port │    │  monolith/server.go│
└──────────┘    └──────────────┘    └────────────────────┘
                       │
                       │ CGI (旧模式)
                       ▼
                ┌──────────────┐
                │  index.cgi   │
                │  (Python)    │
                │  LEGACY      │
                └──────────────┘
```

### 1.1 当前组件职责

| 组件 | 路径 | 职责 | 问题 |
|------|------|------|------|
| FPK Manifest | `manifest` | 应用元数据、端口声明 | 版本不统一 |
| UI Config | `app/ui/config` | FNOS 应用入口配置 | 在端口路由和 URL 路由间反复切换 |
| CGI Handler | `app/ui/index.cgi` | Python CGI 脚本，代理请求到连接器 | **遗留代码**，应删除 |
| Connector | `connector/internal/server/server.go` | 942 行单文件，包含全部逻辑 | **Monolith**，应拆分 |
| Entry Point | `connector/cmd/server/main.go` | 启动参数、环境变量 | 缺少配置文件支持 |
| Docker | `app/docker/docker-compose.yaml` | OnlyOffice DocServer 容器 | 缺少 DOCUMENT_SERVER_PUBLIC_URL |
| Install | `cmd/install_callback` | 安装脚本、nginx 配置、systemd 服务 | nginx 配置未透传身份头 |
| Build | `scripts/build-fpk.sh` | FPK 打包脚本 | 依赖预编译二进制 |

### 1.2 文件结构全景

```
fnos-office-editor/
├── app/
│   ├── connector/officeeditor-connector    # 预编译 Go 二进制
│   ├── docker/docker-compose.yaml          # OnlyOffice Docker 配置
│   ├── templates/new.pptx                  # PPTX 模板
│   ├── ui/
│   │   ├── config                          # FNOS 应用 UI 配置
│   │   ├── index.cgi                       # ⚠️ 遗留 CGI 脚本
│   │   └── images/                         # 图标和赞助码
│   └── www/index.html                      # 静态首页
├── cmd/
│   ├── main                                # systemd start/stop/status
│   ├── install_init / install_callback     # 安装生命周期
│   ├── uninstall_init / uninstall_callback # 卸载生命周期
│   ├── upgrade_init / upgrade_callback     # 升级生命周期
│   └── config_init / config_callback       # 配置生命周期
├── config/
│   ├── privilege                           # 运行权限 (root)
│   └── resource                            # Docker 资源声明
├── connector/
│   ├── go.mod                              # Go 1.22
│   └── internal/server/
│       └── server.go                       # ⚠️ 942 行单文件 monolith
├── scripts/
│   ├── build-fpk.sh                        # FPK 构建
│   ├── build.sh                            # 开发构建
│   └── deploy.sh                           # 部署脚本
└── manifest                                # FPK 应用声明
```

### 1.3 路由表（当前）

| 路径 | 处理方式 | 用途 |
|------|----------|------|
| `/` | mux.HandleFunc | SPA fallback: home page (无 path) 或 editor (有 path) |
| `/health` | mux.HandleFunc | 健康检查 |
| `/api/history` | mux.HandleFunc | 历史记录 |
| `/api/create` | mux.HandleFunc | 创建文档 |
| `/api/config` | mux.HandleFunc (GET/POST) | 配置读写 |
| `/api/version` | mux.HandleFunc | 版本信息 |
| `/api/check-update` | mux.HandleFunc | 更新检查 |
| `/api/editor` | mux.HandleFunc | 编辑器配置(JSON) |
| `/api/download` | mux.HandleFunc | 文件下载 |
| `/api/callback` | mux.HandleFunc | OnlyOffice 保存回调 |
| `/editor` | mux.HandleFunc | 编辑器页面 |
| `/sponsor/` | mux.HandleFunc | 赞助图片 |
| `/officeds/*` | 中间件 → ReverseProxy | OnlyOffice JS/CSS 静态资源 |
| `/cache/*` | 中间件 → ReverseProxy | OnlyOffice 缓存资源 |
| `/officeeditor-api/*` | 中间件 → 前缀剥离 | FNOS nginx 代理入口 |

### 1.4 所有 CGI 依赖点

| 依赖项 | 位置 | 说明 | 处理方案 |
|--------|------|------|----------|
| `HTTP_X_TRIM_USERID` | `ui/index.cgi:9` | CGI 环境变量传递用户 ID | 改为 `X-Trim-UserID` HTTP 头 |
| `HTTP_X_TRIM_USERNAME` | `ui/index.cgi:10` | CGI 环境变量传递用户名 | 改为 `X-Trim-Username` HTTP 头 |
| `HTTP_X_TRIM_ISADMIN` | `ui/index.cgi:12` | CGI 环境变量传递管理员状态 | 改为 `X-Trim-IsAdmin` HTTP 头 |
| `QUERY_STRING` | `ui/index.cgi:5` | CGI 查询字符串 | 标准 HTTP query |
| `REQUEST_METHOD` | `ui/index.cgi:43` | CGI 请求方法 | 标准 HTTP method |
| `CONTENT_LENGTH` | `ui/index.cgi:45` | CGI 内容长度 | 标准 HTTP Content-Length |
| `index.cgi` | `app/ui/index.cgi` | 完整 CGI 代理 | 应删除，已不需要 |
| `cgi_self` | `ui/index.cgi:15` | CGI 自身路径用于 URL 构建 | 已移除 |
| `/cgi/ThirdParty/OfficeEditor/index.cgi` | `app/ui/config` (旧) | CGI 路径路由 | 已改为端口路由 |
| `?user_id=` | 多处 JS + Go Handler | CGI 通过 query 传递用户 ID | 改为 Header 优先 |
| `?user_name=` | 多处 JS + Go Handler | CGI 通过 query 传递用户名 | 改为 Header 优先 |
| `?dir=` | `handleHomePage` | CGI 传递用户目录 | 需要保留（无其他来源） |
| `?api_base=` | `handleHomePage` | CGI 传递 API 地址 | 已改为 `""`（相对路径） |

---

## 2. 目标架构（FNOS Port Gateway）

```
┌──────────┐
│  Browser │
│          │
│ iframe   │  url: "/officeeditor-api/"
│ SPA      │
└────┬─────┘
     │
     │  HTTP (同源，经 FNOS nginx)
     ▼
┌─────────────────────────────────────────┐
│  FNOS Gateway (Nginx :5666)            │
│                                         │
│  • 用户认证 (X-Trim-* 头)               │
│  • /officeeditor-api/* 反向代理          │
│  • 路径转发到 Connector :10088           │
└────────────────┬────────────────────────┘
                 │
    proxy_pass http://127.0.0.1:10088/
    proxy_set_header X-Trim-UserID ...
    proxy_set_header X-Trim-Username ...
                 │
                 ▼
┌─────────────────────────────────────────┐
│  Office Gateway (Connector :10088)      │
│                                         │
│  ┌──────────────────────────────────┐   │
│  │  Router Layer                    │   │
│  │                                  │   │
│  │  1. Middleware Stack             │   │
│  │     • Request Logging            │   │
│  │     • CORS                       │   │
│  │     • User Identity Extraction   │   │
│  │     • Path Normalization         │   │
│  │                                  │   │
│  │  2. API Service                  │   │
│  │     • /health                    │   │
│  │     • /api/*                     │   │
│  │     • /officeeditor-api/*        │   │
│  │                                  │   │
│  │  3. OnlyOffice Proxy             │   │
│  │     • /officeds/*  → DocServer   │   │
│  │     • /cache/*     → DocServer   │   │
│  │     • WebSocket Upgrade          │   │
│  │                                  │   │
│  │  4. File Service                 │   │
│  │     • Download                   │   │
│  │     • Create                     │   │
│  │     • Callback Save              │   │
│  │                                  │   │
│  │  5. History Service              │   │
│  │     • Per-user history           │   │
│  │     • user_id + file_path index  │   │
│  │                                  │   │
│  │  6. SPA Fallback                 │   │
│  │     • Home page                  │   │
│  │     • Editor page                │   │
│  └──────────────────────────────────┘   │
└────────────────┬────────────────────────┘
                 │
     httputil.ReverseProxy (WebSocket 支持)
     http://127.0.0.1:9080
                 │
                 ▼
┌─────────────────────────────────────────┐
│  OnlyOffice Document Server (Docker)    │
│                                         │
│  • 文档转换/渲染                        │
│  • 协同编辑 WebSocket                   │
│  • 字体管理                             │
│  • 缓存管理                             │
└─────────────────────────────────────────┘
```

### 2.1 核心架构原则

1. **前端的唯一出口是 FNOS nginx** — 浏览器不直接连接任何内部端口
2. **连接器是唯一中间层** — 不经过任何 CGI 脚本
3. **所有身份信息通过 HTTP 头传递** — 不从 query 参数读取用户身份
4. **所有前端 URL 使用相对路径** — 不硬编码任何绝对 URL
5. **配置文件驱动** — 不硬编码端口、IP、路径

---

## 3. 代码问题清单

### 3.1 架构问题

| # | 问题 | 位置 | 严重性 |
|---|------|------|--------|
| 1 | 单文件 942 行 monolith | `server.go` | 🔴 严重 |
| 2 | 遗留 CGI 脚本未删除 | `app/ui/index.cgi` | 🟡 中 |
| 3 | HTML 模板内嵌在 Go 代码中 | `server.go:681-824` | 🟡 中 |
| 4 | 无分层路由系统 | `NewServer()` 直接注册 mux | 🟡 中 |
| 5 | 无配置文件系统 | 全靠 flag + env | 🟡 中 |
| 6 | 无用户系统接口 | `getUserID/getUserName` 是函数 | 🟡 中 |
| 7 | 历史记录模型太简单 | 只有 path/name/time | 🟢 低 |

### 3.2 硬编码值

| # | 值 | 位置 | 说明 |
|---|-----|------|------|
| 1 | `10088` | `main.go:19` | 连接器端口 |
| 2 | `127.0.0.1:9080` | `main.go:20` | DocServer 地址 |
| 3 | `host.docker.internal:10088` | `server.go:208` | Docker 回连地址 |
| 4 | `/vol1/1000` | `server.go:549,657` | 默认用户目录 |
| 5 | `/var/apps/OfficeEditor/var` | `server.go:502,635` | 数据目录 |
| 6 | `/var/apps/OfficeEditor/target/docker` | `server.go:615` | Docker compose 目录 |
| 7 | `/var/apps/OfficeEditor/target/ui/images` | `server.go:850` | 图片目录 |

### 3.3 前端 URL 安全分析

| # | URL 模式 | 位置 | 安全性 |
|---|----------|------|--------|
| 1 | `?path=...` | JS 导航 | ✅ 相对路径 |
| 2 | `api/create?...` | JS fetch | ✅ 相对路径 |
| 3 | `api/history?...` | JS fetch | ✅ 相对路径 |
| 4 | `api/config` | JS fetch | ✅ 相对路径 |
| 5 | `api/check-update` | JS fetch | ✅ 相对路径 |
| 6 | `sponsor/donate` | JS img src | ✅ 相对路径 |
| 7 | `officeds/web-apps/...` | 编辑器 HTML template | ✅ 相对路径 |

---

## 4. 用户身份来源优先级

```
优先级 1: X-Trim-UserID / X-Trim-Username     ← FNOS nginx auth 模块注入
优先级 2: X-Auth-UID / X-Auth-Username          ← 其他系统兼容
优先级 3: X-Forwarded-User                      ← 通用反向代理
优先级 4: Remote-User                           ← HTTP Basic Auth
优先级 5: user_id / user_name (query)           ← CGI 兼容 (即将废弃)
优先级 6: 客户端 IP                              ← 最后回退
```

---

## 5. 数据流（文件生命周期）

### 5.1 创建文档

```
1. Browser:   点击 "新建 Word"
2. JS fetch:  POST api/create?type=docx
3. Connector: handleCreateDocument()
              → 生成 OOXML 模板
              → 写入 /vol1/{user_id}/新建Word文档_20260714_...docx
              → addToHistory(user_id, filePath)
              → 返回 {path: "/vol1/.../file.docx"}
4. JS:        window.location.href = "?path=/vol1/.../file.docx"
```

### 5.2 打开编辑

```
1. Browser:   GET /officeeditor-api/?path=/vol1/.../file.docx
2. Nginx:     proxy_pass → http://127.0.0.1:10088/?path=...
              + X-Trim-UserID + X-Trim-Username 头
3. Connector: handleEditorPage()
              → buildEditorConfig() → JSON
              → editorPageHTML + configJSON
              → addToHistory()
4. Browser:   <script src="officeds/web-apps/...api.js">
              → 连接器 /officeds/ → OnlyOffice DocServer
5. OnlyOffice API: GET /api/editor?path=...  → 编辑器配置 JSON
6. OnlyOffice API: GET http://host.docker.internal:10088/api/download?path=...
              → 连接器 handleDownload() → 读取文件系统 → 返回文件内容
7. OnlyOffice DocServer: 转换文档 → 缓存 → WebSocket 实时协作
```

### 5.3 保存文档

```
1. OnlyOffice DocServer: POST callbackUrl (http://host.docker.internal:10088/api/callback)
              → body: {status: 2, url: "http://docserver/download/..."}
2. Connector: handleCallback()
              → 下载 edited file from DocServer
              → 覆盖本地文件
              → 返回 {error: 0}
```

---

## 6. 重构范围总结

### 必须删除
- `app/ui/index.cgi` — 遗留 CGI 脚本

### 必须重构
- `connector/internal/server/server.go` — 拆分为多层架构

### 必须新增
- `config/config.yaml` — 配置文件
- 用户系统接口
- URL 生成器（前端）

### 必须保留
- `httputil.ReverseProxy` — OnlyOffice WebSocket 代理
- CORS 头处理
- 路径安全检查
- JWT 签名
