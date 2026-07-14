// Package user 提供用户身份提取的统一接口
package user

import (
	"net"
	"net/http"
	"strings"
)

// User 代表一个已识别用户
type User struct {
	ID       string
	Name     string
	IsAdmin  bool
}

// Provider 用户身份提供者
type Provider struct{}

// NewProvider 创建用户身份提供者
func NewProvider() *Provider {
	return &Provider{}
}

// GetCurrentUser 从请求中提取用户身份
// 优先级（从高到低）：
// 1. X-Trim-UserID / X-Trim-Username（FNOS nginx auth 模块注入）
// 2. X-Auth-UID / X-Auth-Username（其他系统兼容）
// 3. X-Forwarded-User（通用反向代理）
// 4. Remote-User（HTTP Basic Auth）
// 5. user_id / user_name query（CGI 兼容）
func (p *Provider) GetCurrentUser(r *http.Request) User {
	u := User{
		ID:   p.getUserID(r),
		Name: p.getUserName(r),
	}
	if u.ID == "" {
		u.ID = "anonymous"
	}
	if u.Name == "" {
		u.Name = "FNos 用户"
	}
	return u
}

// EffectiveID 获取用于隔离的用户标识
// 无真实用户时使用客户端 IP
func (p *Provider) EffectiveID(r *http.Request) string {
	if id := p.getUserID(r); id != "" {
		return id
	}
	return "ip_" + clientIP(r)
}

// ClientIP 获取客户端真实 IP
func (p *Provider) ClientIP(r *http.Request) string {
	return clientIP(r)
}

// IsAdmin 判断是否为管理员
func (p *Provider) IsAdmin(r *http.Request) bool {
	return r.Header.Get("X-Trim-IsAdmin") == "true" ||
		r.URL.Query().Get("is_admin") == "true"
}

func (p *Provider) getUserID(r *http.Request) string {
	if uid := r.Header.Get("X-Trim-UserID"); uid != "" {
		return uid
	}
	if uid := r.Header.Get("X-Auth-UID"); uid != "" {
		return uid
	}
	if uid := r.Header.Get("X-Forwarded-User"); uid != "" {
		return uid
	}
	if uid := r.Header.Get("Remote-User"); uid != "" {
		return uid
	}
	return r.URL.Query().Get("user_id")
}

func (p *Provider) getUserName(r *http.Request) string {
	if n := r.Header.Get("X-Trim-Username"); n != "" {
		return n
	}
	if n := r.Header.Get("X-Auth-Username"); n != "" {
		return n
	}
	if n := r.Header.Get("X-Forwarded-User"); n != "" {
		return n
	}
	return r.URL.Query().Get("user_name")
}

func clientIP(r *http.Request) string {
	if ip := r.Header.Get("X-Real-IP"); ip != "" {
		return ip
	}
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		if idx := strings.Index(fwd, ","); idx > 0 {
			return strings.TrimSpace(fwd[:idx])
		}
		return strings.TrimSpace(fwd)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
