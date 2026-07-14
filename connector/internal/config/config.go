// Package config 提供 OfficeEditor 连接器的统一配置管理
package config

import (
	"os"
	"strconv"
)

// Config 应用全部配置
type Config struct {
	// 服务器
	Server ServerConfig

	// OnlyOffice Document Server
	OnlyOffice OnlyOfficeConfig

	// 网络
	Network NetworkConfig

	// 路径
	Paths PathsConfig

	// JWT
	JWTSecret string

	// 日志
	LogLevel string
}

type ServerConfig struct {
	Port int    // 连接器监听端口（默认 10088）
	Host string // 监听地址（默认 0.0.0.0）
}

type OnlyOfficeConfig struct {
	// InternalURL 是连接器访问 DocServer 的内部地址（默认 http://127.0.0.1:9080）
	InternalURL string
	// PublicPrefix 是浏览器访问 DocServer 的代理路径前缀（默认 /officeds）
	PublicPrefix string
}

type NetworkConfig struct {
	BaseURL       string   // 连接器内网地址（如 http://192.168.1.100:10088）
	PublicBaseURL string   // 连接器外网地址（如 https://fnos.xxx.com:10088）
	InternalCIDRs []string // 内网 CIDR 列表
}

type PathsConfig struct {
	// UserHome 用户文件默认存放目录（默认 /vol1/1000）
	UserHome string
	// DataDir 应用数据目录（默认 /var/apps/OfficeEditor/var）
	DataDir string
	// DockerComposeDir docker-compose.yaml 所在目录
	DockerComposeDir string
	// ImageDir 图片资源目录
	ImageDir string
}

// Load 从环境变量加载配置（兼容现有 --flag 和 ENV 方式）
func Load() *Config {
	cfg := &Config{
		Server: ServerConfig{
			Port: getEnvInt("PORT", 10088),
			Host: getEnv("HOST", "0.0.0.0"),
		},
		OnlyOffice: OnlyOfficeConfig{
			InternalURL:  getEnv("DOC_SERVER_URL", "http://127.0.0.1:9080"),
			PublicPrefix: getEnv("OO_PUBLIC_PREFIX", "/officeds"),
		},
		Network: NetworkConfig{
			BaseURL:       getEnv("BASE_URL", ""),
			PublicBaseURL: getEnv("PUBLIC_BASE_URL", ""),
			InternalCIDRs: []string{
				"127.0.0.0/8",
				"10.0.0.0/8",
				"172.16.0.0/12",
				"192.168.0.0/16",
			},
		},
		Paths: PathsConfig{
			UserHome:         getEnv("USER_HOME", "/vol1/1000"),
			DataDir:          getEnv("TRIM_PKGVAR", "/var/apps/OfficeEditor/var"),
			DockerComposeDir: getEnv("DOCKER_COMPOSE_DIR", "/var/apps/OfficeEditor/target/docker"),
			ImageDir:         getEnv("IMAGE_DIR", "/var/apps/OfficeEditor/target/ui/images"),
		},
		JWTSecret: getEnv("JWT_SECRET", ""),
		LogLevel:  getEnv("LOG_LEVEL", "info"),
	}

	// 自动推断 BaseURL
	if cfg.Network.BaseURL == "" {
		cfg.Network.BaseURL = "http://host.docker.internal:" + strconv.Itoa(cfg.Server.Port)
	}

	return cfg
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return fallback
}
