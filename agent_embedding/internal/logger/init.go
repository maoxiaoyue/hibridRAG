package logger

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	hyplogger "github.com/maoxiaoyue/hypgo/pkg/logger"
	"gopkg.in/natefinch/lumberjack.v2"
)

type Config struct {
	Level      string `yaml:"level"`
	Output     string `yaml:"output"`
	File       string `yaml:"file"`
	MaxSize    int    `yaml:"max_size"`
	MaxAge     int    `yaml:"max_age"`
	MaxBackups int    `yaml:"max_backups"`
	Compress   bool   `yaml:"compress"`
	Colors     bool   `yaml:"colors"`
}

var instance *hyplogger.Logger

func Init(cfg Config) (*hyplogger.Logger, error) {
	if cfg.Level == "" {
		cfg.Level = "info"
	}
	if cfg.Output == "" {
		cfg.Output = "stdout"
	}
	if cfg.MaxSize == 0 {
		cfg.MaxSize = 100
	}
	if cfg.MaxAge == 0 {
		cfg.MaxAge = 30
	}
	if cfg.MaxBackups == 0 {
		cfg.MaxBackups = 10
	}

	var writer io.Writer
	switch cfg.Output {
	case "file":
		writer = createFileWriter(cfg)
	case "both":
		writer = io.MultiWriter(os.Stdout, createFileWriter(cfg))
	default:
		writer = os.Stdout
	}

	log, err := hyplogger.New(cfg.Level, cfg.Output, writer, cfg.Colors)
	if err != nil {
		return nil, fmt.Errorf("failed to create logger: %w", err)
	}
	instance = log
	return log, nil
}

func createFileWriter(cfg Config) io.Writer {
	if cfg.File == "" {
		cfg.File = "logs/app.log"
	}
	dir := filepath.Dir(cfg.File)
	if err := os.MkdirAll(dir, 0755); err != nil {
		fmt.Printf("Failed to create log directory: %v\n", err)
	}
	return &lumberjack.Logger{
		Filename:   generateLogFilename(cfg.File),
		MaxSize:    cfg.MaxSize,
		MaxAge:     cfg.MaxAge,
		MaxBackups: cfg.MaxBackups,
		LocalTime:  true,
		Compress:   cfg.Compress,
	}
}

func generateLogFilename(baseFile string) string {
	dir := filepath.Dir(baseFile)
	ext := filepath.Ext(baseFile)
	name := filepath.Base(baseFile)
	if ext != "" {
		name = name[:len(name)-len(ext)]
	}
	return filepath.Join(dir, fmt.Sprintf("%s-%s%s", name, time.Now().Format("2006-01-02"), ext))
}

func Get() *hyplogger.Logger {
	if instance == nil {
		instance = hyplogger.NewLogger()
	}
	return instance
}

// v0.8.11：明確用 printf 系列（呼叫端傳的都是 format string）；
// hyplogger 的 Info/Warning/Error 在 v0.8.11 改為 KV 語意（msg, key, value…），
// 傳含 %v/%s 的字串會被當純文字，不做格式化。
func Info(format string, args ...interface{})    { Get().Infof(format, args...) }
func Warning(format string, args ...interface{}) { Get().Warningf(format, args...) }
func Error(format string, args ...interface{})   { Get().Errorf(format, args...) }
