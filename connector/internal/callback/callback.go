// Package callback 处理 OnlyOffice Document Server 的回调
package callback

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
)

// Payload OnlyOffice 回调请求体
type Payload struct {
	Status int    `json:"status"`
	URL    string `json:"url"`
}

// Handler 回调处理器
type Handler struct{}

// NewHandler 创建回调处理器
func NewHandler() *Handler {
	return &Handler{}
}

// Handle 处理 OnlyOffice 保存回调
// status=2: 文档已就绪，需要下载
// status=6: 正在保存，需要下载
func (h *Handler) Handle(w http.ResponseWriter, r *http.Request, filePath string) {
	if filePath == "" {
		json.NewEncoder(w).Encode(map[string]int{"error": 1})
		return
	}

	var cb Payload
	if err := json.NewDecoder(r.Body).Decode(&cb); err != nil {
		json.NewEncoder(w).Encode(map[string]int{"error": 1})
		return
	}
	log.Printf("callback: status=%d url=%s path=%s", cb.Status, cb.URL, filePath)

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

		written, _ := io.Copy(out, resp.Body)
		log.Printf("file saved: %s (%d bytes)", filePath, written)
	}

	json.NewEncoder(w).Encode(map[string]int{"error": 0})
}

// BuildDownloadURL 构建文档下载 URL（供 DocServer 使用）
func BuildDownloadURL(baseURL, filePath string) string {
	return fmt.Sprintf("%s/api/download?path=%s", baseURL, urlParam(filePath))
}

// BuildCallbackURL 构建回调 URL（供 DocServer 使用）
func BuildCallbackURL(baseURL, filePath string) string {
	return fmt.Sprintf("%s/api/callback?path=%s", baseURL, urlParam(filePath))
}

func urlParam(s string) string {
	return fmt.Sprintf("%s", s)
	// Note: 实际 URL 编码由调用方负责
}
