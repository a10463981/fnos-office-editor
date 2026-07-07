# office 协作 — FNos Office 在线编辑器

FNos 原生 Office 在线协作编辑器。基于 [OnlyOffice](https://www.onlyoffice.com/)，支持 Word/Excel/PPT 在线编辑、多人实时协作、用户身份识别、自定义字体。

---

## 功能特性

| 功能 | 说明 |
|------|------|
| 📝 **在线编辑** | DOCX/XLSX/PPTX 在浏览器中直接编辑 |
| 👁️ **文档预览** | PDF/TXT/CSV 等格式在线查看 |
| 👥 **用户识别** | 批注和协作编辑时显示真实 FNos 用户名 |
| 🆕 **新建文档** | 一键创建 Word/Excel/PPT 空白文档 |
| 📂 **历史记录** | 自动记录最近打开的文件，快速回访 |
| 🔤 **自定义字体** | 设置字体目录，自动加载至 OnlyOffice |
| 🔒 **管理员控制** | 仅 FNos 管理员可修改字体设置 |
| 🔄 **断线重连** | 浏览器切换标签页后自动恢复连接 |
| 🧹 **干净卸载** | 一键清除 Docker 容器、卷、配置 |

---

## 下载安装

### 方式一：应用中心手动安装（推荐）

1. 从 [Releases](https://github.com/a10463981/fnos-office-editor/releases) 下载 `OfficeEditor.fpk`
2. FNos → 应用中心 → 手动安装 → 选择 fpk 文件
3. 等待安装完成（约 1 分钟，包括拉取 OnlyOffice 镜像）
4. 刷新 FNos 文件管理器页面
5. 右键 Office 文件 → **"office 协作"** 即可编辑

### 方式二：开发者构建

```bash
git clone https://github.com/a10463981/fnos-office-editor.git
cd fnos-office-editor
# 需要 Go 1.22+ 和 fnpack
cd connector && go build -o ../app/connector/officeeditor-connector ./cmd/server/
cd .. && fnpack build --directory .
```

---

## 架构

```
FNos 浏览器 (5666)
    │
    ├─ /cgi/ThirdParty/OfficeEditor/index.cgi (CGI 代理)
    │       │
    │       ├─ ?path=xxx → curl → 连接器 (10088) → 编辑器页面
    │       └─ 无参数 → curl → 连接器 (10088) → 首页(新建+历史+设置)
    │
    └─ /officeeditor/ → nginx 代理 → 连接器 (10088)

连接器 (Go, systemd 服务, 端口 10088)
    │
    ├─ /api/create  → 创建空白文档 (docx/xlsx 自生成, pptx 嵌入 OnlyOffice 模板)
    ├─ /api/history → 最近打开记录
    ├─ /api/config  → 字体目录配置 (保存后自动重启 Docker + fc-cache)
    ├─ /api/version → 版本信息
    └─ /editor      → OnlyOffice 编辑器页面

OnlyOffice Document Server (Docker, 端口 9080)
    │
    ├─ onlyoffice/documentserver:latest
    ├─ /usr/share/fonts/custom ← 用户自定义字体
    └─ JWT 安全通信
```

---

## 配置

### 自定义字体

1. 在 FNos 文件管理器创建字体目录（如 `/vol1/1000/我的字体/`）
2. 将 `.ttf` 或 `.otf` 文件放入该目录
3. 打开 office 协作首页 → 点击右上角 **⚙️** → 输入字体目录路径 → 保存
4. Docker 容器自动重启并重建字体缓存（约 20 秒）

### 手动配置

```bash
# 查看当前配置
curl http://<fnos-ip>:10088/api/config

# 设置字体目录
curl -X POST http://<fnos-ip>:10088/api/config \
  -H "Content-Type: application/json" \
  -d '{"fontsDir":"/vol1/1000/我的字体"}'
```

---

## 环境变量

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `JWT_SECRET` | 安装时自动生成 | JWT 通信密钥 |
| `BASE_URL` | `http://localhost:10088` | 连接器地址 |
| `FONTS_DIR` | `/vol1/1000/fonts` | 自定义字体目录 |
| `TRIM_PKGVAR` | `/var/apps/OfficeEditor/var` | 数据目录 |

---

## 开发

```bash
# 克隆项目
git clone https://github.com/a10463981/fnos-office-editor.git
cd fnos-office-editor

# 构建连接器
cd connector
go build -o /usr/local/bin/officeeditor-connector ./cmd/server/

# 启动 OnlyOffice
cd app/officeeditor-docker
docker compose up -d

# 启动连接器
JWT_SECRET=your-secret officeeditor-connector --port 10088

# 构建 FPK
fnpack build --directory .
```

---

## 项目结构

```
├── manifest              # FPK 元信息
├── app/
│   ├── ui/config         # 桌面入口 + 文件类型关联
│   ├── ui/index.cgi      # CGI 代理 (Python)
│   ├── www/index.html    # 首页
│   ├── connector/        # Go 二进制
│   └── officeeditor-docker/
│       └── docker-compose.yaml
├── connector/
│   ├── cmd/server/       # 入口
│   └── internal/server/  # 核心逻辑
│       ├── server.go     # HTTP 路由 + 编辑器
│       ├── embed.go      # 嵌入 PPTX 模板
│       └── templates/    # 模板文件
├── config/
│   ├── privilege         # 运行权限
│   └── resource          # Docker 资源声明
├── cmd/                  # 生命周期脚本
│   ├── install_callback  # 安装后配置
│   ├── uninstall_callback # 彻底清理
│   └── main              # start/stop/status
└── releases/             # 已构建的 FPK
```

---

---

## 💰 赞助支持

如果这个项目对你有帮助，欢迎扫码赞助！

| 微信 | 支付宝 |
|------|--------|
| ![微信赞助](docs/donate-wechat.png) | ![支付宝赞助](docs/donate-alipay.png) |

> 赞助后将你的 GitHub ID 留言，我会添加到致谢名单 🙏

---

## 开源协议

[MIT License](LICENSE)

---

## 致谢

- [OnlyOffice](https://www.onlyoffice.com/) — 开源在线文档编辑器
- [onlyoffice-fnos](https://github.com/tf4fun/onlyoffice-fnos) — 原始灵感来源
- [飞牛 fnOS](https://www.fnnas.com/) — NAS 操作系统平台
