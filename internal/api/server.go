package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/time/rate"

	"pyrosync/internal/config"
	"pyrosync/internal/sched"
)

const apiVersion = "0.3.0"

// Server serves PyroSync REST endpoints and PostGIS vector tiles.
type Server struct {
	pool         *pgxpool.Pool
	cfg          config.Config
	poller       *sched.Poller
	logger       *slog.Logger
	adminLimiter *rate.Limiter
	tileSem      chan struct{}
	router       chi.Router
}

// New constructs an HTTP API server backed by PostgreSQL/PostGIS and an optional NRT poller.
func New(pool *pgxpool.Pool, cfg config.Config, poller *sched.Poller, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	s := &Server{
		pool:         pool,
		cfg:          cfg,
		poller:       poller,
		logger:       logger,
		adminLimiter: rate.NewLimiter(rate.Every(time.Minute), 1),
		tileSem:      make(chan struct{}, 8),
	}
	s.router = s.buildRouter()
	return s
}

// Router returns the configured HTTP handler for tests or embedding.
func (s *Server) Router() http.Handler {
	return s.router
}

// ListenAndServe starts the HTTP server on cfg.HTTPAddr and shuts down gracefully when ctx cancels.
func (s *Server) ListenAndServe(ctx context.Context) error {
	srv := &http.Server{
		Addr:              s.cfg.HTTPAddr,
		Handler:           s.router,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		s.logger.Info("http server listening", "addr", s.cfg.HTTPAddr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}

func (s *Server) buildRouter() chi.Router {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	r.Use(s.corsMiddleware)
	r.Use(middleware.Compress(5, "application/json", "application/vnd.mapbox-vector-tile"))
	r.Use(s.accessLogMiddleware)

	r.Route("/api/v1", func(r chi.Router) {
		r.Get("/health", s.handleHealth)
		r.Get("/ready", s.handleReady)
		r.Get("/meta", s.handleMeta)
		r.Get("/regions", s.handleRegions)
		r.Get("/summary", s.handleSummary)
		r.Get("/timeline", s.handleTimeline)
		r.Get("/hourly", s.handleHourly)
		r.Get("/calendar", s.handleCalendar)
		r.Get("/cell/{id}", s.handleCell)

		r.Get("/tiles/cells10/{z}/{x}/{y}.mvt", s.handleTilesCells10)
		r.Get("/tiles/cells/{z}/{x}/{y}.mvt", s.handleTilesCells)
		r.Get("/tiles/det/{z}/{x}/{y}.mvt", s.handleTilesDetections)

		r.Post("/admin/ingest", s.handleAdminIngest)
	})

	return r
}

func (s *Server) corsMiddleware(next http.Handler) http.Handler {
	allowAll := false
	allowed := make(map[string]struct{}, len(s.cfg.AllowedOrigins))
	for _, origin := range s.cfg.AllowedOrigins {
		if origin == "*" {
			allowAll = true
			continue
		}
		allowed[strings.TrimRight(origin, "/")] = struct{}{}
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := strings.TrimRight(r.Header.Get("Origin"), "/")
		if origin != "" {
			if allowAll {
				w.Header().Set("Access-Control-Allow-Origin", "*")
			} else if _, ok := allowed[origin]; ok {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Add("Vary", "Origin")
			}
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			w.Header().Set("Access-Control-Max-Age", "300")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) accessLogMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)
		status := ww.Status()
		if status == 0 {
			status = http.StatusOK
		}
		s.logger.Debug("http request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", status,
			"bytes", ww.BytesWritten(),
			"duration_ms", time.Since(start).Milliseconds(),
		)
	})
}
