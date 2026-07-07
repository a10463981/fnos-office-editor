package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Config 连接器配置
type Config struct {
	Port             int
	DocServerURL     string
	DocServerPubURL  string
	JWTSecret        string
	BaseURL          string
	PublicBaseURL    string
	InternalNetworks []string
}

// NewServer 创建 HTTP 服务器
func NewServer(cfg *Config) http.Handler {
	mux := http.NewServeMux()

	// 健康检查
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "ok", "version": "1.0.0", "time": time.Now().Unix(),
		})
	})

	// API: 编辑器配置
	mux.HandleFunc("GET /api/editor", func(w http.ResponseWriter, r *http.Request) {
		handleEditorConfig(w, r, cfg)
	})

	// API: 文件下载 (OnlyOffice 调用)
	mux.HandleFunc("GET /api/download", func(w http.ResponseWriter, r *http.Request) {
		handleDownload(w, r)
	})

	// API: 保存回调 (OnlyOffice 调用)
	mux.HandleFunc("POST /api/callback", func(w http.ResponseWriter, r *http.Request) {
		handleCallback(w, r)
	})

	// 编辑器页面
	mux.HandleFunc("GET /editor", func(w http.ResponseWriter, r *http.Request) {
		handleEditorPage(w, r, cfg)
	})

	return mux
}

// handleEditorConfig 生成 OnlyOffice 编辑器配置
func handleEditorConfig(w http.ResponseWriter, r *http.Request, cfg *Config) {
	filePath := r.URL.Query().Get("path")
	if filePath == "" {
		http.Error(w, `{"error":"missing path"}`, http.StatusBadRequest)
		return
	}

	// 用户认证：优先从 FNos Header 读取
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
		userName = "匿名用户"
	}

	// 判断内/外网
	host := r.Host
	if h := r.Header.Get("X-Forwarded-Host"); h != "" {
		host = h
	}
	isInternal := isInternalHost(host, cfg.InternalNetworks)

	// 文件信息
	info, err := os.Stat(filePath)
	if err != nil {
		http.Error(w, `{"error":"file not found"}`, http.StatusNotFound)
		return
	}

	ext := strings.TrimPrefix(filepath.Ext(filePath), ".")
	docType := documentType(ext)
	canEdit := editable(ext)

	// 生成文档 key
	keyData := fmt.Sprintf("%s|%d", filePath, info.ModTime().UnixNano())
	h := sha256.Sum256([]byte(keyData))
	docKey := fmt.Sprintf("%x", h)[:20]

	// URL
	baseURL := cfg.BaseURL
	if !isInternal && cfg.PublicBaseURL != "" {
		baseURL = cfg.PublicBaseURL
	}

	downloadURL := fmt.Sprintf("%s/api/download?path=%s", baseURL, url.QueryEscape(filePath))
	callbackURL := fmt.Sprintf("%s/api/callback?path=%s", baseURL, url.QueryEscape(filePath))

	// 构建配置
	config := map[string]interface{}{
		"document": map[string]interface{}{
			"fileType": ext,
			"key":      docKey,
			"title":    info.Name(),
			"url":      downloadURL,
			"permissions": map[string]interface{}{
				"edit": canEdit, "download": true, "print": true,
			},
		},
		"documentType": docType,
		"editorConfig": map[string]interface{}{
			"callbackUrl": callbackURL,
			"lang":        "zh",
			"mode":        map[bool]string{true: "edit", false: "view"}[canEdit],
			"user": map[string]interface{}{
				"id": userID, "name": userName,
			},
		},
	}

	// JWT 签名
	if cfg.JWTSecret != "" {
		configJSON, _ := json.Marshal(config)
		token, err := signJWT(cfg.JWTSecret, configJSON)
		if err == nil {
			config["token"] = token
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(config)
}

// handleDownload 文件下载（OnlyOffice 调用）
func handleDownload(w http.ResponseWriter, r *http.Request) {
	filePath := r.URL.Query().Get("path")
	if filePath == "" {
		http.Error(w, "missing path", http.StatusBadRequest)
		return
	}
	http.ServeFile(w, r, filePath)
}

// handleCallback 保存回调（OnlyOffice 调用）
func handleCallback(w http.ResponseWriter, r *http.Request) {
	filePath := r.URL.Query().Get("path")
	if filePath == "" {
		json.NewEncoder(w).Encode(map[string]int{"error": 1})
		return
	}

	var cb struct {
		Status int    `json:"status"`
		URL    string `json:"url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&cb); err != nil {
		json.NewEncoder(w).Encode(map[string]int{"error": 1})
		return
	}

	// 状态 2/6 = 需要保存
	if cb.Status == 2 || cb.Status == 6 {
		if cb.URL == "" {
			json.NewEncoder(w).Encode(map[string]int{"error": 1})
			return
		}
		resp, err := http.Get(cb.URL)
		if err != nil {
			log.Printf("下载编辑后文件失败: %v", err)
			json.NewEncoder(w).Encode(map[string]int{"error": 1})
			return
		}
		defer resp.Body.Close()

		out, err := os.Create(filePath)
		if err != nil {
			log.Printf("写入文件失败: %v", err)
			json.NewEncoder(w).Encode(map[string]int{"error": 1})
			return
		}
		defer out.Close()

		if _, err := io.Copy(out, resp.Body); err != nil {
			log.Printf("保存文件失败: %v", err)
			json.NewEncoder(w).Encode(map[string]int{"error": 1})
			return
		}
		log.Printf("文件已保存: %s", filePath)
	}

	json.NewEncoder(w).Encode(map[string]int{"error": 0})
}

// handleEditorPage 渲染编辑器页面
func handleEditorPage(w http.ResponseWriter, r *http.Request, cfg *Config) {
	filePath := r.URL.Query().Get("path")
	if filePath == "" {
		http.Error(w, "missing path", http.StatusBadRequest)
		return
	}

	host := r.Host
	if h := r.Header.Get("X-Forwarded-Host"); h != "" {
		host = h
	}
	isInternal := isInternalHost(host, cfg.InternalNetworks)

	docSvrURL := cfg.DocServerURL
	if !isInternal && cfg.DocServerPubURL != "" {
		docSvrURL = cfg.DocServerPubURL
	}

	ext := strings.TrimPrefix(filepath.Ext(filePath), ".")

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!DOCTYPE html>
<html><head><meta charset="UTF-8">
<title>FNos Office Editor</title>
<style>html,body{height:100%%;margin:0;overflow:hidden}#editor{width:100%%;height:100%%}</style>
</head><body><div id="editor"></div>
<script src="%s/web-apps/apps/api/documents/api.js"></script>
<script>
var cfg=%s;
new DocsAPI.DocEditor("editor",cfg);
</script></body></html>`,
		docSvrURL,
		editorConfigJSON(filePath, cfg),
	)
}

// ========== 辅助函数 ==========

func documentType(ext string) string {
	switch ext {
	case "docx", "doc", "odt", "rtf", "txt", "html", "epub", "fb2":
		return "word"
	case "xlsx", "xls", "ods", "csv":
		return "cell"
	case "pptx", "ppt", "odp":
		return "slide"
	}
	return "word"
}

func editable(ext string) bool {
	switch ext {
	case "docx", "xlsx", "pptx", "doc", "xls", "ppt", "odt", "ods", "odp":
		return true
	}
	return false
}

func isInternalHost(host string, internalNetworks []string) bool {
	h := strings.Split(host, ":")[0]
	if h == "localhost" || h == "127.0.0.1" || h == "::1" {
		return true
	}
	ip := net.ParseIP(h)
	if ip == nil {
		return false
	}
	for _, cidr := range internalNetworks {
		_, network, _ := net.ParseCIDR(cidr)
		if network != nil && network.Contains(ip) {
			return true
		}
	}
	return false
}

func editorConfigJSON(filePath string, cfg *Config) string {
	info, _ := os.Stat(filePath)
	ext := strings.TrimPrefix(filepath.Ext(filePath), ".")
	keyData := fmt.Sprintf("%s|%d", filePath, info.ModTime().UnixNano())
	h := sha256.Sum256([]byte(keyData))
	docKey := fmt.Sprintf("%x", h)[:20]

	config := map[string]interface{}{
		"document": map[string]interface{}{
			"fileType": ext,
			"key":      docKey,
			"title":    info.Name(),
			"url":      fmt.Sprintf("%s/api/download?path=%s", cfg.BaseURL, url.QueryEscape(filePath)),
			"permissions": map[string]interface{}{
				"edit": editable(ext), "download": true, "print": true,
			},
		},
		"documentType": documentType(ext),
		"editorConfig": map[string]interface{}{
			"mode": "edit", "lang": "zh",
			"user": map[string]interface{}{"id": "fnos_user", "name": "FNos 用户"},
		},
	}
	b, _ := json.Marshal(config)
	return string(b)
}

// JWT 签名（HS256）
func signJWT(secret string, payload []byte) (string, error) {
	header := base64URLEncode([]byte(`{"alg":"HS256","typ":"JWT"}`))
	body := base64URLEncode(payload)
	signing := header + "." + body
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(signing))
	sig := base64URLEncode(mac.Sum(nil))
	return signing + "." + sig, nil
}

func base64URLEncode(data []byte) string {
	return strings.TrimRight(base64.URLEncoding.EncodeToString(data), "=")
}
