package router

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

func (rt *Mux) renderEditorPage(w http.ResponseWriter, r *http.Request, filePath string) {
	u := rt.userProv.GetCurrentUser(r)
	dockerURL := rt.docServerURL(r)
	configJSON := buildEditorConfigJSON(filePath, u.ID, u.Name, dockerURL, rt.cfg.JWTSecret, r.Host, rt.cfg.OnlyOffice.PublicPrefix)

	rt.history.Add(rt.userProv.EffectiveID(r), filePath, "")

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, editorPageHTML, configJSON)
}

func (rt *Mux) renderHomePage(w http.ResponseWriter, r *http.Request, opts ...string) {
	u := rt.userProv.GetCurrentUser(r)
	dir := r.URL.Query().Get("dir")
	if dir == "" {
		dir = rt.cfg.Paths.UserHome
	}
	userID := u.ID
	userName := u.Name
	isAdmin := rt.userProv.IsAdmin(r)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	html := strings.Replace(homePageHTML, "USER_DIR_PLACEHOLDER", dir, 1)
	html = strings.Replace(html, "USER_NAME_PLACEHOLDER", userName, -1)
	html = strings.Replace(html, "USER_ID_PLACEHOLDER", userID, 1)
	if isAdmin {
		html = strings.Replace(html, "IS_ADMIN_PLACEHOLDER", "", 1)
	} else {
		html = strings.Replace(html, "IS_ADMIN_PLACEHOLDER", "HIDDEN_BY_ADMIN_CHECK", 1)
	}
	fmt.Fprint(w, html)
}

// buildEditorConfigJSON 构建 OnlyOffice 编辑器初始化配置 JSON
func buildEditorConfigJSON(filePath, userID, userName, baseURL, jwtSecret, host, ooPrefix string) string {
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
			"url":      fmt.Sprintf("%s/api/download?path=%s", baseURL, url.QueryEscape(filePath)),
			"permissions": map[string]interface{}{
				"edit": editable(ext), "download": true, "print": true,
			},
		},
		"documentType": docType(filePath),
		"editorConfig": map[string]interface{}{
			"callbackUrl": fmt.Sprintf("%s/api/callback?path=%s", baseURL, url.QueryEscape(filePath)),
			"serverUrl":   fmt.Sprintf("http://%s%s", host, ooPrefix),
			"lang":        "zh",
			"mode":        "edit",
			"user": map[string]interface{}{
				"id": userID, "name": userName,
			},
		},
	}
	b, _ := json.Marshal(config)
	return string(b)
}

func editable(ext string) bool {
	switch ext {
	case "docx", "xlsx", "pptx", "doc", "xls", "ppt", "odt", "ods", "odp":
		return true
	}
	return false
}

// ========== HTML Templates ==========

const editorPageHTML = `<!DOCTYPE html>
<html><head><meta charset="UTF-8">
<title>FNos Office Editor</title>
<style>html,body{height:100%%;margin:0;overflow:hidden}#editor{width:100%%;height:100%%}</style>
</head><body><div id="editor"></div>
<script src="officeds/web-apps/apps/api/documents/api.js"></script>
<script>
var config=%s;
var editor=new DocsAPI.DocEditor("editor",config);
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

<div class="modal-overlay" id="settingsModal">
  <div class="modal">
    <h3>⚙️ 字体设置</h3>
    <label>自定义字体目录路径</label>
    <input type="text" id="fontsDirInput" placeholder="/vol1/1000/">
    <p style="font-size:12px;color:#999;margin:-8px 0 12px 0">设置后重启 Docker 生效。</p>
    <div class="actions">
      <button class="btn-cancel" onclick="closeSettings()">取消</button>
      <button class="btn-save" id="btnSaveSettings" onclick="saveSettings()">保存</button>
    </div>
  </div>
</div>
<div class="toast" id="toast"></div>
<script>
var userDir="USER_DIR_PLACEHOLDER";
var userName="USER_NAME_PLACEHOLDER";
var userId="USER_ID_PLACEHOLDER";
var apiBase="";
function toast(msg){var t=document.getElementById("toast");t.textContent=msg;t.classList.add("show");setTimeout(function(){t.classList.remove("show")},2000)}
function createDoc(type){
  var btn=event.target;
  btn.disabled=true;
  btn.innerHTML='<span class="spinner"></span>';
  fetch(apiBase+"api/create?type="+type+"&dir="+encodeURIComponent(userDir)+"&user_id="+encodeURIComponent(userId)+"&user_name="+encodeURIComponent(userName),{method:"POST"})
    .then(r=>r.json())
    .then(d=>{
      if(d.error){toast("创建失败: "+d.error);btn.disabled=false;return}
      window.location.href="?path="+encodeURIComponent(d.path)+"&user_id="+encodeURIComponent(userId)+"&user_name="+encodeURIComponent(userName);
    })
    .catch(e=>{toast("创建失败");btn.disabled=false})
}
function loadHistory(){
  fetch(apiBase+"api/history?user_id="+encodeURIComponent(userId))
    .then(r=>r.json())
    .then(items=>{
      var h=document.getElementById("history");
      if(!items.length){h.innerHTML='<div class="empty">还没有打开过文档</div>';return}
      var icons={"docx":"📝","xlsx":"📊","pptx":"📽️","doc":"📝","xls":"📊","ppt":"📽️","pdf":"📕","txt":"📄"};
      h.innerHTML=items.map(function(i){
        var ext=i.name.split(".").pop().toLowerCase();
        return '<a class="history-item" href="?path='+encodeURIComponent(i.path)+'&user_id='+encodeURIComponent(userId)+'&user_name='+encodeURIComponent(userName)+'"><span class="icon">'+(icons[ext]||"📄")+'</span><div class="info"><div class="name">'+i.name+'</div><div class="time">'+i.openedAt+'</div></div></a>'
      }).join("");
    })
}
function openSettings(){
  document.getElementById("settingsModal").classList.add("show");
  fetch(apiBase+"api/config").then(r=>r.json()).then(c=>{
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
  btn.disabled=true;btn.textContent="保存中...";
  fetch(apiBase+"api/config",{method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify({fontsDir:dir})})
    .then(r=>{if(!r.ok)throw new Error(r.status);return r.json()})
    .then(d=>{toast(d.ok?"已保存":"保存失败");closeSettings()})
    .catch(e=>{toast("保存失败: "+e.message)})
    .finally(function(){btn.disabled=false;btn.textContent="保存";});
}
fetch(apiBase+"api/check-update");
loadHistory();
</script>
</body></html>`
