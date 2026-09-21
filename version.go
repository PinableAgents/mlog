package mlog

// Version 当前版本号
const Version = "0.0.22"

// BuildTime 构建时间，在构建时通过 ldflags 注入
var BuildTime string

// GitCommit Git 提交哈希，在构建时通过 ldflags 注入
var GitCommit string

// GetVersion 返回完整的版本信息
func GetVersion() string {
	return Version
}

// GetBuildInfo 返回构建信息
func GetBuildInfo() map[string]string {
	return map[string]string{
		"version":   Version,
		"buildTime": BuildTime,
		"gitCommit": GitCommit,
	}
}
