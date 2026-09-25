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
