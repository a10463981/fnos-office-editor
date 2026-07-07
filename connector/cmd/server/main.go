package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"fnos-office-editor/connector/internal/server"
)

func main() {
	port := flag.Int("port", 10099, "监听端口")
	docSvr := flag.String("doc-server", "http://127.0.0.1:9080", "OnlyOffice Document Server 地址")
	jwtSecret := flag.String("jwt-secret", "", "JWT 密钥")
	baseURL := flag.String("base-url", "", "内网连接器地址")
	pubBaseURL := flag.String("public-base-url", "", "外网连接器地址")
	flag.Parse()

	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)
	log.Printf("FNos OfficeEditor Connector v1.0.0")

	// 从环境变量读取（优先级高于命令行）
	if v := os.Getenv("PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil {
			*port = p
		}
	}
	if v := os.Getenv("DOC_SERVER_URL"); v != "" {
		*docSvr = v
	}
	if v := os.Getenv("JWT_SECRET"); v != "" {
		*jwtSecret = v
	}
	if v := os.Getenv("BASE_URL"); v != "" {
		*baseURL = v
	}
	if v := os.Getenv("PUBLIC_BASE_URL"); v != "" {
		*pubBaseURL = v
	}

	// 自动推断 baseURL
	if *baseURL == "" {
		*baseURL = fmt.Sprintf("http://localhost:%d", *port)
	}

	cfg := &server.Config{
		Port:             *port,
		DocServerURL:     *docSvr,
		JWTSecret:        *jwtSecret,
		BaseURL:          *baseURL,
		PublicBaseURL:    *pubBaseURL,
		InternalNetworks: []string{"127.0.0.0/8", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"},
	}

	handler := server.NewServer(cfg)

	httpServer := &http.Server{
		Addr:         fmt.Sprintf(":%d", *port),
		Handler:      handler,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 120 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	go func() {
		log.Printf("服务器启动，监听 :%d", *port)
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
