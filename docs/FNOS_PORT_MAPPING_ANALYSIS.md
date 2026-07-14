# FNOS Port Gateway 映射分析

> 冻结日期: 2026-07-14
> 架构: FNOS Gateway (:5666) → /officeeditor/ → Office Gateway (:10088) → OnlyOffice (:9080)

---

## 1. 入口映射

```
FNOS Browser
    │
    ▼
http://NAS:5666/officeeditor/      ← FNOS Port Gateway 入口
    │
    │  FNOS 自动剥离 /officeeditor/ 前缀
    │  proxy_pass → http://127.0.0.1:10088/
    │
    ▼
Office Gateway :10088               ← Go Connector
    │
    ├── /                            → 首页 HTML
    ├── /api/*                       → REST API
    ├── /editor                      → 编辑器页面
    ├── /officeds/*                  → OnlyOffice 代理
    ├── /cache/*                     → DocServer 缓存
    └── /sponsor/*                   → 静态资源
```

## 2. 路径转换表

| 浏览器请求 | FNOS 剥离后 | Connector 收到 | Handler |
|-----------|-------------|---------------|---------|
| `/officeeditor/` | `/` | `GET /` | `handleRoot → renderHomePage` |
| `/officeeditor/?path=/vol1/xxx` | `/?path=/vol1/xxx` | `GET /?path=xxx` | `handleRoot → handleEditorPage` |
| `/officeeditor/api/create` | `/api/create` | `POST /api/create` | `handleCreateDocument` |
| `/officeeditor/api/history` | `/api/history` | `GET /api/history` | `handleHistory` |
| `/officeeditor/officeds/web-apps/...` | `/officeds/web-apps/...` | `GET /officeds/...` | `ooProxy → OnlyOffice` |
| `/officeeditor/cache/files/...` | `/cache/files/...` | `GET /cache/...` | `ooProxy → OnlyOffice` |

## 3. 前端 URL 生成

| 用途 | JS 代码 | 生成 URL | 最终到达 |
|------|---------|---------|---------|
| API 请求 | `apiBase + "api/create"` | `/officeeditor/api/create` | Connector `/api/create` |
| 历史记录 | `apiBase + "api/history"` | `/officeeditor/api/history` | Connector `/api/history` |
| 导航编辑 | `"?path=..."` | `/officeeditor/?path=...` | Connector `/?path=...` |
| OnlyOffice JS | `"officeds/web-apps/..."` | `/officeeditor/officeds/...` | Connector `/officeds/...` |
| 赞助码 | `apiBase + "sponsor/donate"` | `/officeeditor/sponsor/donate` | Connector `/sponsor/donate` |

## 4. 配置项

| 配置 | 值 | 来源 |
|------|-----|------|
| FNOS 入口路径 | `/officeeditor/` | `app/ui/config` |
| FNOS 代理端口 | `10088` | `manifest` |
| Connector 端口 | `10088` | 环境变量 `PORT` |
| OnlyOffice 地址 | `http://127.0.0.1:9080` | `DOC_SERVER_URL` |
| API 基础路径 | `/officeeditor/` | `apiBase` JS 变量 |

## 5. 已移除的 CGI 依赖

| 旧依赖 | 替代方案 |
|--------|---------|
| `/cgi/ThirdParty/OfficeEditor/index.cgi` | `/officeeditor/` 统一入口 |
| `HTTP_X_TRIM_USERID` | `X-Trim-UserID` HTTP 头 |
| `HTTP_X_TRIM_USERNAME` | `X-Trim-Username` HTTP 头 |
| `QUERY_STRING` 解析 | 标准 HTTP query |
| `api_base` query 参数 | 硬编码 `/officeeditor/` |
| `user_id` query 参数(非必要) | HTTP 头 → fallback |

## 6. 路由优先级（Connector 内部）

```
1. CORS 中间件
2. /officeds/*     → ReverseProxy → OnlyOffice :9080
3. /cache/*        → ReverseProxy → OnlyOffice :9080
4. /officeeditor/  → 剥离前缀（直连兼容）
5. mux 路由:
   ├── /health
   ├── /api/*
   ├── /editor
   ├── /sponsor/*
   └── /              → SPA fallback (handleRoot)
```

## 7. 验证测试结果

| 测试 | 路径 | 期望 | 结果 |
|------|------|------|------|
| 首页 | `GET /officeeditor/` | HTTP 200, HTML | ✅ |
| 编辑器 | `GET /officeeditor/?path=xxx` | HTTP 200, editor HTML | ✅ |
| API | `GET /officeeditor/api/version` | `{"version":...}` | ✅ |
| 直连首页 | `GET /` | HTTP 200, HTML | ✅ |
| 直连 API | `GET /api/version` | `{"version":...}` | ✅ |
