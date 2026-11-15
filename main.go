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
)

var Version = "dev"

func main() {
	cfgPath := flag.String("config", defaultConfigPath(), "配置文件路径（或 RKMON_CONFIG）")
	showVersion := flag.Bool("version", false, "打印版本并退出")
	genPass := flag.Bool("gen-password", false, "生成随机强密码并退出")
	initCfg := flag.Bool("init", false, "初始化配置：生成随机强密码，写入 0600 配置（密码只打印一次）")
	flag.Parse()

	log.SetFlags(log.LstdFlags)
	log.SetOutput(os.Stderr)

	if *showVersion {
		fmt.Println("rkmon " + Version)
		return
	}
	if *genPass {
		p, err := randomPassword(24)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(p)
		return
	}
	if *initCfg {
		if err := InitConfig(*cfgPath); err != nil {
			log.Fatalf("初始化失败：%v", err)
		}
		return
	}

	cfg, err := LoadConfig(*cfgPath)
	if err != nil {
		log.Fatalf("配置加载失败：%v", err)
	}

	state := NewState(cfg)
	go state.Loop(context.Background())

	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           NewHandler(cfg, state),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       30 * time.Second,
	}

	go func() {
		log.Printf("rkmon %s 启动，监听 %s（只读）", Version, cfg.Listen)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("HTTP 服务失败：%v", err)
		}
	}()

	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	<-ch
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
	log.Println("已停止")
}
