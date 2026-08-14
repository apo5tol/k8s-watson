package diagnostics

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewWithoutPath(t *testing.T) {
	logger, closeLogger, err := New("")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	logger.Info("discarded message")
	if err := closeLogger(); err != nil {
		t.Errorf("close logger error = %v", err)
	}
}

func TestNewCreatesPrivateLogFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "diagnostics.log")
	logger, closeLogger, err := New(path)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	logger.Info("application started", "event", "application_started")
	if err := closeLogger(); err != nil {
		t.Fatalf("close logger error = %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if got := info.Mode().Perm(); got != filePermissions {
		t.Errorf("file permissions = %#o, want %#o", got, filePermissions)
	}

	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if !strings.Contains(string(contents), "application_started") {
		t.Errorf("log contents = %q, want event", contents)
	}
}

func TestNewReturnsCreationError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing", "diagnostics.log")
	if _, _, err := New(path); err == nil {
		t.Fatal("New() error = nil, want creation error")
	}
}
