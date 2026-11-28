// Package observability provides structured, redaction-aware operational and security logging plus
// a small dependency-free metrics registry, shared by the Connection Broker and the Host Bridge.
//
// Design constraints:
//   - Logs are JSON lines so multiple processes can be aggregated without a parser per service.
//   - Security events are distinguishable from operational events by the "kind" field.
//   - Secrets never reach the log stream: field names and values are filtered before encoding.
//   - Logging must never fail a security operation: write errors are ignored and nothing panics.
package observability

import (
	"encoding/json"
	"io"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Kind separates security-relevant events from ordinary operational telemetry.
type Kind string

const (
	KindSecurity    Kind = "security"
	KindOperational Kind = "operational"
)

// Severity levels used in the "level" field.
const (
	LevelDebug = "debug"
	LevelInfo  = "info"
	LevelWarn  = "warn"
	LevelError = "error"
)

// RedactedValue replaces any value considered sensitive.
const RedactedValue = "[REDACTED]"

// maxValueLength bounds non-sensitive string values to avoid logging file contents or AI output.
const maxValueLength = 256

// sensitiveFieldNames are matched case-insensitively against field keys. A field with one of these
// names is always redacted, regardless of its value.
var sensitiveFieldNames = map[string]bool{
	"password": true, "passwd": true, "secret": true, "session_secret": true,
	"master_secret": true, "pairing_secret": true, "token": true, "session_token": true,
	"access_token": true, "refresh_token": true, "authorization": true, "api_key": true,
	"apikey": true, "private_key": true, "credential": true, "credentials": true,
	"cookie": true, "set-cookie": true, "keystore_password": true, "store_password": true,
	"key_password": true, "github_token": true, "bearer": true,
}

// sensitiveValuePatterns catch secrets that arrive under innocuous field names.
var sensitiveValuePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)^bearer\s+\S+$`),
	regexp.MustCompile(`^[0-9a-fA-F]{32,}$`),          // long hex (keys, digests, session secrets)
	regexp.MustCompile(`^[A-Za-z0-9+/]{44,}={0,2}$`), // base64 blobs
}

// Logger writes structured JSON log lines. A nil *Logger is valid and discards all events, so
// callers never need nil checks around best-effort logging.
type Logger struct {
	mu       sync.Mutex
	out      io.Writer
	service  string
	version  string
	env      string
	minLevel string
	now      func() time.Time
}

// NewLogger creates a JSON logger writing to out (os.Stderr when out is nil).
func NewLogger(out io.Writer, service, version, environment, minLevel string) *Logger {
	if out == nil {
		out = os.Stderr
	}
	if strings.TrimSpace(minLevel) == "" {
		minLevel = LevelInfo
	}
	return &Logger{
		out:      out,
		service:  service,
		version:  version,
		env:      environment,
		minLevel: strings.ToLower(minLevel),
		now:      func() time.Time { return time.Now().UTC() },
	}
}

func levelRank(level string) int {
	switch strings.ToLower(level) {
	case LevelDebug:
		return 0
	case LevelInfo:
		return 1
	case LevelWarn:
		return 2
	case LevelError:
		return 3
	default:
		return 1
	}
}

// SecurityEvent logs a security-relevant event.
func (l *Logger) SecurityEvent(level, event string, fields map[string]any) {
	l.log(level, event, KindSecurity, fields)
}

// OperationalEvent logs an operational event.
func (l *Logger) OperationalEvent(level, event string, fields map[string]any) {
	l.log(level, event, KindOperational, fields)
}

func (l *Logger) log(level, event string, kind Kind, fields map[string]any) {
	if l == nil {
		return
	}
	level = strings.ToLower(level)
	if levelRank(level) < levelRank(l.minLevel) {
		return
	}

	entry := make(map[string]any, len(fields)+6)
	entry["ts"] = l.now().Format(time.RFC3339Nano)
	entry["level"] = level
	entry["kind"] = string(kind)
	entry["service"] = l.service
	if l.version != "" {
		entry["version"] = l.version
	}
	if l.env != "" {
		entry["env"] = l.env
	}
	entry["event"] = event

	for key, value := range fields {
		if sanitized, keep := SanitizeField(key, value); keep {
			entry[key] = sanitized
		}
	}

	line, err := json.Marshal(entry)
	if err != nil {
		// Never fail a security operation because logging failed.
		return
	}
	line = append(line, '\n')

	l.mu.Lock()
	defer l.mu.Unlock()
	_, _ = l.out.Write(line)
}

// SanitizeField returns the safe representation of a single log field and whether it should be
// emitted at all. Sensitive names are replaced by a fixed marker; over-long strings are truncated.
func SanitizeField(key string, value any) (any, bool) {
	normalized := strings.ToLower(strings.TrimSpace(key))
	if sensitiveFieldNames[normalized] {
		return RedactedValue, true
	}
	if strings.Contains(normalized, "secret") || strings.Contains(normalized, "password") ||
		strings.HasSuffix(normalized, "token") || strings.Contains(normalized, "api_key") {
		return RedactedValue, true
	}

	switch typed := value.(type) {
	case nil:
		return nil, true
	case string:
		return sanitizeString(typed), true
	case bool, int, int32, int64, uint, uint32, uint64, float32, float64:
		return typed, true
	case error:
		return sanitizeString(typed.Error()), true
	default:
		// Composite values are summarised by type only: they may contain user content.
		return "[composite]", true
	}
}

func sanitizeString(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	for _, pattern := range sensitiveValuePatterns {
		if pattern.MatchString(trimmed) {
			return RedactedValue
		}
	}
	if len(value) > maxValueLength {
		return value[:maxValueLength] + "…[truncated]"
	}
	return value
}
