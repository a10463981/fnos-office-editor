# FNos 办公编辑器 (FNos Office Editor)

FNos 原生 Office 在线编辑器。基于 OnlyOffice，支持 DOCX/XLSX/PPTX 在线编辑、多人协作、用户身份认证、内外网访问。

## 特性

- ✅ **原生性能** — Go 连接器直接运行在 FNos 系统上，无额外开销
- ✅ **用户认证** — 自动识别当前 FNos 用户，多人协作正确显示身份
- ✅ **内外网自适应** — 内网直连、外网通过 FN Connect，智能切换
- ✅ **文档编辑** — 支持 DOCX/XLSX/PPTX 在线编辑
- ✅ **格式转换** — 自动转换旧格式（DOC/XLS/PPT）为 OOXML
- ✅ **文档预览** — 支持 PDF、EPUB、FB2 等格式在线查看
- ✅ **JWT 安全** — 文档传输使用 JWT 签名验证
- ✅ **FPK 一键安装** — 在 FNos 应用中心手动安装即可

## 架构

```
┌─────────────────────────────────────────────┐
│  FPK 应用 (FNos 原生)                        │
│                                              │
│  ├─ Go 连接器 (systemd 服务)                  │
│  │   - 端口 10099                             │
│  │   - 读取 FNos 用户认证                     │
│  │   - 直接读写磁盘文件                       │
│  │                                           │
│  ├─ FNos Nginx 集成                          │
│  │   - 自动配置反向代理                       │
│  │   - 内外网自适应                           │
│  │                                           │
│  └─ OnlyOffice Document Server (Docker)       │
│      - 存储卷直挂，无需 HTTP 中转             │
│      - JWT 安全通信                          │
└─────────────────────────────────────────────┘
```

## 安装

### 方法一：FPK 安装包（推荐）

1. 从 [Releases](https://github.com/a10463981/fnos-office-editor/releases) 下载 `.fpk` 文件
2. 打开 FNos 应用中心 → 手动安装 → 选择文件
3. 按向导配置存储卷和网络
4. 安装完成，开始使用

### 方法二：手动部署

```bash
git clone https://github.com/a10463981/fnos-office-editor.git
cd fnos-office-editor

# 构建 Go 连接器
cd connector
go build -o /usr/local/bin/officeeditor-connector ./cmd/server/

# 创建配置
mkdir -p /etc/officeeditor
cp ../scripts/config.example.json /etc/officeeditor/config.json

# 启动 Docker 服务
cd ../app/docker
docker compose up -d

# 启动连接器
/usr/local/bin/officeeditor-connector --port 10099 --config /etc/officeeditor/config.json
```

## 配置

| 环境变量 | 说明 | 默认值 |
|---------|------|--------|
| `PORT` | 连接器监听端口 | `10099` |
| `JWT_SECRET` | JWT 密钥 | 自动生成 |
| `DOC_SERVER_URL` | OnlyOffice 地址 | `http://127.0.0.1:9080` |
| `BASE_URL` | 内网连接器地址 | `http://localhost:10099` |
| `PUBLIC_BASE_URL` | 外网连接器地址 | 可选 |
| `TRIM_PKGVAR` | 数据目录 | `/var/apps/OfficeEditor/var` |

## 开发

```bash
# 克隆项目
git clone https://github.com/a10463981/fnos-office-editor.git
cd fnos-office-editor

# 修改 Go 连接器
cd connector
go build ./cmd/server/

# 打包 FPK
fnpack build

# 安装到 FNos
appcenter-cli install-fpk OfficeEditor.fpk
```

## 开源协议

Apache-2.0
