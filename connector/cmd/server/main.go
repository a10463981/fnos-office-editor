package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"fnos-office-editor/connector/internal/config"
	"fnos-office-editor/connector/internal/router"
)

func main() {
	port := flag.Int("port", 10088, "监听端口")
	docSvr := flag.String("doc-server", "http://127.0.0.1:9080", "OnlyOffice Document Server 地址")
	jwtSecret := flag.String("jwt-secret", "", "JWT 密钥")
	baseURL := flag.String("base-url", "", "内网连接器地址")
	pubBaseURL := flag.String("public-base-url", "", "外网连接器地址")
	flag.Parse()

	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)
	log.Printf("FNos OfficeEditor Office Gateway v1.0.29")
	log.Printf("架构: FNOS Port Gateway (已移除 CGI 依赖)")

	// config.Load() 已从环境变量读取配置
	// flag 值如果被设置（非默认）则覆盖配置

	// 加载配置（环境变量 > flag > 默认值）
	cfg := config.Load()

	// 命令行 flag 优先级高于 config.Load() 的环境变量读取
	// 保留 flag 覆盖能力
	cfg.Server.Port = *port
	if *docSvr != "http://127.0.0.1:9080" {
		cfg.OnlyOffice.InternalURL = *docSvr
	}
	if *jwtSecret != "" {
		cfg.JWTSecret = *jwtSecret
	}
	if *baseURL != "" {
		cfg.Network.BaseURL = *baseURL
	}
	if *pubBaseURL != "" {
		cfg.Network.PublicBaseURL = *pubBaseURL
	}

	// 构建路由
	rt := router.New(cfg)
	handler := rt.Handler()

	httpServer := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Server.Port),
		Handler:      handler,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 120 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	go func() {
		log.Printf("Office Gateway 启动，监听 :%d", cfg.Server.Port)
		log.Printf("  OnlyOffice: %s", cfg.OnlyOffice.InternalURL)
		log.Printf("  Base URL:   %s", cfg.Network.BaseURL)
		log.Printf("  Data Dir:   %s", cfg.Paths.DataDir)
		if cfg.JWTSecret != "" {
			log.Printf("  JWT: 已启用")
		}
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("启动失败: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("正在关闭...")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	httpServer.Shutdown(ctx)
	log.Println("已关闭")
}



