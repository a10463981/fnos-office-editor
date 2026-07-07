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

	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "ok", "version": "1.0.0", "time": time.Now().Unix(),
		})
	})

	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		filePath := r.URL.Query().Get("path")
		if filePath != "" {
			handleEditorPage(w, r, cfg)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<!DOCTYPE html>
<html lang="zh-CN"><head><meta charset="UTF-8"><title>FNos 办公编辑器</title>
<style>body{font-family:sans-serif;display:flex;justify-content:center;align-items:center;min-height:100vh;margin:0;background:#f5f7fa;color:#333}.c{text-align:center;padding:40px;background:#fff;border-radius:12px;box-shadow:0 2px 12px rgba(0,0,0,0.1)}h1{color:#1a73e8;font-size:28px}p{color:#666}</style>
</head><body><div class="c"><h1>FNos 办公编辑器</h1><p>基于 OnlyOffice 的在线文档编辑器</p><p style="color:#34a853">服务运行中</p><p>右键 Office 文件 → FNos 办公编辑器 即可编辑</p></div></body></html>`)
	})

	mux.HandleFunc("GET /api/editor", func(w http.ResponseWriter, r *http.Request) {
		handleEditorConfig(w, r, cfg)
	})
	mux.HandleFunc("GET /api/download", func(w http.ResponseWriter, r *http.Request) {
		handleDownload(w, r)
	})
	mux.HandleFunc("POST /api/callback", func(w http.ResponseWriter, r *http.Request) {
		handleCallback(w, r)
	})
	mux.HandleFunc("GET /editor", func(w http.ResponseWriter, r *http.Request) {
		handleEditorPage(w, r, cfg)
	})
	return mux
}

func handleEditorConfig(w http.ResponseWriter, r *http.Request, cfg *Config) {
	filePath := r.URL.Query().Get("path")
	if filePath == "" {
		http.Error(w, `{"error":"missing path"}`, http.StatusBadRequest)
		return
	}
	info, err := os.Stat(filePath)
	if err != nil {
		http.Error(w, `{"error":"file not found"}`, http.StatusNotFound)
		return
	}
	ext := strings.TrimPrefix(filepath.Ext(filePath), ".")
	baseURL := getEffectiveBaseURL(r, cfg)

	mode := r.URL.Query().Get("mode")
	canEdit := editable(ext) && mode != "view"
	userID := getUserID(r)
	userName := getUserName(r)

	keyData := fmt.Sprintf("%s|%d", filePath, info.ModTime().UnixNano())
	h := sha256.Sum256([]byte(keyData))
	docKey := fmt.Sprintf("%x", h)[:20]

	downloadURL := fmt.Sprintf("%s/api/download?path=%s", baseURL, url.QueryEscape(filePath))
	callbackURL := fmt.Sprintf("%s/api/callback?path=%s", baseURL, url.QueryEscape(filePath))

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
		"documentType": documentType(ext),
		"editorConfig": map[string]interface{}{
			"callbackUrl": callbackURL,
			"lang":        "zh",
			"mode":        map[bool]string{true: "edit", false: "view"}[canEdit],
			"user": map[string]interface{}{
				"id": userID, "name": userName,
			},
		},
	}
	if cfg.JWTSecret != "" {
		if b, err := json.Marshal(config); err == nil {
			if tok, err := signJWT(cfg.JWTSecret, b); err == nil {
				config["token"] = tok
			}
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(config)
}

func handleEditorPage(w http.ResponseWriter, r *http.Request, cfg *Config) {
	filePath := r.URL.Query().Get("path")
	if filePath == "" {
		http.Error(w, "missing path", http.StatusBadRequest)
		return
	}

	// DocServer URL - use host override if provided by CGI proxy
	docSvrURL := cfg.DocServerURL
	if h := getHostOverride(r); h != "" {
		docSvrURL = "http://" + h + ":9080"
	}

	// Generate config with JWT signing
	configJSON := buildEditorConfig(filePath, r, cfg)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!DOCTYPE html>
<html><head><meta charset="UTF-8">
<title>FNos Office Editor</title>
<style>html,body{height:100%%;margin:0;overflow:hidden}#editor{width:100%%;height:100%%}</style>
</head><body><div id="editor"></div>
<script src="%s/web-apps/apps/api/documents/api.js"></script>
<script>new DocsAPI.DocEditor("editor",%s);</script>
</body></html>`, docSvrURL, configJSON)
}

func handleDownload(w http.ResponseWriter, r *http.Request) {
	filePath := r.URL.Query().Get("path")
	if filePath == "" {
		http.Error(w, "missing path", http.StatusBadRequest)
		return
	}
	http.ServeFile(w, r, filePath)
}

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
	if cb.Status == 2 || cb.Status == 6 {
		if cb.URL == "" {
			json.NewEncoder(w).Encode(map[string]int{"error": 1})
			return
		}
		resp, err := http.Get(cb.URL)
		if err != nil {
			log.Printf("download edited file failed: %v", err)
			json.NewEncoder(w).Encode(map[string]int{"error": 1})
			return
		}
		defer resp.Body.Close()
		out, err := os.Create(filePath)
		if err != nil {
			json.NewEncoder(w).Encode(map[string]int{"error": 1})
			return
		}
		defer out.Close()
		io.Copy(out, resp.Body)
		log.Printf("file saved: %s", filePath)
	}
	json.NewEncoder(w).Encode(map[string]int{"error": 0})
}

// ========== helpers ==========

func getEffectiveBaseURL(r *http.Request, cfg *Config) string {
	host := r.Host
	if h := r.Header.Get("X-Forwarded-Host"); h != "" {
		host = h
	}
	// Server-side calls always use localhost
	if host == "localhost" || host == "127.0.0.1" || strings.HasPrefix(host, "::1") {
		return cfg.BaseURL
	}
	return cfg.BaseURL
}

func getHostOverride(r *http.Request) string {
	if h := r.URL.Query().Get("host"); h != "" {
		return h
	}
	if h := r.Header.Get("X-Forwarded-Host"); h != "" {
		return strings.Split(h, ":")[0]
	}
	return ""
}

func getUserID(r *http.Request) string {
	if uid := r.Header.Get("X-Auth-UID"); uid != "" {
		return uid
	}
	return r.URL.Query().Get("user_id")
}

func getUserName(r *http.Request) string {
	if n := r.Header.Get("X-Auth-Username"); n != "" {
		return n
	}
	return r.URL.Query().Get("user_name")
}

func buildEditorConfig(filePath string, r *http.Request, cfg *Config) string {
	info, _ := os.Stat(filePath)
	ext := strings.TrimPrefix(filepath.Ext(filePath), ".")
	baseURL := cfg.BaseURL
	keyData := fmt.Sprintf("%s|%d", filePath, info.ModTime().UnixNano())
	h := sha256.Sum256([]byte(keyData))
	docKey := fmt.Sprintf("%x", h)[:20]

	userID := getUserID(r)
	if userID == "" {
		userID = "fnos_user"
	}
	userName := getUserName(r)
	if userName == "" {
		userName = "FNos 用户"
	}

	config := map[string]interface{}{
		"document": map[string]interface{}{
			"fileType": ext,
			"key":      docKey,
			"title":    info.Name(),
			"url":      fmt.Sprintf("%s/api/download?path=%s", baseURL, url.QueryEscape(filePath)),
			"permissions": map[string]interface{}{
				"edit": editable(ext), "download": true, "print": true,
			},
		},
		"documentType": documentType(ext),
		"editorConfig": map[string]interface{}{
			"callbackUrl": fmt.Sprintf("%s/api/callback?path=%s", baseURL, url.QueryEscape(filePath)),
			"mode":        "edit",
			"lang":        "zh",
			"user": map[string]interface{}{
				"id": userID, "name": userName,
			},
		},
	}
	// JWT signing
	if cfg.JWTSecret != "" {
		if b, err := json.Marshal(config); err == nil {
			if tok, err := signJWT(cfg.JWTSecret, b); err == nil {
				configJSON, _ := json.Marshal(config)
				tok = tok // Already computed
				_ = tok
			}
		}
		// Re-marshal with token
		if b, err := json.Marshal(config); err == nil {
			if tok, err := signJWT(cfg.JWTSecret, b); err == nil {
				// Add token to config
				config["token"] = tok
			}
		}
	}
	b, _ := json.Marshal(config)
	return string(b)
}

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
