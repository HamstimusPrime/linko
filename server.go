package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/pprof"
	"os"
	"slices"
	"strings"
	"time"

	"boot.dev/linko/internal/store"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

type server struct {
	httpServer *http.Server
	store      store.Store
	cancel     context.CancelFunc
	logger     *slog.Logger
}

type spyRequestReadCloser struct {
	io.ReadCloser
	bytesRead int
}

func (s *spyRequestReadCloser) Read(p []byte) (int, error) {
	numReadBytes, err := s.ReadCloser.Read(p)
	s.bytesRead += numReadBytes
	return s.bytesRead, err
}

func (s *server) start() error {
	ln, err := net.Listen("tcp", s.httpServer.Addr)
	if err != nil {
		return err
	}
	if err := s.httpServer.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {

		return errors.New("unable to assert listener")
	}
	port := addr.Port
	s.logger.Debug(fmt.Sprintf("Linko is running on http://localhost:%d\n", port))
	return nil
}

func (s *server) shutdown(ctx context.Context) error {
	s.logger.Debug("Linko is shutting down")
	return s.httpServer.Shutdown(ctx)
}

func (s *server) handlerShutdown(w http.ResponseWriter, r *http.Request) {
	if os.Getenv("ENV") == "production" {
		http.NotFound(w, r)
		return
	}
	w.WriteHeader(http.StatusOK)
	go s.cancel()
}

type spyResponseWriter struct {
	http.ResponseWriter
	bytesWritten   int
	httpStatusCode int
}

func (s *spyResponseWriter) Write(p []byte) (int, error) {
	//by convention, the write method calls the Writer method passing it an http.StatusOk value
	//if the status code of the wrapper spyResponseWriter struct has not been modified(i.e its values is the zero value of an integer 0)
	//set its value to the http.StatusOK value
	if s.httpStatusCode == 0 {
		s.httpStatusCode = http.StatusOK
	}
	b, err := s.ResponseWriter.Write(p)
	s.bytesWritten += b
	return b, err
}

func (s *spyResponseWriter) WriteHeader(statusCode int) {
	s.httpStatusCode = statusCode
	s.ResponseWriter.WriteHeader(statusCode)
}

const logContextKey contextKey = "log_context"

type LogContext struct {
	Username string
	Error    error
}

func httpError(ctx context.Context, w http.ResponseWriter, statusCode int, err error) {
	//assert that context is of type LogContext
	if logCtx, ok := ctx.Value(logContextKey).(*LogContext); ok {
		logCtx.Error = err
	}
	genericCodes := []int{401, 403, 500}
	if slices.Contains(genericCodes, statusCode) {
		errMsg := http.StatusText(statusCode)
		http.Error(w, errMsg, statusCode)
		return
	}
	http.Error(w, err.Error(), statusCode)

}

func redactIP(addr string) string {

	if hostname, _, err := net.SplitHostPort(addr); err == nil {
		addr = hostname
	}

	addr = strings.Trim(addr, "[]")
	IP := net.ParseIP(addr)
	if IP == nil {
		return ""
	}

	validIPV4 := net.IP.To4(IP)
	if validIPV4 != nil {
		parts := strings.Split(addr, ".")
		parts[len(parts)-1] = "x"
		return strings.Join(parts, ".")

	} else {
		parts := strings.Split(addr, ":")
		parts[len(parts)-1] = "x"
		return "[" + strings.Join(parts, ":") + "]"
	}

}

func requestLogger(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			startTime := time.Now()
			spyRequest := &spyRequestReadCloser{ReadCloser: r.Body}
			r.Body = spyRequest
			logCtx := &LogContext{}
			ctx := context.WithValue(r.Context(), logContextKey, logCtx)
			r = r.WithContext(ctx)
			spyWriter := &spyResponseWriter{ResponseWriter: w}
			w = spyWriter
			next.ServeHTTP(w, r)

			attr := []any{
				slog.String("method", r.Method),
				slog.String("path", r.URL.EscapedPath()),
				slog.String("client_ip", redactIP(r.RemoteAddr)),
				slog.Duration("duration", time.Since(startTime)),
				slog.Int("request_body_bytes", spyRequest.bytesRead),
				slog.Int("response_body_bytes", spyWriter.bytesWritten),
				slog.Int("response_status", spyWriter.httpStatusCode),
				slog.String("request_id", r.Header.Get("X-Request-ID")),
			}

			if logCtx.Username != "" {
				attr = append(attr, slog.String("user", logCtx.Username))
			}

			if logCtx.Error != nil {
				attr = append(attr, slog.Any("error", logCtx.Error))

			}
			logger.Info("Served request", attr...)

		})
	}
}

func newServer(store store.Store, port int, logger *slog.Logger, cancel context.CancelFunc) *server {
	mux := http.NewServeMux()
	h := otelhttp.NewHandler

	srv := &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: h(metricsMiddleware(requestID(requestLogger(logger)(mux))), "http.server"),
	}

	s := &server{
		httpServer: srv,
		store:      store,
		cancel:     cancel,
		logger:     logger,
	}

	mux.Handle("GET /debug/pprof/", s.authMiddleware(http.HandlerFunc(pprof.Index)))
	mux.Handle("GET /debug/pprof/profile", s.authMiddleware(http.HandlerFunc(pprof.Profile)))
	mux.HandleFunc("GET /", s.handlerIndex)
	mux.Handle("POST /api/login", s.authMiddleware(http.HandlerFunc(s.handlerLogin)))
	mux.Handle("POST /api/shorten", s.authMiddleware(http.HandlerFunc(s.handlerShortenLink)))
	mux.Handle("GET /api/stats", s.authMiddleware(http.HandlerFunc(s.handlerStats)))
	mux.Handle("GET /api/urls", s.authMiddleware(http.HandlerFunc(s.handlerListURLs)))
	mux.HandleFunc("GET /{shortCode}", s.handlerRedirect)
	mux.HandleFunc("POST /admin/shutdown", s.handlerShutdown)

	return s
}
