package logging

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenInWritesAndAppends(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "logs")
	logger, err := OpenIn(directory, "experiment", &slog.HandlerOptions{Level: slog.LevelDebug})
	if err != nil {
		t.Fatal(err)
	}
	logger.Info("started", "seed", 7)
	path := logger.Path()
	if err := logger.Close(); err != nil {
		t.Fatal(err)
	}
	if err := logger.Close(); err != nil {
		t.Fatal(err)
	}

	logger, err = OpenIn(directory, "experiment", nil)
	if err != nil {
		t.Fatal(err)
	}
	logger.Warn("finished")
	if err := logger.Close(); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "msg=started") || !strings.Contains(text, "seed=7") || !strings.Contains(text, "msg=finished") {
		t.Fatalf("log contents = %q", text)
	}
}

func TestOpenUsesEnvironment(t *testing.T) {
	directory := t.TempDir()
	t.Setenv(EnvLogDir, directory)
	logger, err := Open("run.log", nil)
	if err != nil {
		t.Fatal(err)
	}
	if logger.Path() != filepath.Join(directory, "run.log") {
		t.Fatalf("path = %q", logger.Path())
	}
	if err := logger.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestRejectsPathTraversal(t *testing.T) {
	for _, name := range []string{"", ".", "..", "../escape", "nested/run"} {
		if _, err := OpenIn(t.TempDir(), name, nil); err == nil {
			t.Fatalf("OpenIn(%q) succeeded", name)
		}
	}
}
