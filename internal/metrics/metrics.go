// Package metrics provides a minimal, stdlib-only Prometheus metrics
// implementation. It supports Counter, Gauge, and Histogram metric types
// with label-less and label-scoped variants, and exposes them via a
// Registry that writes the Prometheus text exposition format.
package metrics

import (
	"fmt"
	"io"
	"math"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// A Metric renders itself to a writer using the Prometheus text format.
type Metric interface {
	writeTo(w io.Writer)
}

// Registry holds a set of metrics and renders them via [Registry.WriteTo].
// It is safe for concurrent registration and scraping.
type Registry struct {
	mu      sync.Mutex
	metrics []Metric
}

// NewRegistry creates an empty Registry.
func NewRegistry() *Registry {
	return &Registry{}
}

// Register adds a metric to the registry. It returns the metric for
// convenient inline use: `c := reg.Register(NewCounter(...)).(*Counter)`.
func (r *Registry) Register(m Metric) Metric {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.metrics = append(r.metrics, m)
	return m
}

// WriteTo renders every registered metric in Prometheus text format.
// It satisfies [io.WriterTo].
func (r *Registry) WriteTo(w io.Writer) (int64, error) {
	r.mu.Lock()
	snapshot := make([]Metric, len(r.metrics))
	copy(snapshot, r.metrics)
	r.mu.Unlock()

	cw := &countingWriter{w: w}
	for _, m := range snapshot {
		m.writeTo(cw)
	}
	writeRuntimeMetrics(cw)
	return cw.n, cw.err
}

// countingWriter tracks total bytes written and the first error.
type countingWriter struct {
	w   io.Writer
	n   int64
	err error
}

func (c *countingWriter) Write(p []byte) (int, error) {
	if c.err != nil {
		return 0, c.err
	}
	n, err := c.w.Write(p)
	c.n += int64(n)
	if err != nil {
		c.err = err
	}
	return n, err
}

// --- Counter ---

// A Counter is a monotonically increasing uint64 metric.
// Counters may carry at most one label dimension with a small set of
// pre-declared values.
type Counter struct {
	name, help string
	labelName  string
	values     map[string]*atomic.Uint64
}

// NewCounter creates a new counter. If labelName is empty, the counter
// has no labels. Otherwise labelValues enumerates the allowed label values
// (e.g. "ok", "error"), each initialized to zero.
func NewCounter(name, help, labelName string, labelValues ...string) *Counter {
	c := &Counter{
		name:      name,
		help:      help,
		labelName: labelName,
		values:    make(map[string]*atomic.Uint64),
	}
	if labelName == "" {
		c.values[""] = &atomic.Uint64{}
	} else {
		for _, v := range labelValues {
			c.values[v] = &atomic.Uint64{}
		}
	}
	return c
}

// Inc adds 1 to the counter with the given label value. For a label-less
// counter, pass the empty string.
func (c *Counter) Inc(labelValue string) {
	if v, ok := c.values[labelValue]; ok {
		v.Add(1)
	}
}

func (c *Counter) writeTo(w io.Writer) {
	fmt.Fprintf(w, "# HELP %s %s\n", c.name, c.help)
	fmt.Fprintf(w, "# TYPE %s counter\n", c.name)
	if c.labelName == "" {
		fmt.Fprintf(w, "%s %d\n", c.name, c.values[""].Load())
		return
	}
	keys := make([]string, 0, len(c.values))
	for k := range c.values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(w, "%s{%s=%q} %d\n", c.name, c.labelName, k, c.values[k].Load())
	}
}

// --- Gauge ---

// A Gauge is a numeric metric whose value can go up or down.
type Gauge struct {
	name, help string
	val        atomic.Uint64 // float64 bits
}

// NewGauge creates a new label-less gauge.
func NewGauge(name, help string) *Gauge {
	return &Gauge{name: name, help: help}
}

// Set replaces the gauge's current value.
func (g *Gauge) Set(v float64) {
	g.val.Store(math.Float64bits(v))
}

func (g *Gauge) writeTo(w io.Writer) {
	fmt.Fprintf(w, "# HELP %s %s\n", g.name, g.help)
	fmt.Fprintf(w, "# TYPE %s gauge\n", g.name)
	fmt.Fprintf(w, "%s %s\n", g.name, formatFloat(math.Float64frombits(g.val.Load())))
}

// --- Histogram ---

// A Histogram observes samples into a fixed set of upper-bound buckets
// plus a total sum and count, following Prometheus cumulative-bucket
// semantics. All updates are lock-free.
type Histogram struct {
	name, help string
	bounds     []float64
	buckets    []atomic.Uint64 // cumulative bucket counts (length len(bounds)+1)
	sum        atomic.Uint64   // float64 bits, updated via CAS loop
	count      atomic.Uint64
}

// NewHistogram creates a histogram with the given upper bounds (sorted).
// A +Inf bucket is always appended implicitly.
func NewHistogram(name, help string, bounds []float64) *Histogram {
	bs := make([]float64, len(bounds))
	copy(bs, bounds)
	sort.Float64s(bs)
	return &Histogram{
		name:    name,
		help:    help,
		bounds:  bs,
		buckets: make([]atomic.Uint64, len(bs)+1),
	}
}

// Observe records a single sample.
func (h *Histogram) Observe(v float64) {
	// Cumulative bucket counts. Bounds are sorted, so we can break as
	// soon as v exceeds the current upper bound.
	for i, b := range h.bounds {
		if v > b {
			continue
		}
		h.buckets[i].Add(1)
	}
	h.buckets[len(h.bounds)].Add(1) // +Inf
	h.count.Add(1)

	// Lock-free float sum via CAS loop on the uint64 bit pattern.
	for {
		oldBits := h.sum.Load()
		newBits := math.Float64bits(math.Float64frombits(oldBits) + v)
		if h.sum.CompareAndSwap(oldBits, newBits) {
			break
		}
	}
}

func (h *Histogram) writeTo(w io.Writer) {
	fmt.Fprintf(w, "# HELP %s %s\n", h.name, h.help)
	fmt.Fprintf(w, "# TYPE %s histogram\n", h.name)
	for i, b := range h.bounds {
		fmt.Fprintf(w, "%s_bucket{le=%q} %d\n", h.name, formatFloat(b), h.buckets[i].Load())
	}
	fmt.Fprintf(w, "%s_bucket{le=\"+Inf\"} %d\n", h.name, h.buckets[len(h.bounds)].Load())
	fmt.Fprintf(w, "%s_sum %s\n", h.name, formatFloat(math.Float64frombits(h.sum.Load())))
	fmt.Fprintf(w, "%s_count %d\n", h.name, h.count.Load())
}

// --- Helpers ---

// formatFloat prints a float64 with the minimum precision needed to
// round-trip, matching how Prometheus libraries commonly emit values.
func formatFloat(v float64) string {
	if math.IsInf(v, 1) {
		return "+Inf"
	}
	if math.IsInf(v, -1) {
		return "-Inf"
	}
	if math.IsNaN(v) {
		return "NaN"
	}
	return strings.TrimRight(strings.TrimRight(strconv.FormatFloat(v, 'f', 6, 64), "0"), ".")
}

// writeRuntimeMetrics emits a handful of Go runtime metrics. Enough for
// basic ops visibility without pulling in a full metrics library.
func writeRuntimeMetrics(w io.Writer) {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	fmt.Fprintf(w, "# HELP go_goroutines Number of goroutines that currently exist.\n")
	fmt.Fprintf(w, "# TYPE go_goroutines gauge\n")
	fmt.Fprintf(w, "go_goroutines %d\n", runtime.NumGoroutine())

	fmt.Fprintf(w, "# HELP go_memstats_heap_alloc_bytes Heap bytes allocated and still in use.\n")
	fmt.Fprintf(w, "# TYPE go_memstats_heap_alloc_bytes gauge\n")
	fmt.Fprintf(w, "go_memstats_heap_alloc_bytes %d\n", m.HeapAlloc)

	fmt.Fprintf(w, "# HELP go_memstats_sys_bytes Total bytes obtained from the OS.\n")
	fmt.Fprintf(w, "# TYPE go_memstats_sys_bytes gauge\n")
	fmt.Fprintf(w, "go_memstats_sys_bytes %d\n", m.Sys)

	fmt.Fprintf(w, "# HELP go_gc_duration_seconds_total Total GC pause time in seconds.\n")
	fmt.Fprintf(w, "# TYPE go_gc_duration_seconds_total counter\n")
	fmt.Fprintf(w, "go_gc_duration_seconds_total %s\n", formatFloat(time.Duration(m.PauseTotalNs).Seconds()))
}
