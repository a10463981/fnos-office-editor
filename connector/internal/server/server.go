package server

import (
	"archive/zip"
	"bytes"
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
		handleHomePage(w, r, cfg)
	})

	mux.HandleFunc("GET /api/history", func(w http.ResponseWriter, r *http.Request) {
		handleHistory(w, cfg)
	})
	mux.HandleFunc("POST /api/create", func(w http.ResponseWriter, r *http.Request) {
		handleCreateDocument(w, r, cfg)
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
	return corsHandler(mux)
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

	// Get effective host for URLs (from CGI proxy)
	overrideHost := getHostOverride(r)
	baseURL := cfg.BaseURL
	if overrideHost != "" {
		baseURL = "http://" + overrideHost + ":10088"
	}

	configJSON := buildEditorConfig(filePath, r, cfg, baseURL)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	addToHistory(cfg, filePath)
	fmt.Fprintf(w, editorPageHTML, configJSON)
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

func buildEditorConfig(filePath string, r *http.Request, cfg *Config, baseURL string) string {
	info, _ := os.Stat(filePath)
	ext := strings.TrimPrefix(filepath.Ext(filePath), ".")
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
<script src="http://127.0.0.1:9080/web-apps/apps/api/documents/api.js"></script>
<script>
var config=%s;
var editor=new DocsAPI.DocEditor("editor",config);

// 浏览器切回前台时检测连接状态，断开则自动重连
var wasDisconnected=false;
editor.on("onRequestClose",function(){wasDisconnected=true;});
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

func historyFilePath(cfg *Config) string {
	d := os.Getenv("TRIM_PKGVAR")
	if d == "" { d = "/var/apps/OfficeEditor/var" }
	return d + "/history.json"
}

func loadHistory(cfg *Config) []HistoryEntry {
	data, err := os.ReadFile(historyFilePath(cfg))
	if err != nil { return nil }
	var entries []HistoryEntry
	json.Unmarshal(data, &entries)
	return entries
}

func saveHistory(cfg *Config, entries []HistoryEntry) {
	// Keep last 50
	if len(entries) > 50 { entries = entries[len(entries)-50:] }
	data, _ := json.MarshalIndent(entries, "", "  ")
	os.MkdirAll(filepath.Dir(historyFilePath(cfg)), 0755)
	os.WriteFile(historyFilePath(cfg), data, 0644)
}

func addToHistory(cfg *Config, filePath string) {
	name := filepath.Base(filePath)
	entries := loadHistory(cfg)
	// Remove existing entry for this path
	filtered := make([]HistoryEntry, 0, len(entries))
	for _, e := range entries {
		if e.Path != filePath { filtered = append(filtered, e) }
	}
	// Prepend new entry
	filtered = append([]HistoryEntry{{Path: filePath, Name: name, OpenedAt: time.Now().Format("2006-01-02 15:04")}}, filtered...)
	saveHistory(cfg, filtered)
}

func handleHistory(w http.ResponseWriter, cfg *Config) {
	entries := loadHistory(cfg)
	if entries == nil { entries = []HistoryEntry{} }
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(entries)
}

func handleCreateDocument(w http.ResponseWriter, r *http.Request, cfg *Config) {
	docType := r.URL.Query().Get("type")
	dir := r.URL.Query().Get("dir")
	if dir == "" { dir = "/vol1/1000" }

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

	addToHistory(cfg, filePath)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"path": filePath, "name": name})
}

func handleHomePage(w http.ResponseWriter, r *http.Request, cfg *Config) {
	dir := r.URL.Query().Get("dir")
	if dir == "" { dir = "/vol1/1000" }
	userName := r.URL.Query().Get("user_name")
	if userName == "" { userName = "FNos 用户" }
	apiBase := r.URL.Query().Get("api_base")
	if apiBase == "" { apiBase = "http://localhost:10088" }

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	html := strings.Replace(homePageHTML, "USER_DIR_PLACEHOLDER", dir, 1)
	html = strings.Replace(html, "USER_NAME_PLACEHOLDER", userName, 1)
	html = strings.Replace(html, "API_BASE_PLACEHOLDER", apiBase, 1)
	fmt.Fprint(w, html)
}

const homePageHTML = `<!DOCTYPE html>
<html lang="zh-CN"><head><meta charset="UTF-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>FNos 办公编辑器</title>
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
</style></head><body>
<div class="header"><h1>📄 FNos 办公编辑器</h1><p>欢迎 USER_NAME_PLACEHOLDER，在线编辑 Word / Excel / PPT</p></div>
<div class="content">
  <div class="section">
    <h2>新建文档</h2>
    <div class="btns">
      <button class="btn btn-word" onclick="createDoc('docx')"><span>📝</span> Word 文档</button>
      <button class="btn btn-excel" onclick="createDoc('xlsx')"><span>📊</span> Excel 表格</button>
      <button class="btn btn-ppt" onclick="createDoc('pptx')"><span>📽️</span> PPT 演示</button>
    </div>
  </div>
  <div class="section">
    <h2>最近打开</h2>
    <div class="history-list" id="history"></div>
  </div>
</div>
<div class="toast" id="toast"></div>
<script>
var userDir="USER_DIR_PLACEHOLDER";
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
  fetch(apiBase+"/api/history")
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
loadHistory();
</script>
</body></html>`

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

func base64URLEncode(data []byte) string {
	return strings.TrimRight(base64.URLEncoding.EncodeToString(data), "=")
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
		entries = []entry{
			{"[Content_Types].xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/ppt/presentation.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.presentation.main+xml"/></Types>`},
			{"_rels/.rels", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="ppt/presentation.xml"/></Relationships>`},
			{"ppt/presentation.xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><p:presentation xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main"><p:sldIdLst><p:sldId id="256" r:id="rId1" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"/></p:sldIdLst></p:presentation>`},
			{"ppt/_rels/presentation.xml.rels", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slide" Target="slides/slide1.xml"/></Relationships>`},
			{"ppt/slides/slide1.xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><p:sld xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main"><p:cSld><p:spTree><p:nvGrpSpPr><p:cNvPr id="1" name=""/><p:cNvGrpSpPr/><p:nvPr/></p:nvGrpSpPr><p:grpSpPr/></p:spTree></p:cSld></p:sld>`},
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
