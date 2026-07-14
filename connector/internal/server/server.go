package server

import (
	"archive/zip"
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
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

	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "ok", "version": "1.0.0", "time": time.Now().Unix(),
		})
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		filePath := r.URL.Query().Get("path")
		if filePath != "" {
			handleEditorPage(w, r, cfg)
			return
		}
		handleHomePage(w, r, cfg)
	})

	mux.HandleFunc("/api/history", func(w http.ResponseWriter, r *http.Request) {
		handleHistory(w, r, cfg)
	})
	mux.HandleFunc("/api/create", func(w http.ResponseWriter, r *http.Request) {
		handleCreateDocument(w, r, cfg)
	})
	mux.HandleFunc("/api/fonts/refresh", func(w http.ResponseWriter, r *http.Request) {
		handleFontRefresh(w)
	})
	// /api/config handles both GET (read) and POST (save)
	mux.HandleFunc("/api/config", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			handleGetConfig(w, cfg)
		case http.MethodPost:
			handleSaveConfig(w, r, cfg)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/api/version", func(w http.ResponseWriter, r *http.Request) {
		handleVersion(w)
	})
	mux.HandleFunc("/api/check-update", func(w http.ResponseWriter, r *http.Request) {
		handleCheckUpdate(w)
	})

	mux.HandleFunc("/api/editor", func(w http.ResponseWriter, r *http.Request) {
		handleEditorConfig(w, r, cfg)
	})
	mux.HandleFunc("/api/download", func(w http.ResponseWriter, r *http.Request) {
		handleDownload(w, r)
	})
	mux.HandleFunc("/api/callback", func(w http.ResponseWriter, r *http.Request) {
		handleCallback(w, r)
	})
	mux.HandleFunc("/editor", func(w http.ResponseWriter, r *http.Request) {
		handleEditorPage(w, r, cfg)
	})
	mux.HandleFunc("/sponsor/", func(w http.ResponseWriter, r *http.Request) {
		handleSponsorImage(w, r)
	})
			// Wrap mux with officeds proxy middleware (catches before routing)
	chain := corsHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/officeds/") {
			handleOfficedsProxy(w, r)
			return
		}
		mux.ServeHTTP(w, r)
	}))
	return withRecover(requestLogMiddleware(guestCookieMiddleware(chain)))
}

func handleEditorConfig(w http.ResponseWriter, r *http.Request, cfg *Config) {
	filePath := r.URL.Query().Get("path")
	if filePath == "" || !isSafePath(filePath) {
		http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
		return
	}
	info, err := os.Stat(filePath)
	if err != nil {
		http.Error(w, `{"error":"file not found"}`, http.StatusNotFound)
		return
	}
	ext := strings.TrimPrefix(filepath.Ext(filePath), ".")
	// 文档下载/回调地址必须从 OnlyOffice Docker 容器可达
	// 使用 host.docker.internal 让容器访问主机端口 10088
	docURL := cfg.BaseURL
	if cfg.PublicBaseURL != "" && !isInternalHost(r.Host, cfg.InternalNetworks) {
		docURL = cfg.PublicBaseURL
	} else {
		docURL = "http://host.docker.internal:10088"
	}

	mode := r.URL.Query().Get("mode")
	canEdit := editable(ext) && mode != "view"
	userID := getEffectiveUserID(r)
	if userID == "" {
		userID = "fnos_user"
	}
	userName := getEffectiveUserName(r)
	if userName == "" || userName == "guest" {
		userName = "FNos 用户"
	}

	keyData := fmt.Sprintf("%s|%d", filePath, info.ModTime().UnixNano())
	h := sha256.Sum256([]byte(keyData))
	docKey := fmt.Sprintf("%x", h)[:20]

	downloadURL := fmt.Sprintf("%s/api/download?path=%s", docURL, url.QueryEscape(filePath))
	callbackURL := fmt.Sprintf("%s/api/callback?path=%s", docURL, url.QueryEscape(filePath))

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
	// 双保险：editor handler 整体 defer recover，绝不让任何 panic 击穿连接。
	defer func() {
		if rec := recover(); rec != nil {
			log.Printf("EDITOR PANIC method=%s path=%s query_path=%q uid=%q err=%v\n%s",
				r.Method, r.URL.Path, r.URL.Query().Get("path"),
				getEffectiveUserID(r), rec, debug.Stack())
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprintf(w, `{"error":"editor internal error","detail":%q}`, fmt.Sprint(rec))
		}
	}()

	filePath := r.URL.Query().Get("path")
	if filePath == "" || !isSafePath(filePath) {
		log.Printf("EDITOR REJECT path=%q remote=%s reason=unsafe_or_empty", filePath, r.RemoteAddr)
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	// 调试日志：进 editorHandler 必打，方便排查 CGI → Connector 的身份链路
	log.Printf("=== EDITOR REQUEST === method=%s path=%s query_path=%q X-Trim-UserID=%q X-Trim-Username=%q X-FNOS-UserID=%q X-FNOS-Username=%q cookie=%q cgi_base=%q",
		r.Method, r.URL.Path, filePath,
		r.Header.Get("X-Trim-UserID"),
		r.Header.Get("X-Trim-Username"),
		r.Header.Get("X-FNOS-UserID"),
		r.Header.Get("X-FNOS-Username"),
		r.Header.Get("Cookie"),
		r.URL.Query().Get("cgi_base"),
	)

	// 文件不存在直接返回 404 HTML，绝不允许走 panic 路径让连接被关掉
	if _, statErr := os.Stat(filePath); statErr != nil {
		log.Printf("EDITOR 404 path=%q err=%v", filePath, statErr)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprintf(w, `<!DOCTYPE html><html><body style="font-family:sans-serif;padding:24px"><h2>文件不存在</h2><p>路径：<code>%s</code></p><p>错误：%v</p><p><a href="javascript:history.back()">返回</a></p></body></html>`, html.EscapeString(filePath), statErr)
		return
	}

	// 文档下载/回调 URL 使用 host.docker.internal（Docker 容器可达）
	// 因为 OnlyOffice Document Server (Docker 内) 需要从服务器端调用这些地址
	dockerURL := "http://host.docker.internal:10088"
	configJSON := buildEditorConfig(filePath, r, cfg, dockerURL)

	// OnlyOffice API JS 通过 CGI 代理路径（浏览器同源加载）
	// CGI 代理将 action=officeds 转发到 OnlyOffice Document Server (9080)
	// 同时兼容连接器自带的 /officeds/ 代理（直接访问 10088 端口时）
	cgiBase := r.URL.Query().Get("cgi_base")
	apiJSBase := cfg.BaseURL
	if cgiBase != "" {
		apiJSBase = cgiBase + "?action=officeds&path="
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// 同一 user_id 共享历史：优先 header / cookie，回退到 query
	histUid := getEffectiveUserID(r)
	if histUid == "" {
		histUid = r.URL.Query().Get("user_id")
	}
	addToHistory(cfg, filePath, histUid)
	html := strings.Replace(editorPageHTML, "__API_JS_BASE__", apiJSBase, 1)
	fmt.Fprintf(w, html, configJSON)
}

func handleDownload(w http.ResponseWriter, r *http.Request) {
	filePath := r.URL.Query().Get("path")
	if filePath == "" || !isSafePath(filePath) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	http.ServeFile(w, r, filePath)
}

func handleCallback(w http.ResponseWriter, r *http.Request) {
	filePath := r.URL.Query().Get("path")
	if filePath == "" || !isSafePath(filePath) {
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
	// 优先级 1: 显式配置的公网地址（用户通过 DDNS / NAT 穿透访问时）
	if cfg.PublicBaseURL != "" {
		// 只有当请求来自外网时才使用公网地址
		if !isInternalHost(r.Host, cfg.InternalNetworks) {
			return cfg.PublicBaseURL
		}
	}

	// 优先级 2: 请求中携带的 Host header（fnconnect / 反向代理场景）
	host := r.Host
	if h := r.Header.Get("X-Forwarded-Host"); h != "" {
		host = h
	}
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	// 如果请求是来自外部（非 localhost/内网），用请求本身的地址
	if !isInternalHost(host, cfg.InternalNetworks) {
		return fmt.Sprintf("%s://%s", scheme, strings.Split(host, ",")[0])
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

// getEffectiveUserID 按固定优先级读取用户 ID：
//  1. X-Trim-UserID 头（CGI / FNOS Gateway 透传）
//  2. X-FNOS-UserID 头（备选）
//  3. office_guest_id cookie（直连场景下由 guestCookieMiddleware 注入）
//  4. 查询参数 user_id（CGI 当前链路使用）
// 禁止任何 IP / 127.0.0.1 fallback，避免 FNOS Gateway 反代后多用户身份串号。
func getEffectiveUserID(r *http.Request) string {
	if v := strings.TrimSpace(r.Header.Get("X-Trim-UserID")); v != "" {
		return v
	}
	if v := strings.TrimSpace(r.Header.Get("X-FNOS-UserID")); v != "" {
		return v
	}
	if c, err := r.Cookie("office_guest_id"); err == nil {
		if v := strings.TrimSpace(c.Value); v != "" {
			return v
		}
	}
	if v := strings.TrimSpace(r.URL.Query().Get("user_id")); v != "" {
		return v
	}
	return ""
}

// getEffectiveUserName 同上优先级，读不到时回退 "guest"（不再用 "FNos 用户" 这种共享身份）。
func getEffectiveUserName(r *http.Request) string {
	if v := strings.TrimSpace(r.Header.Get("X-Trim-Username")); v != "" {
		return v
	}
	if v := strings.TrimSpace(r.Header.Get("X-FNOS-Username")); v != "" {
		return v
	}
	if c, err := r.Cookie("office_guest_id"); err == nil {
		if v := strings.TrimSpace(c.Value); v != "" {
			return v
		}
	}
	if v := strings.TrimSpace(r.URL.Query().Get("user_name")); v != "" {
		return v
	}
	return "guest"
}

func buildEditorConfig(filePath string, r *http.Request, cfg *Config, baseURL string) string {
	// 必须显式处理 os.Stat 错误，否则文件不存在时会因 info==nil 触发 runtime panic，
	// 整个连接被关闭，浏览器看到 "Empty reply from server"。
	info, err := os.Stat(filePath)
	var modNs int64
	var fileName string
	if err != nil || info == nil {
		modNs = time.Now().UnixNano()
		fileName = filepath.Base(filePath)
		log.Printf("EDITOR WARN: file not accessible path=%q err=%v (using fallback timestamp)", filePath, err)
	} else {
		modNs = info.ModTime().UnixNano()
		fileName = info.Name()
	}
	ext := strings.TrimPrefix(filepath.Ext(filePath), ".")
	keyData := fmt.Sprintf("%s|%d", filePath, modNs)
	h := sha256.Sum256([]byte(keyData))
	docKey := fmt.Sprintf("%x", h)[:20]

	userID := getEffectiveUserID(r)
	if userID == "" {
		userID = "fnos_user"
	}
	userName := getEffectiveUserName(r)
	if userName == "" || userName == "guest" {
		userName = "FNos 用户"
	}

	config := map[string]interface{}{
		"document": map[string]interface{}{
			"fileType": ext,
			"key":      docKey,
			"title":    fileName,
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
			if t, err := signJWT(cfg.JWTSecret, b); err == nil {
				config["token"] = t
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

const editorPageHTML = `<!DOCTYPE html>
<html><head><meta charset="UTF-8">
<title>FNos Office Editor</title>
<style>html,body{height:100%%;margin:0;overflow:hidden}#editor{width:100%%;height:100%%}</style>
</head><body><div id="editor"></div>
<script src="__API_JS_BASE__/officeds/web-apps/apps/api/documents/api.js"></script>
<script>
var config=%s;
var editor=new DocsAPI.DocEditor("editor",config);

// 浏览器切回前台时检测连接状态，断开则自动重连
var wasDisconnected=false;
try{editor.on("onRequestClose",function(){wasDisconnected=true;});}catch(e){}
try{editor.on("onRequestRestore",function(){if(wasDisconnected)location.reload();});}catch(e){}
document.addEventListener("visibilitychange",function(){
    if(!document.hidden && wasDisconnected){
        location.reload();
    }
});
</script>
</body></html>`

// ========== history tracking ==========

type HistoryEntry struct {
	Path      string `json:"path"`
	Name      string `json:"name"`
	OpenedAt  string `json:"openedAt"`
}

func historyFilePath(cfg *Config, userId string) string {
	d := os.Getenv("TRIM_PKGVAR")
	if d == "" { d = "/var/apps/OfficeEditor/var" }
	if userId == "" { userId = "shared" }
	return d + "/history_" + userId + ".json"
}

func loadHistory(cfg *Config, userId string) []HistoryEntry {
	data, err := os.ReadFile(historyFilePath(cfg, userId))
	if err != nil { return nil }
	var entries []HistoryEntry
	json.Unmarshal(data, &entries)
	return entries
}

func saveHistory(cfg *Config, entries []HistoryEntry, userId string) {
	// Keep last 50
	if len(entries) > 50 { entries = entries[len(entries)-50:] }
	data, _ := json.MarshalIndent(entries, "", "  ")
	os.MkdirAll(filepath.Dir(historyFilePath(cfg, userId)), 0755)
	os.WriteFile(historyFilePath(cfg, userId), data, 0644)
}

func addToHistory(cfg *Config, filePath string, userId string) {
	name := filepath.Base(filePath)
	entries := loadHistory(cfg, userId)
	// Remove existing entry for this path
	filtered := make([]HistoryEntry, 0, len(entries))
	for _, e := range entries {
		if e.Path != filePath { filtered = append(filtered, e) }
	}
	// Prepend new entry
	filtered = append([]HistoryEntry{{Path: filePath, Name: name, OpenedAt: time.Now().Format("2006-01-02 15:04")}}, filtered...)
	saveHistory(cfg, filtered, userId)
}

func handleHistory(w http.ResponseWriter, r *http.Request, cfg *Config) {
	// 身份优先级：header > cookie > query。避免 IP fallback 串号。
	userId := getEffectiveUserID(r)
	if userId == "" {
		userId = r.URL.Query().Get("user_id")
	}
	if userId == "" {
		userId = "anonymous"
	}
	entries := loadHistory(cfg, userId)
	if entries == nil {
		entries = []HistoryEntry{}
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(entries)
}

func handleCreateDocument(w http.ResponseWriter, r *http.Request, cfg *Config) {
	docType := r.URL.Query().Get("type")
	// 默认写到当前用户的 /vol1/<uid>/ 目录，多用户隔离
	histUid := getEffectiveUserID(r)
	if histUid == "" {
		histUid = r.URL.Query().Get("user_id")
	}
	if histUid == "" {
		histUid = "anonymous"
	}
	dir := r.URL.Query().Get("dir")
	if dir == "" {
		dir = "/vol1/" + histUid
	}

	ext := map[string]string{"docx": "docx", "xlsx": "xlsx", "pptx": "pptx"}[docType]
	if ext == "" {
		http.Error(w, `{"error":"invalid type"}`, http.StatusBadRequest)
		return
	}

	ts := time.Now().Format("20060102_150405")
	name := fmt.Sprintf("新建%s文档_%s.%s", map[string]string{"docx":"Word","xlsx":"Excel","pptx":"PowerPoint"}[docType], ts, ext)
	filePath := filepath.Join(dir, name)

	// Write minimal valid document
	f, err := os.Create(filePath)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"error": err.Error()})
		return
	}
	// Write minimal valid OOXML document
	tmpl := ooxmlTemplate(docType)
	if tmpl != nil {
		f.Write(tmpl)
	} else {
		// fallback: empty zip
		f.Write([]byte{0x50, 0x4B, 0x05, 0x06, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0})
	}
	f.Close()

	addToHistory(cfg, filePath, histUid)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"path": filePath, "name": name})
}

func handleFontRefresh(w http.ResponseWriter) {
	// Rebuild font cache in OnlyOffice container
	cmd := exec.Command("docker", "exec", "officeeditor-docserver", "fc-cache", "-fv")
	out, err := cmd.CombinedOutput()
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": err.Error(), "output": string(out)})
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "output": string(out)})
}

func handleGetConfig(w http.ResponseWriter, cfg *Config) {
	conf := loadAppConfig(cfg)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(conf)
}

func handleSaveConfig(w http.ResponseWriter, r *http.Request, cfg *Config) {
	var conf AppConfig
	if err := json.NewDecoder(r.Body).Decode(&conf); err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": err.Error()})
		return
	}
	saveAppConfig(cfg, &conf)
	// 重启 OnlyOffice 容器应用新配置
	restartOnlyOfficeContainer(&conf)
	json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
}

func restartOnlyOfficeContainer(conf *AppConfig) {
	composeDir := "/var/apps/OfficeEditor/target/docker"
	fontsDir := conf.FontsDir
	if fontsDir == "" { fontsDir = "/vol1/1000/fonts" }
	cmd := exec.Command("docker", "compose", "-f", composeDir+"/docker-compose.yaml",
		"up", "-d", "--force-recreate")
	cmd.Env = append(os.Environ(), "FONTS_DIR="+fontsDir)
	cmd.Dir = composeDir
	cmd.Run()
	// Wait for container to start, then rebuild font cache
	time.Sleep(5 * time.Second)
	exec.Command("docker", "exec", "officeeditor-docserver", "fc-cache", "-fv").Run()
	// Also restart document server services to pick up new fonts
	exec.Command("docker", "exec", "officeeditor-docserver", "supervisorctl", "restart", "all").Run()
}

type AppConfig struct {
	FontsDir string `json:"fontsDir"`
}

func configFilePath() string {
	d := os.Getenv("TRIM_PKGVAR")
	if d == "" { d = "/var/apps/OfficeEditor/var" }
	return d + "/config.json"
}

func loadAppConfig(cfg *Config) *AppConfig {
	data, err := os.ReadFile(configFilePath())
	if err != nil { return &AppConfig{FontsDir: "/vol1/1000/fonts"} }
	var c AppConfig
	json.Unmarshal(data, &c)
	if c.FontsDir == "" { c.FontsDir = "/vol1/1000/fonts" }
	return &c
}

func saveAppConfig(cfg *Config, c *AppConfig) {
	os.MkdirAll(filepath.Dir(configFilePath()), 0755)
	data, _ := json.MarshalIndent(c, "", "  ")
	os.WriteFile(configFilePath(), data, 0644)
}

func handleHomePage(w http.ResponseWriter, r *http.Request, cfg *Config) {
	// 身份参数优先级：header (CGI 透传) > query > cookie > 默认值
	// 避免 FNOS Gateway 反代场景下 IP 串号导致用户历史混乱。
	userId := getEffectiveUserID(r)
	if userId == "" {
		userId = "anonymous"
	}
	userName := getEffectiveUserName(r)
	if userName == "" || userName == "guest" {
		userName = "FNos 用户"
	}
	// dir / is_admin 仍走 query（CGI 单次透传，不会被多个用户共用）
	dir := r.URL.Query().Get("dir")
	if dir == "" {
		dir = "/vol1/" + userId
	}
	isAdmin := r.URL.Query().Get("is_admin")
	// api_base 默认用当前 request 推断（同源 CGI：/cgi/ThirdParty/.../?action=api&path=）
	apiBase := r.URL.Query().Get("api_base")
	if apiBase == "" {
		apiBase = r.URL.Query().Get("apiBase")
	}
	if apiBase == "" {
		apiBase = fmt.Sprintf("http://%s", r.Host)
	}
	log.Printf("HOME uid=%q username=%q dir=%q apiBase=%q isAdmin=%q", userId, userName, dir, apiBase, isAdmin)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	html := strings.Replace(homePageHTML, "USER_DIR_PLACEHOLDER", dir, 1)
	html = strings.Replace(html, "USER_NAME_PLACEHOLDER", userName, 1)
	html = strings.Replace(html, "API_BASE_PLACEHOLDER", apiBase, 1)
	html = strings.Replace(html, "USER_ID_PLACEHOLDER", userId, 1)
	if isAdmin == "true" {
		html = strings.Replace(html, "IS_ADMIN_PLACEHOLDER", "", 1)
	} else {
		html = strings.Replace(html, "IS_ADMIN_PLACEHOLDER", "HIDDEN_BY_ADMIN_CHECK", 1)
	}
	fmt.Fprint(w, html)
}

const homePageHTML = `<!DOCTYPE html>
<html lang="zh-CN"><head><meta charset="UTF-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>office 协作</title>
<style>
*{margin:0;padding:0;box-sizing:border-box}
body{font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,sans-serif;background:#f0f2f5;color:#333;min-height:100vh}
.header{background:linear-gradient(135deg,#1a73e8,#0d47a1);color:#fff;padding:24px 28px}
.header h1{font-size:22px;font-weight:600}
.header p{font-size:13px;opacity:.85;margin-top:4px}
.content{max-width:720px;margin:0 auto;padding:24px 16px}
.section{margin-bottom:28px}
.section h2{font-size:16px;font-weight:600;margin-bottom:12px;color:#1a1a1a}
.btns{display:flex;gap:10px;flex-wrap:wrap}
.btn{padding:11px 20px;border:none;border-radius:8px;font-size:14px;font-weight:500;cursor:pointer;display:inline-flex;align-items:center;gap:6px;transition:transform .1s,box-shadow .1s}
.btn:active{transform:scale(.97)}
.btn-word{background:#2b579a;color:#fff}
.btn-excel{background:#217346;color:#fff}
.btn-ppt{background:#d24726;color:#fff}
.history-list{background:#fff;border-radius:10px;box-shadow:0 1px 4px rgba(0,0,0,.08);overflow:hidden}
.history-item{display:flex;align-items:center;padding:13px 18px;border-bottom:1px solid #f0f0f0;cursor:pointer;transition:background .15s;text-decoration:none;color:inherit}
.history-item:hover{background:#f5f8ff}
.history-item:last-child{border-bottom:none}
.history-item .icon{font-size:20px;margin-right:12px}
.history-item .info{flex:1}
.history-item .name{font-size:14px;font-weight:500}
.history-item .time{font-size:12px;color:#999;margin-top:2px}
.empty{text-align:center;padding:36px;color:#999;font-size:14px}
.toast{position:fixed;bottom:24px;left:50%;transform:translateX(-50%);background:#333;color:#fff;padding:10px 24px;border-radius:20px;font-size:13px;opacity:0;transition:opacity .3s;pointer-events:none;z-index:999}
.toast.show{opacity:1}
.spinner{display:inline-block;width:14px;height:14px;border:2px solid #fff;border-top-color:transparent;border-radius:50%;animation:spin .6s linear infinite}
@keyframes spin{to{transform:rotate(360deg)}}
.settings-btn{cursor:pointer;font-size:24px;opacity:.8;transition:opacity .2s}.settings-btn:hover{opacity:1}
.settings-btn.HIDDEN_BY_ADMIN_CHECK{display:none!important}
.modal-overlay{display:none;position:fixed;top:0;left:0;width:100%;height:100%;background:rgba(0,0,0,.5);z-index:999;justify-content:center;align-items:center}
.modal-overlay.show{display:flex}
.modal{background:#fff;border-radius:12px;padding:28px;width:90%;max-width:420px;box-shadow:0 4px 24px rgba(0,0,0,.15)}
.modal h3{font-size:18px;margin-bottom:16px}
.modal label{font-size:13px;color:#666;display:block;margin-bottom:6px}
.modal input{width:100%;padding:10px 12px;border:1px solid #ddd;border-radius:6px;font-size:14px;margin-bottom:16px}
.modal .actions{display:flex;gap:8px;justify-content:flex-end}
.modal .btn-save{background:#1a73e8;color:#fff;border:none;padding:10px 20px;border-radius:6px;cursor:pointer;font-size:14px}
.modal .btn-cancel{background:#f0f0f0;color:#333;border:none;padding:10px 20px;border-radius:6px;cursor:pointer;font-size:14px}
</style></head><body>
<div id="updateBar" style="display:none;background:#e74c3c;color:#fff;text-align:center;padding:6px;font-size:13px"></div>
<div class="header"><div style="display:flex;justify-content:space-between;align-items:center"><div><h1>📄 office 协作</h1><p>欢迎 USER_NAME_PLACEHOLDER，在线编辑 Word / Excel / PPT</p></div><span class="settings-btn IS_ADMIN_PLACEHOLDER" onclick="openSettings()" title="字体设置">⚙️</span></div></div>
<div class="content">
  <div class="section">
    <h2>新建文档</h2>
    <div class="btns">
      <button class="btn btn-word" onclick="createDoc('docx')"><span>📝</span> Word 文档</button>
      <button class="btn btn-excel" onclick="createDoc('xlsx')"><span>📊</span> Excel 表格</button>
      <button class="btn btn-ppt" onclick="createDoc('pptx')"><span>📽️</span> PPT 演示</button>
    </div>
    <p style="font-size:12px;color:#999;margin-top:8px">📁 新建文件将保存在您的个人目录中</p>
  </div>
  <div class="section">
    <h2>最近打开</h2>
    <div class="history-list" id="history"></div>
  </div>
</div>
  <div class="section" style="text-align:center;margin-top:16px;padding-top:12px;border-top:1px solid #eee">
    <h2>赞助支持</h2>
    <p style="font-size:13px;color:#666;margin-bottom:12px">你的赞助是我更新的动力 💪</p>
    <div>
      <img id="sponsorQr" src="" data-src="sponsor/donate" style="width:280px" alt="赞助码">
    </div>
    <p style="font-size:11px;color:#999;margin-top:12px">
      GitHub: <a href="https://github.com/a10463981/fnos-office-editor" target="_blank">a10463981/fnos-office-editor</a> - v1.0.33
    </p>
  </div>
</div>

<div class="modal-overlay" id="settingsModal">
  <div class="modal">
    <h3>⚙️ 字体设置</h3>
    <label>自定义字体目录路径（.ttf/.otf 文件将自动加载到 OnlyOffice）</label>
    <input type="text" id="fontsDirInput" placeholder="/vol1/1000/我的字体/">
    <p style="font-size:12px;color:#999;margin:-8px 0 12px 0">⚠️ 点击保存后 Docker 重建需要约 20 秒，请耐心等待提示成功后再操作。</p>
    <div class="actions">
      <button class="btn-cancel" onclick="closeSettings()">取消</button>
      <button class="btn-save" id="btnSaveSettings" onclick="saveSettings()">保存并生效</button>
    </div>
  </div>
</div>
<div class="toast" id="toast"></div>
<script>
var userDir="USER_DIR_PLACEHOLDER";
var userId="USER_ID_PLACEHOLDER";
var apiBase="API_BASE_PLACEHOLDER";
function toast(msg){var t=document.getElementById("toast");t.textContent=msg;t.classList.add("show");setTimeout(function(){t.classList.remove("show")},2000)}
function createDoc(type){
  var btn=event.target;
  btn.disabled=true;
  btn.innerHTML=btn.innerHTML.replace(/<span>.*<\/span>/,'<span class="spinner"></span>');
  fetch(apiBase+"/api/create?type="+type+"&dir="+encodeURIComponent(userDir),{method:"POST"})
    .then(r=>r.json())
    .then(d=>{
      if(d.error){toast("创建失败: "+d.error);btn.disabled=false;return}
      window.location.href="/cgi/ThirdParty/OfficeEditor/index.cgi?path="+encodeURIComponent(d.path);
    })
    .catch(e=>{toast("创建失败");btn.disabled=false})
}
function loadHistory(){
  fetch(apiBase+"/api/history?user_id="+encodeURIComponent(userId))
    .then(r=>r.json())
    .then(items=>{
      var h=document.getElementById("history");
      if(!items.length){h.innerHTML='<div class="empty">还没有打开过文档，右键 Office 文件试试吧</div>';return}
      var icons={"docx":"📝","xlsx":"📊","pptx":"📽️","doc":"📝","xls":"📊","ppt":"📽️","pdf":"📕","txt":"📄"};
      h.innerHTML=items.map(function(i){
        var ext=i.name.split(".").pop().toLowerCase();
        return '<a class="history-item" href="/cgi/ThirdParty/OfficeEditor/index.cgi?path='+encodeURIComponent(i.path)+'"><span class="icon">'+(icons[ext]||"📄")+'</span><div class="info"><div class="name">'+i.name+'</div><div class="time">'+i.openedAt+'</div></div></a>'
      }).join("");
    })
}
function openSettings(){
  document.getElementById("settingsModal").classList.add("show");
  fetch(apiBase+"/api/config").then(r=>r.json()).then(c=>{
    document.getElementById("fontsDirInput").value=c.fontsDir||"";
  });
}
function closeSettings(){
  document.getElementById("settingsModal").classList.remove("show");
}
function saveSettings(){
  var dir=document.getElementById("fontsDirInput").value.trim();
  if(!dir){toast("请输入字体目录路径") ;return;}
  var btn=document.getElementById("btnSaveSettings");
  btn.disabled=true;btn.textContent="Docker 重启中...";
  fetch(apiBase+"/api/config",{method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify({fontsDir:dir})})
    .then(r=>{if(!r.ok)throw new Error(r.status);return r.json()})
    .then(d=>{toast(d.ok?"字体设置已保存，Docker 容器重启中...":"保存失败: "+d.error);closeSettings();})
    .catch(e=>{toast("保存失败: "+e.message);})
    .finally(function(){btn.disabled=false;btn.textContent="保存并生效";});
}


fetch(apiBase+"/api/check-update").then(r=>r.json()).then(d=>{if(d.update){var el=document.getElementById("updateBar");el.innerHTML="📢 有新版本 v"+d.latest+"！<a href=\""+d.url+"\" target=\"_blank\" style=\"color:#ff0\">点击下载</a>";el.style.display="block";}});
document.getElementById('sponsorQr').src=apiBase+'/sponsor/donate';
loadHistory();
</script>
</body></html>`

// ========== middleware ==========

// withRecover 把任何 handler 的 panic 转成 500 JSON 响应，避免 "Empty reply from server"。
func withRecover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("PANIC %s %s: %v\n%s", r.Method, r.URL.Path, rec, debug.Stack())
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				fmt.Fprintf(w, `{"error":"internal server error","detail":%q}`, fmt.Sprint(rec))
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// requestLogMiddleware 打印 method/path/query/关键 header/cookie，便于排查身份传递问题。
func requestLogMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("REQ method=%s path=%s query_path=%q X-Trim-UserID=%q X-Trim-Username=%q X-FNOS-UserID=%q X-FNOS-Username=%q cookie=%q ua=%q",
			r.Method,
			r.URL.Path,
			r.URL.Query().Get("path"),
			r.Header.Get("X-Trim-UserID"),
			r.Header.Get("X-Trim-Username"),
			r.Header.Get("X-FNOS-UserID"),
			r.Header.Get("X-FNOS-Username"),
			r.Header.Get("Cookie"),
			r.Header.Get("User-Agent"),
		)
		next.ServeHTTP(w, r)
	})
}

// guestCookieMiddleware 给直接访问（无 CGI、无 header）的浏览器一个 office_guest_id cookie，
// 让身份回退链路在直连场景下也能工作。已有 cookie 就不覆盖。
func guestCookieMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie("office_guest_id"); err != nil || c.Value == "" {
			b := make([]byte, 6)
			rand.Read(b)
			gid := "guest_" + hex.EncodeToString(b)
			http.SetCookie(w, &http.Cookie{
				Name:     "office_guest_id",
				Value:    gid,
				Path:     "/",
				MaxAge:   365 * 24 * 3600,
				HttpOnly: false,
			})
			// 同步写到 Request，让本请求里后续 handler 也能读到这个 guest id
			r.AddCookie(&http.Cookie{Name: "office_guest_id", Value: gid})
		}
		next.ServeHTTP(w, r)
	})
}

func corsHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isSafePath(p string) bool {
	if p == "" || strings.Contains(p, "..") { return false }
	return strings.HasPrefix(p, "/vol") || strings.HasPrefix(p, "/tmp/")
}

func handleOfficedsProxy(w http.ResponseWriter, r *http.Request) {
	// Use ReverseProxy which supports HTTP Upgrade (WebSocket)
	backendPath := strings.TrimPrefix(r.URL.Path, "/officeds")
	target := &url.URL{Scheme: "http", Host: "127.0.0.1:9080"}
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.ModifyResponse = func(resp *http.Response) error {
		return nil
	}
	// Rewrite the request path
	r.URL.Path = backendPath
	r.URL.Host = target.Host
	r.URL.Scheme = target.Scheme
	r.Host = target.Host
	proxy.ServeHTTP(w, r)
}

func base64URLEncode(data []byte) string {
	return strings.TrimRight(base64.URLEncoding.EncodeToString(data), "=")
}

func handleSponsorImage(w http.ResponseWriter, r *http.Request) {
	// Serve sponsor QR images from app/ui/images/
	basePath := "/var/apps/OfficeEditor/target/ui/images"
	if r.URL.Path == "/sponsor/donate" {
		http.ServeFile(w, r, basePath+"/donate.png")
	} else {
		http.ServeFile(w, r, basePath+"/donate.png")
	}
}

const AppVersion = "1.0.36"

func handleCheckUpdate(w http.ResponseWriter) {
	// Check GitHub for latest release
	resp, err := http.Get("https://api.github.com/repos/a10463981/fnos-office-editor/releases/latest")
	if err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"update": false, "error": "network"})
		return
	}
	defer resp.Body.Close()
	var release struct {
		TagName string 
		HTMLURL string 
	}
	json.NewDecoder(resp.Body).Decode(&release)
	latest := strings.TrimPrefix(release.TagName, "v")
	hasUpdate := latest != "" && latest != AppVersion
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"update":    hasUpdate,
		"current":   AppVersion,
		"latest":    latest,
		"url":       release.HTMLURL,
	})
}

func handleVersion(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"version":   AppVersion,
		"connector": "ok",
	})
}

// ooxmlTemplate generates a minimal valid OOXML template
func ooxmlTemplate(docType string) []byte {
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)

	type entry struct{ name, content string }
	var entries []entry

	switch docType {
	case "docx":
		entries = []entry{
			{"[Content_Types].xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>`},
			{"_rels/.rels", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`},
			{"word/document.xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t></w:t></w:r></w:p></w:body></w:document>`},
		}
	case "xlsx":
		entries = []entry{
			{"[Content_Types].xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/></Types>`},
			{"_rels/.rels", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/></Relationships>`},
			{"xl/workbook.xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheets><sheet name="Sheet1" sheetId="1" r:id="rId1" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"/></sheets></workbook>`},
			{"xl/_rels/workbook.xml.rels", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/></Relationships>`},
			{"xl/worksheets/sheet1.xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData/></worksheet>`},
		}
	case "pptx":
		if len(pptxTemplateData) > 0 {
			return pptxTemplateData
		}
		// fallback to generated template
		entries = []entry{
			{"[Content_Types].xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/ppt/presentation.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.presentation.main+xml"/><Override PartName="/ppt/slideMasters/slideMaster1.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.slideMaster+xml"/><Override PartName="/ppt/slides/slide1.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.slide+xml"/><Override PartName="/ppt/slideLayouts/slideLayout1.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.slideLayout+xml"/><Override PartName="/ppt/theme/theme1.xml" ContentType="application/vnd.openxmlformats-officedocument.theme+xml"/></Types>`},
			{"_rels/.rels", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="ppt/presentation.xml"/></Relationships>`},
			{"ppt/presentation.xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><p:presentation xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main"><p:sldMasterIdLst><p:sldMasterId id="2147483648" r:id="rId1"/></p:sldMasterIdLst><p:sldIdLst><p:sldId id="256" r:id="rId2"/></p:sldIdLst><p:sldSz cx="9144000" cy="6858000"/><p:notesSz cx="6858000" cy="9144000"/><p:defaultTextStyle><a:defPPr><a:defRPr lang="zh-CN"/></a:defPPr></p:defaultTextStyle></p:presentation>`},
			{"ppt/_rels/presentation.xml.rels", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slideMaster" Target="slideMasters/slideMaster1.xml"/><Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slide" Target="slides/slide1.xml"/><Relationship Id="rId3" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/theme" Target="theme/theme1.xml"/></Relationships>`},
			{"ppt/slideMasters/slideMaster1.xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><p:sldMaster xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main"><p:cSld><p:bg><p:bgPr><a:solidFill><a:srgbClr val="FFFFFF"/></a:solidFill></p:bgPr></p:bg><p:spTree><p:nvGrpSpPr><p:cNvPr id="1" name=""/><p:cNvGrpSpPr/><p:nvPr/></p:nvGrpSpPr><p:grpSpPr/></p:spTree></p:cSld><p:sldLayoutIdLst><p:sldLayoutId id="2147483649" r:id="rId1"/></p:sldLayoutIdLst></p:sldMaster>`},
			{"ppt/slideLayouts/slideLayout1.xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><p:sldLayout xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main" type="blank"><p:cSld><p:spTree><p:nvGrpSpPr><p:cNvPr id="1" name=""/><p:cNvGrpSpPr/><p:nvPr/></p:nvGrpSpPr><p:grpSpPr/></p:spTree></p:cSld></p:sldLayout>`},
			{"ppt/slides/slide1.xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><p:sld xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main"><p:cSld><p:spTree><p:nvGrpSpPr><p:cNvPr id="2" name=""/><p:cNvGrpSpPr/><p:nvPr/></p:nvGrpSpPr><p:grpSpPr/></p:spTree></p:cSld></p:sld>`},
			{"ppt/_rels/slideMaster1.xml.rels", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slideLayout" Target="../slideLayouts/slideLayout1.xml"/></Relationships>`},
			{"ppt/theme/theme1.xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><a:theme xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" name="Office Theme"><a:themeElements><a:clrScheme name="Office"><a:dk1><a:srgbClr val="000000"/></a:dk1><a:lt1><a:srgbClr val="FFFFFF"/></a:lt1><a:dk2><a:srgbClr val="44546A"/></a:dk2><a:lt2><a:srgbClr val="E7E6E6"/></a:lt2><a:accent1><a:srgbClr val="4472C4"/></a:accent1><a:accent2><a:srgbClr val="ED7D31"/></a:accent2><a:accent3><a:srgbClr val="A5A5A5"/></a:accent3><a:accent4><a:srgbClr val="FFC000"/></a:accent4><a:accent5><a:srgbClr val="5B9BD5"/></a:accent5><a:accent6><a:srgbClr val="70AD47"/></a:accent6><a:hlink><a:srgbClr val="0563C1"/></a:hlink><a:folHlink><a:srgbClr val="954F72"/></a:folHlink></a:clrScheme><a:fontScheme name="Office"><a:majorFont><a:latin typeface="Calibri Light"/></a:majorFont><a:minorFont><a:latin typeface="Calibri"/></a:minorFont></a:fontScheme><a:fmtScheme name="Office"><a:fillStyleLst><a:solidFill><a:schemeClr val="phClr"/></a:solidFill></a:fillStyleLst><a:lnStyleLst><a:ln w="6350"><a:solidFill><a:schemeClr val="phClr"/></a:solidFill></a:ln></a:lnStyleLst><a:effectStyleLst><a:effectStyle><a:effectLst/></a:effectStyle></a:effectStyleLst><a:bgFillStyleLst><a:solidFill><a:schemeClr val="phClr"/></a:solidFill></a:bgFillStyleLst></a:fmtScheme></a:themeElements></a:theme>`},
		}
	default:
		return nil
	}

	for _, e := range entries {
		fw, _ := w.Create(e.name)
		fw.Write([]byte(e.content))
	}
	w.Close()
	return buf.Bytes()
}
