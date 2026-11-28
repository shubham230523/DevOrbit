package observability

import (
	"bufio"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"sync"
)

// metricNamePattern restricts metric names to low-cardinality, Prometheus-compatible identifiers.
// Labels are deliberately NOT supported: values such as device ids, session ids, paths or user
// content must never become labels, and a fixed label set keeps cardinality bounded.
var metricNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{2,62}$`)

// Metrics is a minimal, dependency-free counter/gauge registry.
//
// It intentionally avoids a third-party client so both services keep zero extra dependencies while
// still exposing Prometheus-compatible text exposition.
type Metrics struct {
	mu       sync.RWMutex
	counters map[string]float64
	gauges   map[string]func() float64
	rejected uint64
}

// NewMetrics creates an empty registry.
func NewMetrics() *Metrics {
	return &Metrics{
		counters: make(map[string]float64),
		gauges:   make(map[string]func() float64),
	}
}

// Inc increments a counter by one. Invalid names are rejected and counted, never panicking.
func (m *Metrics) Inc(name string) {
	m.Add(name, 1)
}

// Add increments a counter by delta.
func (m *Metrics) Add(name string, delta float64) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !metricNamePattern.MatchString(name) {
		m.rejected++
		return
	}
	m.counters[name] += delta
}

// SetGaugeFunc registers a callback-backed gauge so live values (active connections, uptime) are
// evaluated at scrape time instead of being pushed.
func (m *Metrics) SetGaugeFunc(name string, fn func() float64) {
	if m == nil || fn == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !metricNamePattern.MatchString(name) {
		m.rejected++
		return
	}
	m.gauges[name] = fn
}

// CounterValue returns the current value of a counter (used by tests and diagnostics).
func (m *Metrics) CounterValue(name string) float64 {
	if m == nil {
		return 0
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.counters[name]
}

// RejectedNameCount reports how many metric operations were rejected for an invalid name.
func (m *Metrics) RejectedNameCount() uint64 {
	if m == nil {
		return 0
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.rejected
}

// WritePrometheus renders the registry in Prometheus text exposition format (version 0.0.4).
func (m *Metrics) WritePrometheus(w *bufio.Writer) error {
	if m == nil {
		return nil
	}
	m.mu.RLock()
	counterNames := make([]string, 0, len(m.counters))
	for name := range m.counters {
		counterNames = append(counterNames, name)
	}
	gaugeNames := make([]string, 0, len(m.gauges))
	for name := range m.gauges {
		gaugeNames = append(gaugeNames, name)
	}
	rejected := m.rejected
	m.mu.RUnlock()

	sort.Strings(counterNames)
	sort.Strings(gaugeNames)

	for _, name := range counterNames {
		m.mu.RLock()
		value := m.counters[name]
		m.mu.RUnlock()
		if _, err := fmt.Fprintf(w, "# TYPE %s counter\n%s %g\n", name, name, value); err != nil {
			return err
		}
	}
	for _, name := range gaugeNames {
		m.mu.RLock()
		fn := m.gauges[name]
		m.mu.RUnlock()
		if fn == nil {
			continue
		}
		if _, err := fmt.Fprintf(w, "# TYPE %s gauge\n%s %g\n", name, name, fn()); err != nil {
			return err
		}
	}
	if rejected > 0 {
		if _, err := fmt.Fprintf(w, "# TYPE devorbit_metrics_rejected_total counter\ndevorbit_metrics_rejected_total %d\n", rejected); err != nil {
			return err
		}
	}
	return nil
}

// Handler returns an HTTP handler exposing the registry. Callers MUST mount it behind
// authentication (host-admin / broker admin); it is intentionally not public.
func (m *Metrics) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		buffered := bufio.NewWriter(w)
		if err := m.WritePrometheus(buffered); err != nil {
			_ = buffered.Flush()
			return
		}
		_ = buffered.Flush()
	})
}
