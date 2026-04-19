package main

import (
	"context"
	"flag"
	"io"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"boot.dev/linko/internal/store"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)

	httpPort := flag.Int("port", 8899, "port to listen on")
	dataDir := flag.String("data", "./data", "directory to store data")
	flag.Parse()

	status := run(ctx, cancel, *httpPort, *dataDir)
	cancel()
	os.Exit(status)
}

func run(ctx context.Context, cancel context.CancelFunc, httpPort int, dataDir string) int {
	logger, err := initializelogger()
	//--- create file where logs from Accesslog would be stored ---
	if err != nil {
		logger.Printf("failed to initialize logger: %v\n", err)
		return 1
	}

	st, err := store.New(dataDir, logger)
	if err != nil {
		logger.Printf("failed to create store: %v\n", err)
		return 1
	}

	s := newServer(*st, httpPort, cancel, logger)
	var serverErr error
	go func() {
		serverErr = s.start()
	}()

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	logger.Println("Linko is shutting down")
	if err := s.shutdown(shutdownCtx); err != nil {
		logger.Printf("failed to shutdown server: %v\n", err)
		return 1
	}
	if serverErr != nil {
		log.Printf("server error: %v\n", serverErr)
		return 1
	}
	return 0
}

func initializelogger() (*log.Logger, error) {
	w := io.Writer(os.Stderr)

	if path, ok := os.LookupEnv("LINKO_LOG_FILE"); ok && path != "" {
		log.Printf("logger variable provided!: %s", path)
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
		if err != nil {
			return nil, err
		}
		w = io.MultiWriter(os.Stderr, file)
	}
	return log.New(w, "", log.LstdFlags), nil
}
