package mlog

import (
	"fmt"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"path/filepath"
	"sync"
)

var (
	globalMutex sync.RWMutex
	coreMutex   sync.RWMutex
	zapCores    []*ZapCore
	zapLogger   *zap.Logger
)

// initZap is called with globalMutex held, using a copied, validated config.
func initZap(serviceName string, serviceID uint64) (*zap.Logger, error) {
	dir := zapConfig.Director
	if serviceID != 0 {
		dir = filepath.Join(dir, fmt.Sprint(serviceID))
	}
	if serviceName != "" {
		dir = filepath.Join(dir, serviceName)
	}
	if err := prepareLogDirectory(zapConfig.Director, dir); err != nil {
		return nil, err
	}
	coreMutex.Lock()
	levels := zapConfig.Levels()
	if zapConfig.SingleFile {
		levels = []zapcore.Level{zapcore.DebugLevel}
	}
	cores := make([]zapcore.Core, 0, len(levels))
	for _, level := range levels {
		core := newZapCoreWithConfig(level, serviceName, serviceID, zapConfig, atomicLevel)
		zapCores = append(zapCores, core)
		cores = append(cores, core)
	}
	coreMutex.Unlock()
	logger := zap.New(zapcore.NewTee(cores...))
	if zapConfig.ShowLine {
		logger = logger.WithOptions(zap.AddCaller())
	}
	return logger, nil
}
