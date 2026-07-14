# 路由诊断报告

> 日期: 2026-07-14
> 诊断目的: 确认 `/officeeditor-api/` 前缀路由正确进入 Go Handler

---

## 测试结果

### 测试 1: 带 `/officeeditor-api/` 前缀

```bash
curl -v "http://127.0.0.1:19988/officeeditor-api/?path=/tmp/test.xlsx"
```

**结果**: HTTP 200 ✅
**响应**: 编辑器页面 HTML（含正确的 OnlyOffice 配置 JSON）
**日志**:
```
REQUEST: GET /officeeditor-api/?path=/tmp/test.xlsx (from 127.0.0.1:51530, host=127.0.0.1:19988)
```
**诊断**: 前缀被正确剥离 → 进入 `handleRoot` → 检测到 `path` 参数 → `handleEditorPage` ✅

### 测试 2: 不带前缀

```bash
curl "http://127.0.0.1:19988/?path=/tmp/test.xlsx"
```

**结果**: HTTP 200 ✅
**日志**:
```
REQUEST: GET /?path=/tmp/test.xlsx (from 127.0.0.1:51546, host=127.0.0.1:19988)
```

### 测试 3: 根路径

```bash
curl "http://127.0.0.1:19988/"
```

**结果**: HTTP 200（首页 HTML）

### 测试 4: API 路由

```bash
curl "http://127.0.0.1:19988/api/version"
```

**结果**: `{"version":"1.0.29","connector":"ok"}` ✅

---

## 路由注册日志

```
REGISTERED ROUTES:
  /api/*        → System API
  /officeds/*   → OnlyOffice Proxy
  /cache/*      → DocServer Cache
  /officeeditor-api/* → FNOS Prefix (stripped)
  /editor       → Editor Page
  /             → SPA Fallback
```

## 中间件链（优先级从高到低）

```
1. CORS 中间件
2. /officeds/*  → httputil.ReverseProxy → OnlyOffice DocServer
3. /cache/*     → httputil.ReverseProxy → OnlyOffice DocServer
4. /officeeditor-api → 剥离前缀 → 进入标准路由
5. mux.ServeHTTP(w, r)
   ├── /health          → handleHealth
   ├── /api/version     → handleVersion
   ├── /api/*           → API handlers
   ├── /editor          → handleEditorPage
   ├── /                → handleRoot
   │   ├── ?path=xxx    → handleEditorPage
   │   └── (no path)    → renderHomePage
   └── /sponsor/*       → handleSponsorImage
```

## 前缀剥离逻辑

```go
// router.go: Handler() → 第 66-71 行
if strings.HasPrefix(r.URL.Path, "/officeeditor-api") {
    r.URL.Path = strings.TrimPrefix(r.URL.Path, "/officeeditor-api")
    if r.URL.Path == "" {
        r.URL.Path = "/"
    }
}
```

| 输入路径 | 处理后 | 匹配 Handler |
|----------|--------|-------------|
| `/officeeditor-api/?path=xxx` | `/?path=xxx` | `handleRoot → handleEditorPage` |
| `/officeeditor-api/api/create` | `/api/create` | `handleCreateDocument` |
| `/officeeditor-api/api/history` | `/api/history` | `handleHistory` |
| `/officeeditor-api/editor?path=xxx` | `/editor?path=xxx` | `handleEditorPage` |
| `/officeeditor-api/` | `/` | `handleRoot → renderHomePage` |
| `/officeeditor-api` | `/` | `handleRoot → renderHomePage` |

---

## 结论

**路由正常工作。** Go 层级的 `/officeeditor-api/` 前缀剥离和请求分发已通过 curl 验证。

如用户环境仍出现 404，请检查：

1. **已安装的 FPK 版本** — 确认是最新构建（含 v2 架构）
2. **FNOS nginx 代理配置** — `cmd/install_callback` 中的 `/officeeditor-api/` location block 是否生效
3. **app/ui/config** — `url: "/officeeditor-api/"` 是否与 nginx 代理路径一致
4. **连接器日志** — 查看 `REQUEST:` 行确认连接器实际收到的路径
