package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"boot.dev/linko/internal/linkoerr"
	"boot.dev/linko/internal/store"

	pkgerr "github.com/pkg/errors"
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
	logFileAddress := os.Getenv("LINKO_LOG_FILE")
	logger, closeFunc, err := initializeLogger(logFileAddress)
	if err != nil {
		logger.Info(fmt.Sprintf("failed to setup logger: %v", err))
		return 1
	}

	defer func() {
		err = closeFunc()
		if err != nil {
			logger.Info(fmt.Sprintf("failed to setup logger: %v", err))
		}
	}()

	st, err := store.New(dataDir, logger)
	if err != nil {
		logger.Info(fmt.Sprintf("failed to create store: %v", err))
		return 1
	}
	s := newServer(*st, httpPort, logger, cancel)
	var serverErr error
	go func() {
		serverErr = s.start()
	}()

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	logger.Debug("Linko is shutting down")
	if err := s.shutdown(shutdownCtx); err != nil {
		logger.Info("failed to shutdown server", "error", err)
		return 1
	}
	if serverErr != nil {
		logger.Info(fmt.Sprintf("server error: %v", serverErr))
		return 1
	}
	return 0

}

func initializeLogger(logFileAddress string) (*slog.Logger, closeFunc, error) {
	debugHandler := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level:       slog.LevelDebug,
		ReplaceAttr: replaceAttr,
	})

	if logFileAddress != "" {
		logFile, err := os.OpenFile(logFileAddress, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o644)
		if err != nil {
			errObj := errors.New("problme writing to log file")
			return nil, func() error { return errObj }, errObj
		}
		bufferedFile := bufio.NewWriterSize(logFile, 8192)
		infoHandler := slog.NewJSONHandler(bufferedFile, &slog.HandlerOptions{
			Level:       slog.LevelInfo,
			ReplaceAttr: replaceAttr,
		})

		closeFunc := func() error {
			err := bufferedFile.Flush()
			if err != nil {
				return fmt.Errorf("failed to flush buffer. err: %v", err)
			}
			err = logFile.Close()
			if err != nil {
				return fmt.Errorf("failed to close file. err: %v", err)
			}
			return nil
		}
		logger := slog.New(slog.NewMultiHandler(debugHandler, infoHandler))

		return logger, closeFunc, nil
	}
	closeFunc := func() error { return nil }
	logger := slog.New(debugHandler)

	return logger, closeFunc, nil
}

type stackTracer interface {
	error
	StackTrace() pkgerr.StackTrace
}

func replaceAttr(groups []string, a slog.Attr) slog.Attr {
	if a.Key == "error" {
		err, ok := a.Value.Any().(error)
		if !ok {
			return a
		}

		me, ok := errors.AsType[multiError](err)
		if !ok {
			return slog.GroupAttrs("error", errorAttrs(err)...)
		}

		multiErrors := me.Unwrap()
		var errAttrs []slog.Attr
		//put each error from multiErrors into a group with the number of their indexes
		//as their key sa
		for i, e := range multiErrors {
			errAttrs = append(errAttrs, slog.GroupAttrs(
				fmt.Sprintf("error_%d", i+1),
				errorAttrs(e)...,
			))
		}
		return slog.GroupAttrs("errors", errAttrs...)
	}
	return a
}

func errorAttrs(err error) []slog.Attr {
	attrs := []slog.Attr{
	{
			Key:   "message",
			Value: slog.StringValue(err.Error()),
		},
	}
	//pass the error value to the attr method and take the list of
	//slog.Attr objects that it returns and append them to the attrs list

	attrs = append(attrs, linkoerr.Attrs(err)...)

	//check if error is a stacktrace and send append it
	//as an slog attribute to attrs

	//--use error to unpack
	if stackErr, ok := errors.AsType[stackTracer](err); ok {
		attrs = append(attrs, slog.Attr{
			Key:   "stack_trace",
			Value: slog.StringValue(fmt.Sprintf("%+v", stackErr.StackTrace())),
		})
	}
	return attrs

}

type closeFunc func() error

type multiError interface {
	error
	Unwrap() []error
}
