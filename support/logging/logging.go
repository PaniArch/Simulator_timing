// Package logging creates project-local structured experiment logs.
package logging

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// EnvLogDir names the environment variable set by env/env.sh.
const EnvLogDir = "SIMULATOR_LOG_DIR"

// FileLogger owns a slog logger and its output file.
type FileLogger struct {
	*slog.Logger
	path      string
	file      *os.File
	closeOnce sync.Once
	closeErr  error
}

// Open creates or appends to name under the configured project log directory.
func Open(name string, options *slog.HandlerOptions) (*FileLogger, error) {
	directory := os.Getenv(EnvLogDir)
	if directory == "" {
		return nil, fmt.Errorf("logging: %s is not set; source env/env.sh", EnvLogDir)
	}
	return OpenIn(directory, name, options)
}

// OpenIn creates or appends to name under directory.
func OpenIn(directory, name string, options *slog.HandlerOptions) (*FileLogger, error) {
	if directory == "" {
		return nil, fmt.Errorf("logging: empty directory")
	}
	if name == "" || name == "." || name == ".." || filepath.Base(name) != name || strings.ContainsAny(name, `/\\`) {
		return nil, fmt.Errorf("logging: invalid log name %q", name)
	}
	if filepath.Ext(name) == "" {
		name += ".log"
	}
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return nil, fmt.Errorf("logging: create directory: %w", err)
	}
	path := filepath.Join(directory, name)
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("logging: open %q: %w", path, err)
	}
	logger := slog.New(slog.NewTextHandler(file, options))
	return &FileLogger{Logger: logger, path: path, file: file}, nil
}

// Path returns the log file path.
func (l *FileLogger) Path() string {
	return l.path
}

// Close flushes and closes the log file. It is safe to call more than once.
func (l *FileLogger) Close() error {
	l.closeOnce.Do(func() {
		l.closeErr = l.file.Close()
	})
	return l.closeErr
}
