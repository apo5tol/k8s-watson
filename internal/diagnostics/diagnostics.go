package diagnostics

import (
	"io"
	"log/slog"
	"os"
)

const filePermissions = 0o600

func New(path string) (*slog.Logger, func() error, error) {
	if path == "" {
		return slog.New(slog.NewTextHandler(io.Discard, nil)), func() error { return nil }, nil
	}

	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, filePermissions)
	if err != nil {
		return nil, nil, err
	}
	if err := file.Chmod(filePermissions); err != nil {
		closeErr := file.Close()
		if closeErr != nil {
			return nil, nil, closeErr
		}

		return nil, nil, err
	}

	return slog.New(slog.NewTextHandler(file, nil)), file.Close, nil
}
