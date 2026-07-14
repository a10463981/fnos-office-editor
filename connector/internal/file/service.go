// Package file 提供文件操作服务
package file

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Service 文件服务
type Service struct {
	userHome string
}

// NewService 创建文件服务
func NewService(userHome string) *Service {
	return &Service{userHome: userHome}
}

// CreateResult 创建文档的结果
type CreateResult struct {
	Path string `json:"path"`
	Name string `json:"name"`
}

// CreateDocument 创建新的 Office 文档
func (s *Service) CreateDocument(docType, dir string) (*CreateResult, error) {
	if dir == "" {
		dir = s.userHome
	}

	ext, ok := map[string]string{"docx": "docx", "xlsx": "xlsx", "pptx": "pptx"}[docType]
	if !ok {
		return nil, fmt.Errorf("invalid type: %s", docType)
	}

	typeName := map[string]string{"docx": "Word", "xlsx": "Excel", "pptx": "PowerPoint"}[docType]
	ts := time.Now().Format("20060102_150405")
	name := fmt.Sprintf("新建%s文档_%s.%s", typeName, ts, ext)
	filePath := filepath.Join(dir, name)

	tmpl := ooxmlTemplate(docType)
	f, err := os.Create(filePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	if tmpl != nil {
		f.Write(tmpl)
	} else {
		f.Write([]byte{0x50, 0x4B, 0x05, 0x06, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0})
	}

	return &CreateResult{Path: filePath, Name: name}, nil
}

// ServeDownload 提供文件下载
func (s *Service) ServeDownload(w http.ResponseWriter, r *http.Request, filePath string) bool {
	if filePath == "" || !SafePath(filePath) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return false
	}
	http.ServeFile(w, r, filePath)
	return true
}

// SafePath 检查路径是否安全
func SafePath(p string) bool {
	if p == "" || strings.Contains(p, "..") {
		return false
	}
	return strings.HasPrefix(p, "/vol") || strings.HasPrefix(p, "/tmp/")
}
