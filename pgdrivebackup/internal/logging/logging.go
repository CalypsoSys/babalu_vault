package logging

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
)

type nopCloser struct{}

func (nopCloser) Close() error { return nil }

func New(logPath string) (*slog.Logger, io.Closer, error) {
	if logPath == "" {
		return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})), nopCloser{}, nil
	}
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return nil, nil, err
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, nil, err
	}
	writer := io.MultiWriter(os.Stdout, logFile)
	return slog.New(slog.NewTextHandler(writer, &slog.HandlerOptions{Level: slog.LevelInfo})), logFile, nil
}
