package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"cxbrush/internal/server"
)

// runServer 启动 Web 版：多用户 + 每个用户独立的学习通会话。
// 除监听地址/数据目录/注册开关外，其余配置都在页面里完成。
func runServer(args []string) int {
	fs := flag.NewFlagSet("cxbrush", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var (
		addr    = fs.String("addr", "127.0.0.1:8080", "监听地址")
		dataDir = fs.String("data", "", "数据目录（默认 ~/.local/share/cxbrush）")
		open    = fs.Bool("open", false, "允许任何人注册（默认只有第一个用户能注册）")
		workers = fs.Int("workers", 8, "全进程同时运行任务数的硬上限（每个用户自己的并行度在页面里设置）")
	)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	dir := *dataDir
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			fmt.Fprintln(os.Stderr, "找不到用户目录:", err)
			return 1
		}
		dir = filepath.Join(home, ".local", "share", "cxbrush")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	fontPath, fontData, err := resolveRefFont()
	if err != nil {
		fmt.Fprintln(os.Stderr, "警告：找不到比对字体，自动做题会不可用：", err)
	}

	return runServe(ctx, server.Options{
		Addr:      *addr,
		DataDir:   dir,
		FontPath:  fontPath,
		FontData:  fontData,
		AllowOpen: *open,
		Workers:   *workers,
	})
}

func runServe(ctx context.Context, opts server.Options) int {
	if err := server.Run(ctx, opts); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}
