// Package env resolves the few things tam takes from the process environment.
package env

import (
	"fmt"
	"os"
	"path/filepath"
)

// DataDir returns the directory that holds the databases and settings file.
// It comes from TAM_DATA_DIR and defaults to ./data. The directory is created
// when it does not exist yet.
func DataDir() (string, error) {
	dir := os.Getenv("TAM_DATA_DIR")
	if dir == "" {
		dir = "./data"
	}
	dir = filepath.Clean(dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create data dir %q: %w", dir, err)
	}
	return dir, nil
}

// OpenLog opens name inside the data directory for appending, so a run
// leaves a record of what happened after its console window is gone. The
// caller closes the file.
func OpenLog(dataDir, name string) (*os.File, error) {
	return os.OpenFile(filepath.Join(dataDir, name), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
}
