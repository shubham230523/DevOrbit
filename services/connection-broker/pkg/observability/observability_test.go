package observability

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

func decodeSingleEntry(t *testing.T, raw string) map[string]any {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(raw), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected exactly one log line, got %d: %q", len(lines), raw)
	}
	var entry map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &entry); err != nil {
		t.Fatalf("log line is not valid JSON: %v (%q)", err, lines[0])
	}
	return entry
}

func TestLogger_EmitsStructuredSecurityAndOperationalEvents(t *testing.T) {
	buf := &bytes.Buffer{}
	logger := NewLogger(buf, "host-bridge", "1.2.1", "test", LevelInfo)

	logger.SecurityEvent(LevelWarn, "pairing_failed", map[string]any{
		"reason": "invalid_proof", "client_ip": "127.0.0.1", "request_id": "req-1",
	})
	entry := decodeSingleEntry(t, buf.String())
	if entry["kind"] != "security" || entry["level"] != "warn" || entry["event"] != "pairing_failed" {
		t.Fatalf("unexpected security event: %v", entry)
	}
	if entry["service"] != "host-bridge" || entry["version"] != "1.2.1" || entry["env"] != "test" {
		t.Fatalf("missing service metadata: %v", entry)
	}
	if entry["reason"] != "invalid_proof" {
		t.Fatalf("expected reason preserved, got %v", entry["reason"])
	}

	buf.Reset()
	logger.OperationalEvent(LevelInfo, "startup", map[string]any{"port": 8082})
	entry = decodeSingleEntry(t, buf.String())
	if entry["kind"] != "operational" {
		t.Fatalf("expected kind=operational, got %v", entry["kind"])
	}
}

func TestLogger_RedactsSensitiveFieldsAndValues(t *testing.T) {
	buf := &bytes.Buffer{}
	logger := NewLogger(buf, "host-bridge", "1.2.1", "test", LevelInfo)

	logger.SecurityEvent(LevelInfo, "test_event", map[string]any{
		"token":            "abc123",
		"session_secret":   "deadbeef",
		"authorization":    "Bearer abc.def",
		"github_token":     "ghp_example",
		"api_key":          "key-value",
		"reason":           "Bearer eyJhbGciOi.payload.sig",
		"master_secret":    "0123456789abcdef0123456789abcdef",
		"payload_bearer":   "Bearer something",
		"session_id":       "sess-123",
		"duration_ms":      42,
		"nested_structure": map[string]string{"file": "secret contents"},
	})

	entry := decodeSingleEntry(t, buf.String())
	for _, key := range []string{"token", "session_secret", "authorization", "github_token", "api_key", "master_secret"} {
		if entry[key] != RedactedValue {
			t.Fatalf("expected %s to be redacted, got %v", key, entry[key])
		}
	}
	if entry["reason"] != RedactedValue || entry["payload_bearer"] != RedactedValue {
		t.Fatalf("expected bearer-shaped values to be redacted, got %v / %v", entry["reason"], entry["payload_bearer"])
	}
	if entry["session_id"] != "sess-123" {
		t.Fatalf("expected session_id preserved, got %v", entry["session_id"])
	}
	if entry["duration_ms"] != float64(42) {
		t.Fatalf("expected numeric field preserved, got %v", entry["duration_ms"])
	}
	if entry["nested_structure"] != "[composite]" {
		t.Fatalf("expected composite field summary, got %v", entry["nested_structure"])
	}
}

func TestLogger_TruncatesLongValuesAndRespectsLevel(t *testing.T) {
	buf := &bytes.Buffer{}
	logger := NewLogger(buf, "host-bridge", "1.2.1", "test", LevelWarn)

	logger.OperationalEvent(LevelDebug, "debug_event", nil)
	logger.OperationalEvent(LevelInfo, "info_event", nil)
	if buf.Len() != 0 {
		t.Fatalf("expected debug/info suppressed at warn level, got %q", buf.String())
	}

	longValue := strings.Repeat("verbose detail ", 40)
	logger.OperationalEvent(LevelError, "long_field", map[string]any{"detail": longValue})
	entry := decodeSingleEntry(t, buf.String())
	detail, _ := entry["detail"].(string)
	if !strings.HasSuffix(detail, "…[truncated]") {
		t.Fatalf("expected truncation marker, got %q", detail)
	}
	if len(detail) > maxValueLength+len("…[truncated]") {
		t.Fatalf("expected bounded value, got length %d", len(detail))
	}
}

func TestLogger_NilLoggerIsSafe(t *testing.T) {
	var logger *Logger
	logger.SecurityEvent(LevelInfo, "noop", nil)
	logger.OperationalEvent(LevelError, "noop", nil)
}

func TestLogger_ConcurrentWritesProduceValidJSON(t *testing.T) {
	buf := &syncBuffer{}
	logger := NewLogger(buf, "host-bridge", "1.2.1", "test", LevelInfo)

	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				logger.OperationalEvent(LevelInfo, "concurrent_event", map[string]any{"worker": worker, "iteration": i})
			}
		}(w)
	}
	wg.Wait()

	scanner := bufio.NewScanner(strings.NewReader(buf.String()))
	lines := 0
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("concurrent log line is not valid JSON: %v (%q)", err, line)
		}
		lines++
	}
	if lines != 8*50 {
		t.Fatalf("expected %d log lines, got %d", 8*50, lines)
	}
}

func TestMetrics_CountersGaugesAndExposition(t *testing.T) {
	m := NewMetrics()
	m.Inc("devorbit_pairing_failures_total")
	m.Inc("devorbit_pairing_failures_total")
	m.Add("devorbit_bytes_relayed_total", 10)
	m.SetGaugeFunc("devorbit_active_sessions", func() float64 { return 3 })

	if got := m.CounterValue("devorbit_pairing_failures_total"); got != 2 {
		t.Fatalf("expected counter 2, got %v", got)
	}

	var buf bytes.Buffer
	writer := bufio.NewWriter(&buf)
	if err := m.WritePrometheus(writer); err != nil {
		t.Fatalf("WritePrometheus failed: %v", err)
	}
	_ = writer.Flush()
	output := buf.String()
	for _, expected := range []string{
		"# TYPE devorbit_pairing_failures_total counter",
		"devorbit_pairing_failures_total 2",
		"# TYPE devorbit_active_sessions gauge",
		"devorbit_active_sessions 3",
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("expected exposition to contain %q, got:\n%s", expected, output)
		}
	}
}

func TestMetrics_RejectsInvalidAndHighCardinalityNames(t *testing.T) {
	m := NewMetrics()
	for _, name := range []string{
		"", "Invalid-Name", "9starts_with_digit", "has space",
		"sess-123-topic", "devorbit_user_123_sessions", "devorbit_path_/tmp/repo",
	} {
		m.Inc(name)
	}
	if m.RejectedNameCount() == 0 {
		t.Fatalf("expected invalid metric names to be rejected")
	}
	if m.CounterValue("Invalid-Name") != 0 {
		t.Fatalf("invalid metric name must not be recorded")
	}
	m.Inc("devorbit_valid_counter_total")
	if m.CounterValue("devorbit_valid_counter_total") != 1 {
		t.Fatalf("expected valid metric recorded")
	}
}

func TestMetrics_NilRegistryIsSafe(t *testing.T) {
	var m *Metrics
	m.Inc("devorbit_anything_total")
	m.Add("devorbit_anything_total", 1)
	m.SetGaugeFunc("devorbit_gauge", func() float64 { return 1 })
	if m.CounterValue("x") != 0 || m.RejectedNameCount() != 0 {
		t.Fatalf("nil metrics must be inert")
	}
	var buf bytes.Buffer
	writer := bufio.NewWriter(&buf)
	if err := m.WritePrometheus(writer); err != nil {
		t.Fatalf("nil metrics WritePrometheus must not fail: %v", err)
	}
}

func TestMetricsHandler_ServesExpositionAndRejectsWrites(t *testing.T) {
	m := NewMetrics()
	m.Inc("devorbit_pairing_success_total")

	server := httptest.NewServer(m.Handler())
	defer server.Close()

	resp, err := http.Get(server.URL)
	if err != nil {
		t.Fatalf("GET /metrics failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/plain") {
		t.Fatalf("unexpected content type: %q", ct)
	}

	postReq, _ := http.NewRequest(http.MethodPost, server.URL, nil)
	postResp, err := http.DefaultClient.Do(postReq)
	if err != nil {
		t.Fatalf("POST /metrics failed: %v", err)
	}
	defer postResp.Body.Close()
	if postResp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 for POST, got %d", postResp.StatusCode)
	}
}
