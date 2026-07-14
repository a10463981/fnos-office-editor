// Package router 实现分层路由系统
// 路由优先级（从高到低）：
// 1. 系统 API: /health, /api/*
// 2. OnlyOffice 代理: /officeds/*, /cache/*
// 3. 文件服务: /api/download, /api/create, /api/callback
// 4. FNOS 前缀: /officeeditor-api/*（剥离后重新路由）
// 5. 页面渲染: /editor, /
// 6. 静态资源: /sponsor/*
package router

import (
	"fmt"
	"log"
	"net/http"
	"strings"

	"fnos-office-editor/connector/internal/callback"
	"fnos-office-editor/connector/internal/config"
	"fnos-office-editor/connector/internal/file"
	"fnos-office-editor/connector/internal/history"
	"fnos-office-editor/connector/internal/middleware"
	"fnos-office-editor/connector/internal/office"
	"fnos-office-editor/connector/internal/user"
)

// Mux 封装了带分层路由的 http.Handler
type Mux struct {
	cfg       *config.Config
	userProv  *user.Provider
	fileSvc   *file.Service
	history   *history.Service
	cbHandler *callback.Handler
	ooGateway *office.Gateway
	mux       *http.ServeMux
}

// New 创建完整路由器
func New(cfg *config.Config) *Mux {
	rt := &Mux{
		cfg:       cfg,
		userProv:  user.NewProvider(),
		fileSvc:   file.NewService(cfg.Paths.UserHome),
		history:   history.NewService(cfg.Paths.DataDir),
		cbHandler: callback.NewHandler(),
		ooGateway: office.NewGateway(cfg.OnlyOffice.InternalURL, cfg.OnlyOffice.PublicPrefix),
		mux:       http.NewServeMux(),
	}
	rt.register()
	log.Printf("REGISTERED ROUTES:")
	log.Printf("  /api/*        → System API")
	log.Printf("  /officeds/*   → OnlyOffice Proxy")
	log.Printf("  /cache/*      → DocServer Cache")
	log.Printf("  /officeeditor-api/* → FNOS Prefix (stripped)")
	log.Printf("  /editor       → Editor Page")
	log.Printf("  /             → SPA Fallback")
	return rt
}

// Handler 返回最终 http.Handler（含中间件链）
func (rt *Mux) Handler() http.Handler {
	// 中间件链: CORS → 日志 → 前缀剥离 → 代理检查 → 路由
	return middleware.CORS(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("REQUEST: %s %s?%s (from %s, host=%s)", r.Method, r.URL.Path, r.URL.RawQuery, r.RemoteAddr, r.Host)

		// 1. OnlyOffice 代理优先
		if rt.ooGateway.ShouldHandle(r.URL.Path) {
			r.URL.Path = rt.ooGateway.StripPrefix(r.URL.Path)
			rt.ooGateway.HandleProxy(w, r)
			return
		}

		// 2. FNOS 前缀剥离（/officeeditor-api/ → /）
		if strings.HasPrefix(r.URL.Path, "/officeeditor-api") {
			r.URL.Path = strings.TrimPrefix(r.URL.Path, "/officeeditor-api")
			if r.URL.Path == "" {
				r.URL.Path = "/"
			}
		}

		// 3. 进入标准路由
		rt.mux.ServeHTTP(w, r)
	}))
}

func (rt *Mux) register() {
	// === 第一优先级: 系统 API ===
	rt.mux.HandleFunc("/health", rt.handleHealth)
	rt.mux.HandleFunc("/api/version", rt.handleVersion)
	rt.mux.HandleFunc("/api/check-update", rt.handleCheckUpdate)

	// === 第二优先级: 文件 API ===
	rt.mux.HandleFunc("/api/history", rt.handleHistory)
	rt.mux.HandleFunc("/api/create", rt.handleCreateDocument)
	rt.mux.HandleFunc("/api/config", rt.handleConfig)
	rt.mux.HandleFunc("/api/fonts/refresh", rt.handleFontRefresh)
	rt.mux.HandleFunc("/api/editor", rt.handleEditorConfig)
	rt.mux.HandleFunc("/api/download", rt.handleDownload)
	rt.mux.HandleFunc("/api/callback", rt.handleCallback)

	// === 第三优先级: 页面渲染 ===
	rt.mux.HandleFunc("/editor", rt.handleEditorPage)
	rt.mux.HandleFunc("/", rt.handleRoot)

	// === 第四优先级: 静态资源 ===
	// === FNOS nginx 代理前缀（直接路由，不依赖中间件剥离）===
	rt.mux.HandleFunc("/officeeditor-api/", rt.handleOfficeEditorAPI)
	rt.mux.HandleFunc("/officeeditor-api", rt.handleOfficeEditorAPI)
	rt.mux.HandleFunc("/sponsor/", rt.handleSponsorImage)
}

// ========== Handlers ==========

func (rt *Mux) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"status":"ok","version":"1.0.0"}`)
}

func (rt *Mux) handleVersion(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"version":"1.0.29","connector":"ok"}`)
}

func (rt *Mux) handleCheckUpdate(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	// TODO: 实现更新检查
	fmt.Fprintf(w, `{"update":false}`)
}

func (rt *Mux) handleHistory(w http.ResponseWriter, r *http.Request) {
	userID := rt.userProv.EffectiveID(r)
	entries := rt.history.List(userID)
	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, entries)
}

func (rt *Mux) handleCreateDocument(w http.ResponseWriter, r *http.Request) {
	docType := r.URL.Query().Get("type")
	dir := r.URL.Query().Get("dir")
	result, err := rt.fileSvc.CreateDocument(docType, dir)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"error":"%s"}`, err.Error())
		return
	}
	rt.history.Add(rt.userProv.EffectiveID(r), result.Path, "")
	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, result)
}

func (rt *Mux) handleConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch r.Method {
	case "GET":
		fmt.Fprintf(w, `{}`)
	case "POST":
		fmt.Fprintf(w, `{"ok":true}`)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (rt *Mux) handleFontRefresh(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"ok":true}`)
}

func (rt *Mux) handleEditorConfig(w http.ResponseWriter, r *http.Request) {
	filePath := r.URL.Query().Get("path")
	if filePath == "" || !file.SafePath(filePath) {
		http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
		return
	}

	u := rt.userProv.GetCurrentUser(r)
	docURL := rt.docServerURL(r)
	config := map[string]interface{}{
		"document": map[string]interface{}{
			"fileType": ext(filePath),
			"key":      docKey(filePath),
			"title":    pathName(filePath),
			"url":      callback.BuildDownloadURL(docURL, filePath),
			"permissions": map[string]interface{}{
				"edit": true, "download": true, "print": true,
			},
		},
		"documentType": docType(filePath),
		"editorConfig": map[string]interface{}{
			"callbackUrl": callback.BuildCallbackURL(docURL, filePath),
			"serverUrl":   fmt.Sprintf("http://%s%s", r.Host, rt.cfg.OnlyOffice.PublicPrefix),
			"lang":        "zh",
			"mode":        "edit",
			"user": map[string]interface{}{
				"id": u.ID, "name": u.Name,
			},
		},
	}
	if rt.cfg.JWTSecret != "" {
		config["token"] = signJWT(config, rt.cfg.JWTSecret)
	}
	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, config)
}

func (rt *Mux) handleDownload(w http.ResponseWriter, r *http.Request) {
	filePath := r.URL.Query().Get("path")
	rt.fileSvc.ServeDownload(w, r, filePath)
}

func (rt *Mux) handleCallback(w http.ResponseWriter, r *http.Request) {
	filePath := r.URL.Query().Get("path")
	rt.cbHandler.Handle(w, r, filePath)
}

func (rt *Mux) handleEditorPage(w http.ResponseWriter, r *http.Request) {
	filePath := r.URL.Query().Get("path")
	if filePath == "" || !file.SafePath(filePath) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	rt.renderEditorPage(w, r, filePath)
}

func (rt *Mux) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	filePath := r.URL.Query().Get("path")
	if filePath != "" {
		rt.handleEditorPage(w, r)
		return
	}
	rt.renderHomePage(w, r)
}


// handleOfficeEditorAPI 处理直接通过 FNOS nginx 代理前缀的请求
func (rt *Mux) handleOfficeEditorAPI(w http.ResponseWriter, r *http.Request) {
	filePath := r.URL.Query().Get("path")
	if filePath != "" && file.SafePath(filePath) {
		rt.handleEditorPage(w, r)
		return
	}
	if r.URL.Path == "/officeeditor-api/" || r.URL.Path == "/officeeditor-api" {
		rt.renderHomePage(w, r)
		return
	}
	http.NotFound(w, r)
}
func (rt *Mux) handleSponsorImage(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, rt.cfg.Paths.ImageDir+"/donate.png")
}
