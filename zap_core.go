package mlog

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/ai-mmo/lumberjack"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// ErrClosed indicates that a stale logger was used after its lifecycle ended.
var ErrClosed = errors.New("mlog: logger closed")

// ZapCore routes one severity (or all enabled severities in single-file mode).
// Configuration is immutable for a core's lifetime. With-derived cores share
// this core's lifecycle and route cache, so they cannot reopen a closed file.
type ZapCore struct {
	zapcore.Core
	syncer              zapcore.WriteSyncer
	level               zapcore.Level
	serviceName         string
	serviceID           uint64
	config              ZapConfig
	levelControl        zap.AtomicLevel
	encoder             zapcore.Encoder
	lumberjackLogger    io.WriteCloser
	specialLoggers      map[string]io.WriteCloser
	routeSyncers        sync.Map // route -> validated sink; same bounded writer registry
	mu                  sync.RWMutex
	specialLoggersMutex sync.Mutex
	closed              bool
}

func NewZapCoreWithService(level zapcore.Level, name string, id uint64) *ZapCore {
	globalMutex.RLock()
	cfg, control := zapConfig, atomicLevel
	globalMutex.RUnlock()
	return newZapCoreWithConfig(level, name, id, cfg, control)
}

func newZapCoreWithConfig(level zapcore.Level, name string, id uint64, cfg ZapConfig, control zap.AtomicLevel) *ZapCore {
	z := &ZapCore{level: level, serviceName: name, serviceID: id, config: cfg,
		levelControl: control, encoder: cfg.Encoder(), specialLoggers: make(map[string]io.WriteCloser)}
	z.syncer = z.WriteSyncer()
	z.Core = zapcore.NewCore(z.encoder, z.syncer, zap.LevelEnablerFunc(z.Enabled))
	return z
}

func (z *ZapCore) getLogFileName() string {
	if z.config.SingleFile {
		if z.config.SingleFileName != "" {
			return z.config.SingleFileName
		}
		return "all.log"
	}
	return z.level.String() + ".log"
}

func (z *ZapCore) WriteSyncer(formats ...string) zapcore.WriteSyncer {
	z.mu.RLock()
	defer z.mu.RUnlock()
	if z.closed {
		return errorSyncer{ErrClosed}
	}
	return z.createWriteSyncer(z.serviceName, z.serviceID, formats...)
}

// validComponent applies portable rules: Linux must also reject Windows
// traversal, drive prefixes, alternate data streams and reserved device names.
func validComponent(s string) bool {
	if s == "" || s == "." || s == ".." || strings.ContainsAny(s, "/\\:\x00*?\"<>|") || strings.TrimRight(s, ". ") != s {
		return false
	}
	for _, r := range s {
		if r < 32 {
			return false
		}
	}
	base := strings.ToUpper(strings.SplitN(s, ".", 2)[0])
	if base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" {
		return false
	}
	if len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && base[3] >= '1' && base[3] <= '9' {
		return false
	}
	return true
}

func validateRoute(route string) error {
	if route == "" {
		return nil
	}
	for _, part := range strings.Split(route, "/") {
		if !validComponent(part) {
			return fmt.Errorf("mlog: invalid route %q", route)
		}
	}
	return nil
}

type errorSyncer struct{ err error }

func (w errorSyncer) Write([]byte) (int, error) { return 0, w.err }
func (w errorSyncer) Sync() error               { return w.err }

func (z *ZapCore) createWriteSyncer(name string, id uint64, formats ...string) zapcore.WriteSyncer {
	dir := z.config.Director
	if id != 0 {
		dir = filepath.Join(dir, fmt.Sprint(id))
	}
	if name != "" {
		if !validComponent(name) {
			return errorSyncer{fmt.Errorf("mlog: invalid service name %q", name)}
		}
		dir = filepath.Join(dir, name)
	}
	route := ""
	if len(formats) > 0 {
		route = formats[0]
	}
	if err := validateRoute(route); err != nil {
		return errorSyncer{err}
	}
	file := z.getLogFileName()
	if !validComponent(file) {
		return errorSyncer{fmt.Errorf("mlog: invalid filename %q", file)}
	}
	dir = filepath.Join(dir, route)
	path := filepath.Join(dir, file)
	z.specialLoggersMutex.Lock()
	defer z.specialLoggersMutex.Unlock()
	writer := z.lumberjackLogger
	if route != "" {
		writer = z.specialLoggers[path]
	}
	if writer == nil {
		if route != "" && z.config.MaxRouteWriters > 0 && len(z.specialLoggers) >= z.config.MaxRouteWriters {
			return errorSyncer{errors.New("mlog: route writer limit reached")}
		}
		// Root and service directories must be controlled by the application.
		// Check existing descendant symlinks; this is defense in depth, not a
		// substitute for filesystem permissions against hostile local processes.
		if err := prepareLogDirectory(z.config.Director, dir); err != nil {
			return errorSyncer{err}
		}
		if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
			return errorSyncer{fmt.Errorf("mlog: symlink logfile %q", path)}
		}
		writer = &lumberjack.Logger{Filename: path, MaxSize: z.config.MaxSize, MaxBackups: z.config.MaxBackups,
			MaxAge: z.config.RetentionDay, Compress: z.config.EnableCompress, LocalTime: true}
		if route != "" {
			z.specialLoggers[path] = writer
		} else {
			z.lumberjackLogger = writer
		}
	}
	if z.config.LogInConsole {
		return zapcore.NewMultiWriteSyncer(zapcore.AddSync(writer), zapcore.Lock(os.Stdout))
	}
	return zapcore.AddSync(writer)
}

func prepareLogDirectory(root, dir string) error {
	rel, err := filepath.Rel(root, dir)
	if err != nil {
		return err
	}
	if !filepath.IsLocal(rel) {
		return fmt.Errorf("mlog: directory outside root")
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return err
	}
	current := root
	if rel != "." {
		for _, part := range strings.Split(rel, string(filepath.Separator)) {
			current = filepath.Join(current, part)
			if info, err := os.Lstat(current); err == nil && info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("mlog: symlink directory %q", current)
			}
			if err := os.MkdirAll(current, 0700); err != nil {
				return err
			}
		}
	}
	return nil
}

func (z *ZapCore) Enabled(level zapcore.Level) bool {
	if z.config.SingleFile {
		return level >= z.level && z.levelControl.Enabled(level)
	}
	return level == z.level && z.levelControl.Enabled(level)
}

// boundCore eagerly encodes structured context, matching Zap's With contract.
// Mutable values need caller synchronization only while With/logging executes.
type boundCore struct {
	root     *ZapCore
	core     zapcore.Core
	encoder  zapcore.Encoder
	route    string
	routeErr error
}

func (z *ZapCore) bind(enc zapcore.Encoder, route string, routeErr error, fields []zapcore.Field) zapcore.Core {
	newRoute, filtered, err := z.routeFields(fields)
	if newRoute != "" {
		route = newRoute
	}
	if err != nil {
		routeErr = err
	}
	owned := enc.Clone()
	for _, f := range filtered {
		f.AddTo(owned)
	}
	return &boundCore{z, zapcore.NewCore(owned, z.syncer, zap.LevelEnablerFunc(z.Enabled)), owned, route, routeErr}
}
func (z *ZapCore) With(fields []zapcore.Field) zapcore.Core {
	return z.bind(z.encoder, "", nil, fields)
}
func (b *boundCore) Enabled(l zapcore.Level) bool { return b.root.Enabled(l) }
func (b *boundCore) With(fields []zapcore.Field) zapcore.Core {
	return b.root.bind(b.encoder, b.route, b.routeErr, fields)
}
func (b *boundCore) Check(e zapcore.Entry, c *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	if b.Enabled(e.Level) {
		return c.AddCore(e, b)
	}
	return c
}
func (b *boundCore) Write(e zapcore.Entry, f []zapcore.Field) error {
	return b.root.writeEncoded(b.core, b.encoder, b.route, b.routeErr, e, f)
}
func (b *boundCore) Sync() error { return b.root.Sync() }
func (z *ZapCore) Check(e zapcore.Entry, c *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	if z.Enabled(e.Level) {
		return c.AddCore(e, z)
	}
	return c
}
func (z *ZapCore) routeFields(fields []zapcore.Field) (string, []zapcore.Field, error) {
	if z.config.SingleFile {
		return "", fields, nil
	}
	// Most records have no routing fields. Reuse the caller's read-only slice
	// in that case; copy only when stripping a routing control field.
	filtered := fields
	copied := false
	route := ""
	for i, f := range fields {
		if f.Key == "business" || f.Key == "folder" || f.Key == "directory" {
			if f.Type != zapcore.StringType {
				return "", nil, errors.New("mlog: route must be a string")
			}
			if !copied {
				filtered = make([]zapcore.Field, 0, len(fields)-1)
				filtered = append(filtered, fields[:i]...)
				copied = true
			}
			route = f.String
		} else if copied {
			filtered = append(filtered, f)
		}
	}
	return route, filtered, nil
}
func (z *ZapCore) Write(e zapcore.Entry, f []zapcore.Field) error {
	return z.writeEncoded(z.Core, z.encoder, "", nil, e, f)
}
func (z *ZapCore) writeEncoded(core zapcore.Core, enc zapcore.Encoder, route string, routeErr error, e zapcore.Entry, fields []zapcore.Field) error {
	z.mu.RLock()
	defer z.mu.RUnlock()
	if z.closed {
		return ErrClosed
	}
	if routeErr != nil {
		return routeErr
	}
	current, filtered, err := z.routeFields(fields)
	if err != nil {
		return err
	}
	if current != "" {
		route = current
	}
	if route != "" {
		return zapcore.NewCore(enc, z.routeWriteSyncer(route), z.level).Write(e, filtered)
	}
	return core.Write(e, filtered)
}

// routeWriteSyncer is called with mu held, so a cache hit cannot resurrect a
// closed generation. Only successful sink construction is cached; validation,
// filesystem failures and capacity failures still go through the original path.
func (z *ZapCore) routeWriteSyncer(route string) zapcore.WriteSyncer {
	// Preserve the existing behavior of resolving os.Stdout on each routed
	// console write. Do not capture a replaced/redirected stdout in this cache.
	if z.config.LogInConsole {
		return z.createWriteSyncer(z.serviceName, z.serviceID, route)
	}
	if cached, ok := z.routeSyncers.Load(route); ok {
		return cached.(zapcore.WriteSyncer)
	}
	writer := z.createWriteSyncer(z.serviceName, z.serviceID, route)
	if _, failed := writer.(errorSyncer); failed {
		return writer
	}
	// createWriteSyncer serializes creation and enforces MaxRouteWriters.
	// Concurrent misses may build wrappers, but never a second file writer.
	actual, _ := z.routeSyncers.LoadOrStore(route, writer)
	return actual.(zapcore.WriteSyncer)
}

func (z *ZapCore) Sync() error {
	z.mu.RLock()
	defer z.mu.RUnlock()
	if z.closed {
		return ErrClosed
	}
	err := z.Core.Sync()
	if isHarmlessSyncError(err) {
		return nil
	}
	return err
}

func (z *ZapCore) Close() error {
	z.mu.Lock()
	defer z.mu.Unlock()
	if z.closed {
		return nil
	}
	z.closed = true
	errs := []error{z.Core.Sync()}
	if isHarmlessSyncError(errs[0]) {
		errs[0] = nil
	}
	z.specialLoggersMutex.Lock()
	defer z.specialLoggersMutex.Unlock()
	if z.lumberjackLogger != nil {
		errs = append(errs, z.lumberjackLogger.Close())
	}
	for _, w := range z.specialLoggers {
		errs = append(errs, w.Close())
	}
	z.specialLoggers = nil
	z.routeSyncers.Clear()
	return errors.Join(errs...)
}
