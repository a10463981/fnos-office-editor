// Package office 提供 OnlyOffice Document Server 的代理网关
package office

import (
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
)

// Gateway 负责将浏览器请求代理到 OnlyOffice Document Server
// 处理路径转换、WebSocket 升级、CORS 头注入
type Gateway struct {
	proxy        *httputil.ReverseProxy
	backend      string
	publicPrefix string
}

// NewGateway 创建 OnlyOffice 代理网关
// backend: DocServer 内部地址 (http://127.0.0.1:9080)
// publicPrefix: 浏览器访问的代理前缀 (/officeds)
func NewGateway(backend, publicPrefix string) *Gateway {
	ooURL, _ := url.Parse(backend)
	ooProxy := httputil.NewSingleHostReverseProxy(ooURL)
	ooProxy.ModifyResponse = func(r *http.Response) error {
		r.Header.Set("Access-Control-Allow-Origin", "*")
		r.Header.Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS, PUT")
		r.Header.Set("Access-Control-Allow-Headers", "Origin, Content-Type, X-Requested-With, Authorization")
		return nil
	}

	return &Gateway{
		proxy:        ooProxy,
		backend:      backend,
		publicPrefix: strings.TrimRight(publicPrefix, "/"),
	}
}

// HandleProxy 处理代理请求（前缀已剥离）
func (g *Gateway) HandleProxy(w http.ResponseWriter, r *http.Request) {
	g.proxy.ServeHTTP(w, r)
}

// ShouldHandle 判断是否应该由本网关处理
func (g *Gateway) ShouldHandle(path string) bool {
	return strings.HasPrefix(path, g.publicPrefix+"/") ||
		strings.HasPrefix(path, "/cache/")
}

// StripPrefix 剥离前缀返回后端路径
func (g *Gateway) StripPrefix(path string) string {
	path = strings.TrimPrefix(path, g.publicPrefix)
	if path == "" {
		path = "/"
	}
	return path
}

// PublicPrefix 返回公共前缀
func (g *Gateway) PublicPrefix() string {
	return g.publicPrefix
}

// BackendURL 返回后端地址
func (g *Gateway) BackendURL() string {
	return g.backend
}
