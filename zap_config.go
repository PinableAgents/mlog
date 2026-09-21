package mlog

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap/zapcore"
)

type ZapConfig struct {
	MaxRouteWriters int    `json:"max-route-writers" yaml:"max-route-writers"` // Per-core open route limit; default 128.
	Level           string `mapstructure:"level" json:"level" yaml:"level"`
	Prefix          string `mapstructure:"prefix" json:"prefix" yaml:"prefix"`
	Format          string `mapstructure:"format" json:"format" yaml:"format"`
	Director        string `mapstructure:"director" json:"director"  yaml:"director"`
	EncodeLevel     string `mapstructure:"encode-level" json:"encode-level" yaml:"encode-level"`
	StacktraceKey   string `mapstructure:"stacktrace-key" json:"stacktrace-key" yaml:"stacktrace-key"`
	ShowLine        bool   `mapstructure:"show-line" json:"show-line" yaml:"show-line"`
	LogInConsole    bool   `mapstructure:"log-in-console" json:"log-in-console" yaml:"log-in-console"`
	RetentionDay    int    `mapstructure:"retention-day" json:"retention-day" yaml:"retention-day"`
	MaxSize         int    `mapstructure:"max-size" json:"max-size" yaml:"max-size"`
	MaxBackups      int    `mapstructure:"max-backups" json:"max-backups" yaml:"max-backups"`
	EnableSplit     bool   `mapstructure:"enable-split" json:"enable-split" yaml:"enable-split"`
	EnableCompress  bool   `mapstructure:"enable-compress" json:"enable-compress" yaml:"enable-compress"`

	EnableAsync     bool `mapstructure:"enable-async" json:"enable-async" yaml:"enable-async"`
	AsyncBufferSize int  `mapstructure:"async-buffer-size" json:"async-buffer-size" yaml:"async-buffer-size"`
	AsyncDropOnFull bool `mapstructure:"async-drop-on-full" json:"async-drop-on-full" yaml:"async-drop-on-full"`

	UseRelativePath bool   `mapstructure:"use-relative-path" json:"use-relative-path" yaml:"use-relative-path"`
	BuildRootPath   string `mapstructure:"build-root-path" json:"build-root-path" yaml:"build-root-path"`

	SingleFile     bool   `mapstructure:"single-file" json:"single-file" yaml:"single-file"`
	SingleFileName string `mapstructure:"single-file-name" json:"single-file-name" yaml:"single-file-name"`
}

// Levels
func (c *ZapConfig) Levels() []zapcore.Level {
	levels := make([]zapcore.Level, 0, 7)
	level := zapcore.DebugLevel
	for ; level <= zapcore.FatalLevel; level++ {
		levels = append(levels, level)
	}
	return levels
}

func (c *ZapConfig) Encoder() zapcore.Encoder {
	appendTime := zapcore.TimeEncoderOfLayout("2006-01-02 15:04:05.000")
	config := zapcore.EncoderConfig{
		TimeKey:       "time",
		NameKey:       "name",
		LevelKey:      "level",
		CallerKey:     "caller",
		MessageKey:    "message",
		StacktraceKey: c.StacktraceKey,
		LineEnding:    zapcore.DefaultLineEnding,
		EncodeTime: func(t time.Time, encoder zapcore.PrimitiveArrayEncoder) {
			// Keep observing Prefix on the receiver, as the original public
			// Encoder method did, but avoid a temporary string when it is empty.
			if c.Prefix == "" {
				appendTime(t, encoder)
				return
			}
			encoder.AppendString(c.Prefix + t.Format("2006-01-02 15:04:05.000"))
		},
		EncodeLevel:    c.LevelEncoder(),
		EncodeCaller:   c.CallerEncoder(),
		EncodeDuration: zapcore.SecondsDurationEncoder,
	}
	if c.Format == "json" {
		return zapcore.NewJSONEncoder(config)
	}
	return zapcore.NewConsoleEncoder(config)

}

func (c *ZapConfig) LevelEncoder() zapcore.LevelEncoder {
	switch {
	case c.EncodeLevel == "LowercaseLevelEncoder":
		return zapcore.LowercaseLevelEncoder
	case c.EncodeLevel == "LowercaseColorLevelEncoder":
		return zapcore.LowercaseColorLevelEncoder
	case c.EncodeLevel == "CapitalLevelEncoder":
		return zapcore.CapitalLevelEncoder
	case c.EncodeLevel == "CapitalColorLevelEncoder":
		return zapcore.CapitalColorLevelEncoder
	default:
		return zapcore.LowercaseLevelEncoder
	}
}

func (c *ZapConfig) CallerEncoder() zapcore.CallerEncoder {
	if !c.UseRelativePath {
		return zapcore.FullCallerEncoder
	}
	pc := newPathCache(workingDir, c.BuildRootPath)
	return func(caller zapcore.EntryCaller, enc zapcore.PrimitiveArrayEncoder) {
		if !caller.Defined {
			enc.AppendString("undefined")
			return
		}
		enc.AppendString(pc.getRelativePathCached(caller.File) + ":" + strconv.Itoa(caller.Line))
	}
}

func RelativeCallerEncoder(caller zapcore.EntryCaller, enc zapcore.PrimitiveArrayEncoder) {
	if !caller.Defined {
		enc.AppendString("undefined")
		return
	}

	relativePath := getRelativePath(caller.File)
	enc.AppendString(relativePath + ":" + strconv.Itoa(caller.Line))
}

func getRelativePath(absolutePath string) string {
	if pc := globalPathCache.Load(); pc != nil {
		return pc.getRelativePathCached(absolutePath)
	}

	return getRelativePathLegacy(absolutePath)
}

func getRelativePathLegacy(absolutePath string) string {
	if cfg := GetConfig(); cfg.BuildRootPath != "" {
		if relPath := getRelativePathFromBuildRoot(absolutePath, cfg.BuildRootPath); relPath != "" {
			return relPath
		}
	}

	if workingDir == "" {
		return extractRelativeFromPath(absolutePath)
	}

	if !strings.Contains(absolutePath, workingDir) {
		return absolutePath
	}

	if relPath, err := filepath.Rel(workingDir, absolutePath); err == nil {
		if strings.HasPrefix(relPath, "../") {
			return absolutePath
		}
		return relPath
	}

	return extractRelativeFromPath(absolutePath)
}

func getRelativePathFromBuildRoot(absolutePath, buildRootPath string) string {
	cleanAbsPath := filepath.Clean(absolutePath)
	cleanBuildRoot := filepath.Clean(buildRootPath)

	if !strings.HasPrefix(cleanAbsPath, cleanBuildRoot) {
		return ""
	}

	if relPath, err := filepath.Rel(cleanBuildRoot, cleanAbsPath); err == nil {
		if !strings.HasPrefix(relPath, "../") && relPath != "." {
			return relPath
		}
	}

	return ""
}

func extractRelativeFromPath(absolutePath string) string {
	parts := strings.Split(absolutePath, string(filepath.Separator))

	for i, part := range parts {
		if part == "aimmo" || part == "plugin" {
			return strings.Join(parts[i:], string(filepath.Separator))
		}
	}

	if len(parts) >= 2 {
		return strings.Join(parts[len(parts)-2:], string(filepath.Separator))
	}

	return filepath.Base(absolutePath)
}

// Validate normalizes defaults on this configuration value, not a live logger.
func (c *ZapConfig) Validate() error {
	if c.Director == "" {
		c.Director = "logs"
	}
	if c.Level == "" {
		c.Level = "info"
	}
	if _, err := zapcore.ParseLevel(c.Level); err != nil {
		return fmt.Errorf("mlog: invalid level: %w", err)
	}
	if c.Format == "" {
		c.Format = "console"
	}
	if c.Format != "console" && c.Format != "json" {
		return fmt.Errorf("mlog: invalid format %q", c.Format)
	}
	if c.SingleFileName != "" && !validComponent(c.SingleFileName) {
		return fmt.Errorf("mlog: invalid filename %q", c.SingleFileName)
	}
	if c.MaxSize < 0 || c.MaxBackups < 0 || c.RetentionDay < 0 {
		return fmt.Errorf("mlog: negative rotation limit")
	}
	if c.AsyncBufferSize < 0 || c.AsyncBufferSize > 1000000 {
		return fmt.Errorf("mlog: async buffer must be between 0 and 1000000")
	}
	if c.AsyncBufferSize == 0 {
		c.AsyncBufferSize = 10000
	}
	if c.MaxRouteWriters < 0 {
		return fmt.Errorf("mlog: negative route limit")
	}
	if c.MaxRouteWriters == 0 {
		c.MaxRouteWriters = 128
	}
	return nil
}
