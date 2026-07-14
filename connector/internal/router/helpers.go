package router

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// writeJSON 便捷 JSON 响应
func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

// docServerURL 返回从 OnlyOffice Docker 可访问的连接器地址
func (rt *Mux) docServerURL(r *http.Request) string {
	if rt.cfg.Network.PublicBaseURL != "" {
		return rt.cfg.Network.PublicBaseURL
	}
	if rt.cfg.Network.BaseURL != "" {
		return rt.cfg.Network.BaseURL
	}
	return fmt.Sprintf("http://host.docker.internal:%d", rt.cfg.Server.Port)
}

func ext(filePath string) string {
	return strings.TrimPrefix(filepath.Ext(filePath), ".")
}

func pathName(filePath string) string {
	return filepath.Base(filePath)
}

// docKey 生成文档唯一标识
func docKey(filePath string) string {
	info, err := os.Stat(filePath)
	if err != nil {
		h := sha256.Sum256([]byte(filePath + time.Now().String()))
		return fmt.Sprintf("%x", h)[:20]
	}
	keyData := fmt.Sprintf("%s|%d", filePath, info.ModTime().UnixNano())
	h := sha256.Sum256([]byte(keyData))
	return fmt.Sprintf("%x", h)[:20]
}

// docType 返回 OnlyOffice 文档类型
func docType(filePath string) string {
	e := ext(filePath)
	switch e {
	case "docx", "doc", "odt", "rtf", "txt", "html", "epub", "fb2":
		return "word"
	case "xlsx", "xls", "ods", "csv":
		return "cell"
	case "pptx", "ppt", "odp":
		return "slide"
	}
	return "word"
}

// signJWT 签署 JWT
func signJWT(data interface{}, secret string) string {
	if secret == "" {
		return ""
	}
	b, _ := json.Marshal(data)
	header := base64URLEncode([]byte(`{"alg":"HS256","typ":"JWT"}`))
	body := base64URLEncode(b)
	signing := header + "." + body
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(signing))
	sig := base64URLEncode(mac.Sum(nil))
	return signing + "." + sig
}

func base64URLEncode(data []byte) string {
	return strings.TrimRight(base64.URLEncoding.EncodeToString(data), "=")
}
