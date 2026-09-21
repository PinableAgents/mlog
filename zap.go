package mlog

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var levelCacheMutex sync.RWMutex

var levelCache = map[string]zapcore.Level{
	"debug":  zapcore.DebugLevel,
	"info":   zapcore.InfoLevel,
	"warn":   zapcore.WarnLevel,
	"error":  zapcore.ErrorLevel,
	"dpanic": zapcore.DPanicLevel,
	"panic":  zapcore.PanicLevel,
	"fatal":  zapcore.FatalLevel,
}

func formatMessage(msg string, args []any, isAsync bool) string {
	if len(args) == 0 {
		return msg
	}

	if shouldUseSafeFormat(isAsync) {
		return SafeFormat(msg, args...)
	}

	if strings.Contains(msg, "%") {
		// Do not copy fmt's completed string into a second builder buffer.
		return formatPrintf(msg, args)
	}
	var sb strings.Builder
	formatToStringBuilder(&sb, msg, args...)
	return sb.String()
}

func zapUpdateLevel(logLevel string) {
	level, err := zapcore.ParseLevel(logLevel)
	if err != nil {
		level = zapcore.InfoLevel
		fmt.Fprintf(os.Stderr, "[mlog] 日志级别解析失败: %s, 使用默认 info 级别\n", logLevel)
		return
	}

	zapConfig.Level = logLevel

	atomicLevel.SetLevel(level)

	levelCacheMutex.Lock()
	levelCache[logLevel] = level
	levelCacheMutex.Unlock()

	if atomicLevel.Level() <= zapcore.DebugLevel {
		logger, ok := getLogger()
		if ok {
			loggerWithSkip := withCachedCallerSkip(logger, 2)
			loggerWithSkip.Debug("日志级别已更新",
				zap.String("level", logLevel),
				zap.Stringer("parsed_level", level))
		}
	}
}

func zapCheckLevel(logLevel string) bool {
	levelCacheMutex.RLock()
	checkLevel, ok := levelCache[logLevel]
	levelCacheMutex.RUnlock()

	if !ok {
		parsedLevel, err := zapcore.ParseLevel(logLevel)
		if err != nil {
			return false
		}
		checkLevel = parsedLevel
		levelCacheMutex.Lock()
		levelCache[logLevel] = checkLevel
		levelCacheMutex.Unlock()
	}

	currentLevel := atomicLevel.Level()
	return currentLevel <= checkLevel
}

func zapDebug(msg string, args ...any) {
	if al, enabled := getAsyncLogger(); enabled {
		al.logAsyncWithSkip(zapcore.DebugLevel, msg, args, 3)
	} else {
		logger, ok := getLogger()
		if !ok {
			return
		}

		loggerWithSkip := withCachedCallerSkip(logger, 2)

		formattedMsg := formatMessage(msg, args, false)
		loggerWithSkip.Debug(formattedMsg)
	}
}

func zapInfo(arg0 string, args ...any) {
	if al, enabled := getAsyncLogger(); enabled {
		al.logAsyncWithSkip(zapcore.InfoLevel, arg0, args, 3)
	} else {
		logger, ok := getLogger()
		if !ok {
			return
		}

		loggerWithSkip := withCachedCallerSkip(logger, 2)

		formattedMsg := formatMessage(arg0, args, false)
		loggerWithSkip.Info(formattedMsg)
	}
}

func zapWarn(arg0 string, args ...any) {
	if al, enabled := getAsyncLogger(); enabled {
		al.logAsyncWithSkip(zapcore.WarnLevel, arg0, args, 3)
	} else {
		logger, ok := getLogger()
		if !ok {
			return
		}

		loggerWithSkip := withCachedCallerSkip(logger, 2)

		formattedMsg := formatMessage(arg0, args, false)
		loggerWithSkip.Warn(formattedMsg)
	}
}

func zapError(arg0 string, args ...any) {
	if al, enabled := getAsyncLogger(); enabled {
		al.logAsyncWithSkip(zapcore.ErrorLevel, arg0, args, 3)
	} else {
		logger, ok := getLogger()
		if !ok {
			return
		}

		loggerWithSkip := withCachedCallerSkip(logger, 2)

		formattedMsg := formatMessage(arg0, args, false)
		loggerWithSkip.Error(formattedMsg)
	}
}

func zapReturnError(arg0 string, args ...any) error {
	zapError(arg0, args...)

	if len(args) == 0 {
		return errors.New(arg0)
	}

	var sb strings.Builder

	formatToStringBuilder(&sb, arg0, args...)

	return errors.New(sb.String())
}

func formatToStringBuilder(sb *strings.Builder, format string, args ...any) {
	if !strings.Contains(format, "%") {
		sb.WriteString(format)
		for _, arg := range args {
			sb.WriteByte(' ')
			sb.WriteString(fmt.Sprint(arg))
		}
		return
	}

	sb.WriteString(formatPrintf(format, args))
}

// formatPrintf retains the legacy single-argument fast cases and fmt's exact
// handling of mismatched verbs/arguments. SafetyMode still selects SafeFormat
// before reaching this helper.
func formatPrintf(format string, args []any) string {
	if len(args) == 1 {
		switch format {
		case "%s":
			if s, ok := args[0].(string); ok {
				return s
			}
		case "%d":
			if i, ok := args[0].(int); ok {
				return strconv.Itoa(i)
			}
			if i, ok := args[0].(int64); ok {
				return strconv.FormatInt(i, 10)
			}
		case "%v":
			return fmt.Sprint(args[0])
		}
	}
	return fmt.Sprintf(format, args...)
}
