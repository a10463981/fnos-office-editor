// Package history 提供按用户隔离的文档历史记录
package history

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

const maxEntries = 50

// Entry 一条历史记录
type Entry struct {
	Path      string `json:"path"`
	Name      string `json:"name"`
	OpenedAt  string `json:"openedAt"`
	UserID    string `json:"userID,omitempty"`
	DocKey    string `json:"docKey,omitempty"`
	Version   int    `json:"version,omitempty"`
}

// Service 历史记录服务
type Service struct {
	dataDir string
}

// NewService 创建历史记录服务
func NewService(dataDir string) *Service {
	return &Service{dataDir: dataDir}
}

// Add 添加一条历史记录
func (s *Service) Add(userID, filePath, docKey string) {
	entries := s.load(userID)
	// 去重
	filtered := make([]Entry, 0, len(entries))
	for _, e := range entries {
		if e.Path != filePath {
			filtered = append(filtered, e)
		}
	}
	// 插到开头
	filtered = append([]Entry{{
		Path:     filePath,
		Name:     filepath.Base(filePath),
		OpenedAt: time.Now().Format("2006-01-02 15:04"),
		UserID:   userID,
		DocKey:   docKey,
	}}, filtered...)
	s.save(userID, filtered)
}

// List 列出用户的历史记录
func (s *Service) List(userID string) []Entry {
	entries := s.load(userID)
	if entries == nil {
		return []Entry{}
	}
	return entries
}

func (s *Service) filePath(userID string) string {
	if userID == "" {
		userID = "shared"
	}
	return s.dataDir + "/history_" + userID + ".json"
}

func (s *Service) load(userID string) []Entry {
	data, err := os.ReadFile(s.filePath(userID))
	if err != nil {
		return nil
	}
	var entries []Entry
	json.Unmarshal(data, &entries)
	return entries
}

func (s *Service) save(userID string, entries []Entry) {
	if len(entries) > maxEntries {
		entries = entries[len(entries)-maxEntries:]
	}
	data, _ := json.MarshalIndent(entries, "", "  ")
	os.MkdirAll(filepath.Dir(s.filePath(userID)), 0755)
	os.WriteFile(s.filePath(userID), data, 0644)
}
