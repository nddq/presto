package metrics

import (
	"bytes"
	"strings"
	"sync"
	"testing"
)

func TestCounterLabelless(t *testing.T) {
	c := NewCounter("test_total", "help text", "")
	c.Inc("")
	c.Inc("")
	c.Inc("")

	var buf bytes.Buffer
	c.writeTo(&buf)
	out := buf.String()
	if !strings.Contains(out, "# TYPE test_total counter") {
		t.Errorf("missing TYPE line: %s", out)
	}
	if !strings.Contains(out, "test_total 3") {
		t.Errorf("expected 'test_total 3', got: %s", out)
	}
}

func TestCounterWithLabels(t *testing.T) {
	c := NewCounter("requests_total", "request count", "status", "ok", "error")
	c.Inc("ok")
	c.Inc("ok")
	c.Inc("error")
	c.Inc("unknown") // should be silently dropped (not registered)

	var buf bytes.Buffer
	c.writeTo(&buf)
	out := buf.String()

	if !strings.Contains(out, `requests_total{status="ok"} 2`) {
		t.Errorf("missing ok label: %s", out)
	}
	if !strings.Contains(out, `requests_total{status="error"} 1`) {
		t.Errorf("missing error label: %s", out)
	}
	if strings.Contains(out, "unknown") {
		t.Errorf("unknown label should not appear: %s", out)
	}
}

func TestGaugeSet(t *testing.T) {
	g := NewGauge("songs", "library size")
	g.Set(42)

	var buf bytes.Buffer
	g.writeTo(&buf)
	out := buf.String()
	if !strings.Contains(out, "songs 42") {
		t.Errorf("expected 'songs 42', got: %s", out)
	}

	g.Set(0)
	buf.Reset()
	g.writeTo(&buf)
	if !strings.Contains(buf.String(), "songs 0") {
		t.Errorf("expected 'songs 0' after set, got: %s", buf.String())
	}
}

func TestHistogramObserve(t *testing.T) {
	h := NewHistogram("latency_seconds", "latency", []float64{0.1, 0.5, 1.0})
	h.Observe(0.05)
	h.Observe(0.3)
	h.Observe(0.7)
	h.Observe(2.0)

	var buf bytes.Buffer
	h.writeTo(&buf)
	out := buf.String()

	// Cumulative: 0.05 → bucket 0 (+1, +1, +1)
	//             0.3  → bucket 1 (+1, +1)
	//             0.7  → bucket 2 (+1)
	//             2.0  → only +Inf (+1)
	// So: le=0.1 → 1, le=0.5 → 2, le=1 → 3, le=+Inf → 4
	cases := []struct {
		needle string
	}{
		{`latency_seconds_bucket{le="0.1"} 1`},
		{`latency_seconds_bucket{le="0.5"} 2`},
		{`latency_seconds_bucket{le="1"} 3`},
		{`latency_seconds_bucket{le="+Inf"} 4`},
		{`latency_seconds_count 4`},
	}
	for _, tc := range cases {
		if !strings.Contains(out, tc.needle) {
			t.Errorf("missing %q in:\n%s", tc.needle, out)
		}
	}

	// sum should be 0.05 + 0.3 + 0.7 + 2.0 = 3.05
	if !strings.Contains(out, "latency_seconds_sum 3.05") {
		t.Errorf("expected sum 3.05: %s", out)
	}
}

func TestCounterConcurrentSafe(t *testing.T) {
	c := NewCounter("concurrent_total", "", "")
	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 1000 {
				c.Inc("")
			}
		}()
	}
	wg.Wait()

	var buf bytes.Buffer
	c.writeTo(&buf)
	if !strings.Contains(buf.String(), "concurrent_total 10000") {
		t.Errorf("expected 10000 after concurrent increment, got: %s", buf.String())
	}
}

func TestHistogramConcurrentSafe(t *testing.T) {
	h := NewHistogram("test_seconds", "", []float64{1, 2, 5})
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 500 {
				h.Observe(float64(i%10) * 0.3)
			}
		}()
	}
	wg.Wait()

	var buf bytes.Buffer
	h.writeTo(&buf)
	if !strings.Contains(buf.String(), "test_seconds_count 4000") {
		t.Errorf("expected count 4000, got: %s", buf.String())
	}
}

func TestRegistryWriteTo(t *testing.T) {
	reg := NewRegistry()
	c := reg.Register(NewCounter("reg_test_total", "test", "")).(*Counter)
	c.Inc("")

	var buf bytes.Buffer
	reg.WriteTo(&buf)
	out := buf.String()

	if !strings.Contains(out, "reg_test_total 1") {
		t.Errorf("registry should render registered counter: %s", out)
	}
	// Runtime metrics should be appended
	if !strings.Contains(out, "go_goroutines") {
		t.Errorf("runtime metrics should be appended: %s", out)
	}
}

func TestFormatFloat(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{0, "0"},
		{1, "1"},
		{0.5, "0.5"},
		{3.14, "3.14"},
		{100, "100"},
	}
	for _, tc := range cases {
		if got := formatFloat(tc.in); got != tc.want {
			t.Errorf("formatFloat(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
