package server

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"fnos-office-editor/connector/internal/config"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/golang-jwt/jwt/v5"
)

// Server HTTP 服务器
type Server struct {
	router chi.Router
	config *config.Config
}

// New 创建新服务器
func New(cfg *config.Config) *Server {
	s := &Server{
		router: chi.NewRouter(),
		config: cfg,
	}

	s.setupMiddleware()
	s.setupRoutes()

	return s
}

func (s *Server) setupMiddleware() {
	s.router.Use(middleware.Logger)
	s.router.Use(middleware.Recoverer)
	s.router.Use(middleware.RequestID)
	s.router.Use(middleware.RealIP)
	s.router.Use(middleware.Timeout(120 * time.Second))
}

func (s *Server) setupRoutes() {
	// 健康检查
	s.router.Get("/health", s.handleHealth)

	// API 路由
	s.router.Route("/api", func(r chi.Router) {
		r.Get("/editor", s.handleEditor)       // 编辑器配置
		r.Get("/download", s.handleDownload)    // 文件下载 (OnlyOffice 调用)
		r.Post("/callback", s.handleCallback)   // 文件保存回调 (OnlyOffice 调用)
		r.Post("/convert", s.handleConvert)     // 格式转换
	})

	// 编辑器页面
	s.router.Get("/editor", s.handleEditorPage)

	// 静态文件
	fileServer := http.FileServer(http.Dir("web/static"))
	s.router.Handle("/static/*", http.StripPrefix("/static/", fileServer))
}

// Router 返回路由
func (s *Server) Router() chi.Router {
	return s.router
}

// ========== 处理函数 ==========

// handleHealth 健康检查
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "ok",
		"version": "1.0.0",
		"time":    time.Now().Unix(),
	})
}

// handleEditor 生成编辑器配置（OnlyOffice 需要）
func (s *Server) handleEditor(w http.ResponseWriter, r *http.Request) {
	filePath := r.URL.Query().Get("path")
	if filePath == "" {
		http.Error(w, `{"error":"missing path"}`, http.StatusBadRequest)
		return
	}

	// 获取用户信息 - 从 FNos 的 HTTP Header 中读取
	userID := r.Header.Get("X-Auth-UID")
	userName := r.Header.Get("X-Auth-Username")
	if userID == "" {
		userID = r.URL.Query().Get("user_id")
	}
	if userName == "" {
		userName = r.URL.Query().Get("user_name")
	}
	if userID == "" {
		userID = "anonymous"
		userName = "Anonymous"
	}

	// 判断是否内网请求
	host := r.Host
	if fwdHost := r.Header.Get("X-Forwarded-Host"); fwdHost != "" {
		host = fwdHost
	}
	isInternal := s.config.IsInternalRequest(strings.Split(host, ":")[0])

	// 获取文件信息
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"file not found: %s"}`, err.Error()), http.StatusNotFound)
		return
	}

	// 文件扩展名
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(filePath), "."))

	// 判断文档类型
	docType := getDocumentType(ext)
	canEdit := isEditable(ext)

	// 生成文档 key (基于路径+修改时间)
	docKey := generateDocKey(filePath, fileInfo.ModTime())

	// 构建下载和回调 URL
	baseURL := s.config.GetEffectiveBaseURL(isInternal)
	docSvrURL := s.config.GetEffectiveDocServerURL(isInternal)

	downloadURL := fmt.Sprintf("%s/api/download?path=%s", baseURL, url.QueryEscape(filePath))
	callbackURL := fmt.Sprintf("%s/api/callback?path=%s", baseURL, url.QueryEscape(filePath))

	// 构建编辑器配置
	editorConfig := map[string]interface{}{
		"document": map[string]interface{}{
			"fileType": ext,
			"key":      docKey,
			"title":    fileInfo.Name(),
			"url":      downloadURL,
			"permissions": map[string]interface{}{
				"edit":     canEdit,
				"download": true,
				"print":    true,
			},
		},
		"documentType": docType,
		"editorConfig": map[string]interface{}{
			"callbackUrl": callbackURL,
			"lang":        getLanguage(r),
			"mode":        map[bool]string{true: "edit", false: "view"}[canEdit],
			"user": map[string]interface{}{
				"id":   userID,
				"name": userName,
			},
		},
	}

	// JWT 签名
	if s.config.JWTSecret != "" {
		token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims(editorConfig))
		tokenStr, err := token.SignedString([]byte(s.config.JWTSecret))
		if err == nil {
			editorConfig["token"] = tokenStr
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(editorConfig)
}

// handleDownload 文件下载（OnlyOffice Document Server 调用）
func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request) {
	filePath := r.URL.Query().Get("path")
	if filePath == "" {
		http.Error(w, "missing path", http.StatusBadRequest)
		return
	}

	// 直接读取文件
	http.ServeFile(w, r, filePath)
}

// handleCallback 保存回调（OnlyOffice Document Server 调用）
func (s *Server) handleCallback(w http.ResponseWriter, r *http.Request) {
	filePath := r.URL.Query().Get("path")
	if filePath == "" {
		json.NewEncoder(w).Encode(map[string]int{"error": 1})
		return
	}

	var callback struct {
		Status int    `json:"status"`
		URL    string `json:"url"`
		Token  string `json:"token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&callback); err != nil {
		json.NewEncoder(w).Encode(map[string]int{"error": 1})
		return
	}

	// 状态 2 或 6 = 需要保存
	if callback.Status == 2 || callback.Status == 6 {
		if callback.URL == "" {
			json.NewEncoder(w).Encode(map[string]int{"error": 1})
			return
		}

		// 从 Document Server 下载已编辑的文件
		resp, err := http.Get(callback.URL)
		if err != nil {
			log.Printf("下载编辑后文件失败: %v", err)
			json.NewEncoder(w).Encode(map[string]int{"error": 1})
			return
		}
		defer resp.Body.Close()

		// 写入原文件
		out, err := os.Create(filePath)
		if err != nil {
			log.Printf("写入文件失败: %v", err)
			json.NewEncoder(w).Encode(map[string]int{"error": 1})
			return
		}
		defer out.Close()

		_, err = io.Copy(out, resp.Body)
		if err != nil {
			log.Printf("保存文件失败: %v", err)
			json.NewEncoder(w).Encode(map[string]int{"error": 1})
			return
		}

		log.Printf("文件已保存: %s", filePath)
	}

	json.NewEncoder(w).Encode(map[string]int{"error": 0})
}

// handleConvert 格式转换
func (s *Server) handleConvert(w http.ResponseWriter, r *http.Request) {
	// TODO: 实现格式转换（可通过 OnlyOffice Document Server API）
	json.NewEncoder(w).Encode(map[string]string{"status": "not_implemented"})
}

// handleEditorPage 编辑器页面
func (s *Server) handleEditorPage(w http.ResponseWriter, r *http.Request) {
	filePath := r.URL.Query().Get("path")
	if filePath == "" {
		http.Error(w, "missing path", http.StatusBadRequest)
		return
	}

	host := r.Host
	if fwdHost := r.Header.Get("X-Forwarded-Host"); fwdHost != "" {
		host = fwdHost
	}
	isInternal := s.config.IsInternalRequest(host)

	docSvrURL := s.config.GetEffectiveDocServerURL(isInternal)
	baseURL := s.config.GetEffectiveBaseURL(isInternal)
	apiURL := baseURL + "/api"

	// 简单编辑器页面
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	tpl := `<!DOCTYPE html>
<html>
<head>
    <meta charset="UTF-8">
    <title>FNos Office Editor</title>
    <style>
        html, body { height: 100%%; margin: 0; overflow: hidden; }
        #editor { width: 100%%; height: 100%%; }
    </style>
</head>
<body>
    <div id="editor"></div>
    <script src="%s/web-apps/apps/api/documents/api.js"></script>
    <script>
        var editorConfig = {
            "document": {
                "fileType": "%s",
                "title": "%s",
                "url": "%s/editor?path=%s"
            },
            "editorConfig": {
                "mode": "edit",
                "lang": "zh"
            }
        };
        new DocsAPI.DocEditor("editor", editorConfig);
    </script>
</body>
</html>`
	fmt.Fprintf(w, tpl,
		docSvrURL,
		strings.TrimPrefix(filepath.Ext(filePath), "."),
		filepath.Base(filePath),
		apiURL, url.QueryEscape(filePath),
	)
}

// ========== 辅助函数 ==========

func getDocumentType(ext string) string {
	switch ext {
	case "docx", "doc", "odt", "rtf", "txt", "html", "htm", "epub", "fb2":
		return "word"
	case "xlsx", "xls", "ods", "csv":
		return "cell"
	case "pptx", "ppt", "odp":
		return "slide"
	default:
		return "word"
	}
}

func isEditable(ext string) bool {
	switch ext {
	case "docx", "xlsx", "pptx", "doc", "xls", "ppt", "odt", "ods", "odp":
		return true
	default:
		return false
	}
}

func generateDocKey(filePath string, modTime time.Time) string {
	data := fmt.Sprintf("%s|%d", filePath, modTime.UnixNano())
	hash := sha256.Sum256([]byte(data))
	return fmt.Sprintf("%x", hash)[:20]
}

func getLanguage(r *http.Request) string {
	lang := r.Header.Get("Accept-Language")
	if strings.HasPrefix(lang, "zh") {
		return "zh"
	}
	return "en"
}
