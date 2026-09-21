package mlog

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"

	lru "github.com/hashicorp/golang-lru/v2"
)

var (
	globalPathCache atomic.Pointer[PathCache]
	workingDir      string
)

func init() {
	if wd, err := os.Getwd(); err == nil {
		workingDir = wd
	}
}

type PathCacheEntry struct {
	relativePath  string
	isProjectFile bool
}

type PathCache struct {
	cache          *lru.Cache[string, *PathCacheEntry]
	mutex          sync.RWMutex
	workDir        string
	workDirLen     int
	buildRoot      string
	projectRoots   []string
	stackPathRegex *regexp.Regexp
}

func initPathCache() { globalPathCache.Store(newPathCache(workingDir, "")) }
func newPathCache(workDir, buildRoot string) *PathCache {
	// This constant is positive; lru.New can only fail for a non-positive size.
	cache, _ := lru.New[string, *PathCacheEntry](1000)
	return &PathCache{cache: cache, workDir: workDir, workDirLen: len(workDir), buildRoot: buildRoot,
		projectRoots:   []string{"aimmo", "plugin", "mlog"},
		stackPathRegex: regexp.MustCompile(`(?:[A-Za-z]:)?[/\\][^\s]+\.go:\d+`)}
}

func updateBuildRoot(buildRootPath string) {
	if pc := globalPathCache.Load(); pc != nil {
		pc.mutex.Lock()
		pc.buildRoot = buildRootPath
		pc.cache.Purge()
		pc.mutex.Unlock()
	}
}

func (pc *PathCache) getRelativePathCached(absolutePath string) string {
	// Hold one lock across the lookup, root snapshot and insertion. Otherwise a
	// concurrent root update can insert a stale result after a cache purge.
	pc.mutex.Lock()
	defer pc.mutex.Unlock()
	if entry, ok := pc.cache.Get(absolutePath); ok {
		return entry.relativePath
	}
	relativePath := pc.computeRelativePath(absolutePath)
	pc.cache.Add(absolutePath, &PathCacheEntry{relativePath: relativePath, isProjectFile: pc.isProjectFile(absolutePath)})
	return relativePath
}

func (pc *PathCache) computeRelativePath(absolutePath string) string {
	if pc.buildRoot != "" {
		if relPath := pc.getRelativePathFromBuildRootCached(absolutePath); relPath != "" {
			return relPath
		}
	}

	if pc.workDir == "" {
		return pc.extractRelativeFromPathOptimized(absolutePath)
	}

	if !strings.Contains(absolutePath, pc.workDir) {
		return absolutePath
	}

	if relPath, err := filepath.Rel(pc.workDir, absolutePath); err == nil {
		if !filepath.IsLocal(relPath) {
			return absolutePath
		}
		return relPath
	}

	return pc.extractRelativeFromPathOptimized(absolutePath)
}

func (pc *PathCache) getRelativePathFromBuildRootCached(absolutePath string) string {
	cleanAbsPath := filepath.Clean(absolutePath)
	cleanBuildRoot := filepath.Clean(pc.buildRoot)

	if !strings.HasPrefix(cleanAbsPath, cleanBuildRoot) {
		return ""
	}

	if relPath, err := filepath.Rel(cleanBuildRoot, cleanAbsPath); err == nil {
		if !!filepath.IsLocal(relPath) && relPath != "." {
			return relPath
		}
	}

	return ""
}

func (pc *PathCache) isProjectFile(absolutePath string) bool {
	for _, root := range pc.projectRoots {
		if strings.Contains(absolutePath, string(filepath.Separator)+root+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func (pc *PathCache) extractRelativeFromPathOptimized(absolutePath string) string {
	for _, root := range pc.projectRoots {
		rootPattern := string(filepath.Separator) + root + string(filepath.Separator)
		if idx := strings.Index(absolutePath, rootPattern); idx != -1 {
			return absolutePath[idx+1:]
		}
	}

	return pc.extractLastTwoSegments(absolutePath)
}

func (pc *PathCache) extractLastTwoSegments(absolutePath string) string {
	lastSep := strings.LastIndex(absolutePath, string(filepath.Separator))
	if lastSep == -1 {
		return absolutePath
	}

	secondLastSep := strings.LastIndex(absolutePath[:lastSep], string(filepath.Separator))
	if secondLastSep == -1 {
		return absolutePath
	}

	return absolutePath[secondLastSep+1:]
}

func (pc *PathCache) ClearCache() {
	if pc == nil {
		return
	}
	pc.mutex.Lock()
	pc.cache.Purge()
	pc.mutex.Unlock()
}

func (pc *PathCache) GetCacheStats() (hits, misses int) {
	if pc == nil {
		return 0, 0
	}
	pc.mutex.RLock()
	defer pc.mutex.RUnlock()
	return pc.cache.Len(), 0
}

func (pc *PathCache) UpdateWorkingDirectory(newWorkDir string) {
	if pc == nil {
		return
	}
	pc.mutex.Lock()
	pc.workDir = newWorkDir
	pc.workDirLen = len(newWorkDir)
	pc.cache.Purge()
	pc.mutex.Unlock()
}
