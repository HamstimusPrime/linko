package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"

	"log/slog"
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
	logger, closeLogger, err := initializelogger()
	//--- create file where logs from Accesslog would be stored ---
	if err != nil {
		fmt.Printf("failed to initialize logger: %v\n", err)
		return 1
	}
	// our logger is using a buffer to write to file only when buffer is full
	// closeLogger closes and flushes the write buffer
	//we defer closing it at the end of the program execution
	defer func() {
		if err := closeLogger(); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to close logger: %v\n", err)
		}
	}()

	st, err := store.New(dataDir, logger)
	if err != nil {
		logger.Info("failed to create store: %v\n", err)
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

	logger.Debug("Linko is shutting down")
	if err := s.shutdown(shutdownCtx); err != nil {
		logger.Error("failed to shutdown server: %v\n", err)
		return 1
	}
	if serverErr != nil {
		logger.Error("server error: %v\n", serverErr)
		return 1
	}
	return 0
}

func initializelogger() (*slog.Logger, closeFunc, error) {

	debugLogHandler := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelDebug})
	//error logger writes logs to a file
	if logFilePath, ok := os.LookupEnv("LINKO_LOG_FILE"); ok && logFilePath != "" {
		file, err := os.OpenFile(logFilePath, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
		if err != nil {
			return nil, nil, err
		}
		bufferedFile := bufio.NewWriterSize(file, 8192)
		close := func() error {
			if err := bufferedFile.Flush(); err != nil {
				return fmt.Errorf("failed to flush log file: %w", err)
			}
			if err := file.Close(); err != nil {
				return fmt.Errorf("failed to close log file: %w", err)
			}
			return nil
		}
		errorLogHandler := slog.NewTextHandler(bufferedFile, &slog.HandlerOptions{Level: slog.LevelInfo})

		logger := slog.New(slog.NewMultiHandler(
			debugLogHandler,
			errorLogHandler,
		))
		return logger, close, nil
	}

	logger := slog.New(slog.NewMultiHandler(
		debugLogHandler,
	))

	close := func() error { return nil }
	return logger, close, nil
}

type closeFunc func() error
