package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/nddq/presto/internal/audio"
	"github.com/nddq/presto/internal/fingerprint"
	"github.com/nddq/presto/internal/metrics"
	"github.com/nddq/presto/internal/store"
)

const (
	defaultListenAddr     = ":8080"
	defaultStorePath      = "/var/lib/presto/library.prfp"
	defaultMaxUploadBytes = int64(10 << 20) // 10 MiB
	shutdownTimeout       = 10 * time.Second
)

// server is the HTTP server state shared across handlers.
type server struct {
	store          atomic.Pointer[store.Store]
	loadedAt       atomic.Int64 // unix seconds
	loadMu         sync.Mutex   // serialises loadStore invocations
	maxUploadBytes int64
	metrics        *metrics.Registry
	matchRequests  *metrics.Counter
	matchDuration  *metrics.Histogram
	storeSongs     *metrics.Gauge
	storeLoadedTS  *metrics.Gauge
	log            *slog.Logger
}

func newServer(maxUploadBytes int64, log *slog.Logger) *server {
	reg := metrics.NewRegistry()
	s := &server{
		maxUploadBytes: maxUploadBytes,
		metrics:        reg,
		log:            log,
	}
	s.matchRequests = reg.Register(metrics.NewCounter(
		"presto_match_requests_total",
		"Total number of /v1/match requests by outcome.",
		"status", "ok", "error",
	)).(*metrics.Counter)
	s.matchDuration = reg.Register(metrics.NewHistogram(
		"presto_match_duration_seconds",
		"Duration of /v1/match handling.",
		[]float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2, 5},
	)).(*metrics.Histogram)
	s.storeSongs = reg.Register(metrics.NewGauge(
		"presto_store_songs",
		"Number of songs currently loaded in the fingerprint store.",
	)).(*metrics.Gauge)
	s.storeLoadedTS = reg.Register(metrics.NewGauge(
		"presto_store_loaded_timestamp_seconds",
		"Unix timestamp when the fingerprint store was loaded.",
	)).(*metrics.Gauge)
	return s
}

// loadStore reads the store from disk and installs it atomically.
//
// Concurrent invocations are serialised by loadMu so two callers cannot
// race on the previous-store Close and cause a double-munmap.
//
// When a previous store was already loaded its Close is currently a
// no-op in practice because loadStore is only called once at startup
// from runServe. If this function is ever wired into a live reload
// endpoint, the deferred Close must be gated on "no in-flight
// /v1/match is still holding a hash slice aliased into the old
// mapping" — a reference count or a drain timeout. The current code
// closes the previous store immediately on the assumption that no
// concurrent match is in flight, which is true at startup.
func (s *server) loadStore(path string) error {
	s.loadMu.Lock()
	defer s.loadMu.Unlock()

	st, err := store.Load(path)
	if err != nil {
		return err
	}
	prev := s.store.Swap(st)
	now := time.Now().Unix()
	s.loadedAt.Store(now)
	s.storeSongs.Set(float64(len(st.Songs)))
	s.storeLoadedTS.Set(float64(now))
	s.log.Info("store loaded", "path", path, "songs", len(st.Songs),
		"winSize", st.WinSize, "hopSize", st.HopSize, "windowFunc", st.WindowFunc)
	if prev != nil {
		if err := prev.Close(); err != nil {
			s.log.Warn("closing previous store", "err", err)
		}
	}
	return nil
}

// routes wires handlers onto a ServeMux.
func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/match", s.handleMatch)
	mux.HandleFunc("GET /v1/stats", s.handleStats)
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET /readyz", s.handleReady)
	mux.HandleFunc("GET /metrics", s.handleMetrics)
	return s.recoverMiddleware(mux)
}

// recoverMiddleware catches any panic that escapes a handler, logs it
// with the request method and path, bumps the match-error counter when
// the panic came out of /v1/match, and returns a 500 to the client.
// Every handler is wrapped so a bug in one path can never take down
// in-flight requests on other paths.
func (s *server) recoverMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				s.log.Error("panic in handler",
					"method", r.Method,
					"path", r.URL.Path,
					"panic", fmt.Sprintf("%v", rec),
				)
				if r.URL.Path == "/v1/match" {
					s.matchRequests.Inc("error")
				}
				// If anything has already been written, the headers
				// are flushed and WriteHeader is a no-op. The client
				// will see a truncated body, which is the best we
				// can do without buffering.
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"error":"internal server error"}`))
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// --- handlers ---

type matchResponse struct {
	Matches []matchEntry `json:"matches"`
	// Margin is the ratio top1.score / top2.score. A value ≫ 1 signals
	// a confident, unambiguous match; a value near 1 means the library
	// contained multiple songs scoring almost equally. Zero when fewer
	// than two candidates are returned.
	Margin    float64 `json:"margin"`
	ElapsedMs int64   `json:"elapsed_ms"`
}

type matchEntry struct {
	Name   string  `json:"name"`
	Score  float64 `json:"score"`
	Offset int     `json:"offset"`
}

func (s *server) handleMatch(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	st := s.store.Load()
	if st == nil {
		s.matchRequests.Inc("error")
		writeJSONError(w, http.StatusServiceUnavailable, "store not loaded")
		return
	}

	// Enforce max upload size before reading.
	r.Body = http.MaxBytesReader(w, r.Body, s.maxUploadBytes)
	data, err := io.ReadAll(r.Body)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			s.matchRequests.Inc("error")
			writeJSONError(w, http.StatusRequestEntityTooLarge, "request body too large")
			return
		}
		s.matchRequests.Inc("error")
		writeJSONError(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}

	sig, err := audio.DecodeWAV(data)
	if err != nil {
		s.matchRequests.Inc("error")
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Use the same algorithm that was used to index the library.
	algo, err := fingerprint.Get(st.AlgoName)
	if err != nil {
		s.matchRequests.Inc("error")
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	sampleFP, err := algo.FingerprintSignal(sig, st.WinSize, st.HopSize, st.WindowFunc, 0)
	if err != nil {
		s.matchRequests.Inc("error")
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	results := st.Match(sampleFP, 5)

	entries := make([]matchEntry, 0, len(results))
	for _, r := range results {
		entries = append(entries, matchEntry{Name: r.Name, Score: r.Score, Offset: r.Offset})
	}
	var margin float64
	if len(entries) >= 2 && entries[1].Score > 0 {
		margin = entries[0].Score / entries[1].Score
	}

	elapsed := time.Since(start)
	s.matchDuration.Observe(elapsed.Seconds())
	s.matchRequests.Inc("ok")

	writeJSON(w, http.StatusOK, matchResponse{
		Matches:   entries,
		Margin:    margin,
		ElapsedMs: elapsed.Milliseconds(),
	})
}

type statsResponse struct {
	Songs      int    `json:"songs"`
	WinSize    int    `json:"win_size"`
	HopSize    int    `json:"hop_size"`
	WindowFunc string `json:"window_func"`
	LoadedAt   string `json:"loaded_at"`
}

func (s *server) handleStats(w http.ResponseWriter, r *http.Request) {
	st := s.store.Load()
	if st == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "store not loaded")
		return
	}
	writeJSON(w, http.StatusOK, statsResponse{
		Songs:      len(st.Songs),
		WinSize:    st.WinSize,
		HopSize:    st.HopSize,
		WindowFunc: st.WindowFunc,
		LoadedAt:   time.Unix(s.loadedAt.Load(), 0).UTC().Format(time.RFC3339),
	})
}

func (s *server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	io.WriteString(w, "ok\n")
}

func (s *server) handleReady(w http.ResponseWriter, r *http.Request) {
	if s.store.Load() == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		io.WriteString(w, "store not loaded\n")
		return
	}
	w.WriteHeader(http.StatusOK)
	io.WriteString(w, "ok\n")
}

func (s *server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	s.metrics.WriteTo(w)
}

// --- helpers ---

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// --- subcommand entry point ---

func runServe(args []string) {
	log := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	addr := envOr("PRESTO_LISTEN_ADDR", defaultListenAddr)
	storePath := envOr("PRESTO_STORE_PATH", defaultStorePath)
	maxUpload := defaultMaxUploadBytes
	if s := os.Getenv("PRESTO_MAX_UPLOAD_BYTES"); s != "" {
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil || n <= 0 {
			log.Error("invalid PRESTO_MAX_UPLOAD_BYTES", "value", s)
			os.Exit(1)
		}
		maxUpload = n
	}

	srv := newServer(maxUpload, log)

	httpSrv := &http.Server{
		Addr:              addr,
		Handler:           srv.routes(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Start the HTTP server first so /healthz and /readyz are available
	// immediately. The store loads in a background goroutine; /readyz
	// returns 503 until it's done so Kubernetes holds traffic during
	// the load instead of flipping the pod into CrashLoopBackoff.
	errCh := make(chan error, 1)
	go func() {
		log.Info("server listening", "addr", addr)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	// Load the store in the background.
	loadCh := make(chan error, 1)
	go func() {
		loadCh <- srv.loadStore(storePath)
	}()
	go func() {
		if err := <-loadCh; err != nil {
			log.Error("failed to load store", "path", storePath, "err", err)
			// Stay up so /readyz can keep reporting 503; the orchestrator
			// will see an unready pod rather than a crashloop. The
			// operator can fix the underlying store file without
			// rotating pods.
		}
	}()

	select {
	case <-ctx.Done():
		log.Info("shutdown signal received, draining")
	case err, ok := <-errCh:
		if ok && err != nil {
			log.Error("server error", "err", err)
			os.Exit(1)
		}
		return
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		log.Error("graceful shutdown failed", "err", err)
		os.Exit(1)
	}
	log.Info("server stopped")
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

