package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"fnos-office-editor/connector/internal/config"
	"fnos-office-editor/connector/internal/server"
)

const (
	defaultPort     = "10099"
	shutdownTimeout = 15 * time.Second
)

func main() {
	// 命令行参数
	port := flag.String("port", defaultPort, "监听端口")
	configPath := flag.String("config", "", "配置文件路径")
	flag.Parse()

	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)
	log.Printf("FNos OfficeEditor Connector v1.0.0")
	log.Printf("端口: %s", *port)

	// 加载配置
	var cfg *config.Config
	if *configPath != "" {
		data, err := os.ReadFile(*configPath)
		if err != nil {
			log.Fatalf("读取配置文件失败: %v", err)
		}
		cfg = &config.Config{}
		if err := json.Unmarshal(data, cfg); err != nil {
			log.Fatalf("解析配置文件失败: %v", err)
		}
	} else {
		// 从环境变量读取
		cfg = config.LoadFromEnv()
	}

	if cfg.Port == 0 {
		p, _ := strconv.Atoi(*port)
		cfg.Port = p
	}

	log.Printf("Document Server URL: %s", cfg.DocServerURL)
	log.Printf("JWT Secret: %s", maskSecret(cfg.JWTSecret))

	// 创建 HTTP 服务器
	srv := server.New(cfg)

	httpServer := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Port),
		Handler:      srv.Router(),
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 120 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// 启动
	go func() {
		log.Printf("服务器已启动，监听 :%d", cfg.Port)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("服务器错误: %v", err)
		}
	}()

	// 等待退出信号
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("正在关闭服务器...")
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	if err := httpServer.Shutdown(ctx); err != nil {
		log.Fatalf("关闭服务器失败: %v", err)
	}
	log.Println("服务器已关闭")
}

func maskSecret(s string) string {
	if len(s) <= 8 {
		return "***"
	}
	return s[:4] + "..." + s[len(s)-4:]
}
