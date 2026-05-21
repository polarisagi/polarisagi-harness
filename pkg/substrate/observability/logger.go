package observability

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

// logLevel 读取 LOG_LEVEL 环境变量，默认 Info。支持 debug/info/warn/error。
func logLevel() slog.Level {
	switch os.Getenv("LOG_LEVEL") {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// SetupLogger 在 dataDir 下创建 polaris.log，同时写 stdout 和文件。
// 返回日志文件句柄，调用方 defer close。
// 用 slog.SetDefault 替换全局 logger，整个进程的 slog.* 调用均写入。
func SetupLogger(dataDir string) *os.File {
	logPath := filepath.Join(dataDir, "polaris.log")
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		// 打开失败只写 stdout，不阻塞启动
		slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
			Level:     logLevel(),
			AddSource: false,
			ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
				if a.Key == slog.TimeKey {
					a.Value = slog.StringValue(a.Value.Time().Local().Format(time.RFC3339))
				}
				return a
			},
		})))
		return nil
	}

	mw := io.MultiWriter(os.Stdout, f)
	slog.SetDefault(slog.New(slog.NewTextHandler(mw, &slog.HandlerOptions{
		Level:     logLevel(),
		AddSource: false,
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				a.Value = slog.StringValue(a.Value.Time().Format(time.RFC3339))
			}
			return a
		},
	})))
	return f
}
