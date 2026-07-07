package config

import (
	"encoding/json"
	"net"
	"os"
	"strconv"
	"strings"
)

// Config 连接器配置
type Config struct {
	Port             int      `json:"port"`              // 监听端口
	DocServerURL     string   `json:"doc_server_url"`    // OnlyOffice Document Server 地址
	DocServerPubURL  string   `json:"doc_server_pub_url"` // 外部 Document Server 地址
	JWTSecret        string   `json:"jwt_secret"`        // JWT 密钥
	LogDir           string   `json:"log_dir"`           // 日志目录
	DataDir          string   `json:"data_dir"`          // 数据目录
	BaseURL          string   `json:"base_url"`          // 连接器自身 base URL
	PublicBaseURL    string   `json:"public_base_url"`   // 外部连接器 base URL
	InternalNetworks []string `json:"internal_networks"` // 内网网段列表
}

// LoadFromEnv 从环境变量加载配置
func LoadFromEnv() *Config {
	cfg := &Config{
		Port:             10099,
		DocServerURL:     "http://127.0.0.1:9080",
		JWTSecret:        getEnv("JWT_SECRET", "default-secret-change-me"),
		LogDir:           getEnv("TRIM_PKGVAR", "/var/apps/OfficeEditor/var") + "/log",
		DataDir:          getEnv("TRIM_PKGVAR", "/var/apps/OfficeEditor/var") + "/data",
		BaseURL:          getEnv("BASE_URL", "http://localhost:10099"),
		PublicBaseURL:    getEnv("PUBLIC_BASE_URL", ""),
		InternalNetworks: []string{"127.0.0.0/8", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"},
	}

	if dsURL := getEnv("DOC_SERVER_URL", ""); dsURL != "" {
		cfg.DocServerURL = dsURL
	}
	if jwtSecret := getEnv("JWT_SECRET", ""); jwtSecret != "" {
		cfg.JWTSecret = jwtSecret
	}
	if portStr := getEnv("PORT", ""); portStr != "" {
		p, _ := strconv.Atoi(portStr)
		if p > 0 {
			cfg.Port = p
		}
	}

	return cfg
}

// LoadFromFile 从文件加载配置
func LoadFromFile(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	cfg := &Config{}
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// IsInternalRequest 判断请求是否来自内网
func (c *Config) IsInternalRequest(host string) bool {
	host = strings.Split(host, ":")[0] // 去掉端口
	if host == "localhost" || host == "127.0.0.1" || host == "::1" {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false // 域名视为外网
	}
	for _, cidr := range c.InternalNetworks {
		_, network, _ := net.ParseCIDR(cidr)
		if network != nil && network.Contains(ip) {
			return true
		}
	}
	return false
}

// GetEffectiveDocServerURL 根据是否是内网请求返回合适的 Document Server URL
func (c *Config) GetEffectiveDocServerURL(isInternal bool) string {
	if isInternal || c.DocServerPubURL == "" {
		return c.DocServerURL
	}
	return c.DocServerPubURL
}

// GetEffectiveBaseURL 根据是否是内网请求返回合适的连接器 base URL
func (c *Config) GetEffectiveBaseURL(isInternal bool) string {
	if isInternal || c.PublicBaseURL == "" {
		return c.BaseURL
	}
	return c.PublicBaseURL
}

func getEnv(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}
