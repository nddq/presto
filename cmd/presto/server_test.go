package main

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nddq/presto/internal/audio"
	"github.com/nddq/presto/internal/fingerprint"
	"github.com/nddq/presto/internal/store"
)

const (
	testSampleRate = 44100
	testWinSize    = 1024
	testHopSize    = 512
	testWindow     = "hann"
)

func genRich(fundamental, durationSec float64) []float64 {
	n := int(durationSec * float64(testSampleRate))
	out := make([]float64, n)
	harmonics := []struct{ mult, amp float64 }{
		{1.0, 0.5}, {2.0, 0.25}, {3.0, 0.12}, {4.0, 0.06}, {5.0, 0.03},
	}
	for i := range out {
		t := float64(i) / float64(testSampleRate)
		env := 1.0
		if t < 0.05 {
			env = t / 0.05
		} else if t > durationSec-0.05 {
			env = (durationSec - t) / 0.05
		}
		freqMod := 1.0 + 0.002*math.Sin(2*math.Pi*5.0*t)
		for _, h := range harmonics {
			out[i] += env * h.amp * math.Sin(2*math.Pi*fundamental*h.mult*freqMod*t)
		}
	}
	return out
}

// buildTestStore creates an in-memory store with synthetic songs.
func buildTestStore(t *testing.T, songs map[string]float64) *store.Store {
	t.Helper()
	s := store.New(testWinSize, testHopSize, testWindow, "constellation")
	for name, fund := range songs {
		samples := genRich(fund, 5.0)
		// Encode to WAV bytes, then decode to Signal, then fingerprint
		wavBytes := audio.EncodeWAV(samples, testSampleRate, 1)
		sig, err := audio.DecodeWAV(wavBytes)
		if err != nil {
			t.Fatal(err)
		}
		fp, err := fingerprint.FingerprintSignal(sig, testWinSize, testHopSize, testWindow, 0)
		if err != nil {
			t.Fatal(err)
		}
		s.Add(name, fp)
	}
	s.Build()
	return s
}

func newTestServer(t *testing.T, st *store.Store) *server {
	t.Helper()
	srv := newServer(defaultMaxUploadBytes, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if st != nil {
		srv.store.Store(st)
		srv.storeSongs.Set(float64(len(st.Songs)))
	}
	return srv
}

func TestHealthz(t *testing.T) {
	srv := newTestServer(t, nil)
	req := httptest.NewRequest("GET", "/healthz", nil)
	rec := httptest.NewRecorder()
	srv.handleHealth(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}

func TestReadyz(t *testing.T) {
	// Not loaded: 503
	srv := newTestServer(t, nil)
	rec := httptest.NewRecorder()
	srv.handleReady(rec, httptest.NewRequest("GET", "/readyz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 before load, got %d", rec.Code)
	}

	// Loaded: 200
	st := buildTestStore(t, map[string]float64{"a.wav": 440})
	srv2 := newTestServer(t, st)
	rec2 := httptest.NewRecorder()
	srv2.handleReady(rec2, httptest.NewRequest("GET", "/readyz", nil))
	if rec2.Code != http.StatusOK {
		t.Errorf("expected 200 after load, got %d", rec2.Code)
	}
}

func TestMatchEndpoint(t *testing.T) {
	st := buildTestStore(t, map[string]float64{
		"song_a.wav": 440,
		"song_b.wav": 523.25,
		"song_c.wav": 329.63,
	})
	srv := newTestServer(t, st)

	// Build a clip from song_a (seconds 2-3 of a rich 440 Hz signal)
	src := genRich(440, 5.0)
	clipWAV := audio.EncodeWAV(src[2*testSampleRate:3*testSampleRate], testSampleRate, 1)

	req := httptest.NewRequest("POST", "/v1/match", bytes.NewReader(clipWAV))
	rec := httptest.NewRecorder()
	srv.handleMatch(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp matchResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Matches) == 0 {
		t.Fatal("expected at least one match")
	}
	if resp.Matches[0].Name != "song_a.wav" {
		t.Errorf("expected top match song_a.wav, got %s (score=%.4f)",
			resp.Matches[0].Name, resp.Matches[0].Score)
	}
	if len(resp.Matches) >= 2 && resp.Matches[1].Score > 0 {
		want := resp.Matches[0].Score / resp.Matches[1].Score
		if resp.Margin != want {
			t.Errorf("margin mismatch: got %.4f, want %.4f (top=%.4f runner-up=%.4f)",
				resp.Margin, want, resp.Matches[0].Score, resp.Matches[1].Score)
		}
		if resp.Margin <= 1 {
			t.Errorf("margin should be >1 for a decisive match, got %.4f", resp.Margin)
		}
	}
}

func TestMatchMalformedWAV(t *testing.T) {
	st := buildTestStore(t, map[string]float64{"a.wav": 440})
	srv := newTestServer(t, st)

	req := httptest.NewRequest("POST", "/v1/match", strings.NewReader("this is not a wav"))
	rec := httptest.NewRecorder()
	srv.handleMatch(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for malformed WAV, got %d", rec.Code)
	}
}

func TestMatchTooLarge(t *testing.T) {
	st := buildTestStore(t, map[string]float64{"a.wav": 440})
	srv := newTestServer(t, st)
	srv.maxUploadBytes = 1024 // override for test

	big := bytes.Repeat([]byte{0}, 4096)
	req := httptest.NewRequest("POST", "/v1/match", bytes.NewReader(big))
	rec := httptest.NewRecorder()
	srv.handleMatch(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("expected 413, got %d", rec.Code)
	}
}

func TestMatchStoreNotLoaded(t *testing.T) {
	srv := newTestServer(t, nil)
	req := httptest.NewRequest("POST", "/v1/match", bytes.NewReader([]byte{}))
	rec := httptest.NewRecorder()
	srv.handleMatch(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 when store is nil, got %d", rec.Code)
	}
}

func TestStatsEndpoint(t *testing.T) {
	st := buildTestStore(t, map[string]float64{"a.wav": 440, "b.wav": 523.25})
	srv := newTestServer(t, st)

	rec := httptest.NewRecorder()
	srv.handleStats(rec, httptest.NewRequest("GET", "/v1/stats", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var resp statsResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Songs != 2 {
		t.Errorf("expected 2 songs, got %d", resp.Songs)
	}
	if resp.WinSize != testWinSize || resp.HopSize != testHopSize {
		t.Errorf("expected winSize=%d hopSize=%d, got %d/%d",
			testWinSize, testHopSize, resp.WinSize, resp.HopSize)
	}
	if resp.WindowFunc != testWindow {
		t.Errorf("expected window %q, got %q", testWindow, resp.WindowFunc)
	}
}

func TestMetricsEndpoint(t *testing.T) {
	st := buildTestStore(t, map[string]float64{"a.wav": 440})
	srv := newTestServer(t, st)

	// Hit /v1/match once so the counter increments
	src := genRich(440, 5.0)
	clipWAV := audio.EncodeWAV(src[2*testSampleRate:3*testSampleRate], testSampleRate, 1)
	matchReq := httptest.NewRequest("POST", "/v1/match", bytes.NewReader(clipWAV))
	srv.handleMatch(httptest.NewRecorder(), matchReq)

	// Scrape /metrics
	metricsReq := httptest.NewRequest("GET", "/metrics", nil)
	rec := httptest.NewRecorder()
	srv.handleMetrics(rec, metricsReq)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `presto_match_requests_total{status="ok"} 1`) {
		t.Errorf("expected match counter incremented:\n%s", body)
	}
	if !strings.Contains(body, "presto_store_songs 1") {
		t.Errorf("expected store songs gauge:\n%s", body)
	}
	if !strings.Contains(body, "go_goroutines") {
		t.Errorf("expected runtime metrics:\n%s", body)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("expected text/plain Content-Type, got %q", ct)
	}
}
