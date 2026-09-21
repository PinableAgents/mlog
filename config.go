package mlog

import "sync/atomic"

// LogSafetyMode selects formatting, not a synchronization policy for caller data.
type LogSafetyMode int

const (
	SafetyModeDefault LogSafetyMode = iota // Safe summaries for asynchronous messages.
	SafetyModeAlways                       // Safe summaries for both synchronous and asynchronous messages.
	SafetyModeNever                        // Caller owns synchronization for all fmt arguments.
)

var globalSafetyMode atomic.Int32

func SetLogSafetyMode(mode LogSafetyMode) { globalSafetyMode.Store(int32(mode)) }
func GetLogSafetyMode() LogSafetyMode     { return LogSafetyMode(globalSafetyMode.Load()) }
func shouldUseSafeFormat(isAsync bool) bool {
	switch GetLogSafetyMode() {
	case SafetyModeAlways:
		return true
	case SafetyModeNever:
		return false
	default:
		return isAsync
	}
}
